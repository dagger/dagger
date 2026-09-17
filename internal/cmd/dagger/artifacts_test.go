package daggercmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/querybuilder"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactCollectionFlags(t *testing.T) {
	root := &cobra.Command{Use: "dagger"}
	root.PersistentFlags().String("env", "", "Workspace environment")
	cmd := newArtifactsCommand()
	root.AddCommand(cmd)
	registerArtifactCollectionFlags(cmd, []string{"go-module", "go-test", "type", "env"})

	child, _, err := cmd.Find([]string{"types"})
	require.NoError(t, err)
	require.NoError(t, child.ParseFlags([]string{
		"--type=Container", "--type=Directory",
		"--go-module=sdk/go", "--go-module=cmd/codegen",
		"--collection-key=go-module=lib,a=b",
		"--go-test=TestConnect", "--collection-key=type=app",
		"--env=dev",
	}))

	recorder := &artifactQueryRecorder{}
	artifacts := (&dagger.Artifacts{}).WithGraphQLQuery(querybuilder.Query().Client(recorder).Select("artifacts"))
	selected, err := selectArtifactFilters(child, artifacts)
	require.NoError(t, err)
	_, err = selected.Pretty(t.Context())
	require.ErrorIs(t, err, errArtifactQueryCaptured)
	require.Contains(t, recorder.query, `filterTypes(types:["Container","Directory"])`)
	require.Contains(t, recorder.query, `collection:"go-module"`)
	require.Contains(t, recorder.query, `keys:["lib,a=b","sdk/go","cmd/codegen"]`)
	require.Contains(t, recorder.query, `collection:"go-test"`)
	require.Contains(t, recorder.query, `keys:["TestConnect"]`)
	require.Contains(t, recorder.query, `collection:"type"`)
	require.Contains(t, recorder.query, `keys:["app"]`)
	require.Equal(t, 3, strings.Count(recorder.query, "filterCollectionKeys("))
	require.NotContains(t, recorder.query, `collection:"env"`)
}

var errArtifactQueryCaptured = errors.New("artifact query captured")

type artifactQueryRecorder struct{ query string }

func (r *artifactQueryRecorder) MakeRequest(_ context.Context, req *graphql.Request, _ *graphql.Response) error {
	r.query = req.Query
	return errArtifactQueryCaptured
}
