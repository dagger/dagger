package core

// These tests cover explicit workspace selection with `--workspace` or `-W`.
// They verify local and remote refs, `--workdir`, command policy, metadata-only
// commands, and explicit environment overlays.
//
// See also:
// - contextual_workspace_test.go: implicit find-up from the current directory.
// - workspace_compat_test.go: legacy compat workspace inference.
// - module_loading_test.go: module loading after the workspace is chosen.

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

// WorkspaceSelectionSuite owns the explicit workspace-selection contract:
// how a declared workspace is chosen, which commands accept it, and how that
// binding propagates through the session once selected.
type WorkspaceSelectionSuite struct{}

func TestWorkspaceSelection(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSelectionSuite{})
}

func workspaceSelectionDaggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func workspaceSelectionDaggerExecFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func workspaceSelectionDaggerCall(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func workspaceSelectionDaggerCallFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func workspaceSelectionDaggerQuery(query string, args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "query"}, args...), dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func workspaceSelectionDaggerQueryFail(query string, args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "query"}, args...), dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func workspaceSelectionDangSource(typeName, fnName, result string) string {
	return `
type ` + typeName + ` {
  pub ` + fnName + `: String! {
    "` + result + `"
  }
}
`
}

func workspaceSelectionSimpleWorkspace(dir, name, typeName, result string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		moduleDir := dir + "/.dagger/modules/" + name
		return ctr.
			WithNewFile(dir+"/dagger.toml", `[modules.`+name+`]
source = ".dagger/modules/`+name+`"
entrypoint = true
`).
			WithNewFile(moduleDir+"/dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
			WithNewFile(moduleDir+"/main.dang", workspaceSelectionDangSource(typeName, "identify", result))
	}
}

func workspaceSelectionSimpleWorkspaceDir(c *dagger.Client, name, typeName, result string) *dagger.Directory {
	moduleDir := ".dagger/modules/" + name
	return c.Directory().
		WithNewFile("dagger.toml", `[modules.`+name+`]
source = ".dagger/modules/`+name+`"
entrypoint = true
`).
		WithNewFile(moduleDir+"/dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
		WithNewFile(moduleDir+"/main.dang", workspaceSelectionDangSource(typeName, "identify", result))
}

func workspaceSelectionEnvWorkspace(dir, base, ci string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		moduleDir := dir + "/.dagger/modules/greeter"
		return ctr.
			WithNewFile(dir+"/dagger.toml", `[modules.greeter]
source = ".dagger/modules/greeter"
entrypoint = true

[modules.greeter.settings]
greeting = "`+base+`"

[env.ci.modules.greeter.settings]
greeting = "`+ci+`"
`).
			WithNewFile(moduleDir+"/dagger.json", `{"name":"greeter","sdk":{"source":"dang"}}`).
			WithNewFile(moduleDir+"/main.dang", `
type Greeter {
  pub greeting: String!

  new(greeting: String! = "default") {
    self.greeting = greeting
    self
  }
}
`)
	}
}

const workspaceSelectionFilesConfig = `[modules.files]
source = ".dagger/modules/files"
entrypoint = true
`

