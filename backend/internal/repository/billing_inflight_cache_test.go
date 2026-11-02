package repository

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newInflightTestEnv(t *testing.T, enabled bool, ttlSeconds int) (*miniredis.Miniredis, *billingCache, *service.BillingCacheService) {
	t.Helper()
	mr, cache := newInflightTestCache(t)
	return mr, cache, newInflightTestService(t, cache, enabled, ttlSeconds)
}

func newInflightTestCache(t *testing.T) (*miniredis.Miniredis, *billingCache) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, &billingCache{rdb: rdb}
}

func newInflightTestService(t *testing.T, cache service.BillingCache, enabled bool, ttlSeconds int) *service.BillingCacheService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{
		Enabled:          enabled,
		TTLSeconds:       ttlSeconds,
		DefaultMaxTokens: 8192,
	}
	svc := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	return svc
}

// inflightTestClock 是手动推进的时钟：预留是否过期只取决于测试推进了多少，与调度延迟无关。
type inflightTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *inflightTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *inflightTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// renewObservingCache 在每次成功续期后上报发起续期时的时钟读数。
type renewObservingCache struct {
	*billingCache
	clock   *inflightTestClock
	renewed chan time.Time
}

func (c *renewObservingCache) RenewInflightBalance(ctx context.Context, userID int64, requestID string, ttl time.Duration) (bool, error) {
	at := c.clock.Now()
	ok, err := c.billingCache.RenewInflightBalance(ctx, userID, requestID, ttl)
	if err == nil && ok {
		select {
		case c.renewed <- at:
		default:
		}
	}
	return ok, err
}

func waitInflightRenewalSince(t *testing.T, renewed <-chan time.Time, since time.Time) {
	t.Helper()
	hang := time.After(10 * time.Second)
	for {
		select {
		case at := <-renewed:
			if !at.Before(since) {
				return
			}
		case <-hang:
			t.Fatal("renewal goroutine stopped renewing")
		}
	}
}

func inflightCount(t *testing.T, cache *billingCache, userID int64) int64 {
	t.Helper()
	zkey, _ := billingInflightKeys(userID)
	n, err := cache.rdb.ZCard(context.Background(), zkey).Result()
	require.NoError(t, err)
	return n
}

func TestInflightReservation_ConcurrentAdmitsOnlyWhatBalanceCovers(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 42}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1.0))

	const n = 20
	var admitted, rejected atomic.Int32
	releases := make(chan func(), n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.3)
			if err != nil {
				if !errors.Is(err, service.ErrInsufficientBalance) {
					t.Errorf("unexpected error: %v", err)
				}
				rejected.Add(1)
				return
			}
			admitted.Add(1)
			releases <- release
		}()
	}
	close(start)
	wg.Wait()
	close(releases)

	// balance 1.0, estimate 0.3: first always admitted, then while 1.0 - inflight >= 0.3 → 3 total.
	require.Equal(t, int32(3), admitted.Load())
	require.Equal(t, int32(n-3), rejected.Load())
	require.Equal(t, int64(3), inflightCount(t, cache, user.ID))

	for release := range releases {
		release()
		release() // idempotent
	}
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))

	// After release, capacity is available again.
	release, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.3)
	require.NoError(t, err)
	release()
}

func TestInflightReservation_FirstRequestAlwaysAdmitted(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 7}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 0.01))

	release, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 5)
	require.NoError(t, err, "a lone request keeps legacy behavior even if estimate exceeds balance")
	_, err = svc.ReserveInflightBalance(ctx, user, nil, nil, 0.001)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	release()
	release2, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.001)
	require.NoError(t, err)
	release2()
}

func TestInflightReservation_TTLExpiryFreesLeakedReservation(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 1)
	ctx := context.Background()
	user := &service.User{ID: 9}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1.0))

	_, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.8) // leaked: never released
	require.NoError(t, err)
	_, err = svc.ReserveInflightBalance(ctx, user, nil, nil, 0.8)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)

	time.Sleep(1100 * time.Millisecond)
	release, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.8)
	require.NoError(t, err)
	require.Equal(t, int64(1), inflightCount(t, cache, user.ID))
	release()
}

func TestInflightReservation_DisabledKeepsLegacyBehavior(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, false, 60)
	ctx := context.Background()
	user := &service.User{ID: 11}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 0.01))
	require.False(t, svc.InflightReservationEnabled())
	for i := 0; i < 5; i++ {
		_, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 10)
		require.NoError(t, err)
	}
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}

