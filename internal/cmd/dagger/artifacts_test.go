package daggercmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/Khan/genqlient/graphql"
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
	selected, err := selectArtifactFilters(child, sel, artifacts)
	require.NoError(t, err)
	_, err = selected.Types(t.Context())
	require.ErrorIs(t, err, errArtifactQueryCaptured)
	require.Contains(t, recorder.query, `filterTypes(types:["Container","Directory"])`)
	require.Contains(t, recorder.query, `dimension:"go-module"`)
	require.Contains(t, recorder.query, `keys:["lib,a=b","sdk/go","cmd/codegen"]`)
	require.Contains(t, recorder.query, `dimension:"go-test"`)
	require.Contains(t, recorder.query, `keys:["TestConnect"]`)
	require.Contains(t, recorder.query, `dimension:"type"`)
	require.Contains(t, recorder.query, `keys:["app"]`)
	require.Equal(t, 3, strings.Count(recorder.query, "filterDimensionKeys("))
	require.NotContains(t, recorder.query, `dimension:"env"`)
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
	require.Equal(t, []string{"golang/modules/tests/container", "provider/docs"}, sel.include)
	require.Equal(t, []string{"container"}, sel.types)

	recorder := &artifactQueryRecorder{}
	artifacts := (&dagger.Artifacts{}).WithGraphQLQuery(querybuilder.Query().Client(recorder).Select("artifacts"))
	selected, err := selectArtifactFilters(child, sel, artifacts)
	require.NoError(t, err)
	_, err = artifactURIs(t.Context(), nil, selected)
	require.ErrorIs(t, err, errArtifactQueryCaptured)
	require.Contains(t, recorder.query, `filterUri(uri:"dag+container://")`)
	require.Contains(t, recorder.query, `filterDimensions(dimensions:["go-module"])`)
	require.Contains(t, recorder.query, `dimension:"go-test"`)
	require.Contains(t, recorder.query, `keys:["TestConnect","TestQuery"]`)
	require.NotContains(t, recorder.query, "filterTypes(")

	_, err = parseArtifactAddresses([]string{"dag://github.com/dagger/dagger@main:base"})
	require.ErrorContains(t, err, "absolute addresses are not supported yet")
	_, err = parseArtifactAddresses([]string{"https://example.com"})
	require.ErrorContains(t, err, "not a DAG address")
}

var errArtifactQueryCaptured = errors.New("artifact query captured")

type artifactQueryRecorder struct{ query string }

func (r *artifactQueryRecorder) MakeRequest(_ context.Context, req *graphql.Request, _ *graphql.Response) error {
	r.query = req.Query
	return errArtifactQueryCaptured
}
