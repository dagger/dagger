package daggercmd

import (
	"bytes"
	"testing"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

func TestResolveUsageMonth(t *testing.T) {
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

	t.Run("defaults to current month", func(t *testing.T) {
		m, err := resolveUsageMonth("", now)
		require.NoError(t, err)
		require.Equal(t, "2026-09", m)
	})

	t.Run("accepts explicit month", func(t *testing.T) {
		m, err := resolveUsageMonth("2026-03", now)
		require.NoError(t, err)
		require.Equal(t, "2026-03", m)
	})

	t.Run("rejects invalid month", func(t *testing.T) {
		_, err := resolveUsageMonth("2026/03", now)
		require.Error(t, err)
		require.Contains(t, err.Error(), "YYYY-MM")
	})
}

func TestFormatTelemetryUsage(t *testing.T) {
	require.Equal(t, "n/a", formatTelemetryUsage(nil))
	require.Equal(t, "1,000 used (unlimited)", formatTelemetryUsage(&cloudapi.MonthlyUsage{Usage: 1000, Cap: 0}))
	require.Equal(t, "2,500 / 5,000 (50.0% used, 2,500 left)", formatTelemetryUsage(&cloudapi.MonthlyUsage{Usage: 2500, Cap: 5000}))
	// Over cap clamps "left" to zero.
	require.Equal(t, "6,000 / 5,000 (120.0% used, 0 left)", formatTelemetryUsage(&cloudapi.MonthlyUsage{Usage: 6000, Cap: 5000}))
}

func TestFormatCloudMinutes(t *testing.T) {
	require.Equal(t, "0 minutes used", formatCloudMinutes(0))
	require.Equal(t, "1 minute used", formatCloudMinutes(1))
	require.Equal(t, "1,234 minutes used", formatCloudMinutes(1234))
}

func TestPrintUsage(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf, usageReport{
		Org:          "acme",
		Month:        "2026-09",
		Telemetry:    &cloudapi.MonthlyUsage{Usage: 2500, Cap: 5000},
		CloudMinutes: 120,
	})
	out := buf.String()
	require.Contains(t, out, "Org:")
	require.Contains(t, out, "acme")
	require.Contains(t, out, "2026-09")
	require.Contains(t, out, "2,500 / 5,000")
	require.Contains(t, out, "120 minutes used")
}
