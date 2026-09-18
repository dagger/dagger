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
	"github.com/stretchr/testify/require"
)

func TestArtifactDimensionFlags(t *testing.T) {
	root := &cobra.Command{Use: "dagger"}
	root.PersistentFlags().String("env", "", "Workspace environment")
	cmd := newArtifactsCommand()
	root.AddCommand(cmd)
	registerArtifactDimensionFlags(cmd, []string{"go-module", "go-test", "type", "env"})

	child, _, err := cmd.Find([]string{"types"})
	require.NoError(t, err)
	require.NoError(t, child.ParseFlags([]string{
		"--type=Container", "--type=Directory",
		"--go-module=sdk/go", "--go-module=cmd/codegen",
		"--dimension-key=go-module=lib,a=b",
		"--go-test=TestConnect", "--dimension-key=type=app",
		"--env=dev",
	}))

	recorder := &artifactQueryRecorder{}
	artifacts := (&dagger.Artifacts{}).WithGraphQLQuery(querybuilder.Query().Client(recorder).Select("artifacts"))
	sel, err := parseArtifactAddresses(nil)
	require.NoError(t, err)
	selected, err := selectArtifactFilters(child, sel[0], artifacts)
	require.NoError(t, err)
	_, err = selected.Types(t.Context())
	require.ErrorIs(t, err, errArtifactQueryCaptured)
	require.Contains(t, recorder.query, `filterTypes(types:["Container","Directory"])`)
	require.Contains(t, recorder.query, `filterUri(uri:"dag://?go-module=lib,a%3Db&type=app&go-module=sdk/go&go-module=cmd/codegen&go-test=TestConnect")`)
	require.NotContains(t, recorder.query, "env=")
}

func TestArtifactAddressArguments(t *testing.T) {
	cmd := newArtifactsCommand()
	child, _, err := cmd.Find([]string{"list"})
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
