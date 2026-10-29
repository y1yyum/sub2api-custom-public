package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestOpenAIWSModelMultiplicityWithoutAllowlist(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeDedicated} {
		for _, payload := range []string{
			`{"type":"response.create","model":"gpt-5.4","model":"gpt-5.4"}`,
			`{"type":"response.create","model":"gpt-5.4","Model":"gpt-4.1"}`,
			`{"type":"response.create","mo\u0064el":"gpt-5.4","model":null}`,
		} {
			t.Run(mode+"/first/"+payload, func(t *testing.T) {
				runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					firstPayload: payload, group: wsAllowlistGroup(false), ingressMode: mode,
					firstFrameCloseExpected: true, closeReason: "duplicate model field",
				})
			})
			t.Run(mode+"/subsequent/"+payload, func(t *testing.T) {
				runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					firstPayload: `{"type":"response.create","model":"gpt-5.4"}`, secondPayload: payload,
					group: wsAllowlistGroup(false), ingressMode: mode, secondTurnCloseExpected: true, closeReason: "duplicate model field",
				})
			})
		}
	}
}
