package daggercmd

import (
	"context"
	"errors"
	"testing"

	"dagger.io/dagger"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/querybuilder"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactDimensionFlags(t *testing.T) {
	root := &cobra.Command{Use: "dagger"}
	root.PersistentFlags().String("env", "", "Workspace environment")
	cmd := newListCommand()
	root.AddCommand(cmd)
	registerArtifactDimensionFlags(cmd, []string{"go-module", "go-test", "type", "env"})

	child, _, err := cmd.Find(nil)
	require.NoError(t, err)
	require.NoError(t, child.ParseFlags([]string{
		"--type=container", "--type=Directory",
		"--go-module=sdk/go", "--go-module=cmd/codegen",
		"--go-module=lib,a=b",
		"--go-test=TestConnect",
		"--env=dev",
	}))

	recorder := &artifactQueryRecorder{}
	artifacts := (&dagger.Artifacts{}).WithGraphQLQuery(querybuilder.Query().Client(recorder).Select("artifacts"))
	sel, err := parseArtifactAddresses([]string{"dag://?type=app"})
	require.NoError(t, err)
	selected := applyArtifactFilters(child, sel[0], artifactKeyFlags(child), artifacts)
	_, err = selected.Types(t.Context())
	require.ErrorIs(t, err, errArtifactQueryCaptured)
	require.Contains(t, recorder.query, `filterUri(uri:"dag+container+directory://")`)
	require.Contains(t, recorder.query, `filterUri(uri:"dag://?type=app&go-module=sdk/go&go-module=cmd/codegen&go-module=lib,a%3Db&go-test=TestConnect")`)
	require.NotContains(t, recorder.query, "env=")
}

func TestArtifactPathsUseModuleSelectors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		addresses []string
		keys      []dagaddress.Pair
		want      []string
	}{
		{name: "query", addresses: []string{"dag://?module=go&module=python"}, want: []string{"go", "python"}},
		{name: "flags", addresses: []string{"dag://"}, keys: []dagaddress.Pair{{Dimension: "module", Key: "go", HasKey: true}}, want: []string{"go"}},
		{name: "combined alternatives", addresses: []string{"dag://?module=go"}, keys: []dagaddress.Pair{{Dimension: "module", Key: "python", HasKey: true}}, want: []string{"go", "python"}},
		{name: "path keeps its scope", addresses: []string{"dag://go/test?module=go"}, want: []string{"go/test"}},
		{name: "unscoped alternative", addresses: []string{"dag://?module=go", "dag://"}},
		{name: "collection is not a module", addresses: []string{"dag://?go-module=api"}},
		{name: "collection binding keeps its scope", addresses: []string{"dag://?module=go&python-package=api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseArtifactAddresses(tc.addresses)
			require.NoError(t, err)
			require.Equal(t, tc.want, artifactPaths(parsed, tc.keys...))
		})
	}
}

func TestArtifactDimensionHelp(t *testing.T) {
	defs := artifact.Dimensions{
		{Identifier: "go/modules", Name: "go-module", QualifiedName: "go-modules", ItemType: "GoModule", CollectionType: "GoModules", KeyName: "path"},
		{Identifier: "type:GoModule", Kind: "TYPE", Name: "go-module", ItemType: "GoModule", KeyName: "name"},
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check", KeyName: "name"},
		{Identifier: "type:Expertise", Kind: "TYPE", Name: "expertise", ItemType: "Expertise", KeyName: "name"},
	}
	cmd := &cobra.Command{Use: "check"}
	registerCommandArtifactFlags(cmd)
	registerArtifactDimensionHelp(cmd, defs, defs)
	require.NoError(t, cmd.ParseFlags([]string{"--go-modules", "--go-module=."}))
	help := artifactCommandFlags(cmd)
	require.Contains(t, help, "--go-module PATH")
	require.Contains(t, help, "--go-modules ")
	require.Contains(t, help, "--artifact-go-module NAME")
	require.NotContains(t, help, "stringArray")
	keys := artifactKeyFlags(cmd)
	require.Contains(t, keys, dagaddress.Pair{Dimension: "go/modules"})
	require.Contains(t, keys, dagaddress.Pair{Dimension: "go/modules", Key: ".", HasKey: true})
}

