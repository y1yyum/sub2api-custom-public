package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

var (
	ErrProbeLease          = errors.New("invalid or expired dispatch probe lease")
	ErrObservationConflict = errors.New("dispatch observation event conflict")
	ErrConfigRevision      = errors.New("dispatch config revision mismatch")
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

type ProbeLimits struct {
	ConfigRevision   int64         `json:"config_revision"`
	LeaseDuration    time.Duration `json:"lease_duration"`
	GlobalConcurrent int           `json:"global_concurrent"`
	ProviderRPM      int           `json:"provider_rpm"`
	DailyRequests    int           `json:"daily_requests"`
}

type ProbeLease struct {
	CoreInstance  string
	Target        Target
	Token         string
	Generation    int64
	ExpiresAt     time.Time
	AttemptID     string
	ReservationID string
	Budget        *ProbeBudgetReservation
}

type storedTarget struct {
	Target     Target
	Health     Health
	Generation int64
}

// 全局事务锁同时覆盖跨 Core 的预算、健康域租约及配置切换，避免锁顺序反转。
func (s *Store) begin(ctx context.Context) (*sql.Tx, time.Time, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, storeLock); err != nil {
		_ = tx.Rollback()
		return nil, time.Time{}, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		_ = tx.Rollback()
		return nil, time.Time{}, err
	}
	return tx, now.UTC(), nil
}

func loadTargets(ctx context.Context, tx *sql.Tx, core string) (map[string]*storedTarget, error) {
	rows, err := tx.QueryContext(ctx, `SELECT metadata, health, generation FROM dispatch.targets WHERE core_instance=$1 ORDER BY target_ref`, core)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	targets := make(map[string]*storedTarget)
	for rows.Next() {
		var metadata, health []byte
		row := new(storedTarget)
		if err := rows.Scan(&metadata, &health, &row.Generation); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &row.Target); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(health, &row.Health); err != nil {
			return nil, err
		}
		targets[row.Target.Ref] = row
	}
	return targets, rows.Err()
}

