package dockerfile

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	v1 "github.com/moby/buildkit/cache/remotecache/v1"
	"github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/continuity/fs/fstest"
	intoto "github.com/in-toto/in-toto-golang/in_toto"
	controlapi "github.com/moby/buildkit/api/services/control"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/dockerui"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/session/upload/uploadprovider"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/solver/pb"
	spb "github.com/moby/buildkit/sourcepolicy/pb"
	"github.com/moby/buildkit/util/contentutil"
	"github.com/moby/buildkit/util/testutil"
	"github.com/moby/buildkit/util/testutil/httpserver"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func init() {
	if workers.IsTestDockerd() {
		workers.InitDockerdWorker()
	} else {
		workers.InitOCIWorker()
		workers.InitContainerdWorker()
	}
}

var allTests = integration.TestFuncs(
	testCmdShell,
	testGlobalArg,
	testDockerfileDirs,
	testDockerfileInvalidCommand,
	testDockerfileADDFromURL,
	testDockerfileAddArchive,
	testDockerfileScratchConfig,
	testExportedHistory,
	testExposeExpansion,
	testUser,
	testUserAdditionalGids,
	testCacheReleased,
	testDockerignore,
	testDockerignoreInvalid,
	testDockerfileFromGit,
	testMultiStageImplicitFrom,
	testMultiStageCaseInsensitive,
	testLabels,
	testCacheImportExport,
	testImageManifestCacheImportExport,
	testReproducibleIDs,
	testImportExportReproducibleIDs,
	testNoCache,
	testDockerfileFromHTTP,
	testBuiltinArgs,
	testPullScratch,
	testSymlinkDestination,
	testHTTPDockerfile,
	testPlatformArgsImplicit,
	testPlatformArgsExplicit,
	testExportMultiPlatform,
	testQuotedMetaArgs,
	testIgnoreEntrypoint,
	testSymlinkedDockerfile,
	testEmptyWildcard,
	testWorkdirCreatesDir,
	testDockerfileAddArchiveWildcard,
	testCopyChownExistingDir,
	testCopyWildcardCache,
	testDockerignoreOverride,
	testTarExporter,
	testDefaultEnvWithArgs,
	testEnvEmptyFormatting,
	testCacheMultiPlatformImportExport,
	testOnBuildCleared,
	testFrontendUseForwardedSolveResults,
	testFrontendEvaluate,
	testFrontendInputs,
	testErrorsSourceMap,
	testMultiArgs,
	testFrontendSubrequests,
	testDockefileCheckHostname,
	testDefaultShellAndPath,
	testDockerfileLowercase,
	testExportCacheLoop,
	testWildcardRenameCache,
	testDockerfileInvalidInstruction,
	testShmSize,
	testUlimit,
	testCgroupParent,
	testNamedImageContext,
	testNamedImageContextPlatform,
	testNamedImageContextTimestamps,
	testNamedImageContextScratch,
	testNamedLocalContext,
	testNamedOCILayoutContext,
	testNamedOCILayoutContextExport,
	testNamedInputContext,
	testNamedMultiplatformInputContext,
	testNamedFilteredContext,
	testEmptyDestDir,
	testCopyChownCreateDest,
	testCopyThroughSymlinkContext,
	testCopyThroughSymlinkMultiStage,
	testCopySocket,
	testContextChangeDirToFile,
	testNoSnapshotLeak,
	testCopySymlinks,
	testCopyChown,
	testCopyChmod,
	testCopyOverrideFiles,
	testCopyVarSubstitution,
	testCopyWildcards,
	testCopyRelative,
	testAddURLChmod,
	testTarContext,
	testTarContextExternalDockerfile,
	testWorkdirUser,
	testWorkdirExists,
	testWorkdirCopyIgnoreRelative,
	testOutOfOrderStage,
	testCopyFollowAllSymlinks,
	testDockerfileAddChownExpand,
	testSourceDateEpochWithoutExporter,
	testSBOMScannerImage,
	testProvenanceAttestation,
	testGitProvenanceAttestation,
	testMultiPlatformProvenance,
	testClientFrontendProvenance,
	testClientLLBProvenance,
	testSecretSSHProvenance,
	testOCILayoutProvenance,
	testNilProvenance,
	testDuplicatePlatformProvenance,
	testDockerIgnoreMissingProvenance,
	testSBOMScannerArgs,
	testLintStageName,
	testLintNoEmptyContinuations,
	testSelfConsistentCommandCasing,
	testFileConsistentCommandCasing,
	testNilContextInSolveGateway,
	testCopyUnicodePath,
	testFrontendDeduplicateSources,
	testDuplicateLayersProvenance,
	testSourcePolicyWithNamedContext,
)

// Tests that depend on the `security.*` entitlements
var securityTests = []integration.Test{}

// Tests that depend on the `network.*` entitlements
var networkTests = []integration.Test{}

// Tests that depend on heredoc support
var heredocTests = []integration.Test{}

// Tests that depend on reproducible env
var reproTests = integration.TestFuncs(
	testReproSourceDateEpoch,
)

var opts []integration.TestOpt
var securityOpts []integration.TestOpt

type frontend interface {
	Solve(context.Context, *client.Client, client.SolveOpt, chan *client.SolveStatus) (*client.SolveResponse, error)
	SolveGateway(context.Context, gateway.Client, gateway.SolveRequest) (*gateway.Result, error)
	DFCmdArgs(string, string) (string, string)
	RequiresBuildctl(t *testing.T)
}

func init() {
	frontends := map[string]interface{}{}

	opts = []integration.TestOpt{
		integration.WithMirroredImages(integration.OfficialImages("busybox:latest")),
		integration.WithMatrix("frontend", frontends),
	}

	if os.Getenv("FRONTEND_BUILTIN_ONLY") == "1" {
		frontends["builtin"] = &builtinFrontend{}
	} else if os.Getenv("FRONTEND_CLIENT_ONLY") == "1" {
		frontends["client"] = &clientFrontend{}
	} else if gw := os.Getenv("FRONTEND_GATEWAY_ONLY"); gw != "" {
		name := "buildkit_test/" + identity.NewID() + ":latest"
		opts = append(opts, integration.WithMirroredImages(map[string]string{
			name: gw,
		}))
		frontends["gateway"] = &gatewayFrontend{gw: name}
	} else {
		frontends["builtin"] = &builtinFrontend{}
		frontends["client"] = &clientFrontend{}
	}
}

func TestIntegration(t *testing.T) {
	integration.Run(t, allTests, opts...)

	integration.Run(t, securityTests, append(append(opts, securityOpts...),
		integration.WithMatrix("security.insecure", map[string]interface{}{
			"granted": securityInsecureGranted,
			"denied":  securityInsecureDenied,
		}))...)
	integration.Run(t, networkTests, append(opts,
		integration.WithMatrix("network.host", map[string]interface{}{
			"granted": networkHostGranted,
			"denied":  networkHostDenied,
		}))...)
	integration.Run(t, heredocTests, opts...)
	integration.Run(t, outlineTests, opts...)
	integration.Run(t, targetsTests, opts...)

	integration.Run(t, reproTests, append(opts,
		// Only use the amd64 digest,  regardless to the host platform
		integration.WithMirroredImages(map[string]string{
			"amd64/bullseye-20230109-slim": "docker.io/amd64/debian:bullseye-20230109-slim@sha256:1acb06a0c31fb467eb8327ad361f1091ab265e0bf26d452dea45dcb0c0ea5e75",
		}),
	)...)
}

