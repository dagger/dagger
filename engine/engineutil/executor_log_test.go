package engineutil

import (
	"bytes"
	"context"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
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

// The engine's log shows the exec's fields as it always has.
func TestStartingContainerLogText(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&out)
	logger.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	ctx := bklog.WithLogger(context.Background(), logrus.NewEntry(logger).WithField("traceID", "t"))

	lg := startingContainerLog(ctx, &execState{
		id:             "exec",
		spec:           &specs.Spec{Process: &specs.Process{Args: []string{"go", "tool", "compile", "-p", "a b"}}},
		execMD:         &ExecutionMetadata{CacheMounts: 1, CallDigest: "xxh3:1"},
		callerClientID: "client",
	})
	lg.Info("starting container")
	lg.WithError(nil).Info("container done")
	require.Equal(t, `level=info msg="starting container" args="[go tool compile -p a b]" cache_mounts=1 call_id="xxh3:1" caller_client_id=client id=exec traceID=t
level=info msg="container done" args="[go tool compile -p a b]" cache_mounts=1 call_id="xxh3:1" caller_client_id=client error="<nil>" id=exec traceID=t
`, out.String())
}
