package custombridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func forwardFixture(t *testing.T) (*ForwardVerifier, SigningKey, ForwardEnvelope, []byte) {
	t.Helper()
	key := SigningKey{ID: "kid-1", Secret: bytes.Repeat([]byte{42}, 32), InternalKeyID: 12, GroupID: 21}
	verifier, err := NewForwardVerifier("core-test", []SigningKey{key})
	require.NoError(t, err)
	body := []byte(`{"model":"test-model","input":"hello","stream":true}`)
	digest := sha256.Sum256(body)
	now := time.Now().Unix()
	e := ForwardEnvelope{Version: ForwardVersion, KID: key.ID, Audience: "core-test", RequestID: "rid-1", ReservationID: "res-1", AttemptID: "attempt-1",
		InternalKeyID: key.InternalKeyID, GroupID: key.GroupID, AccountID: 31, Endpoint: "/v1/responses", CanonicalModel: "test-model",
		PayloadSHA256: hex.EncodeToString(digest[:]), IssuedAt: now, ExpiresAt: now + 60, Deadline: now + 300, PolicyRevision: "policy-1", PoolRef: "pool-1", PayerRef: "payer-1", ActorRef: "actor-1"}
	return verifier, key, e, body
}

func signedForwardFixture(t *testing.T, key SigningKey, e ForwardEnvelope, body []byte) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, ForwardPath, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer synthetic-service-key")
	require.NoError(t, SignForwardRequest(request, body, e, key))
	return request
}

