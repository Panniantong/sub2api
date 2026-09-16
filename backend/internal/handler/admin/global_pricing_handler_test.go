package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubGlobalPricingRepo struct {
	rows []service.GlobalModelPrice
	next int64
}

func (s *stubGlobalPricingRepo) ListEnabled(context.Context) ([]service.GlobalModelPrice, error) {
	out := make([]service.GlobalModelPrice, 0)
	for _, r := range s.rows {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *stubGlobalPricingRepo) ListAll(context.Context) ([]service.GlobalModelPrice, error) {
	return append([]service.GlobalModelPrice(nil), s.rows...), nil
}

func (s *stubGlobalPricingRepo) Create(_ context.Context, in service.GlobalModelPriceInput) (*service.GlobalModelPrice, error) {
	s.next++
	out := service.GlobalModelPrice{ID: s.next, ModelPattern: in.ModelPattern, BillingMode: in.BillingMode,
		InputPrice: in.InputPrice, OutputPrice: in.OutputPrice, Enabled: true}
	s.rows = append(s.rows, out)
	return &out, nil
}

func (s *stubGlobalPricingRepo) GetByID(_ context.Context, id int64) (*service.GlobalModelPrice, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			cp := s.rows[i]
			return &cp, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *stubGlobalPricingRepo) Update(_ context.Context, id int64, in service.GlobalModelPriceInput) (*service.GlobalModelPrice, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].ModelPattern = in.ModelPattern
			s.rows[i].Enabled = in.Enabled
			cp := s.rows[i]
			return &cp, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *stubGlobalPricingRepo) Delete(_ context.Context, id int64) error {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return nil
		}
	}
	return sql.ErrNoRows
}

func (s *stubGlobalPricingRepo) SetEnabled(_ context.Context, id int64, enabled bool) error {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Enabled = enabled
			return nil
		}
	}
	return sql.ErrNoRows
}

func newGlobalPricingTestHandler() *GlobalPricingHandler {
	return NewGlobalPricingHandler(service.NewGlobalModelPricingService(&stubGlobalPricingRepo{}))
}

func serveGlobalPricing(h *GlobalPricingHandler, method, target, body string, params gin.Params) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	c.Request = httptest.NewRequest(method, target, reader)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	switch {
	case method == http.MethodGet:
		h.List(c)
	case method == http.MethodPost && strings.HasSuffix(target, "/enable"):
		h.SetEnabled(c)
	case method == http.MethodPost:
		h.Create(c)
	case method == http.MethodPut:
		h.Update(c)
	case method == http.MethodDelete:
		h.Delete(c)
	}
	return rec
}

func TestGlobalPricingHandlerListEmpty(t *testing.T) {
	rec := serveGlobalPricing(newGlobalPricingTestHandler(), http.MethodGet, "/api/v1/admin/global-pricing", "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, 0, resp.Data.Count)
}

func TestGlobalPricingHandlerCreateAndToggle(t *testing.T) {
	h := newGlobalPricingTestHandler()
	rec := serveGlobalPricing(h, http.MethodPost, "/api/v1/admin/global-pricing",
		`{"model_pattern":"gpt-5","billing_mode":"token","input_price":0.000001}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = serveGlobalPricing(h, http.MethodPost, "/api/v1/admin/global-pricing/1/enable",
		`{"enabled":false}`, gin.Params{{Key: "id", Value: "1"}})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = serveGlobalPricing(h, http.MethodGet, "/api/v1/admin/global-pricing", "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestGlobalPricingHandlerRejectsInvalid(t *testing.T) {
	h := newGlobalPricingTestHandler()
	rec := serveGlobalPricing(h, http.MethodPost, "/api/v1/admin/global-pricing",
		`{"model_pattern":"*foo","billing_mode":"token"}`, nil)
	require.NotEqual(t, http.StatusOK, rec.Code)
}

func TestGlobalPricingHandlerSetEnabledRequiresFlag(t *testing.T) {
	h := newGlobalPricingTestHandler()
	rec := serveGlobalPricing(h, http.MethodPost, "/api/v1/admin/global-pricing/1/enable",
		`{}`, gin.Params{{Key: "id", Value: "1"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGlobalPricingHandlerNotFound(t *testing.T) {
	h := newGlobalPricingTestHandler()
	params := gin.Params{{Key: "id", Value: "999"}}
	rec := serveGlobalPricing(h, http.MethodPut, "/api/v1/admin/global-pricing/999",
		`{"model_pattern":"gpt-5"}`, params)
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = serveGlobalPricing(h, http.MethodDelete, "/api/v1/admin/global-pricing/999", "", params)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
