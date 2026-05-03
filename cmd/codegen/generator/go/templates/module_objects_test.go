package templates

import (
	"fmt"
	"go/format"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObjectUnmarshalImportedInterfaceField(t *testing.T) {
	spec := &parsedObjectType{
		name: "Test",
		fields: []*fieldSpec{
			{
				goName: "Eval",
				typeSpec: &parsedIfaceTypeReference{
					name: "EvalWorkspaceEval",
				},
			},
			{
				goName: "Evals",
				typeSpec: &parsedSliceType{
					underlying: &parsedIfaceTypeReference{
						name: "EvalWorkspaceEval",
					},
				},
			},
		},
	}

	code, err := spec.unmarshalJSONMethodCode()
	require.NoError(t, err)

	formatted, err := format.Source([]byte("package main\n\n" + fmt.Sprintf("%#v", code)))
	require.NoError(t, err)
	got := string(formatted)

	require.Contains(t, got, "Eval  *dagger.EvalWorkspaceEvalClient")
	require.Contains(t, got, "Evals []*dagger.EvalWorkspaceEvalClient")
	require.Contains(t, got, "if concrete.Eval != nil {\n\t\tr.Eval = concrete.Eval\n\t} else {\n\t\tr.Eval = nil\n\t}")
	require.Contains(t, got, "func(v *dagger.EvalWorkspaceEvalClient) dagger.EvalWorkspaceEval")
	require.Contains(t, got, "if v == nil {\n\t\t\treturn nil\n\t\t}")
}
