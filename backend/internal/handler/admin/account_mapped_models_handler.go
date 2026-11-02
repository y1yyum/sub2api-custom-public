package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

const maxMappedModelQuery = 500

// GetMappedModels GET /admin/accounts/:id/mapped-models?model=a&model=b
// returns the account's mapped upstream name for each requested model so the
// scheduled test form can label models by what the account actually calls.
func (h *AccountHandler) GetMappedModels(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	models := c.QueryArray("model")
	if len(models) > maxMappedModelQuery {
		response.BadRequest(c, "Too many models")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.NotFound(c, "Account not found")
		return
	}
	mapped := make(map[string]string, len(models))
	for _, model := range models {
		if model != "" {
			mapped[model] = account.GetMappedModel(model)
		}
	}
	response.Success(c, mapped)
}