func TestInflightReservation_SubscriptionUnaffected(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 12}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 0))
	group := &service.Group{ID: 1, SubscriptionType: service.SubscriptionTypeSubscription}
	sub := &service.UserSubscription{ID: 1}
	for i := 0; i < 5; i++ {
		_, err := svc.ReserveInflightBalance(ctx, user, group, sub, 10)
		require.NoError(t, err)
	}
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}

func TestInflightReservation_ZeroEstimateSkips(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 13}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1))
	_, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0)
	require.NoError(t, err)
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}

// downReserveCache 余额读取正常，但预留走一个已关闭的 Redis。
type downReserveCache struct {
	service.BillingCache
	down *billingCache
}

func (d *downReserveCache) ReserveInflightBalance(ctx context.Context, userID int64, requestID string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	return d.down.ReserveInflightBalance(ctx, userID, requestID, amount, balance, ttl)
}

func (d *downReserveCache) ReleaseInflightBalance(ctx context.Context, userID int64, requestID string) error {
	return d.down.ReleaseInflightBalance(ctx, userID, requestID)
}

func TestInflightReservation_RedisDownFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	up := &billingCache{rdb: rdb}
	require.NoError(t, up.SetUserBalance(context.Background(), 5, 0.01))

	downMR := miniredis.RunT(t)
	downRDB := redis.NewClient(&redis.Options{Addr: downMR.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = downRDB.Close() })
	downMR.Close()

	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	svc := service.NewBillingCacheService(&downReserveCache{BillingCache: up, down: &billingCache{rdb: downRDB}}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	user := &service.User{ID: 5}
	for i := 0; i < 3; i++ {
		release, err := svc.ReserveInflightBalance(context.Background(), user, nil, nil, 100)
		require.NoError(t, err)
		release()
	}
}

func TestInflightReservation_ScriptErrorSurfaces(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	cache := &billingCache{rdb: rdb}
	mr.Close()
	_, _, err := cache.ReserveInflightBalance(context.Background(), 1, "r", 1, 1, time.Second)
	require.Error(t, err)
	require.False(t, errors.Is(err, context.Canceled))
	_ = rdb.Close()
}

// 回归：原实现在 handler 返回时即释放预留，而计费（RecordUsage → 余额缓存扣减）是异步的，
// 窗口内的顺序请求看到「在途=0 且余额未扣」全部放行（余额 $1、5×$0.9 → -4.40）。
// 现在预留由计费任务在余额缓存扣减之后才释放。
func TestInflightReservation_SequentialInBillingWindowAdmitsOnlyWhatBalanceCovers(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 77}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1.0))

	const cost = 0.9
	var pendingBilling []func()
	admitted := 0
	for i := 0; i < 5; i++ {
		res, err := svc.ReserveInflight(ctx, user, nil, nil, cost)
		if err != nil {
			require.ErrorIs(t, err, service.ErrInsufficientBalance)
			continue
		}
		admitted++
		taskDone := res.Acquire() // handler submits the async billing task
		res.HandlerDone()         // handler returns before billing lands
		pendingBilling = append(pendingBilling, func() {
			require.NoError(t, cache.DeductUserBalance(ctx, user.ID, cost))
			taskDone()
		})
	}
	require.Equal(t, 1, admitted, "requests arriving before billing lands must not be admitted")

	for _, bill := range pendingBilling {
		bill()
	}
	bal, err := cache.GetUserBalance(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, 0.1, bal, 1e-9)
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}

