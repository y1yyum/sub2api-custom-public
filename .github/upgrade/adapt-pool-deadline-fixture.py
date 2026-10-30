"""Order one late-success pool fixture on its actual attempt deadline."""
PATH = 'backend/internal/service/token_refresh_pool_health_test.go'
TARGET = 'func TestTokenRefreshService_LateSuccessPastAttemptDeadlineIsRejected(t *testing.T) {'
BEFORE = '''\trefresher := &poolHealthRefresher{
\t\tdelay:         30 * time.Millisecond,
\t\tignoreContext: true,
\t}
'''
AFTER = '\trefresher := &poolHealthLateSuccessRefresher{}\n'
CONSTRUCTOR_BEFORE = '\tsvc := newPoolHealthService(repo, refresher, config.TokenRefreshConfig{MaxRetries: 1})'
CONSTRUCTOR_AFTER = '\tsvc := newPoolHealthService(repo, &refresher.poolHealthRefresher, config.TokenRefreshConfig{MaxRetries: 1})'
HELPER = '''// This fixture must return success after the attempt context is actually done,
// not race two timer callbacks under a loaded race-test runner.
type poolHealthLateSuccessRefresher struct {
\tpoolHealthRefresher
}

func (r *poolHealthLateSuccessRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
\t<-ctx.Done()
\treturn r.poolHealthRefresher.Refresh(ctx, account)
}

'''


def adapt(path, text):
    if path != PATH or text.count(TARGET) != 1 or 'poolHealthLateSuccessRefresher' in text:
        raise ValueError('unreviewed pool deadline fixture path/target')
    start = text.index(TARGET)
    end = text.index('\nfunc ', start + len(TARGET))
    section = text[start:end]
    if section.count(BEFORE) != 1 or section.count(CONSTRUCTOR_BEFORE) != 1:
        raise ValueError('pool deadline fixture setup must occur exactly once')
    section = section.replace(BEFORE, AFTER, 1).replace(CONSTRUCTOR_BEFORE, CONSTRUCTOR_AFTER, 1)
    return text[:start] + HELPER + section + text[end:]
