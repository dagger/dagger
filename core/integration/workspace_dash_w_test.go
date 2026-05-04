package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type WorkspaceDashWSuite struct{}

func TestWorkspaceDashW(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceDashWSuite{})
}

func (WorkspaceDashWSuite) TestLocalWorkspaceSelection(ctx context.Context, t *testctx.T) {
	t.Run("-W selects a workspace outside the command cwd", func(ctx context.Context, t *testctx.T) {
		cwd := t.TempDir()
		selected := t.TempDir()
		writeDashWFile(t, selected, "selected.txt", "from selected workspace")

		res := runDashWWorkspaceQuery(ctx, t, cwd, "-W", selected, "query")

		require.Equal(t, fileURL(selected), res.CurrentWorkspace.Address)
		require.Equal(t, ".", res.CurrentWorkspace.Path)
		require.Equal(t, "from selected workspace", res.CurrentWorkspace.File.Contents)
	})

	t.Run("relative -W is resolved after --workdir", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		caller := filepath.Join(root, "caller")
		selected := filepath.Join(root, "selected")
		require.NoError(t, os.MkdirAll(caller, 0o755))
		writeDashWFile(t, selected, "selected.txt", "from relative workspace")

		res := runDashWWorkspaceQuery(ctx, t, root, "--workdir", caller, "-W", "../selected", "query")

		require.Equal(t, fileURL(selected), res.CurrentWorkspace.Address)
		require.Equal(t, ".", res.CurrentWorkspace.Path)
		require.Equal(t, "from relative workspace", res.CurrentWorkspace.File.Contents)
	})
}

type dashWWorkspaceQueryResult struct {
	CurrentWorkspace struct {
		Address string `json:"address"`
		Path    string `json:"path"`
		File    struct {
			Contents string `json:"contents"`
		} `json:"file"`
	} `json:"currentWorkspace"`
}

func runDashWWorkspaceQuery(ctx context.Context, t *testctx.T, workdir string, args ...string) dashWWorkspaceQueryResult {
	t.Helper()

	cmd := hostDaggerCommand(ctx, t, workdir, append([]string{"--silent"}, args...)...)
	cmd.Stdin = strings.NewReader(`{
  currentWorkspace {
    address
    path
    file(path: "selected.txt") {
      contents
    }
  }
}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s: %w", string(out), err)
	}
	require.NoError(t, err)

	var res dashWWorkspaceQueryResult
	require.NoError(t, json.Unmarshal(out, &res))
	return res
}

func writeDashWFile(t *testctx.T, dir, name, contents string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644))
}

func fileURL(path string) string {
	return (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}).String()
}
