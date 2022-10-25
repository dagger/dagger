package queries

import (
	"os"
	"testing"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/stretchr/testify/require"
)

type testOpts struct {
	opts *testutil.QueryOptions
}

func TestQueries(t *testing.T) {
	t.Parallel()

	tests := map[string]*testOpts{
		"simple.graphql": nil,
		"multi.graphql":  nil,
		"git.graphql":    nil,
		// TODO(vito): bring this back once we figure out the API (#3151)
		// "docker_build.graphql": nil,
		"params.graphql": {
			opts: &testutil.QueryOptions{
				Variables: map[string]any{"version": "v0.2.0"},
			},
		},
		"targets.graphql": {
			opts: &testutil.QueryOptions{
				Operation: "working",
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
