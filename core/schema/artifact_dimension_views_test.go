package schema

import (
	"context"
	"fmt"
	"strings"
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
		{"v1.0.0-beta.25", false}, {"v1.0.0-beta.26", false}, {"v1.0.0-beta.27", false},
		{artifactDimensionViewVersion, true}, {"v1.0.0", true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			view := call.View(engine.APIViewVersion(tc.version))
			_, srv := newNestingTestServer(t, view)
			schema := srv.SchemaForView(view)
			require.Equal(t, tc.all, schema.Types["ArtifactDimensionKind"].EnumValues.ForName("NAME") != nil)
			for _, name := range []string{"MODULE", "COLLECTION", "TYPE"} {
				require.NotNil(t, schema.Types["ArtifactDimensionKind"].EnumValues.ForName(name))
			}
			dimensions := schema.Types["Artifacts"].Fields.ForName("dimensions")
			require.Equal(t, "[String!]!", dimensions.Type.String())
			require.Equal(t, "[ArtifactDimensionKey!]!", schema.Types["Artifact"].Fields.ForName("dimensionKeys").Type.String())
			for _, field := range []struct{ typ, name string }{{"Artifacts", "dimensions"}, {"Artifact", "dimensionKeys"}} {
				spec := schema.Types[field.typ].Fields.ForName(field.name)
				require.NotNil(t, spec)
				require.Equal(t, tc.all, spec.Arguments.ForName("all") != nil)
			}
		})
	}
}

func TestArtifactDimensionMetadataByView(t *testing.T) {
	for _, version := range []string{"v1.0.0-beta.25", "v1.0.0-beta.26", "v1.0.0-beta.27", artifactDimensionViewVersion} {
		t.Run(version, func(t *testing.T) {
			ctx, srv := newNestingTestServer(t, call.View(engine.APIViewVersion(version)))
			row := &core.Artifact{ModuleName: "app", Path: []string{"app", "build"}, TypeName: "Check",
				DimensionKeys: []*core.ArtifactDimensionKey{{Dimension: "module", Key: "app"}, {Dimension: "type:Check", Key: "app/build"}}}
			selection := &core.Artifacts{Entries: []*core.Artifact{row}, AllDimensions: artifact.Dimensions{
				{Identifier: "module", Kind: "MODULE", Name: "module"},
				{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check"},
			}}
			dagql.Fields[*core.Query]{dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) {
				return selection, nil
			})}.Install(srv)
			var definitions dagql.Array[*core.ArtifactDimension]
			require.NoError(t, srv.Select(ctx, srv.Root(), &definitions,
				dagql.Selector{Field: "selection"}, dagql.Selector{Field: "dimensionDefinitions", View: srv.View}))
			byID := map[string]string{}
			for _, definition := range definitions {
				byID[definition.Identifier] = definition.Kind
			}
			require.Equal(t, "MODULE", byID["module"])
			if version == artifactDimensionViewVersion {
				require.Equal(t, map[string]string{"module": "MODULE", "name": "NAME"}, byID)
			} else {
				require.Equal(t, map[string]string{"module": "MODULE", "type:Check": "TYPE"}, byID)
			}
		})
	}
}

func TestArtifactDimensionSnapshot(t *testing.T) {
	for _, initialRows := range []int{1, 2} {
		t.Run(fmt.Sprint(initialRows), func(t *testing.T) {
			ctx, srv := newNestingTestServer(t, call.View(artifactDimensionViewVersion))
			selection := &core.Artifacts{AllDimensions: artifact.Dimensions{{Identifier: "Items", CollectionType: "Items", ItemType: "AppItem", Kind: "COLLECTION"}}}
			dagql.Fields[*core.Query]{dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) { return selection, nil })}.Install(srv)
			var discoveries atomic.Int32
			dagql.Fields[*core.Artifacts]{dagql.Func("items", func(context.Context, *core.Artifacts, struct{}) ([]*core.Artifact, error) {
				count := initialRows
				if discoveries.Add(1) > 1 {
					count = 3 - initialRows
				}
				snapshot := &core.Artifacts{}
				for i := range count {
					snapshot.Entries = append(snapshot.Entries, dimensionViewItem(fmt.Sprint(i)))
				}
				columns := snapshot.Dimensions()
				rows := make([]*core.Artifact, 0, count)
				for _, row := range snapshot.Entries {
					rows = append(rows, row.WithDimensionDisplay(columns))
				}
				return rows, nil
			}).View(core.AllVersion)}.Install(srv)

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
			require.NoError(t, srv.Select(ctx, srv.Root(), &rows, dagql.Selector{Field: "selection"}, dagql.Selector{Field: "items", View: srv.View}))
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

func TestArtifactDimensionItemDisplaySourceIsLazy(t *testing.T) {
	ctx, srv := newNestingTestServer(t, call.View(artifactDimensionViewVersion))
	selection := &core.Artifacts{}
	row := dimensionViewItem("a")
	dagql.Fields[*core.Query]{dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) {
		return selection, nil
	})}.Install(srv)
	var discoveries atomic.Int32
	dagql.Fields[*core.Artifacts]{
		dagql.Func("items", func(context.Context, *core.Artifacts, struct{}) ([]*core.Artifact, error) {
			discoveries.Add(1)

			return []*core.Artifact{row, {
				ModuleName: "app", TypeName: "Check", Path: []string{"app", "items", "check"},
				Node: &core.ModTreeNode{Name: "check", Parent: row.Node},
			}}, nil
		}).View(core.AllVersion),
		dagql.NodeFunc("projectedItem", func(_ context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) (*core.Artifact, error) {
			return row.WithDimensionDisplaySource(parent), nil
		}),
	}.Install(srv)
	var full dagql.Array[*core.ArtifactDimensionKey]
	require.NoError(t, srv.Select(ctx, srv.Root(), &full,
		dagql.Selector{Field: "selection"}, dagql.Selector{Field: "projectedItem"},
		dagql.Selector{Field: "dimensionKeys", View: srv.View, Args: []dagql.NamedInput{{Name: "all", Value: dagql.Boolean(true)}}}))
	require.Equal(t, row.CanonicalDimensionKeys(), []*core.ArtifactDimensionKey(full))
	require.Zero(t, discoveries.Load(), "all:true must not discover descendants")
	for range 2 {
		var visible dagql.Array[*core.ArtifactDimensionKey]
		require.NoError(t, srv.Select(ctx, srv.Root(), &visible,
			dagql.Selector{Field: "selection"}, dagql.Selector{Field: "projectedItem"},
			dagql.Selector{Field: "dimensionKeys", View: srv.View}))
		require.Equal(t, []*core.ArtifactDimensionKey{{Dimension: "name", Key: "items"}}, []*core.ArtifactDimensionKey(visible))
		require.EqualValues(t, 1, discoveries.Load(), "default coordinates must reuse the parent's cached snapshot")
	}
}

