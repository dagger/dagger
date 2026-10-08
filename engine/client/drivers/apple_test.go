package drivers

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppleRunArgsPrivilegedGrantsAllCapabilities(t *testing.T) {
	// Apple's `container` has no `--privileged` flag; the equivalent is
	// `--cap-add ALL`. Since `container` 1.0.0 the default capability set no
	// longer includes CAP_SYS_ADMIN, which the engine needs to bind-mount
	// /etc/resolv.conf at startup, so this must be passed when privileged.
	args, _, err := apple{}.runArgs("dagger-engine-test", runOpts{
		image:      "registry.dagger.io/engine:test",
		privileged: true,
	})
	require.NoError(t, err)
	idx := slices.Index(args, "--cap-add")
	require.NotEqual(t, -1, idx, "expected --cap-add in args: %v", args)
	require.Less(t, idx+1, len(args))
	require.Equal(t, "ALL", args[idx+1])
}

func TestAppleRunArgsNotPrivilegedOmitsCapabilities(t *testing.T) {
	args, _, err := apple{}.runArgs("dagger-engine-test", runOpts{
		image:      "registry.dagger.io/engine:test",
		privileged: false,
	})
	require.NoError(t, err)
	require.NotContains(t, args, "--cap-add")
}

func TestAppleContainerRunningFromInspect(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		status  string
		running bool
		wantErr bool
	}{
		{name: "pre-1.0 running", status: `"running"`, running: true},
		{name: "pre-1.0 stopped", status: `"stopped"`},
		{name: "current running", status: `{"state":"running"}`, running: true},
		{name: "current stopped", status: `{"state":"stopped"}`},
		{name: "unknown status", status: `{"state":"unknown"}`, wantErr: true},
		{name: "missing state", status: `{}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			running, err := appleContainerRunning(`[{"status":` + test.status + `}]`)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.running, running)
		})
	}
}

func TestAppleContainerNotFoundOutput(t *testing.T) {
	t.Parallel()

	require.True(t, isAppleContainerNotFoundOutput("Error: container not found: dagger-engine"))
	require.True(t, isAppleContainerNotFoundOutput("notFound: container with ID dagger-engine not found"))
	require.False(t, isAppleContainerNotFoundOutput("container runtime endpoint not found"))
}
