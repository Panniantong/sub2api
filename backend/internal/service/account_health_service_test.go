package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubHealthAccountStore struct {
	isolated map[int64]time.Time
	cleared  []int64
}

func (s *stubHealthAccountStore) SetTempUnschedulable(_ context.Context, id int64, until time.Time, _ string) error {
	if s.isolated == nil {
		s.isolated = make(map[int64]time.Time)
	}
	s.isolated[id] = until
	return nil
}

func (s *stubHealthAccountStore) ClearTempUnschedulable(_ context.Context, id int64) error {
	s.cleared = append(s.cleared, id)
	return nil
}

type stubHealthSettingRepo struct {
	values map[string]string
}

func (s *stubHealthSettingRepo) Get(ctx context.Context, key string) (*Setting, error) {
	return &Setting{Key: key, Value: s.values[key]}, nil
}

func (s *stubHealthSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}

func (s *stubHealthSettingRepo) Set(_ context.Context, key, value string) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *stubHealthSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	return nil, nil
}

func (s *stubHealthSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	return nil
}

func (s *stubHealthSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return nil, nil
}

func (s *stubHealthSettingRepo) Delete(_ context.Context, _ string) error { return nil }

func TestAccountHealthIsolateResume(t *testing.T) {
	store := &stubHealthAccountStore{}
	svc := NewAccountHealthService(nil, store, &stubHealthSettingRepo{})
	require.NoError(t, svc.Isolate(context.Background(), 7))
	require.Contains(t, store.isolated, int64(7))
	require.NoError(t, svc.Resume(context.Background(), 7))
	require.Contains(t, store.cleared, int64(7))
	require.Empty(t, svc.Snapshot())
}

func TestAccountHealthSettingsRoundtrip(t *testing.T) {
	repo := &stubHealthSettingRepo{}
	svc := NewAccountHealthService(nil, &stubHealthAccountStore{}, repo)
	got := svc.GetSettings(context.Background())
	require.True(t, got.WindowMinutes > 0)

	updated, err := svc.UpdateSettings(context.Background(), AccountHealthSettings{Enabled: false})
	require.NoError(t, err)
	require.False(t, updated.Enabled)
	require.Contains(t, repo.values[SettingKeyAccountHealthSettings], `"enabled":false`)
}
