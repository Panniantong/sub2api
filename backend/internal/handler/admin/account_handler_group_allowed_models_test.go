package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountUpdatePassesGroupAllowedModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		body string
		want map[int64][]string
	}{
		{
			name: "按分组 ID 传入",
			body: `{"group_ids":[3,5],"group_allowed_models":{"5":["gpt-5.5","gpt-5.3-*"]}}`,
			want: map[int64][]string{5: {"gpt-5.5", "gpt-5.3-*"}},
		},
		{
			name: "空对象表示全部恢复为不限制",
			body: `{"group_ids":[3],"group_allowed_models":{}}`,
			want: map[int64][]string{},
		},
		{
			name: "省略时保持不变",
			body: `{"group_ids":[3]}`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := newStubAdminService()
			handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.PUT("/accounts/:id", handler.Update)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/accounts/1", bytes.NewBufferString(tt.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.NotNil(t, stub.lastUpdateAccountInput)
			if tt.want == nil {
				require.Nil(t, stub.lastUpdateAccountInput.GroupAllowedModels)
				return
			}
			require.NotNil(t, stub.lastUpdateAccountInput.GroupAllowedModels)
			require.Equal(t, tt.want, stub.lastUpdateAccountInput.GroupAllowedModels)
		})
	}
}
