package core

// These tests cover the GraphQL Workspace object after a workspace has already
// been selected or injected into the session. They verify API behavior, not how
// the workspace was found.
//
// See also:
// - workspace_selection_test.go: explicit workspace selection.
// - contextual_workspace_test.go: workspace find-up from the current directory.
// - module_loading_test.go: module source selection and entrypoint arbitration.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// WorkspaceAPISuite owns behavior of the Workspace object once a Workspace has
// already been injected or passed explicitly.
type WorkspaceAPISuite struct{}

func TestWorkspaceAPI(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceAPISuite{})
}

// TestWorkspaceFileAndDirectory should cover the core file-system accessors on
// Workspace.
func (WorkspaceAPISuite) TestWorkspaceFileAndDirectory(ctx context.Context, t *testctx.T) {
	t.Run("file reads workspace content", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := workspaceFixture(t, c, "workspace-api").
			WithNewFile("data.txt", "file content here")

		out, err := ctr.With(daggerCall("reader", "read")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file content here", strings.TrimSpace(out))
	})

	t.Run("directory reads entries and subdirectories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		t.Run("directory entries", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceFixture(t, c, "workspace-api").
				WithNewFile("a.txt", "aaa").
				WithNewFile("b.txt", "bbb").
				WithNewFile("sub/c.txt", "ccc")

			out, err := ctr.With(daggerCall("lister", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "a.txt")
			require.Contains(t, entries, "b.txt")
			require.Contains(t, entries, "sub")
		})

		t.Run("subdirectory", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceFixture(t, c, "workspace-api").
				WithNewFile("sub/foo.txt", "foo").
				WithNewFile("sub/bar.txt", "bar")

			out, err := ctr.With(daggerCall("subdir", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "foo.txt")
			require.Contains(t, entries, "bar.txt")
			require.NotContains(t, entries, "sub/")
		})
	})

	t.Run("directory exclude and gitignore filters apply", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		t.Run("exclude patterns", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceFixture(t, c, "workspace-api").
				WithNewFile("keep.txt", "keep me").
				WithNewFile("drop.log", "drop me")

			out, err := ctr.With(daggerCall("filtered", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "keep.txt")
			require.NotContains(t, entries, "drop.log")
		})

		t.Run("gitignore filters", func(ctx context.Context, t *testctx.T) {
			base := workspaceFixture(t, c, "workspace-api").
				WithNewFile(".gitignore", "*.log\nbuild/\n").
				WithNewFile("keep.txt", "kept").
				WithNewFile("drop.log", "dropped").
				WithNewFile("build/out.bin", "binary").
				WithNewFile("src/app.txt", "app").
				WithNewFile("src/debug.log", "debug log").
				WithExec([]string{"git", "add", "."}).
				WithExec([]string{"git", "commit", "-m", "init"})

			t.Run("root directory respects gitignore", func(ctx context.Context, t *testctx.T) {
				ctr := base
				out, err := ctr.With(daggerCall("gi-root", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "keep.txt")
				require.Contains(t, entries, "src")
				require.NotContains(t, entries, "drop.log")
				require.NotContains(t, entries, "build")
			})

			t.Run("subdirectory respects gitignore", func(ctx context.Context, t *testctx.T) {
				ctr := base
				out, err := ctr.With(daggerCall("gi-sub", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "app.txt")
				require.NotContains(t, entries, "debug.log")
			})

			t.Run("without gitignore includes all files", func(ctx context.Context, t *testctx.T) {
				ctr := base
				out, err := ctr.With(daggerCall("gi-off", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "keep.txt")
				require.Contains(t, entries, "drop.log")
				require.Contains(t, entries, "build")
			})
		})
	})
}

// TestWorkspacePathSafety should cover path normalization and traversal
// protection on Workspace APIs.
func (WorkspaceAPISuite) TestWorkspacePathSafety(ctx context.Context, t *testctx.T) {
	t.Run("parent-directory traversal is rejected", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceFixture(t, c, "workspace-api").
			WithNewFile("legit.txt", "legit")

		t.Run("directory traversal", func(ctx context.Context, t *testctx.T) {
			ctr := base
			_, err := ctr.With(daggerCall("escape-dir", "ls")).Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "escapes workspace root")
		})

		t.Run("file traversal", func(ctx context.Context, t *testctx.T) {
			ctr := base
			_, err := ctr.With(daggerCall("escape-file", "read")).Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "escapes workspace root")
		})
	})

	t.Run("absolute paths resolve from the workspace boundary", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceFixture(t, c, "workspace-api").
			WithNewFile("legit.txt", "legit")

		ctr := base.
			WithNewFile("sub/inner.txt", "inner")
		out, err := ctr.With(daggerCall("abs-rel", "ls")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "inner.txt")
	})
}

