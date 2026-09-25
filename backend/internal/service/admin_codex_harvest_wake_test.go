//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type harvestWakeAccountRepo struct {
	accountRepoStubForClearAccountError
	writeErr error
	readErr  error
}

func (r *harvestWakeAccountRepo) SetSchedulable(_ context.Context, _ int64, enabled bool) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	r.account.Schedulable = enabled
	return nil
}

func (r *harvestWakeAccountRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, r.readErr
}

func (r *harvestWakeAccountRepo) ClearError(ctx context.Context, id int64) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	return r.accountRepoStubForClearAccountError.ClearError(ctx, id)
}

func TestCodexHarvestWakeAfterAccountRecovery(t *testing.T) {
	for _, action := range []string{"schedule", "clear-error"} {
		for _, scenario := range []string{"openai", "other-platform", "write-failed", "read-failed"} {
			t.Run(action+"/"+scenario, func(t *testing.T) {
				repo := &harvestWakeAccountRepo{accountRepoStubForClearAccountError: accountRepoStubForClearAccountError{
					account: ticketTestAccount(41),
				}}
				switch scenario {
				case "other-platform":
					repo.account.Platform = PlatformAnthropic
				case "write-failed":
					repo.writeErr = errors.New("write failed")
				case "read-failed":
					repo.readErr = errors.New("read failed")
				}
				settings := &SettingService{}
				admin := &adminServiceImpl{accountRepo: repo, settingService: settings}
				var err error
				if action == "schedule" {
					_, err = admin.SetAccountSchedulable(context.Background(), 41, true)
				} else {
					_, err = admin.ClearAccountError(context.Background(), 41)
				}
				if scenario == "write-failed" || scenario == "read-failed" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				if scenario == "openai" {
					require.Len(t, settings.codexHarvestWakeups(), 1)
					<-settings.codexHarvestWakeups()
					_, err = admin.SetAccountSchedulable(context.Background(), 41, false)
					require.NoError(t, err)
					require.Empty(t, settings.codexHarvestWakeups(), "manual stop must not start harvesting")
				} else {
					require.Empty(t, settings.codexHarvestWakeups())
				}
			})
		}
	}
}
