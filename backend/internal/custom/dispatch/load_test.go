package dispatch

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type loadTestHook struct {
	now       time.Time
	timeErr   error
	counts    []int64
	countErr  error
	pipeErr   error
	timeCalls int
	commands  [][]any
}

func (h *loadTestHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *loadTestHook) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		h.timeCalls++
		value, ok := cmd.(*redis.TimeCmd)
		if !ok || cmd.Name() != "time" {
			return errors.New("unexpected Redis command")
		}
		value.SetVal(h.now)
		value.SetErr(h.timeErr)
		return h.timeErr
	}
}

func (h *loadTestHook) ProcessPipelineHook(_ redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, commands []redis.Cmder) error {
		for i, cmd := range commands {
			h.commands = append(h.commands, append([]any(nil), cmd.Args()...))
			value, ok := cmd.(*redis.IntCmd)
			if !ok || cmd.Name() != "zcount" || i >= len(h.counts) {
				return errors.New("unexpected Redis pipeline command")
			}
			value.SetVal(h.counts[i])
			if i == len(commands)-1 {
				value.SetErr(h.countErr)
			}
		}
		return h.pipeErr
	}
}

func loadTestReader(t *testing.T, h *loadTestHook, ttl int) *RedisLoadReader {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	client.AddHook(h)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	reader, err := NewRedisLoadReader(client, ttl)
	require.NoError(t, err)
	return reader
}

func TestDispatchLoadConstructor(t *testing.T) {
	_, err := NewRedisLoadReader(nil, 900)
	require.Error(t, err)
	var client *redis.Client
	_, err = NewRedisLoadReader(client, 900)
	require.Error(t, err)
	for _, ttl := range []int{-1, 86401, math.MaxInt} {
		t.Run(time.Duration(ttl).String(), func(t *testing.T) {
			rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
			defer func() { require.NoError(t, rdb.Close()) }()
			_, err := NewRedisLoadReader(rdb, ttl)
			require.Error(t, err)
		})
	}
	for _, ttl := range []int{0, 1, 900, 1800, 86400} {
		h := &loadTestHook{}
		r := loadTestReader(t, h, ttl)
		if ttl == 0 {
			require.Equal(t, 900, r.slotTTLSeconds)
		} else {
			require.Equal(t, ttl, r.slotTTLSeconds)
		}
		require.Zero(t, h.timeCalls, "construction must not require PING or any other Redis command")
	}
}

func TestDispatchLoadReadUsesOneNativeClockAndReadOnlyCounts(t *testing.T) {
	now := time.Unix(1800000123, 654321000).In(time.FixedZone("test", 7200))
	lastID := strconv.FormatInt(math.MaxInt64, 10)
	h := &loadTestHook{now: now, counts: []int64{2, 3, 0, 0, 99999, 1}}
	r := loadTestReader(t, h, 1800)
	var reader LoadReader = r
	sample, err := reader.Read(t.Context(), []int64{9, 17, math.MaxInt64})
	require.NoError(t, err)
	require.Equal(t, now.UTC(), sample.ObservedAt)
	require.Equal(t, map[int64]int{9: 5, 17: 0, math.MaxInt64: 100000}, sample.Accounts)
	require.Equal(t, 1, h.timeCalls)
	require.Equal(t, [][]any{
		{"zcount", "concurrency:account:9", "(1799998323", "+inf"},
		{"zcount", "concurrency:live:account:9", "(1800000063", "+inf"},
		{"zcount", "concurrency:account:17", "(1799998323", "+inf"},
		{"zcount", "concurrency:live:account:17", "(1800000063", "+inf"},
		{"zcount", "concurrency:account:" + lastID, "(1799998323", "+inf"},
		{"zcount", "concurrency:live:account:" + lastID, "(1800000063", "+inf"},
	}, h.commands)
}

func TestDispatchLoadReadRejectsInvalidAccountsBeforeRedis(t *testing.T) {
	for name, ids := range map[string][]int64{
		"empty": nil, "zero": {0}, "negative": {-1}, "duplicate_alias": {7, 7},
		"too_many": make([]int64, MaxCandidates+1),
	} {
		t.Run(name, func(t *testing.T) {
			h := &loadTestHook{}
			r := loadTestReader(t, h, 900)
			sample, err := r.Read(t.Context(), ids)
			require.Error(t, err)
			require.Equal(t, LoadSample{}, sample)
			require.Zero(t, h.timeCalls)
		})
	}
}

