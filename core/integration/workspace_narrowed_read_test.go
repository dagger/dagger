package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// narrowedRead is one read of part of a Git workspace: a directory with a
// filter, or one file.
type narrowedRead struct {
	name string
	path string
	file bool
	opts core.WorkspaceDirectoryOpts
	// missingAt lists the commits that don't have what it reads, where it
	// must fail on both; everywhere else it must succeed.
	missingAt []string
}

// plainReadDigest is what read gives on plain, a plain Git checkout of the
// same commit: the same copy, made from files Git itself wrote. It's "" when
// the read fails there.
func plainReadDigest(ctx context.Context, c *dagger.Client, plain *core.Directory, read narrowedRead) string {
	rel := strings.TrimPrefix(read.path, "/")
	if read.file {
		dgst, err := plain.File(rel).Digest(ctx)
		if err != nil {
			return ""
		}
		return dgst
	}
	src := plain
	if rel != "" {
		src = plain.Directory(rel)
	}
	dgst, err := core.NewQuery(c).Directory().WithDirectory("/", src, core.DirectoryWithDirectoryOpts{
		Include:   read.opts.Include,
		Exclude:   read.opts.Exclude,
		Gitignore: read.opts.Gitignore,
	}).Digest(ctx)
	if err != nil {
		return ""
	}
	return dgst
}