func TestArtifactDimensionHelpSelection(t *testing.T) {
	defs := artifact.Dimensions{
		{Identifier: "go/modules", Name: "go-module", ItemType: "GoModule", CollectionType: "GoModules", KeyName: "path"},
		{Identifier: "type:GoModule", Kind: "TYPE", Name: "go-module", ItemType: "GoModule"},
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check"},
		{Identifier: "type:Container", Kind: "TYPE", Name: "container", ItemType: "Container"},
	}
	root := &cobra.Command{Use: "dagger"}
	list := newListCommand()
	child := &cobra.Command{Use: "checks"}
	root.AddCommand(list)
	list.AddCommand(child)
	registerArtifactDimensionHelp(list, defs, defs)
	registerArtifactDimensionHelp(child, defs, artifact.Dimensions{defs[0], defs[2]})
	require.Nil(t, child.Flag("container"))
	require.Nil(t, child.Flag("artifact-go-module"))
	require.NotNil(t, child.Flag("check"))
	require.Equal(t, list.Flag("go-module").Usage, child.Flag("go-module").Usage)
	require.Contains(t, child.Flag("go-module").Usage, "values: 'dagger list go-modules -a'")
}

func TestArtifactListFlagsRequireList(t *testing.T) {
	for _, command := range []string{"check", "generate", "up", "shell", "agent"} {
		for _, flag := range []string{"-a", "-f=table", "--absolute", "--abs"} {
			t.Run(command+flag, func(t *testing.T) {
				cmd := &cobra.Command{Use: command}
				cmd.Flags().BoolP("list", "l", false, "")
				registerCommandArtifactFlags(cmd)
				require.NoError(t, cmd.ParseFlags([]string{flag}))
				require.ErrorContains(t, validateArtifactListFlags(cmd), "requires --list")
				require.NoError(t, cmd.ParseFlags([]string{"-l"}))
				require.NoError(t, validateArtifactListFlags(cmd))
			})
		}
	}
}

func TestArtifactEmptyDimensionKey(t *testing.T) {
	for _, arg := range []string{"--part="} {
		t.Run(arg, func(t *testing.T) {
			cmd := newListCommand()
			registerArtifactDimensionFlags(cmd, []string{"part"})
			require.NoError(t, cmd.ParseFlags([]string{arg}))
			keys := artifactKeyFlags(cmd)
			require.Len(t, keys, 1)
			require.Equal(t, "part", keys[0].Dimension)
			require.True(t, keys[0].HasKey)
			require.Empty(t, keys[0].Key)
		})
	}
}

func TestArtifactDiscoveryRequired(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		discover bool
	}{
		{nil, false}, {[]string{"--type=Container"}, false}, {[]string{"--go-modules"}, true},
		{[]string{"--module=go"}, false}, {[]string{"dag://?module=go"}, false},
		{[]string{"--go-module=."}, true}, {[]string{"--help"}, true}, {[]string{"--", "--literal-path"}, false},
	} {
		cmd := newListCommand()
		require.Equal(t, tc.discover, needsArtifactDiscovery(cmd, tc.args))
		require.Nil(t, cmd.Flag("go-modules"), "preparation must not guess whether a flag takes a key")
	}
}

func TestArtifactPreparationDoesNotConnect(t *testing.T) {
	previous := listCmd
	t.Cleanup(func() { listCmd = previous })
	for _, args := range [][]string{
		{"list", "-a"},
		{"list", "-a", "--type=Container"},
		{"list", "--type=Check"},
		{"list", "--help"},
		{"help", "list"},
	} {
		t.Run(args[1], func(t *testing.T) {
			root := &cobra.Command{Use: "dagger"}
			listCmd = newListCommand()
			root.AddCommand(listCmd)
			ctx, cancel := context.WithCancel(t.Context())
			cancel() // Any engine call during preparation must fail.
			require.NoError(t, prepareArtifactCommands(ctx, root, parseGlobalFlags(root, args), args))
		})
	}
}

