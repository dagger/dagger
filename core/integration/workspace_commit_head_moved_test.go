package core

// Coverage for concurrent saves: staged commits are replayed when another
// session advances the local branch before they land.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// sessionQueryPayload renders a GraphQL document as the JSON body the session
// endpoint expects.
func sessionQueryPayload(t testing.TB, query string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	return string(body)
}

// TestWorkspaceCommitExportHeadMoved replays the staged stack when the local
// branch moved after it was staged, retaining both sessions' commits.
//
// Both queries are POSTed to one `dagger run` session, so they are the *same*
// client: the second query stages against the session's cached view of the
// checkout, exactly as a long-lived `dagger agent` session would, while the
// checkout itself has already advanced.
func (WorkspaceSuite) TestWorkspaceCommitExportHeadMoved(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c).WithExec([]string{"apk", "add", "curl"})

	stage := sessionQueryPayload(t, `{
  currentWorkspace {
    withNewFile(path: "c.txt", contents: "c1") {
      withCommit(message: "staged c", date: "`+commitTestDate+`") {
        withNewFile(path: "d.txt", contents: "d1") {
          withCommit(message: "staged d", date: "`+commitTestDate+`") {
            git { head { commit } }
          }
        }
      }
    }
  }
}`)
	save := sessionQueryPayload(t, `{
  currentWorkspace {
    withNewFile(path: "c.txt", contents: "c1") {
      withCommit(message: "staged c", date: "`+commitTestDate+`") {
        withNewFile(path: "d.txt", contents: "d1") {
          withCommit(message: "staged d", date: "`+commitTestDate+`") {
            export
          }
        }
      }
    }
  }
}`)

	script := fmt.Sprintf(`set -e
exec 2>&1
q() {
  curl -sS -u "$DAGGER_SESSION_TOKEN:" -H 'content-type: application/json' \
    -d "$1" "http://127.0.0.1:$DAGGER_SESSION_PORT/query" || echo "curl rc=$?"
  echo
}
q %s
echo local > local.txt
git add local.txt
git commit -q -m "local commit"
q %s
`, shellSingleQuote(stage), shellSingleQuote(save))

	ran := base.WithExec([]string{"sh", "-c", script}, dagger.ContainerWithExecOpts{
		ExperimentalPrivilegedNesting: true,
	})

	out, err := ran.Stdout(ctx)
	require.NoError(t, err)
	require.NotContains(t, out, `"errors"`)
	require.Contains(t, out, `"export":{}`)

	// The local commit remains in history and both staged commits are replayed
	// on top of it, in order. Both sessions' files land and the checkout is clean.
	require.Equal(t, "staged d\nstaged c\nlocal commit", gitOut(ctx, t, ran, "log", "-3", "--pretty=%s"))
	require.Equal(t, "", gitOut(ctx, t, ran, "status", "--porcelain"))
	entries, err := ran.Directory(".").Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "local.txt")
	require.Contains(t, entries, "c.txt")
	require.Contains(t, entries, "d.txt")
}

// shellSingleQuote wraps a string in single quotes for safe embedding in a
// /bin/sh script.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
