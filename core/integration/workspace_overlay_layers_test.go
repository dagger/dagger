package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (LockfileSuite) TestRepeatedNestedEditsKeepParents(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)

	// Each edit adds another scope to the cumulative touched paths. Reseeding
	// every earlier scope on each edit exhausts the overlay mount options.
	const scopes = 24
	var query strings.Builder
	query.WriteString("{ currentWorkspace {")
	for i := range scopes {
		scope := fmt.Sprintf("modules/scope-%02d", i)
		parent := filepath.Join(workdir, scope, "generated")
		require.NoError(t, os.MkdirAll(parent, 0o755))
		require.NoError(t, os.Chmod(filepath.Dir(parent), 0o750))
		require.NoError(t, os.Chmod(parent, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(parent, "keep.txt"), []byte("keep"), 0o600))
		fmt.Fprintf(&query, "withNewFile(path: %q, contents: %q) {", scope+"/generated/client.go", "generated")
	}
	query.WriteString("export")
	query.WriteString(strings.Repeat("}", scopes+2))

	queryPath := writeQueryDoc(t, workdir, "edits.graphql", query.String())
	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	for i := range scopes {
		parent := filepath.Join(workdir, fmt.Sprintf("modules/scope-%02d/generated", i))
		for name, want := range map[string]string{"client.go": "generated", "keep.txt": "keep"} {
			contents, err := os.ReadFile(filepath.Join(parent, name))
			require.NoError(t, err)
			require.Equal(t, want, string(contents))
		}
		for dir, want := range map[string]os.FileMode{filepath.Dir(parent): 0o750, parent: 0o700} {
			info, err := os.Stat(dir)
			require.NoError(t, err)
			require.Equal(t, want, info.Mode().Perm(), "%s permissions", dir)
		}
	}
}
