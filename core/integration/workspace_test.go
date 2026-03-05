package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type WorkspaceSuite struct{}

func TestWorkspace(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSuite{})
}

const dangSDK = "github.com/vito/dang/dagger-sdk@2de20f19b971dad3ee6038e6728736ef1f9a056b"

// workspaceBase returns a container with git, the dagger CLI, and an
// initialized git repo at /work — the starting point for workspace tests.
func workspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

// initDangModule creates a Dang module in the workspace with the given name
// and source code. Uses "dagger init" and "dagger toolchain install" to
// scaffold the workspace and module, then overwrites main.dang with the
// provided source.
func initDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithWorkdir("toolchains/"+name).
			With(daggerExec("init", "--sdk="+dangSDK, "--name="+name)).
			WithNewFile("main.dang", source).
			WithWorkdir("../../").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "./toolchains/"+name))
	}
}

// initDangBlueprint creates a Dang blueprint module and an app module that
// uses it. The blueprint source is written to blueprints/<name>/ and the app
// module is initialized at the workspace root with --blueprint pointing to it.
func initDangBlueprint(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			// Create the blueprint module
			WithWorkdir("blueprints/"+name).
			With(daggerExec("init", "--sdk="+dangSDK, "--name="+name)).
			WithNewFile("main.dang", source).
			WithWorkdir("../../").
			// Init the workspace root module using the blueprint
			With(daggerExec("init", "--blueprint=./blueprints/"+name))
	}
}

func currentWorkspaceID(ctx context.Context, t *testctx.T, c *dagger.Client) string {
	t.Helper()

	res, err := testutil.QueryWithClient[struct {
		CurrentWorkspace struct {
			ID string
		}
	}](c, t, `{ currentWorkspace { id } }`, nil)
	require.NoError(t, err)
	return res.CurrentWorkspace.ID
}

func withMountedWorkspaceExec(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	workspaceID string,
	args []string,
) (string, string) {
	t.Helper()

	res, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedWorkspace struct {
					WithExec struct {
						ID     string
						Stdout string
					}
				}
			}
		}
	}](c, t, fmt.Sprintf(`query Exec($ws: WorkspaceID!, $args: [String!]!) {
		container {
			from(address: %q) {
				withMountedWorkspace(path: "/ws", source: $ws) {
					withExec(args: $args) {
						id
						stdout
					}
				}
			}
		}
	}`, alpineImage), &testutil.QueryOptions{
		Variables: map[string]any{
			"ws":   workspaceID,
			"args": args,
		},
	})
	require.NoError(t, err)
	execRes := res.Container.From.WithMountedWorkspace.WithExec
	return execRes.ID, execRes.Stdout
}

func withMountedWorkspaceExecWithExport(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	workspaceID string,
	export bool,
	args []string,
) (string, string) {
	t.Helper()

	res, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedWorkspace struct {
					WithExec struct {
						ID     string
						Stdout string
					}
				}
			}
		}
	}](c, t, fmt.Sprintf(`query Exec($ws: WorkspaceID!, $export: Boolean!, $args: [String!]!) {
		container {
			from(address: %q) {
				withMountedWorkspace(path: "/ws", source: $ws, export: $export) {
					withExec(args: $args) {
						id
						stdout
					}
				}
			}
		}
	}`, alpineImage), &testutil.QueryOptions{
		Variables: map[string]any{
			"ws":     workspaceID,
			"export": export,
			"args":   args,
		},
	})
	require.NoError(t, err)
	execRes := res.Container.From.WithMountedWorkspace.WithExec
	return execRes.ID, execRes.Stdout
}