func saveTarget(ctx context.Context, tx *sql.Tx, core string, row *storedTarget) error {
	metadata, err := json.Marshal(row.Target)
	if err != nil {
		return err
	}
	health, err := json.Marshal(row.Health)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch.targets(core_instance,target_ref,metadata,health,generation)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (core_instance,target_ref)
		DO UPDATE SET metadata=EXCLUDED.metadata,health=EXCLUDED.health,generation=EXCLUDED.generation`,
		core, row.Target.Ref, string(metadata), string(health), row.Generation)
	return err
}

func sameIdentity(a, b Target) bool {
	return a.Ref == b.Ref && a.AccountID == b.AccountID && a.CredentialRef == b.CredentialRef &&
		a.ProviderRef == b.ProviderRef && a.PoolRef == b.PoolRef && a.Model == b.Model && a.Region == b.Region
}

func sameDomain(a, b Target) bool {
	return a.AccountID == b.AccountID && a.Model == b.Model && a.Region == b.Region
}

func sameCredential(a, b Target) bool {
	return a.CredentialRef == b.CredentialRef && a.CredentialRevision == b.CredentialRevision
}

func (s *Store) SyncConfig(ctx context.Context, core string, configRevision int64, targets []Target, limits ProbeLimits) error {
	return s.syncConfig(ctx, core, configRevision, targets, limits, nil, nil, nil, nil, nil, nil, nil, nil)
}

func (s *Store) SyncRuntimeConfig(ctx context.Context, cfg Config) error {
	return s.syncConfig(ctx, cfg.CoreInstance, cfg.ConfigRevision, cfg.Targets, cfg.ProbeLimits(), cfg.Telemetry, cfg.Quality, cfg.Scheduled, cfg.Routing, cfg.HealthWindow, cfg.ProbeSchedule, cfg.Probe, cfg.ProbeBudget)
}

func (s *Store) syncConfig(ctx context.Context, core string, configRevision int64, targets []Target, limits ProbeLimits, telemetry *TelemetryConfig, quality *QualityConfig, scheduled *ScheduledConfig, routing *RoutingConfig, window *HealthWindowConfig, schedule *ProbeScheduleConfig, probe *ProbeConfig, budget *ProbeBudgetConfig) error {
	routing, err := normalizeRoutingConfig(routing, telemetry)
	if err != nil {
		return err
	}
	window, err = normalizeHealthWindowConfig(window, routing, telemetry)
	if err != nil {
		return err
	}
	schedule, err = normalizeProbeScheduleConfig(schedule, probe, window, telemetry)
	if err != nil {
		return err
	}
	budget, err = normalizeProbeBudgetConfig(budget, probe, targets)
	if err != nil {
		return err
	}
	if scheduled != nil {
		normalized, err := normalizeScheduledConfig(*scheduled)
		if err != nil {
			return err
		}
		scheduled = &normalized
	}
	if configRevision < 1 || limits.ConfigRevision != configRevision {
		return ErrConfigRevision
	}
	if err := limits.validate(); err != nil {
		return err
	}
	if !validRef(core) || len(targets) > MaxCandidates {
		return errors.New("invalid dispatch target set")
	}
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if err := target.validate(); err != nil {
			return err
		}
		if seen[target.Ref] {
			return errors.New("duplicate dispatch target ref")
		}
		seen[target.Ref] = true
	}
	digest, err := runtimeConfigDigest(Config{Targets: targets, Telemetry: telemetry, Quality: quality, Scheduled: scheduled, Routing: routing, HealthWindow: window, ProbeSchedule: schedule, ProbeBudget: budget}, limits)
	if err != nil {
		return err
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.cores(core_instance) VALUES ($1) ON CONFLICT DO NOTHING`, core); err != nil {
		return err
	}
	var previousRevision int64
	var previousDigest string
	if err := tx.QueryRowContext(ctx, `SELECT config_revision,config_digest FROM dispatch.cores WHERE core_instance=$1`, core).Scan(&previousRevision, &previousDigest); err != nil {
		return err
	}
	if configRevision < previousRevision {
		return fmt.Errorf("%w: rollback", ErrConfigRevision)
	}
	if configRevision == previousRevision {
		if digest != previousDigest {
			return fmt.Errorf("%w: content conflict", ErrConfigRevision)
		}
		if err := syncProbeBudgetPolicy(ctx, tx, core, budget, limits.LeaseDuration > 0, now); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := syncProbeBudgetPolicy(ctx, tx, core, budget, limits.LeaseDuration > 0, now); err != nil {
		return err
	}
	stored, err := loadTargets(ctx, tx, core)
	if err != nil {
		return err
	}
	previousPolicy, err := readProbePolicy(ctx, tx, core)
	if err != nil {
		return err
	}
	if err := checkHeldQuotaConfig(ctx, tx, core, stored, targets); err != nil {
		return err
	}
	maxRevision := make(map[string]int64)
	previousCredentialRevision := make(map[string]int64)
	for _, row := range stored {
		maxRevision[row.Target.CredentialRef] = max(maxRevision[row.Target.CredentialRef], row.Target.CredentialRevision)
		previousCredentialRevision[row.Target.CredentialRef] = maxRevision[row.Target.CredentialRef]
	}
	for _, target := range targets {
		maxRevision[target.CredentialRef] = max(maxRevision[target.CredentialRef], target.CredentialRevision)
	}
	for _, target := range targets {
		if target.CredentialRevision < maxRevision[target.CredentialRef] {
			return errors.New("dispatch credential revision rollback")
		}
		if old := stored[target.Ref]; old != nil && !sameIdentity(old.Target, target) {
			return errors.New("dispatch target identity conflict")
		}
	}
	for _, row := range stored {
		row.Generation++
		row.Health.TTFTMillis, row.Health.TTFTObservedAt, row.Health.Routing = 0, time.Time{}, nil
		row.Health.Rolling = nil
		if previousPolicy.HealthWindow != nil || window != nil {
			row.Health.WindowAt = now
			row.Health.Samples, row.Health.Failures = 0, 0
			row.Health.GoodWindows, row.Health.BadWindows = 0, 0
		}
		if row.Health.State == Probing {
			row.Health.State, row.Health.Reason, row.Health.CoolUntil = Tripped, "config_changed", now
		}
	}
	for _, target := range targets {
		old := stored[target.Ref]
		if old == nil {
			health := InitialHealth(target, now)
			if previousCredentialRevision[target.CredentialRef] > 0 && target.CredentialRevision > previousCredentialRevision[target.CredentialRef] {
				health.State, health.Reason, health.CoolUntil = Tripped, "credential_rotated", now
			}
			for _, previous := range stored {
				if sameDomain(previous.Target, target) && sameCredential(previous.Target, target) && healthSeverity(previous.Health) > healthSeverity(health) {
					health = previous.Health
				}
			}
			stored[target.Ref] = &storedTarget{Target: target, Health: health, Generation: 1}
			continue
		}
		if old.Target == target {
			continue
		}
		if target.CredentialRevision > old.Target.CredentialRevision {
			old.Health = InitialHealth(target, now)
			old.Health.State, old.Health.Reason, old.Health.CoolUntil = Tripped, "credential_rotated", now
		} else if !old.Target.Enabled && target.Enabled && !old.Health.AuthBlocked {
			old.Health.State, old.Health.Reason, old.Health.CoolUntil = Tripped, "target_reenabled", now
		} else if old.Health.State == Probing {
			old.Health.State, old.Health.Reason, old.Health.CoolUntil = Tripped, "config_changed", now
		}
		old.Target = target
		old.Generation++
	}
	if len(stored) > MaxCandidates {
		return errors.New("dispatch retained target limit exceeded")
	}
	for ref, row := range stored {
		if !seen[ref] && row.Target.Enabled {
			row.Target.Enabled = false
			row.Generation++
		}
		// 新增别名不能绕过该凭据版本已确认的认证封禁。
		for _, other := range stored {
			if sameCredential(row.Target, other.Target) && other.Health.AuthBlocked {
				row.Health.AuthBlocked = true
				row.Health.State, row.Health.Reason = Tripped, "credential_rejected"
				break
			}
		}
		if err := saveTarget(ctx, tx, core, row); err != nil {
			return err
		}
	}
	if err := reconcileHealthHolds(ctx, tx, core, stored, now); err != nil {
		return err
	}
	encodedLimits, err := json.Marshal(persistedProbePolicy{ProbeLimits: limits, Routing: routing, HealthWindow: window, ProbeSchedule: schedule, ProbeBudget: budget})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dispatch.cores SET config_revision=$2,config_digest=$3,probe_limits=$4 WHERE core_instance=$1`, core, configRevision, digest, string(encodedLimits)); err != nil {
		return err
	}
	if quality != nil {
		if len(quality.baselineDigest) != 64 || len(quality.baselineBody) == 0 || quality.baseline == nil {
			return errors.New("dispatch quality baseline must be loaded")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.quality_baselines(core_instance,baseline_revision,baseline_digest)
		 VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, core, quality.baseline.Revision(), quality.baselineDigest); err != nil {
			return err
		}
		var baselineDigest string
		if err := tx.QueryRowContext(ctx, `SELECT baseline_digest FROM dispatch.quality_baselines WHERE core_instance=$1 AND baseline_revision=$2`, core, quality.baseline.Revision()).Scan(&baselineDigest); err != nil {
			return err
		}
		if baselineDigest != quality.baselineDigest {
			return errors.New("dispatch quality baseline revision conflict")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.quality_configs(core_instance,config_revision,baseline_digest,activated_at)
		 VALUES ($1,$2,$3,$4) ON CONFLICT (core_instance) DO UPDATE SET
		 config_revision=EXCLUDED.config_revision,baseline_digest=EXCLUDED.baseline_digest,activated_at=EXCLUDED.activated_at`, core, configRevision, quality.baselineDigest, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (l ProbeLimits) validate() error {
	if l.LeaseDuration == 0 && l.GlobalConcurrent == 0 && l.ProviderRPM == 0 && l.DailyRequests == 0 {
		return nil
	}
	if l.LeaseDuration < time.Microsecond || l.GlobalConcurrent < 1 || l.ProviderRPM < 1 || l.DailyRequests < 1 {
		return errors.New("invalid dispatch probe limits")
	}
	return nil
}

func effectiveProbeLimits(ctx context.Context, tx *sql.Tx, provider string) (int, int, int, error) {
	var global, rpm, daily int
	err := tx.QueryRowContext(ctx, `WITH enabled AS (
		SELECT core_instance,probe_limits FROM dispatch.cores
		WHERE (probe_limits->>'lease_duration')::bigint>0)
		SELECT min((probe_limits->>'global_concurrent')::bigint),
		(SELECT min((c.probe_limits->>'provider_rpm')::bigint) FROM enabled c
		 WHERE EXISTS (SELECT 1 FROM dispatch.targets t WHERE t.core_instance=c.core_instance
		 AND t.metadata->>'provider_ref'=$1 AND (t.metadata->>'enabled')::boolean)),
		min((probe_limits->>'daily_requests')::bigint) FROM enabled`, provider).Scan(&global, &rpm, &daily)
	return global, rpm, daily, err
}

func healthSeverity(h Health) int {
	if h.AuthBlocked {
		return 4
	}
	switch h.State {
	case Tripped:
		return 3
	case Probing:
		return 2
	case Degraded:
		return 1
	default:
		return 0
	}
}

func checkConfigRevision(ctx context.Context, tx *sql.Tx, core string, revision int64) error {
	var current int64
	err := tx.QueryRowContext(ctx, `SELECT config_revision FROM dispatch.cores WHERE core_instance=$1`, core).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConfigRevision
	}
	if err != nil {
		return err
	}
	if revision < 1 || revision != current {
		return ErrConfigRevision
	}
	return nil
}

