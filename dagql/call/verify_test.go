package call

import (
	"testing"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/protobuf/proto"
)

func TestValidateRecipeDAG(t *testing.T) {
	typ := &ast.Type{NamedType: "LLM", NonNull: true}
	dep := New().Append(typ, "snapshot")
	mod := New().Append(&ast.Type{NamedType: "Module"}, "module")
	id := New().Append(typ, "llm").Append(typ, "withTools",
		WithModule(NewModule(mod, "tools", "ref", "pin")),
		WithArgs(NewArgument("object", NewLiteralList(NewLiteralObject(NewArgument("lazy", NewLiteralID(dep), false))), false)),
		WithImplicitInputs(NewArgument("input", NewLiteralID(dep), false)),
		WithView("test"),
	).With(WithContentDigest(digest.Digest("sha256:content")))
	pb, err := id.ToProto()
	require.NoError(t, err)
	// Exercise protobuf export/import rather than validating producer-owned pointers.
	encoded, err := proto.Marshal(pb)
	require.NoError(t, err)
	var decoded callpbv1.DAG
	require.NoError(t, proto.Unmarshal(encoded, &decoded))
	require.NoError(t, ValidateRecipeDAG(decoded.GetRecipe()))
	for _, tc := range []struct {
		name   string
		mutate func(*callpbv1.RecipeDAG)
	}{
		{"missing implicit dependency", func(d *callpbv1.RecipeDAG) { delete(d.CallsByDigest, dep.Digest().String()) }},
		{"missing module", func(d *callpbv1.RecipeDAG) { delete(d.CallsByDigest, mod.Digest().String()) }},
		{"altered payload", func(d *callpbv1.RecipeDAG) { d.CallsByDigest[d.RootDigest].Field = "different" }},
		{"cycle", func(d *callpbv1.RecipeDAG) { d.CallsByDigest[d.RootDigest].ReceiverDigest = d.RootDigest }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := proto.Clone(decoded.GetRecipe()).(*callpbv1.RecipeDAG)
			tc.mutate(d)
			require.Error(t, ValidateRecipeDAG(d))
		})
	}
}
