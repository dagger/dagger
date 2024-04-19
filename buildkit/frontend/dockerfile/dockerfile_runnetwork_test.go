package dockerfile

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/testutil/echoserver"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var runNetworkTests = integration.TestFuncs(
	testRunDefaultNetwork,
	testRunNoNetwork,
	testRunHostNetwork,
	testRunGlobalNetwork,
)

func init() {
	networkTests = append(networkTests, runNetworkTests...)
}

func testRunDefaultNetwork(t *testing.T, sb integration.Sandbox) {
	if os.Getenv("BUILDKIT_RUN_NETWORK_INTEGRATION_TESTS") == "" {
		t.SkipNow()
	}
	if sb.Rootless() { // bridge is not used by default, even with detach-netns
		t.SkipNow()
	}

	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN ip link show eth0
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)

	require.NoError(t, err)
}

func testRunNoNetwork(t *testing.T, sb integration.Sandbox) {
	if os.Getenv("BUILDKIT_RUN_NETWORK_INTEGRATION_TESTS") == "" {
		t.SkipNow()
	}

	f := getFrontend(t, sb)

	dockerfile := `
FROM busybox
RUN --network=none ! ip link show eth0
`

	if !sb.Rootless() {
		dockerfile += "RUN ip link show eth0"
	}

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)

	require.NoError(t, err)
}

func testRunHostNetwork(t *testing.T, sb integration.Sandbox) {
	if os.Getenv("BUILDKIT_RUN_NETWORK_INTEGRATION_TESTS") == "" {
		t.SkipNow()
	}

	f := getFrontend(t, sb)

	s, err := echoserver.NewTestServer("foo")
	require.NoError(t, err)
	addrParts := strings.Split(s.Addr().String(), ":")
	port := addrParts[len(addrParts)-1]

	dockerfile := fmt.Sprintf(`
FROM busybox
RUN --network=host nc 127.0.0.1 %s | grep foo
`, port)

	if !sb.Rootless() {
		dockerfile += fmt.Sprintf(`RUN ! nc 127.0.0.1 %s | grep foo`, port)
	}

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		AllowedEntitlements: []entitlements.Entitlement{entitlements.EntitlementNetworkHost},
	}, nil)

	hostAllowed := sb.Value("network.host")
	switch hostAllowed {
	case networkHostGranted:
		require.NoError(t, err)
	case networkHostDenied:
		if !workers.IsTestDockerd() {
			require.Error(t, err)
			require.Contains(t, err.Error(), "entitlement network.host is not allowed")
		} else {
			require.NoError(t, err)
		}
	default:
		require.Fail(t, "unexpected network.host mode %q", hostAllowed)
	}
}

func testRunGlobalNetwork(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	s, err := echoserver.NewTestServer("foo")
	require.NoError(t, err)
	addrParts := strings.Split(s.Addr().String(), ":")
	port := addrParts[len(addrParts)-1]

	dockerfile := fmt.Sprintf(`
FROM busybox
RUN nc 127.0.0.1 %s | grep foo
RUN --network=none ! nc -z 127.0.0.1 %s
`, port, port)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		AllowedEntitlements: []entitlements.Entitlement{entitlements.EntitlementNetworkHost},
		FrontendAttrs: map[string]string{
			"force-network-mode": "host",
		},
	}, nil)

	hostAllowed := sb.Value("network.host")
	switch hostAllowed {
	case networkHostGranted:
		require.NoError(t, err)
	case networkHostDenied:
		if !workers.IsTestDockerd() {
			require.Error(t, err)
			require.Contains(t, err.Error(), "entitlement network.host is not allowed")
		} else {
			require.NoError(t, err)
		}
	default:
		require.Fail(t, "unexpected network.host mode %q", hostAllowed)
	}
}
