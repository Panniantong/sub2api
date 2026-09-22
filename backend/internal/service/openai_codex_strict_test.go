package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTicketStrictResponseIsTerminalAndDoesNotReadBody(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "compatible", true: "strict"}[strict], func(t *testing.T) {
			body := &codexTicketHeaderOnlyBody{}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 200, Header: http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"malformed"}}, Body: body}}}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, upstream)
			values := map[string]string{}
			if strict {
				values[SettingKeyOpenAICodexTicketStrict] = "true"
			}
			svc.settingService = NewSettingService(&codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: values}}, &config.Config{})
			account := ticketTestAccount(41)
			state := fakeCodexTicketState(292)
			require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{Model: "gpt-6-astra", State: state, Length: 292, ExpiresAt: time.Now().Add(time.Hour)}))
			req, _ := http.NewRequest(http.MethodPost, "https://example.org", nil)
			req.Header.Set(openAICodexTurnStateHeader, state)
			resp, err := svc.doOpenAIUpstream(req, "", account)
			require.Len(t, upstream.requests, 1)
			require.Zero(t, body.reads)
			if strict {
				require.Nil(t, resp)
				require.ErrorIs(t, err, ErrCodexTicketResponseRejected)
				require.Equal(t, 1, body.closes)
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				terminal := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account, err, false)
				var retry *UpstreamFailoverError
				require.False(t, errors.As(terminal, &retry))
				require.Equal(t, 502, writer.Code)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Zero(t, body.closes)
			}
		})
	}
}
