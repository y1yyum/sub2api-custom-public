package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type memoryScheduledPlanRepo struct {
	service.ScheduledTestPlanRepository
	plans map[int64]*service.ScheduledTestPlan
}

func (r *memoryScheduledPlanRepo) Create(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	saved := *plan
	saved.ID = int64(len(r.plans) + 1)
	r.plans[saved.ID] = &saved
	return &saved, nil
}

func (r *memoryScheduledPlanRepo) GetByID(_ context.Context, id int64) (*service.ScheduledTestPlan, error) {
	saved := *r.plans[id]
	return &saved, nil
}

func (r *memoryScheduledPlanRepo) Update(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	saved := *plan
	r.plans[plan.ID] = &saved
	return &saved, nil
}

type memoryQuestionRepo struct {
	service.IntelligenceQuestionRepository
	links map[int64][]int64
}

func (r *memoryQuestionRepo) SetPlanQuestions(_ context.Context, planID int64, ids []int64) error {
	r.links[planID] = ids
	return nil
}

func (r *memoryQuestionRepo) ListPlanQuestionIDs(_ context.Context, planIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	for _, id := range planIDs {
		out[id] = r.links[id]
	}
	return out, nil
}

type memoryQuestionImageRepo struct {
	service.IntelligenceQuestionRepository
	question *service.IntelligenceQuestion
}

func (r *memoryQuestionImageRepo) Create(_ context.Context, q *service.IntelligenceQuestion) (*service.IntelligenceQuestion, error) {
	q.ID = 1
	r.question = q
	return q, nil
}

func (r *memoryQuestionImageRepo) GetByID(context.Context, int64) (*service.IntelligenceQuestion, error) {
	return r.question, nil
}

func (r *memoryQuestionImageRepo) Update(ctx context.Context, q *service.IntelligenceQuestion) (*service.IntelligenceQuestion, error) {
	return r.Create(ctx, q)
}

func TestScheduledTestQuestionImageAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &memoryQuestionImageRepo{}
	h := NewScheduledTestHandler(service.ProvideScheduledTestService(nil, nil, repo))
	r := gin.New()
	r.POST("/questions", h.CreateQuestion)
	r.PUT("/questions/:id", h.UpdateQuestion)
	const image = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4//8/AAX+Av4N70a4AAAAAElFTkSuQmCC"
	body := map[string]any{"prompt": "question", "prompt_image": image, "answer_rule": "exact", "expected_answers": []string{"answer"}}
	rec, result := doScheduledTestJSON(t, r, http.MethodPost, "/questions", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, image, result["prompt_image"])
	require.Equal(t, image, repo.question.PromptImage)
	body["prompt"] = "edited"
	rec, result = doScheduledTestJSON(t, r, http.MethodPut, "/questions/1", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, image, result["prompt_image"])
	require.Equal(t, "edited", repo.question.Prompt)
	delete(body, "prompt_image")
	rec, _ = doScheduledTestJSON(t, r, http.MethodPut, "/questions/1", body)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, image, repo.question.PromptImage)
}

func newScheduledTestKindsRouter() (*gin.Engine, *memoryQuestionRepo) {
	gin.SetMode(gin.TestMode)
	questions := &memoryQuestionRepo{links: map[int64][]int64{}}
	svc := service.ProvideScheduledTestService(&memoryScheduledPlanRepo{plans: map[int64]*service.ScheduledTestPlan{}}, nil, questions)
	h := NewScheduledTestHandler(svc)
	r := gin.New()
	r.POST("/plans", h.Create)
	r.PUT("/plans/:id", h.Update)
	r.POST("/questions/grade", h.GradeQuestion)
	return r, questions
}

func doScheduledTestJSON(t *testing.T, r *gin.Engine, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return rec, out
}

func TestScheduledTestHandlerCreatesKindPlans(t *testing.T) {
	r, questions := newScheduledTestKindsRouter()

	rec, quiz := doScheduledTestJSON(t, r, http.MethodPost, "/plans", map[string]any{
		"account_id": 1, "cron_expression": "0 * * * *", "kind": "quiz", "question_ids": []int64{4, 2}, "auto_recover": true,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "quiz", quiz["kind"])
	require.Equal(t, true, quiz["show_in_catalog"], "char_art and quiz plans show in the catalog by default")
	require.Equal(t, false, quiz["auto_recover"])
	require.Equal(t, []any{float64(4), float64(2)}, quiz["question_ids"])
	require.Equal(t, []int64{4, 2}, questions.links[1])

	rec, charArt := doScheduledTestJSON(t, r, http.MethodPost, "/plans", map[string]any{
		"account_id": 1, "cron_expression": "0 * * * *", "kind": "char_art", "prompt": "draw", "show_in_catalog": false,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, false, charArt["show_in_catalog"])
	require.Equal(t, "draw", charArt["prompt"])

	rec, probe := doScheduledTestJSON(t, r, http.MethodPost, "/plans", map[string]any{
		"account_id": 1, "cron_expression": "0 * * * *",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "probe", probe["kind"])
	require.Equal(t, false, probe["show_in_catalog"])

	rec, failed := doScheduledTestJSON(t, r, http.MethodPost, "/plans", map[string]any{
		"account_id": 1, "cron_expression": "0 * * * *", "kind": "char_art",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "SCHEDULED_TEST_PLAN_INVALID", failed["reason"])
}

func TestScheduledTestHandlerUpdateKeepsKind(t *testing.T) {
	r, questions := newScheduledTestKindsRouter()
	rec, _ := doScheduledTestJSON(t, r, http.MethodPost, "/plans", map[string]any{
		"account_id": 1, "cron_expression": "0 * * * *", "kind": "quiz", "question_ids": []int64{1},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec, updated := doScheduledTestJSON(t, r, http.MethodPut, "/plans/1", map[string]any{
		"kind": "probe", "pick_mode": "random", "show_in_catalog": false,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "quiz", updated["kind"])
	require.Equal(t, "random", updated["pick_mode"])
	require.Equal(t, false, updated["show_in_catalog"])
	require.Equal(t, []any{float64(1)}, updated["question_ids"])

	rec, _ = doScheduledTestJSON(t, r, http.MethodPut, "/plans/1", map[string]any{"question_ids": []int64{}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, questions.links[1])
}

func TestScheduledTestHandlerGradePreview(t *testing.T) {
	r, _ := newScheduledTestKindsRouter()
	rec, out := doScheduledTestJSON(t, r, http.MethodPost, "/questions/grade", map[string]any{
		"answer_rule": "number", "expected_answers": []string{"42"}, "response_text": "答案：４２",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, true, out["passed"])

	rec, out = doScheduledTestJSON(t, r, http.MethodPost, "/questions/grade", map[string]any{
		"answer_rule": "regex", "expected_answers": []string{"("}, "response_text": "x",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INTELLIGENCE_QUESTION_INVALID", out["reason"])
}