func TestArtifactAddressArguments(t *testing.T) {
	cmd := newListCommand()
	child, _, err := cmd.Find(nil)
	require.NoError(t, err)
	registerArtifactDimensionFlags(cmd, []string{"go-test"})
	require.NoError(t, child.ParseFlags([]string{"--go-test=TestQuery"}))

	sel, err := parseArtifactAddresses([]string{
		"dag+container://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect",
		"provider:docs",
		"dag://?go-module",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"golang/modules/tests/container"}, artifactPaths(sel[:1]))
	require.Nil(t, artifactPaths(sel)) // The third address selects every path.
	require.Equal(t, []string{"container"}, sel[0].Types)
	require.Empty(t, sel[1].Types)
	require.Empty(t, sel[1].Query)

	for i, want := range []string{
		`dag+container://?go-module=sdk/go&go-test=TestConnect&go-test=TestQuery`,
		`dag://?go-test=TestQuery`,
		`dag://?go-module&go-test=TestQuery`,
	} {
		recorder := &artifactQueryRecorder{}
		artifacts := (&dagger.Artifacts{}).WithGraphQLQuery(querybuilder.Query().Client(recorder).Select("artifacts"))
		selected := applyArtifactFilters(child, sel[i], artifactKeyFlags(child), artifacts)
		_, err = selected.Types(t.Context())
		require.ErrorIs(t, err, errArtifactQueryCaptured)
		require.Contains(t, recorder.query, `filterUri(uri:"`+want+`")`)
	}
	require.Len(t, sel[0].Query, 2) // Flags do not mutate the input address.

	_, err = parseArtifactAddresses([]string{"dag://github.com/dagger/dagger@main:base"})
	require.NoError(t, err)
	_, err = parseArtifactAddresses([]string{"https://example.com"})
	require.ErrorContains(t, err, "not a DAG address")
}

func TestArtifactDimensionAliases(t *testing.T) {
	defs := artifact.Dimensions{
		{Identifier: "golang/modules", Name: "go-module", QualifiedName: "golang-modules"},
		{Identifier: "app/dependencies", Name: "go-module", QualifiedName: "app-dependencies"},
	}
	_, err := defs.Resolve("go-module")
	require.ErrorContains(t, err, "ambiguous dimension")
	name, err := defs[:1].Resolve("go-module")
	require.NoError(t, err)
	require.Equal(t, "golang/modules", name)
	sel, err := parseArtifactAddresses([]string{"modules?go-module=a&golang-modules=b"})
	require.NoError(t, err)
	require.NoError(t, bindArtifactDimensions(sel[0].Query, defs[:1]))
	require.Equal(t, "golang/modules", sel[0].Query[0].Dimension)
	require.Equal(t, "golang/modules", sel[0].Query[1].Dimension)
	require.Equal(t, "a", sel[0].Query[0].Key)
	require.Equal(t, "b", sel[0].Query[1].Key)
}

var errArtifactQueryCaptured = errors.New("artifact query captured")

type artifactQueryRecorder struct{ query string }

func (r *artifactQueryRecorder) MakeRequest(_ context.Context, req *graphql.Request, _ *graphql.Response) error {
	r.query = req.Query
	return errArtifactQueryCaptured
}

func TestAbsoluteArtifactWorkspace(t *testing.T) {
	for _, address := range []string{"github.com/dagger/dagger@main:golang", "dag://github.com/dagger/dagger@main:golang"} {
		params, err := artifactClientParams(client.Params{}, []string{address})
		require.NoError(t, err)
		require.Equal(t, "github.com/dagger/dagger@main", *params.Workspace)
	}
	_, err := artifactClientParams(client.Params{}, []string{"repo@main:one", "repo@other:two"})
	require.ErrorContains(t, err, "different workspaces")
}