// TestWorkspaceFindUp should cover upward search behavior on Workspace.
func (WorkspaceAPISuite) TestWorkspaceFindUp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "workspace-api").
		WithNewFile("root.txt", "at root").
		WithNewFile("a/target.txt", "in a").
		WithNewFile("a/b/other.txt", "in a/b").
		WithExec([]string{"mkdir", "-p", "a/b/c"}).
		WithNewFile("a/b/c/leaf.txt", "leaf").
		WithExec([]string{"mkdir", "-p", "a/somedir"}).
		WithNewFile("a/somedir/hi.txt", "hi")

	t.Run("find file in start directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=other.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/b/other.txt", strings.TrimSpace(out))
	})

	t.Run("find file in parent directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=target.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/target.txt", strings.TrimSpace(out))
	})

	t.Run("find file at workspace root", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=root.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/root.txt", strings.TrimSpace(out))
	})

	t.Run("find directory in parent", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=somedir", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/somedir", strings.TrimSpace(out))
	})

	t.Run("does not find child directory content", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=leaf.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})

	t.Run("does not find missing file", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=nonexistent.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

// TestWorkspaceGlob verifies that Workspace.glob matches files and
// directories on the host filesystem without syncing them into the engine.
func (WorkspaceAPISuite) TestWorkspaceGlob(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "workspace-api").
		WithNewFile("README.md", "readme").
		WithNewFile("CHANGELOG.md", "changelog").
		WithNewFile("main.go", "package main").
		WithNewFile("src/app.go", "package src").
		WithNewFile("src/app_test.go", "package src")

	t.Run("match by extension", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("globber", "--pattern=*.md", "results")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "README.md")
		require.Contains(t, lines, "CHANGELOG.md")
		require.NotContains(t, lines, "main.go")
	})

	t.Run("recursive glob", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("globber", "--pattern=**/*.go", "results")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "main.go")
		require.Contains(t, lines, "src/app.go")
		require.Contains(t, lines, "src/app_test.go")
	})

	t.Run("subdirectory glob", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("globber", "--pattern=src/*.go", "results")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "src/app.go")
		require.Contains(t, lines, "src/app_test.go")
		require.NotContains(t, lines, "main.go")
	})

	t.Run("no matches", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("globber", "--pattern=*.rs", "results")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

func (WorkspaceAPISuite) TestRootlessCurrentWorkspace(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "workspace.txt"), []byte("workspace"), 0o644))
	queryPath := writeQueryDoc(t, t.TempDir(), "rootless.graphql", `{
  currentWorkspace {
    cwd
    configFile
    directory(path: "/") {
      entries
    }
    changes {
      isEmpty
    }
  }
}
`)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"currentWorkspace": {
			"cwd": "/",
			"configFile": "",
			"directory": {"entries": []},
			"changes": {"isEmpty": true}
		}
	}`, string(out))
}

func (WorkspaceAPISuite) TestRootlessCurrentWorkspaceIgnoresIrrelevantDaggerJSON(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.json"), []byte(`{}`), 0o644))
	queryPath := writeQueryDoc(t, workdir, "rootless-irrelevant-legacy.graphql", `{
  currentWorkspace {
    cwd
    configFile
    changes {
      isEmpty
    }
  }
}
`)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"currentWorkspace": {
			"cwd": "/",
			"configFile": "",
			"changes": {"isEmpty": true}
		}
	}`, string(out))
}

