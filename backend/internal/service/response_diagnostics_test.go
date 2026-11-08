//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponseDiagnosticsScaledAnthropicStream(t *testing.T) {
	o := &upstreamResponseModelObserver{}
	for _, event := range []string{
		`{"type":"message_start","_sub2api_display_scaled":true,"message":{"id":"msg_example","model":"claude-haiku-5-5","usage":{"input_tokens":4,"output_tokens":0,"cache_read_input_tokens":1412896,"cache_creation_input_tokens":258377,"service_tier":"standard","inference_geo":"not_available"}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"editor","input":{"secret":"never persist"}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"sensitive body"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","_sub2api_display_scaled":true,"delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":4,"output_tokens":6902,"cache_read_input_tokens":1631328,"cache_creation_input_tokens":298323,"output_tokens_details":{"thinking_tokens":3459}}}`,
		`{"type":"message_stop"}`,
	} {
		o.ObserveAnthropic([]byte(event))
	}
	d := o.diagnostics.snapshot()
	require.NotNil(t, d)
	require.EqualValues(t, 1412896, *d.InitialUsage.CacheReadTokens)
	require.EqualValues(t, 1631328, *d.FinalUsage.CacheReadTokens)
	require.EqualValues(t, 1929655, *d.FinalUsage.TotalInputTokens)
	require.True(t, *d.InitialUsage.DisplayScaled)
	require.True(t, *d.DisplayScaled)
	require.InDelta(t, 0.154599, *d.InputChangeRate, 0.000001)
	require.True(t, d.StreamComplete)
	require.Equal(t, "tool_use", d.StopReason)
	require.Equal(t, "msg_example", d.MessageID)
	require.EqualValues(t, 3459, *d.ThinkingTokens)
	require.Nil(t, d.IterationCount, "an omitted iterations field is unknown, not zero")
	require.Equal(t, []ResponseToolCall{{Type: "tool_use", Name: "editor", Count: 1}}, d.ToolCalls)
	encoded, err := json.Marshal(d)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "sensitive")
	require.NotContains(t, string(encoded), "secret")
}

func TestResponseDiagnosticsIncompleteAndMissingUsage(t *testing.T) {
	o := &upstreamResponseModelObserver{}
	o.ObserveAnthropic([]byte(`{"type":"message_start","message":{"model":"claude-haiku-5-5","usage":{"input_tokens":0}}}`))
	o.ObserveAnthropic([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"do not persist sensitive error bodies"}}`))
	d := o.diagnostics.snapshot()
	require.False(t, d.StreamComplete)
	require.Equal(t, "overloaded_error", d.ErrorType)
	require.NotNil(t, d.InitialUsage.InputTokens)
	require.Zero(t, *d.InitialUsage.InputTokens)
	require.Nil(t, d.InitialUsage.CacheReadTokens)
	require.Nil(t, d.InitialUsage.TotalInputTokens)
	require.Nil(t, d.FinalUsage)
	require.Nil(t, d.InputChangeRate)
	require.Nil(t, d.DisplayScaled)
}

func TestResponseDiagnosticsCumulativeUsageDoesNotDoubleCount(t *testing.T) {
	o := &upstreamResponseModelObserver{}
	o.ObserveAnthropic([]byte(`{"type":"message_start","message":{"model":"claude-haiku-5-5","usage":{"input_tokens":4,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`))
	o.ObserveAnthropic([]byte(`{"type":"message_delta","usage":{"output_tokens":7}}`))
	o.ObserveAnthropic([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10,"cache_read_input_tokens":0,"iterations":[{"type":"message","input_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":20,"output_tokens":10}]}}`))
	o.ObserveAnthropic([]byte(`{"type":"message_stop"}`))
	d := o.diagnostics.snapshot()
	require.EqualValues(t, 100, *d.InitialUsage.CacheReadTokens)
	require.Zero(t, *d.FinalUsage.CacheReadTokens)
	require.EqualValues(t, 10, *d.FinalUsage.OutputTokens)
	require.EqualValues(t, 24, *d.FinalUsage.TotalInputTokens)
	require.Equal(t, 1, *d.IterationCount)
	require.Len(t, d.Iterations, 1)
	require.True(t, d.StreamComplete)
}

func TestResponseDiagnosticsBoundsAndAttemptIsolation(t *testing.T) {
	o := &upstreamResponseModelObserver{}
	require.Nil(t, o.diagnostics.snapshot())
	o.ObserveAnthropic([]byte(`{"type":"message","model":"claude-haiku-5-5","usage":{"input_tokens":4}}`))
	require.Nil(t, o.diagnostics.snapshot(), "non-streaming responses have no stream diagnostics")
	o.ObserveAnthropic([]byte(`{"type":"message_start","message":{"model":"claude-haiku-5-5"}}`))
	iterations := strings.Repeat(`{"type":"message","input_tokens":1},`, 40)
	o.ObserveAnthropic([]byte(`{"type":"message_delta","usage":{"iterations":[` + strings.TrimSuffix(iterations, ",") + `]}}`))
	d := o.diagnostics.snapshot()
	require.Len(t, d.Iterations, responseDiagnosticsMaxItems)
	require.Equal(t, 40, *d.IterationCount)
	require.True(t, d.IterationsTruncated)
	require.Nil(t, (&upstreamResponseModelObserver{}).diagnostics.snapshot())
}

func BenchmarkResponseDiagnosticsTextDelta(b *testing.B) {
	payload := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + strings.Repeat("x", 2048) + `"}}`)
	o := &responseDiagnosticsObserver{}
	o.observeAnthropic([]byte(`{"type":"message_start","message":{"model":"claude-haiku-5-5"}}`))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o.observeAnthropic(payload)
	}
}

