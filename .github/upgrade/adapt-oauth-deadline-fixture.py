"""Order one late-success fixture on actual context completion, not two timers."""
PATH = 'backend/internal/service/oauth_refresh_api_test.go'
TARGET = 'func TestRefreshIfNeeded_LateSuccessAfterDeadlineDoesNotPersist(t *testing.T) {'
REPLACEMENTS = (
    ('\t\tdelay:        30 * time.Millisecond,\n', ''),
    ('\tdefer cancel()\n', '\tdefer cancel()\n\texecutor.onRefresh = func() { <-ctx.Done() }\n'),
)


def adapt(path, text):
    if path != PATH or text.count(TARGET) != 1:
        raise ValueError('unreviewed OAuth deadline fixture path/target')
    start = text.index(TARGET)
    end = text.index('\nfunc ', start+len(TARGET))
    section = text[start:end]
    for before, after in REPLACEMENTS:
        if section.count(before) != 1:
            raise ValueError('OAuth deadline fixture anchor must occur exactly once')
        section = section.replace(before, after, 1)
    return text[:start] + section + text[end:]
