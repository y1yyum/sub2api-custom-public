"""Move constant Gin mode setup out of parallel test execution."""
import re

COUNTS = {
    'backend/internal/handler/payment_handler_resume_test.go': 1,
    'backend/internal/service/account_test_service_gemini_test.go': 1,
    'backend/internal/service/gateway_forward_as_chat_completions_test.go': 5,
    'backend/internal/service/gateway_forward_as_responses_test.go': 7,
    'backend/internal/service/openai_compat_model_test.go': 27,
    'backend/internal/service/openai_fast_service_tier_test.go': 1,
}
HELPER = 'backend/internal/service/account_test_service_openai_test.go'


def adapt(path, text):
    if path == HELPER:
        old = 'func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {\n\tgin.SetMode(gin.TestMode)\n'
        new = '''// Establish the helper's constant mode before any tests execute. The helper
// is also called by parallel tests and must not mutate Gin's global mode.
func init() {
	gin.SetMode(gin.TestMode)
}

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
'''
        if text.count(old) != 1:
            raise ValueError('expected one Gin helper setup')
        return text.replace(old, new)
    if path not in COUNTS:
        raise ValueError('unreviewed Gin mode fixture')
    changed = 0

    def move(match):
        nonlocal changed
        body = match[0]
        # Only top-level parallel tests: nested-only parallel cases are untouched.
        if '\n\tt.Parallel()\n' not in body or 'gin.SetMode' not in body:
            return body
        count = body.count('gin.SetMode(gin.TestMode)')
        expected = 3 if path.endswith('/openai_fast_service_tier_test.go') else 1
        if count != expected:
            raise ValueError('unexpected Gin setup count')
        body, removed = re.subn(r'^\t+gin.SetMode\(gin.TestMode\)\n', '', body, flags=re.M)
        if removed != expected:
            raise ValueError('unexpected Gin setup shape')
        body = body.replace('\n\tt.Parallel()\n', '\n\tgin.SetMode(gin.TestMode)\n\tt.Parallel()\n', 1)
        changed += 1
        return body

    text = re.sub(r'(?ms)^func Test\w+\(t \*testing.T\) \{.*?(?=^func |\Z)', move, text)
    if changed != COUNTS[path]:
        raise ValueError(f'expected {COUNTS[path]} setup moves, got {changed}')
    return text
