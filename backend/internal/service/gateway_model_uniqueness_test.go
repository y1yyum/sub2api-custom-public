package service

import (
	"errors"
	"testing"
)

func TestGatewayParserModelMultiplicityContract(t *testing.T) {
	for _, protocol := range []string{"anthropic", "chat_completions", "responses", "gemini"} {
		for _, body := range []string{`{"model":"a","model":"b"}`, `{"model":"a","model":"a"}`, `{"mo\u0064el":"a","Model":null}`} {
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), protocol)
			if parsed != nil || !errors.Is(err, ErrDuplicateModelField) {
				t.Fatalf("%s: parsed=%v err=%v, want duplicate rejection", protocol, parsed, err)
			}
		}
	}
}

func TestGatewayParserModelInvalidAndNestedInputs(t *testing.T) {
	for _, body := range []string{`{"model":42}`, `{"model":`, `{"model":"a","model":`} {
		if parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), "responses"); parsed != nil || err == nil {
			t.Fatalf("invalid input must retain parse failure, parsed=%v err=%v", parsed, err)
		}
	}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"a","input":{"model":"b"}}`)), "responses")
	if err != nil || parsed.Model != "a" {
		t.Fatalf("nested model must remain legal: parsed=%v err=%v", parsed, err)
	}
	if _, err := parsed.CloneForBody([]byte(`{"model":"a","model":"a"}`)); !errors.Is(err, ErrDuplicateModelField) {
		t.Fatalf("clone must propagate duplicate error: %v", err)
	}
	if err := parsed.ReplaceBody([]byte(`{"model":"a","model":"a"}`)); !errors.Is(err, ErrDuplicateModelField) {
		t.Fatalf("replacement must propagate duplicate error: %v", err)
	}
}
