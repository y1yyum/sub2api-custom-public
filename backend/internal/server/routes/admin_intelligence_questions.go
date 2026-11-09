package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerIntelligenceQuestionRoutes serves the quiz question bank, the
// char art and quiz plans of all accounts, and the mapped model lookup used by
// the scheduled test plan form.
func registerIntelligenceQuestionRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	questions := admin.Group("/intelligence-questions")
	{
		questions.GET("", h.Admin.ScheduledTest.ListQuestions)
		questions.POST("", h.Admin.ScheduledTest.CreateQuestion)
		questions.POST("/grade", h.Admin.ScheduledTest.GradeQuestion)
		questions.PUT("/:id", h.Admin.ScheduledTest.UpdateQuestion)
		questions.DELETE("/:id", h.Admin.ScheduledTest.DeleteQuestion)
	}
	admin.GET("/intelligence-plans", h.Admin.ScheduledTest.ListIntelligencePlans)
	admin.GET("/accounts/:id/mapped-models", h.Admin.Account.GetMappedModels)
}