// workspaceNarrowedReadDigest is what read gives through a session on the
// workspace at ref, as a module reads it. It's "" when the read fails.
func workspaceNarrowedReadDigest(ctx context.Context, t *testctx.T, c *dagger.Client, ref string, read narrowedRead) string {
	t.Helper()
	if !read.file {
		dgst, err := workspaceReadDigest(ctx, t, c, ref, read.path, read.opts)
		if err != nil {
			return ""
		}
		return dgst
	}
	out, err := workspaceQuery(ctx, t, c, ref, fmt.Sprintf(`{currentWorkspace{file(path: %q){digest}}}`, read.path))
	if err != nil {
		return ""
	}
	var res struct {
		CurrentWorkspace struct {
			File struct {
				Digest string
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	return res.CurrentWorkspace.File.Digest
}

// requireNarrowedReadsMatchPlain reads each of reads at each commit in order,
// all on one engine, so a read whose files didn't change between commits can
// be served from an earlier one, and checks every result against a plain
// checkout of its own commit.
func requireNarrowedReadsMatchPlain(ctx context.Context, t *testctx.T, c *dagger.Client, workspace string, commits []string, plain func(commit string) *core.Directory, reads []narrowedRead) {
	t.Helper()
	for i, commit := range commits {
		for _, read := range reads {
			want := plainReadDigest(ctx, c, plain(commit), read)
			got := workspaceNarrowedReadDigest(ctx, t, c, workspace+"@"+commit, read)
			if slices.Contains(read.missingAt, commit) {
				require.Empty(t, want, "step %d, commit %s, read %s: the plain checkout must not have it", i, commit, read.name)
			} else {
				require.NotEmpty(t, want, "step %d, commit %s, read %s: the plain checkout must have it", i, commit, read.name)
			}
			require.Equal(t, want, got, "step %d, commit %s, read %s: the workspace must give what a plain checkout of the commit gives (\"\" is a failed read)", i, commit, read.name)
		}
	}
}

// TestGitWorkspaceNarrowedReadsMatchPlainCheckouts asserts that a read of part
// of a Git workspace gives, at every commit, what a plain checkout of that
// commit gives, by content digest, also when an earlier commit made the same
// read: for a file that changed, one whose mode changed, one deleted, one
// added, an ignore file and an attributes file whose change alters what the
// read returns, and a file that never changed.
func (WorkspaceSuite) TestGitWorkspaceNarrowedReadsMatchPlainCheckouts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "# "+rand.Text()+"\n").
		WithNewFile("data/keep.txt", "keep\n").
		WithNewFile("data/change.txt", "before\n").
		WithNewFile("data/mode.sh", "#!/bin/sh\n").
		WithNewFile("data/gone.txt", "gone\n").
		WithNewFile("data/.gitignore", "*.skip\n").
		WithNewFile("data/listed.tmp", "listed\n").
		WithNewFile("data/listed.skip", "listed\n").
		WithNewFile("attr/.gitattributes", "*.txt -text\n").
		WithNewFile("attr/lines.txt", "one\ntwo\n").
		WithNewFile("attr/other.dat", "other\n").
		// A directory between the read's and a file's, whose attributes change.
		WithNewFile("attr/mid/.gitattributes", "*.dat -text\n").
		WithNewFile("attr/mid/sub/deep.dat", "a\nb\n").
		// A directory a pattern selects while excluding everything in it.
		WithNewFile("data/deep/inner.txt", "inner\n")
	srv := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/repo", content).
		WithWorkdir("/").
		WithExec([]string{"sh", "-c", `set -e
git config --global user.email "root@localhost"
git config --global user.name "Test User"
cd /repo
git init -q -b main
git add -A && git add -f . && git commit -qm base && git tag base
echo after > data/change.txt
chmod +x data/mode.sh
git rm -q data/gone.txt
echo new > data/new.txt
printf '*.skip\n*.tmp\n' > data/.gitignore
printf '*.txt text eol=crlf\n' > attr/.gitattributes
printf '*.dat text eol=crlf\n' > attr/mid/.gitattributes
git add -A && git commit -qm moved && git tag moved
cd /
git clone -q --bare /repo /served/repo.git
git -C /served/repo.git config uploadpack.allowFilter true
git -C /served/repo.git config uploadpack.allowAnySHA1InWant true
git -C /served/repo.git update-server-info
for tag in base moved; do
	git -c core.autocrlf=false clone -q --no-checkout /repo /plain/$tag
	git -C /plain/$tag -c core.autocrlf=false checkout -q $tag
	rm -rf /plain/$tag/.git
done
`})
	gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", srv.Directory("/served"), "", nil)
	gitSrv, err := gitSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })
	host, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)
	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"getent", "hosts", host}).
		Stdout(ctx)
	require.NoError(t, err)
	fields := strings.Fields(out)
	require.NotEmpty(t, fields, "unexpected getent output: %q", out)
	repo := "http://" + fields[0] + "/repo.git"

	reads := []narrowedRead{
		{name: "an unchanged file", path: "/data/keep.txt", file: true},
		{name: "a changed file", path: "/data/change.txt", file: true},
		{name: "a file whose mode changed", path: "/data/mode.sh", file: true},
		{name: "a deleted file", path: "/data/gone.txt", file: true, missingAt: []string{"moved"}},
		{name: "an added file", path: "/data/new.txt", file: true, missingAt: []string{"base"}},
		{name: "a directory selected with nothing in it", path: "/data", opts: core.WorkspaceDirectoryOpts{Include: []string{"deep", "keep.txt"}, Exclude: []string{"deep/**"}}},
		{name: "an attributes file between the read and the file that changed", path: "/attr", opts: core.WorkspaceDirectoryOpts{Include: []string{"mid/sub/*.dat"}}},
		{name: "unchanged files only", path: "/data", opts: core.WorkspaceDirectoryOpts{Include: []string{"keep.txt"}}},
		{name: "every kind of change", path: "/data", opts: core.WorkspaceDirectoryOpts{Include: []string{"*.txt", "*.sh"}}},
		{name: "an ignore file that changed", path: "/data", opts: core.WorkspaceDirectoryOpts{Include: []string{"keep.txt", "listed.*"}, Gitignore: true}},
		{name: "an attributes file that changed", path: "/attr", opts: core.WorkspaceDirectoryOpts{Include: []string{"lines.txt"}}},
		{name: "an attributes file that changed, one file", path: "/attr/lines.txt", file: true},
	}
	requireNarrowedReadsMatchPlain(ctx, t, c, repo, []string{"base", "moved", "base", "moved"},
		func(commit string) *core.Directory { return srv.Directory("/plain/" + commit) },
		reads)
}
