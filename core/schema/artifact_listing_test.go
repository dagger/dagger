package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestArtifactListingMatchesObjectMetadataWithoutEvaluatingLeaves(t *testing.T) {
	ctx, srv, _, _ := resolverOutputFixture(t)
	srv.InstallObject(dagql.NewClass[*core.Artifacts](srv))
	srv.InstallObject(dagql.NewClass[*core.Artifact](srv))
	schema := &artifactsSchema{}
	dagql.Fields[*core.Artifacts]{dagql.Func("items", schema.items), dagql.Func("__listing", schema.listing)}.Install(srv)
	leaves := 0
	dagql.Fields[*core.Artifact]{
		dagql.Func("uri", schema.artifactURI),
		dagql.Func("description", schema.description),
		dagql.Func("value", func(context.Context, *core.Artifact, struct{}) (dagql.String, error) {
			leaves++
			return "executed", nil
		}),
	}.Install(srv)
	entries := []*core.Artifact{
		{ModuleName: "go", Path: []string{"go", "tests", "test"}, TypeName: "Check", Node: &core.ModTreeNode{Description: "Run the selected test."},
			DimensionKeys: []*core.ArtifactDimensionKey{{Dimension: "tests", Key: "é &/空"}}, DimensionNames: map[string]string{"tests": "test"}},
		{ModuleName: "tools", Path: []string{"tools", "verify"}, TypeName: "Check"},
	}
	dagql.Fields[*core.Query]{dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) {
		return &core.Artifacts{Entries: entries}, nil
	})}.Install(srv)
	var rows []struct {
		URI, Description, ModuleName string
		Path                         []string
	}
	var objects dagql.ObjectResultArray[*core.Artifact]
	require.NoError(t, srv.Select(ctx, srv.Root(), &objects, dagql.Selector{Field: "selection"}, dagql.Selector{Field: "items"}))
	for _, typed := range []bool{false, true} {
		var data core.JSON
		args := []dagql.NamedInput{{Name: "typeAssertion", Value: dagql.Boolean(typed)}}
		require.NoError(t, srv.Select(ctx, srv.Root(), &data, dagql.Selector{Field: "selection"}, dagql.Selector{Field: "__listing", Args: args}))
		require.NoError(t, json.Unmarshal(data, &rows))
		require.Len(t, rows, len(objects))
		for i, object := range objects {
			var uri, description dagql.String
			require.NoError(t, srv.Select(ctx, object, &uri, dagql.Selector{Field: "uri", Args: args}))
			require.NoError(t, srv.Select(ctx, object, &description, dagql.Selector{Field: "description"}))
			require.Equal(t, string(uri), rows[i].URI)
			require.Equal(t, string(description), rows[i].Description)
			require.Equal(t, object.Self().ModuleName, rows[i].ModuleName)
			require.Equal(t, object.Self().Path, rows[i].Path)
		}
	}

	require.Zero(t, leaves)
}

func TestArtifactListingEmptyAbsoluteAndCancellation(t *testing.T) {
	data, err := marshalArtifactListing(t.Context(), nil, artifactListingArgs{Absolute: true})
	require.NoError(t, err)
	require.JSONEq(t, "[]", string(data))
	items := []*core.Artifact{{ModuleName: "bound", Path: []string{"bound", "verify"}, TypeName: "Check"}}
	_, err = marshalArtifactListing(t.Context(), items, artifactListingArgs{Absolute: true})
	require.ErrorContains(t, err, "not a workspace artifact")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = marshalArtifactListing(ctx, items, artifactListingArgs{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestArtifactListingResolverCannotMasqueradeAsMissingField(t *testing.T) {
	ctx, srv, _, _ := resolverOutputFixture(t)
	srv.InstallObject(dagql.NewClass[*core.Artifacts](srv))
	calls := 0
	dagql.Fields[*core.Artifacts]{
		dagql.Func("__listing", func(context.Context, *core.Artifacts, struct{}) (core.JSON, error) {
			calls++
			// A nested SDK request can return a validation error after discovery
			// has already executed. The outer response must retain its path so
			// the CLI's missing-field fallback does not execute discovery again.
			return nil, &gqlerror.Error{
				Message:    `Cannot query field "__listing" on type "Artifacts".`,
				Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"},
			}
		}),
	}.Install(srv)
	dagql.Fields[*core.Query]{
		dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) {
			return &core.Artifacts{}, nil
		}),
	}.Install(srv)
	_, err := srv.Query(ctx, `{ selection { listing: __listing } }`, nil)
	require.Error(t, err)
	var list gqlerror.List
	require.ErrorAs(t, err, &list)
	require.Len(t, list, 1)
	require.NotEmpty(t, list[0].Path, "a resolver error must not look like pre-execution validation")
	require.Equal(t, 1, calls)
}
