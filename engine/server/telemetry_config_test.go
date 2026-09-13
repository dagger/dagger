package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/config"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
)

func TestResolveCgroupSampleInterval(t *testing.T) {
	t.Parallel()

	duration := func(d time.Duration) config.Duration {
		return config.Duration(bkconfig.Duration{Duration: d})
	}
	bkDuration := func(d time.Duration) bkconfig.Duration {
		return bkconfig.Duration{Duration: d}
	}

	t.Run("unset defers to executor default", func(t *testing.T) {
		t.Parallel()
		interval, err := resolveCgroupSampleInterval(config.TelemetryConfig{}, bkconfig.TelemetryConfig{})
		require.NoError(t, err)
		require.Zero(t, interval)
	})

	t.Run("engine.json value used", func(t *testing.T) {
		t.Parallel()
		interval, err := resolveCgroupSampleInterval(
			config.TelemetryConfig{CgroupSampleInterval: duration(2 * time.Second)},
			bkconfig.TelemetryConfig{},
		)
		require.NoError(t, err)
		require.Equal(t, 2*time.Second, interval)
	})

	t.Run("TOML value used when engine.json unset", func(t *testing.T) {
		t.Parallel()
		interval, err := resolveCgroupSampleInterval(
			config.TelemetryConfig{},
			bkconfig.TelemetryConfig{CgroupSampleInterval: bkDuration(10 * time.Second)},
		)
		require.NoError(t, err)
		require.Equal(t, 10*time.Second, interval)
	})

	t.Run("engine.json takes precedence over TOML", func(t *testing.T) {
		t.Parallel()
		interval, err := resolveCgroupSampleInterval(
			config.TelemetryConfig{CgroupSampleInterval: duration(time.Second)},
			bkconfig.TelemetryConfig{CgroupSampleInterval: bkDuration(30 * time.Second)},
		)
		require.NoError(t, err)
		require.Equal(t, time.Second, interval)
	})

	t.Run("negative interval rejected", func(t *testing.T) {
		t.Parallel()
		_, err := resolveCgroupSampleInterval(
			config.TelemetryConfig{CgroupSampleInterval: duration(-time.Second)},
			bkconfig.TelemetryConfig{},
		)
		require.ErrorContains(t, err, "must be positive")
	})
}
