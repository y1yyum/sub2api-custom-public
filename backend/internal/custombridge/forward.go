package custombridge

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	ForwardPath                 = "/internal/bridge/v1/responses"
	ForwardEnvelopeHeader       = "X-Bridge-Envelope"
	ForwardSignatureHeader      = "X-Bridge-Signature"
	ForwardVersion              = 1
	MaxForwardLifetime          = 2 * time.Minute
	MaxForwardExecutionLifetime = 15 * time.Minute
)

var ErrForwardUnauthorized = errors.New("bridge forward authentication failed")
var forwardID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

type SigningKey struct {
	ID            string
	Secret        []byte
	InternalKeyID int64
	GroupID       int64
}

// ForwardEnvelope 的身份由服务配置授予，不从最终用户的桥接头推导。
type ForwardEnvelope struct {
	Version        int    `json:"schema_version"`
	KID            string `json:"kid"`
	Audience       string `json:"audience"`
	RequestID      string `json:"request_id"`
	ReservationID  string `json:"reservation_id"`
	AttemptID      string `json:"attempt_id"`
	InternalKeyID  int64  `json:"internal_key_id"`
	GroupID        int64  `json:"group_id"`
	AccountID      int64  `json:"account_id"`
	Endpoint       string `json:"endpoint"`
	CanonicalModel string `json:"canonical_model"`
	PayloadSHA256  string `json:"payload_sha256"`
	IssuedAt       int64  `json:"issued_at"`
	ExpiresAt      int64  `json:"expires_at"`
	Deadline       int64  `json:"deadline"`
	PolicyRevision string `json:"policy_revision"`
	PoolRef        string `json:"pool_ref"`
	PayerRef       string `json:"payer_ref"`
	ActorRef       string `json:"actor_ref"`
}

type ForwardPrincipal struct{ InternalKeyID, GroupID int64 }

type ForwardVerifier struct {
	audience string
	keys     map[string]SigningKey
}

func NewForwardVerifier(audience string, keys []SigningKey) (*ForwardVerifier, error) {
	if !forwardID.MatchString(audience) || len(keys) == 0 {
		return nil, errors.New("bridge audience and signing keys are required")
	}
	v := &ForwardVerifier{audience: audience, keys: make(map[string]SigningKey, len(keys))}
	for _, key := range keys {
		if !forwardID.MatchString(key.ID) || len(key.Secret) < 32 || key.InternalKeyID <= 0 || key.GroupID <= 0 {
			return nil, errors.New("invalid bridge signing key configuration")
		}
		if _, exists := v.keys[key.ID]; exists {
			return nil, errors.New("duplicate bridge signing key")
		}
		key.Secret = bytes.Clone(key.Secret)
		v.keys[key.ID] = key
	}
	return v, nil
}

func SignForwardRequest(request *http.Request, body []byte, envelope ForwardEnvelope, key SigningKey) error {
	if request == nil || len(key.Secret) < 32 {
		return errors.New("bridge signing key and request are required")
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(encoded)
	request.Header.Set(ForwardEnvelopeHeader, value)
	request.Header.Set(ForwardSignatureHeader, base64.RawURLEncoding.EncodeToString(forwardMAC(request, value, body, key.Secret)))
	return nil
}

func forwardMAC(request *http.Request, envelope string, body, secret []byte) []byte {
	bodyHash := sha256.Sum256(body)
	authHash := sha256.Sum256([]byte(request.Header.Get("Authorization")))
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "sub2api.forward.v1\n%s\n%s\n%x\n%x\n%s", request.Method, request.URL.RequestURI(), authHash, bodyHash, envelope)
	return mac.Sum(nil)
}

