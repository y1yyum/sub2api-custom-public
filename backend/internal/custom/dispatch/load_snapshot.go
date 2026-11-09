package dispatch

import (
	"errors"
	"time"
)

func (s *Snapshot) applyLoad(sample LoadSample, maxAge time.Duration, now time.Time) error {
	if maxAge < time.Second || maxAge > 10*time.Second || sample.ObservedAt.IsZero() ||
		sample.ObservedAt.After(now.Add(5*time.Second)) || !sample.ObservedAt.Add(maxAge).After(now) {
		return errors.New("dispatch load sample is stale or invalid")
	}
	accounts := make(map[int64]bool)
	for i := range s.Candidates {
		candidate := &s.Candidates[i]
		active, ok := sample.Accounts[candidate.AccountID]
		if !ok && !candidate.Enabled {
			candidate.Active = 0
			continue
		}
		if !ok || active < 0 || active > 100000 {
			return errors.New("dispatch load sample is incomplete or invalid")
		}
		accounts[candidate.AccountID] = true
		candidate.Active = active
	}
	if len(accounts) != len(sample.Accounts) {
		return errors.New("dispatch load sample contains unexpected accounts")
	}
	s.LoadObservedAt = sample.ObservedAt.UTC()
	s.LoadExpiresAt = s.LoadObservedAt.Add(maxAge)
	s.ExpiresAt = minTime(s.ExpiresAt, s.LoadExpiresAt)
	return s.Validate(now)
}
