package daggercmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestMigrationCommands(t *testing.T) {
	root := testRootCommand()
	for _, prefix := range []string{"workspace", "module"} {
		cmd, _, err := root.Find([]string{prefix, "migrate"})
		require.NoError(t, err)
		require.Equal(t, "migrate", cmd.Name())
		require.NotNil(t, cmd.Flags().Lookup("no-apply"))
		require.NoError(t, validateFlagCapabilities(root, []string{prefix, "migrate", "-W", "/tmp/workspace", "-y"}))
	}
}

func TestMigrationDisposition(t *testing.T) {
	for _, tc := range []struct {
		name           string
		apply          bool
		noApply        bool
		runningInAgent bool
		want           changesetDisposition
		wantErr        string
	}{
		{name: "human prompt", want: changesetDispositionPrompt},
		{name: "agent requires choice", runningInAgent: true, want: changesetDispositionPrompt, wantErr: "dagger workspace migrate requires an explicit changeset choice"},
		{name: "agent apply", apply: true, runningInAgent: true, want: changesetDispositionApply},
		{name: "agent no apply", noApply: true, runningInAgent: true, want: changesetDispositionNoApply},
		{name: "human apply", apply: true, want: changesetDispositionApply},
		{name: "human no apply", noApply: true, want: changesetDispositionNoApply},
		{name: "conflicting choices", apply: true, noApply: true, want: changesetDispositionPrompt, wantErr: "cannot be used together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testRootCommand()
			cmd, _, err := root.Find([]string{"workspace", "migrate"})
			require.NoError(t, err)
			got, err := migrationDisposition(cmd, tc.apply, tc.noApply, tc.runningInAgent)
			require.Equal(t, tc.want, got)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

func TestOptionalModuleCandidatesSkippedWithAutoApply(t *testing.T) {
	old := autoApply
	autoApply = true
	t.Cleanup(func() { autoApply = old })
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	selected, err := selectMigrationCandidates(t.Context(), cmd, []string{"fixture"}, changesetDispositionApply)
	require.NoError(t, err)
	require.Empty(t, selected)
	require.Contains(t, out.String(), "dagger module migrate /fixture")
}

func TestMigrationApplyCommand(t *testing.T) {
	cmd := &cobra.Command{}
	require.Equal(t, "dagger workspace migrate --module /app --module '/fixtures/example module' --auto-apply", migrationApplyCommand(cmd, nil, false, []string{"/app", "/fixtures/example module"}))
	require.Equal(t, "dagger module migrate 'example module' --auto-apply", migrationApplyCommand(cmd, []string{"example module"}, true, nil))
}

func TestMigrationCommandAppearsInApplyForm(t *testing.T) {
	description := "app/dagger-module.toml +5 -0"
	require.Equal(t, description, changesetPromptDescription(t.Context(), description))
	command := "dagger workspace migrate --module /app --auto-apply"
	ctx := context.WithValue(t.Context(), changesetPromptCommandKey{}, command)
	require.Equal(t, command+"\n\n"+description, changesetPromptDescription(ctx, description))
}

func TestRecommendedModuleCommands(t *testing.T) {
	got := recommendedModuleCommands([]recommendation{{Module: registryModule{Name: "Go", Repo: "github.com/dagger/go"}, Match: "go.mod"}})
	require.Contains(t, got, "Go: found go.mod")
	require.Contains(t, got, "dagger module install github.com/dagger/go")
	require.Contains(t, got, "No modules were installed")
}