const workspaceSelectionFilesModuleSource = `package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger/files/internal/dagger"
)

type Files struct{}

func (m *Files) ReadWorkspaceArg(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	return workspace.File("marker.txt").Contents(ctx)
}

func (m *Files) ReadCurrentWorkspace(ctx context.Context) (string, error) {
	return dag.CurrentWorkspace().File("marker.txt").Contents(ctx)
}

func (m *Files) ChangeWorkspaceArg(workspace *dagger.Workspace) *dagger.Changeset {
	before := workspace.Directory(".")
	after := before.WithNewFile("workspace-arg.txt", "changed through workspace arg")
	return after.Changes(before)
}

func (m *Files) ChangeCurrentWorkspace() *dagger.Changeset {
	before := dag.CurrentWorkspace().Directory(".")
	after := before.WithNewFile("current-workspace.txt", "changed through current workspace")
	return after.Changes(before)
}

func (m *Files) ChangeStandalone() *dagger.Changeset {
	before := dag.Directory()
	after := before.WithNewFile("standalone.txt", "changed without workspace")
	return after.Changes(before)
}

func (m *Files) ReturnedDirectory() *dagger.Directory {
	return dag.Directory().WithNewFile("returned-dir.txt", "returned directory")
}

func (m *Files) ReturnedFile() *dagger.File {
	return dag.Directory().WithNewFile("returned-file.txt", "returned file").File("returned-file.txt")
}

func (m *Files) ReturnedContainer() *dagger.Container {
	return dag.Container().
		From("` + alpineImage + `").
		WithExec([]string{"sh", "-c", "printf 'returned container' > /returned-container.txt"})
}

// ExportFromModule checks that Directory.Export writes to the module runtime by
// reading the exported file from the module process after the export completes.
func (m *Files) ExportFromModule(ctx context.Context) (string, error) {
	const (
		dest     = "runtime-export"
		filename = "from-module.txt"
		contents = "exported from module runtime"
	)

	_, err := dag.Directory().WithNewFile(filename, contents).Export(ctx, dest)
	if err != nil {
		return "", err
	}

	out, err := os.ReadFile(filepath.Join(dest, filename))
	if err != nil {
		return "", fmt.Errorf("exported directory was not readable from the module runtime: %w", err)
	}
	return string(out), nil
}
`

func workspaceSelectionRemoteRef(ctx context.Context, t *testctx.T, c *dagger.Client, content *dagger.Directory) string {
	t.Helper()

	gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", makeGitDir(c, content, "main"), "", nil)
	gitSrv, err := gitSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })

	shortHost, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)

	getentOut, err := c.Container().From(alpineImage).
		WithExec([]string{"getent", "hosts", shortHost}).
		Stdout(ctx)
	require.NoError(t, err, "could not resolve git service hostname %q", shortHost)

	fields := strings.Fields(getentOut)
	require.NotEmpty(t, fields, "unexpected getent output: %q", getentOut)
	return "http://" + fields[0] + "/repo.git@main"
}

// TestDeclaredWorkspaceSelection should pin down the main user-visible
// selection contract for --workspace/-W before any compat or ambient find-up
// behavior is involved.
func (WorkspaceSelectionSuite) TestDeclaredWorkspaceSelection(ctx context.Context, t *testctx.T) {
	t.Run("local -W selects that workspace instead of cwd inference", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/caller", "caller", "Caller", "caller workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/selected", "selected", "Selected", "selected workspace")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected workspace", strings.TrimSpace(out))

		out, err = ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{cwd configFile}}`, "-W", "../selected")).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"cwd":"selected","configFile":"selected/dagger.toml"}}`, out)
	})

	t.Run("remote -W selects a git workspace without relying on host cwd", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, workspaceSelectionSimpleWorkspaceDir(c, "remote", "Remote", "remote workspace"))

		ctr := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", remoteRef, "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "remote workspace", strings.TrimSpace(out))

		out, err = ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{address cwd configFile}}`, "-W", remoteRef)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"address":"`+remoteRef+`","cwd":".","configFile":"dagger.toml"}}`, out)
	})

	t.Run("relative -W is resolved after --workdir changes cwd", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/shell/ws", "selected", "Selected", "post-workdir workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/ws", "wrong", "Wrong", "original cwd workspace")).
			WithWorkdir("/work")

		out, err := ctr.With(workspaceSelectionDaggerCall("--workdir", "/work/shell", "-W", "./ws", "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "post-workdir workspace", strings.TrimSpace(out))

		out, err = ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{cwd configFile}}`, "--workdir", "/work/shell", "-W", "./ws")).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"cwd":"shell/ws","configFile":"shell/ws/dagger.toml"}}`, out)
	})

	t.Run("declared workspace wins over ambient workspace and cwd dagger.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/caller", "caller", "Caller", "ambient workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/selected", "selected", "Selected", "declared workspace")).
			WithNewFile("/work/caller/nested/dagger.json", `{"name":"nested","sdk":{"source":"dang"}}`).
			WithNewFile("/work/caller/nested/main.dang", workspaceSelectionDangSource("Nested", "identify", "cwd dagger.json")).
			WithWorkdir("/work/caller/nested")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../../selected", "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "declared workspace", strings.TrimSpace(out))

		out, err = ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{cwd configFile}}`, "-W", "../../selected")).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"cwd":"selected","configFile":"selected/dagger.toml"}}`, out)
	})
}

