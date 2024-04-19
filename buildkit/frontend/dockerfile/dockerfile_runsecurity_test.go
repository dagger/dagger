//go:build dfrunsecurity
// +build dfrunsecurity

package dockerfile

import (
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var runSecurityTests = integration.TestFuncs(
	testRunSecurityInsecure,
	testRunSecuritySandbox,
	testRunSecurityDefault,
	testInsecureDevicesWhitelist,
)

func init() {
	securityOpts = []integration.TestOpt{
		integration.WithMirroredImages(integration.OfficialImages("alpine:latest")),
		integration.WithMirroredImages(map[string]string{
			"tonistiigi/hellofs:latest": "docker.io/tonistiigi/hellofs:latest",
		}),
	}

	securityTests = append(securityTests, runSecurityTests...)
}

func testInsecureDevicesWhitelist(t *testing.T, sb integration.Sandbox) {
	if sb.Rootless() {
		t.SkipNow()
	}

	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM alpine
RUN apk add --no-cache fuse e2fsprogs
RUN [ ! -e /dev/fuse ] && [ ! -e /dev/loop-control ]
# https://github.com/bazil/fuse/blob/master/examples/hellofs/hello.go#L91
COPY --from=tonistiigi/hellofs /hellofs /bin/hellofs
RUN --security=insecure [ -c /dev/fuse ] && [ -c /dev/loop-control ]
RUN --security=insecure dmesg > /dev/null
# testing fuse
RUN --security=insecure hellofs /mnt & sleep 1 && ls -l /mnt && mount && cat /mnt/hello
# testing loopbacks
RUN --security=insecure ls -l /dev && dd if=/dev/zero of=disk.img bs=20M count=1 && \
	mkfs.ext4 disk.img && \
	mount -o loop disk.img /mnt && touch /mnt/foo \
	umount /mnt && \
	rm disk.img
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
		AllowedEntitlements: []entitlements.Entitlement{entitlements.EntitlementSecurityInsecure},
	}, nil)

	secMode := sb.Value("security.insecure")
	switch secMode {
	case securityInsecureGranted:
		require.NoError(t, err)
	case securityInsecureDenied:
		require.Error(t, err)
		require.Contains(t, err.Error(), "entitlement security.insecure is not allowed")
	default:
		require.Fail(t, "unexpected secmode")
	}
}

func testRunSecurityInsecure(t *testing.T, sb integration.Sandbox) {
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --security=insecure [ "$(printf '%x' $(( $(cat /proc/self/status | grep CapBnd | cut -f 2 | sed s#^#0x#) & 0x3fffffffff)))" == "3fffffffff" ]
RUN [ "$(cat /proc/self/status | grep CapBnd)" == "CapBnd:	00000000a80425fb" ]
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
		AllowedEntitlements: []entitlements.Entitlement{entitlements.EntitlementSecurityInsecure},
	}, nil)

	secMode := sb.Value("security.insecure")
	switch secMode {
	case securityInsecureGranted:
		require.NoError(t, err)
	case securityInsecureDenied:
		require.Error(t, err)
		require.Contains(t, err.Error(), "entitlement security.insecure is not allowed")
	default:
		require.Fail(t, "unexpected secmode")
	}
}

func testRunSecuritySandbox(t *testing.T, sb integration.Sandbox) {
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --security=sandbox [ "$(cat /proc/self/status | grep CapBnd)" == "CapBnd:	00000000a80425fb" ]
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

func testRunSecurityDefault(t *testing.T, sb integration.Sandbox) {
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN [ "$(cat /proc/self/status | grep CapBnd)" == "CapBnd:	00000000a80425fb" ]
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
		AllowedEntitlements: []entitlements.Entitlement{entitlements.EntitlementSecurityInsecure},
	}, nil)

	secMode := sb.Value("security.insecure")
	switch secMode {
	case securityInsecureGranted:
		require.NoError(t, err)
	case securityInsecureDenied:
		require.Error(t, err)
		require.Contains(t, err.Error(), "entitlement security.insecure is not allowed")
	default:
		require.Fail(t, "unexpected secmode")
	}
}