func TestResponseDiagnosticsOpenAIStreaming(t *testing.T) {
	t.Run("responses final-only usage", func(t *testing.T) {
		o := &upstreamResponseModelObserver{}
		o.ObserveOpenAI([]byte(`{"type":"response.created","response":{"id":"resp_example","model":"gpt-example","usage":null}}`), "response.created")
		o.ObserveOpenAI([]byte(`{"type":"response.completed","response":{"id":"resp_example","model":"gpt-example","service_tier":"default","usage":{"input_tokens":200,"output_tokens":20,"input_tokens_details":{"cached_tokens":100},"output_tokens_details":{"reasoning_tokens":5}}}}`), "response.completed")
		d := o.diagnostics.snapshot()
		require.True(t, d.StreamComplete)
		require.Nil(t, d.InitialUsage)
		require.Nil(t, d.InputChangeRate)
		require.EqualValues(t, 200, *d.FinalUsage.TotalInputTokens)
		require.EqualValues(t, 100, *d.FinalUsage.InputTokens)
		require.EqualValues(t, 5, *d.ThinkingTokens)
	})
	t.Run("chat trailing usage", func(t *testing.T) {
		o := &upstreamResponseModelObserver{}
		o.ObserveOpenAI([]byte(`{"object":"chat.completion.chunk","id":"chat_example","model":"gpt-example","choices":[{"finish_reason":"stop","delta":{}}]}`), "")
		o.ObserveOpenAI([]byte(`{"object":"chat.completion.chunk","id":"chat_example","model":"gpt-example","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0}}}`), "")
		d := o.diagnostics.snapshot()
		require.True(t, d.StreamComplete)
		require.Equal(t, "stop", d.StopReason)
		require.EqualValues(t, 100, *d.FinalUsage.TotalInputTokens)
		require.Nil(t, d.InitialUsage)
	})
	t.Run("output limit is a complete stream", func(t *testing.T) {
		o := &upstreamResponseModelObserver{}
		o.ObserveOpenAI([]byte(`{"type":"response.incomplete","response":{"model":"gpt-example","incomplete_details":{"reason":"max_output_tokens"}}}`), "response.incomplete")
		d := o.diagnostics.snapshot()
		require.True(t, d.StreamComplete)
		require.Equal(t, "max_output_tokens", d.StopReason)
	})
}

