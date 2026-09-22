package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestHarvestAttemptSessionStartsFreshLineage(t *testing.T) {
	svc := &OpenAIGatewayService{}
	attempt := codexHarvestAttempt{proxy: "http://127.0.0.1:3101"}

	first := svc.harvestAttemptSession(nil, "gpt-6-astra", attempt)
	second := svc.harvestAttemptSession(nil, "gpt-6-astra", attempt)

	require.NotEqual(t, first, second)
	_, err := uuid.Parse(first)
	require.NoError(t, err)
	_, err = uuid.Parse(second)
	require.NoError(t, err)
}
