package core

import (
	"context"
	"os"
	"path/filepath"

	"dagger.io/dagger"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// A changeset must not overwrite independently edited content that it does not
// declare changed, even when its structural diff contains metadata-only entries.
func (ChangesetSuite) TestWithChangesPreservesStatOnlyPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	before := c.Directory().WithNewFile("target/keep.txt", "keep").WithTimestamps(1700000000)
	after := before.WithTimestamps(1700000010).WithNewFile("target/new.txt", "new")
	changes := after.Changes(before)

	modified, err := changes.ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Empty(t, modified)
	added, err := changes.AddedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"target/new.txt"}, added)

	// Establish the metadata-only structural entry independently of the
	// semantic paths: the timestamp change leaves keep.txt's bytes unchanged.
	structural, err := before.Diff(after).Directory("target").Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, structural, "keep.txt")

	// Import the independently edited host file through a fresh filesync call.
	// Checking it before application separates stale capture from reapplication.
	workdir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workdir, "target"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "target", "keep.txt"), []byte("edited on disk"), 0o644))
	target := c.Host().Directory(workdir, dagger.HostDirectoryOpts{NoCache: true})
	contents, err := target.File("target/keep.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "edited on disk", contents)

	result := target.WithChanges(changes)
	addedContents, err := result.File("target/new.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "new", addedContents)
	contents, err = result.File("target/keep.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "edited on disk", contents, "applying declared changes must preserve an independently edited, undeclared path")
}