func TestArtifactDimensionDefinitionsUseGlobalLabels(t *testing.T) {
	ctx, srv := newNestingTestServer(t, call.View(artifactDimensionViewVersion))
	collection := func(typ, itemType string) *core.ArtifactDimension {
		return &core.ArtifactDimension{
			Identifier: typ, Kind: "COLLECTION", CollectionType: typ,
			ItemType: itemType, Name: strings.ToLower(itemType), KeyName: "name",
		}
	}
	packages := collection("Packages", "Package")
	dependencies := collection("Dependencies", "Package")
	tests := collection("Tests", "Test")
	checks := collection("Checks", "Check")
	selection := &core.Artifacts{
		AllDimensions: artifact.Dimensions{
			packages, dependencies, tests, checks,
			{Identifier: "module", Kind: "MODULE", Name: "module"},
			{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check"},
		},
	}

	for _, dimension := range []*core.ArtifactDimension{packages, tests, checks} {
		selection.Entries = append(selection.Entries, &core.Artifact{
			ModuleName: "app", TypeName: "Check",
			Node: &core.ModTreeNode{CollectionDimension: dimension},
		})
	}
	dagql.Fields[*core.Query]{dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) {
		return selection, nil
	})}.Install(srv)
	var definitions dagql.Array[*core.ArtifactDimension]
	require.NoError(t, srv.Select(ctx, srv.Root(), &definitions,
		dagql.Selector{Field: "selection"}, dagql.Selector{Field: "dimensionDefinitions", View: srv.View}))
	byID := map[string]*core.ArtifactDimension{}
	for _, definition := range definitions {
		byID[definition.Identifier] = definition
	}
	require.NotContains(t, byID, "Dependencies")
	require.Contains(t, byID, "Packages")
	require.Equal(t, "packages", byID["Packages"].Name)
	require.Equal(t, "test", byID["Tests"].Name)

	require.Equal(t, "check", byID["Checks"].Name)
	require.Equal(t, "COLLECTION", byID["Checks"].Kind)
	require.NotContains(t, byID, "type:Check")
	require.Equal(t, "module", byID["module"].Name)
	require.Equal(t, "name", byID["name"].Name)
}

func TestArtifactSchemaIncludesByView(t *testing.T) {
	include := []string{"alpha", "beta/**"}
	for _, tc := range []struct {
		view   call.View
		modern bool
	}{
		{call.View(engine.APIViewVersion("v1.0.0-beta.25")), false},
		{call.View(engine.APIViewVersion("v1.0.0-beta.26")), false},
		{call.View(engine.APIViewVersion("v1.0.0-beta.27")), false},
		{call.View(artifactDimensionViewVersion), true},
	} {
		t.Run(string(tc.view), func(t *testing.T) {
			ctx, srv := newNestingTestServer(t, tc.view)
			dagql.Fields[*core.Query]{dagql.Func("schemaIncludes", func(ctx context.Context, _ *core.Query, _ struct{}) (bool, error) {
				got := artifactSchemaIncludes(ctx, include)
				if tc.modern {
					require.Nil(t, got)
				} else {
					require.Equal(t, include, got)
				}
				require.Nil(t, artifactSchemaIncludes(ctx, nil))
				require.Equal(t, []string{"alpha", "beta/**"}, include)
				return got == nil, nil
			}).View(core.AllVersion)}.Install(srv)
			var omitted dagql.Boolean
			require.NoError(t, srv.Select(ctx, srv.Root(), &omitted, dagql.Selector{Field: "schemaIncludes", View: srv.View}))
			require.Equal(t, tc.modern, bool(omitted))
		})
	}
}

func dimensionViewItem(key string) *core.Artifact {
	root := &core.ModTreeNode{Name: "app", Parent: &core.ModTreeNode{}}
	items := &core.ModTreeNode{Name: "items", Parent: root}
	return &core.Artifact{
		ModuleName: "app", TypeName: "AppItem", Path: []string{"app", "items"},
		Node: &core.ModTreeNode{Name: "get", Parent: items, CollectionKey: &key,
			CollectionDimension: &core.ArtifactDimension{Identifier: "Items", CollectionType: "Items", ItemType: "AppItem"}},
	}
}
