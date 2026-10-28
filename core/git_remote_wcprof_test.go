package core

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/util/gitutil"
)

// childOpIdents returns the idents of op's recorded child ops, by class.
func childOpIdents(t *testing.T, op *wcprof.Op) map[string][]string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	children := map[string][]string{}
	for _, ev := range events {
		if ev.Type == "op" && ev.ParentID == op.ID() {
			class := header.Strings[ev.ClassID]
			children[class] = append(children[class], header.Strings[ev.IdentID])
		}
	}
	return children
}

// A mirror's fetch from its remote is its own op under the mirror's hold,
// identified by the remote as the hold is, and only when a fetch runs.
func TestGitMirrorFetchRecorded(t *testing.T) {
	t.Parallel()
	source, base, _ := gitMirrorTestSource(t)
	repo, git, _ := gitMirrorTestRepo(t, source)
	ctx, hold := profiledOp(t, "test.mirror.locked")

	refs, err := gitRefsToFetch(ctx, git, 1, []*RemoteGitRef{{Ref: &gitutil.Ref{SHA: base}}})
	require.NoError(t, err)
	_, err = repo.fetchObjects(ctx, git, 1, false, refs)
	require.NoError(t, err)
	// The commit is in the mirror now: nothing to fetch, and no op.
	refs, err = gitRefsToFetch(ctx, git, 1, []*RemoteGitRef{{Ref: &gitutil.Ref{SHA: base}}})
	require.NoError(t, err)
	_, err = repo.fetchObjects(ctx, git, 1, false, refs)
	require.NoError(t, err)
	hold.End(wcprof.OutcomeOK)

	require.Equal(t, []string{remoteGitLockPrefix + repo.URL.RedactedRemote()}, childOpIdents(t, hold)["git.mirror.fetch"])
}
