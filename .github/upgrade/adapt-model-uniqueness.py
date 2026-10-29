"""Intentional all-duplicates-rejected contract; all other WS tests unchanged."""
import re

PATH = 'backend/internal/handler/openai_gateway_ws_model_allowlist_test.go'
OLD = '''// 重复但同值的 model 键不误伤，连接正常完成两个 turn。
func TestOpenAIResponsesWebSocket_DuplicateIdenticalModelKeysAllowed(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:  `{"type":"response.create","model":"gpt-5.4","model":"gpt-5.4","stream":false}`,
		secondPayload: `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		group:         wsAllowlistGroup(true, "gpt-5.4"),
	})
	if len(got.clientEvents) != 2 {
		t.Fatalf("expected two completed events, got %d", len(got.clientEvents))
	}
}'''
NEW = '''// Custom gateway rejects all duplicate top-level model keys, including identical values.
func TestOpenAIResponsesWebSocket_DuplicateIdenticalModelKeysRejected(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeDedicated} {
		t.Run(mode, func(t *testing.T) {
			runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload: `{"type":"response.create","model":"gpt-5.4","model":"gpt-5.4","stream":false}`,
				group: wsAllowlistGroup(true, "gpt-5.4"), ingressMode: mode,
				firstFrameCloseExpected: true, closeReason: "duplicate model field",
			})
		})
	}
}'''


ROUTE_PATH = 'backend/internal/server/routes/gateway_model_allowlist_test.go'
ROUTE_REPLACEMENTS = {
    'gin.HandlerFunc(apiKeyAuth), groupModelAllowlist,':
        ('gin.HandlerFunc(apiKeyAuth), modelUniqueness, groupModelAllowlist,', 2),
    **{group + '.Use(groupModelAllowlist)':
       (group + '.Use(modelUniqueness, groupModelAllowlist)', 1)
       for group in ('gateway', 'gemini', 'antigravityV1', 'antigravityV1Beta')},
}


def adapt(path, text):
    if path == ROUTE_PATH:
        for old, (new, count) in ROUTE_REPLACEMENTS.items():
            if text.count(old) != count:
                raise ValueError('unreviewed route middleware assertion')
            text = text.replace(old, new)
        return text
    if path != PATH or text.count(OLD) != 1:
        raise ValueError('unreviewed model uniqueness fixture')
    text = text.replace(OLD, NEW)
    for name in ('FirstFrameDuplicateModelKeysRejected', 'FirstFrameCaseVariantModelKeyRejected', 'SubsequentTurnDuplicateModelKeysRejected'):
        pattern = r'(func TestOpenAIResponsesWebSocket_' + name + r'\(t \*testing.T\) \{.*?\n\})'
        matches = list(re.finditer(pattern, text, re.S))
        if len(matches) != 1:
            raise ValueError('missing pinned duplicate WS case')
        old = matches[0].group(0)
        anchor = 'firstFrameCloseExpected: true,' if name.startswith('FirstFrame') else 'secondTurnCloseExpected: true,'
        if old.count(anchor) != 1:
            raise ValueError('unexpected duplicate WS close assertion')
        new = old.replace(anchor, anchor + ' closeReason: "duplicate model field",')
        text = text.replace(old, new)
    return text
