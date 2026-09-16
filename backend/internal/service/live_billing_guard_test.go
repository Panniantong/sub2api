package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLiveBillingGuardStopsBeforeCredentialsOrUpstream(t *testing.T) {
	svc := &OpenAIGatewayService{}
	result, err := svc.CreateLiveCall(context.Background(), &LiveCallRequest{}, LiveCallIdentity{}, 1)
	require.ErrorIs(t, err, ErrLiveBillingUnavailable)
	require.Nil(t, result)
}
