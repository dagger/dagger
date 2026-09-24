package server

import (
	"fmt"
	"time"

	"github.com/dagger/dagger/engine/config"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
)

// resolveCgroupSampleInterval resolves the container metrics sampling interval
// from the engine configs. The dagger-native engine.json setting takes
// precedence over the buildkitd-style TOML setting, mirroring how the GC
// configuration is resolved. Zero/unset defers to the executor's built-in
// default; negative values are rejected.
func resolveCgroupSampleInterval(cfg config.TelemetryConfig, bkCfg bkconfig.TelemetryConfig) (time.Duration, error) {
	interval := cfg.CgroupSampleInterval.Duration
	if interval == 0 {
		interval = bkCfg.CgroupSampleInterval.Duration
	}
	if interval < 0 {
		return 0, fmt.Errorf("telemetry.cgroupSampleInterval must be positive (got %s)", interval)
	}
	return interval, nil
}
