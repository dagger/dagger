package core

import (
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func addressPlanCollectionArtifact(module, test string, fields ...string) *Artifact {
	return &Artifact{
		ModuleName: "go", TypeName: "Check",
		Path: append([]string{"go", "modules", "tests"}, fields...),
		DimensionKeys: []*ArtifactDimensionKey{
			{Dimension: "/go/modules", Key: module},
			{Dimension: "/go/modules/tests", Key: test},
		},
		DimensionNames: map[string]string{"/go/modules": "go-module", "/go/modules/tests": "go-test"},
	}
}

func TestArtifactAddressMinimumCollectionURI(t *testing.T) {
	for _, test := range []struct {
		name string
		rows []*Artifact
		want string
	}{
		{
			name: "unique child drops parent",
			rows: []*Artifact{addressPlanCollectionArtifact("api", "unit", "check"), addressPlanCollectionArtifact("cli", "lint", "check")},
			want: "dag+check://?go-test=unit",
		},
		{
			name: "duplicate child keeps parent",
			rows: []*Artifact{
				addressPlanCollectionArtifact("api", "unit", "check"),
				addressPlanCollectionArtifact("cli", "unit", "check"),
				addressPlanCollectionArtifact("api", "lint", "check"),
			},
			want: "dag+check://?go-module=api&go-test=unit",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := &Artifacts{Entries: test.rows, CompleteSchema: true}
			catalog.PrepareAddressPlans()
			target := catalog.Entries[0]
			require.True(t, target.HasAddressPlan(true))
			uri, err := target.URI(ArtifactURIOpts{TypeAssertion: true, DimensionKeys: true, Prune: true})
			require.NoError(t, err)
			require.Equal(t, test.want, uri)
			require.Equal(t, "check", target.StaticDimensionKey("name"))
		})
	}
}

func TestArtifactAddressCanonicalNameURI(t *testing.T) {
	target := addressPlanCollectionArtifact("api", "unit", "healthCheck", "run")
	catalog := &Artifacts{CompleteSchema: true, Entries: []*Artifact{
		target,
		addressPlanCollectionArtifact("api", "unit", "healthCheck", "stale"),
		addressPlanCollectionArtifact("cli", "lint", "healthCheck", "run"),
	}}
	catalog.PrepareAddressPlans()
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "/go/modules/tests", Key: "unit"},
		{Dimension: "name", Key: "healthCheck.run"},
	}, target.addressKeys(true).Keys)
	uri, err := target.URI(ArtifactURIOpts{TypeAssertion: true, DimensionKeys: true, Prune: true})
	require.NoError(t, err)
	require.Equal(t, "dag+check://?go-test=unit&name=healthCheck.run", uri)
}

func TestArtifactAddressPlansPersistence(t *testing.T) {
	target := &Artifact{ModuleName: "go", TypeName: "Check", Path: []string{"go", "unit"}}
	catalog := &Artifacts{CompleteSchema: true, Entries: []*Artifact{
		target,
		{ModuleName: "go", TypeName: "Check", Path: []string{"go", "lint"}},
		{ModuleName: "docs", TypeName: "File", Path: []string{"docs", "unit"}},
	}}
	catalog.PrepareAddressPlans()
	require.Equal(t, []flattening.Coordinate{{Dimension: "name", Key: "unit"}}, target.addressKeys(true).Keys)
	require.Equal(t, []flattening.Coordinate{{Dimension: "module", Key: "go"}, {Dimension: "name", Key: "unit"}}, target.addressKeys(false).Keys)

	// A load-failure marker permits persistence without a module tree. It does
	// not change the address coordinates or their prepared minimum plans.
	persisted := target.Clone()
	persisted.LoadFailure = &ModuleLoadFailure{}
	encoded, err := persisted.EncodePersistedObject(t.Context(), nil)
	require.NoError(t, err)
	decoded, err := (&Artifact{}).DecodePersistedObject(t.Context(), dagql.NewPersistDecodeContext(nil, 0, nil), encoded.JSON)
	require.NoError(t, err)
	restored := decoded.(*Artifact)
	require.Equal(t, target.addressPlans, restored.addressPlans)
	for _, test := range []struct {
		typed bool
		want  string
	}{
		{typed: true, want: "dag+check://?name=unit"},
		{want: "dag://?module=go&name=unit"},
	} {
		uri, err := restored.URI(ArtifactURIOpts{TypeAssertion: test.typed, DimensionKeys: true, Prune: true})
		require.NoError(t, err)
		require.Equal(t, test.want, uri)
	}
}

