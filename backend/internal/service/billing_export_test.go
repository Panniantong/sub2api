package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseBillingYearMonth(t *testing.T) {
	start, end, err := parseBillingYearMonth(2026, 9)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), start)
	require.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), end)

	_, _, err = parseBillingYearMonth(2026, 13)
	require.Error(t, err)
	_, _, err = parseBillingYearMonth(2019, 1)
	require.Error(t, err)
}

func TestBillingExportNilDB(t *testing.T) {
	svc := NewBillingExportService(nil)
	_, err := svc.Statement(nil, 1, 2026, 9)
	require.Error(t, err)
	_, _, err = svc.ExportCSV(nil, 1, time.Now(), time.Now())
	require.Error(t, err)
}
