"""Hash-pinned URL-only validation fixtures: no DNS, dial or production changes."""


def adapt(path, text):
    if path != 'backend/internal/service/channel_monitor_quota_mode_test.go':
        raise ValueError('unreviewed validation fixture')
    anchor = 'func TestValidateCreateParams_CheckModeMatrix(t *testing.T) {'
    if text.count(anchor) != 1:
        raise ValueError('validation matrix anchor must occur once')
    start = text.index(anchor)
    end = text.find('\nfunc ', start + len(anchor))
    if end == -1:
        end = len(text)
    section = text[start:end]
    expected = {'https://api.openai.com': 2, 'https://example.com': 2, 'https://api.kimi.com': 1}
    for old, count in expected.items():
        if section.count(old) != count:
            raise ValueError('validation fixture URL count changed')
        section = section.replace(old, 'https://192.0.2.1')
    section = section.replace(anchor, anchor + '''
	// URL validation only: a documentation-range IP uses the literal-IP path.
	// No DNS query, HTTP request or TCP dial is performed by validateCreateParams.
	// Keep the production SSRF policy unchanged (loopback remains rejected).
''', 1)
    # Preserve every original assertion and add an explicit SSRF-policy guard.
    key_case = '\t\t{\n\t\t\tname: "probe requires api key",'
    if section.count(key_case) != 1:
        raise ValueError('API key fixture anchor changed')
    section = section.replace(key_case, '''		{
			name: "probe rejects loopback before missing api key",
			params: ChannelMonitorCreateParams{
				Provider: MonitorProviderOpenAI, CheckMode: MonitorCheckModeProbe,
				Endpoint: "https://127.0.0.1", IntervalSeconds: 60, PrimaryModel: "gpt-5",
			},
			wantErr: ErrChannelMonitorEndpointPrivate,
		},
''' + key_case, 1)
    return text[:start] + section + text[end:]
