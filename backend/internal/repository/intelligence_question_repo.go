package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const intelligenceQuestionColumns = `q.id, q.prompt, q.answer_rule, q.expected_answers, q.tolerance, q.enabled,
	(SELECT COUNT(*) FROM scheduled_test_plan_questions l WHERE l.question_id = q.id), q.created_at, q.updated_at`

type intelligenceQuestionRepository struct {
	db *sql.DB
}

func NewIntelligenceQuestionRepository(db *sql.DB) service.IntelligenceQuestionRepository {
	return &intelligenceQuestionRepository{db: db}
}

func (r *intelligenceQuestionRepository) List(ctx context.Context) ([]*service.IntelligenceQuestion, error) {
	return r.query(ctx, `SELECT `+intelligenceQuestionColumns+` FROM intelligence_questions q ORDER BY q.id`)
}

func (r *intelligenceQuestionRepository) GetByID(ctx context.Context, id int64) (*service.IntelligenceQuestion, error) {
	return scanIntelligenceQuestion(r.db.QueryRowContext(ctx,
		`SELECT `+intelligenceQuestionColumns+` FROM intelligence_questions q WHERE q.id = $1`, id))
}

func (r *intelligenceQuestionRepository) Create(ctx context.Context, q *service.IntelligenceQuestion) (*service.IntelligenceQuestion, error) {
	answers, err := json.Marshal(q.ExpectedAnswers)
	if err != nil {
		return nil, err
	}
	var id int64
	if err := r.db.QueryRowContext(ctx, `
		INSERT INTO intelligence_questions (prompt, answer_rule, expected_answers, tolerance, enabled)
		VALUES ($1, $2, $3, $4, $5) RETURNING id
	`, q.Prompt, q.AnswerRule, answers, q.Tolerance, q.Enabled).Scan(&id); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

func (r *intelligenceQuestionRepository) Update(ctx context.Context, q *service.IntelligenceQuestion) (*service.IntelligenceQuestion, error) {
	answers, err := json.Marshal(q.ExpectedAnswers)
	if err != nil {
		return nil, err
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE intelligence_questions
		SET prompt = $2, answer_rule = $3, expected_answers = $4, tolerance = $5, enabled = $6, updated_at = NOW()
		WHERE id = $1
	`, q.ID, q.Prompt, q.AnswerRule, answers, q.Tolerance, q.Enabled)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return nil, service.ErrIntelligenceQuestionNotFound
	}
	return r.GetByID(ctx, q.ID)
}

func (r *intelligenceQuestionRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM intelligence_questions WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return service.ErrIntelligenceQuestionNotFound
	}
	return nil
}

func (r *intelligenceQuestionRepository) ListEnabledForPlan(ctx context.Context, planID int64) ([]*service.IntelligenceQuestion, error) {
	return r.query(ctx, `
		SELECT `+intelligenceQuestionColumns+`
		FROM scheduled_test_plan_questions pq
		JOIN intelligence_questions q ON q.id = pq.question_id
		WHERE pq.plan_id = $1 AND q.enabled
		ORDER BY pq.position, q.id
	`, planID)
}

func (r *intelligenceQuestionRepository) ListPlanQuestionIDs(ctx context.Context, planIDs []int64) (map[int64][]int64, error) {
	out := make(map[int64][]int64, len(planIDs))
	if len(planIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT plan_id, question_id FROM scheduled_test_plan_questions
		WHERE plan_id = ANY($1) ORDER BY plan_id, position, question_id
	`, pq.Array(planIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var planID, questionID int64
		if err := rows.Scan(&planID, &questionID); err != nil {
			return nil, err
		}
		out[planID] = append(out[planID], questionID)
	}
	return out, rows.Err()
}

func (r *intelligenceQuestionRepository) SetPlanQuestions(ctx context.Context, planID int64, questionIDs []int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_plan_questions WHERE plan_id = $1`, planID); err != nil {
		return err
	}
	if len(questionIDs) > 0 {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO scheduled_test_plan_questions (plan_id, question_id, position)
			SELECT $1, q.id, ids.ord - 1
			FROM unnest($2::bigint[]) WITH ORDINALITY AS ids(id, ord)
			JOIN intelligence_questions q ON q.id = ids.id
		`, planID, pq.Array(questionIDs))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != int64(len(questionIDs)) {
			return service.ErrIntelligenceQuestionNotFound
		}
	}
	return tx.Commit()
}

func (r *intelligenceQuestionRepository) AdvanceRotation(ctx context.Context, planID int64) (int, error) {
	var cursor int
	err := r.db.QueryRowContext(ctx, `
		UPDATE scheduled_test_plans SET rotation_cursor = rotation_cursor + 1
		WHERE id = $1 RETURNING rotation_cursor - 1
	`, planID).Scan(&cursor)
	return cursor, err
}

func (r *intelligenceQuestionRepository) query(ctx context.Context, query string, args ...any) ([]*service.IntelligenceQuestion, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var questions []*service.IntelligenceQuestion
	for rows.Next() {
		q, err := scanIntelligenceQuestion(rows)
		if err != nil {
			return nil, err
		}
		questions = append(questions, q)
	}
	return questions, rows.Err()
}

func scanIntelligenceQuestion(row scannable) (*service.IntelligenceQuestion, error) {
	q := &service.IntelligenceQuestion{}
	var answers []byte
	if err := row.Scan(&q.ID, &q.Prompt, &q.AnswerRule, &answers, &q.Tolerance, &q.Enabled, &q.PlanCount, &q.CreatedAt, &q.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrIntelligenceQuestionNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(answers, &q.ExpectedAnswers); err != nil {
		return nil, err
	}
	return q, nil
}
