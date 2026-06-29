package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFunctionName(t *testing.T) {
	type example struct {
		name string
		args []string
		want string
	}
	for _, test := range []example{
		{
			name: "invalid flag without equals",
			args: []string{"--cloud", "test", "specific"},
			want: "",
		},
		{
			name: "valid flag with equals before function",
			args: []string{"--cloud=true", "test", "specific"},
			want: "test",
		},
		{
			name: "no flags before function",
			args: []string{"test", "--race", "specific"},
			want: "test",
		},
		{
			name: "multiple valid flags before function",
			args: []string{"--cloud=true", "--value=test", "test", "--race=true", "specific"},
			want: "test",
		},
		{
			name: "invalid second flag without equals",
			args: []string{"--cloud=true", "--race", "test", "specific"},
			want: "",
		},
		{
			name: "empty args",
			args: []string{},
			want: "",
		},
		{
			name: "only valid flags no function",
			args: []string{"--cloud=true", "--race=true"},
			want: "",
		},
		{
			name: "single dash flags before function",
			args: []string{"-m", "dev", "test", "specific"},
			want: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, functionName(test.args))
		})
	}
}

// TestLoadTypeDefsScopedQuery checks the two query variants differ only by the
// include argument.
func TestLoadTypeDefsScopedQuery(t *testing.T) {
	require.Contains(t, loadTypeDefsScopedQuery, "$include: [String!]")
	require.Contains(t, loadTypeDefsScopedQuery, "include: $include")
	require.NotContains(t, loadTypeDefsQuery, "$include")
}

func TestWorkspaceModuleScope(t *testing.T) {
	type example struct {
		name string
		args []string
		want []string
	}
	for _, test := range []example{
		{
			name: "first positional token is the scope",
			args: []string{"my-mod", "container-echo", "--string-arg", "hi"},
			want: []string{"my-mod"},
		},
		{
			name: "self-contained flags are skipped",
			args: []string{"--json=true", "my-mod", "fn"},
			want: []string{"my-mod"},
		},
		{
			name: "ambiguous flag sends no scope",
			args: []string{"-o", "out.txt", "my-mod", "fn"},
			want: nil,
		},
		{
			name: "no args sends no scope",
			args: nil,
			want: nil,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, workspaceModuleScope(test.args))
		})
	}
}
