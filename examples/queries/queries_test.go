package queries

import (
	"os"
	"testing"

	"github.com/dagger/cloak/testutil"
	"github.com/stretchr/testify/require"
)

type testOpts struct {
	opts *testutil.QueryOptions
}

func TestQueries(t *testing.T) {
	t.Parallel()

	tests := map[string]*testOpts{
		"simple.graphql":       nil,
		"multi.graphql":        nil,
		"git.graphql":          nil,
		"docker_build.graphql": nil,
		"params.graphql": {
			opts: &testutil.QueryOptions{
				Variables: map[string]any{"version": "v0.2.0"},
			},
		},
		"secret.graphql": {
			opts: &testutil.QueryOptions{
				Secrets: map[string]string{
					"secret": "test secret",
				},
			},
		},
		"targets.graphql": {
			opts: &testutil.QueryOptions{
				Operation: "test",
			},
		},
	}
	for f, test := range tests {
		if test == nil {
			test = &testOpts{}
		}
		query, err := os.ReadFile(f)
		require.NoError(t, err, f)
		res := map[string]interface{}{}
		err = testutil.Query(string(query), &res, test.opts)
		require.NoError(t, err, "file: %s", f)
	}
}
