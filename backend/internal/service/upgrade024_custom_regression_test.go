package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecurityPolicyAuthSnapshotRoundTrip(t *testing.T) {
	svc := &APIKeyService{}
	key := &APIKey{ID: 1, UserID: 2, User: &User{ID: 2}, Group: &Group{
		ID: 3, SecurityPolicyEnabled: true, SecurityPolicyMode: SecurityPolicyModeBlockRequest, SecurityPolicyEmailEnabled: true,
	}}
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	restored := svc.snapshotToAPIKey("test-key", snapshot)
	require.True(t, restored.Group.SecurityPolicyEnabled)
	require.Equal(t, SecurityPolicyModeBlockRequest, restored.Group.SecurityPolicyMode)
	require.True(t, restored.Group.SecurityPolicyEmailEnabled)
	_, accepted, err := svc.applyAuthCacheEntry("test-key", &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: 24}})
	require.NoError(t, err)
	require.False(t, accepted, "old cache entries omit security policy and must be reloaded")
}

func TestGlobalPricingOverridesGroupImagePriceInBothGateways(t *testing.T) {
	ctx := context.Background()
	billing := &BillingService{}
	global := NewGlobalModelPricingService(&stubGlobalPricingRepo{rows: []GlobalModelPrice{{
		ID: 1, ModelPattern: "test-image", BillingMode: BillingModeImage, PerRequestPrice: fptr(0.2), Enabled: true,
	}}})
	resolver := NewModelPricingResolverWithGlobal(nil, billing, global)
	key := &APIKey{Group: &Group{ID: 3, ImagePrice1K: fptr(99)}}
	gateway := &GatewayService{billingService: billing, resolver: resolver}
	cost := gateway.calculateImageCost(ctx, &ForwardResult{ImageCount: 2, ImageSize: "1K"}, key, "test-image", 1)
	require.InDelta(t, 0.4, cost.ActualCost, 1e-9)
	openAI := &OpenAIGatewayService{billingService: billing, resolver: resolver}
	cost = openAI.calculateOpenAIImageCost(ctx, "test-image", key, &OpenAIForwardResult{ImageCount: 2, ImageSize: "1K"}, 1)
	require.InDelta(t, 0.4, cost.ActualCost, 1e-9)
}
