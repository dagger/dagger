package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadTelemetryConfig(t *testing.T) {
	t.Parallel()

	t.Run("duration string", func(t *testing.T) {
		t.Parallel()
		cfg, err := Load(strings.NewReader(`{"telemetry": {"cgroupSampleInterval": "10s"}}`))
		require.NoError(t, err)
		require.Equal(t, 10*time.Second, cfg.Telemetry.CgroupSampleInterval.Duration)
	})

	t.Run("integer seconds", func(t *testing.T) {
		t.Parallel()
		cfg, err := Load(strings.NewReader(`{"telemetry": {"cgroupSampleInterval": "30"}}`))
		require.NoError(t, err)
		require.Equal(t, 30*time.Second, cfg.Telemetry.CgroupSampleInterval.Duration)
	})

	t.Run("unset", func(t *testing.T) {
		t.Parallel()
		cfg, err := Load(strings.NewReader(`{}`))
		require.NoError(t, err)
		require.Zero(t, cfg.Telemetry.CgroupSampleInterval.Duration)
	})
}
