package dockerfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var mountTests = integration.TestFuncs(
	testMountContext,
	testMountTmpfs,
	testMountRWCache,
	testCacheMountDefaultID,
	testMountEnvVar,
	testMountArg,
	testMountEnvAcrossStages,
	testMountMetaArg,
	testMountFromError,
	testMountInvalid,
	testMountTmpfsSize,
	testMountDuplicate,
	testCacheMountUser,
)

func init() {
	allTests = append(allTests, mountTests...)
}

func testMountContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --mount=target=/context [ "$(cat /context/testfile)" == "contents0" ]
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("testfile", []byte("contents0"), 0600),
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

func testMountTmpfs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --mount=target=/mytmp,type=tmpfs touch /mytmp/foo
RUN [ ! -f /mytmp/foo ]
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

func testMountInvalid(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
RUN --mont=target=/mytmp,type=tmpfs /bin/true
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
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown flag: mont")
	require.Contains(t, err.Error(), "did you mean mount?")

	dockerfile = []byte(`
	FROM scratch
	RUN --mount=typ=tmpfs /bin/true
	`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected key 'typ'")
	require.Contains(t, err.Error(), "did you mean type?")

	dockerfile = []byte(`
	FROM scratch
	RUN --mount=type=tmp /bin/true
	`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported mount type \"tmp\"")
	require.Contains(t, err.Error(), "did you mean tmpfs?")
}

func testMountRWCache(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
from busybox AS build
copy cachebust /
run mkdir out && echo foo > out/foo

from busybox as second
RUN --mount=from=build,src=out,target=/out,rw touch /out/bar && cat /dev/urandom | head -c 100 | sha256sum > /unique

from scratch
COPY --from=second /unique /unique
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("cachebust", []byte("0"), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt1, err := os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)

	// repeat with changed file that should be still cached by content
	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("cachebust", []byte("1"), 0600),
	)

	destDir = t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt2, err := os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)
	require.Equal(t, dt1, dt2)
}

func testCacheMountUser(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --mount=type=cache,target=/mycache,uid=1001,gid=1002,mode=0751 [ "$(stat -c "%u %g %f" /mycache)" == "1001 1002 41e9" ]
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

func testCacheMountDefaultID(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --mount=type=cache,target=/mycache touch /mycache/foo
RUN --mount=type=cache,target=/mycache2 [ ! -f /mycache2/foo ]
RUN --mount=type=cache,target=/mycache [ -f /mycache/foo ]
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

func testMountEnvVar(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
ENV SOME_PATH=/mycache
RUN --mount=type=cache,target=/mycache touch /mycache/foo
RUN --mount=type=cache,target=$SOME_PATH [ -f $SOME_PATH/foo ]
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

func testMountArg(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
ARG MNT_TYPE=cache
RUN --mount=type=$MNT_TYPE,target=/mycache2 touch /mycache2/foo
RUN --mount=type=cache,target=/mycache2 [ -f /mycache2/foo ]
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

func testMountEnvAcrossStages(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox as stage1

ENV MNT_ID=mycache
ENV MNT_TYPE2=cache
RUN --mount=type=cache,id=mycache,target=/abcabc touch /abcabc/foo
RUN --mount=type=$MNT_TYPE2,id=$MNT_ID,target=/cbacba [ -f /cbacba/foo ]

FROM stage1
RUN --mount=type=$MNT_TYPE2,id=$MNT_ID,target=/whatever [ -f /whatever/foo ]
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

func testMountMetaArg(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
ARG META_PATH=/tmp/meta

FROM busybox
ARG META_PATH
RUN --mount=type=cache,id=mycache,target=/tmp/meta touch /tmp/meta/foo
RUN --mount=type=cache,id=mycache,target=$META_PATH [ -f /tmp/meta/foo ]
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

func testMountFromError(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox as test
RUN touch /tmp/test

FROM busybox
ENV ttt=test
RUN --mount=from=$ttt,type=cache,target=/tmp ls
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
	require.Error(t, err)
	require.Contains(t, err.Error(), "'from' doesn't support variable expansion, define alias stage instead")
}

func testMountTmpfsSize(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS base
RUN --mount=type=tmpfs,target=/dev/shm,size=128m mount | grep /dev/shm > /tmpfssize
FROM scratch
COPY --from=base /tmpfssize /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "tmpfssize"))
	require.NoError(t, err)
	require.Contains(t, string(dt), `size=131072k`)
}

// moby/buildkit#4123
func testMountDuplicate(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS base
RUN --mount=source=.,target=/tmp/test \
  --mount=source=b.txt,target=/tmp/b.txt \
  cat /tmp/test/a.txt /tmp/b.txt > /combined.txt
FROM scratch
COPY --from=base /combined.txt /
`)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	// Run this dockerfile a few times. It should update the context
	// for a.txt properly and update the output.
	test := func(text string) {
		dir := integration.Tmpdir(
			t,
			fstest.CreateFile("Dockerfile", dockerfile, 0600),
			fstest.CreateFile("a.txt", []byte(text), 0600),
			fstest.CreateFile("b.txt", []byte("bar\n"), 0600),
		)

		_, err = f.Solve(sb.Context(), c, client.SolveOpt{
			Exports: []client.ExportEntry{
				{
					Type:      client.ExporterLocal,
					OutputDir: destDir,
				},
			},
			LocalMounts: map[string]fsutil.FS{
				dockerui.DefaultLocalNameDockerfile: dir,
				dockerui.DefaultLocalNameContext:    dir,
			},
		}, nil)
		require.NoError(t, err)

		dt, err := os.ReadFile(filepath.Join(destDir, "combined.txt"))
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("%sbar\n", text), string(dt))
	}

	test("foo\n")
	test("updated\n")
}