func withMountedWorkspaceExecWithLiveRead(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	workspaceID string,
	liveRead bool,
	args []string,
) (string, string) {
	t.Helper()

	res, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedWorkspace struct {
					WithExec struct {
						ID     string
						Stdout string
					}
				}
			}
		}
	}](c, t, fmt.Sprintf(`query Exec($ws: WorkspaceID!, $liveRead: Boolean!, $args: [String!]!) {
		container {
			from(address: %q) {
				withMountedWorkspace(path: "/ws", source: $ws, liveRead: $liveRead) {
					withExec(args: $args) {
						id
						stdout
					}
				}
			}
		}
	}`, alpineImage), &testutil.QueryOptions{
		Variables: map[string]any{
			"ws":       workspaceID,
			"liveRead": liveRead,
			"args":     args,
		},
	})
	require.NoError(t, err)
	execRes := res.Container.From.WithMountedWorkspace.WithExec
	return execRes.ID, execRes.Stdout
}

func loadContainerExec(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	containerID string,
	args []string,
) (string, string) {
	t.Helper()

	res, err := testutil.QueryWithClient[struct {
		Container struct {
			WithExec struct {
				ID     string
				Stdout string
			}
		} `json:"loadContainerFromID"`
	}](c, t, `query Exec($id: ContainerID!, $args: [String!]!) {
		loadContainerFromID(id: $id) {
			withExec(args: $args) {
				id
				stdout
			}
		}
	}`, &testutil.QueryOptions{
		Variables: map[string]any{
			"id":   containerID,
			"args": args,
		},
	})
	require.NoError(t, err)
	execRes := res.Container.WithExec
	return execRes.ID, execRes.Stdout
}

// TestWorkspaceBlueprint verifies that a blueprint module accepting a Workspace
// argument can access the host filesystem, just like a toolchain module.
func (WorkspaceSuite) TestBlueprint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("hello.txt", "hello from workspace").
		With(initDangBlueprint("greeter", `
type Greeter {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub read: String! {
    source.file("hello.txt").contents
  }
}
`))

	out, err := ctr.With(daggerCall("read")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from workspace", strings.TrimSpace(out))
}