// TestWorkspaceSelectionCommandPolicy should pin down which commands accept
// --workspace and where local-only restrictions are enforced.
func (WorkspaceSelectionSuite) TestWorkspaceSelectionCommandPolicy(ctx context.Context, t *testctx.T) {
	t.Run("migrate rejects -W in integration", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c)

		out, err := ctr.With(workspaceSelectionDaggerExecFail("-W", ".", "migrate")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `--workspace is not supported for "dagger migrate"`)
	})

	t.Run("local-only workspace mutations accept a local selected workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "/work/caller", "/work/selected"}).
			WithWorkdir("/work/caller").
			With(workspaceSelectionDaggerExec("-W", "../selected", "workspace", "init", "--here"))

		_, err := ctr.WithExec([]string{"test", "-f", "/work/selected/dagger.toml"}).Sync(ctx)
		require.NoError(t, err)
		_, err = ctr.WithExec([]string{"test", "!", "-e", "/work/caller/dagger.toml"}).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("local-only workspace mutations reject a remote selected workspace at execution time", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, workspaceSelectionSimpleWorkspaceDir(c, "remote", "Remote", "remote workspace"))

		out, err := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty").
			With(workspaceSelectionDaggerQueryFail(`{currentWorkspace{init}}`, "-W", remoteRef)).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "workspace init is local-only")
		require.NotContains(t, out, "--workspace must be a local path")
	})
}