func (WorkspaceAPISuite) TestHostWorkspaceOverlayAndExport(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "base.txt"), []byte("base"), 0o644))

	stageQueryPath := writeQueryDoc(t, workdir, "stage.graphql", `{
  currentWorkspace {
    withNewFile(path: "staged.txt", contents: "staged") {
      changes {
        isEmpty
        addedPaths
      }
      file(path: "staged.txt") {
        contents
      }
    }
  }
}
`)
	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", stageQueryPath)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"currentWorkspace": {
			"withNewFile": {
				"changes": {
					"isEmpty": false,
					"addedPaths": ["staged.txt"]
				},
				"file": {
					"contents": "staged"
				}
			}
		}
	}`, string(out))
	_, err = os.Stat(filepath.Join(workdir, "staged.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)

	exportQueryPath := writeQueryDoc(t, workdir, "export.graphql", `{
  currentWorkspace {
    withNewFile(path: "staged.txt", contents: "staged") {
      export
    }
  }
}
`)
	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", exportQueryPath)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(workdir, "staged.txt"))
	require.NoError(t, err)
	require.Equal(t, "staged", string(got))
}

// TestHostWorkspaceSparseOverlayDiff verifies that editing an existing host file
// through the overlay reports it as modified (not added) and exports correctly.
// The overlay diffs against a sparse base (only the touched paths are synced from
// the host, never the whole tree), so the touched file's host version must be
// present for the diff to classify the edit as a modification; a broken sparse
// base would misreport the edit as an addition.
func (WorkspaceAPISuite) TestHostWorkspaceSparseOverlayDiff(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "existing.txt"), []byte("one\ntwo\nthree\n"), 0o644))

	queryPath := writeQueryDoc(t, workdir, "sparse-modify.graphql", `{
  currentWorkspace {
    withNewFile(path: "existing.txt", contents: "one\nCHANGED\nthree\n") {
      withNewFile(path: "brand-new.txt", contents: "new") {
        changes {
          addedPaths
          modifiedPaths
        }
      }
    }
  }
}
`)
	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"currentWorkspace": {
			"withNewFile": {
				"withNewFile": {
					"changes": {
						"addedPaths": ["brand-new.txt"],
						"modifiedPaths": ["existing.txt"]
					}
				}
			}
		}
	}`, string(out))

	// The host tree is untouched until export.
	sparseGot, err := os.ReadFile(filepath.Join(workdir, "existing.txt"))
	require.NoError(t, err)
	require.Equal(t, "one\ntwo\nthree\n", string(sparseGot))

	exportQueryPath := writeQueryDoc(t, workdir, "sparse-export.graphql", `{
  currentWorkspace {
    withNewFile(path: "existing.txt", contents: "one\nCHANGED\nthree\n") {
      export
    }
  }
}
`)
	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", exportQueryPath)
	require.NoError(t, err)
	sparseGot, err = os.ReadFile(filepath.Join(workdir, "existing.txt"))
	require.NoError(t, err)
	require.Equal(t, "one\nCHANGED\nthree\n", string(sparseGot))
}

func (WorkspaceAPISuite) TestHostWorkspaceExportFromGitWorktree(ctx context.Context, t *testctx.T) {
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "repo")
	worktreeDir := filepath.Join(tmp, "worktree")
	initGitRepo(ctx, t, repoDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "base.txt"), []byte("base"), 0o644))
	runGit(ctx, t, repoDir, "add", ".")
	runGit(ctx, t, repoDir, "commit", "-m", "initial")
	runGit(ctx, t, repoDir, "worktree", "add", worktreeDir, "HEAD")

	exportQueryPath := writeQueryDoc(t, worktreeDir, "worktree-export.graphql", `{
  currentWorkspace {
    withNewFile(path: "staged.txt", contents: "staged") {
      export
    }
  }
}
`)
	_, err := hostDaggerExec(ctx, t, worktreeDir, "--silent", "query", "--doc", exportQueryPath)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(worktreeDir, "staged.txt"))
	require.NoError(t, err)
	require.Equal(t, "staged", string(got))
}

