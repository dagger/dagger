package core

// These tests cover secret module settings in dagger.toml: every secret
// setting names its provider with a full scheme (env://NAME, file://PATH,
// cmd://COMMAND, ...), and `dagger workspace migrate` rewrites the shorthands
// that earlier versions accepted (a bare NAME for env://NAME, and
// <provider>:VALUE) to that form.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const missingSecretSchemeError = "secret address must name its provider with a scheme"

func workspaceSecretSettingsModule() workspaceSettingsModuleFixture {
	return workspaceSettingsModuleFixture{
		relDir: "keeper",
		name:   "keeper",
		main: `package main

import (
	"context"
	"strings"

	"dagger/keeper/internal/dagger"
)

type Keeper struct {
	Token    *dagger.Secret
	Tokens   []*dagger.Secret
	Greeting string
}

func New(
	// +optional
	token *dagger.Secret,
	// +optional
	tokens []*dagger.Secret,
	// +optional
	greeting string,
) *Keeper {
	return &Keeper{Token: token, Tokens: tokens, Greeting: greeting}
}

// Reveal returns the greeting and the plaintext of every configured secret.
func (m *Keeper) Reveal(ctx context.Context) (string, error) {
	parts := []string{m.Greeting}
	secrets := m.Tokens
	if m.Token != nil {
		secrets = append([]*dagger.Secret{m.Token}, secrets...)
	}
	for _, secret := range secrets {
		plaintext, err := secret.Plaintext(ctx)
		if err != nil {
			return "", err
		}
		parts = append(parts, plaintext)
	}
	return strings.Join(parts, ","), nil
}
`,
	}
}

// keeperExec runs the dagger CLI on the host with extra environment
// variables, returning its stdout and, on failure, an error that includes
// stderr.
func keeperExec(ctx context.Context, t *testctx.T, workdir string, env []string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := hostDaggerCommand(ctx, t, workdir, args...)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s\n%s: %w", stdout.String(), stderr.String(), err)
	}
	return stdout.String(), nil
}

func (WorkspaceSuite) TestWorkspaceSecretSettingsRequireScheme(ctx context.Context, t *testctx.T) {
	env := []string{"KEEPER_TOKEN=one", "KEEPER_OTHER=two"}
	for _, tc := range []struct {
		name     string
		settings string
		want     string
	}{
		{
			name:     "provider schemes",
			settings: "greeting = \"hi\"\ntoken = \"env://KEEPER_TOKEN\"\ntokens = [\"env://KEEPER_OTHER\", \"cmd://echo -n three\"]\n",
			want:     "hi,one,two,three",
		},
		{name: "bare name", settings: "token = \"KEEPER_TOKEN\"\n"},
		{name: "provider shorthand", settings: "token = \"env:KEEPER_TOKEN\"\n"},
		{name: "plaintext value", settings: "token = \"pasted-plaintext-value\"\n"},
		{name: "list element without scheme", settings: "tokens = [\"env://KEEPER_TOKEN\", \"KEEPER_OTHER\"]\n"},
		{name: "comma-separated list without scheme", settings: "tokens = \"env://KEEPER_TOKEN,KEEPER_OTHER\"\n"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.keeper]
source = "keeper"
main = true

[modules.keeper.settings]
`+tc.settings, workspaceSecretSettingsModule())

			out, err := keeperExec(ctx, t, workdir, env, "call", "reveal")
			if tc.want != "" {
				require.NoError(t, err)
				require.Equal(t, tc.want, strings.TrimSpace(out))
				return
			}
			require.ErrorContains(t, err, missingSecretSchemeError)
			require.NotContains(t, err.Error(), "pasted-plaintext-value")
		})
	}

	t.Run("env overlay", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.keeper]
source = "keeper"
main = true

[modules.keeper.settings]
token = "env://KEEPER_TOKEN"

[env.ci.modules.keeper.settings]
token = "KEEPER_OTHER"
`, workspaceSecretSettingsModule())

		_, err := keeperExec(ctx, t, workdir, env, "--env=ci", "call", "reveal")
		require.ErrorContains(t, err, missingSecretSchemeError)
	})
}

func (WorkspaceSuite) TestWorkspaceMigrateSecretSettings(ctx context.Context, t *testctx.T) {
	secretFile := filepath.Join(t.TempDir(), "keeper-file")
	require.NoError(t, os.WriteFile(secretFile, []byte("four"), 0o600))
	workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.keeper]
source = "keeper"
main = true

[modules.keeper.settings]
# a greeting is a string, not a secret
greeting = "hi"
token = "KEEPER_TOKEN"
tokens = ["env:KEEPER_OTHER", "cmd:echo -n three", "file:`+secretFile+`"]

[env.ci.modules.keeper.settings]
token = "KEEPER_OTHER"
tokens = "KEEPER_TOKEN,env://KEEPER_OTHER"
`, workspaceSecretSettingsModule())

	_, err := keeperExec(ctx, t, workdir, nil, "workspace", "migrate", "--auto-apply")
	require.NoError(t, err)

	migrated, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
	require.NoError(t, err)
	cfg, err := workspace.ParseConfig(migrated)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"greeting": "hi",
		"token":    "env://KEEPER_TOKEN",
		"tokens":   []any{"env://KEEPER_OTHER", "cmd://echo -n three", "file://" + secretFile},
	}, cfg.Modules["keeper"].Settings)
	require.Equal(t, map[string]any{
		"token":  "env://KEEPER_OTHER",
		"tokens": []any{"env://KEEPER_TOKEN", "env://KEEPER_OTHER"},
	}, cfg.Env["ci"].Modules["keeper"].Settings)
	require.Contains(t, string(migrated), "# a greeting is a string, not a secret")

	// A second migration finds nothing left to rewrite.
	out, err := keeperExec(ctx, t, workdir, nil, "workspace", "migrate", "--auto-apply")
	require.NoError(t, err)
	require.Contains(t, out, "No migration needed.")

	// The migrated settings keep their meaning. Migration hands the module to
	// the Go SDK, which needs its generated files first.
	_, err = keeperExec(ctx, t, workdir, nil, "generate", "--auto-apply")
	require.NoError(t, err)
	env := []string{"KEEPER_TOKEN=one", "KEEPER_OTHER=two"}
	out, err = keeperExec(ctx, t, workdir, env, "call", "reveal")
	require.NoError(t, err)
	require.Equal(t, "hi,one,two,three,four", strings.TrimSpace(out))
	out, err = keeperExec(ctx, t, workdir, env, "--env=ci", "call", "reveal")
	require.NoError(t, err)
	require.Equal(t, "hi,two,one,two", strings.TrimSpace(out))
}
