package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func TestArtifactSchemaListingRetainsModuleAndStructure(t *testing.T) {
	cell := &core.ArtifactDimension{Identifier: "Cell", CollectionType: "Cell", Kind: "COLLECTION", Name: "cell", ItemType: "Check"}
	catalog := &core.Artifacts{CompleteSchema: true, Entries: []*core.Artifact{
		{ModuleName: "alpha", TypeName: "Check", Path: []string{"shared"}},
		{ModuleName: "alpha", TypeName: "Check", Path: []string{"shared"}, Node: &core.ModTreeNode{CollectionDimension: cell}},
		{ModuleName: "beta", TypeName: "Check", Path: []string{"shared"}},
	}, AllPaths: artifact.Paths{
		{Module: "alpha", Type: "type:Check", Key: "shared"},
		{Module: "alpha", Type: "type:Check", Key: "shared", Dimensions: []string{"Cell"}},
		{Module: "beta", Type: "type:Check", Key: "shared"},
	}}
	require.NoError(t, catalog.NameEntries())
	ctx, srv := newNestingTestServer(t, call.View(artifactDimensionViewVersion))
	dagql.Fields[*core.Query]{dagql.Func("structuralListing", func(ctx context.Context, _ *core.Query, _ struct{}) (core.JSON, error) {
		return marshalArtifactListing(ctx, catalog.Entries, artifactListingArgs{Schema: true, TypeAssertion: true})
	})}.Install(srv)
	var data core.JSON
	require.NoError(t, srv.Select(ctx, srv.Root(), &data, dagql.Selector{Field: "structuralListing", View: srv.View}))
	var rows []struct{ URI, ModuleName string }
	require.NoError(t, json.Unmarshal(data, &rows))
	require.Len(t, rows, 3)
	for i, row := range rows {
		addr, err := dagaddress.Parse(row.URI)
		require.NoError(t, err)
		require.Equal(t, "shared", addr.Path)
		require.Contains(t, addr.Query, dagaddress.Pair{Dimension: "module", Key: row.ModuleName, HasKey: true})
		if i == 0 {
			require.Contains(t, addr.Query, dagaddress.Pair{Dimension: "Cell", Absent: true})
		}
		if i == 1 {
			require.Contains(t, addr.Query, dagaddress.Pair{Dimension: "Cell"})
		}
	}
}
