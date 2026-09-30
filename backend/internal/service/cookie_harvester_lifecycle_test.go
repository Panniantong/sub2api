package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The embedded repository deliberately has no implementation: when settings
// are absent, ordinary gateways must never start account discovery for Cookie.
type cookieHarvesterUnusedAccountRepo struct{ AccountRepository }

func TestCookieHarvesterWithoutSettingsDoesNotAccessAccounts(t *testing.T) {
	for _, gateway := range []*OpenAIGatewayService{
		nil,
		{},
		{accountRepo: cookieHarvesterUnusedAccountRepo{}},
		{settingService: &SettingService{}},
	} {
		require.NotPanics(t, func() { gateway.runOpenAICookieHarvester(context.Background()) })
	}
}
