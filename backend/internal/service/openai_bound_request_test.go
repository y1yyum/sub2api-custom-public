//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const boundRequestGroupID int64 = 4202

func boundRequestAccounts() []Account {
	accounts := make([]Account, 2)
	for i := range accounts {
		accounts[i] = Account{
			ID: int64(20201 + i), Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Status: StatusActive, Schedulable: true, Concurrency: 2,
			GroupIDs:    []int64{boundRequestGroupID},
			Credentials: map[string]any{"api_key": "fixture", "base_url": "https://provider.example.test"},
			Extra:       map[string]any{"openai_responses_supported": true},
		}
	}
	return accounts
}

func boundRequestContext() context.Context {
	return custombridge.WithRequest(context.Background(), custombridge.Request{
		ReservationID: "reservation-A", AttemptID: "attempt-A", AccountID: 20201,
	})
}

func boundRequestService(accounts []Account) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		cfg:         &config.Config{RunMode: config.RunModeStandard}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
}

func selectBoundRequestFixture(svc *OpenAIGatewayService, ctx context.Context, excluded map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	groupID := boundRequestGroupID
	return svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "resp_owned_by_B", "sticky-B", "gpt-5.4", excluded,
		OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, true)
}

func TestBoundRequestIgnoresAffinityAndSchedulerToggle(t *testing.T) {
	for _, enabled := range []string{"true", "false"} {
		t.Run(enabled, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
			svc := boundRequestService(boundRequestAccounts())
			svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService(enabled)
			svc.cache = &schedulerTestGatewayCache{}
			groupID := boundRequestGroupID
			require.NoError(t, svc.BindStickySession(context.Background(), &groupID, "sticky-B", 20202))
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(context.Background(), boundRequestGroupID, "resp_owned_by_B", 20202, time.Hour))
			selection, decision, err := selectBoundRequestFixture(svc, boundRequestContext(), nil)
			require.NoError(t, err)
			require.Equal(t, int64(20201), selection.Account.ID)
			require.Equal(t, "bound_request", decision.Layer)
			require.Equal(t, 1, decision.CandidateCount)
			selection.ReleaseFunc()
			// 普通请求仍命中 B；受信实验没有改写原有黏性绑定。
			ordinary, _, err := selectBoundRequestFixture(svc, context.Background(), nil)
			require.NoError(t, err)
			require.Equal(t, int64(20202), ordinary.Account.ID)
			ordinary.ReleaseFunc()
		})
	}
}

func TestBoundRequestPreservesEligibility(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Account)
	}{
		{"disabled", func(a *Account) { a.Status = StatusDisabled }},
		{"unschedulable", func(a *Account) { a.Schedulable = false }},
		{"wrong_group", func(a *Account) { a.GroupIDs = []int64{boundRequestGroupID + 1} }},
		{"wrong_platform", func(a *Account) { a.Platform = PlatformAnthropic }},
		{"model", func(a *Account) {
			a.Credentials["model_mapping"] = map[string]any{"different-model": "different-model"}
		}},
		{"capability", func(a *Account) { a.Extra["openai_responses_supported"] = false }},
		{"rate_limited", func(a *Account) { until := time.Now().Add(time.Hour); a.RateLimitResetAt = &until }},
		{"quota", func(a *Account) {
			a.Extra["codex_5h_used_percent"] = 100.0
			a.Extra["auto_pause_5h_threshold"] = 0.9
			a.Extra["codex_usage_updated_at"] = time.Now().Format(time.RFC3339)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			accounts := boundRequestAccounts()
			test.change(&accounts[0])
			selection, _, err := selectBoundRequestFixture(boundRequestService(accounts), boundRequestContext(), nil)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, selection, "healthy B must never replace rejected A")
		})
	}
	t.Run("profit", func(t *testing.T) {
		accounts := boundRequestAccounts()
		profitControlTestAccountWithRate(&accounts[0], 0.9)
		profitControlTestAccountWithRate(&accounts[1], 0.1)
		ctx := custombridge.WithRequest(profitControlTestCtx(profitControlTestGroup(boundRequestGroupID, 0.5, 0)), custombridge.Request{AccountID: 20201})
		selection, _, err := selectBoundRequestFixture(boundRequestService(accounts), ctx, nil)
		require.ErrorIs(t, err, ErrNoAvailableAccounts)
		require.Nil(t, selection)
	})
	t.Run("failover_excluded", func(t *testing.T) {
		selection, _, err := selectBoundRequestFixture(boundRequestService(boundRequestAccounts()), boundRequestContext(), map[int64]struct{}{20201: {}})
		require.ErrorIs(t, err, ErrNoAvailableAccounts)
		require.Nil(t, selection)
	})
}