func (v *ForwardVerifier) Verify(request *http.Request, body []byte, principal ForwardPrincipal, now time.Time) (ForwardEnvelope, error) {
	var e ForwardEnvelope
	if request.Method != http.MethodPost || request.URL.EscapedPath() != ForwardPath || request.URL.RawQuery != "" || request.URL.ForceQuery ||
		len(request.Header.Values(ForwardEnvelopeHeader)) != 1 || len(request.Header.Values(ForwardSignatureHeader)) != 1 ||
		len(request.Header.Values("Authorization")) != 1 || !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") ||
		request.Header.Get("Content-Encoding") != "" || request.Header.Get("Upgrade") != "" {
		return e, ErrForwardUnauthorized
	}
	value := request.Header.Get(ForwardEnvelopeHeader)
	if len(value) > 8192 {
		return e, ErrForwardUnauthorized
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(raw, &e) != nil {
		return e, ErrForwardUnauthorized
	}
	canonical, err := json.Marshal(e)
	if err != nil || !bytes.Equal(raw, canonical) {
		return e, ErrForwardUnauthorized
	}
	key, ok := v.keys[e.KID]
	actual, err := base64.RawURLEncoding.DecodeString(request.Header.Get(ForwardSignatureHeader))
	if !ok || err != nil || !hmac.Equal(actual, forwardMAC(request, value, body, key.Secret)) {
		return e, ErrForwardUnauthorized
	}
	if e.Version != ForwardVersion || e.Audience != v.audience || e.Endpoint != "/v1/responses" || e.InternalKeyID != key.InternalKeyID || e.GroupID != key.GroupID ||
		principal.InternalKeyID != key.InternalKeyID || principal.GroupID != key.GroupID || e.AccountID <= 0 ||
		e.IssuedAt < now.Add(-MaxForwardLifetime).Unix() || e.IssuedAt > now.Add(5*time.Second).Unix() || e.ExpiresAt <= now.Unix() || e.ExpiresAt <= e.IssuedAt || e.ExpiresAt-e.IssuedAt > int64(MaxForwardLifetime/time.Second) ||
		e.Deadline < e.ExpiresAt || e.Deadline <= now.Unix() || e.Deadline > e.IssuedAt+int64(MaxForwardExecutionLifetime/time.Second) {
		return e, ErrForwardUnauthorized
	}
	for _, id := range []string{e.RequestID, e.ReservationID, e.AttemptID, e.PolicyRevision, e.PoolRef, e.PayerRef, e.ActorRef} {
		if !forwardID.MatchString(id) {
			return e, ErrForwardUnauthorized
		}
	}
	digest := sha256.Sum256(body)
	model, err := ValidateResponsesBody(body)
	if err != nil || e.PayloadSHA256 != hex.EncodeToString(digest[:]) || model != e.CanonicalModel {
		return e, ErrForwardUnauthorized
	}
	return e, nil
}

// ValidateResponsesBody 把 M1 能力限定为无工具、无状态续接的文本 Responses。
func ValidateResponsesBody(body []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return "", errors.New("bridge requires a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", errors.New("invalid bridge JSON")
		}
		name, ok := key.(string)
		if !ok {
			return "", errors.New("invalid bridge JSON field")
		}
		if _, duplicate := fields[name]; duplicate {
			return "", errors.New("duplicate bridge JSON field")
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return "", errors.New("invalid bridge JSON value")
		}
		fields[name] = value
	}
	if _, err = decoder.Token(); err != nil {
		return "", errors.New("invalid bridge JSON object")
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("trailing bridge JSON")
	}
	var model string
	if json.Unmarshal(fields["model"], &model) != nil || strings.TrimSpace(model) != model || model == "" || len(model) > 255 || strings.ContainsAny(model, "\r\n") {
		return "", errors.New("bridge model is required")
	}
	for _, field := range []string{"previous_response_id", "conversation", "background", "tools", "tool_choice", "include"} {
		if _, exists := fields[field]; exists {
			return "", errors.New("unsupported bridge Responses capability")
		}
	}
	if !bridgeTextInput(fields["input"]) {
		return "", errors.New("bridge accepts text input only")
	}
	return model, nil
}

func bridgeTextInput(raw json.RawMessage) bool {
	var text string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &text) == nil {
		return true
	}
	var messages []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return false
	}
	for _, message := range messages {
		if message.Type != "" && message.Type != "message" {
			return false
		}
		switch message.Role {
		case "user", "assistant", "system", "developer":
		default:
			return false
		}
		if len(message.Content) > 0 && message.Content[0] == '"' && json.Unmarshal(message.Content, &text) == nil {
			continue
		}
		var content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(message.Content, &content) != nil || len(content) == 0 {
			return false
		}
		for _, item := range content {
			if item.Type != "input_text" {
				return false
			}
		}
	}
	return true
}