func TestDispatchLoadReadMaximumAccountsAndFreshSamples(t *testing.T) {
	ids := make([]int64, MaxCandidates)
	counts := make([]int64, 2*MaxCandidates)
	for i := range ids {
		ids[i] = int64(i + 1)
		counts[2*i] = 1
	}
	h := &loadTestHook{now: time.Now(), counts: counts}
	r := loadTestReader(t, h, 1)
	first, err := r.Read(t.Context(), ids)
	require.NoError(t, err)
	require.Len(t, first.Accounts, MaxCandidates)
	for _, id := range ids {
		require.Equal(t, 1, first.Accounts[id])
	}
	first.Accounts[1] = 99
	h.now = h.now.Add(time.Second)
	h.counts[0] = 0
	second, err := r.Read(t.Context(), ids)
	require.NoError(t, err)
	require.Equal(t, 0, second.Accounts[1])
	require.Equal(t, 99, first.Accounts[1], "samples must not share their result maps")
	require.Equal(t, first.ObservedAt.Add(time.Second), second.ObservedAt)
	require.Equal(t, 2, h.timeCalls)
}

func TestDispatchLoadReadDoesNotConvertErrorsToZero(t *testing.T) {
	for name, h := range map[string]*loadTestHook{
		"time":              {timeErr: context.DeadlineExceeded},
		"zero_time":         {},
		"negative_time":     {now: time.Unix(-1, 0)},
		"time_out_of_range": {now: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
		"pipeline":          {now: time.Now(), counts: []int64{0, 0, 0, 0}, pipeErr: context.DeadlineExceeded},
		"count":             {now: time.Now(), counts: []int64{2, 1, 0, 0}, countErr: redis.Nil},
		"negative_count":    {now: time.Now(), counts: []int64{2, 1, -1, 0}},
		"regular_overflow":  {now: time.Now(), counts: []int64{2, 1, 100001, 0}},
		"live_overflow":     {now: time.Now(), counts: []int64{2, 1, 0, math.MaxInt64}},
		"sum_overflow":      {now: time.Now(), counts: []int64{2, 1, 99999, 2}},
	} {
		t.Run(name, func(t *testing.T) {
			r := loadTestReader(t, h, 900)
			sample, err := r.Read(t.Context(), []int64{1, 2})
			require.Error(t, err)
			require.Equal(t, LoadSample{}, sample, "never return a partial account map")
			if name == "time" || name == "pipeline" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
}

type incompleteLoadClient struct {
	redis.UniversalClient
	change func([]redis.Cmder) []redis.Cmder
}

func (c incompleteLoadClient) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	commands, err := c.UniversalClient.Pipelined(ctx, fn)
	return c.change(commands), err
}

func TestDispatchLoadReadRejectsIncompleteOrMismatchedResponses(t *testing.T) {
	for name, change := range map[string]func([]redis.Cmder) []redis.Cmder{
		"missing":   func(cmds []redis.Cmder) []redis.Cmder { return cmds[:1] },
		"extra":     func(cmds []redis.Cmder) []redis.Cmder { return append(cmds, cmds[0]) },
		"nil":       func(cmds []redis.Cmder) []redis.Cmder { cmds[1] = nil; return cmds },
		"reordered": func(cmds []redis.Cmder) []redis.Cmder { return []redis.Cmder{cmds[1], cmds[0]} },
	} {
		t.Run(name, func(t *testing.T) {
			h := &loadTestHook{now: time.Now(), counts: []int64{0, 0}}
			r := loadTestReader(t, h, 900)
			r.client = incompleteLoadClient{UniversalClient: r.client, change: change}
			sample, err := r.Read(t.Context(), []int64{7})
			require.Error(t, err)
			require.Equal(t, LoadSample{}, sample)
		})
	}
}

func TestDispatchLoadReadCancellation(t *testing.T) {
	h := &loadTestHook{}
	r := loadTestReader(t, h, 900)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	sample, err := r.Read(ctx, []int64{7})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, LoadSample{}, sample)
	require.Zero(t, h.timeCalls)
	var nilReader *RedisLoadReader
	_, err = nilReader.Read(t.Context(), []int64{7})
	require.Error(t, err)
}
