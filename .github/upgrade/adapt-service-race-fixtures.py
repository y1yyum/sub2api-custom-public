"""Synchronize fixture state and wait for real worker completion; no production edits."""
import re


def once(text, old, new):
    if text.count(old) != 1:
        raise ValueError('service fixture anchor expected exactly once')
    return text.replace(old, new)


def adapt(path, text):
    if path.endswith('/channel_monitor_checker_body_test.go'):
        text = once(text, '\t"strings"\n', '\t"strings"\n\t"sync"\n')
        for typ in ('captureHandler', 'openAICaptureHandler'):
            text = once(text, f'type {typ} struct {{\n', f'type {typ} struct {{\n\tmu sync.Mutex\n')
        # Decode outside the lock; atomically publish observations. Configuration
        # is immutable after httptest server creation; response defaults are local.
        for typ in ('captureHandler', 'openAICaptureHandler'):
            start = text.index(f'func (h *{typ}) ServeHTTP(')
            end = text.index('\nfunc ', start+1)
            section = text[start:end]
            section = section.replace('\th.lastHeaders = r.Header.Clone()\n', '')
            section = section.replace('\th.lastPath = r.URL.Path\n', '')
            publish = '\th.mu.Lock()\n\th.lastHeaders = r.Header.Clone()\n'
            if typ == 'openAICaptureHandler':
                publish += '\th.lastPath = r.URL.Path\n'
            publish += '\th.lastBody = parsed\n\th.mu.Unlock()\n'
            section = once(section, '\th.lastBody = parsed\n', publish)
            section = section.replace('if h.status == 0 {', 'status := h.status\n\tif status == 0 {')
            section = section.replace('h.status = ', 'status = ').replace('w.WriteHeader(h.status)', 'w.WriteHeader(status)')
            section = section.replace('if h.lastPath == providerOpenAIResponsesPath {', 'if r.URL.Path == providerOpenAIResponsesPath {')
            text = text[:start]+section+text[end:]
        # Tests consume immutable captured maps/headers through locked snapshots.
        def observations(match):
            return re.sub(r'\bh\.(lastBody|lastHeaders|lastPath)\b', r'h.snapshot().\1', match.group(0))
        text = re.sub(r'(?ms)^func Test.*?(?=^func |\Z)', observations, text)
        return text + '''
type monitorCaptureSnapshot struct {
	lastBody map[string]any
	lastHeaders http.Header
	lastPath string
}

func (h *captureHandler) snapshot() monitorCaptureSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return monitorCaptureSnapshot{lastBody: cloneMonitorCaptureBody(h.lastBody), lastHeaders: h.lastHeaders.Clone()}
}

func (h *openAICaptureHandler) snapshot() monitorCaptureSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return monitorCaptureSnapshot{lastBody: cloneMonitorCaptureBody(h.lastBody), lastHeaders: h.lastHeaders.Clone(), lastPath: h.lastPath}
}

func cloneMonitorCaptureBody(body map[string]any) map[string]any {
	if body == nil {
		return nil
	}
	copyBody := make(map[string]any, len(body))
	for key, value := range body {
		copyBody[key] = value
	}
	return copyBody
}
'''
    if path.endswith('/usage_cleanup_service_test.go'):
        text = once(text, 'type dashboardRepoStub struct {\n', 'type dashboardRepoStub struct {\n\tmu sync.Mutex\n')
        text = once(text, 'func (s *dashboardRepoStub) RecomputeRange(ctx context.Context, start, end time.Time) error {\n',
                    'func (s *dashboardRepoStub) RecomputeRange(ctx context.Context, start, end time.Time) error {\n\ts.mu.Lock()\n\tdefer s.mu.Unlock()\n')
        text = text.replace('dashboardRepo.recomputeCalls', 'dashboardRepo.recomputeCallCount()')
        return text + '''
func (s *dashboardRepoStub) recomputeCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recomputeCalls
}
'''
    if path.endswith('/auth_service_email_bind_test.go'):
        text = once(text, 'type emailBindCacheStub struct {\n', 'type emailBindCacheStub struct {\n\tmu sync.Mutex\n')
        for signature in (
            'func (s *emailBindCacheStub) GetVerificationCode(context.Context, string) (*service.VerificationCodeData, error) {\n',
            'func (s *emailBindCacheStub) SetVerificationCode(_ context.Context, email string, _ *service.VerificationCodeData, _ time.Duration) error {\n',
            'func (s *emailBindCacheStub) IncrVerificationCodeAttempts(context.Context, string) (int, error) {\n',
        ):
            text = once(text, signature, signature+'\ts.mu.Lock()\n\tdefer s.mu.Unlock()\n')
        text = once(text, '\treturn s.data, nil\n', '''	if s.data == nil {
		return nil, nil
	}
	copyData := *s.data
	return &copyData, nil
''')
        text = text.replace('cache.setEmails)', 'cache.sentEmails())')
        return text + '''
func (s *emailBindCacheStub) sentEmails() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.setEmails...)
}
'''
    if path.endswith('/grok_free_quota_gate_test.go'):
        # Replacing a sync.Map while a goroutine uses it is not safe. Wait on
        # production's in-flight markers (removed after cache stores/sweep),
        # then use sync.Map.Clear rather than overwriting its lock internals.
        text = text.replace('openaiGrokFreeQuotaGateCache = sync.Map{}',
                            'resetGrokQuotaFixtureCache(t, &openaiGrokFreeQuotaGateCache)')
        text = re.sub(r'(\tscheduler := [^\n]+\n)',
                      r'\1\tt.Cleanup(func() { waitGrokQuotaFixtureRefresh(t, &scheduler.grokFreeQuotaGateCache) })\n', text)
        text = text.replace('\tvar cache sync.Map\n', '\tvar cache sync.Map\n\tt.Cleanup(func() { waitGrokQuotaFixtureRefresh(t, &cache) })\n')
        locked_poll = '\t\trepo.mu.Lock()\n\t\tdefer repo.mu.Unlock()\n\t\treturn repo.calls >= 1'
        if text.count(locked_poll) != 2:
            raise ValueError('expected two guarded call-count polls')
        text = text.replace(locked_poll, '\t\treturn repo.callCount() >= 1')
        text = once(text, '''	if root, ok := freeQuotaRefreshInFlight.Load(&scheduler.grokFreeQuotaGateCache); ok {
		if m, ok := root.(*sync.Map); ok {
			m.Delete(int64(1))
		}
	}''', '\twaitGrokQuotaFixtureRefresh(t, &scheduler.grokFreeQuotaGateCache)')
        text = once(text, '// Clear in-flight markers so a refresh is allowed after we force-expire the entry.',
                    '// Wait for the prior worker before force-expiring its cache entry.')
        text = text.replace('repo.calls', 'repo.callCount()')
        text = text.replace('repo.lastIDs', 'repo.queriedIDs()').replace('repo.start,', 'repo.queryStart(),')
        # A call counter records query entry, not completion of its worker.
        text = text.replace('return repo.callCount() >= 1\n\t}, 2*time.Second, 10*time.Millisecond)',
                            'return repo.callCount() >= 1\n\t}, 2*time.Second, 10*time.Millisecond)\n\twaitGrokQuotaFixtureRefresh(t, &scheduler.grokFreeQuotaGateCache)')
        return text + '''
func (r *grokFreeQuotaUsageRepoStub) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *grokFreeQuotaUsageRepoStub) queriedIDs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.lastIDs...)
}

func (r *grokFreeQuotaUsageRepoStub) queryStart() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.start
}

func waitGrokQuotaFixtureRefresh(t *testing.T, cache *sync.Map) {
	t.Helper()
	require.Eventually(t, func() bool {
		root, ok := freeQuotaRefreshInFlight.Load(cache)
		if !ok {
			return true
		}
		inFlight, ok := root.(*sync.Map)
		if !ok {
			return false
		}
		empty := true
		inFlight.Range(func(_, _ any) bool { empty = false; return false })
		return empty
	}, 2*time.Second, 10*time.Millisecond, "fixture refresh must finish before reset/teardown")
}

func resetGrokQuotaFixtureCache(t *testing.T, cache *sync.Map) {
	t.Helper()
	waitGrokQuotaFixtureRefresh(t, cache)
	cache.Clear()
	t.Cleanup(func() {
		waitGrokQuotaFixtureRefresh(t, cache)
		cache.Clear()
	})
}
'''
    raise ValueError('unreviewed service race fixture path: '+path)
