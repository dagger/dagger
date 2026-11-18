package dockerfile2llb

import (
	"context"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	apicapspb "github.com/dagger/dagger/internal/buildkit/util/apicaps/pb"
	"github.com/stretchr/testify/require"
)

// Regression test for https://github.com/dagger/dagger/issues/15198.
//
// The engine (core/container.go) calls Dockerfile2LLB without a dockerui
// Client, so opt.LLBCaps — and therefore dispatchOpt.llbCaps — stays nil. A
// read-write bind mount (RUN --mount=type=bind,…,rw) must not dereference it.
func TestDockerfile2LLBBindMountRWWithoutLLBCaps(t *testing.T) {
	dockerfile := `FROM scratch AS a
RUN mkdir /d
FROM scratch
RUN --mount=type=bind,from=a,source=/d,target=/y,rw cat /y/x
`
	st, _, _, _, err := Dockerfile2LLB(context.Background(), []byte(dockerfile), ConvertOpt{})
	require.NoError(t, err)
	require.NotNil(t, st)

	mount := execMount(t, st, "/y")
	require.Equal(t, pb.MountType_BIND, mount.MountType)
	require.False(t, mount.Readonly)
	// With unknown caps the rw bind mount keeps its output: the
	// ForceNoOutput optimization is skipped rather than applied blindly.
	require.NotEqual(t, pb.SkipOutput, mount.Output)
}

// Same Dockerfile with the capability advertised: ForceNoOutput still applies,
// so the nil guard does not disable the optimization when caps are known.
func TestDockerfile2LLBBindMountRWWithLLBCaps(t *testing.T) {
	dockerfile := `FROM scratch AS a
RUN mkdir /d
FROM scratch
RUN --mount=type=bind,from=a,source=/d,target=/y,rw cat /y/x
`
	caps := pb.Caps.CapSet([]apicapspb.APICap{
		{ID: string(pb.CapExecMountBindReadWriteNoOutput), Enabled: true},
	})
	st, _, _, _, err := Dockerfile2LLB(context.Background(), []byte(dockerfile), ConvertOpt{LLBCaps: &caps})
	require.NoError(t, err)
	require.NotNil(t, st)

	mount := execMount(t, st, "/y")
	require.Equal(t, pb.MountType_BIND, mount.MountType)
	require.False(t, mount.Readonly)
	require.Equal(t, pb.SkipOutput, mount.Output)
}

func execMount(t *testing.T, st *llb.State, dest string) *pb.Mount {
	t.Helper()
	def, err := st.Marshal(context.Background())
	require.NoError(t, err)
	for _, dt := range def.Def {
		var op pb.Op
		require.NoError(t, op.Unmarshal(dt))
		if execOp := op.GetExec(); execOp != nil {
			for _, m := range execOp.Mounts {
				if m.Dest == dest {
					return m
				}
			}
		}
	}
	t.Fatalf("no exec mount at %q in definition", dest)
	return nil
}
