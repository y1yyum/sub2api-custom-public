//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The shared harness prefix hook does not handle ZCOUNT; extend it locally.
type dispatchLoadPrefixHook struct{ prefixHook }

func (h dispatchLoadPrefixHook) prefixCount(cmd redis.Cmder) {
	if cmd.Name() == "zcount" && len(cmd.Args()) > 1 {
		key, ok := cmd.Args()[1].(string)
		if ok && !strings.HasPrefix(key, h.prefix) {
			cmd.Args()[1] = h.prefix + key
		}
	}
}

func (h dispatchLoadPrefixHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	process := h.prefixHook.ProcessHook(next)
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.prefixCount(cmd)
		return process(ctx, cmd)
	}
}

func (h dispatchLoadPrefixHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	process := h.prefixHook.ProcessPipelineHook(next)
	return func(ctx context.Context, commands []redis.Cmder) error {
		for _, cmd := range commands {
			h.prefixCount(cmd)
		}
		return process(ctx, commands)
	}
}

func dispatchLoadRedis(t *testing.T) (*redis.Client, string) {
	t.Helper()
	prefix := fmt.Sprintf("it:%s:%d:%d:", sanitizeRedisNamespace(t.Name()), time.Now().UnixNano(), atomic.AddUint64(&redisNamespaceSeq, 1))
	opts := *integrationRedis.Options()
	opts.ContextTimeoutEnabled = true
	rdb := redis.NewClient(&opts)
	rdb.AddHook(dispatchLoadPrefixHook{prefixHook{prefix: prefix}})
	t.Cleanup(func() {
		require.NoError(t, rdb.Close())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := integrationRedis.Scan(ctx, cursor, prefix+"*", 500).Result()
			require.NoError(t, err)
			if len(keys) > 0 {
				require.NoError(t, integrationRedis.Unlink(ctx, keys...).Err())
			}
			if next == 0 {
				break
			}
			cursor = next
		}
	})
	return rdb, prefix
}

func TestDispatchLoadNativeRegularAndLiveSlots(t *testing.T) {
	ctx := t.Context()
	rdb, _ := dispatchLoadRedis(t)
	cache := NewConcurrencyCache(rdb, 15, 900).(*concurrencyCache)
	reader, err := dispatch.NewRedisLoadReader(rdb, 0)
	require.NoError(t, err)
	for _, request := range []string{"ordinary-request", "internal-request"} {
		ok, err := cache.AcquireAccountSlot(ctx, 11, 10, request)
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := cache.AcquireLiveLease(ctx, 11, 10, 21, 10, 31, "live-request", false)
	require.NoError(t, err)
	require.True(t, ok)
	before, err := rdb.Time(ctx).Result()
	require.NoError(t, err)
	sample, err := reader.Read(ctx, []int64{11, 12})
	require.NoError(t, err)
	after, err := rdb.Time(ctx).Result()
	require.NoError(t, err)
	require.False(t, sample.ObservedAt.Before(before))
	require.False(t, sample.ObservedAt.After(after))
	require.Equal(t, map[int64]int{11: 3, 12: 0}, sample.Accounts)
	native, err := cache.GetAccountConcurrency(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, native, sample.Accounts[11])

	duplicate, err := reader.Read(ctx, []int64{11, 11})
	require.Error(t, err, "aliases must be deduplicated by the caller")
	require.Equal(t, dispatch.LoadSample{}, duplicate)
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 11, "ordinary-request"))
	sample, err = reader.Read(ctx, []int64{11})
	require.NoError(t, err)
	require.Equal(t, 2, sample.Accounts[11])
	require.NoError(t, cache.ReleaseLiveLease(ctx, 11, 21, 31, "live-request"))

	restarted, err := dispatch.NewRedisLoadReader(rdb, 900)
	require.NoError(t, err)
	sample, err = restarted.Read(ctx, []int64{11})
	require.NoError(t, err)
	require.Equal(t, 1, sample.Accounts[11], "an unreleased native slot survives reader restart")
}

