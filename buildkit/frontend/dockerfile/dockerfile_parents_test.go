//go:build dfparents
// +build dfparents

package dockerfile

import (
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

var parentsTests = integration.TestFuncs(
	testCopyParents,
	testCopyRelativeParents,
)

func init() {
	allTests = append(allTests, parentsTests...)
}

func testCopyParents(t *testing.T, sb integration.Sandbox) {
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY --parents foo1/foo2/bar /

WORKDIR /test
COPY --parents foo1/foo2/ba* .
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateDir("foo1", 0700),
		fstest.CreateDir("foo1/foo2", 0700),
		fstest.CreateFile("foo1/foo2/bar", []byte(`testing`), 0600),
		fstest.CreateFile("foo1/foo2/baz", []byte(`testing2`), 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "foo1/foo2/bar"))
	require.NoError(t, err)
	require.Equal(t, "testing", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "test/foo1/foo2/bar"))
	require.NoError(t, err)
	require.Equal(t, "testing", string(dt))
	dt, err = os.ReadFile(filepath.Join(destDir, "test/foo1/foo2/baz"))
	require.NoError(t, err)
	require.Equal(t, "testing2", string(dt))
}

func testCopyRelativeParents(t *testing.T, sb integration.Sandbox) {
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM alpine AS base
WORKDIR /test
RUN <<eot
	set -ex
	mkdir -p a/b/c/d/e
	mkdir -p a/b2/c/d/e
	mkdir -p a/b/c2/d/e
	mkdir -p a/b/c2/d/e2
	touch a/b/c/d/foo
	touch a/b/c/d/e/bay
	touch a/b2/c/d/e/bar
	touch a/b/c2/d/e/baz
	touch a/b/c2/d/e2/baz
eot

FROM alpine AS middle
COPY --from=base --parents /test/a/b/./c/d /out/
RUN <<eot
	set -ex
	[ -d /out/c/d/e ]
	[ -f /out/c/d/foo ]
	[ ! -d /out/a ]
	[ ! -d /out/e ]
eot

FROM alpine AS end
COPY --from=base --parents /test/a/b/c/d/. /out/
RUN <<eot
	set -ex
	[ -d /out/test/a/b/c/d/e ]
	[ -f /out/test/a/b/c/d/foo ]
eot

FROM alpine AS start
COPY --from=base --parents ./test/a/b/c/d /out/
RUN <<eot
	set -ex
	[ -d /out/test/a/b/c/d/e ]
	[ -f /out/test/a/b/c/d/foo ]
eot

FROM alpine AS double
COPY --from=base --parents /test/a/./b/./c /out/
RUN <<eot
	set -ex
	[ -d /out/b/c/d/e ]
	[ -f /out/b/c/d/foo ]
eot

FROM alpine AS wildcard
COPY --from=base --parents /test/a/./*/c /out/
RUN <<eot
	set -ex
	[ -d /out/b/c/d/e ]
	[ -f /out/b2/c/d/e/bar ]
eot

FROM alpine AS doublewildcard
COPY --from=base --parents /test/a/b*/./c/**/e /out/
RUN <<eot
	set -ex
	[ -d /out/c/d/e ]
	[ -f /out/c/d/e/bay ] # via b
	[ -f /out/c/d/e/bar ] # via b2
eot

FROM alpine AS doubleinputs
COPY --from=base --parents /test/a/b/c*/./d/**/baz /test/a/b*/./c/**/bar /out/
RUN <<eot
	set -ex
	[ -f /out/d/e/baz ]
	[ ! -f /out/d/e/bay ]
	[ -f /out/d/e2/baz ]
	[ -f /out/c/d/e/bar ] # via b2
eot
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	for _, target := range []string{"middle", "end", "start", "double", "wildcard", "doublewildcard", "doubleinputs"} {
		t.Logf("target: %s", target)
		_, err = f.Solve(sb.Context(), c, client.SolveOpt{
			FrontendAttrs: map[string]string{
				"target": target,
			},
			LocalMounts: map[string]fsutil.FS{
				dockerui.DefaultLocalNameDockerfile: dir,
				dockerui.DefaultLocalNameContext:    dir,
			},
		}, nil)
		require.NoError(t, err)
	}
}
