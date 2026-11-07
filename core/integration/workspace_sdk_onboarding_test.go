package core

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/dagger/dagger/core/sdk/sdkmeta"
	workspacecfg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/google/shlex"
	"github.com/stretchr/testify/require"
)

// Follow the actual guidance, without adding CLI flags to either printed
// command. The init command gets a terminal so the user can apply its changes.
func (WorkspaceSuite) TestSDKOnboardingCLI(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	before := sdkOnboardingFiles(t, workdir)
	out, err := hostDaggerExecRaw(ctx, t, workdir, "module", "init", "--help")
	require.NoError(t, err)
	require.Contains(t, string(out), "No SDK is installed in this workspace")
	require.Contains(t, string(out), "OFFICIAL SDKs AVAILABLE TO INSTALL")
	registry, err := sdkmeta.LoadRegistry()
	require.NoError(t, err)
	for _, sdk := range registry {
		require.Contains(t, string(out), sdk.Repo)
	}
	out, err = hostDaggerExecRaw(ctx, t, workdir, "module", "init", "dang")
	require.Error(t, err)
	require.Contains(t, string(out), "is not installed in this workspace")
	require.Contains(t, string(out), "--install-sdk")
	require.Equal(t, before, sdkOnboardingFiles(t, workdir))
	var commands [][]string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "dagger module ") && !strings.Contains(line, "--install-sdk") {
			args, err := shlex.Split(line)
			require.NoError(t, err)
			commands = append(commands, args[1:])
		}
	}
	require.Len(t, commands, 2, string(out))
	installed, err := hostDaggerExecRaw(ctx, t, workdir, commands[0]...)
	require.NoError(t, err, string(installed))

	console, err := newTUIConsole(t, 5*time.Minute)
	require.NoError(t, err)
	tty := console.Tty()
	require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 40, Cols: 120}))
	cmd := hostDaggerCommandRaw(ctx, t, workdir, commands[1]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	require.NoError(t, cmd.Start())
	_, err = console.ExpectString("Apply changes?")
	require.NoError(t, err)
	_, err = console.Send("y\r")
	require.NoError(t, err)
	go console.ExpectEOF()
	require.NoError(t, cmd.Wait())
	sdkOnboardingModule(t, workdir, "dang", true)
	// The same opt-in on an installed name initializes another module, without
	// registering or installing the SDK again.
	out, err = hostDaggerExecRaw(ctx, t, workdir, "module", "init", "dang", "--install-sdk", "--name=second", "-y")
	require.NoError(t, err, string(out))
	cfg := readInstalledWorkspaceConfig(t, workdir)
	require.Len(t, cfg.SDKs, 1)
	require.Len(t, cfg.Modules, 3, "one SDK provider and two initialized modules")
	require.Contains(t, cfg.Modules, "second")
}

func (WorkspaceSuite) TestSDKInstallAndInitCLI(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name, config, selected, registered string
		existingLock                       bool
	}{
		{name: "fresh", selected: "dang", registered: "dang"},
		{name: "existing lock", selected: "dang", registered: "dang", existingLock: true},
		{
			name: "conventional name taken", selected: "golang", registered: "go-2",
			config: "[modules.other]\nsource = 'github.com/acme/unfetched-sdk'\n\n[sdks.go]\nmodule = 'other'\n",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			workdir := filepath.Join(t.TempDir(), "onboarding")
			require.NoError(t, os.Mkdir(workdir, 0o755))
			initGitRepo(ctx, t, workdir)
			if tc.config != "" {
				writeWorkspaceConfigFile(t, workdir, tc.config)
			}
			if tc.existingLock {
				lock := workspacecfg.NewLock()
				require.NoError(t, lock.SetLookup("fixture", "keep", []any{"unrelated"}, "original"))
				data, err := lock.Marshal()
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.lock"), data, 0o644))
			}
			before := sdkOnboardingFiles(t, workdir)
			guidance, err := hostDaggerExecRaw(ctx, t, workdir, "module", "init", tc.selected)
			require.Error(t, err)
			require.Contains(t, string(guidance), "dagger module init "+tc.registered)
			out, err := hostDaggerExecRaw(ctx, t, workdir, "module", "init", tc.selected, "--install-sdk", "--no-apply")
			require.NoError(t, err, string(out))
			require.Contains(t, string(out), "dagger.toml")
			require.Contains(t, string(out), "dagger.lock")
			require.Contains(t, string(out), "dagger-module.toml")
			require.Contains(t, string(out), "Generated changes were not applied")
			require.Equal(t, before, sdkOnboardingFiles(t, workdir))

			out, err = hostDaggerExecRaw(ctx, t, workdir, "module", "init", tc.selected, "--install-sdk", "-y")
			require.NoError(t, err, string(out))
			sdkOnboardingModule(t, workdir, tc.registered, true)
			lockData, err := os.ReadFile(filepath.Join(workdir, "dagger.lock"))
			require.NoError(t, err)
			lock, err := workspacecfg.ParseLock(lockData)
			require.NoError(t, err)
			if tc.existingLock {
				value, found := lock.GetLookup("fixture", "keep", []any{"unrelated"})
				require.True(t, found)
				require.Equal(t, "original", value)
			}
			cfg := readInstalledWorkspaceConfig(t, workdir)
			provider := cfg.SDKs[tc.registered].Module
			require.Contains(t, string(lockData), cfg.Modules[provider].Source)
			require.Contains(t, string(guidance), "--name "+provider)
			out, err = hostDaggerExecRaw(ctx, t, workdir, "sdk", "list")
			require.NoError(t, err)
			found := false
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 3 && fields[0] == tc.registered && fields[2] == "installed" {
					found = true
				}
			}
			require.True(t, found, string(out))
		})
	}
}

