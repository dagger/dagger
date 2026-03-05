//go:build linux

package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWSFSWriteJournalTracksMutations(t *testing.T) {
	j := newWSFSWriteJournal()

	j.markUpsert("dir/file.txt")
	upserts, deletes := j.snapshot()
	require.Equal(t, []string{"dir/file.txt"}, upserts)
	require.Empty(t, deletes)

	j.markDelete("dir/file.txt")
	upserts, deletes = j.snapshot()
	require.Empty(t, upserts)
	require.Equal(t, []string{"dir/file.txt"}, deletes)

	j.markUpsert("dir/file.txt")
	upserts, deletes = j.snapshot()
	require.Equal(t, []string{"dir/file.txt"}, upserts)
	require.Empty(t, deletes)

	j.markDelete("dir")
	upserts, deletes = j.snapshot()
	require.Empty(t, upserts)
	require.Equal(t, []string{"dir"}, deletes)

	j.markUpsert("dir/new.txt")
	upserts, deletes = j.snapshot()
	require.Equal(t, []string{"dir/new.txt"}, upserts)
	require.Empty(t, deletes)
}

func TestWSFSWriteJournalPrefersBroadUpserts(t *testing.T) {
	j := newWSFSWriteJournal()

	j.markUpsert("a/file.txt")
	j.markUpsert("a/sub/other.txt")
	j.markUpsert("a")

	upserts, deletes := j.snapshot()
	require.Equal(t, []string{"a"}, upserts)
	require.Empty(t, deletes)
}

func TestWSFSWriteJournalIsDeleted(t *testing.T) {
	j := newWSFSWriteJournal()

	j.markDelete("dir")
	require.True(t, j.isDeleted("dir"))
	require.True(t, j.isDeleted("dir/file.txt"))
	require.False(t, j.isDeleted("other"))

	j.markUpsert("dir/file.txt")
	require.False(t, j.isDeleted("dir/file.txt"))
}

func TestWSFSWriteJournalIsShadowed(t *testing.T) {
	j := newWSFSWriteJournal()

	j.markUpsert("dir/file.txt")
	require.True(t, j.isShadowed("dir/file.txt"))
	require.True(t, j.isShadowed("dir"))
	require.False(t, j.isShadowed("other"))

	j.markDelete("gone")
	require.True(t, j.isShadowed("gone"))
	require.True(t, j.isShadowed("gone/child"))
}
