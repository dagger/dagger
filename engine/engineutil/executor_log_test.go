package engineutil

import (
	"context"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

// The "starting container" line always reports how many cache volume mounts
// the exec has, zero included: measurement tooling reads a missing field as
// "unavailable", not as zero.
func TestStartingContainerLogCacheMounts(t *testing.T) {
	t.Parallel()
	spec := &specs.Spec{Process: &specs.Process{Args: []string{"go", "build"}}}

	lg := startingContainerLog(context.Background(), &execState{
		id:     "exec",
		spec:   spec,
		execMD: &ExecutionMetadata{CacheMounts: 2},
	})
	require.Equal(t, 2, lg.Data["cache_mounts"])

	lg = startingContainerLog(context.Background(), &execState{
		id:     "exec",
		spec:   spec,
		execMD: &ExecutionMetadata{},
	})
	require.Equal(t, 0, lg.Data["cache_mounts"])

	lg = startingContainerLog(context.Background(), &execState{id: "exec", spec: spec})
	require.Equal(t, 0, lg.Data["cache_mounts"], "present even without exec metadata")
}
