package daggercmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

func TestWorkspaceModuleScope(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "first positional token is the scope",
			args: []string{"my-mod", "container-echo", "--string-arg", "hi"},
			want: "my-mod",
		},
		{
			name: "self-contained flags are skipped",
			args: []string{"--json=true", "my-mod", "fn"},
			want: "my-mod",
		},
		{
			name: "ambiguous flag sends no scope",
			args: []string{"-o", "out.txt", "my-mod", "fn"},
			want: "",
		},
		{
			name: "no args sends no scope",
			args: nil,
			want: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, workspaceModuleScope(test.args))
		})
	}
}

// TestTypeDefsOperationRenameCoupling guards the coupling between
// typedefs.graphql's "query TypeDefs(" header and the rename in loadTypeDefs:
// if it drifts, scoping silently stops applying.
func TestTypeDefsOperationRenameCoupling(t *testing.T) {
	header := "query " + typeDefsOperationName + "("
	require.Contains(t, loadTypeDefsQuery, header,
		"typedefs.graphql operation header drifted from %q", header)

	scopedOp := dagql.ModuleScopeOperationName("good-mod")
	scoped := strings.Replace(loadTypeDefsQuery, header, "query "+scopedOp+"(", 1)
	require.NotEqual(t, loadTypeDefsQuery, scoped, "scoped rename did not apply")
	require.Contains(t, scoped, "query "+scopedOp+"(")

	decoded, ok := dagql.ModuleScopeFromOperationName(scopedOp)
	require.True(t, ok)
	require.Equal(t, "good_mod", decoded) // '-' sanitized to '_'; engine kebab-normalizes
}