// Minimal flags for selection and serialization tests.
func registerArtifactDimensionFlags(cmd *cobra.Command, dimensions []string) {
	for _, dimension := range dimensions {
		if cmd.Flag(dimension) != nil {
			continue // Keep the command flag; use another dimension name or a link query.
		}
		cmd.PersistentFlags().StringArray(dimension, nil, "Select items with this `key` (repeat to select more)")
		cmd.PersistentFlags().Lookup(dimension).Annotations = map[string][]string{
			artifactDimensionFlag: {dimension},
		}
	}
}

func TestArtifactSelectorFlags(t *testing.T) {
	defs := artifact.Dimensions{
		{Identifier: "module", Kind: "MODULE", Name: "module"},
		{Identifier: "go/tests", Kind: "COLLECTION", Name: "go-test", QualifiedName: "go-tests"},
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check"},
		{Identifier: "type:Container", Kind: "TYPE", Name: "container", ItemType: "Container"},
		{Identifier: "type:Generator", Kind: "TYPE", Name: "generator", ItemType: "Generator"},
	}
	path := func(module, typ, name string) artifactListPath {
		return artifactListPath{URI: "dag://" + module + "/" + name, ModuleName: module, Dimensions: []string{"module", "type:" + typ}}
	}
	paths := []artifactListPath{
		path("go", "Check", "test"), path("playwright", "Check", "test"),
		path("test", "Check", "all"), path("format", "Check", "test"),
		path("check", "Check", "lint"), path("go-tests", "Check", "test"),
		path("docs", "Generator", "docs"), path("go", "Container", "test"),
	}
	root := &cobra.Command{Use: "dagger"}
	check := &cobra.Command{Use: "check"}
	registerCommandArtifactFlags(check)
	list := newListCommand()
	root.AddCommand(check, list)
	for _, cmd := range []*cobra.Command{check, list} {
		selected := defs
		if cmd == check {
			selected = defs[:3]
		}
		registerArtifactDimensionHelp(cmd, defs, selected)
		registerArtifactModuleFlags(cmd, defs, selected, paths)
		require.Equal(t, "stringArray", cmd.Flag("check").Value.Type())
		require.Equal(t, "stringArray", cmd.Flag("module").Value.Type())
		require.Equal(t, "bool", cmd.Flag("test").Value.Type(), "test selects the module, not the check")
		require.Equal(t, "bool", cmd.Flag("go-tests").Value.Type())
		for _, name := range []string{"go", "by-go", "by-format", "by-check", "by-go-tests"} {
			require.NotNil(t, cmd.Flag(name), name)
		}
		for _, name := range []string{"lint", "check-test", "check-lint"} {
			require.Nil(t, cmd.Flag(name), name)
		}
		help := artifactCommandFlags(cmd)
		require.Regexp(t, `(?m)^\s+--go\s+Select artifacts from module go`, help)
		require.Regexp(t, `(?m)^\s+--check NAME\s+Select checks by`, help)
		require.NotRegexp(t, `(?m)^\s+--by-go\s`, help)
		require.NoError(t, cmd.ParseFlags([]string{"--go", "--module=missing", "--check=test", "--check=stale"}))
		require.ElementsMatch(t, []dagaddress.Pair{
			{Dimension: "module", Key: "go", HasKey: true},
			{Dimension: "module", Key: "missing", HasKey: true},
			{Dimension: "type:Check", Key: "test", HasKey: true},
			{Dimension: "type:Check", Key: "stale", HasKey: true},
		}, artifactKeyFlags(cmd))
		require.NoError(t, cmd.ParseFlags([]string{"--by-go=false"}))
		require.NotContains(t, artifactKeyFlags(cmd), dagaddress.Pair{Dimension: "module", Key: "go", HasKey: true})
		// A path scope without matching types or modules is an empty selection.
		require.NoError(t, validateArtifactDimensionFlags(cmd, artifact.Dimensions{defs[1]}))
	}
	require.Nil(t, check.Flag("container"))
	require.NotNil(t, list.Flag("container"))
	require.NotNil(t, list.Flag("generator"))
	require.NotNil(t, list.Flag("docs"))
}
