package service

import "github.com/Wei-Shaw/sub2api/internal/config"

// The production composition root attaches optional services before starting
// the harvest loop; no post-construction setter races with startup.
type OpenAIGatewayDependencies struct {
	Accounts      AccountRepository
	Proxies       ProxyRepository
	UsageLogs     UsageLogRepository
	UsageBilling  UsageBillingRepository
	Users         UserRepository
	Subscriptions UserSubscriptionRepository
	GroupRates    UserGroupRateRepository
	Cache         GatewayCache
	Config        *config.Config
	Scheduler     *SchedulerSnapshotService
	Concurrency   *ConcurrencyService
	Billing       *BillingService
	RateLimit     *RateLimitService
	BillingCache  *BillingCacheService
	Upstream      HTTPUpstream
	Deferred      *DeferredService
	OpenAITokens  *OpenAITokenProvider
	GrokTokens    *GrokTokenProvider
	Pricing       *ModelPricingResolver
	Channels      *ChannelService
	BalanceNotify *BalanceNotifyService
	Settings      *SettingService
	Quotas        UserPlatformQuotaRepository
	Harvest       *CodexHarvestService
}

func ProvideOpenAIGatewayService(d OpenAIGatewayDependencies) *OpenAIGatewayService {
	return NewOpenAIGatewayService(d.Accounts, d.Proxies, d.UsageLogs, d.UsageBilling, d.Users, d.Subscriptions,
		d.GroupRates, d.Cache, d.Config, d.Scheduler, d.Concurrency, d.Billing, d.RateLimit,
		d.BillingCache, d.Upstream, d.Deferred, d.OpenAITokens, d.GrokTokens, d.Pricing,
		d.Channels, d.BalanceNotify, d.Settings, d.Quotas,
		func(s *OpenAIGatewayService) { s.codexHarvest = d.Harvest })
}
