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
	selected, err := selectArtifactFilters(child, sel[0], artifacts)
	require.NoError(t, err)
	_, err = selected.Types(t.Context())
	require.ErrorIs(t, err, errArtifactQueryCaptured)
	require.Contains(t, recorder.query, `filterUri(uri:"dag+container+directory://")`)
	require.Contains(t, recorder.query, `filterUri(uri:"dag://?type=app&go-module=sdk/go&go-module=cmd/codegen&go-module=lib,a%3Db&go-test=TestConnect")`)
	require.NotContains(t, recorder.query, "env=")
}

func TestArtifactDimensionHelp(t *testing.T) {
	defs := artifact.Dimensions{
		{Identifier: "Go.modules", Name: "go-module", QualifiedName: "go-modules", ItemType: "GoModule", CollectionType: "GoModules", KeyName: "path"},
		{Identifier: "type:GoModule", Kind: "TYPE", Name: "go-module", ItemType: "GoModule", KeyName: "name"},
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check", KeyName: "name"},
	}
	cmd := &cobra.Command{Use: "check"}
	registerCommandArtifactFlags(cmd)
	registerArtifactDimensionHelp(cmd, defs)
	require.NoError(t, cmd.ParseFlags([]string{"--go-modules", "--go-module=.", "--check=test"}))
	help := artifactCommandFlags(cmd)
	require.Contains(t, help, "--go-module PATH")
	require.Contains(t, help, "--go-modules ")
	require.Contains(t, help, "--artifact-go-module NAME")
	require.NotContains(t, help, "stringArray")
	require.Contains(t, cmd.Flag("artifact-go-module").Usage, "values: 'dagger list -a --type=GoModule'")
	keys, err := artifactKeyFlags(cmd)
	require.NoError(t, err)
	require.Contains(t, keys, dagaddress.Pair{Dimension: "Go.modules"})
	require.Contains(t, keys, dagaddress.Pair{Dimension: "Go.modules", Key: ".", HasKey: true})
	require.Contains(t, keys, dagaddress.Pair{Dimension: "type:Check", Key: "test", HasKey: true})
}

func TestArtifactEmptyDimensionKey(t *testing.T) {
	for _, arg := range []string{"--part="} {
		t.Run(arg, func(t *testing.T) {
			cmd := newListCommand()
			registerArtifactDimensionFlags(cmd, []string{"part"})
			require.NoError(t, cmd.ParseFlags([]string{arg}))
			keys, err := artifactKeyFlags(cmd)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			require.Equal(t, "part", keys[0].Dimension)
			require.True(t, keys[0].HasKey)
			require.Empty(t, keys[0].Key)
		})
	}
}

func TestArtifactDimensionFlagPreparation(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		discover bool
	}{
		{nil, false}, {[]string{"--type=Container"}, false}, {[]string{"--go-modules"}, true},
		{[]string{"--go-module=."}, true}, {[]string{"--help"}, true}, {[]string{"--", "--literal-path"}, false},
	} {
		cmd := newListCommand()
		discover, err := prepareArtifactDimensionFlags(cmd, tc.args)
		require.NoError(t, err)
		require.Equal(t, tc.discover, discover)
		require.Nil(t, cmd.Flag("go-modules"), "preparation must not guess whether a flag takes a key")
	}
}

func TestArtifactPreparationDoesNotConnect(t *testing.T) {
	previous := listCmd
	t.Cleanup(func() { listCmd = previous })
	for _, args := range [][]string{
		{"list", "-a"},
		{"list", "-a", "--type=Container"},
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
		selected, err := selectArtifactFilters(child, sel[i], artifacts)
		require.NoError(t, err)
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
		{Identifier: "Golang.modules", Name: "go-module", QualifiedName: "golang-modules"},
		{Identifier: "App.dependencies", Name: "go-module", QualifiedName: "app-dependencies"},
	}
	_, err := defs.Resolve("go-module")
	require.ErrorContains(t, err, "ambiguous dimension")
	name, err := defs[:1].Resolve("go-module")
	require.NoError(t, err)
	require.Equal(t, "Golang.modules", name)
	sel, err := parseArtifactAddresses([]string{"modules?go-module=a&golang-modules=b"})
	require.NoError(t, err)
	require.NoError(t, bindArtifactDimensions(sel[0].Query, defs[:1]))
	require.Equal(t, "Golang.modules", sel[0].Query[0].Dimension)
	require.Equal(t, "Golang.modules", sel[0].Query[1].Dimension)
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
