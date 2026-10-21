package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitMergeFileModes(t *testing.T) {
	for _, merge := range []string{"full", "scoped", "octopus"} {
		t.Run(merge, func(t *testing.T) {
			base := t.TempDir()
			scopedMergeWrite(t, base, "untouched", "keep\n", 0o640)
			scopedMergeWrite(t, base, "modified", "before\n", 0o600)
			scopedMergeWrite(t, base, "deleted", "remove\n", 0o400)
			ours := scopedMergeContent(t, base, func(dir string) {
				scopedMergeWrite(t, dir, "a", "private\x00payload", 0o600)
				scopedMergeWrite(t, dir, "odd\nname", "odd path\n", 0o600)
				scopedMergeWrite(t, dir, "modified", "after\n", 0o640)
				require.NoError(t, os.Remove(filepath.Join(dir, "deleted")))
			})
			theirs := scopedMergeContent(t, base, func(dir string) {
				scopedMergeWrite(t, dir, "nested/b", "executable\x00payload", 0o750)
				require.NoError(t, os.Symlink("untouched", filepath.Join(dir, "link")))
			})
			third := scopedMergeContent(t, base, func(dir string) {
				scopedMergeWrite(t, dir, "third", "third\n", 0o440)
			})
			work := filepath.Join(t.TempDir(), "work")
			copyScopedMergeTree(t, base, work)
			ws := &gitMergeWorkspace{root: work, dir: "/", workDir: work}
			var err error
			if merge == "octopus" {
				err = ws.mergeMultipleChangesets(t.Context(), ours, []*changesetContent{theirs, third})
			} else {
				var scope *gitMergeScope
				if merge == "scoped" {
					scope, err = newGitMergeScope(t.Context(), ours, theirs)
					require.NoError(t, err)
				}
				err = ws.mergeChangesets(t.Context(), scope, ours, theirs, ours.paths.CheckConflicts(theirs.paths), FailOnConflict, nil)
			}
			require.NoError(t, err)
			for p, mode := range map[string]os.FileMode{"untouched": 0o640, "modified": 0o640, "a": 0o600, "nested/b": 0o750, "odd\nname": 0o600} {
				requireMergeFileMode(t, work, p, mode)
			}
			if merge == "octopus" {
				requireMergeFileMode(t, work, "third", 0o440)
			}
			data, err := os.ReadFile(filepath.Join(work, "nested/b"))
			require.NoError(t, err)
			require.Equal(t, "executable\x00payload", string(data))
			require.NoFileExists(t, filepath.Join(work, "deleted"))
			target, err := os.Readlink(filepath.Join(work, "link"))
			require.NoError(t, err)
			require.Equal(t, "untouched", target)
		})
	}
}

func TestGitMergeFileModesConflicts(t *testing.T) {
	const before = "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"
	for _, tc := range []struct {
		name      string
		ours      string
		theirs    string
		ourMode   os.FileMode
		theirMode os.FileMode
		strategy  WithChangesetMergeConflict
		wantMode  os.FileMode
	}{
		{"ours", "OURS", "THEIRS", 0o600, 0o640, PreferOursOnConflict, 0o600},
		{"theirs", "OURS", "THEIRS", 0o600, 0o640, PreferTheirsOnConflict, 0o640},
		{"markers", "OURS", "THEIRS", 0o600, 0o640, LeaveConflictMarkers, 0o600},
		{"executable change", "OURS", "THEIRS", 0o600, 0o750, PreferOursOnConflict, 0o750},
		{"clean merge", "ONE", "SEVEN", 0o600, 0o640, FailOnConflict, 0o600},
		{"one mode changed", "ONE", "SEVEN", 0o660, 0o640, FailOnConflict, 0o640},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			scopedMergeWrite(t, base, "file", before, 0o660)
			ours := scopedMergeContent(t, base, func(dir string) {
				from := "four"
				if tc.strategy == FailOnConflict {
					from = "one"
				}
				scopedMergeWrite(t, dir, "file", strings.Replace(before, from, tc.ours, 1), tc.ourMode)
			})
			theirs := scopedMergeContent(t, base, func(dir string) {
				from := "four"
				if tc.strategy == FailOnConflict {
					from = "seven"
				}
				scopedMergeWrite(t, dir, "file", strings.Replace(before, from, tc.theirs, 1), tc.theirMode)
			})
			work := filepath.Join(t.TempDir(), "work")
			copyScopedMergeTree(t, base, work)
			ws := &gitMergeWorkspace{root: work, dir: "/", workDir: work}
			require.NoError(t, ws.mergeChangesets(t.Context(), nil, ours, theirs, ours.paths.CheckConflicts(theirs.paths), tc.strategy, nil))
			requireMergeFileMode(t, work, "file", tc.wantMode)
		})
	}
}

