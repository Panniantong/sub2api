package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubMarginChannels struct {
	status  map[int64]string
	updates int
}

func (s *stubMarginChannels) GetByID(_ context.Context, id int64) (*Channel, error) {
	st, ok := s.status[id]
	if !ok {
		st = StatusActive
	}
	return &Channel{ID: id, Status: st}, nil
}

func (s *stubMarginChannels) Update(_ context.Context, id int64, input *UpdateChannelInput) (*Channel, error) {
	if s.status == nil {
		s.status = make(map[int64]string)
	}
	s.status[id] = input.Status
	s.updates++
	return &Channel{ID: id, Status: input.Status}, nil
}

func TestNormalizeMarginFuseSettings(t *testing.T) {
	norm := normalizeMarginFuseSettings(MarginFuseSettings{})
	require.True(t, norm.WindowHours > 0)
	require.True(t, norm.CooldownHours > 0)
	require.True(t, norm.IntervalMinutes >= 1)
}

func TestMarginRowWithoutBilling(t *testing.T) {
	svc := NewMarginService(nil, &stubMarginChannels{}, nil, &stubHealthSettingRepo{})
	row := svc.toMarginRow(marginAggRow{model: "gpt-5", requests: 3, revenue: 1.5})
	require.Equal(t, "gpt-5", row.Model)
	require.Nil(t, row.EstCost)
	require.Nil(t, row.Margin)
}

func TestMarginUnfuse(t *testing.T) {
	ch := &stubMarginChannels{status: map[int64]string{9: StatusDisabled}}
	svc := NewMarginService(nil, ch, nil, &stubHealthSettingRepo{})
	svc.mu.Lock()
	svc.fused[9] = time.Now()
	svc.mu.Unlock()
	require.NoError(t, svc.Unfuse(context.Background(), 9))
	require.Equal(t, StatusActive, ch.status[9])
	svc.mu.RLock()
	_, tracked := svc.fused[9]
	svc.mu.RUnlock()
	require.False(t, tracked)
	require.Len(t, svc.Events(), 1)
	require.Equal(t, "recovered", svc.Events()[0].Action)
}
