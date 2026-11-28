package daggercmd

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// rerunTestTable is a listing of two modules' checks: a collection's items,
// whose name repeats in each, and two plain checks of the same name.
func rerunTestTable() *rerunTable {
	entry := func(module, path, name, line string, keys map[string]string) rerunEntry {
		coordinates := map[string]string{artifact.NameDimension: name}
		for id, key := range keys {
			coordinates[id] = key
		}
		return rerunEntry{module: module, typ: "type:Check", path: path, keys: keys, coordinates: coordinates, line: line}
	}
	return &rerunTable{
		definitions: artifact.Dimensions{
			{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check"},
			{Identifier: "Cases", Kind: "COLLECTION", Name: "case", CollectionType: "Cases", ItemType: "Case"},
		},
		entries: []rerunEntry{
			entry("app", "cases/check", "check", "dagger check --case=fail --name=check", map[string]string{"Cases": "fail"}),
			entry("app", "cases/check", "check", "dagger check --case=pass --name=check", map[string]string{"Cases": "pass"}),
			entry("app", "lint", "lint", "dagger check --app --name=lint", map[string]string{}),
			entry("other", "other/lint", "lint", "dagger check --other --name=lint", map[string]string{}),
		},
	}
}

func TestRerunTableFind(t *testing.T) {
	table := rerunTestTable()
	for _, tc := range []struct {
		name, address, want string
	}{
		{"type key and collection key", "dag://?check=cases/check&case=fail", "dagger check --case=fail --name=check"},
		{"canonical name and collection key", "dag://?case=pass&name=check", "dagger check --case=pass --name=check"},
		{"full path type key", "dag://?check=/lint", "dagger check --app --name=lint"},
		{"type key suffix", "dag://?check=other/lint", "dagger check --other --name=lint"},
		{"module key", "dag://?module=other&name=lint", "dagger check --other --name=lint"},
		{"path", "dag://cases/check?case=fail", "dagger check --case=fail --name=check"},
		// What the address selects isn't one item: keep the address.
		{"a collection without its key", "dag://?check=cases/check", ""},
		{"a name two modules have", "dag://?name=lint", ""},
		{"a suffix two paths have", "dag://?check=lint", ""},
		{"a whole collection", "dag://?check=cases/check&case", ""},
		{"no item", "dag://?check=cases/check&case=other", ""},
		{"nothing named", "dag://?case=fail", ""},
		{"a typed address", "dag+check://?check=lint&module=app", ""},
		{"a report's own name", "cases/check", ""},
		{"not an address", "dag://?%", ""},
		{"an absent module", "dag://?!module&name=lint", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := table.find(tc.address)
			if tc.want == "" {
				require.Nil(t, entry)
				return
			}
			require.NotNil(t, entry)
			require.Equal(t, tc.want, entry.line)
		})
	}
}

func TestRerunCommandsLookup(t *testing.T) {
	commands := &artifactRerunCommands{}
	_, ok := commands.lookup("check", "dag://?check=lint&module=app")
	require.False(t, ok, "a run that named nothing knows nothing")
	commands.set("check", rerunTestTable())
	line, ok := commands.lookup("check", "dag://?module=app&check=lint")
	require.True(t, ok)
	require.Equal(t, "dagger check --app --name=lint", line)
	_, ok = commands.lookup("generate", "dag://?module=app&check=lint")
	require.False(t, ok, "each command has its own items")
	commands.reset()
	_, ok = commands.lookup("check", "dag://?module=app&check=lint")
	require.False(t, ok, "a new run forgets the last run's items")
}

func TestFailedArtifactURIs(t *testing.T) {
	result := func(uri, failure string) artifactValueResult {
		var r artifactValueResult
		r.Artifact.URI = uri
		if failure != "" {
			r.Error = &artifactExecutionError{Message: failure}
		}
		return r
	}
	results := []artifactValueResult{
		result("dag://?name=pass", ""),
		result("dag://?name=fail", "exit code: 1"),
		// --fail-fast cancelled it before it ran.
		result("dag://?name=cancelled", context.Canceled.Error()),
	}
	require.Equal(t, []string{"dag://?name=fail"}, failedArtifactURIs(results))
}

func TestArtifactListReplayLoadModule(t *testing.T) {
	cmd := &cobra.Command{Use: "check"}
	cmd.Flags().String("format", "cli", "")
	cmd.Flags().StringP("load-module", "m", "", "")
	cmd.Flags().String("mod", "", "")
	require.Empty(t, artifactListReplayArgs(cmd))
	require.NoError(t, cmd.ParseFlags([]string{"-m", "./my mod"}))
	require.Equal(t, []string{"-m", "./my mod"}, artifactListReplayArgs(cmd))
	item := listedArtifact{URI: "dag+check://verify", DimensionKeys: []struct{ Dimension, Key string }{{"type:Check", "verify"}}}
	args, err := artifactCLIArguments(item, map[string]string{"type:Check": "check"}, artifactListReplayArgs(cmd))
	require.NoError(t, err)
	require.Equal(t, "--check=verify -m './my mod'", args)
	require.Equal(t, []string{"-W", "ws@main"}, artifactWorkspaceOptions([]string{"-W", "ws@main", "-m", "x", "--generated=false"}))
	require.Nil(t, artifactWorkspaceOptions([]string{"-m", "x"}))
}
