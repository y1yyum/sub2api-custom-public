package service

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ResponseDiagnostics contains upstream declarations, never independently measured tokens.
// It is persisted with usage but exposed only through the administrator DTO.
type ResponseDiagnostics struct {
	Protocol            string              `json:"protocol"`
	InitialUsage        *ResponseTokenUsage `json:"initial_usage,omitempty"`
	FinalUsage          *ResponseTokenUsage `json:"final_usage,omitempty"`
	DisplayScaled       *bool               `json:"display_scaled,omitempty"`
	InputChangeRate     *float64            `json:"input_change_rate,omitempty"`
	StopReason          string              `json:"stop_reason,omitempty"`
	StopSequence        string              `json:"stop_sequence,omitempty"`
	StreamComplete      bool                `json:"stream_complete"`
	ErrorType           string              `json:"error_type,omitempty"`
	MessageID           string              `json:"message_id,omitempty"`
	ServiceTier         string              `json:"service_tier,omitempty"`
	InferenceGeo        string              `json:"inference_geo,omitempty"`
	ThinkingTokens      *int64              `json:"thinking_tokens,omitempty"`
	ToolCalls           []ResponseToolCall  `json:"tool_calls,omitempty"`
	Iterations          []ResponseIteration `json:"iterations,omitempty"`
	IterationCount      *int                `json:"iteration_count,omitempty"`
	IterationsTruncated bool                `json:"iterations_truncated,omitempty"`
	EventCount          int                 `json:"event_count"`
	ContentBlockCounts  map[string]int      `json:"content_block_counts,omitempty"`
}

