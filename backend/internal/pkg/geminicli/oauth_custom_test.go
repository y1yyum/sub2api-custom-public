package geminicli

import "testing"

func TestCustomOAuthRequiresBothRuntimeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, id, secret string
	}{
		{"empty", "", ""},
		{"missing-id", "", "synthetic-test-only"},
		{"missing-secret", "custom-test-client", ""},
		{"whitespace", "  ", "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(GeminiCLIOAuthClientIDEnv, tc.id)
			t.Setenv(GeminiCLIOAuthClientSecretEnv, tc.secret)
			cfg, err := EffectiveOAuthConfig(OAuthConfig{}, "code_assist")
			if err == nil || cfg.ClientID != "" || cfg.ClientSecret != "" {
				t.Fatal("missing runtime credentials must fail closed")
			}
		})
	}
}

func TestCustomOAuthRuntimeCredentialsAreTrimmed(t *testing.T) {
	t.Setenv(GeminiCLIOAuthClientIDEnv, " custom-test-client ")
	t.Setenv(GeminiCLIOAuthClientSecretEnv, " synthetic-test-only ")
	cfg, err := EffectiveOAuthConfig(OAuthConfig{}, "code_assist")
	if err != nil || cfg.ClientID != "custom-test-client" || cfg.ClientSecret != "synthetic-test-only" {
		t.Fatal("runtime-injected credential pair must be trimmed and used")
	}
}
