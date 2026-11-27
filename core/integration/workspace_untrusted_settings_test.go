package core

// These tests cover the boundary between settings that the host user wrote
// and settings that come from anywhere else. Object settings (secrets,
// sockets, directories, ...) resolve with the resources of the client that
// started the session, so only the configuration that the session loaded
// for that client may use them. Configuration that module code stages on a
// Workspace, and configuration inside a workspace value, must not reach the
// host, unless it loads the same module with the same settings as the
// session did.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const untrustedSettingsError = "cannot use host resources"

const untrustedSettingsConsumerModule = `name = "consumer"
engineVersion = "v1.0.0-beta.20"

[runtime]
source = "dang"
`

const untrustedSettingsConsumerSource = `type Consumer {
  let token: Secret
  let sock: Socket

  new(token: Secret = null, sock: Socket = null) {
    self.token = token
    self.sock = sock
    self
  }

  pub verify: Void @check {
    null
  }
}
`

const untrustedSettingsStagerModule = `name = "stager"
engineVersion = "v1.0.0-beta.20"

[runtime]
source = "dang"
`

const untrustedSettingsStagerSource = `type Stager {
  pub stagedSecret(ws: Workspace!): String! {
    outcome(ws.withConfigValue(key: "modules.consumer.settings.token", value: "cmd://touch /marker-staged-secret"))
  }

  pub stagedSocket(ws: Workspace!): String! {
    outcome(ws.withConfigValue(key: "modules.consumer.settings.sock", value: "unix:///run/marker.sock"))
  }

  pub stagedDotEnv(ws: Workspace!): String! {
    outcome(ws.withNewFile("consumer/.env", "TOKEN=cmd://touch /marker-staged-env\n"))
  }

  pub stagedSource(ws: Workspace!): String! {
    outcome(ws.withNewFile("consumer/main.dang", ws.file("consumer/main.dang").contents + "\n# edited\n"))
  }

  """Load name from a new workspace with the consumer's code and the given dagger.toml."""
  pub newWorkspace(ws: Workspace!, config: String!, name: String!): String! {
    outcomeOf(directory.withDirectory("consumer", ws.directory("consumer")).withNewFile("dagger.toml", config).asWorkspace, name)
  }

  let outcome(ws: Workspace!): String! {
    outcomeOf(ws, "consumer")
  }

  let outcomeOf(ws: Workspace!, name: String!): String! {
    ws.artifacts(include: [name]).asChecks
      .{{ error.{{ message }} }}
      .map { c => c.error.message ?? "passed" }
      .join("\n")
  }
}
`

// untrustedSettingsWorkspace returns a git workspace at /work with the consumer
// and stager modules installed. extraConfig is appended to dagger.toml.
func untrustedSettingsWorkspace(t *testctx.T, ctr *core.Container, extraConfig string) *core.Container {
	t.Helper()
	return ctr.
		WithNewFile("consumer/dagger-module.toml", untrustedSettingsConsumerModule).
		WithNewFile("consumer/main.dang", untrustedSettingsConsumerSource).
		WithNewFile("stager/dagger-module.toml", untrustedSettingsStagerModule).
		WithNewFile("stager/main.dang", untrustedSettingsStagerSource).
		WithNewFile("dagger.toml", `[modules.stager]
source = "stager"

[modules.consumer]
source = "consumer"
`+extraConfig)
}

func untrustedSettingsCLI(ctr *core.Container, args ...string) *core.Container {
	return ctr.WithExec(append([]string{"dagger"}, args...), core.ContainerWithExecOpts{
		Expect: core.ReturnTypeAny,
	})
}

func untrustedSettingsMarkerExists(ctx context.Context, t *testctx.T, ctr *core.Container, path string) bool {
	t.Helper()
	code, err := ctr.WithExec([]string{"test", "-e", path}, core.ContainerWithExecOpts{
		Expect: core.ReturnTypeAny,
	}).ExitCode(ctx)
	require.NoError(t, err)
	return code == 0
}

func (WorkspaceSuite) TestWorkspaceSettingsFromHostConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The dagger.toml on disk is written by the host user: its provider
	// settings resolve on the host.
	ws := untrustedSettingsWorkspace(t, workspaceBase(t, c), `
[modules.consumer.settings]
token = "cmd://touch /marker-disk"
`)
	ran := untrustedSettingsCLI(ws, "check", "--module=consumer")
	out, err := ran.CombinedOutput(ctx)
	require.NoError(t, err)
	code, err := ran.ExitCode(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, code, out)
	require.True(t, untrustedSettingsMarkerExists(ctx, t, ran, "/marker-disk"), out)
}