// TestSelectedWorkspaceMetadataQueries covers selected workspace metadata
// without loading workspace modules.
func (WorkspaceSelectionSuite) TestSelectedWorkspaceMetadataQueries(ctx context.Context, t *testctx.T) {
	t.Run("current workspace query reports the selected local workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/caller", "caller", "Caller", "caller workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/selected", "selected", "Selected", "selected workspace")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{address cwd configFile}}`, "-W", "../selected")).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"address":"file:///work/selected","cwd":"selected","configFile":"selected/dagger.toml"}}`, out)
	})

	t.Run("current workspace query reports the selected remote workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, workspaceSelectionSimpleWorkspaceDir(c, "remote", "Remote", "remote workspace"))

		out, err := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty").
			With(workspaceSelectionDaggerQuery(`{currentWorkspace{address cwd configFile}}`, "-W", remoteRef)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"address":"`+remoteRef+`","cwd":".","configFile":"dagger.toml"}}`, out)
	})
}

// TestSelectedWorkspaceFileIO documents the host I/O boundary for -W.
// Workspace reads follow the selected workspace. Host writes are CLI writes:
// relative paths use the CLI cwd, and absolute paths use the exact host path.
func (WorkspaceSelectionSuite) TestSelectedWorkspaceFileIO(ctx context.Context, t *testctx.T) {
	writeFilesWorkspace := func(t *testctx.T, dir, marker string) {
		t.Helper()

		require.NoError(t, os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(marker), 0o644))
		writeWorkspaceConfigFile(t, dir, workspaceSelectionFilesConfig)
		moduleDir := filepath.Join(dir, ".dagger", "modules", "files")
		require.NoError(t, os.MkdirAll(moduleDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "dagger.json"), []byte(`{"name":"files","sdk":{"source":"go"}}`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(workspaceSelectionFilesModuleSource), 0o644))
	}

	newLocalFixture := func(ctx context.Context, t *testctx.T) (string, string) {
		t.Helper()

		rootDir := t.TempDir()
		initGitRepo(ctx, t, rootDir)

		callerDir := filepath.Join(rootDir, "caller")
		selectedDir := filepath.Join(rootDir, "selected")
		require.NoError(t, os.MkdirAll(callerDir, 0o755))
		require.NoError(t, os.MkdirAll(selectedDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(callerDir, "marker.txt"), []byte("caller marker"), 0o644))
		writeFilesWorkspace(t, selectedDir, "selected marker")
		return callerDir, selectedDir
	}

	newRemoteWorkspace := func(ctx context.Context, t *testctx.T) string {
		t.Helper()

		c := connect(ctx, t)
		moduleDir := ".dagger/modules/files"
		return workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
			WithNewFile("marker.txt", "remote marker").
			WithNewFile("dagger.toml", workspaceSelectionFilesConfig).
			WithNewFile(moduleDir+"/dagger.json", `{"name":"files","sdk":{"source":"go"}}`).
			WithNewFile(moduleDir+"/main.go", workspaceSelectionFilesModuleSource))
	}

	newNoWorkspaceFixture := func(ctx context.Context, t *testctx.T) (string, []string) {
		t.Helper()

		root := t.TempDir()
		workdir := filepath.Join(root, "caller")
		moduleDir := filepath.Join(root, "module")
		require.NoError(t, os.MkdirAll(workdir, 0o755))
		copyTestdataFixture(ctx, t, moduleDir, "modules", "go", "workspace-selection-files-standalone")

		_, err := os.Stat(filepath.Join(root, ".git"))
		require.ErrorIs(t, err, os.ErrNotExist)
		_, err = os.Stat(filepath.Join(workdir, "dagger.json"))
		require.ErrorIs(t, err, os.ErrNotExist)
		return workdir, []string{"-m", "../module"}
	}

	newBareGitWorkspaceFixture := func(ctx context.Context, t *testctx.T) (string, []string) {
		t.Helper()

		root := t.TempDir()
		initGitRepo(ctx, t, root)

		workdir := filepath.Join(root, "caller")
		selectedDir := filepath.Join(root, "selected")
		moduleDir := filepath.Join(root, "module")
		require.NoError(t, os.MkdirAll(workdir, 0o755))
		require.NoError(t, os.MkdirAll(selectedDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(selectedDir, "marker.txt"), []byte("selected marker"), 0o644))
		copyTestdataFixture(ctx, t, moduleDir, "modules", "go", "workspace-selection-files-standalone")

		_, err := os.Stat(filepath.Join(root, "dagger.toml"))
		require.ErrorIs(t, err, os.ErrNotExist)
		return workdir, []string{"-W", "../selected", "-m", "../module"}
	}

	requireFileContents := func(t *testctx.T, path, contents string) {
		t.Helper()

		got, err := os.ReadFile(path)
		require.NoError(t, err, path)
		require.Equal(t, contents, string(got), path)
	}

	requireNoPath := func(t *testctx.T, path string) {
		t.Helper()

		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist, path)
	}

	requireNonEmptyFile := func(t *testctx.T, path string) {
		t.Helper()

		info, err := os.Stat(path)
		require.NoError(t, err, path)
		require.Greater(t, info.Size(), int64(0), path)
	}

	daggerCallArgs := func(selection []string, autoApply bool, callArgs ...string) []string {
		args := []string{"--silent"}
		if autoApply {
			args = append(args, "-y")
		}
		args = append(args, "call")
		args = append(args, selection...)
		return append(args, callArgs...)
	}

	assertWorkspaceReads := func(ctx context.Context, t *testctx.T, workdir string, selection []string, want string) {
		t.Helper()

		out, err := hostDaggerExec(ctx, t, workdir, daggerCallArgs(selection, false, "read-workspace-arg")...)
		require.NoError(t, err)
		require.Equal(t, want, strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, daggerCallArgs(selection, false, "read-current-workspace")...)
		require.NoError(t, err)
		require.Equal(t, want, strings.TrimSpace(string(out)))
	}

	assertNoWorkspaceReads := func(ctx context.Context, t *testctx.T, workdir string, selection []string) {
		t.Helper()

		out, err := hostDaggerExec(ctx, t, workdir, daggerCallArgs(selection, false, "read-workspace-arg")...)
		require.Error(t, err)
		require.Contains(t, strings.ToLower(string(out)+err.Error()), "workspace")

		out, err = hostDaggerExec(ctx, t, workdir, daggerCallArgs(selection, false, "read-current-workspace")...)
		require.Error(t, err)
		require.Contains(t, strings.ToLower(string(out)+err.Error()), "workspace")
	}

	assertWorkspaceChangesetsUseCWD := func(ctx context.Context, t *testctx.T, workdir string, selection []string, noWriteDir string) {
		t.Helper()

		writes := []struct {
			name     string
			args     []string
			hostFile string
			want     string
		}{
			{
				name:     "Changeset from Workspace argument",
				args:     []string{"change-workspace-arg"},
				hostFile: "workspace-arg.txt",
				want:     "changed through workspace arg",
			},
			{
				name:     "Changeset from dag.CurrentWorkspace",
				args:     []string{"change-current-workspace"},
				hostFile: "current-workspace.txt",
				want:     "changed through current workspace",
			},
		}

		for _, write := range writes {
			_, err := hostDaggerExec(ctx, t, workdir, daggerCallArgs(selection, true, write.args...)...)
			require.NoError(t, err, write.name)
			requireFileContents(t, filepath.Join(workdir, write.hostFile), write.want)
			if noWriteDir != "" {
				requireNoPath(t, filepath.Join(noWriteDir, write.hostFile))
			}
		}
	}

	assertHostWritesUseCWD := func(ctx context.Context, t *testctx.T, workdir string, selection []string, noWriteDir string) {
		t.Helper()

		writes := []struct {
			name       string
			args       []string
			autoApply  bool
			exportPath string
			hostFile   string
			want       string
		}{
			{
				name:      "standalone Changeset",
				autoApply: true,
				args:      []string{"change-standalone"},
				hostFile:  "standalone.txt",
				want:      "changed without workspace",
			},
			{
				name:       "returned Directory export",
				args:       []string{"returned-directory", "export", "--path"},
				exportPath: "./returned-directory",
				hostFile:   filepath.Join("returned-directory", "returned-dir.txt"),
				want:       "returned directory",
			},
			{
				name:       "returned File export",
				args:       []string{"returned-file", "export", "--path"},
				exportPath: "./returned-file.txt",
				hostFile:   "returned-file.txt",
				want:       "returned file",
			},
			{
				name:       "returned Container rootfs Directory export",
				args:       []string{"returned-container", "rootfs", "export", "--path"},
				exportPath: "./returned-container-rootfs",
				hostFile:   filepath.Join("returned-container-rootfs", "returned-container.txt"),
				want:       "returned container",
			},
		}

		for _, write := range writes {
			callArgs := append([]string{}, write.args...)
			if write.exportPath != "" {
				callArgs = append(callArgs, write.exportPath)
			}

			_, err := hostDaggerExec(ctx, t, workdir, daggerCallArgs(selection, write.autoApply, callArgs...)...)
			require.NoError(t, err, write.name)
			requireFileContents(t, filepath.Join(workdir, write.hostFile), write.want)
			if noWriteDir != "" {
				requireNoPath(t, filepath.Join(noWriteDir, write.hostFile))
			}
		}
	}

	assertAbsoluteExportsUseExplicitPath := func(ctx context.Context, t *testctx.T, workdir string, selection []string, noWriteDir string) {
		t.Helper()

		exports := []struct {
			name        string
			args        []string
			destRelPath string
			hostFile    string
			want        string
			nonEmpty    bool
		}{
			{
				name:        "Changeset export",
				args:        []string{"change-standalone", "export", "--path"},
				destRelPath: "absolute-changeset",
				hostFile:    filepath.Join("absolute-changeset", "standalone.txt"),
				want:        "changed without workspace",
			},
			{
				name:        "Directory export",
				args:        []string{"returned-directory", "export", "--path"},
				destRelPath: "absolute-directory",
				hostFile:    filepath.Join("absolute-directory", "returned-dir.txt"),
				want:        "returned directory",
			},
			{
				name:        "File export",
				args:        []string{"returned-file", "export", "--path"},
				destRelPath: "absolute-file.txt",
				hostFile:    "absolute-file.txt",
				want:        "returned file",
			},
			{
				name:        "Container rootfs Directory export",
				args:        []string{"returned-container", "rootfs", "export", "--path"},
				destRelPath: "absolute-container-rootfs",
				hostFile:    filepath.Join("absolute-container-rootfs", "returned-container.txt"),
				want:        "returned container",
			},
			{
				name:        "Container export",
				args:        []string{"returned-container", "export", "--path"},
				destRelPath: "absolute-container.tar",
				hostFile:    "absolute-container.tar",
				nonEmpty:    true,
			},
		}

		for _, export := range exports {
			absoluteDestPath := filepath.Join(workdir, export.destRelPath)
			require.True(t, filepath.IsAbs(absoluteDestPath), absoluteDestPath)

			callArgs := append([]string{}, export.args...)
			callArgs = append(callArgs, absoluteDestPath)
			_, err := hostDaggerExec(ctx, t, workdir, daggerCallArgs(selection, false, callArgs...)...)
			require.NoError(t, err, export.name)
			if export.nonEmpty {
				requireNonEmptyFile(t, filepath.Join(workdir, export.hostFile))
			} else {
				requireFileContents(t, filepath.Join(workdir, export.hostFile), export.want)
			}
			if noWriteDir != "" {
				requireNoPath(t, filepath.Join(noWriteDir, export.hostFile))
			}
		}
	}

	assertModuleRuntimeExportDoesNotWriteHost := func(ctx context.Context, t *testctx.T, workdir string, selection []string, noWriteDirs ...string) {
		t.Helper()

		// The module must read its own export, while host directories stay clean;
		// this catches nested clients attaching exports to the wrong session.
		out, err := hostDaggerExec(ctx, t, workdir, daggerCallArgs(selection, false, "export-from-module")...)
		require.NoError(t, err)
		require.Equal(t, "exported from module runtime", strings.TrimSpace(string(out)))

		for _, noWriteDir := range noWriteDirs {
			requireNoPath(t, filepath.Join(noWriteDir, "runtime-export", "from-module.txt"))
		}
	}

	t.Run("local selected workspace is also the CLI cwd", func(ctx context.Context, t *testctx.T) {
		callerDir, selectedDir := newLocalFixture(ctx, t)
		selection := []string{"-W", "."}

		assertWorkspaceReads(ctx, t, selectedDir, selection, "selected marker")
		assertWorkspaceChangesetsUseCWD(ctx, t, selectedDir, selection, callerDir)
		assertHostWritesUseCWD(ctx, t, selectedDir, selection, callerDir)
		assertAbsoluteExportsUseExplicitPath(ctx, t, selectedDir, selection, callerDir)
	})

	t.Run("local selected workspace differs from the CLI cwd", func(ctx context.Context, t *testctx.T) {
		callerDir, selectedDir := newLocalFixture(ctx, t)
		selection := []string{"-W", "../selected"}

		assertWorkspaceReads(ctx, t, callerDir, selection, "selected marker")
		assertWorkspaceChangesetsUseCWD(ctx, t, callerDir, selection, selectedDir)
		assertHostWritesUseCWD(ctx, t, callerDir, selection, selectedDir)
		assertAbsoluteExportsUseExplicitPath(ctx, t, callerDir, selection, selectedDir)
	})

	t.Run("selected workspace is remote", func(ctx context.Context, t *testctx.T) {
		remoteRef := newRemoteWorkspace(ctx, t)
		callerDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(callerDir, "marker.txt"), []byte("caller marker"), 0o644))
		selection := []string{"-W", remoteRef}

		assertWorkspaceReads(ctx, t, callerDir, selection, "remote marker")
		assertWorkspaceChangesetsUseCWD(ctx, t, callerDir, selection, "")
		assertHostWritesUseCWD(ctx, t, callerDir, selection, "")
		assertAbsoluteExportsUseExplicitPath(ctx, t, callerDir, selection, "")
	})

	t.Run("selected remote workspace without config injects workspace args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
			WithNewFile("marker.txt", "remote marker"))
		workdir, moduleSelection := newNoWorkspaceFixture(ctx, t)
		selection := append([]string{"-W", remoteRef}, moduleSelection...)

		assertWorkspaceReads(ctx, t, workdir, selection, "remote marker")
	})

	t.Run("selected local git workspace without config injects workspace args", func(ctx context.Context, t *testctx.T) {
		workdir, selection := newBareGitWorkspaceFixture(ctx, t)

		assertWorkspaceReads(ctx, t, workdir, selection, "selected marker")
	})

	t.Run("no workspace keeps host writes on the CLI cwd", func(ctx context.Context, t *testctx.T) {
		workdir, selection := newNoWorkspaceFixture(ctx, t)

		// Keep the CLI cwd free of both dagger.toml and dagger.json.
		// The sibling module is selected explicitly so this stays about host
		// I/O without creating ambient workspace context from compat fallback.
		assertNoWorkspaceReads(ctx, t, workdir, selection)
		assertHostWritesUseCWD(ctx, t, workdir, selection, "")
		assertAbsoluteExportsUseExplicitPath(ctx, t, workdir, selection, "")
	})

	// Directory.Export from module code must use the module runtime filesystem
	// even when the selected workspace and CLI cwd are different host dirs.
	t.Run("Directory.Export inside module uses the module runtime filesystem", func(ctx context.Context, t *testctx.T) {
		callerDir, selectedDir := newLocalFixture(ctx, t)
		selection := []string{"-W", "../selected"}

		assertModuleRuntimeExportDoesNotWriteHost(ctx, t, callerDir, selection, callerDir, selectedDir)
	})
}

// TestSelectedWorkspaceEnvOverlay should cover the end-to-end interaction
// between declared workspace selection and --env.
func (WorkspaceSelectionSuite) TestSelectedWorkspaceEnvOverlay(ctx context.Context, t *testctx.T) {
	t.Run("env overlay applies to the explicitly selected workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionEnvWorkspace("/work/caller", "caller-base", "caller-ci")).
			With(workspaceSelectionEnvWorkspace("/work/selected", "selected-base", "selected-ci")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "--env", "ci", "greeting")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected-ci", strings.TrimSpace(out))
	})

	t.Run("undefined env name fails against the selected workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionEnvWorkspace("/work/caller", "caller-base", "caller-ci")).
			With(workspaceSelectionEnvWorkspace("/work/selected", "selected-base", "selected-ci")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCallFail("-W", "../selected", "--env", "missing", "greeting")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `workspace env "missing" is not defined`)
	})

	t.Run("env overlay does not work for selections without native workspace config", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "/work/caller", "/work/bare"}).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCallFail("-W", "../bare", "--env", "ci", "identify")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `workspace env "ci" requires dagger.toml`)
	})
}

// TestDeclaredWorkspaceBindingPropagation should pin down how an explicit
// workspace binding survives once a session is established and other clients
// are created from it.
func (WorkspaceSelectionSuite) TestDeclaredWorkspaceBindingPropagation(ctx context.Context, t *testctx.T) {
	// TODO(#13054): Re-enable once container commands can explicitly inherit a
	// workspace. The intended contract is command-scoped inheritance, not
	// implicit inheritance from the module function that created the exec.
	t.Run("nested clients inherit the declared workspace binding", func(ctx context.Context, t *testctx.T) {
		t.Skip("TODO(#13054): waiting for command-scoped inheritWorkspace")

		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "/work/caller", "/work/selected"}).
			With(workspaceSelectionEnvWorkspace("/work/ambient", "ambient-base", "ambient-ci")).
			WithWorkdir("/work/selected").
			With(workspaceSelectionDaggerExec("workspace", "init", "--here")).
			With(withModuleFixture(t, c, "/work/selected/.dagger/modules/nester", "go/workspace-selection-nester")).
			WithNewFile("/work/selected/dagger.toml", `[modules.nester]