func TestBoundRequestConcurrencyWaitRemainsOnA(t *testing.T) {
	svc := boundRequestService(boundRequestAccounts())
	var acquired []int64
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{20201: false, 20202: true}, acquiredIDs: &acquired,
	})
	selection, _, err := selectBoundRequestFixture(svc, boundRequestContext(), nil)
	require.NoError(t, err)
	require.False(t, selection.Acquired)
	require.Equal(t, int64(20201), selection.Account.ID)
	require.Equal(t, int64(20201), selection.WaitPlan.AccountID)
	require.Equal(t, 2, selection.WaitPlan.MaxConcurrency)
	require.NotEmpty(t, acquired)
	for _, id := range acquired {
		require.Equal(t, int64(20201), id)
	}
}

type boundRequestLatestRepo struct {
	schedulerGroupAwareOpenAIAccountRepo
	latest *Account
	err    error
}

func (r boundRequestLatestRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.latest, r.err
}

func TestBoundRequestDBRecheckCannotFallback(t *testing.T) {
	for _, outcome := range []string{"disabled", "different_account", "different_group", "db_error"} {
		for _, snapshot := range []bool{false, true} {
			t.Run(outcome+"/snapshot_"+strconv.FormatBool(snapshot), func(t *testing.T) {
				accounts := boundRequestAccounts()
				latest := accounts[0]
				var dbErr error
				switch outcome {
				case "disabled":
					latest.Status = StatusDisabled
				case "different_account":
					latest = accounts[1]
				case "different_group":
					latest.GroupIDs = []int64{boundRequestGroupID + 1}
				case "db_error":
					dbErr = errors.New("fixture database failure")
				}
				repo := boundRequestLatestRepo{schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}, &latest, dbErr}
				svc := boundRequestService(accounts)
				svc.accountRepo = repo
				if snapshot {
					svc.schedulerSnapshot = &SchedulerSnapshotService{
						cache:       &openAISnapshotCacheStub{snapshotAccounts: []*Account{&accounts[0], &accounts[1]}, accountsByID: map[int64]*Account{20201: &accounts[0], 20202: &accounts[1]}},
						accountRepo: repo, groupRepo: guardianAffinityGroupRepo{group: &Group{ID: boundRequestGroupID}},
					}
				}
				var acquired, released []int64
				svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired, releasedIDs: &released})
				selection, _, err := selectBoundRequestFixture(svc, boundRequestContext(), nil)
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				require.Nil(t, selection)
				require.Equal(t, []int64{20201, 20201}, acquired, "the scheduler retries A after refreshing load")
				require.Equal(t, acquired, released, "each acquired A slot must be released exactly once")
			})
		}
	}
}

func TestBoundRequestRejectsSimpleMode(t *testing.T) {
	svc := boundRequestService(boundRequestAccounts())
	svc.cfg.RunMode = config.RunModeSimple
	selection, _, err := selectBoundRequestFixture(svc, boundRequestContext(), nil)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := boundRequestAccounts()[0]
	result, err := svc.Forward(boundRequestContext(), c, &account, []byte(`{"model":"gpt-5.4"}`))
	require.ErrorIs(t, err, ErrOpenAIBoundRequestRejected)
	require.Nil(t, result)
}

func TestBoundRequestPrivacyCannotUseEligibleB(t *testing.T) {
	accounts := boundRequestAccounts()
	accounts[1].Extra["privacy_mode"] = PrivacyModeTrainingOff
	svc := boundRequestService(accounts)
	groupID := boundRequestGroupID
	scheduler := newDefaultOpenAIAccountScheduler(svc, nil)
	selection, _, err := scheduler.Select(boundRequestContext(), OpenAIAccountScheduleRequest{
		GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.4", RequirePrivacySet: true,
		RequiredTransport: OpenAIUpstreamTransportHTTPSSE, RequiredCapability: OpenAIEndpointCapabilityResponses,
	})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
}

type boundRequestRevokedBeforeSendRepo struct {
	boundRequestLatestRepo
	reads int
}

func (r *boundRequestRevokedBeforeSendRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	latest := *r.latest
	if r.reads > 1 {
		latest.Status = StatusDisabled
	}
	return &latest, nil
}

func TestBoundRequestRechecksImmediatelyBeforeHTTP(t *testing.T) {
	accounts := boundRequestAccounts()
	repo := &boundRequestRevokedBeforeSendRepo{boundRequestLatestRepo: boundRequestLatestRepo{latest: &accounts[0]}}
	svc := boundRequestService(accounts)
	svc.accountRepo = repo
	upstream := &httpUpstreamRecorder{}
	svc.httpUpstream = upstream
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(boundRequestContext())
	groupID := boundRequestGroupID
	c.Set("api_key", &APIKey{ID: 1802, GroupID: &groupID})
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	result, err := svc.Forward(c.Request.Context(), c, &accounts[0], []byte(`{"model":"gpt-5.4","input":"hello"}`))
	require.ErrorIs(t, err, ErrOpenAIBoundRequestRejected)
	require.Nil(t, result)
	require.Equal(t, 2, repo.reads, "entry admission passes, then the final pre-send check sees revocation")
	require.Empty(t, upstream.requests, "revocation must be observed before an HTTP request is sent")
}

