package core

import (
	"context"
	"fmt"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceAPISuite) TestHostWorkspaceOverlaySearchPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// Run the host search with ripgrep, as TestHostWorkspaceOverlaySearchAndGlob
	// does. The CI runner only has BusyBox grep, which lacks byte offsets.
	base := nativeWorkspaceBase(t, c).
		WithExec([]string{"apk", "add", "ripgrep"}).
		WithNewFile("existing.txt", "needle host\n")

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
			out, err := base.With(daggerQuery(query)).Stdout(ctx)
			if tc.missing {
				requireErrOut(t, err, "No such file or directory")
				requireErrOut(t, err, "missing.txt")
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, `{"currentWorkspace":{"withNewFile":{"search":`+tc.want+`}}}`, out)
		})
	}
}