func testDefaultEnvWithArgs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
ARG image=idlebox
FROM busy${image#idle} AS build
ARG my_arg
ENV my_arg "my_arg=${my_arg:-def_val}"
ENV my_trimmed_arg "${my_arg%%e*}"
COPY myscript.sh myscript.sh
RUN ./myscript.sh $my_arg $my_trimmed_arg
FROM scratch
COPY --from=build /out /out
`)

	script := []byte(`
#!/usr/bin/env sh
echo -n $my_arg $* > /out
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("myscript.sh", script, 0700),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	for _, x := range []struct {
		name          string
		frontendAttrs map[string]string
		expected      string
	}{
		{"nil", nil, "my_arg=def_val my_arg=def_val my_arg=d"},
		{"empty", map[string]string{"build-arg:my_arg": ""}, "my_arg=def_val my_arg=def_val my_arg=d"},
		{"override", map[string]string{"build-arg:my_arg": "override"}, "my_arg=override my_arg=override my_arg=ov"},
	} {
		t.Run(x.name, func(t *testing.T) {
			_, err = f.Solve(sb.Context(), c, client.SolveOpt{
				FrontendAttrs: x.frontendAttrs,
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

			dt, err := os.ReadFile(filepath.Join(destDir, "out"))
			require.NoError(t, err)
			require.Equal(t, x.expected, string(dt))
		})
	}
}

func testEnvEmptyFormatting(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build
ENV myenv foo%sbar
RUN [ "$myenv" = 'foo%sbar' ]
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
}

func testDockerignoreOverride(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	dockerfile := []byte(`
FROM busybox
COPY . .
RUN [ -f foo ] && [ ! -f bar ]
`)

	ignore := []byte(`
bar
`)

	dockerfile2 := []byte(`
FROM busybox
COPY . .
RUN [ ! -f foo ] && [ -f bar ]
`)

	ignore2 := []byte(`
foo
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("Dockerfile.dockerignore", ignore, 0600),
		fstest.CreateFile("Dockerfile2", dockerfile2, 0600),
		fstest.CreateFile("Dockerfile2.dockerignore", ignore2, 0600),
		fstest.CreateFile("foo", []byte("contents0"), 0600),
		fstest.CreateFile("bar", []byte("contents0"), 0600),
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

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"filename": "Dockerfile2",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)
}

func testEmptyDestDir(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
ENV empty=""
COPY testfile $empty
RUN [ "$(cat testfile)" == "contents0" ]
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

func testExportCacheLoop(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureCacheExport, workers.FeatureCacheImport, workers.FeatureCacheBackendLocal)
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM alpine as base
RUN echo aa > /foo
WORKDIR /bar

FROM base as base1
COPY hello.txt .

FROM base as base2
COPY --from=base1 /bar/hello.txt .
RUN true

FROM scratch
COPY --from=base2 /foo /f
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("hello.txt", []byte("hello"), 0600),
	)

	cacheDir := t.TempDir()

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		CacheExports: []client.CacheOptionsEntry{
			{
				Type: "local",
				Attrs: map[string]string{
					"dest": filepath.Join(cacheDir, "cache"),
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	err = c.Prune(sb.Context(), nil)
	require.NoError(t, err)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		CacheExports: []client.CacheOptionsEntry{
			{
				Type: "local",
				Attrs: map[string]string{
					"dest": filepath.Join(cacheDir, "cache"),
				},
			},
		},
		CacheImports: []client.CacheOptionsEntry{
			{
				Type: "local",
				Attrs: map[string]string{
					"src": filepath.Join(cacheDir, "cache"),
				},
			},
		},
		FrontendAttrs: map[string]string{},
	}, nil)
	require.NoError(t, err)
}

func testTarExporter(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch AS stage-linux
COPY foo forlinux

FROM scratch AS stage-darwin
COPY bar fordarwin

FROM stage-$TARGETOS
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("data"), 0600),
		fstest.CreateFile("bar", []byte("data2"), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	buf := &bytes.Buffer{}
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:   client.ExporterTar,
				Output: fixedWriteCloser(&nopWriteCloser{buf}),
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	m, err := testutil.ReadTarToMap(buf.Bytes(), false)
	require.NoError(t, err)

	mi, ok := m["forlinux"]
	require.Equal(t, true, ok)
	require.Equal(t, "data", string(mi.Data))

	// repeat multi-platform
	buf = &bytes.Buffer{}
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:   client.ExporterTar,
				Output: fixedWriteCloser(&nopWriteCloser{buf}),
			},
		},
		FrontendAttrs: map[string]string{
			"platform": "linux/amd64,darwin/amd64",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	m, err = testutil.ReadTarToMap(buf.Bytes(), false)
	require.NoError(t, err)

	mi, ok = m["linux_amd64/forlinux"]
	require.Equal(t, true, ok)
	require.Equal(t, "data", string(mi.Data))

	mi, ok = m["darwin_amd64/fordarwin"]
	require.Equal(t, true, ok)
	require.Equal(t, "data2", string(mi.Data))
}

func testWorkdirCreatesDir(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
WORKDIR /foo
WORKDIR /
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

	fi, err := os.Lstat(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, true, fi.IsDir())
}

func testCacheReleased(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
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

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	checkAllReleasable(t, c, sb, true)
}

func testSymlinkedDockerfile(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
ENV foo bar
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile.web", dockerfile, 0600),
		fstest.Symlink("Dockerfile.web", "Dockerfile"),
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

func testCopyChownExistingDir(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
# Set up files and directories with known ownership
FROM busybox AS source
RUN touch /file && chown 100:200 /file \
 && mkdir -p /dir/subdir \
 && touch /dir/subdir/nestedfile \
 && chown 100:200 /dir \
 && chown 101:201 /dir/subdir \
 && chown 102:202 /dir/subdir/nestedfile

FROM busybox AS test_base
RUN mkdir -p /existingdir/existingsubdir \
 && touch /existingdir/existingfile \
 && chown 500:600 /existingdir \
 && chown 501:601 /existingdir/existingsubdir \
 && chown 501:601 /existingdir/existingfile


# Copy files from the source stage
FROM test_base AS copy_from
COPY --from=source /file .
# Copy to a non-existing target directory creates the target directory (as root), then copies the _contents_ of the source directory into it
COPY --from=source /dir /dir
# Copying to an existing target directory will copy the _contents_ of the source directory into it
COPY --from=source /dir/. /existingdir

RUN e="100:200"; p="/file"                         ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="0:0";     p="/dir"                          ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="101:201"; p="/dir/subdir"                   ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="102:202"; p="/dir/subdir/nestedfile"        ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
# Existing files and directories ownership should not be modified
 && e="500:600"; p="/existingdir"                  ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="501:601"; p="/existingdir/existingsubdir"   ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="501:601"; p="/existingdir/existingfile"     ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
# But new files and directories should maintain their ownership
 && e="101:201"; p="/existingdir/subdir"           ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="102:202"; p="/existingdir/subdir/nestedfile"; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi


# Copy files from the source stage and chown them.
FROM test_base AS copy_from_chowned
COPY --from=source --chown=300:400 /file .
# Copy to a non-existing target directory creates the target directory (as root), then copies the _contents_ of the source directory into it
COPY --from=source --chown=300:400 /dir /dir
# Copying to an existing target directory copies the _contents_ of the source directory into it
COPY --from=source --chown=300:400 /dir/. /existingdir

RUN e="300:400"; p="/file"                         ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="300:400"; p="/dir"                          ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="300:400"; p="/dir/subdir"                   ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="300:400"; p="/dir/subdir/nestedfile"        ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
# Existing files and directories ownership should not be modified
 && e="500:600"; p="/existingdir"                  ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="501:601"; p="/existingdir/existingsubdir"   ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="501:601"; p="/existingdir/existingfile"     ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
# But new files and directories should be chowned
 && e="300:400"; p="/existingdir/subdir"           ; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi \
 && e="300:400"; p="/existingdir/subdir/nestedfile"; a=` + "`" + `stat -c "%u:%g" "$p"` + "`" + `; if [ "$a" != "$e" ]; then echo "incorrect ownership on $p. expected $e, got $a"; exit 1; fi
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile.web", dockerfile, 0600),
		fstest.Symlink("Dockerfile.web", "Dockerfile"),
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

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"target": "copy_from",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)
}

func testCopyWildcardCache(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS base
COPY foo* files/
RUN cat /dev/urandom | head -c 100 | sha256sum > unique
COPY bar files/
FROM scratch
COPY --from=base unique /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo1", []byte("foo1-data"), 0600),
		fstest.CreateFile("foo2", []byte("foo2-data"), 0600),
		fstest.CreateFile("bar", []byte("bar-data"), 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir.Name, "bar"), []byte("bar-data-mod"), 0600)
	require.NoError(t, err)

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
	require.Equal(t, string(dt), string(dt2))

	err = os.WriteFile(filepath.Join(dir.Name, "foo2"), []byte("foo2-data-mod"), 0600)
	require.NoError(t, err)

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

	dt2, err = os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)
	require.NotEqual(t, string(dt), string(dt2))
}

func testEmptyWildcard(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY foo nomatch* /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("contents0"), 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "contents0", string(dt))
}

func testWorkdirUser(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN adduser -D user
USER user
WORKDIR /mydir
RUN [ "$(stat -c "%U %G" /mydir)" == "user user" ]
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

func testOutOfOrderStage(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	for _, src := range []string{"/", "/d2"} {
		dockerfile := []byte(fmt.Sprintf(`
FROM busybox AS target
COPY --from=build %s /out

FROM alpine AS build
COPY /Dockerfile /d2

FROM target
`, src))

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
		require.Contains(t, err.Error(), "cannot copy from stage")
		require.Contains(t, err.Error(), "needs to be defined before current stage")
	}
}

func testWorkdirCopyIgnoreRelative(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch AS base
WORKDIR /foo
COPY Dockerfile / 
FROM scratch
# relative path still loaded as absolute
COPY --from=base Dockerfile .
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

func testWorkdirExists(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN adduser -D user
RUN mkdir /mydir && chown user:user /mydir
WORKDIR /mydir
RUN [ "$(stat -c "%U %G" /mydir)" == "user user" ]
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

func testCopyChownCreateDest(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
ARG group
ENV owner user01
RUN adduser -D user
RUN adduser -D user01
COPY --chown=user:user . /dest
COPY --chown=${owner}:${group} . /dest01
RUN [ "$(stat -c "%U %G" /dest)" == "user user" ]
RUN [ "$(stat -c "%U %G" /dest01)" == "user01 user" ]
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"build-arg:group": "user",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)
}

func testCopyThroughSymlinkContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY link/foo .
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.Symlink("sub", "link"),
		fstest.CreateDir("sub", 0700),
		fstest.CreateFile("sub/foo", []byte(`contents`), 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "contents", string(dt))
}

func testCopyThroughSymlinkMultiStage(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build
RUN mkdir -p /out/sub && ln -s /out/sub /sub && ln -s out/sub /sub2 && echo -n "data" > /sub/foo
FROM scratch
COPY --from=build /sub/foo .
COPY --from=build /sub2/foo bar
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

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "data", string(dt))
}

func testCopySocket(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY . /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateSocket("socket.sock", 0600),
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

	fi, err := os.Lstat(filepath.Join(destDir, "socket.sock"))
	require.NoError(t, err)
	// make sure socket is converted to regular file.
	require.Equal(t, fi.Mode().IsRegular(), true)
}

func testIgnoreEntrypoint(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
ENTRYPOINT ["/nosuchcmd"]
RUN ["ls"]
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

func testQuotedMetaArgs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
ARG a1="box"
ARG a2="$a1-foo"
FROM busy$a1 AS build
ARG a2
ARG a3="bar-$a2"
RUN echo -n $a3 > /out
FROM scratch
COPY --from=build /out .
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
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.Equal(t, "bar-box-foo", string(dt))
}

func testMultiArgs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
ARG a1="foo bar" a2=box
ARG a3="$a2-foo"
FROM busy$a2 AS build
ARG a3 a4="123 456" a1
RUN echo -n "$a1:$a3:$a4" > /out
FROM scratch
COPY --from=build /out .
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
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.Equal(t, "foo bar:box-foo:123 456", string(dt))
}

func testDefaultShellAndPath(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter)
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
ENTRYPOINT foo bar
COPY Dockerfile .
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	out := filepath.Join(destDir, "out.tar")
	outW, err := os.Create(out)
	require.NoError(t, err)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		FrontendAttrs: map[string]string{
			"platform": "windows/amd64,linux/amd64",
		},
		Exports: []client.ExportEntry{
			{
				Type:   client.ExporterOCI,
				Output: fixedWriteCloser(outW),
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out.tar"))
	require.NoError(t, err)

	m, err := testutil.ReadTarToMap(dt, false)
	require.NoError(t, err)

	var idx ocispecs.Index
	err = json.Unmarshal(m[ocispecs.ImageIndexFile].Data, &idx)
	require.NoError(t, err)

	mlistHex := idx.Manifests[0].Digest.Hex()

	idx = ocispecs.Index{}
	err = json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+mlistHex].Data, &idx)
	require.NoError(t, err)

	require.Equal(t, 2, len(idx.Manifests))

	for i, exp := range []struct {
		p          string
		entrypoint []string
		env        []string
	}{
		{p: "windows/amd64", entrypoint: []string{"cmd", "/S", "/C", "foo bar"}, env: []string{"PATH=c:\\Windows\\System32;c:\\Windows"}},
		{p: "linux/amd64", entrypoint: []string{"/bin/sh", "-c", "foo bar"}, env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}},
	} {
		t.Run(exp.p, func(t *testing.T) {
			require.Equal(t, exp.p, platforms.Format(*idx.Manifests[i].Platform))

			var mfst ocispecs.Manifest
			err = json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+idx.Manifests[i].Digest.Hex()].Data, &mfst)
			require.NoError(t, err)

			require.Equal(t, 1, len(mfst.Layers))

			var img ocispecs.Image
			err = json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+mfst.Config.Digest.Hex()].Data, &img)
			require.NoError(t, err)

			require.Equal(t, exp.entrypoint, img.Config.Entrypoint)
			require.Equal(t, exp.env, img.Config.Env)
		})
	}
}

func testExportMultiPlatform(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter, workers.FeatureMultiPlatform)
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
ARG TARGETARCH
ARG TARGETPLATFORM
LABEL target=$TARGETPLATFORM
COPY arch-$TARGETARCH whoami
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("arch-arm", []byte(`i am arm`), 0600),
		fstest.CreateFile("arch-amd64", []byte(`i am amd64`), 0600),
		fstest.CreateFile("arch-s390x", []byte(`i am s390x`), 0600),
		fstest.CreateFile("arch-ppc64le", []byte(`i am ppc64le`), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		FrontendAttrs: map[string]string{
			"platform": "windows/amd64,linux/arm,linux/s390x",
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "windows_amd64/whoami"))
	require.NoError(t, err)
	require.Equal(t, "i am amd64", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "linux_arm_v7/whoami"))
	require.NoError(t, err)
	require.Equal(t, "i am arm", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "linux_s390x/whoami"))
	require.NoError(t, err)
	require.Equal(t, "i am s390x", string(dt))

	// repeat with oci exporter

	destDir = t.TempDir()

	out := filepath.Join(destDir, "out.tar")
	outW, err := os.Create(out)
	require.NoError(t, err)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		FrontendAttrs: map[string]string{
			"platform": "windows/amd64,linux/arm/v6,linux/ppc64le",
		},
		Exports: []client.ExportEntry{
			{
				Type:   client.ExporterOCI,
				Output: fixedWriteCloser(outW),
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err = os.ReadFile(filepath.Join(destDir, "out.tar"))
	require.NoError(t, err)

	m, err := testutil.ReadTarToMap(dt, false)
	require.NoError(t, err)

	var idx ocispecs.Index
	err = json.Unmarshal(m[ocispecs.ImageIndexFile].Data, &idx)
	require.NoError(t, err)

	mlistHex := idx.Manifests[0].Digest.Hex()

	idx = ocispecs.Index{}
	err = json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+mlistHex].Data, &idx)
	require.NoError(t, err)

	require.Equal(t, 3, len(idx.Manifests))

	for i, exp := range []struct {
		p    string
		os   string
		arch string
		dt   string
	}{
		{p: "windows/amd64", os: "windows", arch: "amd64", dt: "i am amd64"},
		{p: "linux/arm/v6", os: "linux", arch: "arm", dt: "i am arm"},
		{p: "linux/ppc64le", os: "linux", arch: "ppc64le", dt: "i am ppc64le"},
	} {
		t.Run(exp.p, func(t *testing.T) {
			require.Equal(t, exp.p, platforms.Format(*idx.Manifests[i].Platform))

			var mfst ocispecs.Manifest
			err = json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+idx.Manifests[i].Digest.Hex()].Data, &mfst)
			require.NoError(t, err)

			require.Equal(t, 1, len(mfst.Layers))

			m2, err := testutil.ReadTarToMap(m[ocispecs.ImageBlobsDir+"/sha256/"+mfst.Layers[0].Digest.Hex()].Data, true)
			require.NoError(t, err)
			require.Equal(t, exp.dt, string(m2["whoami"].Data))

			var img ocispecs.Image
			err = json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+mfst.Config.Digest.Hex()].Data, &img)
			require.NoError(t, err)

			require.Equal(t, exp.os, img.OS)
			require.Equal(t, exp.arch, img.Architecture)
			v, ok := img.Config.Labels["target"]
			require.True(t, ok)
			require.Equal(t, exp.p, v)
		})
	}
}

// tonistiigi/fsutil#46
func testContextChangeDirToFile(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY foo /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateDir("foo", 0700),
		fstest.CreateFile("foo/bar", []byte(`contents`), 0600),
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

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte(`contents2`), 0600),
	)
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

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "contents2", string(dt))
}

func testNoSnapshotLeak(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY foo /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte(`contents`), 0600),
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

	du, err := c.DiskUsage(sb.Context())
	require.NoError(t, err)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	du2, err := c.DiskUsage(sb.Context())
	require.NoError(t, err)

	require.Equal(t, len(du), len(du2))
}

// #1197
func testCopyFollowAllSymlinks(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY foo /
COPY foo/sub bar
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("bar", []byte(`bar-contents`), 0600),
		fstest.CreateDir("foo", 0700),
		fstest.Symlink("../bar", "foo/sub"),
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

func testCopySymlinks(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY foo /
COPY sub/l* alllinks/
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("bar", []byte(`bar-contents`), 0600),
		fstest.Symlink("bar", "foo"),
		fstest.CreateDir("sub", 0700),
		fstest.CreateFile("sub/lfile", []byte(`lfile-contents`), 0600),
		fstest.Symlink("subfile", "sub/l0"),
		fstest.CreateFile("sub/subfile", []byte(`subfile-contents`), 0600),
		fstest.Symlink("second", "sub/l1"),
		fstest.Symlink("baz", "sub/second"),
		fstest.CreateFile("sub/baz", []byte(`baz-contents`), 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "bar-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "alllinks/l0"))
	require.NoError(t, err)
	require.Equal(t, "subfile-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "alllinks/lfile"))
	require.NoError(t, err)
	require.Equal(t, "lfile-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "alllinks/l1"))
	require.NoError(t, err)
	require.Equal(t, "baz-contents", string(dt))
}

func testHTTPDockerfile(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN echo -n "foo-contents" > /foo
FROM scratch
COPY --from=0 /foo /foo
`)

	srcDir := t.TempDir()

	err := os.WriteFile(filepath.Join(srcDir, "Dockerfile"), dockerfile, 0600)
	require.NoError(t, err)

	resp := httpserver.Response{
		Etag:    identity.NewID(),
		Content: dockerfile,
	}

	server := httpserver.NewTestServer(map[string]httpserver.Response{
		"/df": resp,
	})
	defer server.Close()

	destDir := t.TempDir()

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context":  server.URL + "/df",
			"filename": "mydockerfile", // this is bogus, any name should work
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))
}

func testCmdShell(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("test is only for containerd worker")
	}

	dockerfile := []byte(`
FROM scratch
CMD ["test"]
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := "docker.io/moby/cmdoverridetest:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dockerfile = []byte(`
FROM docker.io/moby/cmdoverridetest:latest
SHELL ["ls"]
ENTRYPOINT my entrypoint
`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	target = "docker.io/moby/cmdoverridetest2:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	ctr, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer ctr.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	img, err := ctr.ImageService().Get(ctx, target)
	require.NoError(t, err)

	desc, err := img.Config(ctx, ctr.ContentStore(), platforms.Default())
	require.NoError(t, err)

	dt, err := content.ReadBlob(ctx, ctr.ContentStore(), desc)
	require.NoError(t, err)

	var ociimg ocispecs.Image
	err = json.Unmarshal(dt, &ociimg)
	require.NoError(t, err)

	require.Equal(t, []string(nil), ociimg.Config.Cmd)
	require.Equal(t, []string{"ls", "my entrypoint"}, ociimg.Config.Entrypoint)
}

func testPullScratch(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("test is only for containerd worker")
	}

	dockerfile := []byte(`
FROM scratch
LABEL foo=bar
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := "docker.io/moby/testpullscratch:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dockerfile = []byte(`
FROM docker.io/moby/testpullscratch:latest
LABEL bar=baz
COPY foo .
`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("foo-contents"), 0600),
	)

	target = "docker.io/moby/testpullscratch2:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	ctr, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer ctr.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	img, err := ctr.ImageService().Get(ctx, target)
	require.NoError(t, err)

	desc, err := img.Config(ctx, ctr.ContentStore(), platforms.Default())
	require.NoError(t, err)

	dt, err := content.ReadBlob(ctx, ctr.ContentStore(), desc)
	require.NoError(t, err)

	var ociimg ocispecs.Image
	err = json.Unmarshal(dt, &ociimg)
	require.NoError(t, err)

	require.Equal(t, "layers", ociimg.RootFS.Type)
	require.Equal(t, 1, len(ociimg.RootFS.DiffIDs))
	v, ok := ociimg.Config.Labels["foo"]
	require.True(t, ok)
	require.Equal(t, "bar", v)
	v, ok = ociimg.Config.Labels["bar"]
	require.True(t, ok)
	require.Equal(t, "baz", v)

	echo := llb.Image("busybox").
		Run(llb.Shlex(`sh -c "echo -n foo0 > /empty/foo"`)).
		AddMount("/empty", llb.Image("docker.io/moby/testpullscratch:latest"))

	def, err := echo.Marshal(sb.Context())
	require.NoError(t, err)

	destDir := t.TempDir()

	_, err = c.Solve(sb.Context(), def, client.SolveOpt{
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

	dt, err = os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "foo0", string(dt))
}

func testGlobalArg(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
ARG tag=nosuchtag
FROM busybox:${tag}
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"build-arg:tag": "latest",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)
}

func testDockerfileDirs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)

	dockerfile := []byte(`
	FROM busybox
	COPY foo /foo2
	COPY foo /
	RUN echo -n bar > foo3
	RUN test -f foo
	RUN cmp -s foo foo2
	RUN cmp -s foo foo3
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("bar"), 0600),
	)

	args, trace := f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	cmd := sb.Cmd(args)
	require.NoError(t, cmd.Run())

	_, err := os.Stat(trace)
	require.NoError(t, err)

	// relative urls
	args, trace = f.DFCmdArgs(".", ".")
	defer os.RemoveAll(trace)

	cmd = sb.Cmd(args)
	cmd.Dir = dir.Name
	require.NoError(t, cmd.Run())

	_, err = os.Stat(trace)
	require.NoError(t, err)

	// different context and dockerfile directories
	dir1 := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	dir2 := integration.Tmpdir(
		t,
		fstest.CreateFile("foo", []byte("bar"), 0600),
	)

	args, trace = f.DFCmdArgs(dir2.Name, dir1.Name)
	defer os.RemoveAll(trace)

	cmd = sb.Cmd(args)
	cmd.Dir = dir.Name
	require.NoError(t, cmd.Run())

	_, err = os.Stat(trace)
	require.NoError(t, err)

	// TODO: test trace file output, cache hits, logs etc.
	// TODO: output metadata about original dockerfile command in trace
}

func testDockerfileInvalidCommand(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)
	dockerfile := []byte(`
	FROM busybox
	RUN invalidcmd
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	args, trace := f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	cmd := sb.Cmd(args)
	stdout := new(bytes.Buffer)
	cmd.Stderr = stdout
	err := cmd.Run()
	require.Error(t, err)
	require.Contains(t, stdout.String(), "/bin/sh -c invalidcmd")
	require.Contains(t, stdout.String(), "did not complete successfully")
}

func testDockerfileInvalidInstruction(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)
	dockerfile := []byte(`
	FROM scratch
	FNTRYPOINT ["/bin/sh", "-c", "echo invalidinstruction"]
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
	require.Contains(t, err.Error(), "unknown instruction: FNTRYPOINT")
	require.Contains(t, err.Error(), "did you mean ENTRYPOINT?")
}

func testDockerfileADDFromURL(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)

	modTime := time.Now().Add(-24 * time.Hour) // avoid falso positive with current time

	resp := httpserver.Response{
		Etag:    identity.NewID(),
		Content: []byte("content1"),
	}

	resp2 := httpserver.Response{
		Etag:         identity.NewID(),
		LastModified: &modTime,
		Content:      []byte("content2"),
	}

	server := httpserver.NewTestServer(map[string]httpserver.Response{
		"/foo": resp,
		"/":    resp2,
	})
	defer server.Close()

	dockerfile := []byte(fmt.Sprintf(`
FROM scratch
ADD %s /dest/
`, server.URL+"/foo"))

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	args, trace := f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	destDir := t.TempDir()

	cmd := sb.Cmd(args + fmt.Sprintf(" --output type=local,dest=%s", destDir))
	err := cmd.Run()
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "dest/foo"))
	require.NoError(t, err)
	require.Equal(t, []byte("content1"), dt)

	// test the default properties
	dockerfile = []byte(fmt.Sprintf(`
FROM scratch
ADD %s /dest/
`, server.URL+"/"))

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	args, trace = f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	destDir = t.TempDir()

	cmd = sb.Cmd(args + fmt.Sprintf(" --output type=local,dest=%s", destDir))
	err = cmd.Run()
	require.NoError(t, err)

	destFile := filepath.Join(destDir, "dest/__unnamed__")
	dt, err = os.ReadFile(destFile)
	require.NoError(t, err)
	require.Equal(t, []byte("content2"), dt)

	fi, err := os.Stat(destFile)
	require.NoError(t, err)
	require.Equal(t, modTime.Format(http.TimeFormat), fi.ModTime().Format(http.TimeFormat))
}