// 过期由注入的时钟决定，每次推进都在观察到新一次续期之后且小于 TTL；
// 累计推进超过 2×TTL，未续期的预留必然已过期，续期是否及时与 CI 负载无关。
func TestInflightReservation_RenewalKeepsStreamingReservationAlive(t *testing.T) {
	const ttl = time.Second
	_, base := newInflightTestCache(t)
	clock := &inflightTestClock{now: time.Now()}
	base.now = clock.Now
	cache := &renewObservingCache{billingCache: base, clock: clock, renewed: make(chan time.Time, 16)}
	svc := newInflightTestService(t, cache, true, int(ttl/time.Second))
	ctx := context.Background()
	user := &service.User{ID: 78}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1.0))

	res, err := svc.ReserveInflight(ctx, user, nil, nil, 0.8)
	require.NoError(t, err)
	t.Cleanup(res.HandlerDone)

	start := clock.Now()
	for clock.Now().Sub(start) < 5*ttl/2 { // streaming well past the 1s TTL
		waitInflightRenewalSince(t, cache.renewed, clock.Now())
		clock.Advance(ttl * 4 / 5)
	}
	_, err = svc.ReserveInflight(ctx, user, nil, nil, 0.8)
	require.ErrorIs(t, err, service.ErrInsufficientBalance, "renewed reservation must still count")
	require.Equal(t, int64(1), inflightCount(t, base, user.ID))

	res.HandlerDone()
	require.Equal(t, int64(0), inflightCount(t, base, user.ID))

	ok, err := cache.RenewInflightBalance(ctx, user.ID, "missing", ttl)
	require.NoError(t, err)
	require.False(t, ok, "renew must not resurrect a released reservation")
}

func TestInflightReservation_RenewDoesNotResurrectExpiredMember(t *testing.T) {
	_, cache, _ := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	userID := int64(79)
	zkey, hkey := billingInflightKeys(userID)
	// 已过期但尚未被惰性清理的成员（score 在过去）。
	require.NoError(t, cache.rdb.ZAdd(ctx, zkey, redis.Z{Score: float64(time.Now().UnixMilli() - 1000), Member: "stale"}).Err())
	require.NoError(t, cache.rdb.HSet(ctx, hkey, "stale", "0.5").Err())

	ok, err := cache.RenewInflightBalance(ctx, userID, "stale", time.Minute)
	require.NoError(t, err)
	require.False(t, ok, "renew must not resurrect an expired member")
	require.Equal(t, int64(0), inflightCount(t, cache, userID))
	exists, err := cache.rdb.HExists(ctx, hkey, "stale").Result()
	require.NoError(t, err)
	require.False(t, exists)
}

func TestInflightScopeReservation_ReturnsOthersAndReleases(t *testing.T) {
	mr, cache, _ := newInflightTestEnv(t, true, 60)
	ctx := context.Background()

	others, err := cache.ReserveInflightScope(ctx, "apikey:7", "r1", 3, time.Minute)
	require.NoError(t, err)
	require.Zero(t, others)
	others, err = cache.ReserveInflightScope(ctx, "apikey:7", "r2", 2, time.Minute)
	require.NoError(t, err)
	require.InDelta(t, 3, others, 1e-9, "返回登记前其他在途预留的合计")
	others, err = cache.ReserveInflightScope(ctx, "sub:7", "r3", 5, time.Minute)
	require.NoError(t, err)
	require.Zero(t, others, "不同维度互不影响")

	require.NoError(t, cache.ReleaseInflightScope(ctx, "apikey:7", "r1"))
	require.NoError(t, cache.ReleaseInflightScope(ctx, "apikey:7", "r1"))
	others, err = cache.ReserveInflightScope(ctx, "apikey:7", "r4", 1, time.Minute)
	require.NoError(t, err)
	require.InDelta(t, 2, others, 1e-9)

	mr.FastForward(2 * time.Minute)
	others, err = cache.ReserveInflightScope(ctx, "apikey:7", "r5", 1, time.Minute)
	require.NoError(t, err)
	require.Zero(t, others, "过期预留不再计入")
}

func TestReserveFixedCharge_ConcurrentChargesAdmitOnlyWhatAPIKeyQuotaCovers(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 42}
	apiKey := &service.APIKey{ID: 9, UserID: user.ID, Quota: 1.0}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 100))

	const n = 20
	var admitted atomic.Int32
	reservations := make(chan *service.InflightReservation, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := svc.ReserveFixedCharge(ctx, user, apiKey, nil, nil, 0.3)
			if err != nil {
				if !errors.Is(err, service.ErrAPIKeyQuotaExhausted) {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			admitted.Add(1)
			reservations <- res
		}()
	}
	close(start)
	wg.Wait()
	close(reservations)

	require.Equal(t, int32(3), admitted.Load(), "Key 额度 1.0、每笔 0.3：同时在途的不超过 3 笔")
	for res := range reservations {
		res.Release()
	}
	zkey, _ := billingInflightScopeKeys("apikey:9")
	remaining, err := cache.rdb.ZCard(ctx, zkey).Result()
	require.NoError(t, err)
	require.Zero(t, remaining)
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}
