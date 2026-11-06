package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// sparseWorkspaceRepo serves one Git repository from two servers, both able to
// serve partial fetches, with two commits tagged base and moved. The partial
// server has lost the contents of outside/lost.txt, as a full fetch of either
// commit needs them and a fetch of only what module loading reads doesn't. It
// returns refs without a version for the complete and partial servers.
func sparseWorkspaceRepo(ctx context.Context, t *testctx.T, c *dagger.Client, moduleBody string) (complete, partial string) {
	t.Helper()
	nonce := rand.Text()
	content := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", `[modules.probe]
source = ".dagger/modules/probe"

[[include]]
source = "./settings/base.toml"
`).
		WithNewFile("settings/base.toml", "# "+nonce+"\n").
		// Outside every module, and still applying to its files.
		WithNewFile(".gitattributes", "*.crlf text eol=crlf\n").
		WithNewFile(".dagger/modules/probe/dagger.json", `{"name":"probe","sdk":{"source":"dang"},"dependencies":[{"name":"dep","source":"../dep"}]}`).
		WithNewFile(".dagger/modules/probe/main.dang", moduleBody).
		WithNewFile(".dagger/modules/probe/run.sh", "#!/bin/sh\n", core.DirectoryWithNewFileOpts{Permissions: 0o755}).
		WithSymlink("run.sh", ".dagger/modules/probe/link").
		WithNewFile(".dagger/modules/probe/notes.crlf", "one\ntwo\n").
		WithNewFile(".dagger/modules/probe/sub/keep.txt", "keep\n").
		WithNewFile(".dagger/modules/probe/sub/other.txt", "other\n").
		WithSymlink("../../../outside/also.txt", ".dagger/modules/probe/outward").
		WithNewFile(".dagger/modules/dep/dagger.json", `{"name":"dep","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/dep/main.dang", "type Dep { pub name: String! { \"dep\" } }\n").
		WithNewFile("outside/also.txt", "also\n").
		WithNewFile("outside/lost.txt", "lost "+nonce+"\n").
		WithSymlink("outside", "linkdir")

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
git add -A && git commit -qm base && git tag base
lost=$(git rev-parse HEAD:outside/lost.txt)
echo changed > .dagger/modules/probe/sub/keep.txt
echo moved > outside/also.txt
git add -A && git commit -qm moved && git tag moved
cd /
mkdir -p /complete /partial
git clone -q --no-local --bare /repo /complete/repo.git
git -C /complete/repo.git config uploadpack.allowFilter true
git -C /complete/repo.git config uploadpack.allowAnySHA1InWant true
git -C /complete/repo.git update-server-info
cp -r /complete/repo.git /partial/repo.git
cd /partial/repo.git
mkdir /unpack
mv objects/pack/* /unpack/ 2>/dev/null || true
for pack in /unpack/*.pack; do
	[ -e "$pack" ] && git unpack-objects -q < "$pack"
done
rm "objects/$(echo "$lost" | cut -c1-2)/$(echo "$lost" | cut -c3-)"
`})

	serve := func(dir string) string {
		gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", srv.Directory(dir), "", nil)
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
		return "http://" + fields[0] + "/repo.git"
	}
	return serve("/complete"), serve("/partial")
}

// workspaceQuery runs a query on the workspace at ref from an empty directory,
// as a user's session does.
func workspaceQuery(ctx context.Context, t *testctx.T, c *dagger.Client, ref, query string) (string, error) {
	t.Helper()
	return core.NewQuery(c).Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty").
		With(workspaceSelectionDaggerQuery(query, "-W", ref)).
		Stdout(ctx)
}

func workspaceReadDigest(ctx context.Context, t *testctx.T, c *dagger.Client, ref, dirPath string, opts core.WorkspaceDirectoryOpts) (string, error) {
	t.Helper()
	args := []string{fmt.Sprintf("path: %q", dirPath)}
	quoted := func(patterns []string) string {
		bs, err := json.Marshal(patterns)
		require.NoError(t, err)
		return string(bs)
	}
	if len(opts.Include) > 0 {
		args = append(args, "include: "+quoted(opts.Include))
	}
	if len(opts.Exclude) > 0 {
		args = append(args, "exclude: "+quoted(opts.Exclude))
	}
	out, err := workspaceQuery(ctx, t, c, ref, fmt.Sprintf(`{currentWorkspace{directory(%s){digest}}}`, strings.Join(args, ", ")))
	if err != nil {
		return "", err
	}
	var res struct {
		CurrentWorkspace struct {
			Directory struct {
				Digest string
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	return res.CurrentWorkspace.Directory.Digest, nil
}

// TestGitWorkspaceLoadsWithoutItsWholeTree asserts that a Git workspace's
// session fetches only what it reads: on a server that has lost a file outside
// its modules, which any fetch of the whole tree needs, its modules still list
// and run, and a read of the lost file fails rather than coming back empty.
func (WorkspaceSuite) TestGitWorkspaceLoadsWithoutItsWholeTree(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, partial := sparseWorkspaceRepo(ctx, t, c, `type Probe {
  pub verify: Void @check { null }
  pub hello: String! { "hello from " + dep.name }
}
`)
	for _, tag := range []string{"base", "moved"} {
		t.Run(tag, func(ctx context.Context, t *testctx.T) {
			ref := partial + "@" + tag
			base := core.NewQuery(c).Container().From(alpineImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/empty")

			out, err := base.With(workspaceSelectionDaggerExec("-W", ref, "check", "-l")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "verify")

			out, err = base.With(workspaceSelectionDaggerCall("-W", ref, "probe", "hello")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello from dep", strings.TrimSpace(out))

			_, err = workspaceReadDigest(ctx, t, c, ref, "/outside", core.WorkspaceDirectoryOpts{Include: []string{"lost.txt"}})
			require.Error(t, err, "a read of a file the sparse tree doesn't hold must not come back without it")
		})
	}
}

// TestGitWorkspaceSparseReadsMatchFullReads asserts that what a Git workspace's
// session reads without its whole tree is what the whole tree holds, by content
// digest: file bytes after attributes, modes, symlinks and empty selections
// alike. So work keyed by these reads is reused between the two.
func (WorkspaceSuite) TestGitWorkspaceSparseReadsMatchFullReads(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	complete, partial := sparseWorkspaceRepo(ctx, t, c, "type Probe { pub name: String! { \"probe\" } }\n")

	moduleDir := "/.dagger/modules/probe"
	inModules := []struct {
		name string
		path string
		opts core.WorkspaceDirectoryOpts
	}{
		{"module files", "/", core.WorkspaceDirectoryOpts{Include: []string{".dagger/modules/probe/**"}}},
		{"config file", moduleDir, core.WorkspaceDirectoryOpts{Include: []string{"dagger.json"}}},
		{"executable and symlink", moduleDir, core.WorkspaceDirectoryOpts{Include: []string{"run.sh", "link"}}},
		{"attributes from outside the read", moduleDir, core.WorkspaceDirectoryOpts{Include: []string{"notes.crlf"}}},
		{"exclude", moduleDir, core.WorkspaceDirectoryOpts{Exclude: []string{"sub"}}},
		{"directory with its files excluded", moduleDir, core.WorkspaceDirectoryOpts{Include: []string{"sub"}, Exclude: []string{"sub/**"}}},
		{"nothing selected", moduleDir, core.WorkspaceDirectoryOpts{Include: []string{"no-such-file"}}},
		{"nothing selected in a directory the read doesn't hold files of", "/outside", core.WorkspaceDirectoryOpts{Include: []string{"no-such-file"}}},
	}
	// Reads of files outside the modules come from the whole tree, so these are
	// on the complete server.
	outsideModules := []struct {
		name string
		path string
		opts core.WorkspaceDirectoryOpts
	}{
		{"a file outside the modules", "/outside", core.WorkspaceDirectoryOpts{Include: []string{"also.txt"}}},
		{"both inside and outside", "/", core.WorkspaceDirectoryOpts{Include: []string{".dagger/modules/probe/**", "outside/**"}}},
		{"through a symlink", "/linkdir", core.WorkspaceDirectoryOpts{Include: []string{"also.txt"}}},
	}

	fullRead := func(ctx context.Context, tag, dirPath string, opts core.WorkspaceDirectoryOpts) string {
		t.Helper()
		digest, err := core.NewQuery(c).Git(complete).Ref(tag).AsWorkspace().Directory(dirPath, opts).Digest(ctx)
		require.NoError(t, err)
		return digest
	}

	for _, tag := range []string{"base", "moved"} {
		t.Run(tag, func(ctx context.Context, t *testctx.T) {
			for _, read := range inModules {
				t.Run(read.name, func(ctx context.Context, t *testctx.T) {
					sparse, err := workspaceReadDigest(ctx, t, c, partial+"@"+tag, read.path, read.opts)
					require.NoError(t, err)
					require.Equal(t, fullRead(ctx, tag, read.path, read.opts), sparse)
				})
			}
			for _, read := range outsideModules {
				t.Run(read.name, func(ctx context.Context, t *testctx.T) {
					got, err := workspaceReadDigest(ctx, t, c, complete+"@"+tag, read.path, read.opts)
					require.NoError(t, err)
					require.Equal(t, fullRead(ctx, tag, read.path, read.opts), got)
				})
			}

			t.Run("a path the commit doesn't have", func(ctx context.Context, t *testctx.T) {
				// The read itself fails, in both, so a caller finds out when
				// it makes the read.
				opts := core.WorkspaceDirectoryOpts{Include: []string{"anything"}}
				_, err := workspaceReadDigest(ctx, t, c, partial+"@"+tag, "/no/such/dir", opts)
				require.Error(t, err)
				_, err = workspaceQuery(ctx, t, c, partial+"@"+tag,
					`{currentWorkspace{directory(path: "/no/such/dir", include: ["anything"]){exists(path: "anything")}}}`)
				require.Error(t, err)
				_, err = core.NewQuery(c).Git(complete).Ref(tag).AsWorkspace().Directory("/no/such/dir", opts).Exists(ctx, "anything")
				require.ErrorContains(t, err, "no such file or directory")
			})

			t.Run("bytes after attributes", func(ctx context.Context, t *testctx.T) {
				out, err := workspaceQuery(ctx, t, c, partial+"@"+tag,
					fmt.Sprintf(`{currentWorkspace{file(path: %q){contents}}}`, moduleDir+"/notes.crlf"))
				require.NoError(t, err)
				require.Contains(t, out, `one\r\ntwo\r\n`)
			})

			t.Run("a commit's own files", func(ctx context.Context, t *testctx.T) {
				out, err := workspaceQuery(ctx, t, c, partial+"@"+tag,
					fmt.Sprintf(`{currentWorkspace{file(path: %q){contents}}}`, moduleDir+"/sub/keep.txt"))
				require.NoError(t, err)
				want := "keep"
				if tag == "moved" {
					want = "changed"
				}
				require.Contains(t, out, want)
			})
		})
	}
}

// TestGitWorkspaceFullReadsReachOutsideASparseRead asserts that a read of a
// directory without a filter, a view into the whole tree, reaches what's outside
// it, even after a filtered read of the same directory that the whole tree
// wasn't needed for. The two directories' own files are the same, so work on
// the first must not stand in for the second.
func (WorkspaceSuite) TestGitWorkspaceFullReadsReachOutsideASparseRead(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	moduleDir := "/.dagger/modules/probe"
	for _, tc := range []struct {
		name    string
		include []string
	}{
		{"one file", []string{"dagger.json"}},
		// This copy holds what the directory itself holds, so they have the
		// same content digest.
		{"every file", []string{"**"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			complete, _ := sparseWorkspaceRepo(ctx, t, c, "type Probe { pub name: String! { \"probe\" } }\n")
			ref := complete + "@base"

			_, err := workspaceReadDigest(ctx, t, c, ref, moduleDir, core.WorkspaceDirectoryOpts{Include: tc.include})
			require.NoError(t, err)
			out, err := workspaceQuery(ctx, t, c, ref, `{currentWorkspace{file(path: "/outside/also.txt"){contents}}}`)
			require.NoError(t, err)
			require.Contains(t, out, "also")

			out, err = workspaceQuery(ctx, t, c, ref,
				fmt.Sprintf(`{currentWorkspace{directory(path: %q){file(path: "outward"){contents}}}}`, moduleDir))
			require.NoError(t, err)
			require.Contains(t, out, "also")
		})
	}
}

// TestGitWorkspaceSubmoduleReadsMatchFullReads asserts that a read of files
// inside a submodule holds them: they are in no list of the commit's own paths,
// so such a read must not count as selecting nothing.
func (WorkspaceSuite) TestGitWorkspaceSubmoduleReadsMatchFullReads(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srv := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithNewFile("/parent/dagger.toml", "# "+rand.Text()+"\n").
		WithWorkdir("/").
		WithExec([]string{"sh", "-c", `set -e
git config --global user.email "root@localhost"
git config --global user.name "Test User"
git config --global protocol.file.allow always
git init -q -b main /sub
echo needed > /sub/needed.txt
git -C /sub add -A
git -C /sub commit -qm sub
cd /parent
git init -q -b main
git submodule --quiet add /sub vendor/sub
git config -f .gitmodules submodule.vendor/sub.url ../sub.git
git add -A
git commit -qm parent
mkdir /served
git clone -q --no-local --bare /parent /served/parent.git
git clone -q --no-local --bare /sub /served/sub.git
git -C /served/parent.git update-server-info
git -C /served/sub.git update-server-info
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
	repo := "http://" + fields[0] + "/parent.git"

	opts := core.WorkspaceDirectoryOpts{Include: []string{"vendor/sub/needed.txt"}}
	got, err := workspaceReadDigest(ctx, t, c, repo+"@main", "/", opts)
	require.NoError(t, err)
	want, err := core.NewQuery(c).Git(repo).Ref("main").AsWorkspace().Directory("/", opts).Digest(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)

	out, err = workspaceQuery(ctx, t, c, repo+"@main", `{currentWorkspace{file(path: "/vendor/sub/needed.txt"){contents}}}`)
	require.NoError(t, err)
	require.Contains(t, out, "needed")
}

// TestGitWorkspaceModuleReadsOutsideItsFiles asserts that a module can still
// read what module loading didn't: a +defaultPath directory outside every
// module comes from the whole tree, whole.
func (WorkspaceSuite) TestGitWorkspaceModuleReadsOutsideItsFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	complete, _ := sparseWorkspaceRepo(ctx, t, c, `type Probe {
  pub read(dir: Directory! @defaultPath(path: "/outside")): String! {
    dir.file("also.txt").contents + dir.file("lost.txt").contents
  }
}
`)
	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty").
		With(workspaceSelectionDaggerCall("-W", complete+"@base", "probe", "read")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "also")
	require.Contains(t, out, "lost ")
}
