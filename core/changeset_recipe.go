package core

import (
	"path"
	"path/filepath"
	"strings"

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

// rootPreservingDirectoryFields are the Directory fields whose result has its
// receiver's layout: what is at a path in one is at the same path in the other.
var rootPreservingDirectoryFields = map[string]bool{
	"filter":           true,
	"withFile":         true,
	"withFiles":        true,
	"withNewFile":      true,
	"withoutFile":      true,
	"withoutFiles":     true,
	"withDirectory":    true,
	"withNewDirectory": true,
	"withoutDirectory": true,
	"withSymlink":      true,
	"withTimestamps":   true,
	"withPatch":        true,
	"withPatchFile":    true,
	"withChanges":      true,
}

// workspaceTreePrefix reports where in the workspace root the tree a recipe
// builds sits, when it is a read of the workspace: e.g. "sub" for a read of
// the workspace cwd "sub", which is how vito/editor's tools read it, or "."
// for the root itself. A changeset measured from that tree applies there.
//
// It follows the recipe structurally down to the workspace's state (leaves:
// its root tree or the workspace itself; see impureChangesetRecipe), through
// subdirectory reads and the Directory operations that keep their receiver's
// layout. A read of a host-backed workspace is a host directory read, placed
// by its path within the workspace's host path. Anything else, e.g. a tree
// read back out of a container, is not placed: ok is false.
func workspaceTreePrefix(id *call.ID, leaves map[digest.Digest]bool, ws *Workspace) (prefix string, ok bool) {
	if id == nil || id.IsHandle() {
		return "", false
	}
	if leaves[id.Digest()] {
		return ".", true
	}
	stringArg := func(name string) (string, bool) {
		arg := id.Arg(name)
		if arg == nil {
			return "", false
		}
		lit, ok := arg.Value().(*call.LiteralString)
		if !ok {
			return "", false
		}
		return lit.Value(), true
	}
	within := func(p string) (string, bool) {
		p = path.Clean(p)
		if p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(p) {
			return "", false
		}
		return p, true
	}
	receiver := id.Receiver()
	if receiver == nil || receiver.Type() == nil || id.Module() != nil {
		return "", false
	}
	switch parent, field := receiver.Type().NamedType(), id.Field(); {
	case parent == "Workspace" && (field == "directory"):
		if !leaves[stableIDDigest(receiver)] {
			return "", false
		}
		p, ok := stringArg("path")
		if !ok {
			p = "."
		}
		if strings.HasPrefix(p, "/") {
			return within(strings.TrimPrefix(p, "/"))
		}
		return within(path.Join(ws.Cwd, p))
	case parent == "Host" && field == "directory":
		hostRoot := ws.HostPath()
		p, ok := stringArg("path")
		if hostRoot == "" || !ok {
			return "", false
		}
		hostRoot = path.Clean(filepath.ToSlash(hostRoot))
		p = path.Clean(filepath.ToSlash(p))
		if p == hostRoot {
			return ".", true
		}
		rel, inside := strings.CutPrefix(p, hostRoot+"/")
		if !inside {
			return "", false
		}
		return within(rel)
	case parent != "Directory":
		return "", false
	case field == "directory":
		base, ok := workspaceTreePrefix(receiver, leaves, ws)
		sub, hasPath := stringArg("path")
		if !ok || !hasPath {
			return "", false
		}
		return within(path.Join(base, strings.TrimPrefix(sub, "/")))
	case field == "withDirectory" && receiver.Field() == "directory" && receiver.Receiver() == nil:
		// An empty directory with a tree copied to its root: the copy, e.g.
		// a filtered workspace read.
		dest, _ := stringArg("path")
		if path.Clean("/"+dest) != "/" {
			return "", false
		}
		source := id.Arg("source")
		if source == nil {
			return "", false
		}
		lit, ok := source.Value().(*call.LiteralID)
		if !ok {
			return "", false
		}
		return workspaceTreePrefix(lit.Value(), leaves, ws)
	case rootPreservingDirectoryFields[field]:
		return workspaceTreePrefix(receiver, leaves, ws)
	}
	return "", false
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
