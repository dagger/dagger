package daggercmd

import (
	"context"
	"errors"
	"testing"

	"dagger.io/dagger"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/querybuilder"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

func TestArtifactDimensionFlagPreparation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		help bool
		want []string
		err  string
	}{
		{name: "list"},
		{name: "static flags", args: []string{"--type", "Container", "--env=dev", "--dimension-key", "go-module=sdk/go"}},
		{name: "dimensions", args: []string{"--go-module=sdk/go", "--go-test", "TestOne", "--go-test=TestTwo"}, want: []string{"go-module", "go-test"}},
		{name: "flag value", args: []string{"--go-test", "--literal-key"}, want: []string{"go-test"}},
		{name: "after separator", args: []string{"--", "--literal-path"}},
		{name: "help", args: []string{"--go-test=TestOne", "--help"}, help: true, want: []string{"go-test"}},
		{name: "no help", args: []string{"--help=false"}},
		{name: "missing value", args: []string{"--go-test"}, want: []string{"go-test"}, err: "flag needs an argument"},
		{name: "unknown short flag", args: []string{"-z"}, err: "unknown shorthand flag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "dagger"}
			root.PersistentFlags().String("env", "", "Workspace environment")
			cmd := newArtifactsCommand()
			root.AddCommand(cmd)
			child, _, err := root.Find([]string{"artifact", "list"})
			require.NoError(t, err)
			help, err := prepareArtifactDimensionFlags(child, tc.args)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.help, help)
			var names []string
			child.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
				if len(flag.Annotations[artifactDimensionFlag]) > 0 {
					names = append(names, flag.Name)
				}
			})
			require.Equal(t, tc.want, names)
			// Discovery must not apply values that Cobra will parse again.
			types, err := child.Flags().GetStringArray("type")
			require.NoError(t, err)
			require.Empty(t, types)
			env, err := root.PersistentFlags().GetString("env")
			require.NoError(t, err)
			require.Empty(t, env)
		})
	}
}

func TestArtifactPreparationDoesNotConnect(t *testing.T) {
	previous := artifactsCmd
	t.Cleanup(func() { artifactsCmd = previous })
	for _, args := range [][]string{
		{"artifact", "list"},
		{"artifact", "types", "--type=Container"},
		{"artifact", "dimensions"},
		{"artifact", "keys", "go-test", "--go-module=sdk/go"},
	} {
		t.Run(args[1], func(t *testing.T) {
			root := &cobra.Command{Use: "dagger"}
			artifactsCmd = newArtifactsCommand()
			root.AddCommand(artifactsCmd)
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
		require.NotContains(t, recorder.query, "filterTypes(")
	}
	require.Len(t, sel[0].Query, 2) // Flags do not mutate the input address.

	_, err = parseArtifactAddresses([]string{"dag://github.com/dagger/dagger@main:base"})
	require.NoError(t, err)
	_, err = parseArtifactAddresses([]string{"https://example.com"})
	require.ErrorContains(t, err, "not a DAG address")
}

func TestArtifactDimensionAliases(t *testing.T) {
	defs := []artifactDimensionDefinition{
		{Identifier: "Golang.modules", Name: "go-module", QualifiedName: "golang-modules"},
		{Identifier: "App.dependencies", Name: "go-module", QualifiedName: "app-dependencies"},
	}
	_, err := resolveArtifactDimensionName(defs, "go-module")
	require.ErrorContains(t, err, "ambiguous dimension")
	name, err := resolveArtifactDimensionName(defs[:1], "go-module")
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