func TestDispatchLoadExpiryBoundariesDoNotMutateKeys(t *testing.T) {
	ctx := t.Context()
	rdb, _ := dispatchLoadRedis(t)
	now, err := rdb.Time(ctx).Result()
	require.NoError(t, err)
	regular := []redis.Z{
		{Score: float64(now.Unix() - 901), Member: "expired"},
		{Score: float64(now.Unix() - 900), Member: "boundary"},
		{Score: float64(now.Unix() - 899), Member: "near-boundary"},
		{Score: float64(now.Unix() - 100), Member: "crashed-unreleased"},
		{Score: float64(now.Unix()), Member: "active"},
	}
	live := []redis.Z{
		{Score: float64(now.Unix() - 61), Member: "expired"},
		{Score: float64(now.Unix() - 60), Member: "boundary"},
		{Score: float64(now.Unix() - 59), Member: "near-boundary"},
		{Score: float64(now.Unix()), Member: "active"},
	}
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(11), regular...).Err())
	require.NoError(t, rdb.ZAdd(ctx, liveAccountSlotKey(11), live...).Err())
	reader, err := dispatch.NewRedisLoadReader(rdb, 900)
	require.NoError(t, err)
	sample, err := reader.Read(ctx, []int64{11})
	require.NoError(t, err)
	want := 0
	for _, slot := range regular {
		if slot.Score > float64(sample.ObservedAt.Unix()-900) {
			want++
		}
	}
	for _, slot := range live {
		if slot.Score > float64(sample.ObservedAt.Unix()-60) {
			want++
		}
	}
	require.Equal(t, want, sample.Accounts[11])
	for key, slots := range map[string][]redis.Z{accountSlotKey(11): regular, liveAccountSlotKey(11): live} {
		actual, err := rdb.ZRangeWithScores(ctx, key, 0, -1).Result()
		require.NoError(t, err)
		require.ElementsMatch(t, slots, actual, "read must retain expired members and scores")
		ttl, err := rdb.TTL(ctx, key).Result()
		require.NoError(t, err)
		require.Equal(t, time.Duration(-1), ttl, "read must not add a key TTL")
	}

	short, err := dispatch.NewRedisLoadReader(rdb, 1)
	require.NoError(t, err)
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(12), redis.Z{Score: float64(now.Unix() - 2), Member: "old"}).Err())
	sample, err = short.Read(ctx, []int64{12})
	require.NoError(t, err)
	require.Zero(t, sample.Accounts[12])
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(13), redis.Z{Score: float64(now.Unix()), Member: "expiring-key"}).Err())
	require.NoError(t, rdb.PExpire(ctx, accountSlotKey(13), time.Millisecond).Err())
	require.Eventually(t, func() bool {
		count, err := rdb.Exists(ctx, accountSlotKey(13)).Result()
		return err == nil && count == 0
	}, time.Second, 10*time.Millisecond)
	sample, err = reader.Read(ctx, []int64{13})
	require.NoError(t, err)
	require.Equal(t, map[int64]int{13: 0}, sample.Accounts)
}

func TestDispatchLoadUsesActualThirtyMinuteCoreTTL(t *testing.T) {
	ctx := t.Context()
	rdb, _ := dispatchLoadRedis(t)
	cache := NewConcurrencyCache(rdb, 30, 1800)
	ok, err := cache.AcquireAccountSlot(ctx, 11, 2, "long-running-native")
	require.NoError(t, err)
	require.True(t, ok)
	now, err := rdb.Time(ctx).Result()
	require.NoError(t, err)
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(11), redis.Z{
		Score: float64(now.Unix() - 1000), Member: "long-running-native",
	}).Err())
	reader, err := dispatch.NewRedisLoadReader(rdb, 1800)
	require.NoError(t, err)
	sample, err := reader.Read(ctx, []int64{11})
	require.NoError(t, err)
	require.Equal(t, 1, sample.Accounts[11])
	native, err := cache.GetAccountConcurrency(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, 1, native)

	lowLevelDefault, err := dispatch.NewRedisLoadReader(rdb, 0)
	require.NoError(t, err)
	sample, err = lowLevelDefault.Read(ctx, []int64{11})
	require.NoError(t, err)
	require.Zero(t, sample.Accounts[11], "900s cache fallback is not the Core application default")
	sample, err = reader.Read(ctx, []int64{11})
	require.NoError(t, err)
	require.Equal(t, 1, sample.Accounts[11], "a shorter read must not reap the native slot")
}

