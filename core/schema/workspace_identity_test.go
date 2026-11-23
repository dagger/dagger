package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// A workspace made from another keeps the content identity of what it holds,
// and its result carries that identity as its content digest, so a session
// that makes the same workspace another way meets an earlier session's reads
// of it. Module loading reads a module's files through a workspace made by
// withWorkdir from the session's own, whose recipe is new in every session:
// without the digest, those reads met nothing made in another session, were
// made again in every one, and the Cloud kept a call each time.
func TestWorkspaceMadeAnotherWayMeetsEarlierReads(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	file := func(t *testing.T, ctx context.Context) dagql.ObjectResult[*core.Directory] {
		t.Helper()
		var dir dagql.ObjectResult[*core.Directory]
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &dir,
			dagql.Selector{Field: "directory"},
			dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("sub/notes.txt")},
				{Name: "contents", Value: dagql.NewString("notes")},
			}}))
		return dir
	}
	read := func(t *testing.T, ctx context.Context, ws dagql.ObjectResult[*core.Workspace]) dagql.ObjectResult[*core.Directory] {
		t.Helper()
		var dir dagql.ObjectResult[*core.Directory]
		require.NoError(t, e.dag.Select(ctx, ws, &dir, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(".")},
			{Name: "include", Value: dagql.ArrayInput[dagql.String]{"notes.txt"}},
		}}))
		return dir
	}
	identity := func(t *testing.T, ctx context.Context, ws dagql.ObjectResult[*core.Workspace]) string {
		t.Helper()
		id, ok, err := ws.Self().ContentIdentity(ctx)
		require.NoError(t, err)
		require.True(t, ok)
		return id.String()
	}

	// The first session reads through a directory's workspace, its working
	// directory moved by withWorkdir.
	first := e.session(t, "first")
	var moved dagql.ObjectResult[*core.Workspace]
	require.NoError(t, e.dag.Select(first, file(t, first), &moved,
		dagql.Selector{Field: "asWorkspace"},
		dagql.Selector{Field: "withWorkdir", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("sub")}}}))
	require.NoError(t, e.cache.Evaluate(first, read(t, first, moved)))

	// A later session makes the same workspace another way, its working
	// directory given at once, and makes the same read: it is the first
	// session's, made nothing again.
	later := e.session(t, "later")
	var given dagql.ObjectResult[*core.Workspace]
	require.NoError(t, e.dag.Select(later, file(t, later), &given,
		dagql.Selector{Field: "asWorkspace", Args: []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString("sub")}}}))
	require.Equal(t, identity(t, first, moved), identity(t, later, given), "the two workspaces hold the same")
	require.True(t, read(t, later, given).HitCache(), "the later session's read is the first's, not made again")
}
