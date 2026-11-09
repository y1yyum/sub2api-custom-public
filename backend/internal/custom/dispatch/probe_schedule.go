package dispatch

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"sort"
	"strconv"
	"time"
)

func probeDueAt(core string, target Target, health Health, policy ProbeScheduleConfig, lastReservedAt, now time.Time) time.Time {
	if health.State == Tripped || health.State == Probing {
		due := health.CoolUntil
		if health.Backoff > 0 && health.Rolling != nil &&
			!health.Rolling.CooldownStartedAt.IsZero() && health.Rolling.CooldownUntil.Equal(health.CoolUntil) {
			span := health.CoolUntil.Sub(health.Rolling.CooldownStartedAt)
			room := max(time.Duration(0), 10*time.Minute-span)
			limit := min(max(time.Duration(0), span)/10, room)
			due = due.Add(probeScheduleJitter(core, target, health.CoolUntil, health.Backoff, limit))
		}
		// A future reservation remains a fence after database clock rollback.
		if lastReservedAt.After(now) {
			due = maxProbeTime(due, lastReservedAt.Add(30*time.Second))
		}
		return due
	}
	if lastReservedAt.IsZero() {
		return health.CoolUntil
	}
	interval := time.Duration(policy.IdleIntervalSeconds) * time.Second
	if (health.State == Healthy || health.State == Degraded) && health.RecentPassiveSamples(now) >= policy.PassiveMinSamples {
		interval = time.Duration(policy.BusyIntervalSeconds) * time.Second
	}
	due := lastReservedAt.Add(interval).Add(probeScheduleJitter(core, target, lastReservedAt, 0, interval/10))
	return maxProbeTime(health.CoolUntil, due)
}

func probeScheduleJitter(core string, target Target, point time.Time, backoff int, limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	seed := core + "\x00" + strconv.FormatInt(target.AccountID, 10) + "\x00" + target.Model + "\x00" + target.Region +
		"\x00" + point.UTC().Format(time.RFC3339Nano) + "\x00" + strconv.Itoa(backoff)
	digest := sha256.Sum256([]byte(seed))
	return time.Duration(binary.BigEndian.Uint64(digest[:8]) % (uint64(limit) + 1))
}

func maxProbeTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// ProbeOrder rotates native domains by UTC day and slot, independently of polling.
func ProbeOrder(targets []Target, core string, now time.Time) []Target {
	if len(targets) < 2 {
		return slices.Clone(targets)
	}
	type domain struct {
		accountID     int64
		model, region string
	}
	groups := make(map[domain][]Target)
	for _, target := range targets {
		key := domain{target.AccountID, target.Model, target.Region}
		groups[key] = append(groups[key], target)
	}
	domains := make([]domain, 0, len(groups))
	for key, aliases := range groups {
		sort.Slice(aliases, func(i, j int) bool { return aliases[i].Ref < aliases[j].Ref })
		domains = append(domains, key)
	}
	sort.Slice(domains, func(i, j int) bool {
		a, b := domains[i], domains[j]
		if a.accountID != b.accountID {
			return a.accountID < b.accountID
		}
		if a.model != b.model {
			return a.model < b.model
		}
		return a.region < b.region
	})
	digest := sha256.Sum256([]byte(core))
	size := int64(len(domains))
	day, withinDay := now.Unix()/86400, now.Unix()%86400
	if withinDay < 0 {
		day--
		withinDay += 86400
	}
	offset := (int64(binary.BigEndian.Uint64(digest[:8])%uint64(size)) + day%size + (withinDay/30)%size + size) % size
	ordered := make([]Target, 0, len(targets))
	for i := range domains {
		ordered = append(ordered, groups[domains[(int(offset)+i)%len(domains)]]...)
	}
	return ordered
}
