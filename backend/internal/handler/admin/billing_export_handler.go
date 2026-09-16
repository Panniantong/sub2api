package admin

import (
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BillingExportHandler 账单：用户月账单与用量 CSV 导出，管理员可查任意用户。
type BillingExportHandler struct {
	service *service.BillingExportService
}

func NewBillingExportHandler(svc *service.BillingExportService) *BillingExportHandler {
	return &BillingExportHandler{service: svc}
}

func (h *BillingExportHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("billing export service unavailable"))
		return false
	}
	return true
}

// Statement 用户月账单：?year=&month=。
// GET /billing/statement
func (h *BillingExportHandler) Statement(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	now := time.Now().UTC()
	year, month := now.Year(), int(now.Month())
	if raw := c.Query("year"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			year = v
		}
	}
	if raw := c.Query("month"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			month = v
		}
	}
	stmt, err := h.service.Statement(c.Request.Context(), subject.UserID, year, month)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stmt)
}

// Export 用户用量 CSV：?start=RFC3339&end=RFC3339（≤31 天）。
// GET /billing/export
func (h *BillingExportHandler) Export(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	start, end, ok := billingExportRange(c)
	if !ok {
		return
	}
	data, filename, err := h.service.ExportCSV(c.Request.Context(), subject.UserID, start, end)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(200, "text/csv; charset=utf-8", data)
}

// AdminStatement 管理员查任意用户月账单。
// GET /api/v1/admin/billing/users/:userId/statement?year=&month=
func (h *BillingExportHandler) AdminStatement(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	userID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	now := time.Now().UTC()
	year, month := now.Year(), int(now.Month())
	if raw := c.Query("year"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			year = v
		}
	}
	if raw := c.Query("month"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			month = v
		}
	}
	stmt, err := h.service.Statement(c.Request.Context(), userID, year, month)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stmt)
}

// AdminExport 管理员导出任意用户用量 CSV。
// GET /api/v1/admin/billing/users/:userId/export?start=&end=
func (h *BillingExportHandler) AdminExport(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	userID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	start, end, ok := billingExportRange(c)
	if !ok {
		return
	}
	data, filename, err := h.service.ExportCSV(c.Request.Context(), userID, start, end)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(200, "text/csv; charset=utf-8", data)
}

func billingExportRange(c *gin.Context) (time.Time, time.Time, bool) {
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -7)
	if raw := c.Query("start"); raw != "" {
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, "Invalid start, use RFC3339")
			return time.Time{}, time.Time{}, false
		}
		start = v
	}
	if raw := c.Query("end"); raw != "" {
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, "Invalid end, use RFC3339")
			return time.Time{}, time.Time{}, false
		}
		end = v
	}
	return start, end, true
}
