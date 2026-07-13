package core

// These tests cover Workspace.moduleInit path handling, in particular the
// workspace path contract: relative paths resolve from the caller cwd,
// absolute paths resolve from the workspace root. See
// future/synthetic-workspace.md ("Source Rules").
//
// They use the real go-sdk because moduleInit requires an installed SDK
// implementing initModule, and loadWorkspaceSDK cannot load relative local
// SDK refs today ("local module dep source path must be relative to a parent
// module"), so a local fixture SDK cannot be dispatched to.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type WorkspaceModuleInitSuite struct{}

func TestWorkspaceModuleInit(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceModuleInitSuite{})
}

const goSDKWorkspaceConfig = `[modules.go-sdk]
source = "github.com/dagger/go-sdk"

[modules.go-sdk.as-sdk]
name = "go"
`

func (WorkspaceModuleInitSuite) TestModuleInitFromSubdirectory(ctx context.Context, t *testctx.T) {
	setupSubdirWorkspace := func(t *testctx.T) (workdir, subdir string) {
		t.Helper()
		workdir = t.TempDir()
		subdir = filepath.Join(workdir, "subdir")
		require.NoError(t, os.MkdirAll(subdir, 0o755))
		initGitRepo(ctx, t, workdir)
		require.NoError(t, os.WriteFile(filepath.Join(subdir, "dagger.toml"), []byte(goSDKWorkspaceConfig), 0o644))
		return workdir, subdir
	}

	t.Run("relative path resolves from cwd", func(ctx context.Context, t *testctx.T) {
		_, subdir := setupSubdirWorkspace(t)
		c := connect(ctx, t, dagger.WithWorkdir(subdir))

		changes := c.CurrentWorkspace().ModuleInit("main", dagger.WorkspaceModuleInitOpts{
			SDK:  "go",
			Path: ".dagger",
		})

		added, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.Contains(t, added, "subdir/.dagger/dagger-module.toml")
		require.Contains(t, added, "subdir/.dagger/main.go")

		modified, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.Contains(t, modified, "subdir/dagger.toml")

		config, err := changes.After().File("subdir/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `path = ".dagger"`)
	})

	t.Run("relative path resolves from cwd below the config directory", func(ctx context.Context, t *testctx.T) {
		_, subdir := setupSubdirWorkspace(t)
		subsubdir := filepath.Join(subdir, "subsubdir")
		require.NoError(t, os.MkdirAll(subsubdir, 0o755))
		c := connect(ctx, t, dagger.WithWorkdir(subsubdir))

		changes := c.CurrentWorkspace().ModuleInit("main", dagger.WorkspaceModuleInitOpts{
			SDK:  "go",
			Path: ".dagger",
		})

		added, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.Contains(t, added, "subdir/subsubdir/.dagger/dagger-module.toml")
		require.Contains(t, added, "subdir/subsubdir/.dagger/main.go")

		config, err := changes.After().File("subdir/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `path = "subsubdir/.dagger"`)
	})

	t.Run("absolute path resolves from workspace boundary", func(ctx context.Context, t *testctx.T) {
		_, subdir := setupSubdirWorkspace(t)
		c := connect(ctx, t, dagger.WithWorkdir(subdir))

		changes := c.CurrentWorkspace().ModuleInit("main", dagger.WorkspaceModuleInitOpts{
			SDK:  "go",
			Path: "/subdir/.dagger",
		})

		added, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.Contains(t, added, "subdir/.dagger/dagger-module.toml")
		require.Contains(t, added, "subdir/.dagger/main.go")
	})

	t.Run("default path resolves from cwd and installs the module", func(ctx context.Context, t *testctx.T) {
		_, subdir := setupSubdirWorkspace(t)
		c := connect(ctx, t, dagger.WithWorkdir(subdir))

		changes := c.CurrentWorkspace().ModuleInit("main", dagger.WorkspaceModuleInitOpts{
			SDK: "go",
		})

		added, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.Contains(t, added, "subdir/.dagger/modules/main/dagger-module.toml")
		require.Contains(t, added, "subdir/.dagger/modules/main/main.go")

		config, err := changes.After().File("subdir/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `path = ".dagger/modules/main"`)
		require.Contains(t, config, `source = ".dagger/modules/main"`)
	})

	t.Run("default path with workspace config at the boundary", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		subdir := filepath.Join(workdir, "subdir")
		require.NoError(t, os.MkdirAll(subdir, 0o755))
		initGitRepo(ctx, t, workdir)
		require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.toml"), []byte(goSDKWorkspaceConfig), 0o644))

		c := connect(ctx, t, dagger.WithWorkdir(subdir))

		changes := c.CurrentWorkspace().ModuleInit("main", dagger.WorkspaceModuleInitOpts{
			SDK: "go",
		})

		added, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.Contains(t, added, "subdir/.dagger/modules/main/dagger-module.toml")
		require.Contains(t, added, "subdir/.dagger/modules/main/main.go")

		config, err := changes.After().File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `path = "subdir/.dagger/modules/main"`)
		require.Contains(t, config, `source = "subdir/.dagger/modules/main"`)
	})

	t.Run("path at the workspace root from the root", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)
		require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.toml"), []byte(goSDKWorkspaceConfig), 0o644))

		c := connect(ctx, t, dagger.WithWorkdir(workdir))

		changes := c.CurrentWorkspace().ModuleInit("main", dagger.WorkspaceModuleInitOpts{
			SDK:  "go",
			Path: ".dagger",
		})

		added, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.Contains(t, added, ".dagger/dagger-module.toml")
		require.Contains(t, added, ".dagger/main.go")

		config, err := changes.After().File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `path = ".dagger"`)
	})
}

