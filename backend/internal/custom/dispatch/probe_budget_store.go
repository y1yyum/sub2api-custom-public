package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrProbeBudgetExhausted = errors.New("dispatch probe budget exhausted")
	ErrProbeBudgetConflict  = errors.New("dispatch probe budget conflict")
	ErrProbeBudgetRequired  = errors.New("dispatch active probe requires a budget")
	ErrProbeBudgetSchema    = errors.New("dispatch probe budget schema 004 required")
)

type ProbeBudgetReservation struct {
	Currency      string    `json:"currency"`
	MaxMicrousd   int64     `json:"max_microusd"`
	ProviderRef   string    `json:"provider_ref"`
	Model         string    `json:"model"`
	PriceRevision string    `json:"price_revision"`
	EvidenceRef   string    `json:"evidence_ref"`
	ReservedAt    time.Time `json:"reserved_at"`
	BudgetDay     string    `json:"budget_day"`
}

type ProbeBudgetCloseRequest struct {
	Token           string `json:"token"`
	ExpectedVersion int64  `json:"expected_version"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
	EvidenceRef     string `json:"evidence_ref"`
}

type ProbeBudgetEntry struct {
	ProbeBudgetReservation
	Token        string                   `json:"token"`
	CoreInstance string                   `json:"core_instance"`
	TargetRef    string                   `json:"target_ref"`
	State        string                   `json:"state"`
	Version      int64                    `json:"version"`
	SettledAt    *time.Time               `json:"settled_at,omitempty"`
	CloseRequest *ProbeBudgetCloseRequest `json:"close_request,omitempty"`
}

type ProbeBudgetTotals struct {
	ReservedMicrousd int64 `json:"reserved_microusd"`
	UnknownMicrousd  int64 `json:"unknown_microusd"`
	ChargedMicrousd  int64 `json:"charged_microusd"`
	ExposureMicrousd int64 `json:"exposure_microusd"`
}

type ProbeProviderBudgetReport struct {
	ProviderRef   string `json:"provider_ref"`
	DailyMicrousd *int64 `json:"daily_microusd,omitempty"`
	ProbeBudgetTotals
}

type ProbeBudgetActivation struct {
	CoreInstance          string    `json:"core_instance"`
	ActivationEvidenceRef string    `json:"activation_evidence_ref"`
	ActivatedAt           time.Time `json:"activated_at"`
}

type ProbeBudgetReport struct {
	SchemaReady         bool                        `json:"schema_ready"`
	Enabled             bool                        `json:"enabled"`
	Active              bool                        `json:"active"`
	Fenced              bool                        `json:"fenced"`
	Currency            string                      `json:"currency"`
	ObservedAt          time.Time                   `json:"observed_at"`
	BudgetDay           string                      `json:"budget_day"`
	Activation          *ProbeBudgetActivation      `json:"activation,omitempty"`
	GlobalDailyMicrousd *int64                      `json:"global_daily_microusd,omitempty"`
	Global              ProbeBudgetTotals           `json:"global"`
	Providers           []ProbeProviderBudgetReport `json:"providers"`
	Entries             []ProbeBudgetEntry          `json:"entries"`
	EntryCount          int64                       `json:"entry_count"`
	EntriesTruncated    bool                        `json:"entries_truncated"`
	ProvidersTruncated  bool                        `json:"providers_truncated"`
}

func (r ProbeBudgetCloseRequest) Validate() error {
	id, err := uuid.Parse(r.Token)
	if err != nil || id == uuid.Nil || id.String() != r.Token || r.ExpectedVersion < 1 ||
		!validRef(r.Actor) || !validRef(r.EvidenceRef) || !utf8.ValidString(r.Reason) ||
		strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 512 ||
		strings.IndexFunc(r.Reason, func(c rune) bool { return unicode.IsControl(c) || c == '\u2028' || c == '\u2029' }) >= 0 {
		return errors.New("invalid dispatch probe budget close request")
	}
	return nil
}

func LoadProbeBudgetCloseRequest(path string) (ProbeBudgetCloseRequest, error) {
	var request ProbeBudgetCloseRequest
	file, err := os.Open(path)
	if err != nil {
		return request, errors.New("dispatch probe budget close file unavailable")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(body) > 16<<10 || !utf8.Valid(body) {
		return request, errors.New("invalid dispatch probe budget close file")
	}
	if err := decodeJSON(body, &request); err != nil {
		return ProbeBudgetCloseRequest{}, errors.New("invalid dispatch probe budget close JSON")
	}
	if err := request.Validate(); err != nil {
		return ProbeBudgetCloseRequest{}, err
	}
	return request, nil
}

func probeBudgetSchemaReady(ctx context.Context, tx *sql.Tx) (bool, error) {
	var present int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM unnest(ARRAY[
	 'dispatch.probe_budget_latch','dispatch.probe_budget_policies','dispatch.probe_prices',
	 'dispatch.probe_money','dispatch.probe_money_audit']) AS t(name) WHERE to_regclass(name) IS NOT NULL`).Scan(&present); err != nil {
		return false, err
	}
	if present == 0 {
		// 兼容显式升级前的 001/003；不在运行时自动迁移。
		var migrationTable bool
		if err := tx.QueryRowContext(ctx, `SELECT to_regclass('dispatch.schema_migrations') IS NOT NULL`).Scan(&migrationTable); err != nil {
			return false, err
		}
		if migrationTable {
			var recorded bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch.schema_migrations WHERE version='004')`).Scan(&recorded); err != nil {
				return false, err
			}
			if recorded {
				return false, ErrProbeBudgetSchema
			}
		}
		return false, nil
	}
	if present != 5 {
		return false, ErrProbeBudgetSchema
	}
	var checksum string
	if err := tx.QueryRowContext(ctx, `SELECT checksum FROM dispatch.schema_migrations WHERE version='004'`).Scan(&checksum); err != nil {
		return false, fmt.Errorf("%w: migration record unavailable", ErrProbeBudgetSchema)
	}
	if checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(probeBudgetSchema))) {
		return false, ErrProbeBudgetSchema
	}
	return true, nil
}

func probeBudgetLatched(ctx context.Context, tx *sql.Tx) (bool, error) {
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch.probe_budget_latch)`).Scan(&enabled)
	return enabled, err
}

