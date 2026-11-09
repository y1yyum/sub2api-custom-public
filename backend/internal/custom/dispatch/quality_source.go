package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/lib/pq"
)

type QualityBinding struct {
	PlanID    int64  `json:"plan_id"`
	TargetRef string `json:"target_ref"`
	Kind      string `json:"kind"`
}

// LegacyQualityResult is an operator-bound legacy result, not credential or
// region evidence. Scoring must treat non-success statuses as unknown.
type LegacyQualityResult struct {
	ResultID, PlanID, AccountID                                          int64
	TargetRef, Model, Kind, QuestionID, Prompt, Response, ExpectedAnswer string
	Verdict, Status                                                      string
	StartedAt, FinishedAt                                                time.Time
}

const (
	qualitySourceTextLimit  = 64 << 10
	qualitySourceBatchLimit = 8 << 20
)

type qualityPlan struct {
	ID, AccountID, PolicyVersion int64
	Model, Kind, Prompt          string
	Enabled                      bool
	CreatedAt, UpdatedAt         time.Time
}

type qualityActivation struct {
	Binding QualityBinding
	Target  Target
	Plan    qualityPlan
}

// QualitySource never runs tests or writes their tables. A new instance cannot
// reconstruct an activation after updated_at has advanced past its cutoff.
// Operators must drain old runners before binding, keep the complete target
// identity stable until rebinding, and change plans through the versioned API.
type QualitySource struct {
	db          *sql.DB
	mu          sync.Mutex
	notBefore   time.Time
	activations map[int64]qualityActivation
}

func NewQualitySource(ctx context.Context, db *sql.DB) (*QualitySource, error) {
	if db == nil {
		return nil, errors.New("invalid dispatch quality source")
	}
	s := &QualitySource{db: db}
	if err := checkQualityPrivileges(ctx, db); err != nil {
		return nil, err
	}
	return s, nil
}

type qualityQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkQualityPrivileges(ctx context.Context, db qualityQuerier) error {
	var unsafe bool
	err := db.QueryRowContext(ctx, `WITH allowed(rel,cols) AS (VALUES
	 ('public.scheduled_test_plans',ARRAY['id','account_id','model_id','kind','prompt','enabled','created_at','updated_at','policy_version']),
	 ('public.scheduled_test_results',ARRAY['id','plan_id','model_id','kind','question_id','prompt','response_text','expected_answer','verdict','status','started_at','finished_at']))
	 SELECT EXISTS(SELECT 1 FROM pg_roles p WHERE pg_has_role(current_user,p.oid,'MEMBER')
	 AND (p.rolsuper OR p.rolcreaterole OR p.rolcreatedb OR p.rolbypassrls OR p.rolname LIKE 'pg\_%'))
	 OR has_database_privilege(current_user,current_database(),'CREATE')
	 OR NOT has_schema_privilege(current_user,'public','USAGE')
	 OR EXISTS(SELECT 1 FROM pg_database d WHERE d.datname=current_database() AND pg_has_role(current_user,d.datdba,'MEMBER'))
	 OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname NOT LIKE 'pg\_%' AND n.nspname<>'information_schema'
	 AND (has_schema_privilege(current_user,n.oid,'CREATE') OR pg_has_role(current_user,n.nspowner,'MEMBER')))
	 OR EXISTS(SELECT 1 FROM allowed t,unnest(t.cols) col WHERE NOT has_column_privilege(current_user,t.rel,col,'SELECT'))
	 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
	 WHERE n.nspname NOT LIKE 'pg\_%' AND n.nspname<>'information_schema' AND c.relkind IN ('r','p','v','m','f','S')
	 AND (pg_has_role(current_user,c.relowner,'MEMBER')
	 OR (c.relkind='S' AND has_sequence_privilege(current_user,c.oid,'USAGE,SELECT,UPDATE'))
	 OR (c.relkind<>'S' AND (has_table_privilege(current_user,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
	 OR has_any_column_privilege(current_user,c.oid,'INSERT,UPDATE,REFERENCES')
	 OR EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
	 AND has_column_privilege(current_user,c.oid,a.attnum,'SELECT')
	 AND NOT EXISTS(SELECT 1 FROM allowed t WHERE t.rel::regclass=c.oid AND a.attname=ANY(t.cols)))))))`).Scan(&unsafe)
	if err != nil || unsafe {
		return errors.New("dispatch quality source requires a restricted column SELECT-only role")
	}
	return nil
}