func (WorkspaceModuleInitSuite) TestClientInitFromSubdirectory(ctx context.Context, t *testctx.T) {
	t.Run("relative client path and local module resolve from cwd", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		subdir := filepath.Join(workdir, "subdir")
		require.NoError(t, os.MkdirAll(subdir, 0o755))
		initGitRepo(ctx, t, workdir)
		copyTestdataFixture(ctx, t, filepath.Join(subdir, "mymod"), "modules", "go", "minimal-dep")
		require.NoError(t, os.WriteFile(filepath.Join(subdir, "dagger.toml"), []byte(goSDKWorkspaceConfig), 0o644))

		c := connect(ctx, t, dagger.WithWorkdir(subdir))

		changes := c.CurrentWorkspace().ClientInit("lib/client", "go", "./mymod")

		modified, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.Contains(t, modified, "subdir/dagger.toml")

		config, err := changes.After().File("subdir/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `path = "lib/client"`)
		require.Contains(t, config, `module = "mymod"`)
	})

	t.Run("clientGenerate materializes clients relative to the config directory", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		subdir := filepath.Join(workdir, "subdir")
		require.NoError(t, os.MkdirAll(subdir, 0o755))
		initGitRepo(ctx, t, workdir)
		copyTestdataFixture(ctx, t, filepath.Join(subdir, "mymod"), "modules", "go", "minimal-dep")
		// The builtin go SDK implements generateClient; module-based SDKs
		// don't yet, so generation dispatches on a builtin source ref. The
		// client lives inside the module directory because the go generator
		// rejects output paths above the module root.
		require.NoError(t, os.WriteFile(filepath.Join(subdir, "dagger.toml"), []byte(`[modules.go-sdk]
source = "go"

[modules.go-sdk.as-sdk]
name = "go"

[[modules.go-sdk.as-sdk.clients]]
path = "mymod/client"
module = "mymod"
`), 0o644))

		c := connect(ctx, t, dagger.WithWorkdir(subdir))

		changes := c.CurrentWorkspace().ClientGenerate()

		added, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, added)
		for _, p := range added {
			require.Truef(t, strings.HasPrefix(p, "subdir/mymod/"),
				"generated client path %q should live under subdir/mymod/", p)
		}
	})
}