func syncProbeBudgetPolicy(ctx context.Context, tx *sql.Tx, core string, budget *ProbeBudgetConfig, enabled bool, now time.Time) error {
	ready, err := probeBudgetSchemaReady(ctx, tx)
	if err != nil {
		return err
	}
	if !ready {
		if budget != nil {
			return ErrProbeBudgetSchema
		}
		return nil
	}
	latched, err := probeBudgetLatched(ctx, tx)
	if err != nil {
		return err
	}
	if budget == nil {
		if enabled && latched {
			return ErrProbeBudgetRequired
		}
		_, err := tx.ExecContext(ctx, `UPDATE dispatch.probe_budget_policies SET active=FALSE WHERE core_instance=$1`, core)
		return err
	}
	for _, price := range budget.Prices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.probe_prices(provider_ref,model,revision,evidence_ref,max_microusd)
		 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, price.ProviderRef, price.Model, price.Revision, price.EvidenceRef, price.MaxMicrousd); err != nil {
			return err
		}
		var equal bool
		if err := tx.QueryRowContext(ctx, `SELECT evidence_ref=$4 AND max_microusd=$5 FROM dispatch.probe_prices
		 WHERE provider_ref=$1 AND model=$2 AND revision=$3`, price.ProviderRef, price.Model, price.Revision, price.EvidenceRef, price.MaxMicrousd).Scan(&equal); err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("%w: immutable price revision", ErrProbeBudgetConflict)
		}
	}
	if !latched && enabled {
		if !validRef(budget.ActivationEvidenceRef) {
			return errors.New("dispatch probe budget activation evidence required")
		}
		var unpriced bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch.probes p
		 WHERE p.finished_at IS NULL AND p.lease_until>$1 AND NOT EXISTS
		 (SELECT 1 FROM dispatch.probe_money m WHERE m.token=p.token))`, now).Scan(&unpriced); err != nil {
			return err
		}
		if unpriced {
			return fmt.Errorf("%w: unpriced active lease", ErrProbeBudgetConflict)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.probe_budget_latch(first_core,activation_evidence_ref,activated_at)
		 VALUES ($1,$2,$3)`, core, budget.ActivationEvidenceRef, now); err != nil {
			return err
		}
	}
	body, err := json.Marshal(budget)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch.probe_budget_policies(core_instance,active,daily_microusd,provider_daily_microusd,budget)
	 VALUES ($1,$2,$3,$4,$5) ON CONFLICT(core_instance) DO UPDATE SET active=EXCLUDED.active,
	 daily_microusd=EXCLUDED.daily_microusd,provider_daily_microusd=EXCLUDED.provider_daily_microusd,budget=EXCLUDED.budget`,
		core, enabled, budget.DailyMicrousd, budget.ProviderDailyMicrousd, string(body))
	return err
}

func probeBudgetLimits(ctx context.Context, tx *sql.Tx, provider string) (sql.NullInt64, sql.NullInt64, bool, error) {
	var global, perProvider sql.NullInt64
	var missing bool
	err := tx.QueryRowContext(ctx, `WITH active AS (
	 SELECT c.core_instance,b.daily_microusd,b.provider_daily_microusd,
	 b.active AND (c.probe_limits->'probe_budget'=b.budget) AS active
	 FROM dispatch.cores c LEFT JOIN dispatch.probe_budget_policies b USING(core_instance)
	 WHERE (c.probe_limits->>'lease_duration')::bigint>0)
	 SELECT min(daily_microusd) FILTER(WHERE active),
	 (SELECT min(a.provider_daily_microusd) FROM active a WHERE a.active AND EXISTS
	 (SELECT 1 FROM dispatch.targets t WHERE t.core_instance=a.core_instance
	 AND t.metadata->>'provider_ref'=$1 AND (t.metadata->>'enabled')::boolean)),
	 EXISTS(SELECT 1 FROM active WHERE active IS DISTINCT FROM TRUE) FROM active`, provider).Scan(&global, &perProvider, &missing)
	return global, perProvider, missing, err
}

// SUM(bigint) 保持 numeric；Scan 到 int64 时溢出返回错误，不能截断或回绕。
const probeBudgetTotalsSQL = `SELECT
	COALESCE(sum(max_microusd) FILTER(WHERE state='reserved'),0)::text,
	COALESCE(sum(max_microusd) FILTER(WHERE state='unknown'),0)::text,
	COALESCE(sum(max_microusd) FILTER(WHERE state='charged' AND budget_day>=$1::date),0)::text,
	COALESCE(sum(max_microusd) FILTER(WHERE state IN ('reserved','unknown') OR budget_day>=$1::date),0)::text
	FROM dispatch.probe_money WHERE ($2='' OR provider_ref=$2)`

func probeBudgetTotals(ctx context.Context, tx *sql.Tx, day, provider string) (ProbeBudgetTotals, error) {
	var totals ProbeBudgetTotals
	err := tx.QueryRowContext(ctx, probeBudgetTotalsSQL, day, provider).Scan(&totals.ReservedMicrousd,
		&totals.UnknownMicrousd, &totals.ChargedMicrousd, &totals.ExposureMicrousd)
	return totals, err
}

func probeBudgetDay(now time.Time) string { return now.UTC().Format(time.DateOnly) }

func prepareProbeBudget(ctx context.Context, tx *sql.Tx, core string, target Target, budget *ProbeBudgetConfig, now time.Time) (*ProbeBudgetReservation, error) {
	ready, err := probeBudgetSchemaReady(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ready {
		if budget != nil {
			return nil, ErrProbeBudgetSchema
		}
		return nil, nil
	}
	latched, err := probeBudgetLatched(ctx, tx)
	if err != nil {
		return nil, err
	}
	if budget == nil {
		if latched {
			return nil, ErrProbeBudgetRequired
		}
		return nil, nil
	}
	if !latched {
		return nil, ErrProbeBudgetRequired
	}
	body, err := json.Marshal(budget)
	if err != nil {
		return nil, err
	}
	var matched bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch.probe_budget_policies
	 WHERE core_instance=$1 AND active AND budget=$2::jsonb)`, core, string(body)).Scan(&matched); err != nil {
		return nil, err
	}
	if !matched {
		return nil, ErrConfigRevision
	}
	var quote *ProbePriceBound
	for i := range budget.Prices {
		if budget.Prices[i].ProviderRef == target.ProviderRef && budget.Prices[i].Model == target.Model {
			quote = &budget.Prices[i]
			break
		}
	}
	if quote == nil {
		return nil, errors.New("dispatch probe price bound unavailable")
	}
	globalLimit, providerLimit, missing, err := probeBudgetLimits(ctx, tx, target.ProviderRef)
	if err != nil {
		return nil, err
	}
	if missing || !globalLimit.Valid || !providerLimit.Valid {
		return nil, ErrProbeBudgetRequired
	}
	day := probeBudgetDay(now)
	global, err := probeBudgetTotals(ctx, tx, day, "")
	if err != nil {
		return nil, err
	}
	provider, err := probeBudgetTotals(ctx, tx, day, target.ProviderRef)
	if err != nil {
		return nil, err
	}
	if quote.MaxMicrousd > globalLimit.Int64-global.ExposureMicrousd ||
		quote.MaxMicrousd > providerLimit.Int64-provider.ExposureMicrousd {
		return nil, ErrProbeBudgetExhausted
	}
	return &ProbeBudgetReservation{Currency: "USD", MaxMicrousd: quote.MaxMicrousd,
		ProviderRef: quote.ProviderRef, Model: quote.Model, PriceRevision: quote.Revision,
		EvidenceRef: quote.EvidenceRef, ReservedAt: now.UTC(), BudgetDay: day}, nil
}

