package dispatch

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DefaultLoadSlotTTLSeconds = 900
	MaxLoadSlotTTLSeconds     = 86400
	MaxAccountActive          = 100000
)

type AccountLoad struct {
	AccountID int64 `json:"account_id"`
	Active    int   `json:"active"`
}

type LoadSample struct {
	ObservedAt time.Time     `json:"observed_at"`
	Accounts   map[int64]int `json:"accounts"`
}

type LoadReader interface {
	Read(context.Context, []int64) (LoadSample, error)
}

type RedisLoadReader struct {
	client         redis.UniversalClient
	slotTTLSeconds int
}

// NewRedisLoadReader requires the same slot TTL as the native Core cache.
// Zero selects the cache constructor's default; application configuration may differ.
func NewRedisLoadReader(client redis.UniversalClient, slotTTLSeconds int) (*RedisLoadReader, error) {
	if client == nil || (reflect.ValueOf(client).Kind() == reflect.Pointer && reflect.ValueOf(client).IsNil()) {
		return nil, errors.New("dispatch load Redis client is required")
	}
	if slotTTLSeconds == 0 {
		slotTTLSeconds = DefaultLoadSlotTTLSeconds
	}
	if slotTTLSeconds < 1 || slotTTLSeconds > MaxLoadSlotTTLSeconds {
		return nil, errors.New("dispatch load slot TTL must be between 1 and 86400 seconds")
	}
	return &RedisLoadReader{client: client, slotTTLSeconds: slotTTLSeconds}, nil
}

// Read observes native slots without reaping or refreshing them. Counts share one
// Redis time cutoff, but concurrent acquisitions/releases are not an atomic snapshot.
func (r *RedisLoadReader) Read(ctx context.Context, accountIDs []int64) (LoadSample, error) {
	if r == nil || r.client == nil || ctx == nil {
		return LoadSample{}, errors.New("dispatch load reader and context are required")
	}
	if err := ctx.Err(); err != nil {
		return LoadSample{}, err
	}
	if len(accountIDs) == 0 || len(accountIDs) > MaxCandidates {
		return LoadSample{}, errors.New("dispatch load requires between 1 and 1000 unique accounts")
	}
	ids := append([]int64(nil), accountIDs...)
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return LoadSample{}, errors.New("dispatch load account IDs must be positive and unique")
		}
		seen[id] = true
	}

	now, err := r.client.Time(ctx).Result()
	if err != nil {
		return LoadSample{}, fmt.Errorf("dispatch load Redis TIME: %w", err)
	}
	if now.IsZero() || now.Unix() < int64(r.slotTTLSeconds) || now.Year() > 9999 {
		return LoadSample{}, errors.New("dispatch load Redis TIME is out of range")
	}
	regularMin := "(" + strconv.FormatInt(now.Unix()-int64(r.slotTTLSeconds), 10)
	liveMin := "(" + strconv.FormatInt(now.Unix()-60, 10)
	counts := make([][2]*redis.IntCmd, len(ids))
	commands, err := r.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for i, id := range ids {
			key := strconv.FormatInt(id, 10)
			counts[i][0] = pipe.ZCount(ctx, "concurrency:account:"+key, regularMin, "+inf")
			counts[i][1] = pipe.ZCount(ctx, "concurrency:live:account:"+key, liveMin, "+inf")
		}
		return nil
	})
	if err != nil {
		return LoadSample{}, fmt.Errorf("dispatch load Redis ZCOUNT: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return LoadSample{}, err
	}
	if len(commands) != 2*len(ids) {
		return LoadSample{}, errors.New("dispatch load Redis response is incomplete")
	}
	accounts := make(map[int64]int, len(ids))
	for i, id := range ids {
		var active int64
		for j, cmd := range counts[i] {
			if cmd == nil || commands[2*i+j] != cmd {
				return LoadSample{}, errors.New("dispatch load Redis response is incomplete")
			}
			count, err := cmd.Result()
			if err != nil {
				return LoadSample{}, fmt.Errorf("dispatch load Redis ZCOUNT: %w", err)
			}
			if count < 0 || count > MaxAccountActive || active > MaxAccountActive-count {
				return LoadSample{}, errors.New("dispatch load account count is out of range")
			}
			active += count
		}
		accounts[id] = int(active)
	}
	return LoadSample{ObservedAt: now.UTC(), Accounts: accounts}, nil
}
