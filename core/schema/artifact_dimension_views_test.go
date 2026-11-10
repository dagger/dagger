package schema

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestArtifactDimensionViews(t *testing.T) {
	for _, tc := range []struct {
		version string
		all     bool
	}{
		{"v1.0.0-beta.25", false}, {artifactDimensionViewVersion, true}, {"v1.0.0", true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			view := call.View(engine.APIViewVersion(tc.version))
			_, srv := newNestingTestServer(t, view)
			schema := srv.SchemaForView(view)
			for _, field := range []struct{ typ, name string }{{"Artifacts", "dimensions"}, {"Artifact", "dimensionKeys"}} {
				spec := schema.Types[field.typ].Fields.ForName(field.name)
				require.NotNil(t, spec)
				require.Equal(t, tc.all, spec.Arguments.ForName("all") != nil)
			}
		})
	}
}

func TestArtifactDimensionSelectionMask(t *testing.T) {
	ctx := context.Background()
	selection := &core.Artifacts{Entries: []*core.Artifact{
		{ModuleName: "app", TypeName: "Check", DimensionKeys: []*core.ArtifactDimensionKey{{Dimension: "module", Key: "app"}, {Dimension: "item", Key: "a"}}},
		{ModuleName: "app", TypeName: "Check", DimensionKeys: []*core.ArtifactDimensionKey{{Dimension: "module", Key: "app"}}},
	}, AllDimensions: artifact.Dimensions{{Identifier: "module"}, {Identifier: "item"}, {Identifier: "empty"}}}
	s := &artifactsSchema{}
	expanded, err := expandArtifacts(ctx, selection)
	require.NoError(t, err)
	dimensions := expanded.Dimensions()
	require.Equal(t, []string{"item"}, dimensions)
	items, err := s.items(ctx, selection, struct{}{})
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, item := range items {
		visible, err := s.artifactDimensionKeys(ctx, item, artifactDimensionViewArgs{})
		require.NoError(t, err)
		for _, key := range visible {
			require.Contains(t, dimensions, key.Dimension)
		}
		full, err := s.artifactDimensionKeys(ctx, item, artifactDimensionViewArgs{All: true})
		require.NoError(t, err)
		require.Equal(t, item.DimensionKeys, full)
	}
	require.Len(t, items[0].DisplayDimensionKeys(false), 1)
	require.Empty(t, items[1].DisplayDimensionKeys(false))
	legacy, err := s.legacyDimensions(ctx, selection, struct{}{})
	require.NoError(t, err)
	require.Equal(t, []string{"item", "module", "type:Check"}, legacy)
	all := selection.FilterTypes([]string{}, false).SchemaDimensions()
	require.Equal(t, []string{"empty", "item", "module"}, all)
	singleton := selection.FilterTypes([]string{}, false)
	singleton.Entries = selection.Entries[:1]
	one, err := s.one(ctx, singleton, struct{}{})
	require.NoError(t, err)
	require.Empty(t, one.DisplayDimensionKeys(false))
	require.Len(t, one.DisplayDimensionKeys(true), 3)
}

// Each items resolver call models discovery of an uncached collection source.
// Subsequent discovery would change both membership and visible dimensions.
func TestArtifactDimensionSnapshot(t *testing.T) {
	for _, initialRows := range []int{1, 2} {
		t.Run(fmt.Sprint(initialRows), func(t *testing.T) {
			ctx, srv := newNestingTestServer(t, call.View(artifactDimensionViewVersion))
			selection := &core.Artifacts{AllDimensions: artifact.Dimensions{{Identifier: "item"}}}
			dagql.Fields[*core.Query]{dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) { return selection, nil })}.Install(srv)
			var discoveries atomic.Int32
			dagql.Fields[*core.Artifacts]{dagql.Func("items", func(context.Context, *core.Artifacts, struct{}) ([]*core.Artifact, error) {
				count := initialRows
				if discoveries.Add(1) > 1 {
					count = 3 - initialRows
				}
				snapshot := &core.Artifacts{}
				for i := range count {
					snapshot.Entries = append(snapshot.Entries, &core.Artifact{Path: []string{"check"}, DimensionKeys: []*core.ArtifactDimensionKey{{Dimension: "item", Key: fmt.Sprint(i)}}})
				}
				columns := snapshot.Dimensions()
				rows := make([]*core.Artifact, 0, count)
				for _, row := range snapshot.Entries {
					rows = append(rows, row.WithDimensionDisplay(columns))
				}
				return rows, nil
			})}.Install(srv)
			// all:true reads schema only, even when items have never been discovered.
			_, err := srv.Query(ctx, `{ selection { dimensions(all:true) } }`, nil)
			require.NoError(t, err)
			require.Zero(t, discoveries.Load())
			_, err = srv.Query(ctx, `{ selection { dimensions items { dimensionKeys { dimension key } } } }`, nil)
			require.NoError(t, err)
			require.EqualValues(t, 1, discoveries.Load())
			var columns dagql.Array[dagql.String]
			require.NoError(t, srv.Select(ctx, srv.Root(), &columns, dagql.Selector{Field: "selection"}, dagql.Selector{Field: "dimensions", View: srv.View}))
			require.Len(t, columns, initialRows-1)
			var rows dagql.ObjectResultArray[*core.Artifact]
			require.NoError(t, srv.Select(ctx, srv.Root(), &rows, dagql.Selector{Field: "selection"}, dagql.Selector{Field: "items"}))
			require.Len(t, rows, initialRows)
			for _, row := range rows {
				require.Len(t, row.Self().DisplayDimensionKeys(false), initialRows-1)
			}
			var one dagql.ObjectResult[*core.Artifact]
			err = srv.Select(ctx, srv.Root(), &one, dagql.Selector{Field: "selection"}, dagql.Selector{Field: "one", View: srv.View})
			if initialRows == 1 {
				require.NoError(t, err)
				require.Empty(t, one.Self().DisplayDimensionKeys(false))
			} else {
				require.ErrorContains(t, err, "matches 2 artifacts")
			}
			require.EqualValues(t, 1, discoveries.Load())
		})
	}
}
