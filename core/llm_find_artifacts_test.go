package core

import (
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

// findArtifactsFixture is a scope with a workspace check, a module that
// failed to load, and a bound roster whose members are a collection.
func findArtifactsFixture() (*Artifacts, func(*Artifact) string) {
	member := &ArtifactDimension{
		Kind: "COLLECTION", CollectionType: "RosterMembers", Identifier: "/roster/members",
		Name: "member", QualifiedName: "roster-members-member", ItemType: "RosterMember",
		KeyName: "name", KeyDescription: "The member's name.\n\nUnique on the roster.",
	}
	root := &ModTreeNode{Name: "roster"}
	members := &ModTreeNode{Parent: root, Name: "members", Description: "The roster's members."}
	get := &ModTreeNode{Parent: members, Name: "get", Description: "The member with this name.", CollectionDimension: member}
	// Hard-wrapped, as doc strings often are: only the first paragraph is
	// shown, on one line.
	head := &ModTreeNode{Parent: get, Name: "head", Description: "The member's committed\nhistory.\n\nIts HEAD."}
	lint := &ModTreeNode{Parent: &ModTreeNode{Name: "go"}, Name: "lint", Description: "Lint the Go code."}
	scope := &Artifacts{Entries: []*Artifact{
		{ModuleName: "broken", Path: []string{"broken", "load"}, TypeName: "Check", Directives: []string{"check"},
			LoadFailure: &ModuleLoadFailure{Name: "broken", Message: "loading module \"broken\": boom\nsyntax error"}},
		{ModuleName: "go", Path: []string{"go", "lint"}, TypeName: "Check", Directives: []string{"check"}, Node: lint},
		{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "RosterMembers", Node: members},
		{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "RosterMember", Node: get},
		{ModuleName: "roster", Path: []string{"roster", "members", "head"}, TypeName: "GitRef", Node: head},
	}}
	tag := func(entry *Artifact) string {
		if entry.ModuleName == "roster" {
			return "tool Roster, live"
		}
		return ""
	}
	return scope, tag
}

func TestFindArtifactsOverview(t *testing.T) {
	scope, tag := findArtifactsFixture()
	require.Equal(t, `Artifact types in scope (schema paths: modules):
  Check (1): go
  GitRef (1): roster [tool]
  RosterMember (1): roster [tool]
  RosterMembers (1): roster [tool]

Collections (dimensions whose keys address items):
  member: RosterMember items of RosterMembers, keyed by name (The member's name)

Modules that failed to load (they may hide artifacts of any type):
  dag+check://broken/load — LOAD ERROR: loading module "broken": boom
    syntax error

Narrow with type (e.g. type: "Check"), address (e.g. address: "<module>/**") or keys. view: "items" lists every fully keyed address; dimension: "<collection>" lists its keys.`,
		renderArtifactOverview(artifactOverviewOf(scope, tag)))

	require.Equal(t,
		"No artifacts in scope: neither the workspace's modules nor your bound tool modules have any.",
		renderArtifactOverview(artifactOverviewOf(&Artifacts{}, tag)))
}

func TestFindArtifactsPathRows(t *testing.T) {
	scope, tag := findArtifactsFixture()
	render := func(selection *Artifacts) string {
		t.Helper()
		selection, err := selection.SchemaSelection()
		require.NoError(t, err)
		rows, err := artifactPathRows(scope, selection, tag)
		require.NoError(t, err)
		return renderArtifactRows(rows)
	}

	// Each path appears once, with a placeholder for every collection it
	// still needs a key for, and the placeholders are explained once.
	require.Equal(t, `dag+check://broken/load — LOAD ERROR: loading module "broken": boom
    syntax error
dag+check://go/lint — Lint the Go code.
dag+git-ref://roster/members/head?member=<name> — The member's committed history. [tool Roster, live]
dag+roster-member://roster/members?member=<name> — The member with this name. [tool Roster, live]
dag+roster-members://roster/members — The roster's members. [tool Roster, live]

Replace each <placeholder> with a key (view: "items" lists every fully keyed address):
  member=<name> — The member's name. (keys: FindArtifacts(dimension: "member"))`, render(scope))

	// A type filter keeps load failures: they could hide any type.
	require.Equal(t, `dag+check://broken/load — LOAD ERROR: loading module "broken": boom
    syntax error
dag+git-ref://roster/members/head?member=<name> — The member's committed history. [tool Roster, live]

Replace each <placeholder> with a key (view: "items" lists every fully keyed address):
  member=<name> — The member's name. (keys: FindArtifacts(dimension: "member"))`,
		render(scope.FilterTypeNames([]string{ArtifactTypeName("GitRef")})))

	checks, err := scope.FilterURI(&dagaddress.Address{HasScheme: true, Types: []string{"check"}, Path: "go/**"})
	require.NoError(t, err)
	require.Equal(t, "dag+check://go/lint — Lint the Go code.", render(checks))

	none, err := scope.FilterURI(&dagaddress.Address{Path: "nothing/**"})
	require.NoError(t, err)
	require.Equal(t,
		"No artifacts match. Call FindArtifacts with no arguments for an overview of the scope.",
		render(none))
}

func TestFindArtifactsItemRows(t *testing.T) {
	scope, tag := findArtifactsFixture()
	// Items as Artifacts.expand leaves them: keyed, with display names.
	keyed := func(entry *Artifact, key string) *Artifact {
		entry = entry.Clone()
		entry.DimensionKeys = []*ArtifactDimensionKey{{Dimension: "/roster/members", Key: key}}
		entry.DimensionNames = map[string]string{"/roster/members": "member"}
		entry.setStaticDimensions()
		return entry
	}
	expanded := &Artifacts{Entries: []*Artifact{
		scope.Entries[0],
		keyed(scope.Entries[4], "chief"),
		keyed(scope.Entries[4], "worker one"),
	}}
	rows, err := artifactItemRows(expanded, tag)
	require.NoError(t, err)
	require.Equal(t, `dag+check://broken/load — LOAD ERROR: loading module "broken": boom
    syntax error
dag+git-ref://roster/members/head?member=chief — The member's committed history. [tool Roster, live]
dag+git-ref://roster/members/head?member=worker%20one — The member's committed history. [tool Roster, live]`,
		renderArtifactRows(rows))

	// The addresses select the items they name.
	addr, err := dagaddress.Parse(rows[2].Address)
	require.NoError(t, err)
	require.Equal(t, []string{"git-ref"}, addr.Types)
	require.Equal(t, []dagaddress.Pair{{Dimension: "member", Key: "worker one", HasKey: true}}, addr.Query)
}

func TestFindArtifactsKeys(t *testing.T) {
	scope, _ := findArtifactsFixture()
	dims := scope.DimensionDefinitions()
	member := dims[0]
	require.Equal(t, "/roster/members", member.Identifier)
	require.Equal(t, `Keys of member (RosterMember, by name: The member's name.):
chief
worker
Select one with ?member=<key> in an address, or keys: {"member": ["<key>"]}.`,
		renderArtifactKeys("member", member, []string{"chief", "worker"}))
	require.Equal(t, `Keys of member (RosterMember, by name: The member's name.): none in this selection.`,
		renderArtifactKeys("member", member, nil))
}

func TestParseFindArtifactsArgs(t *testing.T) {
	args, err := parseFindArtifactsArgs(map[string]any{})
	require.NoError(t, err)
	require.False(t, args.filtered())
	require.Empty(t, args.View)

	args, err = parseFindArtifactsArgs(map[string]any{
		"address": " dag+git-ref://roster/** ", "type": "GitRef",
		"keys": map[string]any{"member": []any{"chief"}, "module": "roster"},
	})
	require.NoError(t, err)
	require.Equal(t, findArtifactsArgs{
		Address: "dag+git-ref://roster/**", Type: "GitRef",
		Keys: map[string][]string{"member": {"chief"}, "module": {"roster"}},
	}, args)
	require.True(t, args.filtered())
	require.True(t, args.keyFiltered(nil))

	// A dimension implies the keys view.
	args, err = parseFindArtifactsArgs(map[string]any{"dimension": "member"})
	require.NoError(t, err)
	require.Equal(t, findArtifactsViewKeys, args.View)

	_, err = parseFindArtifactsArgs(map[string]any{"dimension": "member", "view": "items"})
	require.ErrorContains(t, err, `dimension is only used by view "keys"`)
	_, err = parseFindArtifactsArgs(map[string]any{"view": "all"})
	require.ErrorContains(t, err, `unknown view "all"`)
	_, err = parseFindArtifactsArgs(map[string]any{"keys": []any{"chief"}})
	require.ErrorContains(t, err, "keys: expected an object")
	_, err = parseFindArtifactsArgs(map[string]any{"limit": 3})
	require.ErrorContains(t, err, `unknown argument "limit"`)
}

func TestFindArtifactsKeyFiltered(t *testing.T) {
	parse := func(s string) *dagaddress.Address {
		addr, err := dagaddress.Parse(s)
		require.NoError(t, err)
		return addr
	}
	var args findArtifactsArgs
	// Static keys filter schema paths; collection keys need items.
	require.False(t, args.keyFiltered(parse("dag://roster/**?module=roster")))
	require.False(t, args.keyFiltered(parse("dag://roster/members/head?member")))
	require.True(t, args.keyFiltered(parse("dag://roster/members/head?member=chief")))
	require.False(t, findArtifactsArgs{Keys: map[string][]string{"module": {"go"}}}.keyFiltered(nil))
}
