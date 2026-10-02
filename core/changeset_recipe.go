package core

import (
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

// pureRecipeFields are the fields a pure changeset recipe may call: pure
// Directory, File and Changeset operations, whose results follow from their
// inputs alone. They are what file-editing tools build changesets from (e.g.
// vito/editor's edit, mv and cp: withFile, withReplaced, withoutFile, ...).
//
// It is an allowlist: anything else, such as a container exec, a service, a
// git or HTTP fetch, or a module function, may be expensive or impossible to
// replay.
var pureRecipeFields = map[string]bool{
	"Query.directory": true,
	"Query.file":      true,
	"Query.blob":      true,
	"Query.changeset": true,

	"Directory.directory":        true,
	"Directory.file":             true,
	"Directory.filter":           true,
	"Directory.withFile":         true,
	"Directory.withFiles":        true,
	"Directory.withNewFile":      true,
	"Directory.withoutFile":      true,
	"Directory.withoutFiles":     true,
	"Directory.withDirectory":    true,
	"Directory.withNewDirectory": true,
	"Directory.withoutDirectory": true,
	"Directory.withSymlink":      true,
	"Directory.withTimestamps":   true,
	"Directory.withPatch":        true,
	"Directory.withPatchFile":    true,
	"Directory.withChanges":      true,
	"Directory.diff":             true,
	"Directory.changes":          true,

	"File.withName":       true,
	"File.withReplaced":   true,
	"File.withTimestamps": true,

	"Changeset.before":         true,
	"Changeset.after":          true,
	"Changeset.filter":         true,
	"Changeset.withChangeset":  true,
	"Changeset.withChangesets": true,
}

// pureWorkspaceReads are the reads of the bound workspace a pure recipe may
// start from.
var pureWorkspaceReads = map[string]bool{
	"Workspace.directory": true,
	"Workspace.file":      true,
}

// impureChangesetRecipe reports the first call that keeps a changeset's Before
// and After recipes from being pure, or "" when they consist only of the
// workspace's own state (leaves, by recipe digest: the workspace and the trees
// it reads from) and pure operations on it (pureRecipeFields). Such a
// changeset is cheap and safe to replay as After.changes(from: Before), which
// drops whatever returned it — a tool call — from the recipe.
//
// The decision is structural: it walks the recipes, evaluating nothing. A read
// such as Workspace.directory may return the workspace's root tree itself, so
// a recipe can reach the workspace's state without passing through a
// Workspace at all; either way the walk stops there, since that recipe is the
// conversation's already.
func impureChangesetRecipe(leaves map[digest.Digest]bool, recipes ...*call.ID) string {
	memo := map[digest.Digest]string{}
	for _, id := range recipes {
		if blocker := impureRecipe(id, leaves, memo); blocker != "" {
			return blocker
		}
	}
	return ""
}

func impureRecipe(id *call.ID, leaves map[digest.Digest]bool, memo map[digest.Digest]string) string {
	if id == nil {
		return "no recipe"
	}
	if leaves[stableIDDigest(id)] {
		return ""
	}
	if typ := id.Type(); typ != nil && typ.NamedType() == "Workspace" {
		return "another workspace"
	}
	if id.IsHandle() {
		// A handle names an existing result, not a recipe to inspect.
		return "a result handle"
	}
	dgst := id.Digest()
	if blocker, ok := memo[dgst]; ok {
		return blocker
	}
	// Provisionally impure, should a malformed recipe loop back here.
	memo[dgst] = "a cycle"
	blocker := impureCall(id, leaves, memo)
	memo[dgst] = blocker
	return blocker
}

func impureCall(id *call.ID, leaves map[digest.Digest]bool, memo map[digest.Digest]string) string {
	receiver := id.Receiver()
	parent := "Query"
	if receiver != nil {
		if receiver.Type() == nil {
			return "an untyped receiver"
		}
		parent = receiver.Type().NamedType()
	}
	key := parent + "." + id.Field()
	if id.Module() != nil {
		return key + " (a module function)"
	}
	switch {
	case parent == "Workspace":
		if !pureWorkspaceReads[key] {
			return key
		}
	case !pureRecipeFields[key]:
		return key
	}
	if receiver != nil {
		if blocker := impureRecipe(receiver, leaves, memo); blocker != "" {
			return blocker
		}
	}
	for _, arg := range id.Args() {
		if blocker := impureLiteral(arg.Value(), leaves, memo); blocker != "" {
			return blocker
		}
	}
	return ""
}

func impureLiteral(lit call.Literal, leaves map[digest.Digest]bool, memo map[digest.Digest]string) string {
	switch v := lit.(type) {
	case *call.LiteralID:
		return impureRecipe(v.Value(), leaves, memo)
	case *call.LiteralList:
		for _, item := range v.Values() {
			if blocker := impureLiteral(item, leaves, memo); blocker != "" {
				return blocker
			}
		}
	case *call.LiteralObject:
		for _, field := range v.Args() {
			if field == nil {
				continue
			}
			if blocker := impureLiteral(field.Value(), leaves, memo); blocker != "" {
				return blocker
			}
		}
	}
	return ""
}
