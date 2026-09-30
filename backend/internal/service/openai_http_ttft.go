package service

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/streamlatency"
)

// openAIHTTPFirstTokenMs exposes CPA's first-positive-body-read metric when
// enabled. Other transports and legacy modes retain their existing semantic
// values, including nil when no stream data was received.
func (s *OpenAIGatewayService) openAIHTTPFirstTokenMs(resp *http.Response, legacy *int) *int {
	if resp != nil && resp.Request != nil {
		if measured, tracked := streamlatency.FirstByteMilliseconds(resp); tracked && s.openAITTFTMode(resp.Request.Context()) == OpenAITTFTModeCPA {
			return measured
		}
	}
	return legacy
}
