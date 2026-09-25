package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func deniedModelsTestContext(t *testing.T, path string, denied ...string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		User: &service.User{ID: 7, UserGroupDeniedModels: denied},
	})
	return c, w
}

func listedModelIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))
	ids := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestWriteModelsListResponseHidesUserDeniedModels(t *testing.T) {
	models := []openai.Model{{ID: "gpt-6-luna", Object: "model"}, {ID: "gpt-6-sol", Object: "model"}}

	c, w := deniedModelsTestContext(t, "/v1/models", "gpt-6-luna")
	writeModelsListResponse(c, models)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"gpt-6-sol"}, listedModelIDs(t, w.Body.Bytes()))

	// 不受限的用户保持原样
	c, w = deniedModelsTestContext(t, "/v1/models")
	writeModelsListResponse(c, models)
	require.Equal(t, []string{"gpt-6-luna", "gpt-6-sol"}, listedModelIDs(t, w.Body.Bytes()))
}

func TestWriteModelsListResponseRetrievingDeniedModelReturnsNotFound(t *testing.T) {
	c, w := deniedModelsTestContext(t, "/v1/models/gpt-6-luna", "gpt-6-luna")
	c.Params = gin.Params{{Key: "model", Value: "gpt-6-luna"}}

	writeModelsListResponse(c, []openai.Model{{ID: "gpt-6-luna", Object: "model"}})
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestRemoveDeniedCatalogEntriesHandlesListAndCodexManifest(t *testing.T) {
	denied := []string{"gpt-6-luna"}

	list, err := removeDeniedCatalogEntries([]byte(`{"object":"list","data":[{"id":"gpt-6-luna"},{"id":"gpt-6-sol","owned_by":"openai"}]}`), denied)
	require.NoError(t, err)
	require.JSONEq(t, `{"object":"list","data":[{"id":"gpt-6-sol","owned_by":"openai"}]}`, string(list))

	manifest, err := removeDeniedCatalogEntries([]byte(`{"models":[{"slug":"gpt-6-luna","priority":1},{"slug":"gpt-6-sol","priority":2}]}`), denied)
	require.NoError(t, err)
	require.JSONEq(t, `{"models":[{"slug":"gpt-6-sol","priority":2}]}`, string(manifest))
}

func TestWriteOpenAIModelsResponseFiltersAndDropsSharedETag(t *testing.T) {
	c, w := deniedModelsTestContext(t, "/v1/models", "gpt-6-luna")
	writeOpenAIModelsResponse(c, &service.OpenAIModelsResponse{
		Body: []byte(`{"object":"list","data":[{"id":"gpt-6-luna"},{"id":"gpt-6-sol"}]}`),
		ETag: `"shared"`,
	})
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Header().Get("ETag"), "按用户过滤后的目录不能带分组共享的 ETag")
	require.Equal(t, []string{"gpt-6-sol"}, listedModelIDs(t, w.Body.Bytes()))
}

func TestModelsIfNoneMatchIgnoredForRestrictedUsers(t *testing.T) {
	c, _ := deniedModelsTestContext(t, "/v1/models", "gpt-6-luna")
	c.Request.Header.Set("If-None-Match", `"shared"`)
	require.Empty(t, modelsIfNoneMatch(c))

	c, _ = deniedModelsTestContext(t, "/v1/models")
	c.Request.Header.Set("If-None-Match", `"shared"`)
	require.Equal(t, `"shared"`, modelsIfNoneMatch(c))
}

func TestToModelPlazaGroupDTOHidesUserDeniedModels(t *testing.T) {
	g := service.PlazaGroup{
		ID: 2, Name: "codex", Platform: "openai",
		Models: []service.PlazaModel{{Name: "gpt-6-luna", Platform: "openai"}, {Name: "gpt-6-sol", Platform: "openai"}},
	}

	dto := toModelPlazaGroupDTO(&g, nil, []string{"gpt-6-luna"})
	require.Len(t, dto.Models, 1)
	require.Equal(t, "gpt-6-sol", dto.Models[0].Name)
	require.Len(t, toModelPlazaGroupDTO(&g, nil, nil).Models, 2)
}
