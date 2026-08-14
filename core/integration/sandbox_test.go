package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	fscopy "github.com/dagger/dagger/internal/fsutil/copy"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type SandboxSuite struct{}

func TestSandbox(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SandboxSuite{})
}

func (SandboxSuite) TestExecUsesGitWorkspace(ctx context.Context, t *testctx.T) {
	repo := t.TempDir()
	moduleSrc, err := filepath.Abs(filepath.Join("..", "..", "modules", "sandbox"))
	require.NoError(t, err)
	moduleDst := filepath.Join(repo, ".dagger", "modules", "sandbox")
	require.NoError(t, fscopy.Copy(ctx, moduleSrc, "/", moduleDst, "/"))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "dagger.toml"), []byte(`[modules.sandbox]
source = ".dagger/modules/sandbox"
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("committed\n"), 0o644))

	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init")
	git("config", "user.name", "Dagger Tests")
	git("config", "user.email", "dagger@example.com")
	git("add", ".")
	git("commit", "-m", "initial")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("working tree\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("untracked\n"), 0o644))

	c := connect(ctx, t, dagger.WithWorkdir(repo))
	require.NoError(t, c.ModuleSource(moduleDst).AsModule().Serve(ctx))
	sandboxID, err := c.Container().From("alpine:3.20").ID(ctx)
	require.NoError(t, err)

	got, err := testutil.QueryWithClient[struct {
		Sandbox struct {
			Exec struct {
				Changes struct {
					AsPatch struct {
						Contents string
					}
				}
			}
		}
	}](c, t, `query($sandbox: ID!) {
  sandbox {
    exec(
      sandbox: $sandbox
      args: ["sh", "-c", "test -d .git && cat tracked.txt untracked.txt > result.txt"]
    ) {
      changes { asPatch { contents } }
    }
  }
}`, &testutil.QueryOptions{Variables: map[string]any{"sandbox": sandboxID}})
	require.NoError(t, err)
	patch := got.Sandbox.Exec.Changes.AsPatch.Contents
	require.Contains(t, patch, "working tree")
	require.Contains(t, patch, "untracked")
}

func (SandboxSuite) TestDangUsesNestedDaggerSchema(ctx context.Context, t *testctx.T) {
	repo := t.TempDir()
	moduleSrc, err := filepath.Abs(filepath.Join("..", "..", "modules", "sandbox"))
	require.NoError(t, err)
	moduleDst := filepath.Join(repo, ".dagger", "modules", "sandbox")
	require.NoError(t, fscopy.Copy(ctx, moduleSrc, "/", moduleDst, "/"))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "dagger.toml"), []byte(`[modules.sandbox]
source = ".dagger/modules/sandbox"
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "dang.toml"), []byte(`[imports.Dagger]
dagger = true
service = ["sh", "-c", "printf '{\"port\":%s,\"session_token\":\"%s\"}\\n' \"$DAGGER_SESSION_PORT\" \"$DAGGER_SESSION_TOKEN\"; exec sleep infinity >/dev/null"]
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "nested.dang"), []byte(`print(Dagger.container
  .from("alpine:3.20")
  .withExec(["echo", "nested Dang schema works"])
  .stdout
  .trimSpace)
`), 0o644))

	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init")
	git("config", "user.name", "Dagger Tests")
	git("config", "user.email", "dagger@example.com")
	git("add", ".")
	git("commit", "-m", "initial")

	c := connect(ctx, t, dagger.WithWorkdir(repo))
	require.NoError(t, c.ModuleSource(moduleDst).AsModule().Serve(ctx))

	dang, err := testutil.QueryWithClient[struct {
		Sandbox struct {
			Dang struct {
				ID string
			}
		}
	}](c, t, `{ sandbox { dang { id } } }`, nil)
	require.NoError(t, err)

	got, err := testutil.QueryWithClient[struct {
		Sandbox struct {
			Exec struct {
				Changes struct {
					AsPatch struct {
						Contents string
					}
				}
			}
		}
	}](c, t, `query($sandbox: ID!) {
  sandbox {
    exec(
      sandbox: $sandbox
      args: ["sh", "-c", "dang nested.dang > nested-result.txt"]
    ) {
      changes { asPatch { contents } }
    }
  }
}`, &testutil.QueryOptions{Variables: map[string]any{"sandbox": dang.Sandbox.Dang.ID}})
	require.NoError(t, err)
	require.Contains(t, got.Sandbox.Exec.Changes.AsPatch.Contents, "nested Dang schema works")
}

func (SandboxSuite) TestGitSandbox(ctx context.Context, t *testctx.T) {
	repo := t.TempDir()
	moduleSrc, err := filepath.Abs(filepath.Join("..", "..", "modules", "sandbox"))
	require.NoError(t, err)
	moduleDst := filepath.Join(repo, ".dagger", "modules", "sandbox")
	require.NoError(t, fscopy.Copy(ctx, moduleSrc, "/", moduleDst, "/"))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "dagger.toml"), []byte(`[modules.sandbox]
source = ".dagger/modules/sandbox"
`), 0o644))

	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init")
	git("config", "user.name", "Dagger Tests")
	git("config", "user.email", "dagger@example.com")
	git("add", ".")
	git("commit", "-m", "initial")

	c := connect(ctx, t, dagger.WithWorkdir(repo))
	require.NoError(t, c.ModuleSource(moduleDst).AsModule().Serve(ctx))

	gitSandbox, err := testutil.QueryWithClient[struct {
		Sandbox struct {
			Git struct {
				ID string
			}
		}
	}](c, t, `{ sandbox { git { id } } }`, nil)
	require.NoError(t, err)

	got, err := testutil.QueryWithClient[struct {
		Sandbox struct {
			Exec struct {
				Changes struct {
					AsPatch struct {
						Contents string
					}
				}
			}
		}
	}](c, t, `query($sandbox: ID!) {
  sandbox {
    exec(
      sandbox: $sandbox
      args: ["sh", "-c", "git --version > git-version.txt"]
    ) {
      changes { asPatch { contents } }
    }
  }
}`, &testutil.QueryOptions{Variables: map[string]any{"sandbox": gitSandbox.Sandbox.Git.ID}})
	require.NoError(t, err)
	require.Contains(t, got.Sandbox.Exec.Changes.AsPatch.Contents, "git version")
}
