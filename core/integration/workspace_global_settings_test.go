package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Global module settings: configuration-only entries in dagger.toml — a
// `modules` entry keyed by a module source ref, with no source field —
// inject their settings as constructor defaults into any load of that
// source in the session, at any depth of the dependency tree.

func (WorkspaceSuite) TestGlobalModuleSettings(ctx context.Context, t *testctx.T) {
	t.Run("configures a dependency of an installed module", func(ctx context.Context, t *testctx.T) {
		workdir := newGlobalSettingsWorkdir(ctx, t, `[modules.app]
source = "./app"

[modules."./lib".settings]
greeting = "configured"
`)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "app", "greet")
		require.NoError(t, err)
		require.Equal(t, "configured", strings.TrimSpace(string(out)))
	})

	t.Run("explicit caller argument wins", func(ctx context.Context, t *testctx.T) {
		workdir := newGlobalSettingsWorkdir(ctx, t, `[modules.app]
source = "./app"

[modules."./lib".settings]
greeting = "configured"
`)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "app", "greet-explicit")
		require.NoError(t, err)
		require.Equal(t, "explicit", strings.TrimSpace(string(out)))
	})

	t.Run("reaches dependencies at any depth", func(ctx context.Context, t *testctx.T) {
		workdir := newGlobalSettingsWorkdir(ctx, t, `[modules.deep-app]
source = "./deep-app"

[modules."./lib".settings]
greeting = "configured"
`)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "deep-app", "deep-greet")
		require.NoError(t, err)
		require.Equal(t, "configured", strings.TrimSpace(string(out)))
	})

	t.Run("installed instance settings win, the rest still applies", func(ctx context.Context, t *testctx.T) {
		workdir := newGlobalSettingsWorkdir(ctx, t, `[modules.lib]
source = "./lib"

[modules.lib.settings]
greeting = "instance"

[modules."./lib".settings]
greeting = "configured"
suffix = "!"
`)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "lib", "message")
		require.NoError(t, err)
		require.Equal(t, "instance!", strings.TrimSpace(string(out)))
	})

	t.Run("applies to modules loaded with -m", func(ctx context.Context, t *testctx.T) {
		workdir := newGlobalSettingsWorkdir(ctx, t, `[modules."./lib".settings]
greeting = "configured"

[env.ci.modules."./lib".settings]
greeting = "from-ci"
`)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "-m", "./lib", "call", "message")
		require.NoError(t, err)
		require.Equal(t, "configured", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "-m", "./lib", "call", "--help")
		require.NoError(t, err)
		require.Regexp(t, `--greeting string *\(default "configured"\)`, string(out))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "-m", "./lib", "call", "message")
		require.NoError(t, err)
		require.Equal(t, "from-ci", strings.TrimSpace(string(out)))
	})

	t.Run("env overlays apply to configuration-only entries", func(ctx context.Context, t *testctx.T) {
		workdir := newGlobalSettingsWorkdir(ctx, t, `[modules.app]
source = "./app"

[modules."./lib".settings]
greeting = "configured"

[env.ci.modules."./lib".settings]
greeting = "from-ci"
`)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "app", "greet")
		require.NoError(t, err)
		require.Equal(t, "configured", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "call", "app", "greet")
		require.NoError(t, err)
		require.Equal(t, "from-ci", strings.TrimSpace(string(out)))
	})

	t.Run("rejects a bare name without source", func(ctx context.Context, t *testctx.T) {
		workdir := newGlobalSettingsWorkdir(ctx, t, `[modules.app]
source = "./app"

[modules.lib.settings]
greeting = "configured"
`)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "app", "greet")
		requireErrOut(t, err, `workspace module "lib" has no source`)
	})

	t.Run("rejects non-settings fields on configuration-only entries", func(ctx context.Context, t *testctx.T) {
		workdir := newGlobalSettingsWorkdir(ctx, t, `[modules.app]
source = "./app"

[modules."./lib"]
entrypoint = true

[modules."./lib".settings]
greeting = "configured"
`)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "app", "greet")
		requireErrOut(t, err, `configuration-only module entry "./lib" must only carry settings`)
	})
}

// newGlobalSettingsWorkdir scaffolds a workspace with the given dagger.toml
// and a small module tree: app depends on lib, deep-app depends on mid which
// depends on lib. lib's constructor args are optional so callers leave them
// unset and settings can inject them.
func newGlobalSettingsWorkdir(ctx context.Context, t *testctx.T, configTOML string) string {
	t.Helper()

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	writeGlobalSettingsFiles(t, workdir, map[string]string{
		"lib/dagger.json": `{"name":"lib","engineVersion":"latest","sdk":{"source":"go"}}`,
		"lib/main.go": `package main

type Lib struct {
	GreetingValue string
	SuffixValue   string
}

func New(
	// +optional
	greeting string,
	// +optional
	suffix string,
) *Lib {
	return &Lib{GreetingValue: greeting, SuffixValue: suffix}
}

func (m *Lib) Message() string {
	return m.GreetingValue + m.SuffixValue
}
`,
		"app/dagger.json": `{"name":"app","engineVersion":"latest","sdk":{"source":"go"},"dependencies":[{"name":"lib","source":"../lib"}]}`,
		"app/main.go": `package main

import (
	"context"

	"dagger/app/internal/dagger"
)

type App struct{}

func (m *App) Greet(ctx context.Context) (string, error) {
	return dag.Lib().Message(ctx)
}

func (m *App) GreetExplicit(ctx context.Context) (string, error) {
	return dag.Lib(dagger.LibOpts{Greeting: "explicit"}).Message(ctx)
}
`,
		"mid/dagger.json": `{"name":"mid","engineVersion":"latest","sdk":{"source":"go"},"dependencies":[{"name":"lib","source":"../lib"}]}`,
		"mid/main.go": `package main

import "context"

type Mid struct{}

func (m *Mid) Relay(ctx context.Context) (string, error) {
	return dag.Lib().Message(ctx)
}
`,
		"deep-app/dagger.json": `{"name":"deep-app","engineVersion":"latest","sdk":{"source":"go"},"dependencies":[{"name":"mid","source":"../mid"}]}`,
		"deep-app/main.go": `package main

import "context"

type DeepApp struct{}

func (m *DeepApp) DeepGreet(ctx context.Context) (string, error) {
	return dag.Mid().Relay(ctx)
}
`,
	})
	writeWorkspaceConfigFile(t, workdir, configTOML)
	return workdir
}

func writeGlobalSettingsFiles(t *testctx.T, workdir string, files map[string]string) {
	t.Helper()
	for path, contents := range files {
		fullPath := filepath.Join(workdir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
		require.NoError(t, os.WriteFile(fullPath, []byte(contents), 0o644))
	}
}