func (WorkspaceSuite) TestWorkspaceSettingsStagedByModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	newWorkspaceConfig := func(name, marker string) string {
		return fmt.Sprintf(`[modules.%[1]s]
source = "consumer"

[modules.%[1]s.settings]
token = "cmd://touch %[2]s"
`, name, marker)
	}

	for _, tc := range []struct {
		name string
		args []string
		// marker is created on the host if the staged value reaches it.
		marker string
		// wantErr is false when the staged value is dropped, so the module
		// runs without it.
		wantErr bool
	}{
		{name: "secret provider", args: []string{"staged-secret"}, marker: "/marker-staged-secret", wantErr: true},
		{name: "host socket", args: []string{"staged-socket"}, wantErr: true},
		{name: ".env defaults", args: []string{"staged-dot-env"}, marker: "/marker-staged-env"},
		{
			name:    "new workspace with the session's module name",
			args:    []string{"new-workspace", "--name=consumer", "--config=" + newWorkspaceConfig("consumer", "/marker-new-consumer")},
			marker:  "/marker-new-consumer",
			wantErr: true,
		},
		{
			name:    "new workspace with a new module name",
			args:    []string{"new-workspace", "--name=other", "--config=" + newWorkspaceConfig("other", "/marker-new-other")},
			marker:  "/marker-new-other",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			ws := untrustedSettingsWorkspace(t, workspaceBase(t, c), "")
			ran := untrustedSettingsCLI(ws, append([]string{"call", "stager"}, tc.args...)...)
			out, err := ran.CombinedOutput(ctx)
			require.NoError(t, err)
			if tc.marker != "" {
				require.False(t, untrustedSettingsMarkerExists(ctx, t, ran, tc.marker), out)
			}
			if tc.wantErr {
				require.Contains(t, out, untrustedSettingsError)
			} else {
				require.Contains(t, out, "passed")
			}
		})
	}

	t.Run("edited module source", func(ctx context.Context, t *testctx.T) {
		// The setting is on disk, but the module that would receive it is
		// the staged copy that module code edited.
		ws := untrustedSettingsWorkspace(t, workspaceBase(t, c), `
[modules.consumer.settings]
token = "cmd://touch /marker-disk"
`)
		ran := untrustedSettingsCLI(ws, "call", "stager", "staged-source")
		out, err := ran.CombinedOutput(ctx)
		require.NoError(t, err)
		require.False(t, untrustedSettingsMarkerExists(ctx, t, ran, "/marker-disk"), out)
		require.Contains(t, out, untrustedSettingsError)
	})
}

// untrustedSettingsCheckOutcome runs the consumer's check in ws and returns its
// error message, or "passed".
func untrustedSettingsCheckOutcome(ctx context.Context, t *testctx.T, ws *core.Workspace) string {
	t.Helper()
	checks, err := ws.Artifacts(core.WorkspaceArtifactsOpts{Include: []string{"consumer"}}).AsChecks(ctx)
	require.NoError(t, err)
	require.Len(t, checks, 1)
	checkErr, err := checks[0].Error(ctx)
	require.NoError(t, err)
	if checkErr == nil {
		return "passed"
	}
	message, err := checkErr.Message(ctx)
	require.NoError(t, err)
	return message
}

func (WorkspaceSuite) TestWorkspaceSettingsInWorkspaceValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// This test process is the host client: a provider that resolves on the
	// host runs here.
	marker := filepath.Join(t.TempDir(), "marker")
	source := core.NewQuery(c).Directory().
		WithNewFile("consumer/dagger-module.toml", untrustedSettingsConsumerModule).
		WithNewFile("consumer/main.dang", untrustedSettingsConsumerSource).
		WithNewFile("dagger.toml", fmt.Sprintf(`[modules.consumer]
source = "consumer"

[modules.consumer.settings]
token = "cmd://touch %s"
`, marker))

	outcome := untrustedSettingsCheckOutcome(ctx, t, source.AsWorkspace())
	_, statErr := os.Stat(marker)
	require.ErrorIs(t, statErr, os.ErrNotExist, "a provider from a workspace value ran on the host")
	require.Contains(t, outcome, untrustedSettingsError)
}

// A snapshot of the session workspace, as `dagger agent` loads its modules
// from, holds the host's own configuration: its modules keep their settings
// while their settings and source match the ones that the session loaded.
func (WorkspaceSuite) TestWorkspaceSettingsInSnapshot(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name string
		edit func(*core.Workspace) *core.Workspace
	}{
		{name: "unchanged", edit: func(ws *core.Workspace) *core.Workspace { return ws }},
		{name: "unrelated edit", edit: func(ws *core.Workspace) *core.Workspace {
			return ws.WithNewFile("README.md", "edited by an agent")
		}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			checkout, git := workspaceExportCheckout(ctx, t)
			marker := filepath.Join(t.TempDir(), "marker")
			// The local dependency is loaded from the copy too, so the match
			// covers dependencies.
			for name, contents := range map[string]string{
				"consumer/dagger-module.toml": untrustedSettingsConsumerModule + `
[[dependencies]]
name = "helper"
source = "../helper"
`,
				"consumer/main.dang":        untrustedSettingsConsumerSource,
				"helper/dagger-module.toml": strings.ReplaceAll(untrustedSettingsConsumerModule, "consumer", "helper"),
				"helper/main.dang":          "type Helper {\n  pub ok: String! { \"ok\" }\n}\n",
				"dagger.toml": fmt.Sprintf(`[modules.consumer]
source = "consumer"

[modules.consumer.settings]
token = "cmd://touch %s"
`, marker),
			} {
				path := filepath.Join(checkout, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
			}
			git("add", ".")
			git("commit", "-m", "consumer")

			c := connect(ctx, t, dagger.WithWorkdir(checkout))
			snapshot := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
			require.Equal(t, "passed", untrustedSettingsCheckOutcome(ctx, t, tc.edit(snapshot)))
			_, err := os.Stat(marker)
			require.NoError(t, err, "the setting from the host's dagger.toml did not resolve")
		})
	}
}
