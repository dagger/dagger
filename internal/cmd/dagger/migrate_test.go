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