func TestDispatchLoadConcurrentNativeSlots(t *testing.T) {
	ctx := t.Context()
	rdb, _ := dispatchLoadRedis(t)
	cache := NewConcurrencyCache(rdb, 15, 900)
	const slots = 16
	errorsCh := make(chan error, slots)
	var workers sync.WaitGroup
	for i := range slots {
		workers.Go(func() {
			ok, err := cache.AcquireAccountSlot(ctx, 11, slots, fmt.Sprintf("request-%d", i))
			if err == nil && !ok {
				err = errors.New("native slot unexpectedly denied")
			}
			errorsCh <- err
		})
	}
	workers.Wait()
	for range slots {
		require.NoError(t, <-errorsCh)
	}
	reader, err := dispatch.NewRedisLoadReader(rdb, 900)
	require.NoError(t, err)
	for range slots {
		workers.Go(func() {
			sample, err := reader.Read(ctx, []int64{11})
			if err == nil && sample.Accounts[11] != slots {
				err = fmt.Errorf("native load = %d, want %d", sample.Accounts[11], slots)
			}
			errorsCh <- err
		})
	}
	workers.Wait()
	for range slots {
		require.NoError(t, <-errorsCh)
	}
	for i := range slots {
		require.NoError(t, cache.ReleaseAccountSlot(ctx, 11, fmt.Sprintf("request-%d", i)))
	}
	sample, err := reader.Read(ctx, []int64{11})
	require.NoError(t, err)
	require.Equal(t, map[int64]int{11: 0}, sample.Accounts)
}

func TestDispatchLoadRestrictedACL(t *testing.T) {
	ctx := t.Context()
	rdb, prefix := dispatchLoadRedis(t)
	now, err := rdb.Time(ctx).Result()
	require.NoError(t, err)
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(11), redis.Z{Score: float64(now.Unix()), Member: "native"}).Err())
	user := fmt.Sprintf("dispatch-load-%d", atomic.AddUint64(&redisNamespaceSeq, 1))
	password := "synthetic-" + prefix
	require.NoError(t, integrationRedis.Do(ctx, "ACL", "SETUSER", user, "reset", "on", ">"+password,
		"~"+prefix+accountSlotKey(11), "~"+prefix+liveAccountSlotKey(11), "-@all", "+time", "+zcount").Err())
	t.Cleanup(func() {
		require.NoError(t, integrationRedis.Do(context.Background(), "ACL", "DELUSER", user).Err())
	})
	opts := *integrationRedis.Options()
	opts.Username, opts.Password = user, password
	opts.Protocol, opts.PoolSize = 2, 1
	opts.DisableIdentity, opts.ContextTimeoutEnabled = true, true
	limited := redis.NewClient(&opts)
	limited.AddHook(dispatchLoadPrefixHook{prefixHook{prefix: prefix}})
	t.Cleanup(func() { require.NoError(t, limited.Close()) })
	reader, err := dispatch.NewRedisLoadReader(limited, 900)
	require.NoError(t, err)
	sample, err := reader.Read(ctx, []int64{11})
	require.NoError(t, err, "fresh ACL client needs only TIME and ZCOUNT after authentication")
	require.Equal(t, map[int64]int{11: 1}, sample.Accounts)
	for _, denied := range []error{
		limited.Ping(ctx).Err(), limited.Eval(ctx, "return 1", nil).Err(),
		limited.ZAdd(ctx, accountSlotKey(11), redis.Z{Score: float64(now.Unix()), Member: "forbidden"}).Err(),
	} {
		require.ErrorContains(t, denied, "NOPERM")
	}
	sample, err = reader.Read(ctx, []int64{11, 12})
	require.ErrorContains(t, err, "NOPERM")
	require.Equal(t, dispatch.LoadSample{}, sample, "a denied account cannot produce a partial map")
	for _, command := range []string{"time", "zcount"} {
		require.NoError(t, integrationRedis.Do(ctx, "ACL", "SETUSER", user, "-"+command).Err())
		sample, err = reader.Read(ctx, []int64{11})
		require.ErrorContains(t, err, "NOPERM")
		require.Equal(t, dispatch.LoadSample{}, sample)
		require.NoError(t, integrationRedis.Do(ctx, "ACL", "SETUSER", user, "+"+command).Err())
	}
}

