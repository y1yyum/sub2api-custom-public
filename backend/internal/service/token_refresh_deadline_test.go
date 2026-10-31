//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOAuthRefreshAPI_PendingDeadlineRejectsBothCredentialWrites(t *testing.T) {
	for _, platform := range []string{PlatformGemini, PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := pendingRefreshDeadlineContext{context.Background(), time.Now().Add(10 * time.Millisecond)}
				account := &Account{
					ID: 201, Platform: platform, Type: AccountTypeOAuth, Status: StatusActive,
					Credentials: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh"},
				}
				repo := &refreshAPIAccountRepo{account: account}
				executor := &refreshAPIExecutorStub{
					needsRefresh: true, delay: 20 * time.Millisecond,
					credentials: map[string]any{"access_token": "late-access", "refresh_token": "late-refresh"},
				}

				result, err := NewOAuthRefreshAPI(repo, nil).RefreshIfNeeded(ctx, account, executor, time.Hour)

				require.NoError(t, ctx.Err(), "deadline delivery must still be pending")
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Nil(t, result)
				require.Equal(t, 1, executor.refreshCalls)
				require.Zero(t, repo.successCASCalls)
				require.Zero(t, repo.updateCalls)
				require.Zero(t, repo.updateCredentialsCalls)
				require.Equal(t, "old-refresh", account.Credentials["refresh_token"])
			})
		})
	}
}

func TestTokenRefreshService_ParentBoundaryPrecedesAttemptFailure(t *testing.T) {
	for _, unified := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			name := "legacy/deadline"
			if unified {
				name = "unified/deadline"
			}
			if canceled {
				name += "/canceled"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					parent, cancel := context.WithCancelCause(context.Background())
					defer cancel(nil)
					ctx := pendingRefreshDeadlineContext{parent, time.Now().Add(10 * time.Millisecond)}
					cause := errors.New("parent cycle stopped")
					account := &Account{
						ID: 202, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive,
						Credentials: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh"},
					}
					repo := &tokenRefreshAccountRepo{}
					repo.accountsByID = map[int64]*Account{account.ID: account}
					svc := &TokenRefreshService{
						accountRepo: repo, refreshPolicy: DefaultBackgroundRefreshPolicy(),
						cfg:                    &config.TokenRefreshConfig{MaxRetries: 3, ProviderFailureThreshold: 1},
						attemptTimeoutOverride: time.Hour,
					}
					if unified {
						svc.refreshAPI = NewOAuthRefreshAPI(repo, nil)
					}
					executor := &refreshAPIExecutorStub{
						needsRefresh: true, delay: 20 * time.Millisecond,
						credentials: map[string]any{"access_token": "late-access", "refresh_token": "late-refresh"},
						onRefresh: func() {
							if canceled {
								cancel(cause)
							}
						},
					}
					state := &tokenRefreshProviderState{service: svc}

					err := svc.refreshWithRetryWithRateGate(ctx, account, executor, executor, time.Hour, nil)
					state.recordResult(err)

					want := context.DeadlineExceeded
					if canceled {
						want = context.Canceled
						require.ErrorIs(t, context.Cause(parent), cause)
					} else {
						require.NoError(t, ctx.Err(), "deadline delivery must still be pending")
					}
					require.ErrorIs(t, err, want)
					var attemptErr *refreshAttemptTimeoutError
					require.False(t, errors.As(err, &attemptErr), "a parent boundary is not an attempt failure")
					require.Equal(t, 1, executor.refreshCalls, "parent termination must not retry")
					require.Zero(t, repo.updateCalls)
					require.Zero(t, repo.conditionalSuccessCalls)
					require.Zero(t, repo.conditionalErrorCalls)
					require.Zero(t, repo.conditionalTempCalls)
					require.Zero(t, repo.setErrorCalls)
					require.Zero(t, repo.setTempUnschedCalls)
					require.Zero(t, state.consecutiveFailures)
					require.False(t, state.isTripped())
					require.Equal(t, "old-refresh", account.GetGrokRefreshToken())
				})
			})
		}
	}
}

func TestTokenRefreshService_PendingParentDeadlinePreventsUpstreamCall(t *testing.T) {
	ctx := pendingRefreshDeadlineContext{context.Background(), time.Now().Add(-time.Hour)}
	account := &Account{ID: 204, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive}
	repo := &tokenRefreshAccountRepo{}
	svc := &TokenRefreshService{
		accountRepo: repo, refreshPolicy: DefaultBackgroundRefreshPolicy(),
		cfg: &config.TokenRefreshConfig{MaxRetries: 3},
	}
	executor := &refreshAPIExecutorStub{needsRefresh: true}

	err := svc.refreshWithRetry(ctx, account, executor, executor, time.Hour)

	require.NoError(t, ctx.Err())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, executor.refreshCalls)
	require.Zero(t, repo.updateCalls)
	require.Zero(t, repo.conditionalTempCalls)
	require.Zero(t, repo.setTempUnschedCalls)
}

func TestTokenRefreshService_PendingParentDeadlineAfterCASStillSynchronizesCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := pendingRefreshDeadlineContext{context.Background(), time.Now().Add(10 * time.Millisecond)}
		account := &Account{
			ID: 203, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive,
			Credentials: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh"},
		}
		repo := &tokenRefreshAccountRepo{snapshotReads: true, durableReadDelay: 30 * time.Millisecond}
		repo.accountsByID = map[int64]*Account{account.ID: account}
		invalidator := &tokenCacheInvalidatorStub{}
		scheduler := &tokenRefreshSchedulerCache{}
		svc := &TokenRefreshService{
			accountRepo: repo, refreshAPI: NewOAuthRefreshAPI(repo, nil),
			refreshPolicy:    DefaultBackgroundRefreshPolicy(),
			cfg:              &config.TokenRefreshConfig{MaxRetries: 3, ProviderFailureThreshold: 1},
			cacheInvalidator: invalidator, schedulerCache: scheduler,
			attemptTimeoutOverride: time.Hour,
		}
		executor := &tokenRefresherStub{credentials: map[string]any{
			"access_token": "provider-access", "refresh_token": "provider-refresh",
		}}
		state := &tokenRefreshProviderState{service: svc}

		err := svc.refreshWithRetry(ctx, account, executor, executor, time.Hour)
		state.recordResult(err)

		require.NoError(t, ctx.Err(), "deadline delivery must still be pending")
		require.ErrorIs(t, err, context.DeadlineExceeded)
		var attemptErr *refreshAttemptTimeoutError
		require.False(t, errors.As(err, &attemptErr))
		require.Equal(t, 1, executor.calls)
		require.Equal(t, 1, repo.conditionalSuccessCalls)
		require.Equal(t, "provider-refresh", repo.accountsByID[account.ID].GetGrokRefreshToken())
		require.Equal(t, 1, invalidator.calls)
		require.NoError(t, invalidator.ctxErr)
		require.Equal(t, 1, scheduler.setAccountCalls)
		require.NoError(t, scheduler.ctxErr)
		require.Zero(t, repo.conditionalTempCalls)
		require.Zero(t, repo.setTempUnschedCalls)
		require.Zero(t, state.consecutiveFailures)
		require.False(t, state.isTripped())
	})
}
