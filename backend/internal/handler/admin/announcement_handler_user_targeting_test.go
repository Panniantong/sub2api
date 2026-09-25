package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type announcementCreateCapture struct {
	service.AnnouncementRepository
	created []*service.Announcement
}

func (r *announcementCreateCapture) Create(_ context.Context, a *service.Announcement) error {
	a.ID = int64(len(r.created) + 1)
	r.created = append(r.created, a)
	return nil
}

func newAnnouncementCreateTestRouter(repo *announcementCreateCapture) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewAnnouncementService(repo, &announcementReadRepoCapture{}, &announcementUserRepoCapture{}, &announcementUserSubRepoCapture{})
	handler := NewAnnouncementHandler(svc)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1})
		c.Next()
	})
	router.POST("/admin/announcements", handler.Create)
	return router
}

func postAnnouncement(router *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/admin/announcements", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAdminAnnouncementCreateTargetsSpecificUsers(t *testing.T) {
	repo := &announcementCreateCapture{}
	router := newAnnouncementCreateTestRouter(repo)

	rec := postAnnouncement(router, `{
		"title": "使用警告", "content": "检测到违规使用，请遵守使用条例", "status": "active", "notify_mode": "popup",
		"targeting": {"any_of": [{"all_of": [{"type": "user", "operator": "in", "user_ids": [7, 9, 7]}]}]}
	}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, repo.created, 1)
	require.Equal(t, []int64{7, 9}, repo.created[0].Targeting.AnyOf[0].AllOf[0].UserIDs)

	var resp struct {
		Data struct {
			Targeting service.AnnouncementTargeting `json:"targeting"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, []int64{7, 9}, resp.Data.Targeting.AnyOf[0].AllOf[0].UserIDs)
}

func TestAdminAnnouncementCreateRejectsUserConditionWithoutUsers(t *testing.T) {
	repo := &announcementCreateCapture{}
	router := newAnnouncementCreateTestRouter(repo)

	rec := postAnnouncement(router, `{
		"title": "使用警告", "content": "内容", "status": "active",
		"targeting": {"any_of": [{"all_of": [{"type": "user", "operator": "in", "user_ids": []}]}]}
	}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "ANNOUNCEMENT_INVALID_TARGET")
	require.Empty(t, repo.created, "规则不合法时不能落库，更不能变成面向全部用户")
}