source = ".dagger/modules/nester"
entrypoint = true

[modules.nester.settings]
greeting = "selected-base"

[env.ci.modules.nester.settings]
greeting = "selected-ci"
`).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "nested-workspace", "--cli", testCLIBinPath)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"cwd":"selected","configFile":"selected/dagger.toml"}}`, out)
	})

	t.Run("nested clients inherit the declared workspace env overlay", func(ctx context.Context, t *testctx.T) {
		t.Skip("TODO(#13054): waiting for command-scoped inheritWorkspace")

		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "/work/caller", "/work/selected"}).
			With(workspaceSelectionEnvWorkspace("/work/ambient", "ambient-base", "ambient-ci")).
			WithWorkdir("/work/selected").
			With(workspaceSelectionDaggerExec("workspace", "init", "--here")).
			With(withModuleFixture(t, c, "/work/selected/.dagger/modules/nester", "go/workspace-selection-nester")).
			WithNewFile("/work/selected/dagger.toml", `[modules.nester]
source = ".dagger/modules/nester"
entrypoint = true

[modules.nester.settings]
greeting = "selected-base"

[env.ci.modules.nester.settings]
greeting = "selected-ci"
`).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "--env", "ci", "nested-greeting", "--cli", testCLIBinPath)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected-ci", strings.TrimSpace(out))
	})
}
