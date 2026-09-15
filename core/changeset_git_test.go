package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func joinNul(parts ...string) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
		b = append(b, 0)
	}
	return b
}

func TestParseGitOutput(t *testing.T) {
	oldDir := "/old"
	newDir := "/new"

	tests := []struct {
		name   string
		output []byte
		want   fileChanges
	}{
		{
			name:   "empty",
			output: nil,
			want:   fileChanges{},
		},
		{
			name: "all change types",
			output: joinNul(
				"A", newDir+"/added.txt",
				"M", oldDir+"/modified.txt",
				"D", oldDir+"/deleted.txt",
				"A", newDir+"/nested/deep.txt",
			),
			want: fileChanges{
				Added:    []string{"added.txt", "nested/deep.txt"},
				Modified: []string{"modified.txt"},
				Removed:  []string{"deleted.txt"},
			},
		},
		{
			name:   "rename",
			output: joinNul("R100", oldDir+"/old.txt", newDir+"/new.txt"),
			want: fileChanges{
				Renamed: map[string]string{"new.txt": "old.txt"},
			},
		},
		{
			name:   "copy",
			output: joinNul("C100", oldDir+"/src.txt", newDir+"/dst.txt"),
			want: fileChanges{
				Added: []string{"dst.txt"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitOutput(tt.output, oldDir, newDir)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseGitNumStatOutput(t *testing.T) {
	oldDir := "/old"
	newDir := "/new"

	t.Run("normal files", func(t *testing.T) {
		got := parseGitNumStatOutput(joinNul(
			"1\t0\t"+newDir+"/added.txt",
			"0\t2\t"+oldDir+"/removed.txt",
			"3\t4\t"+oldDir+"/modified.txt",
			"-\t-\t"+oldDir+"/binary.bin",
		), oldDir, newDir)

		require.Equal(t, map[string]lineChanges{
			"added.txt":    {Added: 1},
			"removed.txt":  {Removed: 2},
			"modified.txt": {Added: 3, Removed: 4},
			"binary.bin":   {},
		}, got)
	})

	t.Run("renames", func(t *testing.T) {
		got := parseGitNumStatOutput(joinNul(
			"1\t0\t", "/dev/null", newDir+"/added-v2.txt",
			"2\t3\t", oldDir+"/modified-v2.txt", newDir+"/modified-v2.txt",
			"7\t8\t", oldDir+"/renamed-old.txt", newDir+"/renamed-new.txt",
			"0\t1\t", oldDir+"/removed-v2.txt", "/dev/null",
		), oldDir, newDir)

		require.Equal(t, map[string]lineChanges{
			"added-v2.txt":    {Added: 1},
			"modified-v2.txt": {Added: 2, Removed: 3},
			"renamed-new.txt": {Added: 7, Removed: 8},
			"removed-v2.txt":  {Removed: 1},
		}, got)
	})
}

func TestRelativeDiffPath(t *testing.T) {
	tests := []struct {
		path, base, want string
	}{
		{"/tmp/old/sub/file.txt", "/tmp/old", "sub/file.txt"},
		{"/tmp/old2/file.txt", "/tmp/old", ""},
		{"old/sub/file.txt", "old", "sub/file.txt"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, relativeDiffPath(tt.path, tt.base))
	}
}

func TestListSubdirectories(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "c"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("."), 0644))

	dirs, err := listSubdirectories(root)
	require.NoError(t, err)
	slices.Sort(dirs)
	require.Equal(t, []string{"a/", "a/b/", "c/"}, dirs)
}

func TestDiffStringSlices(t *testing.T) {
	tests := []struct {
		name        string
		old, new    []string
		wantAdded   []string
		wantRemoved []string
	}{
		{
			name: "no changes",
			old:  []string{"a/", "b/"},
			new:  []string{"a/", "b/"},
		},
		{
			name:        "mixed",
			old:         []string{"removed/", "kept/"},
			new:         []string{"added/", "kept/"},
			wantAdded:   []string{"added/"},
			wantRemoved: []string{"removed/"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, removed := diffStringSlices(tt.old, tt.new)
			require.Equal(t, tt.wantAdded, added)
			require.Equal(t, tt.wantRemoved, removed)
		})
	}
}

func TestCollapseChildPaths(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  []string
	}{
		{
			name: "empty",
		},
		{
			name:  "directory hides children",
			paths: []string{"dir/", "dir/file.txt", "dir/sub/", "dir/sub/deep.txt"},
			want:  []string{"dir/"},
		},
		{
			name:  "siblings preserved",
			paths: []string{"a/", "a/file.txt", "b/", "b/file.txt"},
			want:  []string{"a/", "b/"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseChildPaths(tt.paths)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCompareDirectories_Integration(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	brokenWorktree := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "file1.txt"), []byte("v1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "file2.txt"), []byte("gone"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(oldDir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "subdir", "nested.txt"), []byte("same"), 0644))

	require.NoError(t, os.WriteFile(filepath.Join(newDir, "file1.txt"), []byte("v2"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "file3.txt"), []byte("new"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(newDir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "subdir", "nested.txt"), []byte("same"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(brokenWorktree, ".git"), []byte("gitdir: /does/not/exist\n"), 0644))

	// Reproduce the worktree layout used by the repo checkout in Linux-backed tests.
	t.Chdir(brokenWorktree)

	ctx := context.Background()

	changes, err := compareDirectories(ctx, oldDir, newDir)
	require.NoError(t, err)
	slices.Sort(changes.Added)
	slices.Sort(changes.Modified)
	slices.Sort(changes.Removed)
	require.Equal(t, []string{"file3.txt"}, changes.Added)
	require.Equal(t, []string{"file1.txt"}, changes.Modified)
	require.Equal(t, []string{"file2.txt"}, changes.Removed)

	identical, err := directoriesAreIdentical(ctx, oldDir, newDir)
	require.NoError(t, err)
	require.False(t, identical)

	identical, err = directoriesAreIdentical(ctx, oldDir, oldDir)
	require.NoError(t, err)
	require.True(t, identical)
}

// Regression test: the buildkit refs mounted for a Changeset's Before and
// After can come from a git worktree checkout whose root is a `.git` FILE
// (a one-line "gitdir:" pointer to an absolute host path). When
// compareDirectories used to set cmd.Dir = oldDir, git's repository
// discovery would pick up that pointer and bail out with
//
//	fatal: not a git repository: <host path>/.git/worktrees/<name>
//
// even though `git diff --no-index` doesn't need a repository at all.
// This test reproduces that layout and asserts that the diff still
// succeeds.
func TestCompareDirectories_OldDirIsBrokenWorktree(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()

	// Plant a worktree-style .git pointer file at the root of both dirs,
	// matching what a worktree checkout looks like on disk.
	brokenPointer := []byte("gitdir: /does/not/exist\n")
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, ".git"), brokenPointer, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, ".git"), brokenPointer, 0644))

	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "file.txt"), []byte("v1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "file.txt"), []byte("v2"), 0644))

	ctx := context.Background()

	changes, err := compareDirectories(ctx, oldDir, newDir)
	require.NoError(t, err)
	require.Equal(t, []string{"file.txt"}, changes.Modified)

	stats, err := compareDirectoriesNumStat(ctx, oldDir, newDir)
	require.NoError(t, err)
	require.Contains(t, stats, "file.txt")

	identical, err := directoriesAreIdentical(ctx, oldDir, newDir)
	require.NoError(t, err)
	require.False(t, identical)
}

func TestCompareDirectoriesNumStat_Integration(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "modified.txt"), []byte("a\nb\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "removed.txt"), []byte("gone\n"), 0644))

	require.NoError(t, os.WriteFile(filepath.Join(newDir, "modified.txt"), []byte("a\nc\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "added.txt"), []byte("new\n"), 0644))

	got, err := compareDirectoriesNumStat(context.Background(), oldDir, newDir)
	require.NoError(t, err)
	require.Equal(t, map[string]lineChanges{
		"modified.txt": {Added: 1, Removed: 1},
		"added.txt":    {Added: 1},
		"removed.txt":  {Removed: 1},
	}, got)
}
