package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceAPISuite) TestHostWorkspaceOverlaySearchPaths(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "existing.txt"), []byte("needle host\n"), 0o600))

	for _, tc := range []struct {
		name    string
		paths   string
		want    string
		missing bool
	}{
		{"overlay file", `["/created/new.txt"]`, `[{"filePath":"created/new.txt"}]`, false},
		{"mixed sources", `["existing.txt", "/created/new.txt"]`, `[{"filePath":"created/new.txt"},{"filePath":"existing.txt"}]`, false},
		{"overlay parent", `["created"]`, `[{"filePath":"created/new.txt"}]`, false},
		{"unrelated missing path", `["created/new.txt", "missing.txt"]`, "", true},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			query := fmt.Sprintf(`{
  currentWorkspace {
    withNewFile(path: "created/new.txt", contents: "needle overlay\n") {
      search(paths: %s, pattern: "needle") { filePath }
    }
  }
}`, tc.paths)
			queryPath := writeQueryDoc(t, workdir, tc.name+".graphql", query)
			out, err := hostDaggerOutput(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
			if tc.missing {
				require.ErrorContains(t, err, "No such file or directory")
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, `{"currentWorkspace":{"withNewFile":{"search":`+tc.want+`}}}`, string(out))
		})
	}
}
