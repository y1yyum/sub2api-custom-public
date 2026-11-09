package scheduledowner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

type snapshot struct {
	JSON          []byte
	Digest        string
	Count, MaxID  int64
	Latest        *time.Time
	ResultsDigest string
	Enabled       bool
	Due           *time.Time
}

// Progress is owned by Core and is deliberately excluded from the edit fence.
// Question content, grading rules, ordering, enabled flags and policy versions
// remain part of that fence.
func capture(ctx context.Context, q querier, id int64, history bool) (snapshot, error) {
	var s snapshot
	var raw, stable []byte
	err := q.QueryRowContext(ctx, `WITH current AS (
 SELECT to_jsonb(p) AS plan,COALESCE((SELECT jsonb_agg(jsonb_build_object('position',l.position,'question',to_jsonb(q)) ORDER BY l.position,q.id)
 FROM public.scheduled_test_plan_questions l JOIN public.intelligence_questions q ON q.id=l.question_id WHERE l.plan_id=p.id),'[]'::jsonb) AS questions,
 p.enabled,p.next_run_at FROM public.scheduled_test_plans p WHERE p.id=$1), document AS (
 SELECT jsonb_build_object('plan',plan,'questions',questions) AS document_body,
 jsonb_build_object('plan',plan-ARRAY['updated_at','last_run_at','next_run_at','rotation_cursor','last_quiz_outcome_at'],'questions',questions) AS stable,
 enabled,next_run_at FROM current)
 SELECT CASE WHEN octet_length(document_body::text)<=8388608 THEN document_body::text END,
 CASE WHEN octet_length(stable::text)<=8388608 THEN stable::text END,enabled,next_run_at FROM document`, id).Scan(&raw, &stable, &s.Enabled, &s.Due)
	if err != nil {
		return s, err
	}
	if len(raw) == 0 || len(stable) == 0 {
		return s, errors.New("scheduled owner plan snapshot exceeds 8 MiB")
	}
	s.JSON = raw
	s.Digest = fmt.Sprintf("%x", sha256.Sum256(stable))
	if !history {
		return s, nil
	}
	// Always rescan every committed row. A sequence ID is not a commit cursor.
	rows, err := q.QueryContext(ctx, `SELECT id,finished_at,octet_length(to_jsonb(r)::text),
 CASE WHEN octet_length(to_jsonb(r)::text)<=8388608 THEN sha256(convert_to(to_jsonb(r)::text,'UTF8')) END
 FROM public.scheduled_test_results r WHERE plan_id=$1 ORDER BY id`, id)
	if err != nil {
		return s, err
	}
	defer func() { _ = rows.Close() }()
	hash := sha256.New()
	var bytes int64
	for rows.Next() {
		var rowID, size int64
		var finished time.Time
		var digest []byte
		if err = rows.Scan(&rowID, &finished, &size, &digest); err != nil {
			return s, err
		}
		bytes += size
		if len(digest) != sha256.Size || bytes > 256<<20 || s.Count >= 1000000 {
			return s, errors.New("scheduled owner result watermark exceeds row, 256 MiB, or 1000000-row limit")
		}
		s.Count++
		if rowID > s.MaxID {
			s.MaxID = rowID
		}
		if s.Latest == nil || finished.After(*s.Latest) {
			copyTime := finished
			s.Latest = &copyTime
		}
		_, _ = hash.Write(digest)
	}
	if err = rows.Err(); err != nil {
		return s, err
	}
	s.ResultsDigest = fmt.Sprintf("%x", hash.Sum(nil))
	return s, nil
}
