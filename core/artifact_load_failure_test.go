package core

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func loadFailureArtifact(module string) *Artifact {
	return &Artifact{
		ModuleName: module, Path: []string{ArtifactTypeName(module), "load"}, TypeName: "Check",
		Directives: []string{"check"}, LoadFailure: &ModuleLoadFailure{Name: module, Message: module + " failed"},
	}
}

// schemaPaths lists the selection's paths after its schema filters, sorted.
func schemaPaths(t *testing.T, selected *Artifacts) []string {
	t.Helper()
	selected, err := selected.SchemaSelection()
	require.NoError(t, err)
	paths := []string{}
	for _, entry := range selected.Entries {
		paths = append(paths, strings.Join(entry.Path, "/"))
	}
	slices.Sort(paths)
	return paths
}

func filterURI(t *testing.T, selection *Artifacts, uri string) *Artifacts {
	t.Helper()
	addr, err := dagaddress.Parse(uri)
	require.NoError(t, err)
	selected, err := selection.FilterURI(addr)
	require.NoError(t, err)
	return selected
}

// requireRoundTrip checks that the selection's own address selects it again.
func requireRoundTrip(t *testing.T, all, selected *Artifacts) {
	t.Helper()
	require.Equal(t, schemaPaths(t, selected), schemaPaths(t, filterURI(t, all, selected.URI())), "replaying %s", selected.URI())
}

func TestArtifactLoadFailureFilters(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{ModuleName: "coll", Path: []string{"coll", "helper"}, TypeName: "Expertise"},
		{ModuleName: "my_mod3", Path: []string{"my-mod3", "helper"}, TypeName: "Expertise"},
		loadFailureArtifact("staff"),
		loadFailureArtifact("my_mod"),
		loadFailureArtifact("myMod2"),
	}}
	all.AllDimensions = all.DimensionDefinitions()
	myMod2 := ArtifactTypeName("myMod2") + "/load"
	for uri, want := range map[string][]string{
		// The module could hold what the address selects: keep its failure.
		"dag://staff/worker":                          {"staff/load"},
		"dag://staff/**":                              {"staff/load"},
		"dag://{staff,coll}/worker":                   {"staff/load"},
		"dag://{staff/worker,nope/worker}":            {"staff/load"},
		"dag://st*/worker":                            {"staff/load"},
		"dag://staff/members/head?staff-member=chief": {"staff/load"},
		"dag://?staff-member=chief":                   {"staff/load"},
		"dag://?expertise=staff/worker":               {"staff/load"},
		"dag://**?expertise=staff/worker":             {"staff/load"},
		"dag://staff/worker?expertise=staff/worker":   {"staff/load"},
		"dag+expertise://?expertise=staff/worker":     {"staff/load"},
		"dag://?module=staff":                         {"staff/load"},
		// Module names in their address form, kebab-case.
		"dag://my_mod/worker":        {"my-mod/load"},
		"dag://my-mod/worker":        {"my-mod/load"},
		"dag://?module=my_mod":       {"my-mod/load"},
		"dag://?module=my-mod":       {"my-mod/load"},
		"dag://?my-mod-member=x":     {"my-mod/load"},
		"dag://myMod2/worker":        {myMod2},
		"dag://?expertise=" + myMod2: {myMod2},
		// A module key matches in either form, for healthy modules too.
		"dag://?module=my_mod3": {"my-mod3/helper"},
		"dag://?module=my-mod3": {"my-mod3/helper"},
		// What the module cannot hold selects nothing.
		"dag://staff/worker?module=coll":           {},
		"dag://staff/worker?expertise=coll/helper": {},
		"dag://nope/worker":                        {},
		"dag://?expertise=nope/worker":             {},
		"dag://nope/coll/item?nope-item=a":         {},
		"dag://?expertise=coll/helper":             {"coll/helper"},
	} {
		t.Run("filter "+uri, func(t *testing.T) {
			selected := filterURI(t, all, uri)
			require.Equal(t, want, schemaPaths(t, selected))
			requireRoundTrip(t, all, selected)
		})
	}

	t.Run("chained filters", func(t *testing.T) {
		worker := filterURI(t, all, "dag://?expertise=staff/worker")
		for _, tc := range []struct {
			name     string
			selected *Artifacts
			want     []string
		}{
			// An empty filter matches nothing, load failures included.
			{"empty keys", worker.FilterDimensionKeys("expertise", []string{}), []string{}},
			{"disjoint keys", filterURI(t, worker, "dag://?expertise=staff/other"), []string{}},
			{"empty types", worker.FilterTypes([]string{}, false), []string{}},
			{"glob", filterURI(t, worker, "dag://**"), []string{"staff/load"}},
			{"other module", filterURI(t, worker, "dag://coll/**"), []string{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, tc.want, schemaPaths(t, tc.selected))
				requireRoundTrip(t, all, tc.selected)
			})
		}
	})

	t.Run("filterPath", func(t *testing.T) {
		require.Equal(t, []string{"staff/load"}, schemaPaths(t, all.FilterPath([]string{"staff", "worker"})))
		require.Equal(t, []string{}, schemaPaths(t, all.FilterPath([]string{"nope", "worker"})))
	})

	t.Run("dimension keys", func(t *testing.T) {
		// A failure kept by a type filter has no key in that type.
		keys, err := all.ExpandDimensionKeys(t.Context(), "expertise")
		require.NoError(t, err)
		require.Equal(t, []string{"coll/helper", "my-mod3/helper"}, keys)
	})

	// Exclusion matches a failure only by its own path and keys: excluding
	// part of a failed module keeps its failure; excluding all of it drops
	// the failure.
	for uri, want := range map[string][]string{
		"dag://staff/**":                {"coll/helper", "my-mod3/helper", "my-mod/load", myMod2},
		"dag://?module=staff":           {"coll/helper", "my-mod3/helper", "my-mod/load", myMod2},
		"dag://?module=my-mod":          {"coll/helper", "my-mod3/helper", "staff/load", myMod2},
		"dag://?module=my_mod":          {"coll/helper", "my-mod3/helper", "staff/load", myMod2},
		"dag://staff/worker":            {"coll/helper", "my-mod3/helper", "my-mod/load", myMod2, "staff/load"},
		"dag://?expertise=staff/worker": {"coll/helper", "my-mod3/helper", "my-mod/load", myMod2, "staff/load"},
	} {
		t.Run("without "+uri, func(t *testing.T) {
			addr, err := dagaddress.Parse(uri)
			require.NoError(t, err)
			selected, err := all.WithoutURI(addr)
			require.NoError(t, err)
			slices.Sort(want)
			require.Equal(t, want, schemaPaths(t, selected))
		})
	}

	t.Run("resolve names the failure", func(t *testing.T) {
		addr, err := dagaddress.Parse("dag://staff/worker")
		require.NoError(t, err)
		_, err = all.ResolveURI(t.Context(), addr, func(_ context.Context, a *Artifacts) (*Artifacts, error) { return a.SchemaSelection() })
		require.ErrorContains(t, err, `no artifact matches; module "staff" failed to load: staff failed`)
	})
}