func TestBridgeForwardSignatureAndIdentity(t *testing.T) {
	verifier, key, envelope, body := forwardFixture(t)
	principal := ForwardPrincipal{InternalKeyID: key.InternalKeyID, GroupID: key.GroupID}
	request := signedForwardFixture(t, key, envelope, body)
	got, err := verifier.Verify(request, body, principal, time.Now())
	require.NoError(t, err)
	require.Equal(t, envelope, got)
	for _, field := range []string{"unknown_kid", "version", "audience", "account", "key", "group", "model", "hash", "endpoint", "expired", "future", "lifetime", "execution_deadline", "actor", "reservation"} {
		t.Run(field, func(t *testing.T) {
			e := envelope
			switch field {
			case "unknown_kid":
				e.KID = "revoked"
			case "version":
				e.Version++
			case "audience":
				e.Audience = "other-core"
			case "account":
				e.AccountID = 0
			case "key":
				e.InternalKeyID++
			case "group":
				e.GroupID++
			case "model":
				e.CanonicalModel = "other-model"
			case "hash":
				e.PayloadSHA256 = strings.Repeat("0", 64)
			case "endpoint":
				e.Endpoint = "/v1/responses/compact"
			case "expired":
				e.IssuedAt -= 180
				e.ExpiresAt -= 180
			case "future":
				e.IssuedAt += 60
				e.ExpiresAt += 60
			case "lifetime":
				e.ExpiresAt += 600
			case "execution_deadline":
				e.Deadline = e.IssuedAt + int64(MaxForwardExecutionLifetime/time.Second) + 1
			case "actor":
				e.ActorRef = "bad\nactor"
			case "reservation":
				e.ReservationID = ""
			}
			_, err := verifier.Verify(signedForwardFixture(t, key, e, body), body, principal, time.Now())
			require.ErrorIs(t, err, ErrForwardUnauthorized)
		})
	}
	for _, field := range []string{"body", "authorization", "method", "path", "query", "encoding", "upgrade", "duplicate_signature", "signature", "wrong_principal", "wrong_group", "noncanonical", "duplicate_field"} {
		t.Run(field, func(t *testing.T) {
			r := signedForwardFixture(t, key, envelope, body)
			payload, identity := bytes.Clone(body), principal
			switch field {
			case "body":
				payload = []byte(`{"model":"test-model","input":"changed"}`)
			case "authorization":
				r.Header.Set("Authorization", "Bearer other-key")
			case "method":
				r.Method = http.MethodGet
			case "path":
				r.URL.Path = "/v1/responses"
			case "query":
				r.URL.RawQuery = "model=other"
			case "encoding":
				r.Header.Set("Content-Encoding", "gzip")
			case "upgrade":
				r.Header.Set("Upgrade", "websocket")
			case "duplicate_signature":
				r.Header.Add(ForwardSignatureHeader, r.Header.Get(ForwardSignatureHeader))
			case "signature":
				r.Header.Set(ForwardSignatureHeader, base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
			case "wrong_principal":
				identity.InternalKeyID++
			case "wrong_group":
				identity.GroupID++
			case "noncanonical", "duplicate_field":
				raw, err := json.Marshal(envelope)
				require.NoError(t, err)
				if field == "noncanonical" {
					raw = append([]byte(" "), raw...)
				} else {
					raw = bytes.Replace(raw, []byte(`{"schema_version":1,`), []byte(`{"schema_version":1,"schema_version":1,`), 1)
				}
				value := base64.RawURLEncoding.EncodeToString(raw)
				r.Header.Set(ForwardEnvelopeHeader, value)
				r.Header.Set(ForwardSignatureHeader, base64.RawURLEncoding.EncodeToString(forwardMAC(r, value, payload, key.Secret)))
			}
			_, err := verifier.Verify(r, payload, identity, time.Now())
			require.ErrorIs(t, err, ErrForwardUnauthorized)
		})
	}
}

func TestBridgeForwardKeyRotationAndStableDigest(t *testing.T) {
	_, first, e, body := forwardFixture(t)
	second := first
	second.ID = "kid-2"
	second.Secret = bytes.Repeat([]byte{73}, 32)
	verifier, err := NewForwardVerifier("core-test", []SigningKey{first, second})
	require.NoError(t, err)
	for _, key := range []SigningKey{first, second} {
		e.KID = key.ID
		_, err := verifier.Verify(signedForwardFixture(t, key, e, body), body, ForwardPrincipal{first.InternalKeyID, first.GroupID}, time.Now())
		require.NoError(t, err)
	}
	revoked, err := NewForwardVerifier("core-test", []SigningKey{second})
	require.NoError(t, err)
	e.KID = first.ID
	_, err = revoked.Verify(signedForwardFixture(t, first, e, body), body, ForwardPrincipal{first.InternalKeyID, first.GroupID}, time.Now())
	require.ErrorIs(t, err, ErrForwardUnauthorized)
	digest := ForwardDigest(e)
	e.KID, e.RequestID, e.IssuedAt, e.ExpiresAt = "kid-2", "another-rid", e.IssuedAt+1, e.ExpiresAt+1
	require.Equal(t, digest, ForwardDigest(e))
	for _, change := range []func(*ForwardEnvelope){func(e *ForwardEnvelope) { e.AccountID++ }, func(e *ForwardEnvelope) { e.PayloadSHA256 = "changed" }, func(e *ForwardEnvelope) { e.ActorRef = "other-actor" }, func(e *ForwardEnvelope) { e.PolicyRevision = "other-policy" }} {
		changed := e
		change(&changed)
		require.NotEqual(t, digest, ForwardDigest(changed))
	}
}

func TestBridgeForwardResponsesCapability(t *testing.T) {
	for _, body := range []string{`{"model":"m","input":"hello"}`, `{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`} {
		model, err := ValidateResponsesBody([]byte(body))
		require.NoError(t, err)
		require.Equal(t, "m", model)
	}
	for _, body := range []string{`[]`, `{"model":"m","model":"other","input":"hello"}`, `{"model":"m","input":"hello"} {}`, `{"model":"m","input":"hello","tools":[]}`, `{"model":"m","input":"hello","previous_response_id":"r"}`, `{"model":"m","input":[{"type":"function_call_output","call_id":"x","output":"x"}]}`, `{"model":"m","input":[{"role":"user","content":[{"type":"input_image","image_url":"x"}]}]}`} {
		_, err := ValidateResponsesBody([]byte(body))
		require.Error(t, err, body)
	}
}

func TestBridgeForwardConfigDisabledAndFailClosed(t *testing.T) {
	t.Setenv("SUB2API_BRIDGE_CONFIG", "nonexistent-config")
	for _, value := range []string{"", "false", "0"} {
		t.Setenv("SUB2API_BRIDGE_ENABLED", value)
		cfg, err := LoadForwardConfigFromEnv()
		require.NoError(t, err)
		require.Nil(t, cfg)
	}
	t.Setenv("SUB2API_BRIDGE_ENABLED", "true")
	_, err := LoadForwardConfigFromEnv()
	require.Error(t, err)
	_, key, _, _ := forwardFixture(t)
	file := filepath.Join(t.TempDir(), "bridge.json")
	encoded := `{"schema_version":1,"audience":"core-test","keys":[{"id":"kid-1","secret_base64":"` + base64.StdEncoding.EncodeToString(key.Secret) + `","internal_key_id":12,"group_id":21}]}`
	require.NoError(t, os.WriteFile(file, []byte(encoded), 0600))
	t.Setenv("SUB2API_BRIDGE_CONFIG", file)
	cfg, err := LoadForwardConfigFromEnv()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NoError(t, os.WriteFile(file, []byte(encoded+` {}`), 0600))
	_, err = LoadForwardConfigFromEnv()
	require.Error(t, err)
	require.NotContains(t, err.Error(), base64.StdEncoding.EncodeToString(key.Secret))
}