func (WorkspaceSuite) TestSDKInitErrorsDoNotWriteCLI(ctx context.Context, t *testctx.T) {
	for _, config := range []string{"", "# keep this config\n"} {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)
		if config != "" {
			writeWorkspaceConfigFile(t, workdir, config)
		}
		before := sdkOnboardingFiles(t, workdir)
		for _, name := range []string{"missing-sdk", "github.com/dagger/dang-sdk", "github.com/acme/sdk@v1", "dang"} {
			out, err := hostDaggerExecRaw(ctx, t, workdir, "module", "init", name, "-y")
			require.Error(t, err)
			require.Equal(t, before, sdkOnboardingFiles(t, workdir))
			if name == "dang" {
				continue
			}
			optIn, err := hostDaggerExecRaw(ctx, t, workdir, "module", "init", name, "--install-sdk", "-y")
			require.Error(t, err)
			require.Contains(t, string(optIn), strings.TrimSpace(string(out)))
			require.Equal(t, before, sdkOnboardingFiles(t, workdir))
		}
	}
}

func (WorkspaceSuite) TestSDKInitInstalledPrecedenceCLI(ctx context.Context, t *testctx.T) {
	t.Run("renamed official Go", func(ctx context.Context, t *testctx.T) {
		workdir := filepath.Join(t.TempDir(), "onboarding")
		require.NoError(t, os.Mkdir(workdir, 0o755))
		initGitRepo(ctx, t, workdir)
		writeWorkspaceConfigFile(t, workdir, "[modules.provider]\nsource = 'github.com/dagger/go-sdk'\n\n[sdks.my-go]\nmodule = 'provider'\n")
		out, err := hostDaggerExecRaw(ctx, t, workdir, "module", "init", "go", "--install-sdk", "-y")
		require.NoError(t, err, string(out))
		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Len(t, cfg.SDKs, 1)
		require.Equal(t, "provider", cfg.SDKs["my-go"].Module)
		sdkOnboardingModule(t, workdir, "my-go", true)
	})
	t.Run("installed third party keeps settings under an agent", func(ctx context.Context, t *testctx.T) {
		for _, optIn := range []bool{false, true} {
			workdir := t.TempDir()
			initGitRepo(ctx, t, workdir)
			require.NoError(t, os.CopyFS(filepath.Join(workdir, "sdk"), os.DirFS("testdata/sdks/module-max-lifecycle")))
			writeWorkspaceConfigFile(t, workdir, "[modules.provider]\nsource = 'sdk'\n\n[sdks.go]\nmodule = 'provider'\n")
			args := []string{"module", "init", "go", "--name=demo", "--path=demo", "--starter=empty", "-y"}
			if optIn {
				args = append(args, "--install-sdk")
			}
			cmd := hostDaggerCommandRaw(ctx, t, workdir, args...)
			cmd.Env = append(cmd.Env, "AI_AGENT=test")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			cfg := readInstalledWorkspaceConfig(t, workdir)
			require.Len(t, cfg.SDKs, 1)
			require.Len(t, cfg.Modules, 1, "custom path does not install the new module")
			require.Equal(t, "empty", cfg.SDKs["go"].Scopes["demo"].Settings["starter"])
			main, err := os.ReadFile(filepath.Join(workdir, "demo", "main.go"))
			require.NoError(t, err)
			require.NotContains(t, string(main), "func (m *Module) Hello()")
		}
	})
}

func sdkOnboardingFiles(t *testctx.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		files[rel] = string(data)
		return err
	}))
	return files
}

func sdkOnboardingModule(t *testctx.T, root, sdk string, main bool) {
	t.Helper()
	cfg := readInstalledWorkspaceConfig(t, root)
	entry, ok := cfg.SDKs[sdk]
	require.True(t, ok)
	var scopes []string
	for path, scope := range entry.Scopes {
		if scope.IsModule {
			scopes = append(scopes, path)
		}
	}
	require.Len(t, scopes, 1)
	require.FileExists(t, filepath.Join(root, scopes[0], "dagger-module.toml"))
	// Other SDK providers in collision fixtures are not initialized modules.
	var initialized []workspacecfg.ModuleEntry
	for _, module := range cfg.Modules {
		if module.Source == scopes[0] {
			initialized = append(initialized, module)
		}
	}
	require.Len(t, initialized, 1)
	require.Equal(t, main, initialized[0].Main)
}
