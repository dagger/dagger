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
				Added:   []string{"new.txt"},
				Removed: []string{"old.txt"},
			},
		},
		{
			name:   "copy",
			output: joinNul("C100", oldDir+"/src.txt", newDir+"/dst.txt"),
			want: fileChanges{
				Added: []string{"dst.txt"},
			},
		},
		{
			name:   "filename with newline",
			output: joinNul("A", newDir+"/has\nnewline.txt"),
			want: fileChanges{
				Added: []string{"has\nnewline.txt"},
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

	// old: file1 (will be modified), file2 (will be deleted), subdir/nested
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "file1.txt"), []byte("v1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "file2.txt"), []byte("gone"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(oldDir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "subdir", "nested.txt"), []byte("same"), 0644))

	// new: file1 (modified), file3 (added), subdir/nested (unchanged)
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "file1.txt"), []byte("v2"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "file3.txt"), []byte("new"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(newDir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "subdir", "nested.txt"), []byte("same"), 0644))

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
