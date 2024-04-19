//go:build dfexcludepatterns
// +build dfexcludepatterns

package dockerfile

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var excludedFilesTests = integration.TestFuncs(
	testExcludedFilesOnCopy,
)

func init() {
	allTests = append(allTests, excludedFilesTests...)
}

// See #4439
func testExcludedFilesOnCopy(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	srcDir := integration.Tmpdir(
		t,
		fstest.CreateDir("dir1", fs.ModePerm),
		fstest.CreateDir("dir2", fs.ModePerm),
		fstest.CreateDir("dir3", fs.ModePerm),
		fstest.CreateFile("dir1/file-101.png", []byte(`2`), 0600),
		fstest.CreateFile("dir1/file-102.txt", []byte(`3`), 0600),
		fstest.CreateFile("dir1/file-103.jpeg", []byte(`4`), 0600),
		fstest.CreateFile("dir2/file-201.pdf", []byte(`6`), 0600),
		fstest.CreateFile("dir2/file-202.jpeg", []byte(`7`), 0600),
		fstest.CreateFile("dir2/file-203.png", []byte(`8`), 0600),
		fstest.CreateFile("dir3/file-301.mp3", []byte(`9`), 0600),
		fstest.CreateFile("dir3/file-302.mpeg", []byte(`10`), 0600),
	)

	runTest := func(dockerfile []byte, localMounts map[string]fsutil.FS) {
		f := getFrontend(t, sb)

		destDir := integration.Tmpdir(t)

		dockerDir := integration.Tmpdir(
			t,
			fstest.CreateFile(dockerui.DefaultDockerfileName, dockerfile, 0600),
		)

		if localMounts == nil {
			localMounts = map[string]fsutil.FS{}
		}

		localMounts[dockerui.DefaultLocalNameDockerfile] = dockerDir

		_, err = f.Solve(ctx, c, client.SolveOpt{
			LocalMounts: localMounts,
			Exports: []client.ExportEntry{
				{
					Type:      client.ExporterLocal,
					OutputDir: destDir.Name,
				},
			},
		}, nil)

		require.NoError(t, err)

		testCases := []struct {
			filename        string
			excluded        bool
			expectedContent string
		}{
			// Files copied with COPY command
			{filename: "builder1/1/dir1/file-101.png", excluded: true},
			{filename: "builder1/1/dir1/file-102.txt", excluded: false, expectedContent: `3`},
			{filename: "builder1/1/dir1/file-103.jpeg", excluded: true},
			{filename: "builder1/1/dir2/file-201.pdf", excluded: false, expectedContent: `6`},
			{filename: "builder1/1/dir2/file-202.jpeg", excluded: true},
			{filename: "builder1/1/dir2/file-203.png", excluded: true},
			{filename: "builder1/1/dir3/file-301.mp3", excluded: false, expectedContent: `9`},
			{filename: "builder1/1/dir3/file-302.mpeg", excluded: false, expectedContent: `10`},

			// files copied with COPY command
			{filename: "builder2/2/dir1/file-101.png", excluded: false, expectedContent: `2`},
			{filename: "builder2/2/dir1/file-102.txt", excluded: true},
			{filename: "builder2/2/dir1/file-103.jpeg", excluded: false, expectedContent: `4`},
			{filename: "builder2/2/dir2/file-201.pdf", excluded: true},
			{filename: "builder2/2/dir2/file-202.jpeg", excluded: false, expectedContent: `7`},
			{filename: "builder2/2/dir2/file-203.png", excluded: false, expectedContent: `8`},
			{filename: "builder2/2/dir3/file-301.mp3", excluded: false, expectedContent: `9`},
			{filename: "builder2/2/dir3/file-302.mpeg", excluded: false, expectedContent: `10`},

			// Files copied with ADD command
			{filename: "builder3/3/file-301.mp3", excluded: true},
			{filename: "builder3/3/file-302.mpeg", excluded: false, expectedContent: `10`},

			// Files copied with COPY wildcard
			{filename: "builder4/4/file-101.png", excluded: true},
			{filename: "builder4/4/file-102.txt", excluded: true},
			{filename: "builder4/4/file-103.jpeg", excluded: true},
			{filename: "builder4/4/file-201.pdf", excluded: true},
			{filename: "builder4/4/file-202.jpeg", excluded: true},
			{filename: "builder4/4/file-203.png", excluded: true},
			{filename: "builder4/4/file-301.mp3", excluded: true},
			{filename: "builder4/4/file-301.mp3", excluded: true},
			{filename: "builder4/4/file-302.mpeg", excluded: false, expectedContent: `10`},

			// Files copied with ADD wildcard
			{filename: "builder5/5/file-101.png", excluded: true},
			{filename: "builder5/5/file-102.txt", excluded: false, expectedContent: `3`},
			{filename: "builder5/5/file-103.jpeg", excluded: true},
			{filename: "builder5/5/file-201.pdf", excluded: true},
			{filename: "builder5/5/file-202.jpeg", excluded: true},
			{filename: "builder5/5/file-203.png", excluded: true},
			{filename: "builder5/5/file-301.mp3", excluded: true},
			{filename: "builder5/5/file-301.mp3", excluded: true},
			{filename: "builder5/5/file-302.mpeg", excluded: true},
		}

		for _, tc := range testCases {
			dt, err := os.ReadFile(path.Join(destDir.Name, tc.filename))
			if tc.excluded {
				require.NotNilf(t, err, "File %s should not exist: %v", tc.filename, err)
				continue
			}

			require.NoErrorf(t, err, "File %s should exist", tc.filename)
			require.Equalf(t, tc.expectedContent, string(dt), "File %s does not have matched content", tc.filename)
		}

		// As all files are excluded, the directory must be empty
		items, err := os.ReadDir(path.Join(destDir.Name, `builder6/6`))
		require.NoError(t, err)
		require.Empty(t, items)
	}

	// First, test with reading from a plain directory
	runTest([]byte(`
# ignore all the image files
FROM scratch as builder1
COPY --exclude=*/*.png --exclude=*/*.jpeg . /1

# ignore all document files
FROM scratch as builder2
COPY --exclude=*/*.pdf --exclude=*/*.txt . /2

# ignore all mp3 files
FROM scratch as builder3
ADD --exclude=*.mp3 dir3/ /3/

# Ignore everything, keeping only .mpeg files, using src as a wildcard.
# it's a fake example, just to be sure that src as wildcards work as expected.
FROM scratch as builder4
COPY --exclude=*.mp3 --exclude=*.jpeg --exclude=*.png --exclude=*.pdf --exclude=*.txt dir* /4/

# Ignore everything, keeping only txt files.
# it's a fake example, just to be sure that src as wildcards work as expected.
FROM scratch as builder5
ADD --exclude=*.mp* --exclude=*.jpeg --exclude=*.png --exclude=*.pdf dir* /5

# Exclude all files. No idea how it could be useful, but it should not be forbidden.
FROM scratch as builder6
ADD --exclude=* dir* /6

FROM scratch
COPY --from=builder1 / /builder1
COPY --from=builder2 / /builder2
COPY --from=builder3 / /builder3
COPY --from=builder4 / /builder4
COPY --from=builder5 / /builder5
COPY --from=builder6 / /builder6
`), map[string]fsutil.FS{
		dockerui.DefaultLocalNameContext: srcDir,
	})

	// Load context from a git repository
	{
		gitCommands := []string{
			"git init",
			"git config --local user.email test",
			"git config --local user.name test",
			"git add *",
			"git commit -m 0.1",
			"git tag 0.1",
			"git update-server-info",
		}

		err = runShell(srcDir.Name, gitCommands...)
		require.NoError(t, err)

		server := httptest.NewServer(http.FileServer(http.Dir(filepath.Join(srcDir.Name))))

		defer server.Close()
		serverURL := server.URL

		// In this example, all the ADD commands were replaced by COPY, as ADD does not support
		// the `--from=` flag, and we want to keep the assertions intact.
		runTest([]byte(`
FROM scratch AS base
ARG REPO="`+serverURL+`/.git"
ARG TAG="0.1"
ADD --keep-git-dir=true ${REPO}#${TAG} /

# ignore all the image files
FROM scratch as builder1
COPY --from=base --exclude=*/*.png --exclude=*/*.jpeg . /1

# ignore all document files
FROM scratch as builder2
COPY --from=base --exclude=*/*.pdf --exclude=*/*.txt . /2

# ignore all mp3 files
FROM scratch as builder3
COPY --from=base --exclude=*.mp3 dir3/ /3/

# Ignore everything, keeping only .mpeg files, using src as a wildcard.
# it's a fake example, just to be sure that src as wildcards work as expected.
FROM scratch as builder4
COPY --from=base --exclude=*.mp3 --exclude=*.jpeg --exclude=*.png --exclude=*.pdf --exclude=*.txt dir* /4/

# Ignore everything, keeping only txt files.
# it's a fake example, just to be sure that src as wildcards work as expected.
FROM scratch as builder5
COPY --from=base --exclude=*.mp* --exclude=*.jpeg --exclude=*.png --exclude=*.pdf dir* /5

# Exclude all files. No idea how it could be useful, but it should not be forbidden.
FROM scratch as builder6
COPY --from=base --exclude=* dir* /6

FROM scratch
COPY --from=builder1 / /builder1
COPY --from=builder2 / /builder2
COPY --from=builder3 / /builder3
COPY --from=builder4 / /builder4
COPY --from=builder5 / /builder5
COPY --from=builder6 / /builder6
`), nil)
	}
}