// TestWorkspaceFindUp verifies that Workspace.findUp searches up from the
// start path and stops at the workspace root.
func (WorkspaceSuite) TestFindUp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("root.txt", "at root").
		WithNewFile("a/target.txt", "in a").
		WithNewFile("a/b/other.txt", "in a/b").
		WithExec([]string{"mkdir", "-p", "a/b/c"}).
		WithNewFile("a/b/c/leaf.txt", "leaf").
		WithExec([]string{"mkdir", "-p", "a/somedir"}).
		WithNewFile("a/somedir/hi.txt", "hi").
		With(initDangModule("finder", `
type Finder {
  pub result: String!

  new(ws: Workspace!, name: String!, from: String!) {
    self.result = ws.findUp(name: name, from: from) ?? ""
    self
  }
}
`))

	t.Run("find file in start directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=other.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a/b/other.txt", strings.TrimSpace(out))
	})

	t.Run("find file in parent directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=target.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a/target.txt", strings.TrimSpace(out))
	})

	t.Run("find file at workspace root", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=root.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "root.txt", strings.TrimSpace(out))
	})

	t.Run("find directory in parent", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=somedir", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a/somedir", strings.TrimSpace(out))
	})

	t.Run("do not find file in child directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=leaf.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})

	t.Run("do not find non-existent file", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=nonexistent.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

// TestWorkspaceArg verifies that a module function accepting a Workspace
// argument can access the host filesystem.
func (WorkspaceSuite) TestWorkspaceArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("hello.txt", "hello from workspace").
		With(initDangModule("greeter", `
type Greeter {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub read: String! {
    source.file("hello.txt").contents
  }
}
`))

	out, err := ctr.With(daggerCall("greeter", "read")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from workspace", strings.TrimSpace(out))
}

// TestWorkspaceDirectoryEntries verifies that Workspace.directory returns the
// correct entries from the host filesystem.
func (WorkspaceSuite) TestWorkspaceDirectoryEntries(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("a.txt", "aaa").
		WithNewFile("b.txt", "bbb").
		WithNewFile("sub/c.txt", "ccc").
		With(initDangModule("lister", `
type Lister {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))

	out, err := ctr.With(daggerCall("lister", "ls")).Stdout(ctx)
	require.NoError(t, err)
	entries := strings.TrimSpace(out)
	require.Contains(t, entries, "a.txt")
	require.Contains(t, entries, "b.txt")
	require.Contains(t, entries, "sub/")
}

// TestWithMountedWorkspaceWritesPersistInLineage verifies that writes to a
// mounted workspace are visible to subsequent withExec calls in the same
// container lineage, but not to fresh mounts.
func (WorkspaceSuite) TestWithMountedWorkspaceWritesPersistInLineage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	var randBytes [8]byte
	_, err := rand.Read(randBytes[:])
	require.NoError(t, err)
	relPath := fmt.Sprintf(".wsfs-test-%x", randBytes)
	writeCmd := fmt.Sprintf("echo wsfs > /ws/%s", relPath)
	mountedPath := "/ws/" + relPath

	wsRes, err := testutil.QueryWithClient[struct {
		CurrentWorkspace struct {
			ID string
		}
	}](c, t, `{ currentWorkspace { id } }`, nil)
	require.NoError(t, err)

	writeThenReadRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedWorkspace struct {
					WithExec struct {
						WithExec struct {
							Stdout string
						}
					}
				}
			}
		}
	}](c, t, fmt.Sprintf(`query Test($ws: WorkspaceID!) {
		container {
			from(address: %q) {
				withMountedWorkspace(path: "/ws", source: $ws) {
					withExec(args: ["sh", "-c", %q]) {
						withExec(args: ["cat", %q]) {
							stdout
						}
					}
				}
			}
		}
	}`, alpineImage, writeCmd, mountedPath), &testutil.QueryOptions{
		Variables: map[string]any{
			"ws": wsRes.CurrentWorkspace.ID,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "wsfs", strings.TrimSpace(writeThenReadRes.Container.From.WithMountedWorkspace.WithExec.WithExec.Stdout))

	freshMountRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedWorkspace struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](c, t, fmt.Sprintf(`query Test($ws: WorkspaceID!) {
		container {
			from(address: %q) {
				withMountedWorkspace(path: "/ws", source: $ws) {
					withExec(args: ["sh", "-c", "if [ ! -e %s ]; then echo missing; fi"]) {
						stdout
					}
				}
			}
		}
	}`, alpineImage, mountedPath), &testutil.QueryOptions{
		Variables: map[string]any{
			"ws": wsRes.CurrentWorkspace.ID,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "missing", strings.TrimSpace(freshMountRes.Container.From.WithMountedWorkspace.WithExec.Stdout))
}

func (WorkspaceSuite) TestWithMountedWorkspaceExportSync(ctx context.Context, t *testctx.T) {
	wd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wd, "existing.txt"), []byte("old\n"), 0o600))

	c := connect(ctx, t, dagger.WithWorkdir(wd))
	wsID := currentWorkspaceID(ctx, t, c)

	_, _ = withMountedWorkspaceExecWithExport(ctx, t, c, wsID, true, []string{
		"sh", "-ec", "echo updated > /ws/existing.txt; echo created > /ws/created.txt",
	})

	existingBytes, err := os.ReadFile(filepath.Join(wd, "existing.txt"))
	require.NoError(t, err)
	require.Equal(t, "updated\n", string(existingBytes))

	createdBytes, err := os.ReadFile(filepath.Join(wd, "created.txt"))
	require.NoError(t, err)
	require.Equal(t, "created\n", string(createdBytes))
}

func (WorkspaceSuite) TestWithMountedWorkspaceDefaultsToCurrentWorkspace(ctx context.Context, t *testctx.T) {
	wd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wd, "input.txt"), []byte("default-ws"), 0o600))

	c := connect(ctx, t, dagger.WithWorkdir(wd))

	res, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedWorkspace struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](c, t, fmt.Sprintf(`{
		container {
			from(address: %q) {
				withMountedWorkspace(path: "/ws") {
					withExec(args: ["cat", "/ws/input.txt"]) {
						stdout
					}
				}
			}
		}
	}`, alpineImage), nil)
	require.NoError(t, err)
	require.Equal(t, "default-ws", strings.TrimSpace(res.Container.From.WithMountedWorkspace.WithExec.Stdout))
}

func (WorkspaceSuite) TestWithMountedWorkspaceLiveReadRefreshesSamePathAcrossExecs(ctx context.Context, t *testctx.T) {
	t.Run("default mode keeps stale materialized read", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(wd, "input.txt"), []byte("old"), 0o600))

		c := connect(ctx, t, dagger.WithWorkdir(wd))
		wsID := currentWorkspaceID(ctx, t, c)

		ctrID, _ := withMountedWorkspaceExec(ctx, t, c, wsID, []string{
			"sh", "-ec", "cat /ws/input.txt >/dev/null",
		})

		require.NoError(t, os.WriteFile(filepath.Join(wd, "input.txt"), []byte("new"), 0o600))

		_, stdout := loadContainerExec(ctx, t, c, ctrID, []string{
			"cat", "/ws/input.txt",
		})
		require.Equal(t, "old", strings.TrimSpace(stdout))
	})

	t.Run("liveRead refreshes materialized reads", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(wd, "input.txt"), []byte("old"), 0o600))

		c := connect(ctx, t, dagger.WithWorkdir(wd))
		wsID := currentWorkspaceID(ctx, t, c)

		ctrID, _ := withMountedWorkspaceExecWithLiveRead(ctx, t, c, wsID, true, []string{
			"sh", "-ec", "cat /ws/input.txt >/dev/null",
		})

		require.NoError(t, os.WriteFile(filepath.Join(wd, "input.txt"), []byte("new"), 0o600))

		_, stdout := loadContainerExec(ctx, t, c, ctrID, []string{
			"cat", "/ws/input.txt",
		})
		require.Equal(t, "new", strings.TrimSpace(stdout))
	})
}

func (WorkspaceSuite) TestWithMountedWorkspaceLazyMaterialization(ctx context.Context, t *testctx.T) {
	t.Run("read does not materialize siblings", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(wd, "readme.txt"), []byte("first"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(wd, "sibling.txt"), []byte("old"), 0o600))

		c := connect(ctx, t, dagger.WithWorkdir(wd))
		wsID := currentWorkspaceID(ctx, t, c)

		ctrID, _ := withMountedWorkspaceExec(ctx, t, c, wsID, []string{
			"sh", "-c", "cat /ws/readme.txt >/dev/null",
		})

		require.NoError(t, os.WriteFile(filepath.Join(wd, "sibling.txt"), []byte("new"), 0o600))

		_, stdout := loadContainerExec(ctx, t, c, ctrID, []string{
			"cat", "/ws/sibling.txt",
		})
		require.Equal(t, "new", strings.TrimSpace(stdout))
	})

	t.Run("readdir does not materialize descendants", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(wd, "dir", "nested"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(wd, "dir", "top.txt"), []byte("top"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(wd, "dir", "nested", "deep.txt"), []byte("old-deep"), 0o600))

		c := connect(ctx, t, dagger.WithWorkdir(wd))
		wsID := currentWorkspaceID(ctx, t, c)

		ctrID, _ := withMountedWorkspaceExec(ctx, t, c, wsID, []string{
			"sh", "-c", "ls -1 /ws/dir >/dev/null",
		})

		require.NoError(t, os.WriteFile(filepath.Join(wd, "dir", "nested", "deep.txt"), []byte("new-deep"), 0o600))

		_, stdout := loadContainerExec(ctx, t, c, ctrID, []string{
			"cat", "/ws/dir/nested/deep.txt",
		})
		require.Equal(t, "new-deep", strings.TrimSpace(stdout))
	})
}

func (WorkspaceSuite) TestWithMountedWorkspaceSymlinkedSubdirectory(ctx context.Context, t *testctx.T) {
	wd := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(wd, "real"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wd, "real", "file.txt"), []byte("target"), 0o600))
	require.NoError(t, os.Symlink("real", filepath.Join(wd, "linkdir")))

	c := connect(ctx, t, dagger.WithWorkdir(wd))
	wsID := currentWorkspaceID(ctx, t, c)

	_, rootStdout := withMountedWorkspaceExec(ctx, t, c, wsID, []string{
		"sh", "-c", "ls -1A /ws",
	})
	require.Contains(t, rootStdout, "linkdir")

	_, linkedStdout := withMountedWorkspaceExec(ctx, t, c, wsID, []string{
		"cat", "/ws/linkdir/file.txt",
	})
	require.Equal(t, "target", strings.TrimSpace(linkedStdout))
}

func (WorkspaceSuite) TestWithMountedWorkspaceServiceStart(ctx context.Context, t *testctx.T) {
	wd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wd, "input.txt"), []byte("wsfs"), 0o600))

	c := connect(ctx, t, dagger.WithWorkdir(wd))
	wsID := currentWorkspaceID(ctx, t, c)

	res, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedWorkspace struct {
					AsService struct {
						Start string
					}
				}
			}
		}
	}](c, t, fmt.Sprintf(`query Test($ws: WorkspaceID!) {
		container {
			from(address: %q) {
				withMountedWorkspace(path: "/ws", source: $ws) {
					asService(args: ["sh", "-ec", "test \"$(cat /ws/input.txt)\" = wsfs; tail -f /dev/null"]) {
						start
					}
				}
			}
		}
	}`, alpineImage), &testutil.QueryOptions{
		Variables: map[string]any{
			"ws": wsID,
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(res.Container.From.WithMountedWorkspace.AsService.Start))
}

func (WorkspaceSuite) TestWithMountedWorkspaceExecCachePolicy(ctx context.Context, t *testctx.T) {
	runNoWorkspace := func() string {
		c := connect(ctx, t)
		res, err := testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}](c, t, fmt.Sprintf(`{
			container {
				from(address: %q) {
					withExec(args: ["sh", "-c", "cat /proc/sys/kernel/random/uuid"]) {
						stdout
					}
				}
			}
		}`, alpineImage), nil)
		require.NoError(t, err)
		return strings.TrimSpace(res.Container.From.WithExec.Stdout)
	}

	runWithWorkspaceMount := func(workdir string) string {
		c := connect(ctx, t, dagger.WithWorkdir(workdir))
		wsID := currentWorkspaceID(ctx, t, c)
		_, stdout := withMountedWorkspaceExec(ctx, t, c, wsID, []string{
			"sh", "-c", "cat /ws/input.txt >/dev/null; cat /proc/sys/kernel/random/uuid",
		})
		return strings.TrimSpace(stdout)
	}

	cached1 := runNoWorkspace()
	cached2 := runNoWorkspace()
	require.Equal(t, cached1, cached2, "expected withExec to stay cached without workspace mounts")

	wd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wd, "input.txt"), []byte("x"), 0o600))

	uncached1 := runWithWorkspaceMount(wd)
	uncached2 := runWithWorkspaceMount(wd)
	require.NotEqual(t, uncached1, uncached2, "expected withExec to be cache-per-call with workspace mounts")
}

func (WorkspaceSuite) TestWorkspaceEntriesAndStatPrimitives(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	res, err := testutil.QueryWithClient[struct {
		CurrentWorkspace struct {
			Entries []string
			Stat    struct {
				FileType string
			}
		}
	}](c, t, `{
		currentWorkspace {
			entries(path: ".")
			stat(path: "go.mod") {
				fileType
			}
		}
	}`, nil)
	require.NoError(t, err)
	require.Contains(t, res.CurrentWorkspace.Entries, "go.mod")
	require.Equal(t, "REGULAR", res.CurrentWorkspace.Stat.FileType)
}

// TestWorkspaceDirectoryExclude verifies that include/exclude patterns work
// when calling Workspace.directory.
func (WorkspaceSuite) TestWorkspaceDirectoryExclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("keep.txt", "keep me").
		WithNewFile("drop.log", "drop me").
		With(initDangModule("filtered", `
type Filtered {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", exclude: ["*.log"])
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))

	out, err := ctr.With(daggerCall("filtered", "ls")).Stdout(ctx)
	require.NoError(t, err)
	entries := strings.TrimSpace(out)
	require.Contains(t, entries, "keep.txt")
	require.NotContains(t, entries, "drop.log")
}

// TestWorkspaceNotCached verifies that functions accepting Workspace args are
// never persistently cached — changes to the host filesystem are reflected
// on subsequent calls without needing a cache buster.
func (WorkspaceSuite) TestWorkspaceNotCached(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set up a module that lists workspace entries.
	base := workspaceBase(t, c).
		WithNewFile("original.txt", "original").
		With(initDangModule("cachechk", `
type Cachechk {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))

	// First call — should see original.txt.
	out, err := base.With(daggerCall("cachechk", "ls")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "original.txt")
	require.NotContains(t, out, "added.txt")

	// Add a file and call again — should see the new file without any cache buster.
	out, err = base.
		WithNewFile("added.txt", "added").
		With(daggerCall("cachechk", "ls")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "original.txt")
	require.Contains(t, out, "added.txt")
}

// TestWorkspaceFile verifies that Workspace.file returns the correct file
// content from the host filesystem.
func (WorkspaceSuite) TestWorkspaceFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("data.txt", "file content here").
		With(initDangModule("reader", `
type Reader {
  pub content: String!

  new(ws: Workspace!) {
    self.content = ws.file("data.txt").contents
    self
  }

  pub read: String! {
    content
  }
}
`))

	out, err := ctr.With(daggerCall("reader", "read")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "file content here", strings.TrimSpace(out))
}

// TestWorkspaceSubdirectory verifies that Workspace.directory can access
// a subdirectory of the workspace.
func (WorkspaceSuite) TestWorkspaceSubdirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("sub/foo.txt", "foo").
		WithNewFile("sub/bar.txt", "bar").
		With(initDangModule("subdir", `
type Subdir {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory("sub")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))

	out, err := ctr.With(daggerCall("subdir", "ls")).Stdout(ctx)
	require.NoError(t, err)
	entries := strings.TrimSpace(out)
	require.Contains(t, entries, "foo.txt")
	require.Contains(t, entries, "bar.txt")
	// Should NOT contain top-level workspace files.
	require.NotContains(t, entries, "sub/")
}

// TestWorkspacePathTraversal verifies that a module cannot use Workspace to
// escape the workspace root and access arbitrary host paths.
func (WorkspaceSuite) TestWorkspacePathTraversal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("legit.txt", "legit")

	t.Run("directory traversal with ..", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("escape-dir", `
type EscapeDir {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory("../..")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
		_, err := ctr.With(daggerCall("escape-dir", "ls")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "resolves outside root")
	})

	t.Run("file traversal with ..", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("escape-file", `
type EscapeFile {
  pub content: String!

  new(source: Workspace!) {
    self.content = source.file("../../etc/hostname").contents
    self
  }

  pub read: String! {
    content
  }
}
`))
		_, err := ctr.With(daggerCall("escape-file", "read")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "resolves outside root")
	})

	t.Run("absolute path treated as relative", func(ctx context.Context, t *testctx.T) {
		// Absolute paths are relative to workspace root, not the host root.
		// /sub should resolve to <workspace>/sub, not /sub on the host.
		ctr := base.
			WithNewFile("sub/inner.txt", "inner").
			With(initDangModule("abs-rel", `
type AbsRel {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory("/sub")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
		out, err := ctr.With(daggerCall("abs-rel", "ls")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "inner.txt")
	})
}

// TestWorkspaceArgNotExposedAsCLIFlag verifies that Workspace arguments are
// "magical" — injected by the server — and not exposed as CLI flags, but the
// function is still visible and callable.
func (WorkspaceSuite) TestWorkspaceArgNotExposedAsCLIFlag(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("test.txt", "test").
		With(initDangModule("magic", `
type Magic {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))

	// The function should be callable without passing --source (it's auto-injected).
	out, err := ctr.With(daggerCall("magic", "ls")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "test.txt")

	// --help should NOT show a --source flag for the constructor.
	help, err := ctr.With(daggerCall("magic", "--help")).Stdout(ctx)
	require.NoError(t, err)
	require.NotContains(t, help, "--source")
}

// TestWorkspaceDirectoryGitignore verifies that Workspace.directory with
// gitignore: true filters out files matched by .gitignore rules.
func (WorkspaceSuite) TestWorkspaceDirectoryGitignore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile(".gitignore", "*.log\nbuild/\n").
		WithNewFile("keep.txt", "kept").
		WithNewFile("drop.log", "dropped").
		WithNewFile("build/out.bin", "binary").
		WithNewFile("src/app.txt", "app").
		WithNewFile("src/debug.log", "debug log").
		// commit so .gitignore is well-established
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init"})

	t.Run("root directory respects gitignore", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("gi-root", `
type GiRoot {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", gitignore: true)
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
		out, err := ctr.With(daggerCall("gi-root", "ls")).Stdout(ctx)
		require.NoError(t, err)
		entries := strings.TrimSpace(out)
		require.Contains(t, entries, "keep.txt")
		require.Contains(t, entries, "src/")
		require.NotContains(t, entries, "drop.log")
		require.NotContains(t, entries, "build/")
	})

	t.Run("subdirectory respects gitignore", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("gi-sub", `
type GiSub {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory("src", gitignore: true)
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
		out, err := ctr.With(daggerCall("gi-sub", "ls")).Stdout(ctx)
		require.NoError(t, err)
		entries := strings.TrimSpace(out)
		require.Contains(t, entries, "app.txt")
		require.NotContains(t, entries, "debug.log")
	})

	t.Run("without gitignore flag includes all files", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("gi-off", `
type GiOff {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
		out, err := ctr.With(daggerCall("gi-off", "ls")).Stdout(ctx)
		require.NoError(t, err)
		entries := strings.TrimSpace(out)
		require.Contains(t, entries, "keep.txt")
		require.Contains(t, entries, "drop.log")
		require.Contains(t, entries, "build/")
	})
}

// TestWorkspaceContentAddressed verifies that when a module constructor takes
// a Workspace argument, the result is content-addressed: calling a function
// twice with the same workspace content should be cached (the function body
// should not re-execute).
//
// We use nonNestedDevEngine so that each `dagger call` starts a fresh session
// against the same engine. This avoids the session-local dagql cache that
// would mask caching bugs — we need to test the engine's persistent cache.
func (WorkspaceSuite) TestWorkspaceContentAddressed(ctx context.Context, t *testctx.T) {
	var marker = "FUNCTION_EXECUTED:" + rand.Text()

	daggerCallWithLogs := func(args ...string) dagger.WithContainerFunc {
		return func(ctr *dagger.Container) *dagger.Container {
			execArgs := append([]string{"dagger", "--progress=logs", "call"}, args...)
			return ctr.WithExec(execArgs, dagger.ContainerWithExecOpts{
				UseEntrypoint: true,
			})
		}
	}

	t.Run("storing a Directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := workspaceBase(t, c).
			// use a non-nested dev engine - if we use nesting, we'll just hit
			// session-local caches, we need to ensure that each `dagger call` runs with
			// a fresh session to really test the caching semantics
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", exclude: ["*", "!included-file"])
    self
  }

  pub read: String! {
    print("`+marker+`")
    source.file("included-file").contents
  }
}
`))

		// First call — function should execute, marker appears in logs.
		first := base.With(daggerCallWithLogs("cacheme", "read"))
		out1, err := first.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out1, marker, "expected function to execute on first call")

		// Second call — same workspace content, function should be cached.
		// Uses a fresh session (non-nested), so only the engine's persistent
		// content-addressed cache can prevent re-execution.
		second := first.With(daggerCallWithLogs("cacheme", "read"))
		out2, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		// The marker should NOT appear in the second call's stderr, because the
		// function result should have been served from cache.
		require.NotContains(t, out2, marker,
			"expected function to be cached on second call with unchanged workspace content")

		// Third call - write to an unaffected file, function should still be cached
		third := second.
			WithNewFile("another-file", rand.Text()).
			With(daggerCallWithLogs("cacheme", "read"))
		out3, err := third.CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out3, marker,
			"expected function to be cached on third call with unchanged workspace content")

		// Fourth call - write to an affected file, function should not be cached
		newText := rand.Text()
		fourth := third.
			WithNewFile("included-file", newText).
			With(daggerCallWithLogs("cacheme", "read"))
		out4, err := fourth.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out4, newText,
			"expected function to pick up the new text")
		require.Contains(t, out4, marker,
			"expected function to be re-executed on fourth call with changed workspace content")
	})

	t.Run("storing a File", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := workspaceBase(t, c).
			// use a non-nested dev engine - if we use nesting, we'll just hit
			// session-local caches, we need to ensure that each `dagger call` runs with
			// a fresh session to really test the caching semantics
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: File!

  new(source: Workspace!) {
    self.source = source.file("included-file")
    self
  }

  pub read: String! {
    print("`+marker+`")
    source.contents
  }
}
`))

		// First call — function should execute, marker appears in logs.
		first := base.With(daggerCallWithLogs("cacheme", "read"))
		out1, err := first.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out1, marker, "expected function to execute on first call")

		// Second call — same workspace content, function should be cached.
		// Uses a fresh session (non-nested), so only the engine's persistent
		// content-addressed cache can prevent re-execution.
		second := first.With(daggerCallWithLogs("cacheme", "read"))
		out2, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		// The marker should NOT appear in the second call's stderr, because the
		// function result should have been served from cache.
		require.NotContains(t, out2, marker,
			"expected function to be cached on second call with unchanged workspace content")

		// Third call - write to an unaffected file, function should still be cached
		third := second.
			WithNewFile("another-file", rand.Text()).
			With(daggerCallWithLogs("cacheme", "read"))
		out3, err := third.CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out3, marker,
			"expected function to be cached on third call with unchanged workspace content")

		// Fourth call - write to an affected file, function should not be cached
		newText := rand.Text()
		fourth := third.
			WithNewFile("included-file", newText).
			With(daggerCallWithLogs("cacheme", "read"))
		out4, err := fourth.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out4, newText,
			"expected function to pick up the new text")
		require.Contains(t, out4, marker,
			"expected function to be re-executed on fourth call with changed workspace content")
	})

	t.Run("storing the contents of a File", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := workspaceBase(t, c).
			// use a non-nested dev engine - if we use nesting, we'll just hit
			// session-local caches, we need to ensure that each `dagger call` runs with
			// a fresh session to really test the caching semantics
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: String!

  new(source: Workspace!) {
    self.source = source.file("included-file").contents
    self
  }

  pub read: String! {
    print("`+marker+`")
    source
  }
}
`))

		// First call — function should execute, marker appears in logs.
		first := base.With(daggerCallWithLogs("cacheme", "read"))
		out1, err := first.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out1, marker, "expected function to execute on first call")

		// Second call — same workspace content, function should be cached.
		// Uses a fresh session (non-nested), so only the engine's persistent
		// content-addressed cache can prevent re-execution.
		second := first.With(daggerCallWithLogs("cacheme", "read"))
		out2, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		// The marker should NOT appear in the second call's stderr, because the
		// function result should have been served from cache.
		require.NotContains(t, out2, marker,
			"expected function to be cached on second call with unchanged workspace content")

		// Third call - write to an unaffected file, function should still be cached
		third := second.
			WithNewFile("another-file", rand.Text()).
			With(daggerCallWithLogs("cacheme", "read"))
		out3, err := third.CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out3, marker,
			"expected function to be cached on third call with unchanged workspace content")

		// Fourth call - write to an affected file, function should not be cached
		newText := rand.Text()
		fourth := third.
			WithNewFile("included-file", newText).
			With(daggerCallWithLogs("cacheme", "read"))
		out4, err := fourth.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out4, newText,
			"expected function to pick up the new text")
		require.Contains(t, out4, marker,
			"expected function to be re-executed on fourth call with changed workspace content")
	})
}
