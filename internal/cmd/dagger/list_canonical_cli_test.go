package daggercmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func canonicalCLICommands(t *testing.T, output string) [][]string {
	t.Helper()
	program, err := syntax.NewParser().Parse(strings.NewReader(output), "")
	require.NoError(t, err)
	var commands [][]string
	shell, err := interp.New(interp.ExecHandlers(func(_ interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(_ context.Context, args []string) error {
			commands = append(commands, append([]string{}, args...))
			return nil
		}
	}))
	require.NoError(t, err)
	require.NoError(t, shell.Run(t.Context(), program))
	return commands
}

func TestArtifactCanonicalCLIName(t *testing.T) {
	cmd := &cobra.Command{Use: "check"}
	cmd.Flags().String("format", "cli", "")
	definitions := artifact.Dimensions{
		{Identifier: "module", Kind: "MODULE", Name: "module"},
		{Identifier: "name", Kind: "NAME", Name: "name"},
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check"},
	}
	items := []listedArtifact{{
		URI: "dag+check://?name=python310.unit", Name: "python310.unit", ModuleName: "python-client",
		Coordinates:   []struct{ Dimension, Key string }{{"module", "python-client"}, {"name", "python310.unit"}},
		DimensionKeys: []struct{ Dimension, Key string }{{"module", "python-client"}, {"type:Check", "python-client/python-310/unit"}},
	}}
	paths := []artifactListPath{
		{URI: "dag+check://python-client/python-310/unit", ModuleName: "python-client", Name: "python310.unit", Dimensions: []string{"module", "name", "type:Check"}},
		{URI: "dag+check://python-client/python-311/unit", ModuleName: "python-client", Name: "python311.unit", Dimensions: []string{"module", "name", "type:Check"}},
	}
	names, err := prepareArtifactOutputWithSchema(cmd, items, definitions, paths)
	require.NoError(t, err)
	require.Empty(t, items[0].CLIPath)
	var output bytes.Buffer
	cmd.SetOut(&output)
	require.NoError(t, writeArtifactList(cmd, items, names))
	require.Equal(t, [][]string{{"dagger", "check", "--python-client", "--name=python310.unit"}}, canonicalCLICommands(t, output.String()))
	require.NotContains(t, output.String(), "--check=")
	require.NotContains(t, output.String(), "python-310")
	require.Equal(t, "python-client/python-310/unit", items[0].fullPath())
}

func TestArtifactCanonicalCLIPathProof(t *testing.T) {
	target := artifactListPath{URI: "dag+check://tools/items/test", ModuleName: "tools", Name: "test", Dimensions: []string{"module", "name", "/tools/items", "type:Check"}}
	child := artifactListPath{URI: "dag+check://tools/items/parts/test", ModuleName: "tools", Name: "test", Dimensions: []string{"module", "name", "/tools/items", "/tools/items/parts", "type:Check"}}
	for _, tc := range []struct {
		name      string
		candidate artifactListPath
		types     []string
		exact     bool
	}{
		{name: "same local name in descendant", candidate: child, types: []string{"Check"}, exact: true},
		{name: "unknown schema name", candidate: artifactListPath{URI: child.URI, ModuleName: child.ModuleName, Dimensions: child.Dimensions}, types: []string{"Check"}, exact: true},
		{name: "different local name", candidate: artifactListPath{URI: child.URI, ModuleName: child.ModuleName, Name: "validate", Dimensions: child.Dimensions}, types: []string{"Check"}},
		{name: "another module", candidate: artifactListPath{URI: "dag+check://other/items/test", ModuleName: "other", Name: "test", Dimensions: target.Dimensions}, types: []string{"Check"}},
		{name: "parent lacks selected dimension", candidate: artifactListPath{URI: "dag+check://tools/test", ModuleName: "tools", Name: "test", Dimensions: []string{"module", "name", "type:Check"}}, types: []string{"Check"}},
		{name: "command excludes other type", candidate: artifactListPath{URI: "dag+container://tools/items/test", ModuleName: "tools", Name: "test", Dimensions: []string{"module", "name", "/tools/items", "type:Container"}}, types: []string{"Check"}},
		{name: "generic list retains type constraint", candidate: artifactListPath{URI: "dag+container://tools/items/test", ModuleName: "tools", Name: "test", Dimensions: []string{"module", "name", "/tools/items", "type:Container"}}, exact: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := listedArtifact{
				URI: "dag+check://?item=one&name=test", Name: "test",
				Coordinates:   []struct{ Dimension, Key string }{{"module", "tools"}, {"/tools/items", "one"}, {"name", "test"}},
				DimensionKeys: []struct{ Dimension, Key string }{{"module", "tools"}, {"/tools/items", "one"}, {"type:Check", "tools/items/test"}},
			}
			items := []listedArtifact{row}
			require.NoError(t, prepareCanonicalArtifactCLIPaths(items, []artifactListPath{target, tc.candidate}, tc.types))
			if !tc.exact {
				require.Empty(t, items[0].CLIPath)
				return
			}
			selector, err := dagaddress.Parse(items[0].CLIPath)
			require.NoError(t, err)
			require.Equal(t, []string{"check"}, selector.Types)
			require.Equal(t, "tools/items/test", selector.Path)
			require.Equal(t, []dagaddress.Pair{{Dimension: "type:Check", Key: "/tools/items/test", HasKey: true}}, selector.Query)
			candidates := artifact.Paths{{Type: "type:Check", Key: "tools/items/test"}, {Type: "type:Check", Key: "tools/items/parts/test"}}
			require.Equal(t, []string{"tools/items/test"}, artifact.ResolveTypeKey(candidates, selector.Query[0].Key))
			arguments, err := artifactCLIArguments(items[0], map[string]string{"/tools/items": "item"}, nil)
			require.NoError(t, err)
			require.Equal(t, [][]string{{"dagger", "check", items[0].CLIPath, "--module=tools", "--item=one", "--name=test"}}, canonicalCLICommands(t, "dagger check "+arguments))
		})
	}
}

func TestArtifactCanonicalCLISchemaPresenceAndLiteralName(t *testing.T) {
	value := "generate.a'b!c\n$(echo injected)"
	item := listedArtifact{
		URI: "dag+check://tools/items/generate/stale", Name: value, ModuleFlag: "tools",
		Coordinates:   []struct{ Dimension, Key string }{{"module", "tools"}, {"name", value}},
		DimensionKeys: []struct{ Dimension, Key string }{{"module", "tools"}, {"type:Check", "tools/items/generate/stale"}},
		Presence:      []string{"/tools/items"}, PresenceNames: map[string]string{"/tools/items": "tools-items"},
		DisplayKeys: map[string]string{"name": "stale", "type:Check": "stale"},
	}
	arguments, err := artifactCLIArguments(item, map[string]string{"type:Check": "check"}, nil)
	require.NoError(t, err)
	require.NotContains(t, arguments, "\n")
	require.Equal(t, [][]string{{"dagger", "check", "--tools", "--tools-items", "--name=" + value}}, canonicalCLICommands(t, "dagger check "+arguments))
	item.OmitCLITypeKey = true
	arguments, err = artifactCLIArguments(item, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "--tools --tools-items", arguments)
	item.OmitCLITypeKey, item.CollectionItem, item.OmitTypeKey = false, true, true
	arguments, err = artifactCLIArguments(item, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "--tools --tools-items", arguments)
}
