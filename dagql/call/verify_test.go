package call

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestRecipeReferences(t *testing.T) {
	typ := &ast.Type{NamedType: "LLM", NonNull: true}
	dep := New().Append(typ, "snapshot")
	mod := New().Append(&ast.Type{NamedType: "Module"}, "module")
	receiver := New().Append(typ, "llm")
	id := receiver.Append(typ, "withTools",
		WithModule(NewModule(mod, "tools", "ref", "pin")),
		WithArgs(NewArgument("object", NewLiteralList(NewLiteralObject(NewArgument("lazy", NewLiteralID(dep), false))), false)),
		WithImplicitInputs(NewArgument("input", NewLiteralID(dep), false)),
		WithView("test"),
	).With(WithContentDigest(digest.Digest("sha256:content")))
	refs, err := RecipeReferences(id.Call())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		receiver.Digest().String(),
		mod.Digest().String(),
		dep.Digest().String(), // nested in a list of objects
		dep.Digest().String(), // implicit input
	}, refs)
}