// Several dimension names keep a load failure that any one of them keeps.
func TestArtifactLoadFailureDimensionAlternatives(t *testing.T) {
	dim := &ArtifactDimension{Kind: "COLLECTION", Identifier: "/good/items", Name: "good-item", QualifiedName: "good-items-item"}
	items := &ModTreeNode{Name: "items"}
	item := &ModTreeNode{Parent: items, Name: "get", CollectionDimension: dim}
	all := &Artifacts{Entries: []*Artifact{
		{ModuleName: "good", Path: []string{"good", "items"}, TypeName: "GoodItem", Node: item},
		loadFailureArtifact("staff"),
	}}
	all.AllDimensions = all.DimensionDefinitions()
	for _, tc := range []struct {
		names []string
		want  []string
	}{
		{[]string{"staff-member"}, []string{"staff/load"}},
		{[]string{"good-item"}, []string{"good/items"}},
		{[]string{"staff-member", "good-item"}, []string{"good/items", "staff/load"}},
		{[]string{"good-item", "staff-member"}, []string{"good/items", "staff/load"}},
	} {
		t.Run(strings.Join(tc.names, ","), func(t *testing.T) {
			require.Equal(t, tc.want, schemaPaths(t, all.FilterDimensions(tc.names)))
		})
	}
}

func TestArtifactMainLoadFailure(t *testing.T) {
	app := loadFailureArtifact("app")
	app.LoadFailure.Entrypoint = true
	app.LoadFailure.Siblings = []string{"coll"}
	all := &Artifacts{Entries: []*Artifact{
		app,
		{ModuleName: "coll", Path: []string{"coll", "helper"}, TypeName: "Expertise"},
	}}
	all.AllDimensions = all.DimensionDefinitions()
	for uri, want := range map[string][]string{
		// A shorthand path can belong to the main...
		"dag://worker":            {"app/load"},
		"dag://app/worker":        {"app/load"},
		"dag://?expertise=worker": {"app/load"},
		"dag://w*":                {"app/load"},
		// ...unless it starts with another module's name, or names another
		// module's collection.
		"dag://coll/helper":            {"coll/helper"},
		"dag://?expertise=coll/helper": {"coll/helper"},
		"dag://?coll-item=a":           {},
	} {
		t.Run(uri, func(t *testing.T) {
			selected := filterURI(t, all, uri)
			require.Equal(t, want, schemaPaths(t, selected))
			requireRoundTrip(t, all, selected)
		})
	}
}

// A module that failed to load has no known engine version, so its failure
// answers to the spelling of its name by either naming rules: a legacy module
// lists its artifacts as e-2-e-tests, a current one as e2e-tests.
func TestArtifactLoadFailureLegacySpelling(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{ModuleName: "coll", Path: []string{"coll", "helper"}, TypeName: "Expertise"},
		loadFailureArtifact("e2e-tests"),
	}}
	all.AllDimensions = all.DimensionDefinitions()
	failure := ArtifactTypeName("e2e-tests") + "/load"
	for uri, want := range map[string][]string{
		"dag://e2e-tests/lint":                         {failure},
		"dag://e-2-e-tests/lint":                       {failure},
		"dag://e-2-e-*/lint":                           {failure},
		"dag://?module=e2e-tests":                      {failure},
		"dag://?module=e-2-e-tests":                    {failure},
		"dag://e-2-e-tests/items/x?e-2-e-tests-item=a": {failure},
		"dag://coll/helper":                            {"coll/helper"},
	} {
		t.Run(uri, func(t *testing.T) {
			selected := filterURI(t, all, uri)
			require.Equal(t, want, schemaPaths(t, selected))
		})
	}
}

func TestExpandBraces(t *testing.T) {
	require.Equal(t, []string{"a"}, expandBraces("a"))
	require.Equal(t, []string{"a/x", "b/x"}, expandBraces("{a,b}/x"))
	require.Equal(t, []string{"a/b", "c"}, expandBraces("{a/b,c}"))
	require.Equal(t, []string{"a1", "a2", "b1", "b2"}, expandBraces("{a,b}{1,2}"))
	require.Equal(t, []string{"a", "b/c", "b/d"}, expandBraces("{a,b/{c,d}}"))
	require.Equal(t, []string{"{a"}, expandBraces("{a"))
}
