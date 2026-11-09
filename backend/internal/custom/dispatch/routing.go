package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

type routingWeightState struct {
	Key       string    `json:"key"`
	Factor    float64   `json:"factor"`
	UpdatedAt time.Time `json:"updated_at"`
}

func routingKey(cfg Config, target Target) string {
	body, _ := json.Marshal(struct {
		Version  int
		Core     string
		Revision int64
		Policy   RoutingConfig
		Target   Target
	}{1, cfg.CoreInstance, cfg.ConfigRevision, *cfg.Routing, target})
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func smoothRoutingFactor(previous, desired float64, elapsed time.Duration, seconds int) float64 {
	if elapsed <= 0 {
		return previous
	}
	// 超过 30 个时间常数已收敛；同时避免长时间停机导致时间计算溢出。
	if elapsed >= time.Duration(seconds)*30*time.Second {
		return desired
	}
	alpha := -math.Expm1(-elapsed.Seconds() / float64(seconds))
	return max(0, min(1, previous+(desired-previous)*alpha))
}

func (h *Health) routingCandidate(target Target, cfg Config, active int, now time.Time) Candidate {
	// 复用健康和恢复规则，显式移除 legacy 完整响应延迟乘数。
	health := *h
	health.LatencyMillis = 0
	candidate := health.Candidate(target)
	candidate.Active = active
	key := routingKey(cfg, target)
	desired := 1.0
	if h.hasFreshTTFT(now) {
		desired = max(0.25, min(1, float64(cfg.Routing.TTFTTargetMillis)/float64(h.TTFTMillis)))
	}
	desired *= max(0.05, 1-float64(active)/float64(target.MaxInflight))
	if candidate.EffectiveWeight == 0 || active >= target.MaxInflight {
		candidate.EffectiveWeight = 0
		h.Routing = &routingWeightState{Key: key, UpdatedAt: now}
		return candidate
	}
	state := h.Routing
	if state == nil || state.Key != key || state.UpdatedAt.IsZero() || math.IsNaN(state.Factor) || math.IsInf(state.Factor, 0) || state.Factor < 0 || state.Factor > 1 {
		state = &routingWeightState{Key: key, Factor: desired, UpdatedAt: now}
	} else {
		factor := smoothRoutingFactor(state.Factor, desired, now.Sub(state.UpdatedAt), cfg.Routing.WeightSmoothingSeconds)
		if state.UpdatedAt.After(now) {
			factor = min(state.Factor, desired)
		}
		state = &routingWeightState{Key: key,
			Factor: factor, UpdatedAt: now}
	}
	h.Routing = state
	if state.Factor == 0 {
		candidate.EffectiveWeight = 0
	} else {
		candidate.EffectiveWeight = max(1, int64(float64(candidate.EffectiveWeight)*state.Factor))
	}
	return candidate
}

func applyRoutingWeights(ctx context.Context, tx *sql.Tx, snapshot *Snapshot, cfg Config, now time.Time) error {
	rows, err := loadTargets(ctx, tx, cfg.CoreInstance)
	if err != nil {
		return err
	}
	for i, candidate := range snapshot.Candidates {
		row := rows[candidate.Ref]
		snapshot.Candidates[i] = row.Health.routingCandidate(row.Target, cfg, candidate.Active, now)
		if err := saveTarget(ctx, tx, cfg.CoreInstance, row); err != nil {
			return err
		}
	}
	return snapshot.Validate(now)
}

func checkRoutingPolicy(ctx context.Context, tx *sql.Tx, core string, cfg *Config, load *LoadSample) error {
	var body []byte
	var persistedDigest string
	if err := tx.QueryRowContext(ctx, `SELECT probe_limits,config_digest FROM dispatch.cores WHERE core_instance=$1`, core).Scan(&body, &persistedDigest); err != nil {
		return err
	}
	var policy persistedProbePolicy
	if err := json.Unmarshal(body, &policy); err != nil {
		return err
	}
	if policy.Routing == nil && (cfg == nil || cfg.Routing == nil) {
		return nil
	}
	if policy.Routing == nil || cfg == nil || cfg.Routing == nil || *policy.Routing != *cfg.Routing {
		return ErrConfigRevision
	}
	digest, err := runtimeConfigDigest(*cfg, cfg.ProbeLimits())
	if err != nil {
		return err
	}
	if digest != persistedDigest {
		return ErrConfigRevision
	}
	if load == nil {
		return errors.New("dispatch routing requires a live load sample")
	}
	return nil
}
