"""Reviewed test adaptations for deliberately different custom contracts.

Upstream test input hashes are pinned in manifest.json. This file contains no
upstream OAuth credential literals. All other upstream assertions are retained.
"""
import re
from pathlib import Path
import runpy


def replace_once(text, pattern, replacement):
    text, count = re.subn(pattern, lambda _: replacement, text, flags=re.S)
    if count != 1:
        raise ValueError(f'adaptation anchor expected once, got {count}')
    return text


def adapt(path, text):
    if path == 'backend/internal/pkg/antigravity/oauth_test.go':
        start = text.index('func TestConstants_')
        before, section = text[:start], text[start:]
        section = replace_once(section, r'\tif ClientID != .*?(?=\tif RedirectURI !=)', '''
	// Custom distribution intentionally has no built-in credentials.
	oldID, oldSecret := ClientID, defaultClientSecret
	t.Cleanup(func() { ClientID, defaultClientSecret = oldID, oldSecret })
	ClientID, defaultClientSecret = "", ""
	if secret, err := getClientSecret(); err == nil || secret != "" {
		t.Fatal("missing runtime secret must fail closed")
	}
	ClientID, defaultClientSecret = "custom-test-client", "synthetic-test-only"
	if secret, err := getClientSecret(); err != nil || secret != "synthetic-test-only" {
		t.Fatal("runtime-injected secret must be used")
	}
''')
        return before + section
    if path == 'backend/internal/pkg/geminicli/oauth_test.go':
        # Keep scope and URL assertions, but feed the runtime-injection seam.
        text = re.sub(r'\bGeminiCLIOAuthClientID\b', '"custom-test-client"', text)
        text = re.sub(r'(func Test\w+\(t \*testing.T\) \{)',
                      r'\1\n\tt.Setenv(GeminiCLIOAuthClientIDEnv, "custom-test-client")', text)
        text = replace_once(text, r'func TestBuildAuthorizationURL_UsesBuiltinSecretFallback\(t \*testing.T\) \{.*?\n\}', '''func TestBuildAuthorizationURL_RequiresRuntimeSecret(t *testing.T) {
	t.Setenv(GeminiCLIOAuthClientIDEnv, "custom-test-client")
	t.Setenv(GeminiCLIOAuthClientSecretEnv, "")
	authURL, err := BuildAuthorizationURL(OAuthConfig{}, "test-state", "test-challenge", "https://example.com/callback", "", "code_assist")
	if err == nil || authURL != "" {
		t.Fatal("missing runtime secret must not produce an authorization URL")
	}
}''')
        text = replace_once(text, r'func TestEffectiveOAuthConfig_NoEnvSecret\(t \*testing.T\) \{.*?\n\}', '''func TestEffectiveOAuthConfig_NoEnvSecret(t *testing.T) {
	t.Setenv(GeminiCLIOAuthClientIDEnv, "custom-test-client")
	t.Setenv(GeminiCLIOAuthClientSecretEnv, "")
	cfg, err := EffectiveOAuthConfig(OAuthConfig{}, "code_assist")
	if err == nil || cfg.ClientID != "" || cfg.ClientSecret != "" {
		t.Fatal("missing runtime secret must fail closed without built-in credentials")
	}
}''')
        return text
    if path == 'backend/internal/service/gemini_oauth_service_test.go':
        start = text.index('func TestGeminiOAuthService_GenerateAuthURL_RedirectURIStrategy(')
        end = text.index('\nfunc TestValidateTierID(', start)
        section = text[start:end]
        setup = '\tt.Setenv(geminicli.GeminiCLIOAuthClientSecretEnv, "test-built-in-secret")'
        if section.count(setup) != 1 or section.count('geminicli.GeminiCLIOAuthClientID') != 3:
            raise ValueError('unexpected Gemini service authorization URL fixture')
        section = section.replace(setup, setup + '\n\tt.Setenv(geminicli.GeminiCLIOAuthClientIDEnv, "custom-test-client")')
        section = re.sub(r'\bgeminicli\.GeminiCLIOAuthClientID\b', '"custom-test-client"', section)
        return text[:start] + section + text[end:]
    if path == 'backend/internal/server/api_contract_test.go':
        anchor = '"openai_fast_policy_settings": {'
        if text.count(anchor) != 2:
            raise ValueError('expected two admin-settings contract fixtures')
        return text.replace(anchor, '"unified_model_channel_status_enabled": false,\n\t\t\t\t\t' + anchor)
    if path in {'backend/internal/repository/integration_harness_test.go', 'backend/internal/server/routes/auth_rate_limit_integration_test.go', 'backend/internal/securityaudit/prompt_repository_integration_test.go', 'backend/internal/middleware/rate_limiter_integration_test.go'}:
        fixture = Path(__file__).with_name('adapt-integration-fixtures.py')
        return runpy.run_path(str(fixture))['adapt'](path, text)
    if path.startswith('frontend/'):
        frontend = Path(__file__).with_name('adapt-frontend-tests.py')
        return runpy.run_path(str(frontend))['adapt'](path, text)
    raise ValueError('unreviewed test adaptation: '+path)
