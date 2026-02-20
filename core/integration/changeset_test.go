package core

import (
	"context"
	"fmt"
	"testing"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ChangesetSuite struct{}

func TestChangeset(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ChangesetSuite{})
}

func (ChangesetSuite) TestChangeset(ctx context.Context, t *testctx.T) {
	t.Run("removedPaths basic", func(ctx context.Context, t *testctx.T) {
		// Create a directory with files
		c := connect(ctx, t)

		// Create initial directory with multiple files
		oldDir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2").
			WithNewFile("removed.txt", "to be removed")

		// Create new directory without one of the files
		newDir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2")

		changes := newDir.Changes(oldDir)

		removedPaths, err := changes.RemovedPaths(ctx)
		require.NoError(t, err)

		require.Contains(t, removedPaths, "removed.txt")
		require.NotContains(t, removedPaths, "file1.txt")
		require.NotContains(t, removedPaths, "dir/file2.txt")
	})

	t.Run("removedPaths with directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with subdirectories and nested files
		oldDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("remove-dir/file.txt", "remove").
			WithNewFile("remove-dir/subdir/nested.txt", "nested").
			WithNewDirectory("empty-dir")

		// Create new directory without the subdirectories
		newDir := c.Directory().
			WithNewFile("keep.txt", "keep")

		changes := newDir.Changes(oldDir)

		removedPaths, err := changes.RemovedPaths(ctx)
		require.NoError(t, err)

		// Should include the directories with trailing slash
		require.Contains(t, removedPaths, "remove-dir/")
		require.Contains(t, removedPaths, "empty-dir/")

		// Should NOT include individual files in the removed directory
		require.NotContains(t, removedPaths, "remove-dir/file.txt")
		require.NotContains(t, removedPaths, "remove-dir/subdir/")
		require.NotContains(t, removedPaths, "remove-dir/subdir/nested.txt")

		// Should not include files that weren't removed
		require.NotContains(t, removedPaths, "keep.txt")
	})

	t.Run("removedPaths mixed files and directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with mix of files and directories
		oldDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("remove-file.txt", "remove me").
			WithNewFile("remove-dir/file.txt", "in dir").
			WithNewFile("keep-dir/file.txt", "keep dir")

		// Create new directory keeping some files and directories
		newDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("keep-dir/file.txt", "keep dir")

		changes := newDir.Changes(oldDir)

		removedPaths, err := changes.RemovedPaths(ctx)
		require.NoError(t, err)

		// Should include individual removed files
		require.Contains(t, removedPaths, "remove-file.txt")

		// Should include removed directory but not its contents
		require.Contains(t, removedPaths, "remove-dir/")
		require.NotContains(t, removedPaths, "remove-dir/file.txt")

		// Should not include kept items
		require.NotContains(t, removedPaths, "keep.txt")
		require.NotContains(t, removedPaths, "keep-dir/")
		require.NotContains(t, removedPaths, "keep-dir/file.txt")
	})

	t.Run("addedFiles basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with some files
		oldDir := c.Directory().
			WithNewFile("existing.txt", "content1").
			WithNewFile("dir/existing2.txt", "content2")

		// Create new directory with additional files
		newDir := c.Directory().
			WithNewFile("existing.txt", "content1").
			WithNewFile("dir/existing2.txt", "content2").
			WithNewFile("added.txt", "new content").
			WithNewFile("dir/added2.txt", "new content2").
			WithNewFile("new-dir/added3.txt", "new content3")

		changes := newDir.Changes(oldDir)

		addedFiles, err := changes.AddedPaths(ctx)
		require.NoError(t, err)

		// Should include added files
		require.Contains(t, addedFiles, "added.txt")
		require.Contains(t, addedFiles, "dir/added2.txt")
		require.Contains(t, addedFiles, "new-dir/added3.txt")

		// Should not include existing files
		require.NotContains(t, addedFiles, "existing.txt")
		require.NotContains(t, addedFiles, "dir/existing2.txt")
	})

	t.Run("addedFiles excludes directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("old-dir/file.txt", "new")

		newDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("old-dir/new-file.txt", "new").
			WithNewFile("new-dir/file.txt", "new").
			WithNewDirectory("empty-dir")

		changes := newDir.Changes(oldDir)

		addedFiles, err := changes.AddedPaths(ctx)
		require.NoError(t, err)

		// Should include added files only
		require.Contains(t, addedFiles, "new-dir/file.txt")

		// Should only include NEW directories
		require.NotContains(t, addedFiles, "old-dir/")
		require.Contains(t, addedFiles, "new-dir/")
		require.Contains(t, addedFiles, "empty-dir/")

		// Should not include existing files
		require.NotContains(t, addedFiles, "keep.txt")
	})

	t.Run("modifiedPaths basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory
		oldDir := c.Directory().
			WithNewFile("unchanged.txt", "same content").
			WithNewFile("changed.txt", "original content").
			WithNewFile("dir/changed2.txt", "original content2").
			WithNewFile("will-be-removed.txt", "remove me")

		// Create new directory with changes
		newDir := c.Directory().
			WithNewFile("unchanged.txt", "same content").
			WithNewFile("changed.txt", "modified content").
			WithNewFile("dir/changed2.txt", "modified content2").
			WithNewFile("added.txt", "new file")

		changes := newDir.Changes(oldDir)

		modifiedPaths, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)

		// Should include changed files
		require.Contains(t, modifiedPaths, "changed.txt")
		require.Contains(t, modifiedPaths, "dir/changed2.txt")

		// Should not include unchanged files
		require.NotContains(t, modifiedPaths, "unchanged.txt")

		// Should not include added files
		require.NotContains(t, modifiedPaths, "added.txt")

		// Should not include removed files
		require.NotContains(t, modifiedPaths, "will-be-removed.txt")
	})

	t.Run("modifiedPaths with empty changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create identical directories
		dir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2")

		changes := dir.Changes(dir)

		modifiedPaths, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)

		// Should be empty when no changes
		require.Empty(t, modifiedPaths)
	})

	t.Run("modifiedPaths excludes directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithNewFile("dir/file.txt", "old content")

		newDir := c.Directory().
			WithNewFile("dir/file.txt", "new content").
			WithNewFile("dir/added.txt", "added content")

		changes := newDir.Changes(oldDir)

		modifiedPaths, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)

		// Should include changed files only
		require.Contains(t, modifiedPaths, "dir/file.txt")

		// Should NOT include directories or added files
		require.NotContains(t, modifiedPaths, "dir/")
		require.NotContains(t, modifiedPaths, "dir/added.txt")
	})

	t.Run("layer basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory
		oldDir := c.Directory().
			WithNewFile("unchanged.txt", "same content").
			WithNewFile("changed.txt", "original content").
			WithNewFile("dir/changed2.txt", "original content2").
			WithNewFile("will-be-removed.txt", "remove me")

		// Create new directory with changes
		newDir := c.Directory().
			WithNewFile("unchanged.txt", "same content").
			WithNewFile("changed.txt", "modified content").
			WithNewFile("dir/changed2.txt", "modified content2").
			WithNewFile("added.txt", "new file")

		changes := newDir.Changes(oldDir)
		layer := changes.Layer()

		// Verify layer contains modified files
		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "changed.txt")
		require.Contains(t, entries, "dir/")
		require.Contains(t, entries, "added.txt")

		// Verify layer excludes unchanged and removed files
		require.NotContains(t, entries, "unchanged.txt")
		require.NotContains(t, entries, "will-be-removed.txt")

		// Verify file contents in layer
		changedContent, err := layer.File("changed.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified content", changedContent)

		addedContent, err := layer.File("added.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new file", addedContent)

		// Verify nested file in layer
		dirEntries, err := layer.Directory("dir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEntries, "changed2.txt")

		changed2Content, err := layer.File("dir/changed2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified content2", changed2Content)
	})

	t.Run("layer with only added files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with some files
		oldDir := c.Directory().
			WithNewFile("existing.txt", "content1").
			WithNewFile("dir/existing2.txt", "content2")

		// Create new directory with additional files (no modifications)
		newDir := c.Directory().
			WithNewFile("existing.txt", "content1").
			WithNewFile("dir/existing2.txt", "content2").
			WithNewFile("added.txt", "new content").
			WithNewFile("dir/added2.txt", "new content2").
			WithNewFile("new-dir/added3.txt", "new content3")

		changes := newDir.Changes(oldDir)
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		// Should include added files
		require.Contains(t, entries, "added.txt")
		require.Contains(t, entries, "dir/")
		require.Contains(t, entries, "new-dir/")

		// Should not include existing files
		require.NotContains(t, entries, "existing.txt")

		// Verify added files have correct content
		addedContent, err := layer.File("added.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content", addedContent)

		added2Content, err := layer.File("dir/added2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content2", added2Content)

		added3Content, err := layer.File("new-dir/added3.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content3", added3Content)
	})

	t.Run("layer excludes removed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with files to be removed and modified
		oldDir := c.Directory().
			WithNewFile("keep-and-change.txt", "original").
			WithNewFile("remove-me.txt", "will be removed").
			WithNewFile("remove-dir/file.txt", "in removed dir")

		// Create new directory without removed files but with changes
		newDir := c.Directory().
			WithNewFile("keep-and-change.txt", "modified").
			WithNewFile("new-file.txt", "newly added")

		changes := newDir.Changes(oldDir)
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		// Should include modified and added files
		require.Contains(t, entries, "keep-and-change.txt")
		require.Contains(t, entries, "new-file.txt")

		// Should NOT include removed files or directories
		require.NotContains(t, entries, "remove-me.txt")
		require.NotContains(t, entries, "remove-dir/")

		// Verify modified file has new content
		modifiedContent, err := layer.File("keep-and-change.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified", modifiedContent)
	})

	t.Run("layer with empty changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create identical directories
		dir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2")

		changes := dir.Changes(dir)
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		// Should be empty when no changes
		require.Empty(t, entries)
	})

	t.Run("layer with nested directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with nested structure
		oldDir := c.Directory().
			WithNewFile("root.txt", "root content").
			WithNewFile("level1/file.txt", "level1 original").
			WithNewFile("level1/level2/file.txt", "level2 original").
			WithNewFile("level1/level2/level3/deep.txt", "deep original")

		// Create new directory with changes at various levels
		newDir := c.Directory().
			WithNewFile("root.txt", "root content").                       // unchanged
			WithNewFile("level1/file.txt", "level1 modified").             // changed
			WithNewFile("level1/level2/file.txt", "level2 original").      // unchanged
			WithNewFile("level1/level2/level3/deep.txt", "deep modified"). // changed
			WithNewFile("level1/level2/level3/added.txt", "newly added").  // added
			WithNewFile("level1/added-level2/new.txt", "added in new dir") // added in new dir

		changes := newDir.Changes(oldDir)
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		// Should include directories with changes
		require.Contains(t, entries, "level1/")

		// Verify nested structure is preserved
		level1Entries, err := layer.Directory("level1").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, level1Entries, "file.txt")
		require.Contains(t, level1Entries, "level2/")
		require.Contains(t, level1Entries, "added-level2/")

		level2Entries, err := layer.Directory("level1/level2").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, level2Entries, "level3/")

		level3Entries, err := layer.Directory("level1/level2/level3").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, level3Entries, "deep.txt")
		require.Contains(t, level3Entries, "added.txt")

		// Verify file contents
		modifiedContent, err := layer.File("level1/file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "level1 modified", modifiedContent)

		deepContent, err := layer.File("level1/level2/level3/deep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep modified", deepContent)

		addedContent, err := layer.File("level1/level2/level3/added.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "newly added", addedContent)

		newDirContent, err := layer.File("level1/added-level2/new.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "added in new dir", newDirContent)

		// Verify root.txt is NOT included (unchanged)
		require.NotContains(t, entries, "root.txt")
	})

	t.Run("layer is scoped to subdirectory changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithDirectory("public", c.Directory())

		newDir := oldDir.
			WithNewFile("Gemfile", "source \"https://rubygems.org\"").
			WithDirectory("public", c.Directory().
				WithNewFile("asset_foo", "foo").
				WithNewFile("asset_bar", "bar"))

		changes := newDir.Directory("/public").Changes(oldDir.Directory("/public"))
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"asset_foo", "asset_bar"}, entries)
		require.NotContains(t, entries, "Gemfile")
		require.NotContains(t, entries, "public/")
	})

	t.Run("layer ignores out-of-scope changes when scoped to subdirectory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithDirectory("public", c.Directory())

		newDir := oldDir.
			WithNewFile("Gemfile", "source \"https://rubygems.org\"")

		changes := newDir.Directory("/public").Changes(oldDir.Directory("/public"))
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, entries)
	})

	t.Run("layer is relative to nested scoped path", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithDirectory("assets", c.Directory().
				WithDirectory("public", c.Directory()))

		newDir := oldDir.
			WithNewFile("Gemfile", "source \"https://rubygems.org\"").
			WithDirectory("assets", c.Directory().
				WithDirectory("public", c.Directory().
					WithNewFile("asset_foo", "foo").
					WithNewFile("asset_bar", "bar")))

		changes := newDir.Directory("/assets/public").Changes(oldDir.Directory("/assets/public"))
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"asset_foo", "asset_bar"}, entries)
		require.NotContains(t, entries, "assets/")
		require.NotContains(t, entries, "public/")
	})

	t.Run("scoped layer can be applied at explicit destination", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithDirectory("public", c.Directory())

		newDir := oldDir.
			WithDirectory("public", c.Directory().
				WithNewFile("asset_foo", "foo").
				WithNewFile("asset_bar", "bar"))

		layer := newDir.Directory("/public").Changes(oldDir.Directory("/public")).Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"asset_foo", "asset_bar"}, entries)

		applied := c.Directory().WithDirectory("public", layer)
		publicEntries, err := applied.Directory("public").Entries(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"asset_foo", "asset_bar"}, publicEntries)
	})

	t.Run("test withChanges can be applied on subdir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with a single file
		oldDir := c.Directory().
			WithNewFile("file1.txt", "content1")

		// Create new directory with multiple files
		newDir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("file2.txt", "content2") // new file

		changes := newDir.Changes(oldDir)

		addedFiles, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, addedFiles, []string{"file2.txt"})

		modifiedFiles, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, modifiedFiles)

		removedFiles, err := changes.RemovedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, removedFiles)

		// apply changes to a subdirectory
		d := c.Directory().WithNewDirectory("subdir").Directory("/subdir").WithChanges(changes)

		entries, err := d.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"file2.txt"}, entries)

		topLevelDir := d.Directory("..")

		entries, err = topLevelDir.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"subdir/"}, entries)

		s, err := topLevelDir.File("subdir/file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, s, "content2")
	})

	t.Run("test changes are restricted to subdir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithNewFile("ignored.txt", "").
			WithNewDirectory("new-dir").
			Directory("/new-dir").
			WithNewFile("file1.txt", "content1").
			WithTimestamps(0) // without this file1.txt will have different timestamps, which would cause it to show up as being modified

		newDir := c.Directory().
			WithNewDirectory("new-dir").
			Directory("/new-dir").
			WithNewFile("file1.txt", "content1").
			WithNewFile("file2.txt", "content2"). // new file
			WithTimestamps(0)

		changes := newDir.Changes(oldDir)

		addedFiles, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, addedFiles, []string{"file2.txt"})

		modifiedFiles, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, modifiedFiles)

		removedFiles, err := changes.RemovedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, removedFiles)

		// re-create the same "new-dir" directory structure, and apply changes to it
		d := c.Directory().WithNewDirectory("new-dir").Directory("/new-dir").WithChanges(changes)

		// make sure we only got file2.txt added
		entries, err := d.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"file2.txt"}, entries)

		// make sure ignored.txt didn't show up in the parent dir
		entries, err = d.Directory("..").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"new-dir/"}, entries)
	})
}