func recordProbeBudget(ctx context.Context, tx *sql.Tx, lease ProbeLease, reservation *ProbeBudgetReservation, now time.Time) error {
	if reservation == nil {
		ready, err := probeBudgetSchemaReady(ctx, tx)
		if err != nil || !ready {
			return err
		}
		latched, err := probeBudgetLatched(ctx, tx)
		if err != nil {
			return err
		}
		if latched {
			return ErrProbeBudgetRequired
		}
		return nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO dispatch.probe_money
	 (token,currency,provider_ref,model,price_revision,evidence_ref,max_microusd,reserved_at,budget_day,state,version)
	 SELECT p.token,'USD',p.provider_ref,p.model,q.revision,q.evidence_ref,q.max_microusd,
	 p.reserved_at,(p.reserved_at AT TIME ZONE 'UTC')::date,'reserved',1 FROM dispatch.probes p
	 JOIN dispatch.probe_prices q ON q.provider_ref=p.provider_ref AND q.model=p.model
	 WHERE p.token=$1 AND p.core_instance=$2 AND q.revision=$3 AND q.evidence_ref=$4 AND q.max_microusd=$5
	 AND p.provider_ref=$6 AND p.model=$7 AND p.reserved_at=$8 AND $9='USD'
	 AND (p.reserved_at AT TIME ZONE 'UTC')::date=$10::date`, lease.Token, lease.CoreInstance,
		reservation.PriceRevision, reservation.EvidenceRef, reservation.MaxMicrousd, reservation.ProviderRef,
		reservation.Model, reservation.ReservedAt, reservation.Currency, reservation.BudgetDay)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrProbeBudgetConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch.probe_money_audit(token,version,action,recorded_at) VALUES ($1,1,'reserve',$2)`, lease.Token, now)
	return err
}

