package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/pelletier/go-toml"
)

func TestTelemetryTOMLConfigParsing(t *testing.T) {
	t.Parallel()

	var cfg bkconfig.Config
	require.NoError(t, toml.Unmarshal([]byte("[telemetry]\ncgroupSampleInterval = \"10s\"\n"), &cfg))
	require.Equal(t, 10*time.Second, cfg.Telemetry.CgroupSampleInterval.Duration)
}