func (s ChangesetSuite) TestExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
	return &Test{
		Dir: dag.Directory().
			WithNewFile("foo.txt", "foo\nbar\nbaz").
			WithNewFile("bar.txt", "hey").
			WithNewDirectory("emptydir"),
	}
}

type Test struct {
	Dir *dagger.Directory
}

func (t *Test) Update() *dagger.Changeset {
	return t.Dir.
		WithNewFile("foo.txt", "foo\nbaz").
		WithoutFile("bar.txt").
		WithNewFile("baz.txt", "im new here").
		WithoutDirectory("emptydir").
		Changes(t.Dir)
}

func (t *Test) NoChanges() *dagger.Changeset {
	return t.Dir.Changes(t.Dir)
}
`,
		).
		With(daggerCall("dir", "-o", "./outdir"))

	t.Run("export", func(ctx context.Context, t *testctx.T) {
		modGen := modGen

		entries, err := modGen.Directory("./outdir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"bar.txt", "emptydir/", "foo.txt"}, entries)

		modGen, err = modGen.With(daggerCall("update", "export", "--path", "./outdir")).Sync(ctx)
		require.NoError(t, err)

		entries, err = modGen.Directory("./outdir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"baz.txt", "foo.txt"}, entries)

		contents, err := modGen.File("./outdir/foo.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\nbaz", contents)

		contents, err = modGen.File("./outdir/baz.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "im new here", contents)
	})

	t.Run("output flag", func(ctx context.Context, t *testctx.T) {
		modGen := modGen

		entries, err := modGen.Directory("./outdir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"bar.txt", "emptydir/", "foo.txt"}, entries)

		modGen, err = modGen.With(daggerCall("update", "-o", "./outdir")).Sync(ctx)
		require.NoError(t, err)

		entries, err = modGen.Directory("./outdir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"baz.txt", "foo.txt"}, entries)

		contents, err := modGen.File("./outdir/foo.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\nbaz", contents)

		contents, err = modGen.File("./outdir/baz.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "im new here", contents)
	})

	t.Run("no changes", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("no-changes")).Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "no changes to apply")
	})
}

func (s ChangesetSuite) TestWithChanges(ctx context.Context, t *testctx.T) {
	s.testChangeApplying(t, func(dest *dagger.Directory, source *dagger.Changeset) *dagger.Directory {
		return dest.WithChanges(source)
	}, false)
}

func (s ChangesetSuite) TestChangesAsPatch(ctx context.Context, t *testctx.T) {
	s.testChangeApplying(t, func(dest *dagger.Directory, source *dagger.Changeset) *dagger.Directory {
		return dest.WithPatchFile(source.AsPatch())
	}, true)
}

func (ChangesetSuite) testChangeApplying(t *testctx.T, apply func(*dagger.Directory, *dagger.Changeset) *dagger.Directory, leaveDirs bool) {
	t.Run("basic usage with added, changed, and removed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory
		baseDir := c.Directory().
			WithNewFile("keep.txt", "unchanged").
			WithNewFile("change.txt", "original").
			WithNewFile("remove.txt", "will be removed").
			WithNewFile("subdir/nested.txt", "nested original")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory with changes
		afterDir := c.Directory().
			WithNewFile("keep.txt", "unchanged").           // unchanged
			WithNewFile("change.txt", "modified").          // changed
			WithNewFile("add.txt", "newly added").          // added
			WithNewFile("subdir/nested.txt", "nested mod"). // changed in subdir
			WithNewFile("subdir/new.txt", "new in subdir")  // added in subdir
		// Note: remove.txt is not included (removed)

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		// Should have: keep.txt, change.txt (modified), add.txt (new), subdir/ (with changes)
		// Should NOT have: remove.txt (removed)
		require.Contains(t, entries, "keep.txt")
		require.Contains(t, entries, "change.txt")
		require.Contains(t, entries, "add.txt")
		require.Contains(t, entries, "subdir/")
		require.NotContains(t, entries, "remove.txt")

		// Verify file contents
		keepContent, err := resultDir.File("keep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "unchanged", keepContent)

		changeContent, err := resultDir.File("change.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified", changeContent)

		addContent, err := resultDir.File("add.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "newly added", addContent)

		// Verify subdirectory entries
		subdirEntries, err := resultDir.Directory("subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, subdirEntries, "nested.txt")
		require.Contains(t, subdirEntries, "new.txt")

		// Verify subdirectory file contents
		nestedContent, err := resultDir.File("subdir/nested.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "nested mod", nestedContent)

		newInSubdirContent, err := resultDir.File("subdir/new.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new in subdir", newInSubdirContent)
	})

	t.Run("only added files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory with some files
		baseDir := c.Directory().
			WithNewFile("existing.txt", "existing content")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory with additional files
		afterDir := baseDir.
			WithNewFile("new1.txt", "new content 1").
			WithNewFile("dir/new2.txt", "new content 2")

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "existing.txt")
		require.Contains(t, entries, "new1.txt")
		require.Contains(t, entries, "dir/")

		// Verify content
		existingContent, err := resultDir.File("existing.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "existing content", existingContent)

		new1Content, err := resultDir.File("new1.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content 1", new1Content)

		new2Content, err := resultDir.File("dir/new2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content 2", new2Content)
	})

	t.Run("only changed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory
		baseDir := c.Directory().
			WithNewFile("file1.txt", "original 1").
			WithNewFile("dir/file2.txt", "original 2")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory with modifications
		afterDir := c.Directory().
			WithNewFile("file1.txt", "modified 1").
			WithNewFile("dir/file2.txt", "modified 2")

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		file1Content, err := resultDir.File("file1.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified 1", file1Content)

		file2Content, err := resultDir.File("dir/file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified 2", file2Content)
	})

	t.Run("only removed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory
		baseDir := c.Directory().
			WithNewFile("keep.txt", "keep this").
			WithNewFile("remove1.txt", "remove this").
			WithNewFile("dir/remove2.txt", "remove this too")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory with files removed
		afterDir := c.Directory().
			WithNewFile("keep.txt", "keep this")
		// Note: remove1.txt and dir/remove2.txt are not included

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		entries, err := resultDir.Glob(ctx, "**")
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"keep.txt"}, entries)

		// Verify content of kept file
		keepContent, err := resultDir.File("keep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep this", keepContent)
	})

	t.Run("no changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory
		baseDir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2")

		// Create identical before and after directories
		beforeDir := baseDir
		afterDir := baseDir

		// Create changes (should be empty)
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result is identical to the original
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		originalEntries, err := baseDir.Entries(ctx)
		require.NoError(t, err)

		require.ElementsMatch(t, originalEntries, entries)

		// Verify file contents are unchanged
		file1Content, err := resultDir.File("file1.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "content1", file1Content)

		file2Content, err := resultDir.File("dir/file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "content2", file2Content)
	})

	t.Run("applying changes to different base directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create before directory
		beforeDir := c.Directory().
			WithNewFile("common.txt", "before").
			WithNewFile("only-in-before.txt", "before only")

		// Create after directory with changes
		afterDir := c.Directory().
			WithNewFile("common.txt", "after").
			WithNewFile("only-in-after.txt", "after only")
		// Note: only-in-before.txt is removed

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to a different base directory
		differentBaseDir := c.Directory().
			WithNewFile("common.txt", "base version").
			WithNewFile("only-in-before.txt", "base has this too").
			WithNewFile("base-specific.txt", "only in base")

		resultDir := differentBaseDir.WithChanges(changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "common.txt")
		require.Contains(t, entries, "only-in-after.txt")
		require.Contains(t, entries, "base-specific.txt")
		require.NotContains(t, entries, "only-in-before.txt") // Should be removed

		// Verify contents
		commonContent, err := resultDir.File("common.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "after", commonContent) // Should be the "after" version

		afterOnlyContent, err := resultDir.File("only-in-after.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "after only", afterOnlyContent)

		baseSpecificContent, err := resultDir.File("base-specific.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "only in base", baseSpecificContent) // Should be preserved
	})

	t.Run("complex nested structure changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create complex base directory
		baseDir := c.Directory().
			WithNewFile("root.txt", "root content").
			WithNewFile("level1/file1.txt", "level1 content").
			WithNewFile("level1/level2/file2.txt", "level2 content").
			WithNewFile("level1/level2/level3/file3.txt", "level3 content").
			WithNewFile("another/path/file.txt", "another content")

		beforeDir := baseDir

		// Create after directory with complex changes
		afterDir := baseDir.
			WithNewFile("root.txt", "modified root").              // changed
			WithNewFile("level1/level2/file2.txt", "modified l2"). // changed
			WithNewFile("level1/level2/newfile.txt", "new file").  // added
			WithNewFile("new/deep/path/newfile.txt", "deep new").  // added deep
			WithNewFile("another/different.txt", "different")      // added

		changes := afterDir.Changes(beforeDir)
		resultDir := apply(baseDir, changes)

		// Verify structure
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, entries, "root.txt")
		require.Contains(t, entries, "level1/")
		require.Contains(t, entries, "new/")
		require.Contains(t, entries, "another/")

		// Verify changed content
		rootContent, err := resultDir.File("root.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified root", rootContent)

		l2Content, err := resultDir.File("level1/level2/file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified l2", l2Content)

		// Verify added files
		newFileContent, err := resultDir.File("level1/level2/newfile.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new file", newFileContent)

		deepNewContent, err := resultDir.File("new/deep/path/newfile.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep new", deepNewContent)

		// Verify another/different.txt was added
		differentContent, err := resultDir.File("another/different.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "different", differentContent)
	})

	if leaveDirs {
		return
	}

	t.Run("removed entire directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory with nested structure
		baseDir := c.Directory().
			WithNewFile("keep.txt", "keep this").
			WithNewFile("removedir/file1.txt", "remove me").
			WithNewFile("removedir/subdir/file2.txt", "remove me too").
			WithNewDirectory("emptydir")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory without the directories
		afterDir := c.Directory().
			WithNewFile("keep.txt", "keep this")

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "keep.txt")
		require.NotContains(t, entries, "removedir/")
		require.NotContains(t, entries, "emptydir/")

		// Verify we can't access removed files
		_, err = resultDir.File("removedir/file1.txt").Contents(ctx)
		require.Error(t, err)
	})

	t.Run("empty directories handling", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory with empty directories
		baseDir := c.Directory().
			WithNewFile("file.txt", "content").
			WithNewDirectory("empty1").
			WithNewDirectory("empty2")

		beforeDir := baseDir

		// Create after directory removing one empty dir and adding another
		afterDir := c.Directory().
			WithNewFile("file.txt", "content").
			WithNewDirectory("empty2").
			WithNewDirectory("new-empty")

		changes := afterDir.Changes(beforeDir)
		resultDir := apply(baseDir, changes)

		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "file.txt")
		require.Contains(t, entries, "empty2/")
		require.Contains(t, entries, "new-empty/")
		require.NotContains(t, entries, "empty1/")

		// Verify directories are actually directories
		exists, err := resultDir.Directory("empty2").Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, exists) // Should be empty

		exists2, err := resultDir.Directory("new-empty").Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, exists2) // Should be empty
	})
}

func (ChangesetSuite) TestEmpty(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseDir := c.Directory().
		WithNewFile("file.txt", "content")

	// empty
	empty, err := baseDir.Changes(baseDir).IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)

	// empty - added + removed
	addedAndRemovedDir := baseDir.
		WithNewFile("newfile.txt", "new").
		WithoutFile("newfile.txt")
	empty, err = addedAndRemovedDir.Changes(baseDir).IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)

	// not empty - modified
	modifiedDir := baseDir.WithNewFile("file.txt", "modified")
	empty, err = modifiedDir.Changes(baseDir).IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, empty)

	// not empty - added
	addedDir := baseDir.WithNewFile("newfile.txt", "new")
	empty, err = addedDir.Changes(baseDir).IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, empty)

	// not empty - removed
	removedDir := baseDir.WithoutFile("file.txt")
	empty, err = removedDir.Changes(baseDir).IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, empty)
}

func (ChangesetSuite) TestChangesetMerge(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseDir := c.Directory().
		WithNewFile("filea.txt", "initial file a content").
		WithNewFile("fileb.txt", "initial file b content").
		WithNewFile("filec.txt", "initial file c content").
		WithNewFile("filed.txt", "initial file d content").
		WithNewFile("filee.txt", "initial file e content")
	original := baseDir.
		WithNewFile("filea.txt", "file a modified in original").
		WithNewFile("fileb.txt", "file b modified in original").
		WithoutFile("filec.txt").
		WithNewFile("filef.txt", "file f added in original").
		WithNewFile("fileg.txt", "file g added in original").
		Changes(baseDir)

	t.Run("initial state", func(ctx context.Context, t *testctx.T) {
		modifiedPaths, err := original.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, modifiedPaths, []string{"filea.txt", "fileb.txt"})
		addedPaths, err := original.AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, addedPaths, []string{"filef.txt", "fileg.txt"})
		removedPaths, err := original.RemovedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, removedPaths, []string{"filec.txt"})
	})

	t.Run("no conflicts", func(ctx context.Context, t *testctx.T) {
		other := baseDir.
			WithNewFile("filee.txt", "file e modified in other").
			WithNewFile("fileh.txt", "file h added in other").
			WithoutFile("filed.txt").
			Changes(baseDir)

		// ensure resulting changeset contains modifications from both
		// changesets
		res, err := original.WithChangeset(other).Sync(ctx)
		require.NoError(t, err)
		modifiedPath, err := res.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, modifiedPath, []string{"filea.txt", "fileb.txt", "filee.txt"})
		addedPath, err := res.AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, addedPath, []string{"filef.txt", "fileg.txt", "fileh.txt"})
		removedPaths, err := res.RemovedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, removedPaths, []string{"filec.txt", "filed.txt"})
	})

	t.Run("with conflict", func(ctx context.Context, t *testctx.T) {
		// this will cause multiple conflicts:
		// - on filea.txt, file is modified in both changesets
		// - on fileb.txt, file is modified in 'original' changeset and removed in 'other' changeset
		// - on filec.txt, file is removed in 'original' changeset and modified in 'other' changeset
		// - on filef.txt, file is added in both changesets
		other := baseDir.
			WithNewFile("filea.txt", "file a modified in other").
			WithoutFile("fileb.txt").
			WithNewFile("filec.txt", "file c modified in other").
			WithNewFile("filef.txt", "file f added in other").
			WithNewFile("filee.txt", "file e modified in other").
			WithNewFile("filei.txt", "file i added in other").
			Changes(baseDir)

		// Conflict resolution strategies:
		// - FAIL_EARLY: fail before merge if file-level conflicts detected
		// - FAIL: attempt merge, fail if git merge fails
		// - LEAVE_CONFLICT_MARKERS: keep conflict markers, keep modified for modify/delete
		// - PREFER_OURS: use -X ours, resolve modify/delete by preferring ours
		// - PREFER_THEIRS: use -X theirs, resolve modify/delete by preferring theirs

		t.Run("fail early", func(ctx context.Context, t *testctx.T) {
			// FAIL_EARLY checks file-level conflicts before attempting merge
			_, err := original.WithChangeset(other, dagger.ChangesetWithChangesetOpts{
				OnConflict: dagger.ChangesetMergeConflictFailEarly,
			}).Sync(ctx)
			require.ErrorContains(t, err, "filea.txt")
			require.ErrorContains(t, err, "fileb.txt")
			require.ErrorContains(t, err, "filec.txt")
			require.ErrorContains(t, err, "filef.txt")
		})

		t.Run("fail", func(ctx context.Context, t *testctx.T) {
			// FAIL is the default - attempts merge and fails if conflicts occur
			_, err := original.WithChangeset(other).Sync(ctx)
			require.Error(t, err)
			// explicit FAIL
			_, err = original.WithChangeset(other, dagger.ChangesetWithChangesetOpts{
				OnConflict: dagger.ChangesetMergeConflictFail,
			}).Sync(ctx)
			require.Error(t, err)
		})

		t.Run("leave conflict markers", func(ctx context.Context, t *testctx.T) {
			res, err := original.WithChangeset(other, dagger.ChangesetWithChangesetOpts{
				OnConflict: dagger.ChangesetMergeConflictLeaveConflictMarkers,
			}).Sync(ctx)
			require.NoError(t, err)

			// For files modified in both changesets, content should have conflict markers
			c, err := res.After().File("filea.txt").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, c, "<<<<<<<")
			require.Contains(t, c, "=======")
			require.Contains(t, c, ">>>>>>>")

			// For modify/delete conflicts, keep the modified version
			// fileb.txt: modified in original, removed in other -> keep original's modification
			c, err = res.After().File("fileb.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "file b modified in original", c)
			// filec.txt: removed in original, modified in other -> keep other's modification
			c, err = res.After().File("filec.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "file c modified in other", c)

			// For files added in both, content should have conflict markers
			c, err = res.After().File("filef.txt").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, c, "<<<<<<<")
		})

		t.Run("prefer ours", func(ctx context.Context, t *testctx.T) {
			res, err := original.WithChangeset(other, dagger.ChangesetWithChangesetOpts{
				OnConflict: dagger.ChangesetMergeConflictPreferOurs,
			}).Sync(ctx)
			require.NoError(t, err)

			modifiedPaths, err := res.ModifiedPaths(ctx)
			require.NoError(t, err)
			addedPaths, err := res.AddedPaths(ctx)
			require.NoError(t, err)
			removedPaths, err := res.RemovedPaths(ctx)
			require.NoError(t, err)

			// - on filea.txt, file is modified in both changesets
			require.Contains(t, modifiedPaths, "filea.txt")
			c, err := res.After().File("filea.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "file a modified in original", c)
			// - on fileb.txt, file is modified in 'original' changeset and removed in 'other' changeset
			require.Contains(t, modifiedPaths, "fileb.txt")
			c, err = res.After().File("fileb.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "file b modified in original", c)
			// - on filec.txt, file is removed in 'original' changeset and modified in 'other' changeset
			require.Contains(t, removedPaths, "filec.txt")
			// - on filef.txt, file is added in both changesets
			require.Contains(t, addedPaths, "filef.txt")
			c, err = res.After().File("filef.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "file f added in original", c)

			// ensure other changes are still applied
			require.Contains(t, modifiedPaths, "filee.txt")
			require.Contains(t, addedPaths, "fileg.txt")
			require.Contains(t, addedPaths, "filei.txt")
		})

		t.Run("prefer theirs", func(ctx context.Context, t *testctx.T) {
			res, err := original.WithChangeset(other, dagger.ChangesetWithChangesetOpts{
				OnConflict: dagger.ChangesetMergeConflictPreferTheirs,
			}).Sync(ctx)
			require.NoError(t, err)

			modifiedPaths, err := res.ModifiedPaths(ctx)
			require.NoError(t, err)
			addedPaths, err := res.AddedPaths(ctx)
			require.NoError(t, err)
			removedPaths, err := res.RemovedPaths(ctx)
			require.NoError(t, err)

			// - on filea.txt, file is modified in both changesets
			require.Contains(t, modifiedPaths, "filea.txt")
			c, err := res.After().File("filea.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "file a modified in other", c)
			// - on fileb.txt, file is modified in 'original' changeset and removed in 'other' changeset
			require.Contains(t, removedPaths, "fileb.txt")
			// - on filec.txt, file is removed in 'original' changeset and modified in 'other' changeset
			require.Contains(t, modifiedPaths, "filec.txt")
			c, err = res.After().File("filec.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "file c modified in other", c)
			// - on filef.txt, file is added in both changesets
			require.Contains(t, addedPaths, "filef.txt")
			c, err = res.After().File("filef.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "file f added in other", c)

			// ensure other changes are still applied
			require.Contains(t, modifiedPaths, "filee.txt")
			require.Contains(t, addedPaths, "fileg.txt")
			require.Contains(t, addedPaths, "filei.txt")
		})
	})

	t.Run("sequential merge", func(ctx context.Context, t *testctx.T) {
		// Create three independent changesets from the same base, each modifying different files
		changeset1 := baseDir.
			WithNewFile("filea.txt", "file a modified in changeset1").
			WithNewFile("file1.txt", "file 1 added in changeset1").
			Changes(baseDir)

		changeset2 := baseDir.
			WithNewFile("fileb.txt", "file b modified in changeset2").
			WithNewFile("file2.txt", "file 2 added in changeset2").
			WithoutFile("filec.txt").
			Changes(baseDir)

		changeset3 := baseDir.
			WithNewFile("filed.txt", "file d modified in changeset3").
			WithNewFile("file3.txt", "file 3 added in changeset3").
			WithoutFile("filee.txt").
			Changes(baseDir)

		// Merge changesets sequentially using WithChangeset
		merged, err := changeset1.WithChangeset(changeset2).Sync(ctx)
		require.NoError(t, err)
		res, err := merged.WithChangeset(changeset3).Sync(ctx)
		require.NoError(t, err)

		// Verify the merged changeset contains changes from all three
		modifiedPaths, err := res.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, modifiedPaths, []string{"filea.txt", "fileb.txt", "filed.txt"})

		addedPaths, err := res.AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, addedPaths, []string{"file1.txt", "file2.txt", "file3.txt"})

		removedPaths, err := res.RemovedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, removedPaths, []string{"filec.txt", "filee.txt"})

		// Verify file contents from each changeset are present
		content, err := res.After().File("filea.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file a modified in changeset1", content)

		content, err = res.After().File("fileb.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file b modified in changeset2", content)

		content, err = res.After().File("filed.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file d modified in changeset3", content)

		content, err = res.After().File("file1.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file 1 added in changeset1", content)

		content, err = res.After().File("file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file 2 added in changeset2", content)

		content, err = res.After().File("file3.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file 3 added in changeset3", content)
	})
}

func (ChangesetSuite) TestWithChangesets(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseDir := c.Directory().
		WithNewFile("filea.txt", "initial file a content").
		WithNewFile("fileb.txt", "initial file b content").
		WithNewFile("filec.txt", "initial file c content").
		WithNewFile("filed.txt", "initial file d content").
		WithNewFile("filee.txt", "initial file e content")

	t.Run("empty array returns unchanged changeset", func(ctx context.Context, t *testctx.T) {
		original := baseDir.
			WithNewFile("filea.txt", "file a modified").
			WithNewFile("newfile.txt", "new file").
			Changes(baseDir)

		// WithChangesets with empty array should return unchanged changeset
		res, err := original.WithChangesets([]*dagger.Changeset{}).Sync(ctx)
		require.NoError(t, err)

		// Verify the changeset is unchanged
		modifiedPaths, err := res.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"filea.txt"}, modifiedPaths)

		addedPaths, err := res.AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"newfile.txt"}, addedPaths)
	})

	t.Run("single element delegates to WithChangeset", func(ctx context.Context, t *testctx.T) {
		original := baseDir.
			WithNewFile("filea.txt", "file a modified in original").
			Changes(baseDir)

		other := baseDir.
			WithNewFile("fileb.txt", "file b modified in other").
			WithNewFile("newfile.txt", "new file").
			Changes(baseDir)

		// WithChangesets with single element should work like WithChangeset
		res, err := original.WithChangesets([]*dagger.Changeset{other}).Sync(ctx)
		require.NoError(t, err)

		modifiedPaths, err := res.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"filea.txt", "fileb.txt"}, modifiedPaths)

		addedPaths, err := res.AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"newfile.txt"}, addedPaths)
	})

	t.Run("multiple changesets no conflicts", func(ctx context.Context, t *testctx.T) {
		// Create base changeset
		original := baseDir.
			WithNewFile("filea.txt", "file a modified in original").
			WithNewFile("file1.txt", "file 1 added in original").
			Changes(baseDir)

		// Create multiple independent changesets from the same base
		changeset1 := baseDir.
			WithNewFile("fileb.txt", "file b modified in changeset1").
			WithNewFile("file2.txt", "file 2 added in changeset1").
			Changes(baseDir)

		changeset2 := baseDir.
			WithNewFile("filec.txt", "file c modified in changeset2").
			WithNewFile("file3.txt", "file 3 added in changeset2").
			WithoutFile("filed.txt").
			Changes(baseDir)

		changeset3 := baseDir.
			WithNewFile("filee.txt", "file e modified in changeset3").
			WithNewFile("file4.txt", "file 4 added in changeset3").
			Changes(baseDir)

		// Merge all changesets at once using octopus merge
		res, err := original.WithChangesets([]*dagger.Changeset{changeset1, changeset2, changeset3}).Sync(ctx)
		require.NoError(t, err)

		// Verify the merged changeset contains changes from all changesets
		modifiedPaths, err := res.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"filea.txt", "fileb.txt", "filec.txt", "filee.txt"}, modifiedPaths)

		addedPaths, err := res.AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"file1.txt", "file2.txt", "file3.txt", "file4.txt"}, addedPaths)

		removedPaths, err := res.RemovedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"filed.txt"}, removedPaths)

		// Verify file contents from each changeset are present
		content, err := res.After().File("filea.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file a modified in original", content)

		content, err = res.After().File("fileb.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file b modified in changeset1", content)

		content, err = res.After().File("filec.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file c modified in changeset2", content)

		content, err = res.After().File("filee.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file e modified in changeset3", content)
	})

	t.Run("with conflicts - fail early", func(ctx context.Context, t *testctx.T) {
		original := baseDir.
			WithNewFile("filea.txt", "file a modified in original").
			Changes(baseDir)

		// Create changesets with conflicts
		changeset1 := baseDir.
			WithNewFile("filea.txt", "file a modified in changeset1"). // conflict with original
			Changes(baseDir)

		changeset2 := baseDir.
			WithNewFile("fileb.txt", "file b modified in changeset2").
			Changes(baseDir)

		// FAIL_EARLY should detect conflicts before attempting merge
		_, err := original.WithChangesets([]*dagger.Changeset{changeset1, changeset2}, dagger.ChangesetWithChangesetsOpts{
			OnConflict: dagger.ChangesetsMergeConflictFailEarly,
		}).Sync(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, "filea.txt")
	})

	t.Run("with conflicts between other changesets - fail early", func(ctx context.Context, t *testctx.T) {
		original := baseDir.
			WithNewFile("filea.txt", "file a modified in original").
			Changes(baseDir)

		// Create changesets that conflict with each other (not with original)
		changeset1 := baseDir.
			WithNewFile("fileb.txt", "file b modified in changeset1").
			Changes(baseDir)

		changeset2 := baseDir.
			WithNewFile("fileb.txt", "file b modified in changeset2"). // conflict with changeset1
			Changes(baseDir)

		// FAIL_EARLY should detect conflicts between any pair of changesets
		_, err := original.WithChangesets([]*dagger.Changeset{changeset1, changeset2}, dagger.ChangesetWithChangesetsOpts{
			OnConflict: dagger.ChangesetsMergeConflictFailEarly,
		}).Sync(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, "fileb.txt")
	})

	t.Run("with conflicts - fail", func(ctx context.Context, t *testctx.T) {
		original := baseDir.
			WithNewFile("filea.txt", "file a modified in original").
			Changes(baseDir)

		// Create changesets with conflicts
		changeset1 := baseDir.
			WithNewFile("filea.txt", "file a modified in changeset1"). // conflict with original
			Changes(baseDir)

		// FAIL is the default - attempts merge and fails if conflicts occur
		_, err := original.WithChangesets([]*dagger.Changeset{changeset1}).Sync(ctx)
		require.Error(t, err)

		// explicit FAIL
		_, err = original.WithChangesets([]*dagger.Changeset{changeset1}, dagger.ChangesetWithChangesetsOpts{
			OnConflict: dagger.ChangesetsMergeConflictFail,
		}).Sync(ctx)
		require.Error(t, err)
	})

	t.Run("comparison with sequential merge", func(ctx context.Context, t *testctx.T) {
		// Create the same changesets and verify that WithChangesets produces
		// equivalent results to sequential WithChangeset calls

		original := baseDir.
			WithNewFile("filea.txt", "file a modified in original").
			Changes(baseDir)

		changeset1 := baseDir.
			WithNewFile("fileb.txt", "file b modified in changeset1").
			WithNewFile("file1.txt", "file 1 added").
			Changes(baseDir)

		changeset2 := baseDir.
			WithNewFile("filec.txt", "file c modified in changeset2").
			WithNewFile("file2.txt", "file 2 added").
			Changes(baseDir)

		// Sequential merge
		seqMerged, err := original.WithChangeset(changeset1).Sync(ctx)
		require.NoError(t, err)
		seqResult, err := seqMerged.WithChangeset(changeset2).Sync(ctx)
		require.NoError(t, err)

		// Octopus merge
		octopusResult, err := original.WithChangesets([]*dagger.Changeset{changeset1, changeset2}).Sync(ctx)
		require.NoError(t, err)

		// Both should have the same paths
		seqModified, err := seqResult.ModifiedPaths(ctx)
		require.NoError(t, err)
		octopusModified, err := octopusResult.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, seqModified, octopusModified)

		seqAdded, err := seqResult.AddedPaths(ctx)
		require.NoError(t, err)
		octopusAdded, err := octopusResult.AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, seqAdded, octopusAdded)

		// Verify content is the same
		seqContent, err := seqResult.After().File("filea.txt").Contents(ctx)
		require.NoError(t, err)
		octopusContent, err := octopusResult.After().File("filea.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, seqContent, octopusContent)

		seqContent, err = seqResult.After().File("fileb.txt").Contents(ctx)
		require.NoError(t, err)
		octopusContent, err = octopusResult.After().File("fileb.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, seqContent, octopusContent)

		seqContent, err = seqResult.After().File("filec.txt").Contents(ctx)
		require.NoError(t, err)
		octopusContent, err = octopusResult.After().File("filec.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, seqContent, octopusContent)
	})

	t.Run("many changesets", func(ctx context.Context, t *testctx.T) {
		// Test with many changesets (simulating the 10-20 changeset use case)
		original := baseDir.Changes(baseDir) // empty changeset

		changesets := make([]*dagger.Changeset, 10)
		for i := range 10 {
			changesets[i] = baseDir.
				WithNewFile(fmt.Sprintf("newfile%d.txt", i), fmt.Sprintf("content from changeset %d", i)).
				Changes(baseDir)
		}

		res, err := original.WithChangesets(changesets).Sync(ctx)
		require.NoError(t, err)

		// Verify all added files are present
		addedPaths, err := res.AddedPaths(ctx)
		require.NoError(t, err)
		require.Len(t, addedPaths, 10)

		for i := range 10 {
			content, err := res.After().File(fmt.Sprintf("newfile%d.txt", i)).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("content from changeset %d", i), content)
		}
	})
}
