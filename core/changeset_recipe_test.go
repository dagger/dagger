package core

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

func TestWorkspaceTreePrefix(t *testing.T) {
	typ := func(name string) *ast.Type { return &ast.Type{NamedType: name, NonNull: true} }
	str := func(name, val string) call.IDOpt {
		return call.WithArgs(call.NewArgument(name, call.NewLiteralString(val), false))
	}
	idArg := func(name string, id *call.ID) call.IDOpt {
		return call.WithArgs(call.NewArgument(name, call.NewLiteralID(id), false))
	}
	wsID := call.New().Append(typ("Workspace"), "currentWorkspace")
	tree := call.New().Append(typ("GitRepository"), "git", str("url", "https://example.com/repo")).
		Append(typ("GitRef"), "head").
		Append(typ("Directory"), "tree")
	state := map[digest.Digest]bool{wsID.Digest(): true, tree.Digest(): true}
	ws := &Workspace{Cwd: "sub"}
	ws.SetHostPath("/home/me/repo")
	prefix := func(id *call.ID) string {
		p, ok := workspaceTreePrefix(id, state, ws)
		if !ok {
			return "<none>"
		}
		return p
	}
	empty := call.New().Append(typ("Directory"), "directory")

	for _, tc := range []struct {
		name string
		id   *call.ID
		want string
	}{
		{"the root tree", tree, "."},
		{"a workspace read of the cwd", wsID.Append(typ("Directory"), "directory", str("path", ".")), "sub"},
		{"a workspace read of the root", wsID.Append(typ("Directory"), "directory", str("path", "/")), "."},
		{"a workspace read below the cwd", wsID.Append(typ("Directory"), "directory", str("path", "pkg")), "sub/pkg"},
		{"a subdirectory of the root tree", tree.Append(typ("Directory"), "directory", str("path", "sub")), "sub"},
		{
			"a filtered read",
			empty.Append(typ("Directory"), "withDirectory", str("path", "/"),
				idArg("source", tree.Append(typ("Directory"), "directory", str("path", "sub")))),
			"sub",
		},
		{
			"edits keep the layout",
			tree.Append(typ("Directory"), "directory", str("path", "sub")).
				Append(typ("Directory"), "withNewFile", str("path", "x")).
				Append(typ("Directory"), "withoutFile", str("path", "y")),
			"sub",
		},
		{
			"a host read",
			call.New().Append(typ("Host"), "host").Append(typ("Directory"), "directory", str("path", "/home/me/repo/sub")),
			"sub",
		},
		{
			"a host read elsewhere",
			call.New().Append(typ("Host"), "host").Append(typ("Directory"), "directory", str("path", "/home/me/other")),
			"<none>",
		},
		{
			"a container's tree",
			call.New().Append(typ("Container"), "container").Append(typ("Directory"), "directory", str("path", "/src")),
			"<none>",
		},
		{"another workspace", call.New().Append(typ("Workspace"), "other").Append(typ("Directory"), "directory", str("path", ".")), "<none>"},
		{"an escape", tree.Append(typ("Directory"), "directory", str("path", "../x")), "<none>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, prefix(tc.id))
		})
	}
}
