package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchFailureClassRequiresTrustedMatchingOutcome(t *testing.T) {
	target := testTarget("a", 1)
	now := time.Now().UTC()
	for _, class := range []string{"", FailureQuotaExhausted, FailureCredentialBanned, FailureCapabilityDenied, "invalid"} {
		for _, outcome := range []Outcome{Success, UpstreamAuth, UpstreamRateLimit, UpstreamFailure, Unknown, LocalRejection, ClientCanceled} {
			for _, evidence := range []bool{false, true} {
				o := testObservation(target, now, outcome)
				o.ProviderEvidence, o.FailureClass = evidence, class
				want := class == "" || evidence && (class == FailureQuotaExhausted && outcome == UpstreamRateLimit ||
					(class == FailureCredentialBanned || class == FailureCapabilityDenied) && outcome == UpstreamAuth)
				require.Equal(t, want, validateObservation(o, target, now) == nil, "%s/%s/%v", class, outcome, evidence)
			}
		}
	}
	o := testObservation(target, now, UpstreamAuth)
	o.FailureClass = FailureCapabilityDenied
	h := InitialHealth(target, now)
	require.Error(t, h.Apply(target, o, now, false), "scoped failures must not fall into legacy auth propagation")
	require.False(t, h.AuthBlocked)
	o.FailureClass = ""
	body, err := json.Marshal(o)
	require.NoError(t, err)
	require.NotContains(t, string(body), "failure_class")
}

func TestDispatchFailureScopeAndQuotaMetadata(t *testing.T) {
	a := testTarget("a", 1)
	b := a
	b.Ref, b.AccountID, b.Model, b.Region, b.CredentialRevision = "alias", 2, "other", "other", 2
	scope := failureScope(FailureCredentialBanned, a)
	require.True(t, scope.matches(b), "credential ban spans accounts/models/regions/revisions")
	b.ProviderRef = "other"
	require.False(t, scope.matches(b))
	scope = failureScope(FailureCapabilityDenied, a)
	b = a
	b.CredentialRef, b.CredentialRevision = "other", 2
	require.True(t, scope.matches(b))
	b.Model = "other"
	require.False(t, scope.matches(b))
	scope = failureScope(FailureQuotaExhausted, a)
	b = a
	b.QuotaRef = "new-quota"
	require.True(t, scope.matches(b), "unconfigured quota hold remains an account domain")
	a.QuotaRef, b.QuotaRef, b.AccountID = "shared", "shared", 2
	scope = failureScope(FailureQuotaExhausted, a)
	require.True(t, scope.matches(b))
	b.ProviderRef = "other"
	require.False(t, scope.matches(b))
	a.QuotaRef = "bad/ref"
	require.Error(t, a.validate())
	a.QuotaRef = ""
	body, err := json.Marshal(a)
	require.NoError(t, err)
	require.NotContains(t, string(body), "quota_ref")
	before, err := runtimeConfigDigest(Config{Targets: []Target{a}}, ProbeLimits{ConfigRevision: 1})
	require.NoError(t, err)
	a.QuotaRef = "shared"
	after, err := runtimeConfigDigest(Config{Targets: []Target{a}}, ProbeLimits{ConfigRevision: 1})
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestDispatchHoldReleaseRequestValidationAndStrictFile(t *testing.T) {
	request := HoldReleaseRequest{CoreInstance: "core", ConfigRevision: 1, HoldID: "hold:abc", HoldRevision: 1,
		OperationID: "op:1", Actor: "operator", Reason: "已核验并补充额度", EvidenceRef: "ticket:228"}
	require.NoError(t, request.Validate())
	withTask := request
	withTask.Reason = "task-228 resolved"
	require.NoError(t, withTask.Validate())
	for _, change := range []func(*HoldReleaseRequest){
		func(r *HoldReleaseRequest) { r.CoreInstance = "" }, func(r *HoldReleaseRequest) { r.ConfigRevision = 0 },
		func(r *HoldReleaseRequest) { r.HoldID = "bad/id" }, func(r *HoldReleaseRequest) { r.HoldRevision = 0 },
		func(r *HoldReleaseRequest) { r.OperationID = strings.Repeat("a", 129) }, func(r *HoldReleaseRequest) { r.Actor = "name surname" },
		func(r *HoldReleaseRequest) { r.EvidenceRef = "" }, func(r *HoldReleaseRequest) { r.Reason = "  " },
		func(r *HoldReleaseRequest) { r.Reason = "bad\nreason" }, func(r *HoldReleaseRequest) { r.Reason = strings.Repeat("a", 513) },
		func(r *HoldReleaseRequest) { r.Reason = "bad\u2028reason" }, func(r *HoldReleaseRequest) { r.Reason = string([]byte{0xff}) },
	} {
		invalid := request
		change(&invalid)
		require.Error(t, invalid.Validate())
	}
	path := filepath.Join(t.TempDir(), "request.json")
	body, err := json.Marshal(request)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0600))
	got, err := LoadHoldReleaseRequest(path)
	require.NoError(t, err)
	require.Equal(t, request, got)
	for _, bad := range []string{
		strings.TrimSuffix(string(body), "}") + `,"actor":"other"}`,
		strings.Replace(string(body), `"actor"`, `"Actor"`, 1),
		strings.TrimSuffix(string(body), "}") + `,"unknown":true}`,
		string(body) + string(body), strings.Repeat(" ", (16<<10)+1),
		`{}`, string([]byte{0xff}),
	} {
		require.NoError(t, os.WriteFile(path, []byte(bad), 0600))
		_, err := LoadHoldReleaseRequest(path)
		require.Error(t, err)
	}
}
