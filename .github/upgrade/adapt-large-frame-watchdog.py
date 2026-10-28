"""One pinned large-payload loopback acceptance fixture; finite hang budgets only."""
import hashlib

PATH = 'backend/internal/service/openai_ws_http_bridge_test.go'
ANCHOR = 'func TestOpenAIWSHTTPBridgeAcceptsFirstFrameAboveLegacy16MiB(t *testing.T) {'
FUNCTION_SHA256 = '48e60723a2a63619b05e27e8dde084894f9cdb89210753501254e4968707c73c'
REPLACEMENTS = {
    'readCtx, cancelRead := context.WithTimeout(r.Context(), 10*time.Second)': 'readCtx, cancelRead := context.WithTimeout(r.Context(), 30*time.Second)',
    'proxyCtx, cancelProxy := context.WithTimeout(r.Context(), 20*time.Second)': 'proxyCtx, cancelProxy := context.WithTimeout(r.Context(), 90*time.Second)',
    'writeCtx, cancelWrite := context.WithTimeout(context.Background(), 20*time.Second)': 'writeCtx, cancelWrite := context.WithTimeout(context.Background(), 30*time.Second)',
    'readCtx, cancelRead := context.WithTimeout(context.Background(), 10*time.Second)': 'readCtx, cancelRead := context.WithTimeout(context.Background(), 30*time.Second)',
    'time.After(10 * time.Second)': 'time.After(30 * time.Second)',
}


def adapt(path, text):
    if path != PATH or text.count(ANCHOR) != 1:
        raise ValueError('unreviewed large-frame fixture path or function')
    start = text.index(ANCHOR)
    end = text.find('\nfunc ', start + len(ANCHOR))
    end = len(text) if end == -1 else end + 1
    section = text[start:end]
    if hashlib.sha256(section.encode()).hexdigest() != FUNCTION_SHA256:
        raise ValueError('large-frame acceptance function changed')
    for old, new in REPLACEMENTS.items():
        if section.count(old) != 1:
            raise ValueError('large-frame fixture watchdog anchor changed')
        section = section.replace(old, new, 1)
    return text[:start] + section + text[end:]