func runGit(ctx context.Context, t *testctx.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func (WorkspaceAPISuite) TestWorkspaceConfigBuildersStageOverlay(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	queryPath := writeQueryDoc(t, workdir, "config-builders.graphql", `{
  currentWorkspace {
    withConfigValue(key: "modules.demo.source", value: "./demo") {
      withConfigEnv(name: "dev") {
        configFile
        changes {
          isEmpty
          addedPaths
        }
        file(path: "dagger.toml") {
          contents
        }
      }
    }
  }
}
`)
	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithConfigValue struct {
				WithConfigEnv struct {
					ConfigFile string `json:"configFile"`
					Changes    struct {
						IsEmpty    bool     `json:"isEmpty"`
						AddedPaths []string `json:"addedPaths"`
					} `json:"changes"`
					File struct {
						Contents string `json:"contents"`
					} `json:"file"`
				} `json:"withConfigEnv"`
			} `json:"withConfigValue"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	staged := got.CurrentWorkspace.WithConfigValue.WithConfigEnv
	require.Equal(t, "dagger.toml", staged.ConfigFile)
	require.False(t, staged.Changes.IsEmpty)
	require.Equal(t, []string{"dagger.toml"}, staged.Changes.AddedPaths)
	require.Contains(t, staged.File.Contents, `[modules.demo]`)
	require.Contains(t, staged.File.Contents, `source = "./demo"`)
	require.Contains(t, staged.File.Contents, `[env.dev]`)

	_, err = os.Stat(filepath.Join(workdir, "dagger.toml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func (WorkspaceAPISuite) TestWorkspaceBuilderExportPreservesCommentsAndFullOverlay(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.toml"), []byte(`# keep this comment
[modules.demo]
source = "./demo"
`), 0o644))

	queryPath := writeQueryDoc(t, workdir, "builder-export.graphql", `{
  currentWorkspace {
    withConfigValue(key: "modules.demo.settings.message", value: "hello") {
      withNewFile(path: "generated.txt", contents: "generated") {
        export
      }
    }
  }
}
`)
	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	config, err := os.ReadFile(filepath.Join(workdir, "dagger.toml"))
	require.NoError(t, err)
	require.Contains(t, string(config), "# keep this comment")
	require.Contains(t, string(config), `message = "hello"`)
	generated, err := os.ReadFile(filepath.Join(workdir, "generated.txt"))
	require.NoError(t, err)
	require.Equal(t, "generated", string(generated))
}

func (WorkspaceAPISuite) TestSyntheticWorkspaceModuleBuildersUseWorkspaceSnapshot(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	workspaceID, err := c.Directory().
		WithNewFile("modules/demo/dagger-module.toml", `name = "demo"
engineVersion = "latest"
source = "."

[runtime]
source = "go"
`).
		AsWorkspace().
		ID(ctx)
	require.NoError(t, err)

	var got struct {
		Node struct {
			WithModule struct {
				ConfigFile string `json:"configFile"`
				Changes    struct {
					AddedPaths []string `json:"addedPaths"`
				} `json:"changes"`
				Module struct {
					Name   string `json:"name"`
					Source string `json:"source"`
				} `json:"module"`
				WithUpdatedLock struct {
					Changes struct {
						AddedPaths []string `json:"addedPaths"`
					} `json:"changes"`
					Lock struct {
						Contents string `json:"contents"`
					} `json:"lock"`
				} `json:"withUpdatedLock"`
				WithoutModule struct {
					Modules []struct {
						Name string `json:"name"`
					} `json:"modules"`
					Config struct {
						Contents string `json:"contents"`
					} `json:"config"`
				} `json:"withoutModule"`
			} `json:"withModule"`
		} `json:"node"`
	}
	err = c.Do(ctx, &dagger.Request{
		Query: `query SyntheticWorkspaceModuleBuilders($workspace: ID!) {
  node(id: $workspace) {
    ... on Workspace {
      withModule(ref: "./modules/demo") {
        configFile
        changes { addedPaths }
        module(name: "demo") { name source }
        withUpdatedLock {
          changes { addedPaths }
          lock: file(path: "dagger.lock") { contents }
        }
        withoutModule(name: "demo") {
          modules { name }
          config: file(path: "dagger.toml") { contents }
        }
      }
    }
  }
}`,
		Variables: map[string]any{"workspace": workspaceID},
	}, &dagger.Response{Data: &got})
	require.NoError(t, err)
	require.Equal(t, "dagger.toml", got.Node.WithModule.ConfigFile)
	require.Equal(t, []string{"dagger.toml"}, got.Node.WithModule.Changes.AddedPaths)
	require.Equal(t, "demo", got.Node.WithModule.Module.Name)
	require.Equal(t, "modules/demo", got.Node.WithModule.Module.Source)
	require.ElementsMatch(t, []string{"dagger.lock", "dagger.toml"}, got.Node.WithModule.WithUpdatedLock.Changes.AddedPaths)
	require.Empty(t, got.Node.WithModule.WithUpdatedLock.Lock.Contents)
	require.Empty(t, got.Node.WithModule.WithoutModule.Modules)
	require.NotContains(t, got.Node.WithModule.WithoutModule.Config.Contents, "modules.demo")
}

func (WorkspaceAPISuite) TestAbsoluteModuleRefInsideLocalWorkspaceUsesOverlaySnapshot(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	updated := c.CurrentWorkspace().
		WithNewFile("modules/demo/dagger-module.toml", `name = "demo"
engineVersion = "latest"
source = "."

[runtime]
source = "go"
`).
		WithModule(filepath.Join(workdir, "modules", "demo"))

	name, err := updated.Module("demo").Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "demo", name)
	added, err := updated.Changes().AddedPaths(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"dagger.toml", "modules/", "modules/demo/", "modules/demo/dagger-module.toml"}, added)

	_, err = os.Stat(filepath.Join(workdir, "modules", "demo", "dagger-module.toml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func (WorkspaceAPISuite) TestSyntheticWorkspaceGitModuleStagesConfigAndLock(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	workspaceID, err := c.Directory().AsWorkspace().ID(ctx)
	require.NoError(t, err)

	var got struct {
		Node struct {
			WithModule struct {
				Changes struct {
					AddedPaths []string `json:"addedPaths"`
				} `json:"changes"`
				Config struct {
					Contents string `json:"contents"`
				} `json:"config"`
				Lock struct {
					Contents string `json:"contents"`
				} `json:"lock"`
			} `json:"withModule"`
		} `json:"node"`
	}
	err = c.Do(ctx, &dagger.Request{
		Query: `query SyntheticWorkspaceGitModule($workspace: ID!) {
  node(id: $workspace) {
    ... on Workspace {
      withModule(ref: "github.com/dagger/dagger/modules/wolfi@v0.20.2") {
        changes { addedPaths }
        config: file(path: "dagger.toml") { contents }
        lock: file(path: "dagger.lock") { contents }
      }
    }
  }
}`,
		Variables: map[string]any{"workspace": workspaceID},
	}, &dagger.Response{Data: &got})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"dagger.lock", "dagger.toml"}, got.Node.WithModule.Changes.AddedPaths)
	require.Contains(t, got.Node.WithModule.Config.Contents, `source = "github.com/dagger/dagger/modules/wolfi@v0.20.2"`)
	require.Contains(t, got.Node.WithModule.Lock.Contents, `[["version","1"]]`)
	require.Contains(t, got.Node.WithModule.Lock.Contents, `"git.tag"`)
}

func (WorkspaceAPISuite) TestSyntheticWorkspaceDetectsExistingConfigAndLock(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().
		WithNewFile("app/dagger.toml", `# keep this comment
[modules.existing]
source = "../modules/existing"
`).
		WithNewFile("app/dagger.lock", "").
		WithNewFile("app/nested/marker", "nested").
		AsWorkspace(dagger.DirectoryAsWorkspaceOpts{Cwd: "/app/nested"})

	configFile, err := ws.ConfigFile(ctx)
	require.NoError(t, err)
	require.Equal(t, "app/dagger.toml", configFile)

	updated := ws.WithModule("github.com/dagger/dagger/modules/wolfi@v0.20.2")
	modified, err := updated.Changes().ModifiedPaths(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"app/dagger.lock", "app/dagger.toml"}, modified)

	config, err := updated.File("/app/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, config, "# keep this comment")
	require.Contains(t, config, "[modules.existing]")
	require.Contains(t, config, "[modules.wolfi]")
}

func (WorkspaceAPISuite) TestWorkspaceSDKReadersUseStagedOverlay(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.MkdirAll(filepath.Join(workdir, "sdk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "sdk", "dagger-module.toml"), []byte(`name = "custom-sdk"
engineVersion = "latest"
source = "."

[runtime]
source = "go"
`), 0o644))

	queryPath := writeQueryDoc(t, workdir, "sdk-readers.graphql", `{
  currentWorkspace {
    withSDK(ref: "./sdk", name: "go-sdk", asSdkName: "go") {
      changes {
        addedPaths
      }
      file(path: "dagger.toml") {
        contents
      }
      sdks {
        name
        ref
        modules {
          name
          source
        }
        clients {
          name
          source
        }
      }
      sdk(name: "go") {
        name
        ref
      }
    }
  }
}
`)
	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithSDK struct {
				Changes struct {
					AddedPaths []string `json:"addedPaths"`
				} `json:"changes"`
				File struct {
					Contents string `json:"contents"`
				} `json:"file"`
				SDKs []struct {
					Name    string `json:"name"`
					Ref     string `json:"ref"`
					Modules []any  `json:"modules"`
					Clients []any  `json:"clients"`
				} `json:"sdks"`
				SDK struct {
					Name string `json:"name"`
					Ref  string `json:"ref"`
				} `json:"sdk"`
			} `json:"withSDK"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	staged := got.CurrentWorkspace.WithSDK
	require.Equal(t, []string{"dagger.toml"}, staged.Changes.AddedPaths)
	require.Contains(t, staged.File.Contents, `[modules.go-sdk]`)
	require.Contains(t, staged.File.Contents, `source = "sdk"`)
	require.Contains(t, staged.File.Contents, `[modules.go-sdk.as-sdk]`)
	require.Contains(t, staged.File.Contents, `name = "go"`)
	require.Len(t, staged.SDKs, 1)
	require.Equal(t, "go", staged.SDKs[0].Name)
	require.Equal(t, "sdk", staged.SDKs[0].Ref)
	require.Empty(t, staged.SDKs[0].Modules)
	require.Empty(t, staged.SDKs[0].Clients)
	require.Equal(t, "go", staged.SDK.Name)
	require.Equal(t, "sdk", staged.SDK.Ref)

	_, err = os.Stat(filepath.Join(workdir, "dagger.toml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func (WorkspaceAPISuite) TestWorkspaceExportFailureBySource(ctx context.Context, t *testctx.T) {
	t.Run("rootless local", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		queryPath := writeQueryDoc(t, workdir, "rootless-export.graphql", `{
  currentWorkspace {
    withNewFile(path: "staged.txt", contents: "staged") {
      export
    }
  }
}
`)
		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
		require.Error(t, err)
		requireErrOut(t, err, "workspace export requires a local Git workspace")
	})

	t.Run("synthetic directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		_, err := testutil.QueryWithClient[struct{}](c, t, `{
  directory {
    asWorkspace {
      withNewFile(path: "staged.txt", contents: "staged") {
        export
      }
    }
  }
}`, nil)
		requireErrOut(t, err, "cannot export a synthetic workspace")
	})

	t.Run("remote git ref", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
			WithNewFile("base.txt", "base"))
		workdir := t.TempDir()
		queryPath := writeQueryDoc(t, workdir, "remote-export.graphql", `{
  currentWorkspace {
    withNewFile(path: "staged.txt", contents: "staged") {
      export
    }
  }
}
`)
		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "-W", remoteRef, "query", "--doc", queryPath)
		require.Error(t, err)
		requireErrOut(t, err, "cannot export a remote Git workspace")
	})
}

func (WorkspaceAPISuite) TestHostWorkspaceFunctionalOverlayAPIsChain(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	sourceID, err := c.Directory().WithNewFile("nested.txt", "nested").ID(ctx)
	require.NoError(t, err)

	baseDir := c.Directory().WithNewFile("base.txt", "base")
	changedDir := baseDir.WithNewFile("changed.txt", "changed")
	changesID, err := changedDir.Changes(baseDir).ID(ctx)
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		query      string
		variables  map[string]any
		wantOutput string
	}{
		{
			name: "withNewFile",
			query: `{
				currentWorkspace {
					withNewFile(path: "new.txt", contents: "new") {
						changes {
							addedPaths
						}
					}
				}
			}`,
			wantOutput: `{"currentWorkspace":{"withNewFile":{"changes":{"addedPaths":["new.txt"]}}}}`,
		},
		{
			name: "withNewDirectory",
			query: `query HostWorkspaceWithNewDirectory($source: ID!) {
				currentWorkspace {
					withNewDirectory(path: "dir", source: $source) {
						changes {
							addedPaths
						}
					}
				}
			}`,
			variables:  map[string]any{"source": sourceID},
			wantOutput: `{"currentWorkspace":{"withNewDirectory":{"changes":{"addedPaths":["dir/nested.txt","dir/"]}}}}`,
		},
		{
			name: "withChanges",
			query: `query HostWorkspaceWithChanges($changes: ID!) {
				currentWorkspace {
					withChanges(changes: $changes) {
						changes {
							addedPaths
						}
					}
				}
			}`,
			variables:  map[string]any{"changes": changesID},
			wantOutput: `{"currentWorkspace":{"withChanges":{"changes":{"addedPaths":["changed.txt"]}}}}`,
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := testutil.QueryWithClient[map[string]any](c, t, tc.query, &testutil.QueryOptions{
				Variables: tc.variables,
			})
			require.NoError(t, err)
			got, err := json.Marshal(out)
			require.NoError(t, err)
			require.JSONEq(t, tc.wantOutput, string(got))
		})
	}
}

// TestHostWorkspaceOverlayReads verifies reads through a host overlay: the
// overlay stores no full read root (materializing one would upload the whole
// host tree — the perf half is checked by tracing Host.directory for a missing
// include filter), so reads resolve as a sparse host slice with the overlay
// changeset applied on top. Untouched paths must serve host content, edited and
// created paths the overlay content, and removed paths must not resurface from
// the host.
func (WorkspaceAPISuite) TestHostWorkspaceOverlayReads(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "untouched.txt"), []byte("host"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "edited.txt"), []byte("before"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "doomed.txt"), []byte("doomed"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "notes.md"), []byte("# notes"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(workdir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "sub", "inner.txt"), []byte("inner"), 0o644))

	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	// A changeset that modifies edited.txt and removes doomed.txt.
	baseDir := c.Directory().
		WithNewFile("edited.txt", "before").
		WithNewFile("doomed.txt", "doomed")
	changedDir := c.Directory().WithNewFile("edited.txt", "after")
	changesID, err := changedDir.Changes(baseDir).ID(ctx)
	require.NoError(t, err)

	// All reads go through the same overlay: changeset applied, plus files
	// created directly (one at the root, one in a new directory for glob).
	const overlayPrefix = `query OverlayReads($changes: ID!) {
		currentWorkspace {
			withChanges(changes: $changes) {
				withNewFile(path: "created.txt", contents: "created") {
					withNewFile(path: "docs/new.md", contents: "new doc") {
	`
	const overlaySuffix = `
					}
				}
			}
		}
	}`
	overlayQuery := func(body string) (map[string]any, error) {
		out, err := testutil.QueryWithClient[map[string]any](c, t, overlayPrefix+body+overlaySuffix, &testutil.QueryOptions{
			Variables: map[string]any{"changes": changesID},
		})
		if err != nil {
			return nil, err
		}
		result := *out
		for _, key := range []string{"currentWorkspace", "withChanges", "withNewFile", "withNewFile"} {
			result = result[key].(map[string]any)
		}
		return result, nil
	}

	t.Run("untouched file serves host content", func(ctx context.Context, t *testctx.T) {
		got, err := overlayQuery(`untouched: file(path: "untouched.txt") { contents }`)
		require.NoError(t, err)
		require.Equal(t, "host", got["untouched"].(map[string]any)["contents"])
	})

	t.Run("edited and created files serve overlay content", func(ctx context.Context, t *testctx.T) {
		got, err := overlayQuery(`
			edited: file(path: "edited.txt") { contents }
			created: file(path: "created.txt") { contents }
		`)
		require.NoError(t, err)
		require.Equal(t, "after", got["edited"].(map[string]any)["contents"])
		require.Equal(t, "created", got["created"].(map[string]any)["contents"])
	})

	t.Run("removed file does not resurface from the host", func(ctx context.Context, t *testctx.T) {
		_, err := overlayQuery(`doomed: file(path: "doomed.txt") { contents }`)
		require.Error(t, err)
		require.Contains(t, err.Error(), "doomed.txt")
	})

	t.Run("filtered listing merges host and overlay", func(ctx context.Context, t *testctx.T) {
		got, err := overlayQuery(`listing: directory(path: ".", include: ["*.txt"]) { entries }`)
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]any{"created.txt", "edited.txt", "untouched.txt"},
			got["listing"].(map[string]any)["entries"],
		)
	})

	t.Run("untouched subdirectory reads from host", func(ctx context.Context, t *testctx.T) {
		got, err := overlayQuery(`
			sub: directory(path: "sub") { entries }
			inner: file(path: "sub/inner.txt") { contents }
		`)
		require.NoError(t, err)
		require.ElementsMatch(t, []any{"inner.txt"}, got["sub"].(map[string]any)["entries"])
		require.Equal(t, "inner", got["inner"].(map[string]any)["contents"])
	})

	t.Run("glob includes span host and overlay", func(ctx context.Context, t *testctx.T) {
		got, err := overlayQuery(`markdown: directory(path: ".", include: ["**/*.md"]) { glob(pattern: "**/*.md") }`)
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]any{"notes.md", "docs/new.md"},
			got["markdown"].(map[string]any)["glob"],
		)
	})
}

// TestWorkspaceConfigBuildersAfterUnrelatedEdit verifies that config builders
// still read the host's dagger.toml through an overlay whose edits don't touch
// it (host overlays store no full read root, so config reads dispatch on the
// touched-paths set), and that subsequent builder writes accumulate through the
// overlay's delta.
func (WorkspaceAPISuite) TestWorkspaceConfigBuildersAfterUnrelatedEdit(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.toml"), []byte("[modules.existing]\nsource = \"./existing\"\n"), 0o644))

	queryPath := writeQueryDoc(t, workdir, "config-after-edit.graphql", `{
  currentWorkspace {
    withNewFile(path: "unrelated.txt", contents: "x") {
      withConfigValue(key: "modules.demo.source", value: "./demo") {
        withConfigEnv(name: "dev") {
          changes {
            addedPaths
            modifiedPaths
          }
          file(path: "dagger.toml") {
            contents
          }
        }
      }
    }
  }
}
`)
	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithNewFile struct {
				WithConfigValue struct {
					WithConfigEnv struct {
						Changes struct {
							AddedPaths    []string `json:"addedPaths"`
							ModifiedPaths []string `json:"modifiedPaths"`
						} `json:"changes"`
						File struct {
							Contents string `json:"contents"`
						} `json:"file"`
					} `json:"withConfigEnv"`
				} `json:"withConfigValue"`
			} `json:"withNewFile"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	staged := got.CurrentWorkspace.WithNewFile.WithConfigValue.WithConfigEnv
	require.Equal(t, []string{"unrelated.txt"}, staged.Changes.AddedPaths)
	require.Equal(t, []string{"dagger.toml"}, staged.Changes.ModifiedPaths)
	// Host config content survives (the first builder read the untouched host
	// file), and both builder writes are present (the second read the staged
	// overlay copy).
	require.Contains(t, staged.File.Contents, `[modules.existing]`)
	require.Contains(t, staged.File.Contents, `[modules.demo]`)
	require.Contains(t, staged.File.Contents, `[env.dev]`)

	// The host tree stays untouched until export.
	hostConfig, err := os.ReadFile(filepath.Join(workdir, "dagger.toml"))
	require.NoError(t, err)
	require.NotContains(t, string(hostConfig), "demo")
}
