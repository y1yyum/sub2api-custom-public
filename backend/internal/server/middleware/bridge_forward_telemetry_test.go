//go:build unit

package middleware

import (
	"bytes"
	"crypto/tls"
	"database/sql/driver"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBridgeTelemetryObserverRequiresVerifiedIngress(t *testing.T) {
	verifier, key, envelope, body := bridgeForwardMiddlewareFixture(t)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	digest, err := custombridge.TelemetryCredentialSHA256(map[string]any{"api_key": "synthetic-telemetry"})
	require.NoError(t, err)
	cfg := custombridge.TelemetryConfig{SchemaVersion: 1, CoreInstance: "core", ConfigRevision: 1, Targets: []custombridge.TelemetryTarget{{
		TargetRef: "target", AccountID: envelope.AccountID, CredentialRef: "credential", CredentialRevision: 1, CredentialSHA256: digest,
		ProviderRef: "provider", PoolRef: envelope.PoolRef, PolicyRevision: envelope.PolicyRevision, Model: envelope.CanonicalModel, Region: "region",
	}}}
	mock.ExpectQuery("SELECT .* FROM bridge.dispatch_observation LIMIT 0").WillReturnRows(sqlmock.NewRows([]string{"event_id"}))
	mock.ExpectQuery("INSERT INTO bridge.dispatch_telemetry_source").WillReturnRows(sqlmock.NewRows([]string{"activated_at"}).AddRow(time.Now()))
	producer, err := custombridge.ActivateTelemetry(t.Context(), db, cfg, 1800)
	require.NoError(t, err)
	store := &bridgeForwardTestStore{requests: map[string]string{}, sent: map[string]bool{}, available: true}
	router := gin.New()
	router.Use(BridgeTelemetry(producer))
	router.POST("/v1/responses", func(c *gin.Context) {
		require.Nil(t, custombridge.PrepareTelemetryHTTP(c.Request.Context(), envelope.AccountID, nil))
		c.Status(http.StatusOK)
	})
	router.POST(custombridge.ForwardPath, ClientRequestID(), func(c *gin.Context) {
		groupID := key.GroupID
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: key.InternalKeyID, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI}})
	}, BridgeForward(verifier, store, false, 1024), func(c *gin.Context) {
		require.NotNil(t, custombridge.PrepareTelemetryHTTP(c.Request.Context(), envelope.AccountID, nil))
		// A locally generated status/header is never outbound provider evidence.
		c.Header("Retry-After", "120")
		c.Status(http.StatusServiceUnavailable)
	})
	ordinary := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	ordinary.Header.Set(custombridge.ForwardEnvelopeHeader, "user-controlled")
	router.ServeHTTP(httptest.NewRecorder(), ordinary)
	request := httptest.NewRequest(http.MethodPost, custombridge.ForwardPath, bytes.NewReader(body))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer synthetic-key")
	request.Header.Set("X-Bridge-Provider-Status", "401")
	denied := newBridgeForwardDeadlineRecorder()
	deniedRequest := request.Clone(t.Context())
	deniedRequest.Body = io.NopCloser(bytes.NewReader(body))
	router.ServeHTTP(denied, deniedRequest)
	require.Equal(t, http.StatusUnauthorized, denied.Code)
	require.Empty(t, store.requests)
	require.NoError(t, custombridge.SignForwardRequest(request, body, envelope, key))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT singleton FROM bridge.dispatch_telemetry_source").WillReturnRows(sqlmock.NewRows([]string{"singleton"}).AddRow(true))
	args := make([]driver.Value, 27)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	args[12] = "local_rejection"
	args[13] = false
	args[21] = int64(0)
	args[22] = ""
	args[23] = false
	// A local rejection has no outbound and therefore no protocol facts.
	args[24] = ""
	args[25] = ""
	args[26] = false
	mock.ExpectExec("INSERT INTO bridge.dispatch_observation").WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	accepted := newBridgeForwardDeadlineRecorder()
	router.ServeHTTP(accepted, request)
	require.Equal(t, http.StatusServiceUnavailable, accepted.Code)
	require.Len(t, store.requests, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}