func testDockerfileAddArchive(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)

	buf := bytes.NewBuffer(nil)
	tw := tar.NewWriter(buf)
	expectedContent := []byte("content0")
	err := tw.WriteHeader(&tar.Header{
		Name:     "foo",
		Typeflag: tar.TypeReg,
		Size:     int64(len(expectedContent)),
		Mode:     0644,
	})
	require.NoError(t, err)
	_, err = tw.Write(expectedContent)
	require.NoError(t, err)
	err = tw.Close()
	require.NoError(t, err)

	dockerfile := []byte(`
FROM scratch
ADD t.tar /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("t.tar", buf.Bytes(), 0600),
	)

	args, trace := f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	destDir := t.TempDir()

	cmd := sb.Cmd(args + fmt.Sprintf(" --output type=local,dest=%s", destDir))
	require.NoError(t, cmd.Run())

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, expectedContent, dt)

	// add gzip tar
	buf2 := bytes.NewBuffer(nil)
	gz := gzip.NewWriter(buf2)
	_, err = gz.Write(buf.Bytes())
	require.NoError(t, err)
	err = gz.Close()
	require.NoError(t, err)

	dockerfile = []byte(`
FROM scratch
ADD t.tar.gz /
`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("t.tar.gz", buf2.Bytes(), 0600),
	)

	args, trace = f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	destDir = t.TempDir()

	cmd = sb.Cmd(args + fmt.Sprintf(" --output type=local,dest=%s", destDir))
	require.NoError(t, cmd.Run())

	dt, err = os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, expectedContent, dt)

	// COPY doesn't extract
	dockerfile = []byte(`
FROM scratch
COPY t.tar.gz /
`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("t.tar.gz", buf2.Bytes(), 0600),
	)

	args, trace = f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	destDir = t.TempDir()

	cmd = sb.Cmd(args + fmt.Sprintf(" --output type=local,dest=%s", destDir))
	require.NoError(t, cmd.Run())

	dt, err = os.ReadFile(filepath.Join(destDir, "t.tar.gz"))
	require.NoError(t, err)
	require.Equal(t, buf2.Bytes(), dt)

	// ADD from URL doesn't extract
	resp := httpserver.Response{
		Etag:    identity.NewID(),
		Content: buf2.Bytes(),
	}

	server := httpserver.NewTestServer(map[string]httpserver.Response{
		"/t.tar.gz": resp,
	})
	defer server.Close()

	dockerfile = []byte(fmt.Sprintf(`
FROM scratch
ADD %s /
`, server.URL+"/t.tar.gz"))

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	args, trace = f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	destDir = t.TempDir()

	cmd = sb.Cmd(args + fmt.Sprintf(" --output type=local,dest=%s", destDir))
	require.NoError(t, cmd.Run())

	dt, err = os.ReadFile(filepath.Join(destDir, "t.tar.gz"))
	require.NoError(t, err)
	require.Equal(t, buf2.Bytes(), dt)

	// https://github.com/moby/buildkit/issues/386
	dockerfile = []byte(fmt.Sprintf(`
FROM scratch
ADD %s /newname.tar.gz
`, server.URL+"/t.tar.gz"))

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	args, trace = f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	destDir = t.TempDir()

	cmd = sb.Cmd(args + fmt.Sprintf(" --output type=local,dest=%s", destDir))
	require.NoError(t, cmd.Run())

	dt, err = os.ReadFile(filepath.Join(destDir, "newname.tar.gz"))
	require.NoError(t, err)
	require.Equal(t, buf2.Bytes(), dt)
}

func testDockerfileAddArchiveWildcard(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	buf := bytes.NewBuffer(nil)
	tw := tar.NewWriter(buf)
	expectedContent := []byte("content0")
	err := tw.WriteHeader(&tar.Header{
		Name:     "foo",
		Typeflag: tar.TypeReg,
		Size:     int64(len(expectedContent)),
		Mode:     0644,
	})
	require.NoError(t, err)
	_, err = tw.Write(expectedContent)
	require.NoError(t, err)
	err = tw.Close()
	require.NoError(t, err)

	buf2 := bytes.NewBuffer(nil)
	tw = tar.NewWriter(buf2)
	expectedContent = []byte("content1")
	err = tw.WriteHeader(&tar.Header{
		Name:     "bar",
		Typeflag: tar.TypeReg,
		Size:     int64(len(expectedContent)),
		Mode:     0644,
	})
	require.NoError(t, err)
	_, err = tw.Write(expectedContent)
	require.NoError(t, err)
	err = tw.Close()
	require.NoError(t, err)

	dockerfile := []byte(`
FROM scratch
ADD *.tar /dest
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("t.tar", buf.Bytes(), 0600),
		fstest.CreateFile("b.tar", buf2.Bytes(), 0600),
	)

	destDir := t.TempDir()

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

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

	dt, err := os.ReadFile(filepath.Join(destDir, "dest/foo"))
	require.NoError(t, err)
	require.Equal(t, "content0", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "dest/bar"))
	require.NoError(t, err)
	require.Equal(t, "content1", string(dt))
}

func testDockerfileAddChownExpand(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
ARG group
ENV owner 1000
ADD --chown=${owner}:${group} foo /
RUN [ "$(stat -c "%u %G" /foo)" == "1000 nobody" ]
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte(`foo-contents`), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"build-arg:group": "nobody",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)
}

func testSymlinkDestination(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)

	buf := bytes.NewBuffer(nil)
	tw := tar.NewWriter(buf)
	expectedContent := []byte("content0")
	err := tw.WriteHeader(&tar.Header{
		Name:     "symlink",
		Typeflag: tar.TypeSymlink,
		Linkname: "../tmp/symlink-target",
		Mode:     0755,
	})
	require.NoError(t, err)
	err = tw.Close()
	require.NoError(t, err)

	dockerfile := []byte(`
FROM scratch
ADD t.tar /
COPY foo /symlink/
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", expectedContent, 0600),
		fstest.CreateFile("t.tar", buf.Bytes(), 0600),
	)

	args, trace := f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	destDir := t.TempDir()

	cmd := sb.Cmd(args + fmt.Sprintf(" --output type=local,dest=%s", destDir))
	require.NoError(t, cmd.Run())

	dt, err := os.ReadFile(filepath.Join(destDir, "tmp/symlink-target/foo"))
	require.NoError(t, err)
	require.Equal(t, expectedContent, dt)
}

func testDockerfileScratchConfig(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("test requires containerd worker")
	}

	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)
	dockerfile := []byte(`
FROM scratch
ENV foo=bar
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	args, trace := f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	target := "example.com/moby/dockerfilescratch:test"
	cmd := sb.Cmd(args + " --output type=image,name=" + target)
	err := cmd.Run()
	require.NoError(t, err)

	client, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	img, err := client.ImageService().Get(ctx, target)
	require.NoError(t, err)

	desc, err := img.Config(ctx, client.ContentStore(), platforms.Default())
	require.NoError(t, err)

	dt, err := content.ReadBlob(ctx, client.ContentStore(), desc)
	require.NoError(t, err)

	var ociimg ocispecs.Image
	err = json.Unmarshal(dt, &ociimg)
	require.NoError(t, err)

	require.NotEqual(t, "", ociimg.OS)
	require.NotEqual(t, "", ociimg.Architecture)
	require.NotEqual(t, "", ociimg.Config.WorkingDir)
	require.Equal(t, "layers", ociimg.RootFS.Type)
	require.Equal(t, 0, len(ociimg.RootFS.DiffIDs))

	require.Equal(t, 1, len(ociimg.History))
	require.Contains(t, ociimg.History[0].CreatedBy, "ENV foo=bar")
	require.Equal(t, true, ociimg.History[0].EmptyLayer)

	require.Contains(t, ociimg.Config.Env, "foo=bar")
	require.Condition(t, func() bool {
		for _, env := range ociimg.Config.Env {
			if strings.HasPrefix(env, "PATH=") {
				return true
			}
		}
		return false
	})
}

func testExposeExpansion(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureImageExporter)
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
ARG PORTS="3000 4000/udp"
EXPOSE $PORTS
EXPOSE 5000
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := "example.com/moby/dockerfileexpansion:test"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("rest of test requires containerd worker")
	}

	client, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	img, err := client.ImageService().Get(ctx, target)
	require.NoError(t, err)

	desc, err := img.Config(ctx, client.ContentStore(), platforms.Default())
	require.NoError(t, err)

	dt, err := content.ReadBlob(ctx, client.ContentStore(), desc)
	require.NoError(t, err)

	var ociimg ocispecs.Image
	err = json.Unmarshal(dt, &ociimg)
	require.NoError(t, err)

	require.Equal(t, 3, len(ociimg.Config.ExposedPorts))

	var ports []string
	for p := range ociimg.Config.ExposedPorts {
		ports = append(ports, p)
	}

	sort.Strings(ports)

	require.Equal(t, "3000/tcp", ports[0])
	require.Equal(t, "4000/udp", ports[1])
	require.Equal(t, "5000/tcp", ports[2])
}

func testDockerignore(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY . .
`)

	dockerignore := []byte(`
ba*
Dockerfile
!bay
.dockerignore
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte(`foo-contents`), 0600),
		fstest.CreateFile("bar", []byte(`bar-contents`), 0600),
		fstest.CreateFile("baz", []byte(`baz-contents`), 0600),
		fstest.CreateFile("bay", []byte(`bay-contents`), 0600),
		fstest.CreateFile(".dockerignore", dockerignore, 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	_, err = os.Stat(filepath.Join(destDir, ".dockerignore"))
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

	_, err = os.Stat(filepath.Join(destDir, "Dockerfile"))
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

	_, err = os.Stat(filepath.Join(destDir, "bar"))
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

	_, err = os.Stat(filepath.Join(destDir, "baz"))
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

	dt, err = os.ReadFile(filepath.Join(destDir, "bay"))
	require.NoError(t, err)
	require.Equal(t, "bay-contents", string(dt))
}

func testDockerignoreInvalid(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY . .
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile(".dockerignore", []byte("!\n"), 0600),
	)

	ctx, cancel := context.WithCancelCause(sb.Context())
	ctx, _ = context.WithTimeoutCause(ctx, 15*time.Second, errors.WithStack(context.DeadlineExceeded))
	defer cancel(errors.WithStack(context.Canceled))

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(ctx, c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	// err is either the expected error due to invalid dockerignore or error from the timeout
	require.Error(t, err)
	select {
	case <-ctx.Done():
		t.Fatal("timed out")
	default:
	}
}

// moby/moby#10858
func testDockerfileLowercase(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`FROM scratch
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("dockerfile", dockerfile, 0600),
	)

	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(ctx, c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)
}

func testExportedHistory(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)

	// using multi-stage to test that history is scoped to one stage
	dockerfile := []byte(`
FROM busybox AS base
ENV foo=bar
COPY foo /foo2
FROM busybox
LABEL lbl=val
COPY --from=base foo2 foo3
WORKDIR /
RUN echo bar > foo4
RUN ["ls"]
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("contents0"), 0600),
	)

	args, trace := f.DFCmdArgs(dir.Name, dir.Name)
	defer os.RemoveAll(trace)

	workers.CheckFeatureCompat(t, sb, workers.FeatureImageExporter)

	target := "example.com/moby/dockerfilescratch:test"
	cmd := sb.Cmd(args + " --output type=image,name=" + target)
	require.NoError(t, cmd.Run())

	// TODO: expose this test to OCI worker
	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("rest of test requires containerd worker")
	}

	client, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	img, err := client.ImageService().Get(ctx, target)
	require.NoError(t, err)

	desc, err := img.Config(ctx, client.ContentStore(), platforms.Default())
	require.NoError(t, err)

	dt, err := content.ReadBlob(ctx, client.ContentStore(), desc)
	require.NoError(t, err)

	var ociimg ocispecs.Image
	err = json.Unmarshal(dt, &ociimg)
	require.NoError(t, err)

	require.Equal(t, "layers", ociimg.RootFS.Type)
	// this depends on busybox. should be ok after freezing images
	require.Equal(t, 4, len(ociimg.RootFS.DiffIDs))

	require.Equal(t, 7, len(ociimg.History))
	require.Contains(t, ociimg.History[2].CreatedBy, "lbl=val")
	require.Equal(t, true, ociimg.History[2].EmptyLayer)
	require.NotNil(t, ociimg.History[2].Created)
	require.Contains(t, ociimg.History[3].CreatedBy, "COPY foo2 foo3")
	require.Equal(t, false, ociimg.History[3].EmptyLayer)
	require.NotNil(t, ociimg.History[3].Created)
	require.Contains(t, ociimg.History[4].CreatedBy, "WORKDIR /")
	require.Equal(t, true, ociimg.History[4].EmptyLayer)
	require.NotNil(t, ociimg.History[4].Created)
	require.Contains(t, ociimg.History[5].CreatedBy, "echo bar > foo4")
	require.Equal(t, false, ociimg.History[5].EmptyLayer)
	require.NotNil(t, ociimg.History[5].Created)
	require.Contains(t, ociimg.History[6].CreatedBy, "RUN ls")
	require.Equal(t, false, ociimg.History[6].EmptyLayer)
	require.NotNil(t, ociimg.History[6].Created)
}

func testUser(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureImageExporter)
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS base
RUN mkdir -m 0777 /out
RUN id -un > /out/rootuser

# Make sure our defaults work
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)" = '0:0/root:root' ]

# TODO decide if "args.user = strconv.Itoa(syscall.Getuid())" is acceptable behavior for changeUser in sysvinit instead of "return nil" when "USER" isn't specified (so that we get the proper group list even if that is the empty list, even in the default case of not supplying an explicit USER to run as, which implies USER 0)
USER root
RUN [ "$(id -G):$(id -Gn)" = '0 10:root wheel' ]

# Setup dockerio user and group
RUN echo 'dockerio:x:1001:1001::/bin:/bin/false' >> /etc/passwd && \
	echo 'dockerio:x:1001:' >> /etc/group

# Make sure we can switch to our user and all the information is exactly as we expect it to be
USER dockerio
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1001/dockerio:dockerio/1001:dockerio' ]

# Switch back to root and double check that worked exactly as we might expect it to
USER root
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '0:0/root:root/0 10:root wheel' ] && \
        # Add a "supplementary" group for our dockerio user
	echo 'supplementary:x:1002:dockerio' >> /etc/group

# ... and then go verify that we get it like we expect
USER dockerio
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1001/dockerio:dockerio/1001 1002:dockerio supplementary' ]
USER 1001
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1001/dockerio:dockerio/1001 1002:dockerio supplementary' ]

# super test the new "user:group" syntax
USER dockerio:dockerio
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1001/dockerio:dockerio/1001:dockerio' ]
USER 1001:dockerio
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1001/dockerio:dockerio/1001:dockerio' ]
USER dockerio:1001
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1001/dockerio:dockerio/1001:dockerio' ]
USER 1001:1001
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1001/dockerio:dockerio/1001:dockerio' ]
USER dockerio:supplementary
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1002/dockerio:supplementary/1002:supplementary' ]
USER dockerio:1002
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1002/dockerio:supplementary/1002:supplementary' ]
USER 1001:supplementary
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1002/dockerio:supplementary/1002:supplementary' ]
USER 1001:1002
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1001:1002/dockerio:supplementary/1002:supplementary' ]

# make sure unknown uid/gid still works properly
USER 1042:1043
RUN [ "$(id -u):$(id -g)/$(id -un):$(id -gn)/$(id -G):$(id -Gn)" = '1042:1043/1042:1043/1043:1043' ]
USER daemon
RUN id -un > /out/daemonuser
FROM scratch
COPY --from=base /out /
USER nobody
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

	dt, err := os.ReadFile(filepath.Join(destDir, "rootuser"))
	require.NoError(t, err)
	require.Equal(t, "root\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "daemonuser"))
	require.NoError(t, err)
	require.Equal(t, "daemon\n", string(dt))

	// test user in exported
	target := "example.com/moby/dockerfileuser:test"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("rest of test requires containerd worker")
	}

	client, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	img, err := client.ImageService().Get(ctx, target)
	require.NoError(t, err)

	desc, err := img.Config(ctx, client.ContentStore(), platforms.Default())
	require.NoError(t, err)

	dt, err = content.ReadBlob(ctx, client.ContentStore(), desc)
	require.NoError(t, err)

	var ociimg ocispecs.Image
	err = json.Unmarshal(dt, &ociimg)
	require.NoError(t, err)

	require.Equal(t, "nobody", ociimg.Config.User)
}