func recordObservation(ctx context.Context, tx *sql.Tx, core string, o Observation, now time.Time) (bool, error) {
	o.At = o.At.UTC()
	body, err := json.Marshal(o)
	if err != nil {
		return false, err
	}
	var equal bool
	err = tx.QueryRowContext(ctx, `SELECT payload=$3::jsonb FROM dispatch.observations WHERE core_instance=$1 AND event_id=$2`, core, o.EventID, string(body)).Scan(&equal)
	if err == nil {
		if !equal {
			return false, ErrObservationConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch.observations(core_instance,event_id,payload,recorded_at) VALUES ($1,$2,$3,$4)`, core, o.EventID, string(body), now)
	return err == nil, err
}

type observationOrigin int

const (
	observationImport observationOrigin = iota
	observationProbe
	observationPassive
)

func applyObservation(ctx context.Context, tx *sql.Tx, core string, rows map[string]*storedTarget, source Target, o Observation, now time.Time, origin observationOrigin) error {
	if o.FailureClass != "" {
		return triggerHealthHold(ctx, tx, core, rows, source, o, now)
	}
	policy, err := readProbePolicy(ctx, tx, core)
	if err != nil {
		return err
	}
	for _, row := range rows {
		target := row.Target
		auth := o.ProviderEvidence && o.Outcome == UpstreamAuth && sameCredential(source, target)
		failure := o.ProviderEvidence && (o.Outcome == UpstreamRateLimit || o.Outcome == UpstreamFailure) && sameDomain(source, target)
		probePeer := origin == observationProbe && sameDomain(source, target) && sameCredential(source, target)
		if target.Ref != source.Ref && !auth && !failure && !probePeer {
			continue
		}
		derived := o
		derived.TargetRef, derived.AccountID, derived.CredentialRevision = target.Ref, target.AccountID, target.CredentialRevision
		// 在线事实按持久确认时间应用；原始事件时间保留在去重事实中。
		if origin == observationProbe || origin == observationPassive && derived.ProviderEvidence {
			derived.At = now
		}
		if policy.HealthWindow == nil && (auth || origin != observationImport) && derived.At.Before(row.Health.UpdatedAt) {
			derived.At = row.Health.UpdatedAt
		}
		if err := row.Health.ApplyWithWindow(target, derived, now, origin == observationProbe, origin == observationPassive, policy.HealthWindow, policy.ttftTargetMillis()); err != nil {
			return err
		}
		if err := saveTarget(ctx, tx, core, row); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Observe(ctx context.Context, core string, o Observation) (bool, error) {
	if !validRef(core) {
		return false, errors.New("invalid dispatch core identity")
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := recordObservation(ctx, tx, core, o, now)
	if err != nil || !inserted {
		return false, err
	}
	rows, err := loadTargets(ctx, tx, core)
	if err != nil {
		return false, err
	}
	row := rows[o.TargetRef]
	if row == nil {
		return false, errors.New("unknown dispatch target")
	}
	if err := validateObservation(o, row.Target, now); err != nil {
		return false, err
	}
	if err := applyObservation(ctx, tx, core, rows, row.Target, o, now, observationImport); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) Publish(ctx context.Context, core string, configRevision int64, ttl time.Duration) (Snapshot, error) {
	snapshot, _, err := s.publish(ctx, core, configRevision, ttl, nil, nil, 0, nil)
	return snapshot, err
}

func (s *Store) PublishTo(ctx context.Context, core string, configRevision int64, ttl time.Duration, path string) (Snapshot, error) {
	snapshot, _, err := s.publish(ctx, core, configRevision, ttl, &path, nil, 0, nil)
	return snapshot, err
}

func (s *Store) PublishWithLoad(ctx context.Context, core string, configRevision int64, ttl time.Duration, path string, load LoadSample, maxAge time.Duration) (Snapshot, error) {
	snapshot, _, err := s.publish(ctx, core, configRevision, ttl, &path, &load, maxAge, nil)
	return snapshot, err
}

func (s *Store) PublishRuntime(ctx context.Context, cfg Config, load *LoadSample) (Snapshot, error) {
	snapshot, _, err := s.publishRuntime(ctx, cfg, load)
	return snapshot, err
}

// publishRuntime 仅在配置 snapshot_server 时读取发布 lineage；文件内容不含 lineage。
func (s *Store) publishRuntime(ctx context.Context, cfg Config, load *LoadSample) (Snapshot, string, error) {
	var err error
	cfg.Routing, err = normalizeRoutingConfig(cfg.Routing, cfg.Telemetry)
	if err != nil {
		return Snapshot{}, "", err
	}
	cfg.HealthWindow, err = normalizeHealthWindowConfig(cfg.HealthWindow, cfg.Routing, cfg.Telemetry)
	if err != nil {
		return Snapshot{}, "", err
	}
	cfg.ProbeSchedule, err = normalizeProbeScheduleConfig(cfg.ProbeSchedule, cfg.Probe, cfg.HealthWindow, cfg.Telemetry)
	if err != nil {
		return Snapshot{}, "", err
	}
	cfg.ProbeBudget, err = normalizeProbeBudgetConfig(cfg.ProbeBudget, cfg.Probe, cfg.Targets)
	if err != nil {
		return Snapshot{}, "", err
	}
	maxAge := time.Duration(0)
	if load != nil && cfg.Telemetry != nil {
		maxAge = time.Duration(cfg.Telemetry.LoadMaxAgeSeconds) * time.Second
	}
	return s.publish(ctx, cfg.CoreInstance, cfg.ConfigRevision, time.Duration(cfg.SnapshotTTLSeconds)*time.Second, &cfg.SnapshotFile, load, maxAge, &cfg)
}

func (s *Store) publish(ctx context.Context, core string, configRevision int64, ttl time.Duration, path *string, load *LoadSample, maxAge time.Duration, cfg *Config) (Snapshot, string, error) {
	if !validRef(core) || ttl < time.Microsecond || ttl > 30*time.Second {
		return Snapshot{}, "", errors.New("invalid dispatch snapshot lifetime")
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return Snapshot{}, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, configRevision); err != nil {
		return Snapshot{}, "", err
	}
	if err := checkRoutingPolicy(ctx, tx, core, cfg, load); err != nil {
		return Snapshot{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "dispatch.publish:"+core); err != nil {
		return Snapshot{}, "", err
	}
	snapshot, err := buildSnapshot(ctx, tx, core, ttl, now)
	if err != nil {
		return Snapshot{}, "", err
	}
	if load != nil {
		if err := snapshot.applyLoad(*load, maxAge, now); err != nil {
			return Snapshot{}, "", err
		}
	}
	if cfg != nil && cfg.Routing != nil {
		if err := applyRoutingWeights(ctx, tx, &snapshot, *cfg, now); err != nil {
			return Snapshot{}, "", err
		}
	}
	if err := applyHealthWindowWeights(ctx, tx, &snapshot, now); err != nil {
		return Snapshot{}, "", err
	}
	if cfg != nil && cfg.Quality != nil {
		samples, err := readQualitySamples(ctx, tx, *cfg, now)
		if err != nil {
			return Snapshot{}, "", err
		}
		if cfg.Quality.ApplyWeight {
			applyQualityWeights(&snapshot, qualityReports(cfg.Targets, samples))
		}
	}
	if err := applyHealthHoldVeto(ctx, tx, &snapshot); err != nil {
		return Snapshot{}, "", err
	}
	var lineage string
	if cfg != nil && cfg.SnapshotServer != nil {
		if lineage, err = currentPublishLineage(ctx, tx, now); err != nil {
			return Snapshot{}, "", err
		}
	}
	if path != nil {
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return Snapshot{}, "", err
		}
		if err := WriteSnapshot(*path, snapshot, now); err != nil {
			return Snapshot{}, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, "", err
	}
	return snapshot, lineage, nil
}

func buildSnapshot(ctx context.Context, tx *sql.Tx, core string, ttl time.Duration, now time.Time) (Snapshot, error) {
	snapshot := Snapshot{Version: SnapshotVersion, CoreInstance: core, GeneratedAt: now, ExpiresAt: now.Add(ttl), Candidates: []Candidate{}}
	// nextval 不随事务回滚；文件已可见而 commit ACK 丢失时也不会复用版本。
	if err := tx.QueryRowContext(ctx, `UPDATE dispatch.cores SET revision=nextval('dispatch.snapshot_revision') WHERE core_instance=$1 RETURNING revision`, core).Scan(&snapshot.Revision); err != nil {
		return Snapshot{}, err
	}
	rows, err := loadTargets(ctx, tx, core)
	if err != nil {
		return Snapshot{}, err
	}
	if err := reconcileHealthHolds(ctx, tx, core, rows, now); err != nil {
		return Snapshot{}, err
	}
	policy, err := readProbePolicy(ctx, tx, core)
	if err != nil {
		return Snapshot{}, err
	}
	for _, row := range rows {
		if policy.HealthWindow != nil {
			row.Health.AdvanceHealthWindow(now, policy.HealthWindow, policy.ttftTargetMillis())
			if err := saveTarget(ctx, tx, core, row); err != nil {
				return Snapshot{}, err
			}
		}
		snapshot.Candidates = append(snapshot.Candidates, row.Health.Candidate(row.Target))
	}
	sort.Slice(snapshot.Candidates, func(i, j int) bool { return snapshot.Candidates[i].Ref < snapshot.Candidates[j].Ref })
	if err := snapshot.Validate(now); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Store) ClaimProbe(ctx context.Context, core, targetRef, owner string, limits ProbeLimits) (*ProbeLease, error) {
	if !validRef(core) || !validRef(targetRef) || !validRef(owner) {
		return nil, errors.New("invalid dispatch probe identity")
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, limits.ConfigRevision); err != nil {
		return nil, err
	}
	var encodedLimits []byte
	if err := tx.QueryRowContext(ctx, `SELECT probe_limits FROM dispatch.cores WHERE core_instance=$1`, core).Scan(&encodedLimits); err != nil {
		return nil, err
	}
	var persisted persistedProbePolicy
	if err := json.Unmarshal(encodedLimits, &persisted); err != nil {
		return nil, err
	}
	if persisted.ProbeLimits != limits {
		return nil, ErrConfigRevision
	}
	if limits.LeaseDuration == 0 {
		return nil, nil
	}
	rows, err := loadTargets(ctx, tx, core)
	if err != nil {
		return nil, err
	}
	row := rows[targetRef]
	if row == nil || !row.Target.Enabled {
		return nil, nil
	}
	reservation, err := prepareProbeBudget(ctx, tx, core, row.Target, persisted.ProbeBudget, now)
	if errors.Is(err, ErrProbeBudgetExhausted) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Health.AuthBlocked || now.Before(row.Health.CoolUntil) {
		return nil, nil
	}
	holds, err := loadHealthHolds(ctx, tx, core)
	if err != nil {
		return nil, err
	}
	for _, hold := range holds {
		if hold.Active && hold.Scope.matches(row.Target) {
			return nil, nil
		}
	}
	if persisted.ProbeSchedule != nil {
		pacingHealth := row.Health
		for _, peer := range rows {
			if sameDomain(row.Target, peer.Target) && peer.Health.RecentPassiveSamples(now) > pacingHealth.RecentPassiveSamples(now) {
				pacingHealth.Rolling = peer.Health.Rolling
			}
		}
		var previous sql.NullTime
		if err := tx.QueryRowContext(ctx, `SELECT max(reserved_at) FROM dispatch.probes
		 WHERE core_instance=$1 AND account_id=$2 AND model=$3 AND region=$4`,
			core, row.Target.AccountID, row.Target.Model, row.Target.Region).Scan(&previous); err != nil {
			return nil, err
		}
		if now.Before(probeDueAt(core, row.Target, pacingHealth, *persisted.ProbeSchedule, previous.Time, now)) {
			return nil, nil
		}
	}
	var domainBusy, slotUsed bool
	slot := now.Unix() / 30
	if err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM dispatch.probes WHERE core_instance=$1 AND account_id=$2 AND model=$3 AND region=$4 AND finished_at IS NULL AND lease_until>$5),
		EXISTS(SELECT 1 FROM dispatch.probes WHERE core_instance=$1 AND account_id=$2 AND model=$3 AND region=$4 AND slot=$6)`,
		core, row.Target.AccountID, row.Target.Model, row.Target.Region, now, slot).Scan(&domainBusy, &slotUsed); err != nil {
		return nil, err
	}
	if domainBusy || slotUsed {
		return nil, nil
	}
	globalLimit, rpmLimit, dailyLimit, err := effectiveProbeLimits(ctx, tx, row.Target.ProviderRef)
	if err != nil {
		return nil, err
	}
	var concurrent, rpm, daily int
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM dispatch.probes WHERE finished_at IS NULL AND lease_until>$1),
		(SELECT count(*) FROM dispatch.probes WHERE provider_ref=$2 AND reserved_at>=$3),
		(SELECT count(*) FROM dispatch.probes WHERE reserved_at>=$4)`, now, row.Target.ProviderRef, now.Add(-time.Minute), day).Scan(&concurrent, &rpm, &daily); err != nil {
		return nil, err
	}
	if concurrent >= globalLimit || rpm >= rpmLimit || daily >= dailyLimit {
		return nil, nil
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	row.Generation++
	lease := &ProbeLease{CoreInstance: core, Target: row.Target, Token: id.String(), Generation: row.Generation,
		ExpiresAt: now.Add(limits.LeaseDuration.Truncate(time.Microsecond)), AttemptID: "dispatch-attempt:" + id.String(), ReservationID: "dispatch-reservation:" + id.String(), Budget: reservation}
	metadata, err := json.Marshal(row.Target)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.probes(token,core_instance,target_ref,target,generation,owner,account_id,model,region,provider_ref,slot,attempt_id,reservation_id,reserved_at,lease_until)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, lease.Token, core, targetRef, string(metadata), lease.Generation, owner,
		row.Target.AccountID, row.Target.Model, row.Target.Region, row.Target.ProviderRef, slot, lease.AttemptID, lease.ReservationID, now, lease.ExpiresAt); err != nil {
		return nil, err
	}
	if err := recordProbeBudget(ctx, tx, *lease, reservation, now); err != nil {
		return nil, err
	}
	for _, peer := range rows {
		if sameDomain(row.Target, peer.Target) && sameCredential(row.Target, peer.Target) && !peer.Health.AuthBlocked &&
			(peer.Health.State == Tripped || peer.Health.State == Probing) {
			peer.Health.State, peer.Health.Reason = Probing, "probe_inflight"
			if err := saveTarget(ctx, tx, core, peer); err != nil {
				return nil, err
			}
		}
	}
	if err := saveTarget(ctx, tx, core, row); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return lease, nil
}

func (s *Store) FinishProbe(ctx context.Context, lease ProbeLease, o Observation) error {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var core, targetRef, attempt, reservation string
	var metadata []byte
	var generation int64
	var expiry time.Time
	var finished sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT core_instance,target_ref,target,generation,lease_until,attempt_id,reservation_id,finished_at FROM dispatch.probes WHERE token=$1`, lease.Token).
		Scan(&core, &targetRef, &metadata, &generation, &expiry, &attempt, &reservation, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProbeLease
	}
	if err != nil {
		return err
	}
	var target Target
	if err := json.Unmarshal(metadata, &target); err != nil {
		return err
	}
	if finished.Valid || !now.Before(expiry) || core != lease.CoreInstance || target != lease.Target || targetRef != lease.Target.Ref ||
		generation != lease.Generation || !expiry.Equal(lease.ExpiresAt) || attempt != lease.AttemptID || o.EventID != attempt || reservation != lease.ReservationID {
		return ErrProbeLease
	}
	rows, err := loadTargets(ctx, tx, core)
	if err != nil {
		return err
	}
	row := rows[targetRef]
	if row == nil || row.Generation != generation || row.Target != target || !row.Target.Enabled {
		return ErrProbeLease
	}
	if err := validateObservation(o, target, now); err != nil {
		return err
	}
	inserted, err := recordObservation(ctx, tx, core, o, now)
	if err != nil {
		return err
	}
	if !inserted {
		return fmt.Errorf("probe observation already consumed: %w", ErrObservationConflict)
	}
	if err := applyObservation(ctx, tx, core, rows, target, o, now, observationProbe); err != nil {
		return err
	}
	var finishedAt time.Time
	err = tx.QueryRowContext(ctx, `UPDATE dispatch.probes SET finished_at=clock_timestamp()
		WHERE token=$1 AND lease_until>clock_timestamp() RETURNING finished_at`, lease.Token).Scan(&finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProbeLease
	}
	if err != nil {
		return err
	}
	if err := finishProbeBudget(ctx, tx, lease.Token, o.Outcome == Success && o.ProviderEvidence, finishedAt); err != nil {
		return err
	}
	return tx.Commit()
}