func TestDispatchLoadRejectsWrongTypeAndCapacityOverflow(t *testing.T) {
	ctx := t.Context()
	rdb, _ := dispatchLoadRedis(t)
	reader, err := dispatch.NewRedisLoadReader(rdb, 900)
	require.NoError(t, err)
	require.NoError(t, rdb.Set(ctx, accountSlotKey(11), "not-a-sorted-set", 0).Err())
	sample, err := reader.Read(ctx, []int64{12, 11})
	require.ErrorContains(t, err, "WRONGTYPE")
	require.Equal(t, dispatch.LoadSample{}, sample)
	require.NoError(t, rdb.Del(ctx, accountSlotKey(11)).Err())
	now, err := rdb.Time(ctx).Result()
	require.NoError(t, err)
	for _, key := range []string{accountSlotKey(11), liveAccountSlotKey(11)} {
		for start := 0; start < 50000; start += 1000 {
			members := make([]redis.Z, 1000)
			for i := range members {
				members[i] = redis.Z{Score: float64(now.Unix()), Member: fmt.Sprintf("slot-%d", start+i)}
			}
			require.NoError(t, rdb.ZAdd(ctx, key, members...).Err())
		}
	}
	sample, err = reader.Read(ctx, []int64{11})
	require.NoError(t, err)
	require.Equal(t, 100000, sample.Accounts[11])
	require.NoError(t, rdb.ZAdd(ctx, liveAccountSlotKey(11), redis.Z{Score: float64(now.Unix()), Member: "overflow"}).Err())
	sample, err = reader.Read(ctx, []int64{12, 11})
	require.Error(t, err)
	require.Equal(t, dispatch.LoadSample{}, sample)
}

func TestDispatchLoadConnectionLossIsNotZero(t *testing.T) {
	ctx := t.Context()
	rdb, prefix := dispatchLoadRedis(t)
	cache := NewConcurrencyCache(rdb, 15, 900)
	ok, err := cache.AcquireAccountSlot(ctx, 11, 2, "native-request")
	require.NoError(t, err)
	require.True(t, ok)
	var mu sync.Mutex
	var connections []net.Conn
	var disconnected bool
	opts := *integrationRedis.Options()
	opts.Protocol, opts.PoolSize, opts.MaxRetries = 2, 1, -1
	opts.DialerRetries = -1
	opts.ContextTimeoutEnabled, opts.DisableIdentity = true, true
	opts.Dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		if disconnected {
			return nil, errors.New("integration transport disconnected")
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err == nil {
			connections = append(connections, conn)
		}
		return conn, err
	}
	client := redis.NewClient(&opts)
	client.AddHook(dispatchLoadPrefixHook{prefixHook{prefix: prefix}})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	reader, err := dispatch.NewRedisLoadReader(client, 900)
	require.NoError(t, err)
	sample, err := reader.Read(ctx, []int64{11})
	require.NoError(t, err)
	require.Equal(t, 1, sample.Accounts[11])
	mu.Lock()
	disconnected = true
	for _, conn := range connections {
		_ = conn.Close()
	}
	mu.Unlock()
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	sample, err = reader.Read(readCtx, []int64{11})
	require.Error(t, err)
	require.Equal(t, dispatch.LoadSample{}, sample)
	native, err := cache.GetAccountConcurrency(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, 1, native, "connection loss must not release or hide the native slot")
}