func TestArtifactAddressPlansRejectFilteredCatalog(t *testing.T) {
	for _, test := range []struct {
		name     string
		selector ArtifactSelector
		complete bool
	}{
		{name: "incomplete"},
		{name: "path", selector: ArtifactSelector{Paths: []string{"go/unit"}}, complete: true},
		{name: "empty path selection", selector: ArtifactSelector{Paths: []string{}}, complete: true},
		{name: "dimension key", selector: ArtifactSelector{Dimensions: []ArtifactDimensionFilter{{Dimension: "name", Keys: []string{"unit"}}}}, complete: true},
		{name: "dimension alternatives", selector: ArtifactSelector{DimensionAlternatives: [][]string{{"name"}}}, complete: true},
		{name: "excluded URI", selector: ArtifactSelector{ExcludedURIs: []string{"dag://?name=lint"}}, complete: true},
		{name: "collection path", selector: ArtifactSelector{CollectionPaths: []string{"go/modules"}}, complete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &Artifact{ModuleName: "go", TypeName: "Check", Path: []string{"go", "unit"}}
			catalog := &Artifacts{Entries: []*Artifact{target}, Selector: test.selector, CompleteSchema: test.complete}
			catalog.PrepareAddressPlans()
			require.False(t, target.HasAddressPlan(true))
			require.False(t, target.HasAddressPlan(false))
		})
	}
}

func TestArtifactAddressPlansSurviveSelection(t *testing.T) {
	target := &Artifact{ModuleName: "go", TypeName: "Check", Path: []string{"go", "unit"}}
	catalog := &Artifacts{CompleteSchema: true, Entries: []*Artifact{
		target, {ModuleName: "go", TypeName: "Check", Path: []string{"go", "lint"}},
	}}
	filtered := catalog.FilterPath(target.Path)
	require.Len(t, filtered.Entries, 1)
	filtered.PrepareAddressPlans()
	require.False(t, filtered.Entries[0].HasAddressPlan(true), "one filtered row cannot prove a type-only address")

	catalog.PrepareAddressPlans()
	filtered = catalog.FilterPath(target.Path)
	filtered.PrepareAddressPlans()
	require.Equal(t, target.addressPlans, filtered.Entries[0].addressPlans)
	uri, err := filtered.Entries[0].URI(ArtifactURIOpts{TypeAssertion: true, DimensionKeys: true, Prune: true})
	require.NoError(t, err)
	require.Equal(t, "dag+check://?name=unit", uri)
}

func TestArtifactAddressTypeFilteredCatalog(t *testing.T) {
	catalog := &Artifacts{CompleteSchema: true, Entries: []*Artifact{
		{ModuleName: "go", TypeName: "Check", Path: []string{"go", "unit"}},
		{ModuleName: "go", TypeName: "File", Path: []string{"go", "unit"}},
	}}
	filtered := catalog.FilterTypes([]string{"Check"}, false)
	require.Len(t, filtered.Entries, 1)
	filtered.PrepareAddressPlans()
	target := filtered.Entries[0]
	require.True(t, target.HasAddressPlan(true))
	require.Empty(t, target.addressKeys(true).Keys)
	require.False(t, target.HasAddressPlan(false), "other types remain possible without a type constraint")
	uri, err := target.URI(ArtifactURIOpts{TypeAssertion: true, DimensionKeys: true, Prune: true})
	require.NoError(t, err)
	require.Equal(t, "dag+check://", uri)
}

func TestArtifactAddressUntypedDuplicateCoordinates(t *testing.T) {
	catalog := &Artifacts{CompleteSchema: true, Entries: []*Artifact{
		{ModuleName: "go", TypeName: "Check", Path: []string{"go", "unit"}},
		{ModuleName: "go", TypeName: "File", Path: []string{"go", "unit"}},
		{ModuleName: "go", TypeName: "Check", Path: []string{"go", "lint"}},
	}}
	catalog.PrepareAddressPlans()
	for _, target := range catalog.Entries[:2] {
		require.True(t, target.HasAddressPlan(true))
		require.False(t, target.HasAddressPlan(false), "untyped duplicate coordinates are ambiguous")
	}
	unique := catalog.Entries[2]
	require.True(t, unique.HasAddressPlan(false), "ambiguous rows do not invalidate a distinct row")
	uri, err := unique.URI(ArtifactURIOpts{DimensionKeys: true, Prune: true})
	require.NoError(t, err)
	require.Equal(t, "dag://?name=lint", uri)
}
