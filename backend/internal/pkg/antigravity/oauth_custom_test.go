package antigravity

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A subprocess is required: these credentials are read during package init.
func TestCustomOAuthStartupCredentials(t *testing.T) {
	if os.Getenv("SUB2API_CUSTOM_OAUTH_TEST_CHILD") == "1" {
		if ClientID != strings.TrimSpace(os.Getenv(AntigravityOAuthClientIDEnv)) {
			t.Fatal("client ID must come only from the startup environment")
		}
		if defaultClientSecret != os.Getenv(AntigravityOAuthClientSecretEnv) {
			t.Fatal("client secret must come only from the startup environment")
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate current test executable: %v", err)
	}
	for _, injected := range []bool{false, true} {
		name := "empty"
		if injected {
			name = "injected"
		}
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(executable, "-test.run=^TestCustomOAuthStartupCredentials$")
			for _, entry := range os.Environ() {
				if strings.HasPrefix(entry, AntigravityOAuthClientIDEnv+"=") || strings.HasPrefix(entry, AntigravityOAuthClientSecretEnv+"=") || strings.HasPrefix(entry, "SUB2API_CUSTOM_OAUTH_TEST_CHILD=") {
					continue
				}
				cmd.Env = append(cmd.Env, entry)
			}
			cmd.Env = append(cmd.Env, "SUB2API_CUSTOM_OAUTH_TEST_CHILD=1")
			if injected {
				cmd.Env = append(cmd.Env, AntigravityOAuthClientIDEnv+"= custom-test-client ", AntigravityOAuthClientSecretEnv+"=synthetic-test-only")
			}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("credential startup regression: %v\n%s", err, output)
			}
		})
	}
}
