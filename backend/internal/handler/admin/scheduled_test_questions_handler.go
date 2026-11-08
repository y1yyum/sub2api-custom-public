package admin

import (
	"errors"
	"net/http"
	"strconv"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// scheduledTestPlanKindFields are the char_art / quiz settings shared by the
// create and update requests. Nil QuestionIDs keeps the current links.
type scheduledTestPlanKindFields struct {
	FailureAction *string `json:"failure_action"`
	Prompt        *string `json:"prompt"`
	PickMode      string  `json:"pick_mode"`
	ShowInCatalog *bool   `json:"show_in_catalog"`
	QuestionIDs   []int64 `json:"question_ids"`
}

func (f scheduledTestPlanKindFields) apply(plan *service.ScheduledTestPlan) {
	if f.FailureAction != nil {
		plan.FailureAction = *f.FailureAction
	}
	if f.Prompt != nil {
		plan.Prompt = *f.Prompt
	}
	if f.PickMode != "" {
		plan.PickMode = f.PickMode
	}
	if f.ShowInCatalog != nil {
		plan.ShowInCatalog = *f.ShowInCatalog
	}
}

// respondScheduledTestError keeps application error reasons and reports other
// failures (such as an invalid cron expression) as bad requests.
func respondScheduledTestError(c *gin.Context, err error) {
	var appErr *infraerrors.ApplicationError
	if errors.As(err, &appErr) {
		response.ErrorFrom(c, err)
		return
	}
	response.BadRequest(c, err.Error())
}

type intelligenceQuestionRequest struct {
	Prompt          string   `json:"prompt"`
	PromptImage     string   `json:"prompt_image"`
	AnswerRule      string   `json:"answer_rule"`
	ExpectedAnswers []string `json:"expected_answers"`
	Tolerance       float64  `json:"tolerance"`
	Enabled         *bool    `json:"enabled"`
}

func (r intelligenceQuestionRequest) question() *service.IntelligenceQuestion {
	q := &service.IntelligenceQuestion{
		Prompt:          r.Prompt,
		PromptImage:     r.PromptImage,
		AnswerRule:      r.AnswerRule,
		ExpectedAnswers: r.ExpectedAnswers,
		Tolerance:       r.Tolerance,
		Enabled:         true,
	}
	if r.Enabled != nil {
		q.Enabled = *r.Enabled
	}
	return q
}

type gradeIntelligenceQuestionRequest struct {
	AnswerRule      string   `json:"answer_rule"`
	ExpectedAnswers []string `json:"expected_answers"`
	Tolerance       float64  `json:"tolerance"`
	ResponseText    string   `json:"response_text"`
}

// ListQuestions GET /admin/intelligence-questions
func (h *ScheduledTestHandler) ListQuestions(c *gin.Context) {
	questions, err := h.scheduledTestSvc.ListQuestions(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if questions == nil {
		questions = []*service.IntelligenceQuestion{}
	}
	c.JSON(http.StatusOK, questions)
}

// CreateQuestion POST /admin/intelligence-questions
func (h *ScheduledTestHandler) CreateQuestion(c *gin.Context) {
	var req intelligenceQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	created, err := h.scheduledTestSvc.CreateQuestion(c.Request.Context(), req.question())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, created)
}

// UpdateQuestion PUT /admin/intelligence-questions/:id; omitted enabled keeps the current state.
func (h *ScheduledTestHandler) UpdateQuestion(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid question id")
		return
	}
	var req intelligenceQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	existing, err := h.scheduledTestSvc.GetQuestion(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	q := req.question()
	q.ID = id
	if req.Enabled == nil {
		q.Enabled = existing.Enabled
	}
	updated, err := h.scheduledTestSvc.UpdateQuestion(c.Request.Context(), q)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

// DeleteQuestion DELETE /admin/intelligence-questions/:id
func (h *ScheduledTestHandler) DeleteQuestion(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid question id")
		return
	}
	if err := h.scheduledTestSvc.DeleteQuestion(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// GradeQuestion POST /admin/intelligence-questions/grade previews grading for an unsaved question.
func (h *ScheduledTestHandler) GradeQuestion(c *gin.Context) {
	var req gradeIntelligenceQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	passed, err := h.scheduledTestSvc.GradePreview(&service.IntelligenceQuestion{
		AnswerRule:      req.AnswerRule,
		ExpectedAnswers: req.ExpectedAnswers,
		Tolerance:       req.Tolerance,
	}, req.ResponseText)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"passed": passed})
}
