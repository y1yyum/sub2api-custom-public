package edge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const strictBody = `{"model":"fixture-model","input":"hello","max_output_tokens":16}`

func TestEdgeStrictRequestAllowlist(t *testing.T) {
	valid := []string{
		strictBody,
		`{"model":"fixture-model","input":"","max_output_tokens":4096,"stream":false,"instructions":"Reply in plain text"}`,
		`{"model":"fixture-model","input":[{"role":"system","content":"Rules"},{"role":"user","content":[{"type":"input_text","text":"Hello"}]}],"max_output_tokens":1,"stream":true}`,
	}
	for _, body := range valid {
		if model, err := validateStrictResponsesBody([]byte(body)); err != nil || model != "fixture-model" {
			t.Fatalf("拒绝安全文本请求: model=%q err=%v", model, err)
		}
	}
	invalid := map[string]string{
		"missing_limit":      textBody,
		"zero_limit":         strings.Replace(strictBody, ":16", ":0", 1),
		"negative_limit":     strings.Replace(strictBody, ":16", ":-1", 1),
		"large_limit":        strings.Replace(strictBody, ":16", ":4097", 1),
		"fraction_limit":     strings.Replace(strictBody, ":16", ":1.5", 1),
		"string_limit":       strings.Replace(strictBody, ":16", `:"16"`, 1),
		"null_limit":         strings.Replace(strictBody, ":16", ":null", 1),
		"null_input":         strings.Replace(strictBody, `"hello"`, "null", 1),
		"number_input":       strings.Replace(strictBody, `"hello"`, "42", 1),
		"empty_messages":     strings.Replace(strictBody, `"hello"`, "[]", 1),
		"model_alias":        strings.Replace(strictBody, `"model"`, `"Model"`, 1),
		"duplicate_model":    strings.Replace(strictBody, `"input"`, `"model":"other","input"`, 1),
		"escaped_duplicate":  strings.Replace(strictBody, `"input"`, `"mo\u0064el":"other","input"`, 1),
		"non_ascii_model":    strings.Replace(strictBody, "fixture-model", "模型", 1),
		"control_model":      strings.Replace(strictBody, "fixture-model", `fixture\u0000`, 1),
		"space_model":        strings.Replace(strictBody, "fixture-model", "fixture model", 1),
		"trailing":           strictBody + `{}`,
		"non_object":         `[]`,
		"invalid_utf8":       strings.Replace(strictBody, "hello", string([]byte{0xff}), 1),
		"instructions_bound": strings.TrimSuffix(strictBody, "}") + `,"instructions":"` + strings.Repeat("x", strictMaxInstructions+1) + `"}`,
		"text_bound":         strings.Replace(strictBody, "hello", strings.Repeat("x", strictMaxTextBytes+1), 1),
	}
	for _, field := range []string{
		`"store":false`, `"background":false`, `"tools":[]`, `"tool_choice":"none"`,
		`"previous_response_id":null`, `"conversation":null`, `"session":{}`, `"metadata":{}`, `"include":[]`,
		`"account_id":7`, `"stream":null`, `"stream":"true"`, `"instructions":null`, `"unknown":true`,
	} {
		invalid[field] = strings.TrimSuffix(strictBody, "}") + "," + field + "}"
	}
	for name, input := range map[string]string{
		"role_alias":     `[{"Role":"user","content":"hello"}]`,
		"role_duplicate": `[{"role":"user","role":"assistant","content":"hello"}]`,
		"tool_role":      `[{"role":"tool","content":"hello"}]`,
		"message_id":     `[{"role":"user","content":"hello","id":"remote"}]`,
		"message_type":   `[{"type":"message","role":"user","content":"hello"}]`,
		"image":          `[{"role":"user","content":[{"type":"input_image","text":"hello"}]}]`,
		"file":           `[{"role":"user","content":[{"type":"input_file","file_id":"remote"}]}]`,
		"text_alias":     `[{"role":"user","content":[{"type":"input_text","Text":"hello"}]}]`,
		"text_duplicate": `[{"role":"user","content":[{"type":"input_text","text":"a","text":"b"}]}]`,
		"text_null":      `[{"role":"user","content":[{"type":"input_text","text":null}]}]`,
		"extra_content":  `[{"role":"user","content":[{"type":"input_text","text":"hello","file_id":"remote"}]}]`,
		"message_bound":  `[` + strings.TrimSuffix(strings.Repeat(`{"role":"user","content":"a"},`, strictMaxMessages+1), ",") + `]`,
		"content_bound":  `[{"role":"user","content":[` + strings.TrimSuffix(strings.Repeat(`{"type":"input_text","text":"a"},`, strictMaxContentItems+1), ",") + `]}]`,
	} {
		invalid[name] = strings.Replace(strictBody, `"hello"`, input, 1)
	}
	var calls atomic.Int64
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer core.Close()
	handler, server := newTestEdge(t, strictConfig(t, core.URL))
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := validateStrictResponsesBody([]byte(body)); err == nil {
				t.Fatal("接受了 strict 能力之外的请求")
			}
			if len(body) <= 1<<20 {
				response := edgePost(t, server.URL, body, nil)
				result := readBody(t, response)
				if response.StatusCode != 400 || !strings.Contains(result, "unsupported_responses_request") {
					t.Fatalf("未在请求边界拒绝: %d %s", response.StatusCode, result)
				}
			}
		})
	}
	if calls.Load() != 0 || handler.StrictStats().Acquisitions != 0 {
		t.Fatal("非法请求进入选号或发送到 Core")
	}
	// 1 MiB 是文本总量的边界，也适用于消息数组的合计。
	input, err := json.Marshal([]map[string]string{{"role": "user", "content": strings.Repeat("x", 600000)}, {"role": "assistant", "content": strings.Repeat("y", 600000)}})
	if err != nil {
		t.Fatal(err)
	}
	if strictTextInput(input) {
		t.Fatal("消息文本合计越界")
	}
	for _, input := range []string{
		`"` + strings.Repeat("x", strictMaxTextBytes) + `"`,
		`[{"role":"user","content":"` + strings.Repeat("x", strictMaxTextBytes) + `"}]`,
		`[{"role":"user","content":[{"type":"input_text","text":"` + strings.Repeat("x", strictMaxTextBytes) + `"}]}]`,
	} {
		if !strictTextInput([]byte(input)) {
			t.Fatal("精确文本上限被误拒绝")
		}
	}
}