func validateQualityBindings(bindings []QualityBinding, targets []Target) (map[int64]qualityActivation, error) {
	if len(bindings) < 1 || len(bindings) > MaxCandidates || len(targets) > MaxCandidates {
		return nil, errors.New("dispatch quality binding limit exceeded")
	}
	known := make(map[string]Target, len(targets))
	for _, target := range targets {
		if err := target.validate(); err != nil {
			return nil, err
		}
		if _, exists := known[target.Ref]; exists {
			return nil, errors.New("duplicate dispatch quality target")
		}
		known[target.Ref] = target
	}
	result := make(map[int64]qualityActivation, len(bindings))
	for _, binding := range bindings {
		target, exists := known[binding.TargetRef]
		if binding.PlanID < 1 || !exists || !target.Enabled || (binding.Kind != "char_art" && binding.Kind != "quiz") {
			return nil, errors.New("invalid dispatch quality binding")
		}
		_, duplicate := result[binding.PlanID]
		if duplicate {
			return nil, errors.New("ambiguous dispatch quality binding")
		}
		result[binding.PlanID] = qualityActivation{Binding: binding, Target: target}
	}
	return result, nil
}

// Read rescans seven days by source timestamps, never by a sequence cursor.
// The caller owns persistent deduplication and activation/config fencing. A
// changed binding, target, or plan requires a strictly newer activation cutoff.
func (s *QualitySource) Read(ctx context.Context, bindings []QualityBinding, targets []Target, notBefore time.Time, maxResults int) ([]LegacyQualityResult, error) {
	bound, err := validateQualityBindings(bindings, targets)
	if err != nil {
		return nil, err
	}
	if notBefore.IsZero() || maxResults < 1 || maxResults > 10000 {
		return nil, errors.New("invalid dispatch quality read limits")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.notBefore.IsZero() && notBefore.Before(s.notBefore) {
		return nil, errors.New("dispatch quality activation cannot move backwards")
	}
	if notBefore.Equal(s.notBefore) {
		if len(s.activations) != len(bound) {
			return nil, errors.New("dispatch quality bindings require a fresh activation")
		}
		for id, activation := range bound {
			previous, exists := s.activations[id]
			if !exists || previous.Binding != activation.Binding || previous.Target != activation.Target {
				return nil, errors.New("dispatch quality binding changed after activation")
			}
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, errors.New("dispatch quality source unavailable")
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkQualityPrivileges(ctx, tx); err != nil {
		return nil, err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, errors.New("dispatch quality source clock unavailable")
	}
	if notBefore.After(now) {
		return nil, errors.New("dispatch quality activation is in the future")
	}
	ids := make([]int64, 0, len(bound))
	for id := range bound {
		ids = append(ids, id)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,account_id,model_id,kind,enabled,created_at,updated_at,policy_version,
	 CASE WHEN octet_length(prompt)<=$2 THEN prompt ELSE NULL END
	 FROM public.scheduled_test_plans WHERE id=ANY($1)`, pq.Array(ids), qualitySourceTextLimit)
	if err != nil {
		return nil, errors.New("dispatch quality plans unavailable")
	}
	planBytes := 0
	for rows.Next() {
		var plan qualityPlan
		if err := rows.Scan(&plan.ID, &plan.AccountID, &plan.Model, &plan.Kind, &plan.Enabled, &plan.CreatedAt, &plan.UpdatedAt, &plan.PolicyVersion, &plan.Prompt); err != nil {
			_ = rows.Close()
			return nil, errors.New("invalid or oversized dispatch quality plan")
		}
		activation := bound[plan.ID]
		planBytes += len(plan.Prompt)
		if planBytes > qualitySourceBatchLimit {
			_ = rows.Close()
			return nil, errors.New("dispatch quality plans exceed text limit")
		}
		if !plan.Enabled || plan.AccountID != activation.Target.AccountID || plan.Model != activation.Target.Model || plan.Kind != activation.Binding.Kind ||
			plan.CreatedAt.After(notBefore) || plan.CreatedAt.After(plan.UpdatedAt) || plan.UpdatedAt.After(now) || plan.PolicyVersion < 0 {
			_ = rows.Close()
			return nil, errors.New("dispatch quality plan identity mismatch")
		}
		previous, exists := s.activations[plan.ID]
		if notBefore.Equal(s.notBefore) && exists {
			if previous.Binding != activation.Binding || previous.Target != activation.Target || !sameQualityPlan(previous.Plan, plan) || plan.UpdatedAt.Before(previous.Plan.UpdatedAt) {
				_ = rows.Close()
				return nil, errors.New("dispatch quality binding changed after activation")
			}
		} else if plan.UpdatedAt.After(notBefore) {
			_ = rows.Close()
			return nil, errors.New("dispatch quality plan requires a fresh activation")
		}
		activation.Plan = plan
		bound[plan.ID] = activation
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, errors.New("dispatch quality plans unavailable")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("dispatch quality plans unavailable")
	}
	for _, activation := range bound {
		if activation.Plan.ID == 0 {
			return nil, errors.New("dispatch quality bound plan is missing")
		}
	}
	cutoff := now.Add(-7 * 24 * time.Hour)
	if notBefore.After(cutoff) {
		cutoff = notBefore
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,plan_id,model_id,kind,COALESCE(question_id::text,''),verdict,status,started_at,finished_at,
	 CASE WHEN octet_length(prompt)<=$4 THEN prompt ELSE NULL END,
	 CASE WHEN octet_length(response_text)<=$4 THEN response_text ELSE NULL END,
	 CASE WHEN octet_length(expected_answer)<=$4 THEN expected_answer ELSE NULL END
	 FROM public.scheduled_test_results WHERE plan_id=ANY($1) AND started_at >= $2 AND started_at <= $3
	 ORDER BY started_at,id LIMIT $5`, pq.Array(ids), cutoff, now, qualitySourceTextLimit, maxResults+1)
	if err != nil {
		return nil, errors.New("dispatch quality results unavailable")
	}
	defer func() { _ = rows.Close() }()
	results := make([]LegacyQualityResult, 0)
	textBytes := 0
	for rows.Next() {
		var result LegacyQualityResult
		if err := rows.Scan(&result.ResultID, &result.PlanID, &result.Model, &result.Kind, &result.QuestionID, &result.Verdict, &result.Status,
			&result.StartedAt, &result.FinishedAt, &result.Prompt, &result.Response, &result.ExpectedAnswer); err != nil {
			return nil, errors.New("invalid or oversized dispatch quality result")
		}
		activation := bound[result.PlanID]
		if result.ResultID < 1 || result.Model != activation.Target.Model || result.Kind != activation.Binding.Kind ||
			result.FinishedAt.Before(result.StartedAt) || result.FinishedAt.After(now) ||
			(result.Kind == "char_art" && result.Prompt != activation.Plan.Prompt) {
			return nil, errors.New("dispatch quality result identity mismatch")
		}
		result.AccountID, result.TargetRef = activation.Target.AccountID, activation.Target.Ref
		textBytes += len(result.Prompt) + len(result.Response) + len(result.ExpectedAnswer)
		if textBytes > qualitySourceBatchLimit || len(results) == maxResults {
			return nil, errors.New("dispatch quality result window exceeds limit")
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("dispatch quality results unavailable")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("dispatch quality results unavailable")
	}
	if err := tx.Commit(); err != nil {
		return nil, errors.New("dispatch quality read did not commit")
	}
	s.notBefore, s.activations = notBefore, bound
	return results, nil
}

func sameQualityPlan(a, b qualityPlan) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	return a == b
}