// testUserAdditionalGids ensures that that the primary GID is also included in the additional GID list.
// CVE-2023-25173: https://github.com/advisories/GHSA-hmfx-3pcx-653p
func testUserAdditionalGids(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
# Mimics the tests in https://github.com/containerd/containerd/commit/3eda46af12b1deedab3d0802adb2e81cb3521950
FROM busybox
SHELL ["/bin/sh", "-euxc"]
RUN [ "$(id)" = "uid=0(root) gid=0(root) groups=0(root),10(wheel)" ]
USER 1234
RUN [ "$(id)" = "uid=1234 gid=0(root) groups=0(root)" ]
USER 1234:1234
RUN [ "$(id)" = "uid=1234 gid=1234 groups=1234" ]
USER daemon
RUN [ "$(id)" = "uid=1(daemon) gid=1(daemon) groups=1(daemon)" ]
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

func testCopyChown(t *testing.T, sb integration.Sandbox) {
	// This test should work on Windows, but requires a proper image, and we will need
	// to check SIDs instead of UIDs.
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS base
ENV owner 1000
RUN mkdir -m 0777 /out
COPY --chown=daemon foo /
COPY --chown=1000:nobody bar /baz
ARG group
COPY --chown=${owner}:${group} foo /foobis
RUN stat -c "%U %G" /foo  > /out/fooowner
RUN stat -c "%u %G" /baz/sub  > /out/subowner
RUN stat -c "%u %G" /foobis  > /out/foobisowner
FROM scratch
COPY --from=base /out /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte(`foo-contents`), 0600),
		fstest.CreateDir("bar", 0700),
		fstest.CreateFile("bar/sub", nil, 0600),
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
		FrontendAttrs: map[string]string{
			"build-arg:group": "nobody",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "fooowner"))
	require.NoError(t, err)
	require.Equal(t, "daemon daemon\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "subowner"))
	require.NoError(t, err)
	require.Equal(t, "1000 nobody\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "foobisowner"))
	require.NoError(t, err)
	require.Equal(t, "1000 nobody\n", string(dt))
}

func testCopyChmod(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS base

RUN mkdir -m 0777 /out
COPY --chmod=0644 foo /
COPY --chmod=777 bar /baz
COPY --chmod=0 foo /foobis

RUN stat -c "%04a" /foo  > /out/fooperm
RUN stat -c "%04a" /baz  > /out/barperm
RUN stat -c "%04a" /foobis  > /out/foobisperm
FROM scratch
COPY --from=base /out /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte(`foo-contents`), 0600),
		fstest.CreateFile("bar", []byte(`bar-contents`), 0700),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "fooperm"))
	require.NoError(t, err)
	require.Equal(t, "0644\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "barperm"))
	require.NoError(t, err)
	require.Equal(t, "0777\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "foobisperm"))
	require.NoError(t, err)
	require.Equal(t, "0000\n", string(dt))
}

func testCopyOverrideFiles(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch AS base
COPY sub sub
COPY sub sub
COPY files/foo.go dest/foo.go
COPY files/foo.go dest/foo.go
COPY files dest
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateDir("sub", 0700),
		fstest.CreateDir("sub/dir1", 0700),
		fstest.CreateDir("sub/dir1/dir2", 0700),
		fstest.CreateFile("sub/dir1/dir2/foo", []byte(`foo-contents`), 0600),
		fstest.CreateDir("files", 0700),
		fstest.CreateFile("files/foo.go", []byte(`foo.go-contents`), 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "sub/dir1/dir2/foo"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "dest/foo.go"))
	require.NoError(t, err)
	require.Equal(t, "foo.go-contents", string(dt))
}

func testCopyVarSubstitution(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch AS base
ENV FOO bar
COPY $FOO baz
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("bar", []byte(`bar-contents`), 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "baz"))
	require.NoError(t, err)
	require.Equal(t, "bar-contents", string(dt))
}