func TestResponseDiagnosticsResponsesTerminalVariants(t *testing.T) {
	for _, kind := range []string{"response.done", "response.cancelled", "response.canceled"} {
		t.Run(kind, func(t *testing.T) {
			o := &upstreamResponseModelObserver{}
			o.ObserveOpenAI([]byte(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_example","usage":{"input_tokens":100,"output_tokens":10}}}`, kind)), kind)
			d := o.diagnostics.snapshot()
			require.True(t, d.StreamComplete)
			require.Equal(t, strings.TrimPrefix(kind, "response."), d.StopReason)
			require.EqualValues(t, 100, *d.FinalUsage.TotalInputTokens)
			require.EqualValues(t, 10, *d.FinalUsage.OutputTokens)
		})
	}
}

func TestResponseDiagnosticsNativeAnthropicConversions(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", endpoint, stream), func(t *testing.T) {
				request := map[string]any{"model": "k3", "stream": stream}
				if endpoint == "responses" {
					request["input"] = "hello"
				} else {
					request["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
				}
				body, err := json.Marshal(request)
				require.NoError(t, err)
				upstream := &httpUpstreamRecorder{resp: nativeAnthropicStreamResponse()}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				c := adaptiveProtocolTestContext("/v1/"+endpoint, body)
				account := nativeAnthropicTestAccount()
				account.Credentials["base_url"] = "http://anthropic.example"
				var result *OpenAIForwardResult
				if endpoint == "responses" {
					result, err = svc.Forward(context.Background(), c, account, body)
				} else {
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				}
				require.NoError(t, err)
				d := result.ResponseDiagnostics
				require.NotNil(t, d)
				require.Equal(t, "anthropic", d.Protocol)
				require.True(t, d.StreamComplete)
				require.Equal(t, "end_turn", d.StopReason)
				require.EqualValues(t, 93, *d.InitialUsage.InputTokens)
				require.EqualValues(t, 16, *d.FinalUsage.OutputTokens)
			})
		}
	}
}

func TestResponseDiagnosticsNilObserver(t *testing.T) {
	var observer *upstreamResponseModelObserver
	require.NotPanics(t, func() {
		observer.ObserveAnthropic([]byte(`{"type":"message_start","message":{"model":"claude-example"}}`))
		observer.ObserveOpenAI([]byte(`{"type":"response.done","response":{"model":"gpt-example"}}`), "response.done")
	})
}

func TestResponseDiagnosticsResponsesRecoveredError(t *testing.T) {
	for _, kind := range []string{"response.completed", "response.done"} {
		t.Run(kind, func(t *testing.T) {
			o := &upstreamResponseModelObserver{}
			o.ObserveOpenAI([]byte(`{"type":"response.created","response":{"id":"resp_example"}}`), "response.created")
			o.ObserveOpenAI([]byte(`{"type":"error","error":{"type":"server_error"}}`), "error")
			require.False(t, o.diagnostics.snapshot().StreamComplete)
			o.ObserveOpenAI([]byte(fmt.Sprintf(`{"type":%q,"response":{"usage":{"input_tokens":100,"output_tokens":10}}}`, kind)), kind)
			d := o.diagnostics.snapshot()
			require.True(t, d.StreamComplete)
			require.Empty(t, d.ErrorType)
			require.EqualValues(t, 100, *d.FinalUsage.TotalInputTokens)
		})
	}
	o := &upstreamResponseModelObserver{}
	o.ObserveOpenAI([]byte(`{"type":"response.failed","response":{}}`), "response.failed")
	o.ObserveOpenAI([]byte(`{"type":"response.completed","response":{}}`), "response.completed")
	require.False(t, o.diagnostics.snapshot().StreamComplete)
	require.Equal(t, "failed", o.diagnostics.snapshot().ErrorType)
}

func TestResponseDiagnosticsStopSequencePreservesWhitespace(t *testing.T) {
	for _, sequence := range []string{"\n\nHuman:", "   ", strings.Repeat("界", 250)} {
		o := &upstreamResponseModelObserver{}
		o.ObserveAnthropic([]byte(`{"type":"message_start","message":{}}`))
		payload, err := json.Marshal(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "stop_sequence", "stop_sequence": sequence}})
		require.NoError(t, err)
		o.ObserveAnthropic(payload)
		want := []rune(sequence)
		if len(want) > 200 {
			want = want[:200]
		}
		require.Equal(t, string(want), o.diagnostics.snapshot().StopSequence)
	}
}