func TestBoundRequestForwardRejectsBeforeTransport(t *testing.T) {
	for _, outcome := range []string{"different_account", "disabled", "concurrency_changed", "group_changed", "missing_scope", "passthrough", "oauth", "setup_token", "shadow", "latest_oauth", "latest_shadow", "client_unknown", "client_ws"} {
		t.Run(outcome, func(t *testing.T) {
			accounts := boundRequestAccounts()
			selected := accounts[0]
			latest := accounts[0]
			switch outcome {
			case "different_account":
				selected = accounts[1]
			case "disabled":
				latest.Status = StatusDisabled
			case "concurrency_changed":
				latest.Concurrency = 1
			case "group_changed":
				latest.GroupIDs = []int64{boundRequestGroupID + 1}
			case "passthrough":
				latest.Extra = map[string]any{"openai_passthrough": true, "openai_responses_supported": true}
			case "oauth":
				selected.Type, latest.Type = AccountTypeOAuth, AccountTypeOAuth
			case "setup_token":
				selected.Type, latest.Type = AccountTypeSetupToken, AccountTypeSetupToken
			case "shadow":
				parentID := int64(20202)
				selected.ParentAccountID, latest.ParentAccountID = &parentID, &parentID
			case "latest_oauth":
				latest.Type = AccountTypeOAuth
			case "latest_shadow":
				parentID := int64(20202)
				latest.ParentAccountID = &parentID
			}
			svc := boundRequestService(accounts)
			svc.accountRepo = boundRequestLatestRepo{schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}, &latest, nil}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(boundRequestContext())
			if outcome != "client_unknown" {
				transport := OpenAIClientTransportHTTP
				if outcome == "client_ws" {
					transport = OpenAIClientTransportWS
				}
				SetOpenAIClientTransport(c, transport)
			}
			if outcome != "missing_scope" {
				groupID := boundRequestGroupID
				c.Set("api_key", &APIKey{GroupID: &groupID})
			}
			result, err := svc.Forward(c.Request.Context(), c, &selected, []byte(`{"model":"gpt-5.4","input":"hi"}`))
			require.ErrorIs(t, err, ErrOpenAIBoundRequestRejected)
			require.Nil(t, result)
			// 未注入 HTTPUpstream：任何到达传输层的调用都会让本测试失败。
		})
	}
}

func TestBoundRequestRejectsImageModelsAndResolvedTools(t *testing.T) {
	for _, model := range []string{"gpt-image-1", "image-alias"} {
		t.Run(model, func(t *testing.T) {
			accounts := boundRequestAccounts()
			accounts[0].Credentials["model_mapping"] = map[string]any{"image-alias": "gpt-image-1", "gpt-image-1": "gpt-image-1"}
			svc := boundRequestService(accounts)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(boundRequestContext())
			groupID := boundRequestGroupID
			c.Set("api_key", &APIKey{ID: 1802, GroupID: &groupID, Group: &Group{AllowImageGeneration: true}})
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			result, err := svc.Forward(c.Request.Context(), c, &accounts[0], []byte(`{"model":"`+model+`","input":"draw"}`))
			require.ErrorIs(t, err, ErrOpenAIBoundRequestRejected)
			require.Nil(t, result)
		})
	}
	for _, body := range []string{`{"model":"gpt-5.4","input":"hello","tools":[{"type":"image_generation"}]}`, `{"model":"gpt-5.4","input":"hello","tools":[{"type":"web_search"}]}`, `{"model":"gpt-image-1","input":"draw"}`} {
		require.ErrorIs(t, validateBoundOpenAIResolvedBody(boundRequestContext(), []byte(body)), ErrOpenAIBoundRequestRejected)
		require.NoError(t, validateBoundOpenAIResolvedBody(context.Background(), []byte(body)))
	}
}

func TestBoundRequestExecutionDeadlineSurvivesClientCancellation(t *testing.T) {
	incoming, cancel := context.WithTimeout(boundRequestContext(), 80*time.Millisecond)
	defer cancel()
	deadline, ok := incoming.Deadline()
	require.True(t, ok)
	execution, release := boundOpenAIExecutionContext(incoming)
	defer release()
	cancel()
	require.NoError(t, execution.Err(), "the existing detached client-cancellation behavior is preserved")
	got, ok := execution.Deadline()
	require.True(t, ok)
	require.Equal(t, deadline, got)
	select {
	case <-execution.Done():
		require.ErrorIs(t, execution.Err(), context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("the signed execution budget was lost while detaching the upstream")
	}
}
