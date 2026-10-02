package core

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

func TestImpureChangesetRecipe(t *testing.T) {
	typ := func(name string) *ast.Type { return &ast.Type{NamedType: name, NonNull: true} }
	str := func(name, val string) call.IDOpt {
		return call.WithArgs(call.NewArgument(name, call.NewLiteralString(val), false))
	}
	idArg := func(name string, id *call.ID) call.IDOpt {
		return call.WithArgs(call.NewArgument(name, call.NewLiteralID(id), false))
	}

	ws := call.New().Append(typ("Directory"), "directory").Append(typ("Workspace"), "asWorkspace")
	other := call.New().Append(typ("Workspace"), "currentWorkspace")
	root := ws.Append(typ("Directory"), "directory", str("path", "/"))
	leaves := map[digest.Digest]bool{ws.Digest(): true}

	// vito/editor's edit: base.withFile(path, source.file(path).withReplaced(...))
	edit := func(ws *call.ID) (before, after *call.ID) {
		before = ws.Append(typ("Directory"), "directory", str("path", "."))
		replaced := ws.Append(typ("File"), "file", str("path", "a.txt")).
			Append(typ("File"), "withReplaced", str("search", "old"))
		after = before.Append(typ("Directory"), "withFile", idArg("source", replaced))
		return before, after
	}

	t.Run("an edit of the bound workspace is pure", func(t *testing.T) {
		before, after := edit(ws)
		require.Empty(t, impureChangesetRecipe(leaves, before, after))
	})

	t.Run("an edit of the workspace's root tree is pure", func(t *testing.T) {
		// A workspace read can return the root tree itself, whose recipe
		// is the workspace's source: here a git checkout.
		tree := call.New().Append(typ("GitRepository"), "git", str("url", "https://example.com/repo")).
			Append(typ("GitRef"), "head").
			Append(typ("Directory"), "tree")
		after := tree.Append(typ("Directory"), "withNewFile", str("path", "x"))
		require.Equal(t, "GitRef.tree", impureChangesetRecipe(leaves, tree, after))
		withRoot := map[digest.Digest]bool{ws.Digest(): true, tree.Digest(): true}
		require.Empty(t, impureChangesetRecipe(withRoot, tree, after))
	})

	t.Run("reads of another workspace are not", func(t *testing.T) {
		before, after := edit(other)
		require.Equal(t, "another workspace", impureChangesetRecipe(leaves, before, after))
	})

	t.Run("a command is not", func(t *testing.T) {
		ctr := call.New().Append(typ("Container"), "container").
			Append(typ("Container"), "withDirectory", idArg("source", root)).
			Append(typ("Container"), "withExec", str("args", "make"))
		after := ctr.Append(typ("Directory"), "directory", str("path", "."))
		require.Equal(t, "Container.directory", impureChangesetRecipe(leaves, root, after))
	})

	t.Run("a command nested in an argument is not", func(t *testing.T) {
		generated := call.New().Append(typ("Container"), "container").
			Append(typ("Container"), "withExec", str("args", "gen")).
			Append(typ("File"), "file", str("path", "out"))
		after := root.Append(typ("Directory"), "withFile", idArg("source", generated))
		require.Equal(t, "Container.file", impureChangesetRecipe(leaves, root, after))
	})

	t.Run("a module function is not", func(t *testing.T) {
		mod := call.NewModule(call.New().Append(typ("Module"), "module"), "editor", "ref", "pin")
		after := root.Append(typ("Directory"), "withNewFile", call.WithModule(mod))
		require.Equal(t, "Directory.withNewFile (a module function)", impureChangesetRecipe(leaves, root, after))
	})

	t.Run("a git fetch is not", func(t *testing.T) {
		tree := call.New().Append(typ("GitRepository"), "git", str("url", "https://example.com/repo")).
			Append(typ("GitRef"), "head").
			Append(typ("Directory"), "tree")
		after := root.Append(typ("Directory"), "withDirectory", idArg("source", tree))
		require.Equal(t, "GitRef.tree", impureChangesetRecipe(leaves, root, after))
	})

	t.Run("workspace writes are not reads", func(t *testing.T) {
		edited := ws.Append(typ("Workspace"), "withNewFile", str("path", "x"))
		after := edited.Append(typ("Directory"), "directory", str("path", "/"))
		require.Equal(t, "another workspace", impureChangesetRecipe(leaves, root, after))
	})

	t.Run("a handle is not", func(t *testing.T) {
		handle := call.NewEngineResultID(42, call.NewType(typ("Directory")))
		require.Equal(t, "a result handle", impureChangesetRecipe(leaves, root, handle))
	})
}