func finishProbeBudget(ctx context.Context, tx *sql.Tx, token string, knownSuccess bool, now time.Time) error {
	ready, err := probeBudgetSchemaReady(ctx, tx)
	if err != nil || !ready {
		return err
	}
	var state string
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT state,version FROM dispatch.probe_money WHERE token=$1`, token).Scan(&state, &version)
	if errors.Is(err, sql.ErrNoRows) {
		// 首次开启前的已结束历史不补算供应商费用。
		return nil
	}
	if err != nil {
		return err
	}
	if state != "reserved" {
		return ErrProbeBudgetConflict
	}
	state, action := "unknown", "unknown"
	var settled any
	if knownSuccess {
		state, action, settled = "charged", "charge-ceiling", now
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dispatch.probe_money SET state=$2,version=version+1,settled_at=$3
	 WHERE token=$1 AND version=$4`, token, state, settled, version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch.probe_money_audit(token,version,action,recorded_at) VALUES ($1,$2,$3,$4)`, token, version+1, action, now)
	return err
}

const probeBudgetEntrySQL = `SELECT m.currency,m.max_microusd,m.provider_ref,m.model,m.price_revision,
	m.evidence_ref,m.reserved_at,m.budget_day::text,m.token,p.core_instance,p.target_ref,m.state,m.version,m.settled_at,
	(SELECT a.request FROM dispatch.probe_money_audit a WHERE a.token=m.token AND a.action='manual-close')
	FROM dispatch.probe_money m JOIN dispatch.probes p USING(token)`

type probeBudgetScanner interface{ Scan(...any) error }

func scanProbeBudgetEntry(row probeBudgetScanner) (ProbeBudgetEntry, error) {
	var entry ProbeBudgetEntry
	var settled sql.NullTime
	var request []byte
	err := row.Scan(&entry.Currency, &entry.MaxMicrousd, &entry.ProviderRef, &entry.Model, &entry.PriceRevision,
		&entry.EvidenceRef, &entry.ReservedAt, &entry.BudgetDay, &entry.Token, &entry.CoreInstance, &entry.TargetRef,
		&entry.State, &entry.Version, &settled, &request)
	if err != nil {
		return entry, err
	}
	entry.ReservedAt = entry.ReservedAt.UTC()
	if settled.Valid {
		at := settled.Time.UTC()
		entry.SettledAt = &at
	}
	if request != nil {
		entry.CloseRequest = new(ProbeBudgetCloseRequest)
		if err := decodeJSON(request, entry.CloseRequest); err != nil {
			return ProbeBudgetEntry{}, err
		}
	}
	return entry, nil
}

func (s *Store) CloseProbeBudget(ctx context.Context, core string, configRevision int64, req ProbeBudgetCloseRequest) (ProbeBudgetEntry, error) {
	if !validRef(core) || configRevision < 1 {
		return ProbeBudgetEntry{}, ErrConfigRevision
	}
	if err := req.Validate(); err != nil {
		return ProbeBudgetEntry{}, err
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return ProbeBudgetEntry{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, configRevision); err != nil {
		return ProbeBudgetEntry{}, err
	}
	ready, err := probeBudgetSchemaReady(ctx, tx)
	if err != nil {
		return ProbeBudgetEntry{}, err
	}
	if !ready {
		return ProbeBudgetEntry{}, ErrProbeBudgetSchema
	}
	body, err := json.Marshal(req)
	if err != nil {
		return ProbeBudgetEntry{}, err
	}
	var equal bool
	var original []byte
	err = tx.QueryRowContext(ctx, `SELECT a.request=$3::jsonb,a.result FROM dispatch.probe_money_audit a
	 JOIN dispatch.probes p USING(token) WHERE p.core_instance=$1 AND a.token=$2 AND a.action='manual-close'`,
		core, req.Token, string(body)).Scan(&equal, &original)
	if err == nil {
		if !equal {
			return ProbeBudgetEntry{}, ErrProbeBudgetConflict
		}
		var entry ProbeBudgetEntry
		if err := decodeJSON(original, &entry); err != nil {
			return ProbeBudgetEntry{}, err
		}
		return entry, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ProbeBudgetEntry{}, err
	}
	entry, err := scanProbeBudgetEntry(tx.QueryRowContext(ctx, probeBudgetEntrySQL+` WHERE m.token=$1 AND p.core_instance=$2`, req.Token, core))
	if errors.Is(err, sql.ErrNoRows) {
		return ProbeBudgetEntry{}, ErrProbeBudgetConflict
	}
	if err != nil {
		return ProbeBudgetEntry{}, err
	}
	if entry.Version != req.ExpectedVersion || (entry.State != "reserved" && entry.State != "unknown") {
		return ProbeBudgetEntry{}, ErrProbeBudgetConflict
	}
	var eligible bool
	if err := tx.QueryRowContext(ctx, `SELECT finished_at IS NOT NULL OR lease_until<=$2 FROM dispatch.probes WHERE token=$1`, req.Token, now).Scan(&eligible); err != nil {
		return ProbeBudgetEntry{}, err
	}
	if !eligible {
		return ProbeBudgetEntry{}, ErrProbeBudgetConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE dispatch.probe_money SET state='charged',version=version+1,settled_at=$3
	 WHERE token=$1 AND version=$2 AND state IN ('reserved','unknown')`, req.Token, req.ExpectedVersion, now)
	if err != nil {
		return ProbeBudgetEntry{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ProbeBudgetEntry{}, err
	}
	if count != 1 {
		return ProbeBudgetEntry{}, ErrProbeBudgetConflict
	}
	entry.State, entry.Version, entry.SettledAt, entry.CloseRequest = "charged", entry.Version+1, &now, &req
	report, err := json.Marshal(entry)
	if err != nil {
		return ProbeBudgetEntry{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch.probe_money_audit
	 (token,version,action,actor,reason,evidence_ref,request,result,recorded_at)
	 VALUES ($1,$2,'manual-close',$3,$4,$5,$6,$7,$8)`, req.Token, entry.Version, req.Actor,
		req.Reason, req.EvidenceRef, string(body), string(report), now); err != nil {
		return ProbeBudgetEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProbeBudgetEntry{}, err
	}
	return entry, nil
}

func (s *Store) ProbeBudgetReport(ctx context.Context, core string, revision int64) (ProbeBudgetReport, error) {
	report := ProbeBudgetReport{Currency: "USD", Providers: []ProbeProviderBudgetReport{}, Entries: []ProbeBudgetEntry{}}
	if !validRef(core) || revision < 1 {
		return report, ErrConfigRevision
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return report, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkConfigRevision(ctx, tx, core, revision); err != nil {
		return report, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&report.ObservedAt); err != nil {
		return report, err
	}
	report.ObservedAt = report.ObservedAt.UTC()
	report.BudgetDay = probeBudgetDay(report.ObservedAt)
	report.SchemaReady, err = probeBudgetSchemaReady(ctx, tx)
	if err != nil {
		return report, err
	}
	if !report.SchemaReady {
		return report, tx.Commit()
	}
	activation := new(ProbeBudgetActivation)
	err = tx.QueryRowContext(ctx, `SELECT first_core,activation_evidence_ref,activated_at FROM dispatch.probe_budget_latch`).Scan(
		&activation.CoreInstance, &activation.ActivationEvidenceRef, &activation.ActivatedAt)
	if err == nil {
		activation.ActivatedAt = activation.ActivatedAt.UTC()
		report.Enabled, report.Activation = true, activation
	} else if !errors.Is(err, sql.ErrNoRows) {
		return report, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch.probe_budget_policies b
	 JOIN dispatch.cores c USING(core_instance) WHERE b.core_instance=$1 AND b.active
	 AND c.probe_limits->'probe_budget'=b.budget
	 AND (c.probe_limits->>'lease_duration')::bigint>0)`, core).Scan(&report.Active); err != nil {
		return report, err
	}
	globalLimit, _, missing, err := probeBudgetLimits(ctx, tx, "")
	if err != nil {
		return report, err
	}
	report.Fenced = report.Enabled && missing
	if globalLimit.Valid {
		report.GlobalDailyMicrousd = &globalLimit.Int64
	}
	report.Global, err = probeBudgetTotals(ctx, tx, report.BudgetDay, "")
	if err != nil {
		return report, err
	}
	providers, err := tx.QueryContext(ctx, `SELECT provider_ref FROM (
	 SELECT metadata->>'provider_ref' AS provider_ref FROM dispatch.targets WHERE core_instance=$1
	 UNION SELECT m.provider_ref FROM dispatch.probe_money m JOIN dispatch.probes p USING(token) WHERE p.core_instance=$1
	 ) AS related ORDER BY provider_ref LIMIT 1001`, core)
	if err != nil {
		return report, err
	}
	for providers.Next() {
		var provider ProbeProviderBudgetReport
		if err := providers.Scan(&provider.ProviderRef); err != nil {
			_ = providers.Close()
			return report, err
		}
		if len(report.Providers) == 1000 {
			report.ProvidersTruncated = true
			break
		}
		report.Providers = append(report.Providers, provider)
	}
	err = providers.Err()
	_ = providers.Close()
	if err != nil {
		return report, err
	}
	for i := range report.Providers {
		provider := &report.Providers[i]
		_, limit, _, err := probeBudgetLimits(ctx, tx, provider.ProviderRef)
		if err != nil {
			return report, err
		}
		if limit.Valid {
			provider.DailyMicrousd = &limit.Int64
		}
		provider.ProbeBudgetTotals, err = probeBudgetTotals(ctx, tx, report.BudgetDay, provider.ProviderRef)
		if err != nil {
			return report, err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM dispatch.probe_money m JOIN dispatch.probes p USING(token)
	 WHERE p.core_instance=$1`, core).Scan(&report.EntryCount); err != nil {
		return report, err
	}
	entries, err := tx.QueryContext(ctx, probeBudgetEntrySQL+` WHERE p.core_instance=$1
	 ORDER BY (m.state='charged'),m.reserved_at,m.token LIMIT 1000`, core)
	if err != nil {
		return report, err
	}
	for entries.Next() {
		entry, err := scanProbeBudgetEntry(entries)
		if err != nil {
			_ = entries.Close()
			return report, err
		}
		report.Entries = append(report.Entries, entry)
	}
	err = entries.Err()
	_ = entries.Close()
	if err != nil {
		return report, err
	}
	report.EntriesTruncated = report.EntryCount > int64(len(report.Entries))
	return report, tx.Commit()
}