func testCopyWildcards(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch AS base
COPY *.go /gofiles/
COPY f*.go foo2.go
COPY sub/* /subdest/
COPY sub/*/dir2/foo /subdest2/
COPY sub/*/dir2/foo /subdest3/bar
COPY . all/
COPY sub/dir1/ subdest4
COPY sub/dir1/. subdest5
COPY sub/dir1 subdest6
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo.go", []byte(`foo-contents`), 0600),
		fstest.CreateFile("bar.go", []byte(`bar-contents`), 0600),
		fstest.CreateDir("sub", 0700),
		fstest.CreateDir("sub/dir1", 0700),
		fstest.CreateDir("sub/dir1/dir2", 0700),
		fstest.CreateFile("sub/dir1/dir2/foo", []byte(`foo-contents`), 0600),
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

	dt, err := os.ReadFile(filepath.Join(destDir, "gofiles/foo.go"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "gofiles/bar.go"))
	require.NoError(t, err)
	require.Equal(t, "bar-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "foo2.go"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "subdest/dir2/foo"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "subdest2/foo"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "subdest3/bar"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "all/foo.go"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "subdest4/dir2/foo"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "subdest5/dir2/foo"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "subdest6/dir2/foo"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))
}

func testCopyRelative(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
WORKDIR /test1
WORKDIR test2
RUN sh -c "[ "$PWD" = '/test1/test2' ]"
COPY foo ./
RUN sh -c "[ $(cat /test1/test2/foo) = 'hello' ]"
ADD foo ./bar/baz
RUN sh -c "[ $(cat /test1/test2/bar/baz) = 'hello' ]"
COPY foo ./bar/baz2
RUN sh -c "[ $(cat /test1/test2/bar/baz2) = 'hello' ]"
WORKDIR ..
COPY foo ./
RUN sh -c "[ $(cat /test1/foo) = 'hello' ]"
COPY foo /test3/
RUN sh -c "[ $(cat /test3/foo) = 'hello' ]"
WORKDIR /test4
COPY . .
RUN sh -c "[ $(cat /test4/foo) = 'hello' ]"
WORKDIR /test5/test6
COPY foo ../
RUN sh -c "[ $(cat /test5/foo) = 'hello' ]"
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte(`hello`), 0600),
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

func testAddURLChmod(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	f.RequiresBuildctl(t)

	resp := httpserver.Response{
		Etag:    identity.NewID(),
		Content: []byte("content1"),
	}
	server := httpserver.NewTestServer(map[string]httpserver.Response{
		"/foo": resp,
	})
	defer server.Close()

	dockerfile := []byte(fmt.Sprintf(`
FROM busybox AS build
ADD --chmod=644 %[1]s /tmp/foo1
ADD --chmod=755 %[1]s /tmp/foo2
ADD --chmod=0413 %[1]s /tmp/foo3
RUN stat -c "%%04a" /tmp/foo1 >> /dest && \
	stat -c "%%04a" /tmp/foo2 >> /dest && \
	stat -c "%%04a" /tmp/foo3 >> /dest

FROM scratch
COPY --from=build /dest /dest
`, server.URL+"/foo"))

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

	dt, err := os.ReadFile(filepath.Join(destDir, "dest"))
	require.NoError(t, err)
	require.Equal(t, []byte("0644\n0755\n0413\n"), dt)
}

func testDockerfileFromGit(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	gitDir := t.TempDir()

	dockerfile := `
FROM busybox AS build
RUN echo -n fromgit > foo
FROM scratch
COPY --from=build foo bar
`

	err := os.WriteFile(filepath.Join(gitDir, "Dockerfile"), []byte(dockerfile), 0600)
	require.NoError(t, err)

	err = runShell(gitDir,
		"git init",
		"git config --local user.email test",
		"git config --local user.name test",
		"git add Dockerfile",
		"git commit -m initial",
		"git branch first",
	)
	require.NoError(t, err)

	dockerfile += `
COPY --from=build foo bar2
`

	err = os.WriteFile(filepath.Join(gitDir, "Dockerfile"), []byte(dockerfile), 0600)
	require.NoError(t, err)

	err = runShell(gitDir,
		"git add Dockerfile",
		"git commit -m second",
		"git update-server-info",
	)
	require.NoError(t, err)

	server := httptest.NewServer(http.FileServer(http.Dir(filepath.Join(gitDir))))
	defer server.Close()

	destDir := t.TempDir()

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context": server.URL + "/.git#first",
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "bar"))
	require.NoError(t, err)
	require.Equal(t, "fromgit", string(dt))

	_, err = os.Stat(filepath.Join(destDir, "bar2"))
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

	// second request from master branch contains both files
	destDir = t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context": server.URL + "/.git",
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err = os.ReadFile(filepath.Join(destDir, "bar"))
	require.NoError(t, err)
	require.Equal(t, "fromgit", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "bar2"))
	require.NoError(t, err)
	require.Equal(t, "fromgit", string(dt))
}

func testDockerfileFromHTTP(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	buf := bytes.NewBuffer(nil)
	w := tar.NewWriter(buf)

	writeFile := func(fn, dt string) {
		err := w.WriteHeader(&tar.Header{
			Name:     fn,
			Mode:     0600,
			Size:     int64(len(dt)),
			Typeflag: tar.TypeReg,
		})
		require.NoError(t, err)
		_, err = w.Write([]byte(dt))
		require.NoError(t, err)
	}

	writeFile("mydockerfile", `FROM scratch
COPY foo bar
`)

	writeFile("foo", "foo-contents")

	require.NoError(t, w.Flush())

	resp := httpserver.Response{
		Etag:    identity.NewID(),
		Content: buf.Bytes(),
	}

	server := httpserver.NewTestServer(map[string]httpserver.Response{
		"/myurl": resp,
	})
	defer server.Close()

	destDir := t.TempDir()

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context":  server.URL + "/myurl",
			"filename": "mydockerfile",
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "bar"))
	require.NoError(t, err)
	require.Equal(t, "foo-contents", string(dt))
}

func testMultiStageImplicitFrom(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY --from=busybox /etc/passwd test
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

	dt, err := os.ReadFile(filepath.Join(destDir, "test"))
	require.NoError(t, err)
	require.Contains(t, string(dt), "root")

	// testing masked image will load actual stage

	dockerfile = []byte(`
FROM busybox AS golang
RUN mkdir -p /usr/bin && echo -n foo > /usr/bin/go

FROM scratch
COPY --from=golang /usr/bin/go go
`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
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

	dt, err = os.ReadFile(filepath.Join(destDir, "go"))
	require.NoError(t, err)
	require.Contains(t, string(dt), "foo")
}

func testMultiStageCaseInsensitive(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch AS STAge0
COPY foo bar
FROM scratch AS staGE1
COPY --from=staGE0 bar baz
FROM scratch
COPY --from=stage1 baz bax
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("foo-contents"), 0600),
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
		FrontendAttrs: map[string]string{
			"target": "Stage1",
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "baz"))
	require.NoError(t, err)
	require.Contains(t, string(dt), "foo-contents")
}

func testLabels(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureImageExporter)
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
LABEL foo=bar
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := "example.com/moby/dockerfilelabels:test"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"label:bar": "baz",
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("rest of test requires containerd worker")
	}

	client, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	img, err := client.ImageService().Get(ctx, target)
	require.NoError(t, err)

	desc, err := img.Config(ctx, client.ContentStore(), platforms.Default())
	require.NoError(t, err)

	dt, err := content.ReadBlob(ctx, client.ContentStore(), desc)
	require.NoError(t, err)

	var ociimg ocispecs.Image
	err = json.Unmarshal(dt, &ociimg)
	require.NoError(t, err)

	v, ok := ociimg.Config.Labels["foo"]
	require.True(t, ok)
	require.Equal(t, "bar", v)

	v, ok = ociimg.Config.Labels["bar"]
	require.True(t, ok)
	require.Equal(t, "baz", v)
}

// #2008
func testWildcardRenameCache(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM alpine
COPY file* /files/
RUN ls /files/file1
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("file1", []byte("foo"), 0600),
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

	err = os.Rename(filepath.Join(dir.Name, "file1"), filepath.Join(dir.Name, "file2"))
	require.NoError(t, err)

	// cache should be invalidated and build should fail
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.Error(t, err)
}

func testOnBuildCleared(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureDirectPush)
	f := getFrontend(t, sb)

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	dockerfile := []byte(`
FROM busybox
ONBUILD RUN mkdir -p /out && echo -n 11 >> /out/foo
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := registry + "/buildkit/testonbuild:base"

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"push": "true",
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dockerfile = []byte(fmt.Sprintf(`
	FROM %s 
	`, target))

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	target2 := registry + "/buildkit/testonbuild:child"

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"push": "true",
					"name": target2,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dockerfile = []byte(fmt.Sprintf(`
	FROM %s AS base
	FROM scratch
	COPY --from=base /out /
	`, target2))

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)
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

	dt, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "11", string(dt))
}

func testCacheMultiPlatformImportExport(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb,
		workers.FeatureDirectPush,
		workers.FeatureCacheExport,
		workers.FeatureCacheBackendInline,
		workers.FeatureCacheBackendRegistry,
	)
	f := getFrontend(t, sb)

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	dockerfile := []byte(`
FROM --platform=$BUILDPLATFORM busybox AS base
ARG TARGETARCH
RUN echo -n $TARGETARCH> arch && cat /dev/urandom | head -c 100 | sha256sum > unique
FROM scratch
COPY --from=base unique /
COPY --from=base arch /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := registry + "/buildkit/testexportdf:multi"

	// exportCache := []client.CacheOptionsEntry{
	// 	{
	// 		Type:  "registry",
	// 		Attrs: map[string]string{"ref": target},
	// 	},
	// }
	// importCache := target

	exportCache := []client.CacheOptionsEntry{
		{
			Type: "inline",
		},
	}
	importCache := target + "-img"

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"push": "true",
					"name": target + "-img",
				},
			},
		},
		CacheExports: exportCache,
		FrontendAttrs: map[string]string{
			"platform": "linux/amd64,linux/arm/v7",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	desc, provider, err := contentutil.ProviderFromRef(target + "-img")
	require.NoError(t, err)

	imgs, err := testutil.ReadImages(sb.Context(), provider, desc)
	require.NoError(t, err)

	require.Equal(t, 2, len(imgs.Images))

	require.Equal(t, "amd64", string(imgs.Find("linux/amd64").Layers[1]["arch"].Data))
	dtamd := imgs.Find("linux/amd64").Layers[0]["unique"].Data
	dtarm := imgs.Find("linux/arm/v7").Layers[0]["unique"].Data
	require.NotEqual(t, dtamd, dtarm)

	for i := 0; i < 2; i++ {
		ensurePruneAll(t, c, sb)

		_, err = f.Solve(sb.Context(), c, client.SolveOpt{
			FrontendAttrs: map[string]string{
				"cache-from": importCache,
				"platform":   "linux/amd64,linux/arm/v7",
			},
			Exports: []client.ExportEntry{
				{
					Type: client.ExporterImage,
					Attrs: map[string]string{
						"push": "true",
						"name": target + "-img",
					},
				},
			},
			CacheExports: exportCache,
			LocalMounts: map[string]fsutil.FS{
				dockerui.DefaultLocalNameDockerfile: dir,
				dockerui.DefaultLocalNameContext:    dir,
			},
		}, nil)
		require.NoError(t, err)

		desc2, provider, err := contentutil.ProviderFromRef(target + "-img")
		require.NoError(t, err)

		require.Equal(t, desc.Digest, desc2.Digest)

		imgs, err = testutil.ReadImages(sb.Context(), provider, desc2)
		require.NoError(t, err)

		require.Equal(t, 2, len(imgs.Images))

		require.Equal(t, "arm", string(imgs.Find("linux/arm/v7").Layers[1]["arch"].Data))
		dtamd2 := imgs.Find("linux/amd64").Layers[0]["unique"].Data
		dtarm2 := imgs.Find("linux/arm/v7").Layers[0]["unique"].Data
		require.Equal(t, string(dtamd), string(dtamd2))
		require.Equal(t, string(dtarm), string(dtarm2))
	}
}

func testImageManifestCacheImportExport(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureCacheExport, workers.FeatureCacheBackendLocal)
	f := getFrontend(t, sb)

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	dockerfile := []byte(`
FROM busybox AS base
COPY foo const
#RUN echo -n foobar > const
RUN cat /dev/urandom | head -c 100 | sha256sum > unique
FROM scratch
COPY --from=base const /
COPY --from=base unique /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("foobar"), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	target := registry + "/buildkit/testexportdf:latest"

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		CacheExports: []client.CacheOptionsEntry{
			{
				Type: "registry",
				Attrs: map[string]string{
					"ref":            target,
					"oci-mediatypes": "true",
					"image-manifest": "true",
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	desc, provider, err := contentutil.ProviderFromRef(target)
	require.NoError(t, err)
	img, err := testutil.ReadImage(sb.Context(), provider, desc)
	require.NoError(t, err)

	require.Equal(t, ocispecs.MediaTypeImageManifest, img.Manifest.MediaType)
	require.Equal(t, v1.CacheConfigMediaTypeV0, img.Manifest.Config.MediaType)

	dt, err := os.ReadFile(filepath.Join(destDir, "const"))
	require.NoError(t, err)
	require.Equal(t, "foobar", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)

	ensurePruneAll(t, c, sb)

	destDir = t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"cache-from": target,
		},
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

	dt2, err := os.ReadFile(filepath.Join(destDir, "const"))
	require.NoError(t, err)
	require.Equal(t, "foobar", string(dt2))

	dt2, err = os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)
	require.Equal(t, string(dt), string(dt2))
}
func testCacheImportExport(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureCacheExport, workers.FeatureCacheBackendLocal)
	f := getFrontend(t, sb)

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	dockerfile := []byte(`
FROM busybox AS base
COPY foo const
#RUN echo -n foobar > const
RUN cat /dev/urandom | head -c 100 | sha256sum > unique
FROM scratch
COPY --from=base const /
COPY --from=base unique /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("foobar"), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	target := registry + "/buildkit/testexportdf:latest"

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		CacheExports: []client.CacheOptionsEntry{
			{
				Type:  "registry",
				Attrs: map[string]string{"ref": target},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "const"))
	require.NoError(t, err)
	require.Equal(t, "foobar", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)

	ensurePruneAll(t, c, sb)

	destDir = t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"cache-from": target,
		},
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

	dt2, err := os.ReadFile(filepath.Join(destDir, "const"))
	require.NoError(t, err)
	require.Equal(t, "foobar", string(dt2))

	dt2, err = os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)
	require.Equal(t, string(dt), string(dt2))
}

func testReproducibleIDs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureImageExporter)
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
ENV foo=bar
COPY foo /
RUN echo bar > bar
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("foo-contents"), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := "example.com/moby/dockerfileids:test"
	opt := client.SolveOpt{
		FrontendAttrs: map[string]string{},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	target2 := "example.com/moby/dockerfileids2:test"
	opt.Exports[0].Attrs["name"] = target2

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("rest of test requires containerd worker")
	}

	client, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	img, err := client.ImageService().Get(ctx, target)
	require.NoError(t, err)
	img2, err := client.ImageService().Get(ctx, target2)
	require.NoError(t, err)

	require.Equal(t, img.Target, img2.Target)
}

func testImportExportReproducibleIDs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("test requires containerd worker")
	}

	f := getFrontend(t, sb)

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	dockerfile := []byte(`
FROM busybox
ENV foo=bar
COPY foo /
RUN echo bar > bar
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("foobar"), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := "example.com/moby/dockerfileexpids:test"
	cacheTarget := registry + "/test/dockerfileexpids:cache"
	opt := client.SolveOpt{
		FrontendAttrs: map[string]string{},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
				},
			},
		},
		CacheExports: []client.CacheOptionsEntry{
			{
				Type:  "registry",
				Attrs: map[string]string{"ref": cacheTarget},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}

	ctd, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer ctd.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	img, err := ctd.ImageService().Get(ctx, target)
	require.NoError(t, err)

	err = ctd.ImageService().Delete(ctx, target)
	require.NoError(t, err)

	ensurePruneAll(t, c, sb)

	target2 := "example.com/moby/dockerfileexpids2:test"

	opt.Exports[0].Attrs["name"] = target2
	opt.FrontendAttrs["cache-from"] = cacheTarget

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	img2, err := ctd.ImageService().Get(ctx, target2)
	require.NoError(t, err)

	require.Equal(t, img.Target, img2.Target)
}

func testNoCache(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS s0
RUN cat /dev/urandom | head -c 100 | sha256sum | tee unique
FROM busybox AS s1
RUN cat /dev/urandom | head -c 100 | sha256sum | tee unique2
FROM scratch
COPY --from=s0 unique /
COPY --from=s1 unique2 /
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	opt := client.SolveOpt{
		FrontendAttrs: map[string]string{},
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
	}

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	destDir2 := t.TempDir()

	opt.FrontendAttrs["no-cache"] = ""
	opt.Exports[0].OutputDir = destDir2

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	unique1Dir1, err := os.ReadFile(filepath.Join(destDir, "unique"))
	require.NoError(t, err)

	unique1Dir2, err := os.ReadFile(filepath.Join(destDir2, "unique"))
	require.NoError(t, err)

	unique2Dir1, err := os.ReadFile(filepath.Join(destDir, "unique2"))
	require.NoError(t, err)

	unique2Dir2, err := os.ReadFile(filepath.Join(destDir2, "unique2"))
	require.NoError(t, err)

	require.NotEqual(t, string(unique1Dir1), string(unique1Dir2))
	require.NotEqual(t, string(unique2Dir1), string(unique2Dir2))

	destDir3 := t.TempDir()

	opt.FrontendAttrs["no-cache"] = "s1"
	opt.Exports[0].OutputDir = destDir3

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	unique1Dir3, err := os.ReadFile(filepath.Join(destDir3, "unique"))
	require.NoError(t, err)

	unique2Dir3, err := os.ReadFile(filepath.Join(destDir3, "unique2"))
	require.NoError(t, err)

	require.Equal(t, string(unique1Dir2), string(unique1Dir3))
	require.NotEqual(t, string(unique2Dir1), string(unique2Dir3))
}

func testPlatformArgsImplicit(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(fmt.Sprintf(`
FROM scratch AS build-%s
COPY foo bar
FROM build-${TARGETOS}
COPY foo2 bar2
`, runtime.GOOS))

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("d0"), 0600),
		fstest.CreateFile("foo2", []byte("d1"), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	opt := client.SolveOpt{
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
	}

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "bar"))
	require.NoError(t, err)
	require.Equal(t, "d0", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "bar2"))
	require.NoError(t, err)
	require.Equal(t, "d1", string(dt))
}

func testPlatformArgsExplicit(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM --platform=$BUILDPLATFORM busybox AS build
ARG TARGETPLATFORM
ARG TARGETOS
RUN mkdir /out && echo -n $TARGETPLATFORM > /out/platform && echo -n $TARGETOS > /out/os
FROM scratch
COPY --from=build out .
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	opt := client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		FrontendAttrs: map[string]string{
			"platform":           "darwin/ppc64le",
			"build-arg:TARGETOS": "freebsd",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "platform"))
	require.NoError(t, err)
	require.Equal(t, "darwin/ppc64le", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "os"))
	require.NoError(t, err)
	require.Equal(t, "freebsd", string(dt))
}

func testBuiltinArgs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build
ARG FOO
ARG BAR
ARG BAZ=bazcontent
RUN echo -n $HTTP_PROXY::$NO_PROXY::$FOO::$BAR::$BAZ > /out
FROM scratch
COPY --from=build /out /

`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	opt := client.SolveOpt{
		FrontendAttrs: map[string]string{
			"build-arg:FOO":        "foocontents",
			"build-arg:http_proxy": "hpvalue",
			"build-arg:NO_PROXY":   "npvalue",
		},
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
	}

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.Equal(t, "hpvalue::npvalue::foocontents::::bazcontent", string(dt))

	// repeat with changed default args should match the old cache
	destDir = t.TempDir()

	opt = client.SolveOpt{
		FrontendAttrs: map[string]string{
			"build-arg:FOO":        "foocontents",
			"build-arg:http_proxy": "hpvalue2",
		},
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
	}

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	dt, err = os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.Equal(t, "hpvalue::npvalue::foocontents::::bazcontent", string(dt))

	// changing actual value invalidates cache
	destDir = t.TempDir()

	opt = client.SolveOpt{
		FrontendAttrs: map[string]string{
			"build-arg:FOO":        "foocontents2",
			"build-arg:http_proxy": "hpvalue2",
		},
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
	}

	_, err = f.Solve(sb.Context(), c, opt, nil)
	require.NoError(t, err)

	dt, err = os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.Equal(t, "hpvalue2::::foocontents2::::bazcontent", string(dt))
}

func testTarContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
COPY foo /
`)

	foo := []byte("contents")

	buf := bytes.NewBuffer(nil)
	tw := tar.NewWriter(buf)
	err := tw.WriteHeader(&tar.Header{
		Name:     "Dockerfile",
		Typeflag: tar.TypeReg,
		Size:     int64(len(dockerfile)),
		Mode:     0644,
	})
	require.NoError(t, err)
	_, err = tw.Write(dockerfile)
	require.NoError(t, err)
	err = tw.WriteHeader(&tar.Header{
		Name:     "foo",
		Typeflag: tar.TypeReg,
		Size:     int64(len(foo)),
		Mode:     0644,
	})
	require.NoError(t, err)
	_, err = tw.Write(foo)
	require.NoError(t, err)
	err = tw.Close()
	require.NoError(t, err)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	up := uploadprovider.New()
	url := up.Add(buf)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context": url,
		},
		Session: []session.Attachable{up},
	}, nil)
	require.NoError(t, err)
}

func testTarContextExternalDockerfile(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	foo := []byte("contents")

	buf := bytes.NewBuffer(nil)
	tw := tar.NewWriter(buf)
	err := tw.WriteHeader(&tar.Header{
		Name:     "sub/dir/foo",
		Typeflag: tar.TypeReg,
		Size:     int64(len(foo)),
		Mode:     0644,
	})
	require.NoError(t, err)
	_, err = tw.Write(foo)
	require.NoError(t, err)
	err = tw.Close()
	require.NoError(t, err)

	dockerfile := []byte(`
FROM scratch
COPY foo bar
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	up := uploadprovider.New()
	url := up.Add(buf)

	// repeat with changed default args should match the old cache
	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context":       url,
			"dockerfilekey": dockerui.DefaultLocalNameDockerfile,
			"contextsubdir": "sub/dir",
		},
		Session: []session.Attachable{up},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "bar"))
	require.NoError(t, err)
	require.Equal(t, string(dt), "contents")
}

func testFrontendUseForwardedSolveResults(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM scratch
COPY foo foo2
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("foo", []byte("data"), 0600),
	)

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		res, err := c.Solve(ctx, gateway.SolveRequest{
			Frontend: "dockerfile.v0",
		})
		if err != nil {
			return nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		st2, err := ref.ToState()
		if err != nil {
			return nil, err
		}

		st := llb.Scratch().File(
			llb.Copy(st2, "foo2", "foo3"),
		)

		def, err := st.Marshal(sb.Context())
		if err != nil {
			return nil, err
		}

		return c.Solve(ctx, gateway.SolveRequest{
			Definition: def.ToPB(),
		})
	}

	destDir := t.TempDir()

	_, err = c.Build(sb.Context(), client.SolveOpt{
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
	}, "", frontend, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "foo3"))
	require.NoError(t, err)
	require.Equal(t, dt, []byte("data"))
}

func testFrontendEvaluate(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM scratch
COPY badfile /
`)
	dir := integration.Tmpdir(t, fstest.CreateFile("Dockerfile", dockerfile, 0600))

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		_, err := c.Solve(ctx, gateway.SolveRequest{
			Frontend: "dockerfile.v0",
			Evaluate: true,
		})
		require.ErrorContains(t, err, `"/badfile": not found`)

		_, err = c.Solve(ctx, gateway.SolveRequest{
			Frontend: "dockerfile.v0",
			FrontendOpt: map[string]string{
				"platform": "linux/amd64,linux/arm64",
			},
			Evaluate: true,
		})
		require.ErrorContains(t, err, `"/badfile": not found`)

		return nil, nil
	}

	_, err = c.Build(sb.Context(), client.SolveOpt{
		Exports: []client.ExportEntry{},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, "", frontend, nil)
	require.NoError(t, err)
}

func testFrontendInputs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	outMount := llb.Image("busybox").Run(
		llb.Shlex(`sh -c "cat /dev/urandom | head -c 100 | sha256sum > /out/foo"`),
	).AddMount("/out", llb.Scratch())

	def, err := outMount.Marshal(sb.Context())
	require.NoError(t, err)

	_, err = c.Solve(sb.Context(), def, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	expected, err := os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)

	dockerfile := []byte(`
FROM scratch
COPY foo foo2
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
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
		},
		FrontendInputs: map[string]llb.State{
			dockerui.DefaultLocalNameContext: outMount,
		},
	}, nil)
	require.NoError(t, err)

	actual, err := os.ReadFile(filepath.Join(destDir, "foo2"))
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func testFrontendSubrequests(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	if _, ok := f.(*clientFrontend); !ok {
		t.Skip("only test with client frontend")
	}

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM scratch
COPY Dockerfile Dockerfile
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	called := false

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		reqs, err := subrequests.Describe(ctx, c)
		require.NoError(t, err)

		require.True(t, len(reqs) > 0)

		hasDescribe := false

		for _, req := range reqs {
			if req.Name == "frontend.subrequests.describe" {
				hasDescribe = true
				require.Equal(t, subrequests.RequestType("rpc"), req.Type)
				require.NotEqual(t, req.Version, "")
				require.True(t, len(req.Metadata) > 0)
				require.Equal(t, "result.json", req.Metadata[0].Name)
			}
		}
		require.True(t, hasDescribe)

		_, err = c.Solve(ctx, gateway.SolveRequest{
			FrontendOpt: map[string]string{
				"requestid":     "frontend.subrequests.notexist",
				"frontend.caps": "moby.buildkit.frontend.subrequests",
			},
			Frontend: "dockerfile.v0",
		})
		require.Error(t, err)
		var reqErr *errdefs.UnsupportedSubrequestError
		require.True(t, errors.As(err, &reqErr))
		require.Equal(t, "frontend.subrequests.notexist", reqErr.GetName())

		_, err = c.Solve(ctx, gateway.SolveRequest{
			FrontendOpt: map[string]string{
				"frontend.caps": "moby.buildkit.frontend.notexistcap",
			},
			Frontend: "dockerfile.v0",
		})
		require.Error(t, err)
		var capErr *errdefs.UnsupportedFrontendCapError
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, "moby.buildkit.frontend.notexistcap", capErr.GetName())

		called = true
		return nil, nil
	}

	_, err = c.Build(sb.Context(), client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
		},
	}, "", frontend, nil)
	require.NoError(t, err)

	require.True(t, called)
}

// moby/buildkit#1301
func testDockefileCheckHostname(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	dockerfile := []byte(`
FROM busybox
RUN cat /etc/hosts | grep foo
RUN echo $HOSTNAME | grep foo
RUN echo $(hostname) | grep foo
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	cases := []struct {
		name  string
		attrs map[string]string
	}{
		{
			name: "meta",
			attrs: map[string]string{
				"hostname": "foo",
			},
		},
		{
			name: "arg",
			attrs: map[string]string{
				"build-arg:BUILDKIT_SANDBOX_HOSTNAME": "foo",
			},
		},
		{
			name: "meta and arg",
			attrs: map[string]string{
				"hostname":                            "bar",
				"build-arg:BUILDKIT_SANDBOX_HOSTNAME": "foo",
			},
		},
	}
	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err = f.Solve(sb.Context(), c, client.SolveOpt{
				FrontendAttrs: tt.attrs,
				LocalMounts: map[string]fsutil.FS{
					dockerui.DefaultLocalNameDockerfile: dir,
					dockerui.DefaultLocalNameContext:    dir,
				},
			}, nil)
			require.NoError(t, err)
		})
	}
}

func testShmSize(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	dockerfile := []byte(`
FROM busybox AS base
RUN mount | grep /dev/shm > /shmsize
FROM scratch
COPY --from=base /shmsize /
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
		FrontendAttrs: map[string]string{
			"shm-size": "134217728",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "shmsize"))
	require.NoError(t, err)
	require.Contains(t, string(dt), `size=131072k`)
}

func testUlimit(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	dockerfile := []byte(`
FROM busybox AS base
RUN ulimit -n > /ulimit
FROM scratch
COPY --from=base /ulimit /
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
		FrontendAttrs: map[string]string{
			"ulimit": "nofile=1062:1062",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "ulimit"))
	require.NoError(t, err)
	require.Equal(t, `1062`, strings.TrimSpace(string(dt)))
}

func testCgroupParent(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	if sb.Rootless() {
		t.SkipNow()
	}

	if _, err := os.Lstat("/sys/fs/cgroup/cgroup.subtree_control"); os.IsNotExist(err) {
		t.Skipf("test requires cgroup v2")
	}

	cgroupName := "test." + identity.NewID()

	err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", cgroupName), 0755)
	require.NoError(t, err)

	defer func() {
		err := os.RemoveAll(filepath.Join("/sys/fs/cgroup", cgroupName))
		require.NoError(t, err)
	}()

	err = os.WriteFile(filepath.Join("/sys/fs/cgroup", cgroupName, "pids.max"), []byte("10"), 0644)
	require.NoError(t, err)

	f := getFrontend(t, sb)
	dockerfile := []byte(`
FROM alpine AS base
RUN mkdir /out; (for i in $(seq 1 10); do sleep 1 & done 2>/out/error); cat /proc/self/cgroup > /out/cgroup
FROM scratch
COPY --from=base /out /
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
		FrontendAttrs: map[string]string{
			"cgroup-parent": cgroupName,
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "cgroup"))
	require.NoError(t, err)
	// cgroupns does not leak the parent cgroup name
	require.NotContains(t, strings.TrimSpace(string(dt)), `foocgroup`)

	dt, err = os.ReadFile(filepath.Join(destDir, "error"))
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(string(dt)), `Resource temporarily unavailable`)
}

func testNamedImageContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM busybox AS base
RUN cat /etc/alpine-release > /out
FROM scratch
COPY --from=base /out /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	f := getFrontend(t, sb)

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			// Make sure image resolution works as expected, do not add a tag or locator.
			"context:busybox": "docker-image://alpine",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.True(t, len(dt) > 0)

	workers.CheckFeatureCompat(t, sb, workers.FeatureDirectPush)

	// Now test with an image with custom envs
	dockerfile = []byte(`
FROM alpine:latest
ENV PATH=/foobar:$PATH
ENV FOOBAR=foobar
`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)
	target := registry + "/buildkit/testnamedimagecontext:latest"

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	dockerfile = []byte(`
FROM busybox AS base
RUN cat /etc/alpine-release > /out
RUN env | grep PATH > /env_path
RUN env | grep FOOBAR > /env_foobar
FROM scratch
COPY --from=base /out /
COPY --from=base /env_path /
COPY --from=base /env_foobar /
	`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	f = getFrontend(t, sb)

	destDir = t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context:busybox": "docker-image://" + target,
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err = os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.True(t, len(dt) > 0)

	dt, err = os.ReadFile(filepath.Join(destDir, "env_foobar"))
	require.NoError(t, err)
	require.Equal(t, "FOOBAR=foobar", strings.TrimSpace(string(dt)))

	dt, err = os.ReadFile(filepath.Join(destDir, "env_path"))
	require.NoError(t, err)
	require.Contains(t, string(dt), "/foobar:")
}

func testNamedImageContextPlatform(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureDirectPush)
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	// Build a base image and force buildkit to generate a manifest list.
	dockerfile := []byte(`FROM --platform=$BUILDPLATFORM alpine:latest`)
	target := registry + "/buildkit/testnamedimagecontextplatform:latest"

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	f := getFrontend(t, sb)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"build-arg:BUILDKIT_MULTI_PLATFORM": "true",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	dockerfile = []byte(`
FROM --platform=$BUILDPLATFORM busybox AS target
RUN echo hello
`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	f = getFrontend(t, sb)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context:busybox": "docker-image://" + target,
			// random platform that would never exist so it doesn't conflict with the build machine
			// here we specifically want to make sure that the platform chosen for the image source is the one in the dockerfile not the target platform.
			"platform": "darwin/ppc64le",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)
}

func testNamedImageContextTimestamps(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureDirectPush)
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM alpine
RUN echo foo >> /test
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	target := registry + "/buildkit/testnamedimagecontexttimestamps:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	desc, provider, err := contentutil.ProviderFromRef(target)
	require.NoError(t, err)
	img, err := testutil.ReadImage(sb.Context(), provider, desc)
	require.NoError(t, err)

	dirDerived := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	targetDerived := registry + "/buildkit/testnamedimagecontexttimestampsderived:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context:alpine": "docker-image://" + target,
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dirDerived,
			dockerui.DefaultLocalNameContext:    dirDerived,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": targetDerived,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	desc, provider, err = contentutil.ProviderFromRef(targetDerived)
	require.NoError(t, err)
	imgDerived, err := testutil.ReadImage(sb.Context(), provider, desc)
	require.NoError(t, err)

	require.NotEqual(t, img.Img.Created, imgDerived.Img.Created)
	diff := imgDerived.Img.Created.Sub(*img.Img.Created)
	require.Greater(t, diff, time.Duration(0))
	require.Less(t, diff, 10*time.Minute)
}

func testNamedImageContextScratch(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM busybox
COPY <<EOF /out
hello world!
EOF
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	f := getFrontend(t, sb)

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context:busybox": "docker-image://scratch",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	items, err := os.ReadDir(destDir)
	require.NoError(t, err)
	require.Equal(t, 1, len(items))
	require.Equal(t, "out", items[0].Name())

	dt, err := os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.Equal(t, "hello world!\n", string(dt))
}

func testNamedLocalContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM busybox AS base
RUN cat /etc/alpine-release > /out
FROM scratch
COPY --from=base /o* /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	outf := []byte(`dummy-result`)

	dir2 := integration.Tmpdir(
		t,
		fstest.CreateFile("out", outf, 0600),
		fstest.CreateFile("out2", outf, 0600),
		fstest.CreateFile(".dockerignore", []byte("out2\n"), 0600),
	)

	f := getFrontend(t, sb)

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context:base": "local:basedir",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
			"basedir":                           dir2,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.True(t, len(dt) > 0)

	_, err = os.ReadFile(filepath.Join(destDir, "out2"))
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func testNamedOCILayoutContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter, workers.FeatureOCILayout)
	// how this test works:
	// 1- we use a regular builder with a dockerfile to create an image two files: "out" with content "first", "out2" with content "second"
	// 2- we save the output to an OCI layout dir
	// 3- we use another regular builder with a dockerfile to build using a referenced context "base", but override it to reference the output of the previous build
	// 4- we check that the output of the second build matches our OCI layout, and not the referenced image
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	// create a tempdir where we will store the OCI layout
	ocidir := t.TempDir()

	ociDockerfile := []byte(`
	FROM busybox:latest
	WORKDIR /test
	RUN sh -c "echo -n first > out"
	RUN sh -c "echo -n second > out2"
	ENV foo=bar
	`)
	inDir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", ociDockerfile, 0600),
	)

	f := getFrontend(t, sb)

	outW := bytes.NewBuffer(nil)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: inDir,
			dockerui.DefaultLocalNameContext:    inDir,
		},
		Exports: []client.ExportEntry{
			{
				Type:   client.ExporterOCI,
				Output: fixedWriteCloser(nopWriteCloser{outW}),
			},
		},
	}, nil)
	require.NoError(t, err)

	// extract the tar stream to the directory as OCI layout
	m, err := testutil.ReadTarToMap(outW.Bytes(), false)
	require.NoError(t, err)

	for filename, content := range m {
		fullFilename := path.Join(ocidir, filename)
		err = os.MkdirAll(path.Dir(fullFilename), 0755)
		require.NoError(t, err)
		if content.Header.FileInfo().IsDir() {
			err = os.MkdirAll(fullFilename, 0755)
			require.NoError(t, err)
		} else {
			err = os.WriteFile(fullFilename, content.Data, 0644)
			require.NoError(t, err)
		}
	}

	var index ocispecs.Index
	err = json.Unmarshal(m[ocispecs.ImageIndexFile].Data, &index)
	require.NoError(t, err)
	require.Equal(t, 1, len(index.Manifests))
	digest := index.Manifests[0].Digest.Hex()

	store, err := local.NewStore(ocidir)
	ociID := "ocione"
	require.NoError(t, err)

	// we will use this simple dockerfile to test
	// 1. busybox is used as is, but because we override the context for base,
	//    when we run `COPY --from=base`, it should take the /o* from the image in the store,
	//    rather than what we built on the first 2 lines here.
	// 2. we override the context for `foo` to be our local OCI store, which has an `ENV foo=bar` override.
	//    As such, the `RUN echo $foo` step should have `$foo` set to `"bar"`, and so
	//    when we `COPY --from=imported`, it should have the content of `/outfoo` as `"bar"`
	dockerfile := []byte(`
FROM busybox AS base
RUN cat /etc/alpine-release > out

FROM foo AS imported
RUN echo -n $foo > outfoo

FROM scratch
COPY --from=base /test/o* /
COPY --from=imported /test/outfoo /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context:base": fmt.Sprintf("oci-layout:%s@sha256:%s", ociID, digest),
			"context:foo":  fmt.Sprintf("oci-layout:%s@sha256:%s", ociID, digest),
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		OCIStores: map[string]content.Store{
			ociID: store,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.True(t, len(dt) > 0)
	require.Equal(t, []byte("first"), dt)

	dt, err = os.ReadFile(filepath.Join(destDir, "out2"))
	require.NoError(t, err)
	require.True(t, len(dt) > 0)
	require.Equal(t, []byte("second"), dt)

	dt, err = os.ReadFile(filepath.Join(destDir, "outfoo"))
	require.NoError(t, err)
	require.True(t, len(dt) > 0)
	require.Equal(t, []byte("bar"), dt)
}

func testNamedOCILayoutContextExport(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter, workers.FeatureOCILayout)
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	ocidir := t.TempDir()

	dockerfile := []byte(`
FROM scratch
WORKDIR /test
ENV foo=bar
	`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	f := getFrontend(t, sb)

	outW := bytes.NewBuffer(nil)
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{{
			Type:   client.ExporterOCI,
			Output: fixedWriteCloser(nopWriteCloser{outW}),
		}},
	}, nil)
	require.NoError(t, err)

	m, err := testutil.ReadTarToMap(outW.Bytes(), false)
	require.NoError(t, err)

	for filename, content := range m {
		fullFilename := path.Join(ocidir, filename)
		err = os.MkdirAll(path.Dir(fullFilename), 0755)
		require.NoError(t, err)
		if content.Header.FileInfo().IsDir() {
			err = os.MkdirAll(fullFilename, 0755)
			require.NoError(t, err)
		} else {
			err = os.WriteFile(fullFilename, content.Data, 0644)
			require.NoError(t, err)
		}
	}

	var index ocispecs.Index
	err = json.Unmarshal(m[ocispecs.ImageIndexFile].Data, &index)
	require.NoError(t, err)
	require.Equal(t, 1, len(index.Manifests))
	digest := index.Manifests[0].Digest.Hex()

	store, err := local.NewStore(ocidir)
	ociID := "ocione"
	require.NoError(t, err)

	dockerfile = []byte(`
FROM nonexistent AS base
`)

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	outW = bytes.NewBuffer(nil)
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"context:nonexistent": fmt.Sprintf("oci-layout:%s@sha256:%s", ociID, digest),
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		OCIStores: map[string]content.Store{
			ociID: store,
		},
		Exports: []client.ExportEntry{
			{
				Type:   client.ExporterOCI,
				Output: fixedWriteCloser(nopWriteCloser{outW}),
			},
		},
	}, nil)
	require.NoError(t, err)

	m, err = testutil.ReadTarToMap(outW.Bytes(), false)
	require.NoError(t, err)

	err = json.Unmarshal(m[ocispecs.ImageIndexFile].Data, &index)
	require.NoError(t, err)
	require.Equal(t, 1, len(index.Manifests))
	digest = index.Manifests[0].Digest.Hex()

	var mfst ocispecs.Manifest
	require.NoError(t, json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+digest].Data, &mfst))
	digest = mfst.Config.Digest.Hex()

	var cfg ocispecs.Image
	require.NoError(t, json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+digest].Data, &cfg))

	require.Equal(t, "/test", cfg.Config.WorkingDir)
	require.Contains(t, cfg.Config.Env, "foo=bar")
}

func testNamedInputContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM alpine
ENV FOO=bar
RUN echo first > /out
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	dockerfile2 := []byte(`
FROM base AS build
RUN echo "foo is $FOO" > /foo
FROM scratch
COPY --from=build /foo /out /
`)

	dir2 := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile2, 0600),
	)

	f := getFrontend(t, sb)

	b := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		res, err := f.SolveGateway(ctx, c, gateway.SolveRequest{})
		if err != nil {
			return nil, err
		}
		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}
		st, err := ref.ToState()
		if err != nil {
			return nil, err
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, err
		}

		dt, ok := res.Metadata["containerimage.config"]
		if !ok {
			return nil, errors.Errorf("no containerimage.config in metadata")
		}

		dt, err = json.Marshal(map[string][]byte{
			"containerimage.config": dt,
		})
		if err != nil {
			return nil, err
		}

		res, err = f.SolveGateway(ctx, c, gateway.SolveRequest{
			FrontendOpt: map[string]string{
				"dockerfilekey":       dockerui.DefaultLocalNameDockerfile + "2",
				"context:base":        "input:base",
				"input-metadata:base": string(dt),
			},
			FrontendInputs: map[string]*pb.Definition{
				"base": def.ToPB(),
			},
		})
		if err != nil {
			return nil, err
		}
		return res, nil
	}

	product := "buildkit_test"

	destDir := t.TempDir()

	_, err = c.Build(ctx, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile:       dir,
			dockerui.DefaultLocalNameContext:          dir,
			dockerui.DefaultLocalNameDockerfile + "2": dir2,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, product, b, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out"))
	require.NoError(t, err)
	require.Equal(t, "first\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "foo"))
	require.NoError(t, err)
	require.Equal(t, "foo is bar\n", string(dt))
}

func testNamedMultiplatformInputContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureMultiPlatform)
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM --platform=$BUILDPLATFORM alpine
ARG TARGETARCH
ENV FOO=bar-$TARGETARCH
RUN echo "foo $TARGETARCH" > /out
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	dockerfile2 := []byte(`
FROM base AS build
RUN echo "foo is $FOO" > /foo
FROM scratch
COPY --from=build /foo /out /
`)

	dir2 := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile2, 0600),
	)

	f := getFrontend(t, sb)

	b := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		res, err := f.SolveGateway(ctx, c, gateway.SolveRequest{
			FrontendOpt: map[string]string{
				"platform": "linux/amd64,linux/arm64",
			},
		})
		if err != nil {
			return nil, err
		}

		if len(res.Refs) != 2 {
			return nil, errors.Errorf("expected 2 refs, got %d", len(res.Refs))
		}

		inputs := map[string]*pb.Definition{}
		st, err := res.Refs["linux/amd64"].ToState()
		if err != nil {
			return nil, err
		}
		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, err
		}
		inputs["base::linux/amd64"] = def.ToPB()

		st, err = res.Refs["linux/arm64"].ToState()
		if err != nil {
			return nil, err
		}
		def, err = st.Marshal(ctx)
		if err != nil {
			return nil, err
		}
		inputs["base::linux/arm64"] = def.ToPB()

		frontendOpt := map[string]string{
			"dockerfilekey":             dockerui.DefaultLocalNameDockerfile + "2",
			"context:base::linux/amd64": "input:base::linux/amd64",
			"context:base::linux/arm64": "input:base::linux/arm64",
			"platform":                  "linux/amd64,linux/arm64",
		}

		dt, ok := res.Metadata["containerimage.config/linux/amd64"]
		if !ok {
			return nil, errors.Errorf("no containerimage.config in metadata")
		}
		dt, err = json.Marshal(map[string][]byte{
			"containerimage.config": dt,
		})
		if err != nil {
			return nil, err
		}
		frontendOpt["input-metadata:base::linux/amd64"] = string(dt)

		dt, ok = res.Metadata["containerimage.config/linux/arm64"]
		if !ok {
			return nil, errors.Errorf("no containerimage.config in metadata")
		}
		dt, err = json.Marshal(map[string][]byte{
			"containerimage.config": dt,
		})
		if err != nil {
			return nil, err
		}
		frontendOpt["input-metadata:base::linux/arm64"] = string(dt)

		res, err = f.SolveGateway(ctx, c, gateway.SolveRequest{
			FrontendOpt:    frontendOpt,
			FrontendInputs: inputs,
		})
		if err != nil {
			return nil, err
		}
		return res, nil
	}

	product := "buildkit_test"

	destDir := t.TempDir()

	_, err = c.Build(ctx, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile:       dir,
			dockerui.DefaultLocalNameContext:          dir,
			dockerui.DefaultLocalNameDockerfile + "2": dir2,
		},
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}, product, b, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "linux_amd64/out"))
	require.NoError(t, err)
	require.Equal(t, "foo amd64\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "linux_amd64/foo"))
	require.NoError(t, err)
	require.Equal(t, "foo is bar-amd64\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "linux_arm64/out"))
	require.NoError(t, err)
	require.Equal(t, "foo arm64\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "linux_arm64/foo"))
	require.NoError(t, err)
	require.Equal(t, "foo is bar-arm64\n", string(dt))
}

func testNamedFilteredContext(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	fooDir := integration.Tmpdir(t,
		// small file
		fstest.CreateFile("foo", []byte(`foo`), 0600),
		// blank file that's just large
		fstest.CreateFile("bar", make([]byte, 4096*1000), 0600),
	)

	f := getFrontend(t, sb)

	runTest := func(t *testing.T, dockerfile []byte, target string, min, max int64) {
		t.Run(target, func(t *testing.T) {
			dir := integration.Tmpdir(
				t,
				fstest.CreateFile(dockerui.DefaultDockerfileName, dockerfile, 0600),
			)

			ch := make(chan *client.SolveStatus)

			eg, ctx := errgroup.WithContext(sb.Context())
			eg.Go(func() error {
				_, err := f.Solve(ctx, c, client.SolveOpt{
					FrontendAttrs: map[string]string{
						"context:foo": "local:foo",
						"target":      target,
					},
					LocalMounts: map[string]fsutil.FS{
						dockerui.DefaultLocalNameDockerfile: dir,
						dockerui.DefaultLocalNameContext:    dir,
						"foo":                               fooDir,
					},
				}, ch)
				return err
			})

			eg.Go(func() error {
				transferred := make(map[string]int64)
				re := regexp.MustCompile(`transferring (.+):`)
				for ss := range ch {
					for _, status := range ss.Statuses {
						m := re.FindStringSubmatch(status.ID)
						if m == nil {
							continue
						}

						ctxName := m[1]
						transferred[ctxName] = status.Current
					}
				}

				if foo := transferred["foo"]; foo < min {
					return errors.Errorf("not enough data was transferred, %d < %d", foo, min)
				} else if foo > max {
					return errors.Errorf("too much data was transferred, %d > %d", foo, max)
				}
				return nil
			})

			err := eg.Wait()
			require.NoError(t, err)
		})
	}

	dockerfileBase := []byte(`
FROM scratch AS copy_from
COPY --from=foo /foo /

FROM alpine AS run_mount
RUN --mount=from=foo,src=/foo,target=/in/foo cp /in/foo /foo

FROM foo AS image_source
COPY --from=alpine / /
RUN cat /foo > /bar

FROM scratch AS all
COPY --link --from=copy_from /foo /foo.b
COPY --link --from=run_mount /foo /foo.c
COPY --link --from=image_source /bar /foo.d
`)

	t.Run("new", func(t *testing.T) {
		runTest(t, dockerfileBase, "run_mount", 1, 1024)
		runTest(t, dockerfileBase, "copy_from", 1, 1024)
		runTest(t, dockerfileBase, "image_source", 4096*1000, math.MaxInt64)
		runTest(t, dockerfileBase, "all", 4096*1000, math.MaxInt64)
	})

	dockerfileFull := append([]byte(`
FROM scratch AS foo
COPY <<EOF /foo
test
EOF
`), dockerfileBase...)

	t.Run("replace", func(t *testing.T) {
		runTest(t, dockerfileFull, "run_mount", 1, 1024)
		runTest(t, dockerfileFull, "copy_from", 1, 1024)
		runTest(t, dockerfileFull, "image_source", 4096*1000, math.MaxInt64)
		runTest(t, dockerfileFull, "all", 4096*1000, math.MaxInt64)
	})
}

func testSourceDateEpochWithoutExporter(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter, workers.FeatureSourceDateEpoch)
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch
ENTRYPOINT foo bar
COPY Dockerfile .
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir, err := os.MkdirTemp("", "buildkit")
	require.NoError(t, err)
	defer os.RemoveAll(destDir)

	out := filepath.Join(destDir, "out.tar")
	outW, err := os.Create(out)
	require.NoError(t, err)

	tm := time.Date(2015, time.October, 21, 7, 28, 0, 0, time.UTC)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"build-arg:SOURCE_DATE_EPOCH": fmt.Sprintf("%d", tm.Unix()),
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterOCI,
				// disable exporter epoch to make sure we test dockerfile
				Attrs:  map[string]string{"source-date-epoch": ""},
				Output: fixedWriteCloser(outW),
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "out.tar"))
	require.NoError(t, err)

	m, err := testutil.ReadTarToMap(dt, false)
	require.NoError(t, err)

	var idx ocispecs.Index
	err = json.Unmarshal(m[ocispecs.ImageIndexFile].Data, &idx)
	require.NoError(t, err)

	mlistHex := idx.Manifests[0].Digest.Hex()

	var mfst ocispecs.Manifest
	err = json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+mlistHex].Data, &mfst)
	require.NoError(t, err)

	var img ocispecs.Image
	err = json.Unmarshal(m[ocispecs.ImageBlobsDir+"/sha256/"+mfst.Config.Digest.Hex()].Data, &img)
	require.NoError(t, err)

	require.Equal(t, tm.Unix(), img.Created.Unix())
	for _, h := range img.History {
		require.Equal(t, tm.Unix(), h.Created.Unix())
	}
}

func testSBOMScannerImage(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureDirectPush, workers.FeatureSBOM)
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox:latest
COPY <<-"EOF" /scan.sh
	set -e
	cat <<BUNDLE > $BUILDKIT_SCAN_DESTINATION/spdx.json
	{
	  "_type": "https://in-toto.io/Statement/v0.1",
	  "predicateType": "https://spdx.dev/Document",
	  "predicate": {"name": "sbom-scan"}
	}
	BUNDLE
EOF
CMD sh /scan.sh
`)
	scannerDir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	scannerTarget := registry + "/buildkit/testsbomscanner:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: scannerDir,
			dockerui.DefaultLocalNameContext:    scannerDir,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": scannerTarget,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	dockerfile = []byte(`
FROM scratch
COPY <<EOF /foo
data
EOF
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	target := registry + "/buildkit/testsbomscannertarget:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		FrontendAttrs: map[string]string{
			"attest:sbom": "generator=" + scannerTarget,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	desc, provider, err := contentutil.ProviderFromRef(target)
	require.NoError(t, err)
	imgs, err := testutil.ReadImages(sb.Context(), provider, desc)
	require.NoError(t, err)
	require.Equal(t, 2, len(imgs.Images))

	img := imgs.Find(platforms.Format(platforms.Normalize(platforms.DefaultSpec())))
	require.NotNil(t, img)
	require.Equal(t, []byte("data\n"), img.Layers[0]["foo"].Data)

	att := imgs.Find("unknown/unknown")
	require.Equal(t, 1, len(att.LayersRaw))
	var attest intoto.Statement
	require.NoError(t, json.Unmarshal(att.LayersRaw[0], &attest))
	require.Equal(t, "https://in-toto.io/Statement/v0.1", attest.Type)
	require.Equal(t, intoto.PredicateSPDX, attest.PredicateType)
	require.Subset(t, attest.Predicate, map[string]interface{}{"name": "sbom-scan"})
}

func testSBOMScannerArgs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureDirectPush, workers.FeatureSBOM)
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox:latest
COPY <<-"EOF" /scan.sh
	set -e
	cat <<BUNDLE > $BUILDKIT_SCAN_DESTINATION/spdx.json
	{
	  "_type": "https://in-toto.io/Statement/v0.1",
	  "predicateType": "https://spdx.dev/Document",
	  "predicate": {"name": "core"}
	}
	BUNDLE
	if [ "${BUILDKIT_SCAN_SOURCE_EXTRAS}" ]; then
		for src in "${BUILDKIT_SCAN_SOURCE_EXTRAS}"/*; do
			cat <<BUNDLE > $BUILDKIT_SCAN_DESTINATION/$(basename $src).spdx.json
			{
			  "_type": "https://in-toto.io/Statement/v0.1",
			  "predicateType": "https://spdx.dev/Document",
			  "predicate": {"name": "extra"}
			}
			BUNDLE
		done
	fi
EOF
CMD sh /scan.sh
`)

	scannerDir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	scannerTarget := registry + "/buildkit/testsbomscannerargs:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: scannerDir,
			dockerui.DefaultLocalNameContext:    scannerDir,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": scannerTarget,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	// scan an image with no additional sboms
	dockerfile = []byte(`
FROM scratch as base
COPY <<EOF /foo
data
EOF
FROM base
`)
	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	target := registry + "/buildkit/testsbomscannerargstarget1:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		FrontendAttrs: map[string]string{
			"attest:sbom":                          "generator=" + scannerTarget,
			"build-arg:BUILDKIT_SBOM_SCAN_CONTEXT": "true",
			"build-arg:BUILDKIT_SBOM_SCAN_STAGE":   "true",
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	desc, provider, err := contentutil.ProviderFromRef(target)
	require.NoError(t, err)
	imgs, err := testutil.ReadImages(sb.Context(), provider, desc)
	require.NoError(t, err)
	require.Equal(t, 2, len(imgs.Images))

	img := imgs.Find(platforms.Format(platforms.Normalize(platforms.DefaultSpec())))
	require.NotNil(t, img)

	att := imgs.Find("unknown/unknown")
	require.Equal(t, 1, len(att.LayersRaw))
	var attest intoto.Statement
	require.NoError(t, json.Unmarshal(att.LayersRaw[0], &attest))
	require.Subset(t, attest.Predicate, map[string]interface{}{"name": "core"})

	dockerfile = []byte(`
ARG BUILDKIT_SBOM_SCAN_CONTEXT=true

FROM scratch as file
ARG BUILDKIT_SBOM_SCAN_STAGE=true
COPY <<EOF /file
data
EOF

FROM scratch as base
ARG BUILDKIT_SBOM_SCAN_STAGE=true
COPY --from=file /file /foo

FROM scratch as base2
ARG BUILDKIT_SBOM_SCAN_STAGE=true
COPY --from=file /file /bar
RUN non-existent-command-would-fail

FROM base
ARG BUILDKIT_SBOM_SCAN_STAGE=true
`)
	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	// scan an image with additional sboms
	target = registry + "/buildkit/testsbomscannertarget2:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		FrontendAttrs: map[string]string{
			"attest:sbom": "generator=" + scannerTarget,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	desc, provider, err = contentutil.ProviderFromRef(target)
	require.NoError(t, err)
	imgs, err = testutil.ReadImages(sb.Context(), provider, desc)
	require.NoError(t, err)
	require.Equal(t, 2, len(imgs.Images))

	img = imgs.Find(platforms.Format(platforms.Normalize(platforms.DefaultSpec())))
	require.NotNil(t, img)

	att = imgs.Find("unknown/unknown")
	require.Equal(t, 4, len(att.LayersRaw))
	extraCount := 0
	for _, l := range att.LayersRaw {
		var attest intoto.Statement
		require.NoError(t, json.Unmarshal(l, &attest))
		att := attest.Predicate.(map[string]interface{})
		switch att["name"] {
		case "core":
		case "extra":
			extraCount++
		default:
			require.Fail(t, "unexpected attestation", "%v", att)
		}
	}
	require.Equal(t, extraCount, len(att.LayersRaw)-1)

	// scan an image with additional sboms, but disable them
	target = registry + "/buildkit/testsbomscannertarget3:latest"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		FrontendAttrs: map[string]string{
			"attest:sbom":                          "generator=" + scannerTarget,
			"build-arg:BUILDKIT_SBOM_SCAN_STAGE":   "false",
			"build-arg:BUILDKIT_SBOM_SCAN_CONTEXT": "false",
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": target,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	desc, provider, err = contentutil.ProviderFromRef(target)
	require.NoError(t, err)
	imgs, err = testutil.ReadImages(sb.Context(), provider, desc)
	require.NoError(t, err)
	require.Equal(t, 2, len(imgs.Images))

	img = imgs.Find(platforms.Format(platforms.Normalize(platforms.DefaultSpec())))
	require.NotNil(t, img)

	att = imgs.Find("unknown/unknown")
	require.Equal(t, 1, len(att.LayersRaw))
}

func testLintStageName(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
# warning: stage name should be lowercase
FROM scratch AS BadStageName

# warning: 'as' should match 'FROM' cmd casing.
FROM scratch as base2

FROM scratch AS base3
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'StageNameCasing': Stage name 'BadStageName' should be lowercase (line 3)",
			Detail: "Stage names should be lowercase",
			Level:  1,
		},
		{
			Short:  "Lint Rule 'FromAsCasing': 'as' and 'FROM' keywords' casing do not match (line 6)",
			Detail: "The 'as' keyword should match the case of the 'from' keyword",
			Level:  1,
		},
	})

	dockerfile = []byte(`
# warning: 'AS' should match 'from' cmd casing.
from scratch AS base

from scratch as base2
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'FromAsCasing': 'AS' and 'from' keywords' casing do not match (line 3)",
			Detail: "The 'as' keyword should match the case of the 'from' keyword",
			Level:  1,
		},
	})
}

func testLintNoEmptyContinuations(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
# warning: empty continuation line
COPY Dockerfile \

.
COPY Dockerfile \
.
`)

	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'NoEmptyContinuations': Empty continuation line (line 6)",
			Detail: "Empty continuation lines will become errors in a future release",
			URL:    "https://github.com/moby/moby/pull/33719",
			Level:  1,
		},
	})
}

func testSelfConsistentCommandCasing(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
# warning: 'FROM' should be either lowercased or uppercased
From scratch as base
FROM scratch AS base2
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'SelfConsistentCommandCasing': Command 'From' should be consistently cased (line 3)",
			Detail: "Commands should be in consistent casing (all lower or all upper)",
			Level:  1,
		},
	})
	dockerfile = []byte(`
# warning: 'FROM' should be either lowercased or uppercased
frOM scratch as base
from scratch as base2
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'SelfConsistentCommandCasing': Command 'frOM' should be consistently cased (line 3)",
			Detail: "Commands should be in consistent casing (all lower or all upper)",
			Level:  1,
		},
	})
}

func testFileConsistentCommandCasing(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
# warning: 'copy' should match command majority's casing (uppercase)
copy Dockerfile /foo
COPY Dockerfile /bar
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'FileConsistentCommandCasing': Command 'copy' should match the case of the command majority (uppercase) (line 4)",
			Detail: "All commands within the Dockerfile should use the same casing (either upper or lower)",
			Level:  1,
		},
	})

	dockerfile = []byte(`
from scratch
# warning: 'COPY' should match command majority's casing (lowercase)
COPY Dockerfile /foo
copy Dockerfile /bar
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'FileConsistentCommandCasing': Command 'COPY' should match the case of the command majority (lowercase) (line 4)",
			Detail: "All commands within the Dockerfile should use the same casing (either upper or lower)",
			Level:  1,
		},
	})

	dockerfile = []byte(`
# warning: 'from' should match command majority's casing (uppercase)
from scratch
COPY Dockerfile /foo
COPY Dockerfile /bar
COPY Dockerfile /baz
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'FileConsistentCommandCasing': Command 'from' should match the case of the command majority (uppercase) (line 3)",
			Detail: "All commands within the Dockerfile should use the same casing (either upper or lower)",
			Level:  1,
		},
	})

	dockerfile = []byte(`
# warning: 'FROM' should match command majority's casing (lowercase)
FROM scratch
copy Dockerfile /foo
copy Dockerfile /bar
copy Dockerfile /baz
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{
		{
			Short:  "Lint Rule 'FileConsistentCommandCasing': Command 'FROM' should match the case of the command majority (lowercase) (line 3)",
			Detail: "All commands within the Dockerfile should use the same casing (either upper or lower)",
			Level:  1,
		},
	})

	dockerfile = []byte(`
from scratch
copy Dockerfile /foo
copy Dockerfile /bar
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{})

	dockerfile = []byte(`
FROM scratch
COPY Dockerfile /foo
COPY Dockerfile /bar
`)
	checkLintWarnings(t, sb, dockerfile, []expectedLintWarning{})
}

func testReproSourceDateEpoch(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter, workers.FeatureSourceDateEpoch)
	if sb.Snapshotter() == "native" {
		t.Skip("the digest is not reproducible with the \"native\" snapshotter because hardlinks are processed in a different way: https://github.com/moby/buildkit/pull/3456#discussion_r1062650263")
	}

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	f := getFrontend(t, sb)

	tm := time.Date(2023, time.January, 10, 12, 34, 56, 0, time.UTC) // 1673354096
	t.Logf("SOURCE_DATE_EPOCH=%d", tm.Unix())

	type testCase struct {
		name           string
		dockerfile     string
		files          []fstest.Applier
		expectedDigest string
	}
	testCases := []testCase{
		{
			name: "Basic",
			dockerfile: `# The base image could not be busybox, due to https://github.com/moby/buildkit/issues/3455
FROM amd64/debian:bullseye-20230109-slim
RUN touch /foo
RUN touch /foo.1
RUN touch -d '2010-01-01 12:34:56' /foo-2010
RUN touch -d '2010-01-01 12:34:56' /foo-2010.1
RUN touch -d '2030-01-01 12:34:56' /foo-2030
RUN touch -d '2030-01-01 12:34:56' /foo-2030.1
RUN rm -f /foo.1
RUN rm -f /foo-2010.1
RUN rm -f /foo-2030.1
`,
			expectedDigest: "sha256:04e5d0cbee3317c79f50494cfeb4d8a728402a970ef32582ee47c62050037e3f",
		},
		{
			// https://github.com/moby/buildkit/issues/4746
			name: "CopyLink",
			dockerfile: `FROM amd64/debian:bullseye-20230109-slim
COPY --link foo foo
`,
			files:          []fstest.Applier{fstest.CreateFile("foo", []byte("foo"), 0600)},
			expectedDigest: "sha256:9f75e4bdbf3d825acb36bb603ddef4a25742afb8ccb674763ffc611ae047d8a6",
		},
	}

	// https://explore.ggcr.dev/?image=amd64%2Fdebian%3Abullseye-20230109-slim
	baseImageLayers := []digest.Digest{
		"sha256:8740c948ffd4c816ea7ca963f99ca52f4788baa23f228da9581a9ea2edd3fcd7",
	}
	baseImageHistoryTimestamps := []time.Time{
		timeMustParse(t, time.RFC3339Nano, "2023-01-11T02:34:44.402266175Z"),
		timeMustParse(t, time.RFC3339Nano, "2023-01-11T02:34:44.829692296Z"),
	}

	ctx := sb.Context()
	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := integration.Tmpdir(
				t,
				append([]fstest.Applier{fstest.CreateFile("Dockerfile", []byte(tc.dockerfile), 0600)}, tc.files...)...,
			)

			target := registry + "/buildkit/testreprosourcedateepoch-" + strings.ToLower(tc.name) + ":" + fmt.Sprintf("%d", tm.Unix())
			solveOpt := client.SolveOpt{
				FrontendAttrs: map[string]string{
					"build-arg:SOURCE_DATE_EPOCH": fmt.Sprintf("%d", tm.Unix()),
					"platform":                    "linux/amd64",
				},
				LocalMounts: map[string]fsutil.FS{
					dockerui.DefaultLocalNameDockerfile: dir,
					dockerui.DefaultLocalNameContext:    dir,
				},
				Exports: []client.ExportEntry{
					{
						Type: client.ExporterImage,
						Attrs: map[string]string{
							"name":              target,
							"push":              "true",
							"oci-mediatypes":    "true",
							"rewrite-timestamp": "true",
						},
					},
				},
				CacheExports: []client.CacheOptionsEntry{
					{
						Type: "registry",
						Attrs: map[string]string{
							"ref":            target + "-cache",
							"oci-mediatypes": "true",
							"image-manifest": "true",
						},
					},
				},
			}
			_, err = f.Solve(ctx, c, solveOpt, nil)
			require.NoError(t, err)

			desc, manifest, img := readImage(t, ctx, target)
			_, cacheManifest, _ := readImage(t, ctx, target+"-cache")
			t.Log("The digest may change depending on the BuildKit version, the snapshotter configuration, etc.")
			require.Equal(t, tc.expectedDigest, desc.Digest.String())

			// Image history from the base config must remain immutable
			for i, tm := range baseImageHistoryTimestamps {
				require.True(t, img.History[i].Created.Equal(tm))
			}

			// Image layers, *except the base layers*, must have rewritten-timestamp
			for i, l := range manifest.Layers {
				if i < len(baseImageLayers) {
					require.Empty(t, l.Annotations["buildkit/rewritten-timestamp"])
					require.Equal(t, baseImageLayers[i], l.Digest)
				} else {
					require.Equal(t, fmt.Sprintf("%d", tm.Unix()), l.Annotations["buildkit/rewritten-timestamp"])
				}
			}
			// Cache layers must *not* have rewritten-timestamp
			for _, l := range cacheManifest.Layers {
				require.Empty(t, l.Annotations["buildkit/rewritten-timestamp"])
			}

			// Build again, after pruning the base image layer cache.
			// For testing https://github.com/moby/buildkit/issues/4746
			ensurePruneAll(t, c, sb)
			_, err = f.Solve(ctx, c, solveOpt, nil)
			require.NoError(t, err)
			descAfterPrune, _, _ := readImage(t, ctx, target)
			require.Equal(t, desc.Digest.String(), descAfterPrune.Digest.String())

			// Build again, but without rewrite-timestamp
			solveOpt2 := solveOpt
			delete(solveOpt2.Exports[0].Attrs, "rewrite-timestamp")
			_, err = f.Solve(ctx, c, solveOpt2, nil)
			require.NoError(t, err)
			_, manifest2, img2 := readImage(t, ctx, target)
			for i, tm := range baseImageHistoryTimestamps {
				require.True(t, img2.History[i].Created.Equal(tm))
			}
			for _, l := range manifest2.Layers {
				require.Empty(t, l.Annotations["buildkit/rewritten-timestamp"])
			}
		})
	}
}

func checkLintWarnings(t *testing.T, sb integration.Sandbox, dockerfile []byte, expected []expectedLintWarning) {
	// As a note, this test depends on the `expected` lint
	// warnings to be in order of appearance in the Dockerfile.

	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	status := make(chan *client.SolveStatus)
	statusDone := make(chan struct{})
	done := make(chan struct{})

	var warnings []*client.VertexWarning

	go func() {
		defer close(statusDone)
		for {
			select {
			case st, ok := <-status:
				if !ok {
					return
				}
				warnings = append(warnings, st.Warnings...)
			case <-done:
				return
			}
		}
	}()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		FrontendAttrs: map[string]string{
			"platform": "linux/amd64,linux/arm64",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, status)
	require.NoError(t, err)

	select {
	case <-statusDone:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for statusDone")
	}

	// two platforms only show one warning
	require.Equal(t, len(expected), len(warnings))
	sort.Slice(warnings, func(i, j int) bool {
		w1 := warnings[i]
		w2 := warnings[j]
		if len(w1.Range) == 0 {
			return true
		} else if len(w2.Range) == 0 {
			return false
		}
		return w1.Range[0].Start.Line < w2.Range[0].Start.Line
	})
	for i, w := range warnings {
		require.Equal(t, expected[i].Short, string(w.Short))
		require.Equal(t, expected[i].Detail, string(w.Detail[0]))
		require.Equal(t, expected[i].URL, w.URL)
		require.Equal(t, expected[i].Level, w.Level)
	}
}

func timeMustParse(t *testing.T, layout, value string) time.Time {
	tm, err := time.Parse(layout, value)
	require.NoError(t, err)
	return tm
}

//nolint:revive // context-as-argument: context.Context should be the first parameter of a function
func readImage(t *testing.T, ctx context.Context, ref string) (ocispecs.Descriptor, ocispecs.Manifest, ocispecs.Image) {
	desc, provider, err := contentutil.ProviderFromRef(ref)
	require.NoError(t, err)
	dt, err := content.ReadBlob(ctx, provider, desc)
	require.NoError(t, err)
	var manifest ocispecs.Manifest
	require.NoError(t, json.Unmarshal(dt, &manifest))
	imgDt, err := content.ReadBlob(ctx, provider, manifest.Config)
	require.NoError(t, err)
	// Verify that all the layer blobs are present
	for _, layer := range manifest.Layers {
		layerRA, err := provider.ReaderAt(ctx, layer)
		require.NoError(t, err)
		layerDigest, err := layer.Digest.Algorithm().FromReader(content.NewReader(layerRA))
		require.NoError(t, err)
		require.Equal(t, layer.Digest, layerDigest)
	}
	var img ocispecs.Image
	require.NoError(t, json.Unmarshal(imgDt, &img))
	return desc, manifest, img
}

func testNilContextInSolveGateway(t *testing.T, sb integration.Sandbox) {
	f := getFrontend(t, sb)
	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Build(sb.Context(), client.SolveOpt{}, "", func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		res, err := f.SolveGateway(ctx, c, gateway.SolveRequest{
			Frontend: "dockerfile.v0",
			FrontendInputs: map[string]*pb.Definition{
				dockerui.DefaultLocalNameContext:    nil,
				dockerui.DefaultLocalNameDockerfile: nil,
			},
		})
		if err != nil {
			return nil, err
		}
		return res, nil
	}, nil)
	// should not cause buildkitd to panic
	require.ErrorContains(t, err, "invalid nil input definition to definition op")
}

func testCopyUnicodePath(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)
	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM alpine
COPY test-äöü.txt /
COPY test-%C3%A4%C3%B6%C3%BC.txt /
COPY test+aou.txt /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
		fstest.CreateFile("test-äöü.txt", []byte("foo"), 0644),
		fstest.CreateFile("test-%C3%A4%C3%B6%C3%BC.txt", []byte("bar"), 0644),
		fstest.CreateFile("test+aou.txt", []byte("baz"), 0644),
	)
	destDir := integration.Tmpdir(t)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir.Name,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir.Name, "test-äöü.txt"))
	require.NoError(t, err)
	require.Equal(t, "foo", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir.Name, "test-%C3%A4%C3%B6%C3%BC.txt"))
	require.NoError(t, err)
	require.Equal(t, "bar", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir.Name, "test+aou.txt"))
	require.NoError(t, err)
	require.Equal(t, "baz", string(dt))
}

func testSourcePolicyWithNamedContext(t *testing.T, sb integration.Sandbox) {
	f := getFrontend(t, sb)
	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
	FROM scratch AS replace

	FROM scratch
	COPY --from=replace /foo /
	`)

	replaceContext := integration.Tmpdir(t, fstest.CreateFile("foo", []byte("foo"), 0644))
	mainContext := integration.Tmpdir(t, fstest.CreateFile("Dockerfile", dockerfile, 0600))

	out := t.TempDir()
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{Type: client.ExporterLocal, OutputDir: out},
		},
		FrontendAttrs: map[string]string{
			"context:replace": "docker-image:docker.io/library/alpine:latest",
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: mainContext,
			dockerui.DefaultLocalNameContext:    mainContext,
			"test":                              replaceContext,
		},
		SourcePolicy: &spb.Policy{
			Rules: []*spb.Rule{
				{
					Action: spb.PolicyAction_CONVERT,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/alpine:latest",
					},
					Updates: &spb.Update{
						Identifier: "local://test",
					},
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(out, "foo"))
	require.NoError(t, err)
	require.Equal(t, "foo", string(dt))
}

func runShell(dir string, cmds ...string) error {
	for _, args := range cmds {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("powershell", "-command", args)
		} else {
			cmd = exec.Command("sh", "-c", args)
		}
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			return errors.Wrapf(err, "error running %v", args)
		}
	}
	return nil
}

// ensurePruneAll tries to ensure Prune completes with retries.
// Current cache implementation defers release-related logic using goroutine so
// there can be situation where a build has finished but the following prune doesn't
// cleanup cache because some records still haven't been released.
// This function tries to ensure prune by retrying it.
func ensurePruneAll(t *testing.T, c *client.Client, sb integration.Sandbox) {
	for i := 0; i < 2; i++ {
		require.NoError(t, c.Prune(sb.Context(), nil, client.PruneAll))
		for j := 0; j < 20; j++ {
			du, err := c.DiskUsage(sb.Context())
			require.NoError(t, err)
			if len(du) == 0 {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Logf("retrying prune(%d)", i)
	}
	t.Fatalf("failed to ensure prune")
}

func checkAllReleasable(t *testing.T, c *client.Client, sb integration.Sandbox, checkContent bool) {
	cl, err := c.ControlClient().ListenBuildHistory(sb.Context(), &controlapi.BuildHistoryRequest{
		EarlyExit: true,
	})
	require.NoError(t, err)

	for {
		resp, err := cl.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		_, err = c.ControlClient().UpdateBuildHistory(sb.Context(), &controlapi.UpdateBuildHistoryRequest{
			Ref:    resp.Record.Ref,
			Delete: true,
		})
		require.NoError(t, err)
	}

	retries := 0
loop0:
	for {
		require.True(t, 20 > retries)
		retries++
		du, err := c.DiskUsage(sb.Context())
		require.NoError(t, err)
		for _, d := range du {
			if d.InUse {
				time.Sleep(500 * time.Millisecond)
				continue loop0
			}
		}
		break
	}

	err = c.Prune(sb.Context(), nil, client.PruneAll)
	require.NoError(t, err)

	du, err := c.DiskUsage(sb.Context())
	require.NoError(t, err)
	require.Equal(t, 0, len(du))

	// examine contents of exported tars (requires containerd)
	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		return
	}

	// TODO: make public pull helper function so this can be checked for standalone as well

	client, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")
	snapshotService := client.SnapshotService("overlayfs")

	retries = 0
	for {
		count := 0
		err = snapshotService.Walk(ctx, func(context.Context, snapshots.Info) error {
			count++
			return nil
		})
		require.NoError(t, err)
		if count == 0 {
			break
		}
		require.True(t, 20 > retries)
		retries++
		time.Sleep(500 * time.Millisecond)
	}

	if !checkContent {
		return
	}

	retries = 0
	for {
		count := 0
		err = client.ContentStore().Walk(ctx, func(content.Info) error {
			count++
			return nil
		})
		require.NoError(t, err)
		if count == 0 {
			break
		}
		require.True(t, 20 > retries)
		retries++
		time.Sleep(500 * time.Millisecond)
	}
}

func newContainerd(cdAddress string) (*containerd.Client, error) {
	return containerd.New(cdAddress, containerd.WithTimeout(60*time.Second))
}

func dfCmdArgs(ctx, dockerfile, args string) (string, string) {
	traceFile := filepath.Join(os.TempDir(), "trace"+identity.NewID())
	return fmt.Sprintf("build --progress=plain %s --local context=%s --local dockerfile=%s --trace=%s", args, ctx, dockerfile, traceFile), traceFile
}

type builtinFrontend struct{}

var _ frontend = &builtinFrontend{}

func (f *builtinFrontend) Solve(ctx context.Context, c *client.Client, opt client.SolveOpt, statusChan chan *client.SolveStatus) (*client.SolveResponse, error) {
	opt.Frontend = "dockerfile.v0"
	return c.Solve(ctx, nil, opt, statusChan)
}

func (f *builtinFrontend) SolveGateway(ctx context.Context, c gateway.Client, req gateway.SolveRequest) (*gateway.Result, error) {
	req.Frontend = "dockerfile.v0"
	return c.Solve(ctx, req)
}

func (f *builtinFrontend) DFCmdArgs(ctx, dockerfile string) (string, string) {
	return dfCmdArgs(ctx, dockerfile, "--frontend dockerfile.v0")
}

func (f *builtinFrontend) RequiresBuildctl(t *testing.T) {}

type clientFrontend struct{}

var _ frontend = &clientFrontend{}

func (f *clientFrontend) Solve(ctx context.Context, c *client.Client, opt client.SolveOpt, statusChan chan *client.SolveStatus) (*client.SolveResponse, error) {
	return c.Build(ctx, opt, "", builder.Build, statusChan)
}

func (f *clientFrontend) SolveGateway(ctx context.Context, c gateway.Client, req gateway.SolveRequest) (*gateway.Result, error) {
	if req.Frontend == "" && req.Definition == nil {
		req.Frontend = "dockerfile.v0"
	}
	return c.Solve(ctx, req)
}

func (f *clientFrontend) DFCmdArgs(ctx, dockerfile string) (string, string) {
	return "", ""
}
func (f *clientFrontend) RequiresBuildctl(t *testing.T) {
	t.Skip()
}

type gatewayFrontend struct {
	gw string
}

var _ frontend = &gatewayFrontend{}

func (f *gatewayFrontend) Solve(ctx context.Context, c *client.Client, opt client.SolveOpt, statusChan chan *client.SolveStatus) (*client.SolveResponse, error) {
	opt.Frontend = "gateway.v0"
	if opt.FrontendAttrs == nil {
		opt.FrontendAttrs = make(map[string]string)
	}
	opt.FrontendAttrs["source"] = f.gw
	return c.Solve(ctx, nil, opt, statusChan)
}

func (f *gatewayFrontend) SolveGateway(ctx context.Context, c gateway.Client, req gateway.SolveRequest) (*gateway.Result, error) {
	req.Frontend = "gateway.v0"
	if req.FrontendOpt == nil {
		req.FrontendOpt = make(map[string]string)
	}
	req.FrontendOpt["source"] = f.gw
	return c.Solve(ctx, req)
}

func (f *gatewayFrontend) DFCmdArgs(ctx, dockerfile string) (string, string) {
	return dfCmdArgs(ctx, dockerfile, "--frontend gateway.v0 --opt=source="+f.gw)
}

func (f *gatewayFrontend) RequiresBuildctl(t *testing.T) {}

func getFrontend(t *testing.T, sb integration.Sandbox) frontend {
	v := sb.Value("frontend")
	require.NotNil(t, v)
	fn, ok := v.(frontend)
	require.True(t, ok)
	return fn
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

type expectedLintWarning struct {
	Short  string
	Detail string
	URL    string
	Level  int
}

type secModeSandbox struct{}

func (*secModeSandbox) UpdateConfigFile(in string) string {
	return in
}

type secModeInsecure struct{}

func (*secModeInsecure) UpdateConfigFile(in string) string {
	return in + "\n\ninsecure-entitlements = [\"security.insecure\"]\n"
}

var securityInsecureGranted integration.ConfigUpdater = &secModeInsecure{}
var securityInsecureDenied integration.ConfigUpdater = &secModeSandbox{}

type networkModeHost struct{}

func (*networkModeHost) UpdateConfigFile(in string) string {
	return in + "\n\ninsecure-entitlements = [\"network.host\"]\n"
}

type networkModeSandbox struct{}

func (*networkModeSandbox) UpdateConfigFile(in string) string {
	return in
}

var networkHostGranted integration.ConfigUpdater = &networkModeHost{}
var networkHostDenied integration.ConfigUpdater = &networkModeSandbox{}

func fixedWriteCloser(wc io.WriteCloser) filesync.FileOutputFunc {
	return func(map[string]string) (io.WriteCloser, error) {
		return wc, nil
	}
}
