package admin

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGroupStatsUnavailableDoesNotReportInventedZeroTotals(t *testing.T) {
	h := NewGroupHandlerWithConfig(&stubAdminService{}, nil, nil, &config.Config{})
	r := gin.New()
	r.GET("/groups/:id/stats", h.GetStats)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/groups/1/stats", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_STATS_UNAVAILABLE")
	require.NotContains(t, w.Body.String(), "total_cost")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/groups/-1/stats", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

type groupDetailStatsStub struct {
	service.UsageLogRepository
	calls int
}

func (s *groupDetailStatsStub) GetGroupDetailStats(_ context.Context, id int64, from, to *time.Time) (*service.GroupDetailStats, error) {
	s.calls++
	return &service.GroupDetailStats{GroupID: id, TotalCost: 32, TotalActualCost: 16, TotalAccountCost: 13, From: from, To: to}, nil
}
func TestGroupStatsReportsDistinctCostsAndRejectsInvalidDates(t *testing.T) {
	for _, mode := range []string{"", config.RunModeSimple} {
		repo := &groupDetailStatsStub{}
		dashboard := service.NewDashboardService(repo, nil, nil, nil)
		h := NewGroupHandlerWithConfig(&stubAdminService{}, dashboard, nil, &config.Config{RunMode: mode})
		r := gin.New()
		r.GET("/groups/:id/stats", h.GetStats)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/groups/42/stats", nil))
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), `"total_cost":32`)
		require.Contains(t, w.Body.String(), `"total_actual_cost":16`)
		require.Contains(t, w.Body.String(), `"total_account_cost":13`)
		for _, query := range []string{"?from=bad", "?from=2026-09-14T00:00:00Z&to=2026-09-13T00:00:00Z"} {
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/groups/42/stats"+query, nil))
			require.Equal(t, 400, w.Code)
		}
		require.Equal(t, 1, repo.calls)
	}
}