func TestGitMergeFileModesRenameAndModify(t *testing.T) {
	base := t.TempDir()
	scopedMergeWrite(t, base, "old", strings.Repeat("line\n", 20), 0o600)
	ours := scopedMergeContent(t, base, func(dir string) {
		scopedMergeRename(t, dir, "old", "new")
	})
	theirs := scopedMergeContent(t, base, func(dir string) {
		scopedMergeWrite(t, dir, "old", strings.Repeat("line\n", 19)+"modified\n", 0o640)
	})
	work := filepath.Join(t.TempDir(), "work")
	copyScopedMergeTree(t, base, work)
	ws := &gitMergeWorkspace{root: work, dir: "/", workDir: work}
	require.NoError(t, ws.mergeChangesets(t.Context(), nil, ours, theirs, ours.paths.CheckConflicts(theirs.paths), FailOnConflict, nil))
	requireMergeFileMode(t, work, "new", 0o640)
	require.NoFileExists(t, filepath.Join(work, "old"))
}

func TestGitMergeFileModesReplacements(t *testing.T) {
	base := t.TempDir()
	scopedMergeWrite(t, base, "dir/child", "old child\n", 0o400)
	scopedMergeWrite(t, base, "target/child", "link target\n", 0o640)
	scopedMergeWrite(t, base, "file", "old file\n", 0o600)
	scopedMergeWrite(t, base, "was-dir/child", "old nested file\n", 0o440)
	ours := scopedMergeContent(t, base, func(dir string) {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, "dir")))
		require.NoError(t, os.Symlink("target", filepath.Join(dir, "dir")))
		require.NoError(t, os.Remove(filepath.Join(dir, "file")))
		scopedMergeWrite(t, dir, "file/child", "new nested file\n", 0o600)
		require.NoError(t, os.RemoveAll(filepath.Join(dir, "was-dir")))
		scopedMergeWrite(t, dir, "was-dir", "new file\n", 0o640)
	})
	theirs := scopedMergeContent(t, base, func(dir string) {
		scopedMergeWrite(t, dir, "other", "other\n", 0o750)
	})
	work := filepath.Join(t.TempDir(), "work")
	copyScopedMergeTree(t, base, work)
	ws := &gitMergeWorkspace{root: work, dir: "/", workDir: work}
	require.NoError(t, ws.mergeChangesets(t.Context(), nil, ours, theirs, ours.paths.CheckConflicts(theirs.paths), FailOnConflict, nil))
	requireMergeFileMode(t, work, "target/child", 0o640)
	requireMergeFileMode(t, work, "file/child", 0o600)
	requireMergeFileMode(t, work, "was-dir", 0o640)
}

func TestGitMergeFileModesInferredDirectoryRename(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		scopedMergeWrite(t, base, "old/"+name, strings.Repeat(name+"\n", 20), 0o600)
	}
	ours := scopedMergeContent(t, base, func(dir string) {
		for _, name := range []string{"a", "b", "c"} {
			scopedMergeRename(t, dir, "old/"+name, "new/"+name)
		}
	})
	theirs := scopedMergeContent(t, base, func(dir string) {
		scopedMergeWrite(t, dir, "old/added", "executable addition\n", 0o750)
	})
	work := filepath.Join(t.TempDir(), "work")
	copyScopedMergeTree(t, base, work)
	ws := &gitMergeWorkspace{root: work, dir: "/", workDir: work}
	require.NoError(t, ws.mergeChangesets(t.Context(), nil, ours, theirs, ours.paths.CheckConflicts(theirs.paths), LeaveConflictMarkers, nil))
	requireMergeFileMode(t, work, "new/added", 0o750)
	require.NoFileExists(t, filepath.Join(work, "old/added"))
}

func requireMergeFileMode(t *testing.T, dir, p string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(dir, p))
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%04o", want), fmt.Sprintf("%04o", info.Mode().Perm()), p)
}