type ResponseTokenUsage struct {
	InputTokens           *int64 `json:"input_tokens,omitempty"`
	OutputTokens          *int64 `json:"output_tokens,omitempty"`
	CacheReadTokens       *int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens   *int64 `json:"cache_creation_tokens,omitempty"`
	CacheCreation5mTokens *int64 `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1hTokens *int64 `json:"cache_creation_1h_tokens,omitempty"`
	TotalInputTokens      *int64 `json:"total_input_tokens,omitempty"`
	DisplayScaled         *bool  `json:"display_scaled,omitempty"`
}

type ResponseToolCall struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ResponseIteration struct {
	Type  string              `json:"type"`
	Model string              `json:"model,omitempty"`
	Usage *ResponseTokenUsage `json:"usage,omitempty"`
}

const responseDiagnosticsMaxItems = 32

type responseDiagnosticsObserver struct {
	data       ResponseDiagnostics
	latest     *ResponseTokenUsage
	terminal   bool
	openBlocks int
}

func diagnosticToken(value gjson.Result) *int64 {
	if value.Type != gjson.Number || value.Int() < 0 || value.Int() > 1<<53-1 {
		return nil
	}
	n := value.Int()
	return &n
}

func diagnosticText(value gjson.Result) string {
	if value.Type != gjson.String {
		return ""
	}
	text := strings.TrimSpace(value.String())
	if len(text) > 200 {
		return string([]rune(text)[:min(len([]rune(text)), 200)])
	}
	return text
}

func anthropicDiagnosticUsage(node gjson.Result, previous *ResponseTokenUsage) *ResponseTokenUsage {
	if !node.IsObject() {
		return nil
	}
	u := &ResponseTokenUsage{}
	if previous != nil {
		*u = *previous
	}
	fields := []struct {
		path string
		dest **int64
	}{
		{"input_tokens", &u.InputTokens}, {"output_tokens", &u.OutputTokens},
		{"cache_read_input_tokens", &u.CacheReadTokens}, {"cache_creation_input_tokens", &u.CacheCreationTokens},
		{"cache_creation.ephemeral_5m_input_tokens", &u.CacheCreation5mTokens},
		{"cache_creation.ephemeral_1h_input_tokens", &u.CacheCreation1hTokens},
	}
	for _, field := range fields {
		if n := diagnosticToken(node.Get(field.path)); n != nil {
			*field.dest = n
		}
	}
	if u.InputTokens != nil && u.CacheReadTokens != nil && u.CacheCreationTokens != nil {
		total := *u.InputTokens + *u.CacheReadTokens + *u.CacheCreationTokens
		u.TotalInputTokens = &total
	}
	return u
}

func (o *responseDiagnosticsObserver) observeAnthropic(payload []byte) {
	kind := gjson.GetBytes(payload, "type").String()
	if kind == "message" || kind == "" {
		return
	}
	if o.data.Protocol == "" && kind != "message_start" {
		return
	}
	o.data.Protocol = "anthropic"
	o.data.EventCount++
	// Content deltas contain potentially large text, signatures or tool inputs.
	// Only lifecycle and usage events need further inspection.
	if kind == "content_block_delta" || kind == "ping" {
		return
	}
	if !gjson.ValidBytes(payload) {
		o.data.ErrorType = "invalid_sse_event"
		return
	}
	node := gjson.ParseBytes(payload)
	var scaled *bool
	if marker := node.Get("_sub2api_display_scaled"); marker.Type == gjson.True || marker.Type == gjson.False {
		value := marker.Bool()
		scaled = &value
		if o.data.DisplayScaled == nil || value {
			o.data.DisplayScaled = &value
		}
	}
	switch kind {
	case "message_start":
		o.data.MessageID = diagnosticText(node.Get("message.id"))
		o.latest = anthropicDiagnosticUsage(node.Get("message.usage"), nil)
		if o.latest != nil {
			o.latest.DisplayScaled = scaled
			initial := *o.latest
			o.data.InitialUsage = &initial
		}
		o.observeUsageDetails(node.Get("message.usage"))
	case "message_delta":
		if usage := anthropicDiagnosticUsage(node.Get("usage"), o.latest); usage != nil {
			usage.DisplayScaled = scaled
			o.latest = usage
			o.data.FinalUsage = usage
		}
		if reason := diagnosticText(node.Get("delta.stop_reason")); reason != "" {
			o.data.StopReason = reason
		}
		o.data.StopSequence = diagnosticText(node.Get("delta.stop_sequence"))
		o.observeUsageDetails(node.Get("usage"))
	case "content_block_start":
		o.openBlocks++
		block := node.Get("content_block")
		blockType := diagnosticText(block.Get("type"))
		switch blockType {
		case "text", "thinking", "redacted_thinking", "tool_use", "server_tool_use", "fallback":
			if o.data.ContentBlockCounts == nil {
				o.data.ContentBlockCounts = make(map[string]int)
			}
			o.data.ContentBlockCounts[blockType]++
		}
		if blockType == "tool_use" || blockType == "server_tool_use" {
			o.addTool(blockType, diagnosticText(block.Get("name")))
		}
	case "content_block_stop":
		o.openBlocks--
	case "message_stop":
		o.terminal = true
	case "error":
		o.data.ErrorType = diagnosticText(node.Get("error.type"))
		if o.data.ErrorType == "" {
			o.data.ErrorType = "stream_error"
		}
	}
}

func (o *responseDiagnosticsObserver) observeUsageDetails(usage gjson.Result) {
	if tier := diagnosticText(usage.Get("service_tier")); tier != "" {
		o.data.ServiceTier = tier
	} else if speed := diagnosticText(usage.Get("speed")); speed != "" {
		o.data.ServiceTier = speed
	}
	if geo := diagnosticText(usage.Get("inference_geo")); geo != "" {
		o.data.InferenceGeo = geo
	}
	if tokens := diagnosticToken(usage.Get("output_tokens_details.thinking_tokens")); tokens != nil {
		o.data.ThinkingTokens = tokens
	}
	iterations := usage.Get("iterations")
	if iterations.IsArray() {
		o.data.Iterations = nil
		count := 0
		iterations.ForEach(func(_, value gjson.Result) bool {
			count++
			if len(o.data.Iterations) < responseDiagnosticsMaxItems {
				o.data.Iterations = append(o.data.Iterations, ResponseIteration{
					Type: diagnosticText(value.Get("type")), Model: diagnosticText(value.Get("model")),
					Usage: anthropicDiagnosticUsage(value, nil),
				})
			}
			return true
		})
		o.data.IterationCount = &count
		o.data.IterationsTruncated = count > responseDiagnosticsMaxItems
	}
}

func (o *responseDiagnosticsObserver) addTool(kind, name string) {
	for i := range o.data.ToolCalls {
		if o.data.ToolCalls[i].Type == kind && o.data.ToolCalls[i].Name == name {
			o.data.ToolCalls[i].Count++
			return
		}
	}
	if len(o.data.ToolCalls) < responseDiagnosticsMaxItems {
		o.data.ToolCalls = append(o.data.ToolCalls, ResponseToolCall{Type: kind, Name: name, Count: 1})
	}
}

func (o *responseDiagnosticsObserver) snapshot() *ResponseDiagnostics {
	if o == nil || o.data.Protocol == "" {
		return nil
	}
	d := o.data
	d.StreamComplete = o.terminal && o.openBlocks == 0 && d.ErrorType == ""
	if d.InitialUsage != nil && d.FinalUsage != nil && d.InitialUsage.TotalInputTokens != nil && d.FinalUsage.TotalInputTokens != nil && *d.InitialUsage.TotalInputTokens > 0 {
		rate := float64(*d.FinalUsage.TotalInputTokens-*d.InitialUsage.TotalInputTokens) / float64(*d.InitialUsage.TotalInputTokens)
		d.InputChangeRate = &rate
	}
	return &d
}

func observedResponseDiagnostics(c *gin.Context) *ResponseDiagnostics {
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		return nil
	}
	return observer.diagnostics.snapshot()
}

func openAIDiagnosticUsage(node gjson.Result) *ResponseTokenUsage {
	if !node.IsObject() {
		return nil
	}
	total := diagnosticToken(node.Get("input_tokens"))
	if total == nil {
		total = diagnosticToken(node.Get("prompt_tokens"))
	}
	output := diagnosticToken(node.Get("output_tokens"))
	if output == nil {
		output = diagnosticToken(node.Get("completion_tokens"))
	}
	cached := diagnosticToken(node.Get("input_tokens_details.cached_tokens"))
	if cached == nil {
		cached = diagnosticToken(node.Get("prompt_tokens_details.cached_tokens"))
	}
	u := &ResponseTokenUsage{TotalInputTokens: total, OutputTokens: output, CacheReadTokens: cached}
	if total != nil && cached != nil && *total >= *cached {
		uncached, creation := *total-*cached, int64(0)
		u.InputTokens, u.CacheCreationTokens = &uncached, &creation
	}
	return u
}

func (o *responseDiagnosticsObserver) observeOpenAI(payload []byte, kind string) {
	if kind == "" {
		kind = gjson.GetBytes(payload, "type").String()
	}
	responses := strings.HasPrefix(kind, "response.")
	if kind == "error" && strings.HasPrefix(o.data.Protocol, "openai_") {
		o.data.EventCount++
		o.data.ErrorType = diagnosticText(gjson.GetBytes(payload, "error.type"))
		if o.data.ErrorType == "" {
			o.data.ErrorType = "stream_error"
		}
		return
	}
	if !responses && gjson.GetBytes(payload, "object").String() != "chat.completion.chunk" {
		return
	}
	o.data.EventCount++
	if responses {
		o.data.Protocol = "openai_responses"
		if strings.HasSuffix(kind, ".delta") || (strings.HasSuffix(kind, ".done") && !isUpstreamResponseModelTerminalEvent(kind)) {
			return
		}
	} else {
		o.data.Protocol = "openai_chat"
	}
	node := gjson.ParseBytes(payload)
	if responses {
		response := node.Get("response")
		if id := diagnosticText(response.Get("id")); id != "" {
			o.data.MessageID = id
		}
		terminal := isUpstreamResponseModelTerminalEvent(kind)
		if usage := openAIDiagnosticUsage(response.Get("usage")); usage != nil {
			if terminal {
				o.data.FinalUsage = usage
			} else if o.data.InitialUsage == nil {
				o.data.InitialUsage = usage
			}
		}
		if tier := diagnosticText(response.Get("service_tier")); tier != "" {
			o.data.ServiceTier = tier
		}
		if thinking := diagnosticToken(response.Get("usage.output_tokens_details.reasoning_tokens")); thinking != nil {
			o.data.ThinkingTokens = thinking
		}
		if terminal {
			o.terminal = true
			o.data.StopReason = strings.TrimPrefix(kind, "response.")
			if reason := diagnosticText(response.Get("incomplete_details.reason")); reason != "" {
				o.data.StopReason = reason
			}
			if kind == "response.failed" {
				o.data.ErrorType = strings.TrimPrefix(kind, "response.")
			}
		}
		if kind == "response.output_item.added" && node.Get("item.type").String() == "function_call" {
			o.addTool("tool_use", diagnosticText(node.Get("item.name")))
		}
		return
	}
	o.data.MessageID = diagnosticText(node.Get("id"))
	if tier := diagnosticText(node.Get("service_tier")); tier != "" {
		o.data.ServiceTier = tier
	}
	node.Get("choices").ForEach(func(_, choice gjson.Result) bool {
		if reason := diagnosticText(choice.Get("finish_reason")); reason != "" {
			o.data.StopReason = reason
			o.terminal = true
		}
		choice.Get("delta.tool_calls").ForEach(func(_, tool gjson.Result) bool {
			if name := diagnosticText(tool.Get("function.name")); name != "" {
				o.addTool("tool_use", name)
			}
			return true
		})
		return true
	})
	if usage := openAIDiagnosticUsage(node.Get("usage")); usage != nil {
		if o.terminal {
			o.data.FinalUsage = usage
		} else if o.data.InitialUsage == nil {
			o.data.InitialUsage = usage
		}
	}
	if thinking := diagnosticToken(node.Get("usage.completion_tokens_details.reasoning_tokens")); thinking != nil {
		o.data.ThinkingTokens = thinking
	}
}
