package core

import (
	"context"
	"fmt"
	gofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/internal/fsutil"
	"github.com/stretchr/testify/require"
)

// These tests attack one assumption behind the workspace's sparse diff base:
// that
//
//	host.directory(path: <workspace root>, include: [p, p+"/**", ...])
//
// faithfully returns every included path that exists on the host. The pattern
// set is built by sparseHostBase (core/schema/workspace.go:2681-2685) from the
// overlay's cumulative touched paths, rewritten by hostSchema.directory
// (core/schema/host.go:226-246), and finally handed to fsutil.NewFilterFS
// (internal/fsutil/filter.go:77) on both the client (engine/client/filesync.go:185)
// and mirror (engine/filesync/localfs.go:96) side of the sync. Everything below
// exercises that final filter with the exact patterns those two layers produce.

// sparseIncludePatterns reproduces the include list a sparse host base read
// ends up applying, for the given cumulative touched paths.
func sparseIncludePatterns(touched []string) []string {
	// sparseHostBase, core/schema/workspace.go:2681-2685
	raw := make([]string, 0, len(touched)*2)
	for _, p := range touched {
		p = strings.TrimSuffix(p, "/")
		raw = append(raw, p, p+"/**")
	}

	// hostSchema.directory, core/schema/host.go:226-246, with
	// relPathFromRoot == "." (gitignore is off for sparse base reads).
	out := make([]string, 0, len(raw))
	for _, include := range raw {
		include, negative := strings.CutPrefix(include, "!")
		if !filepath.IsLocal(include) {
			continue
		}
		include = filepath.Join(".", include)
		if include == "." {
			include = "*"
		}
		if negative {
			include = "!" + include
		}
		out = append(out, include)
	}
	return out
}

// walkSparseBase returns the set of paths a sparse host base read would yield
// for root, given the cumulative touched paths.
func walkSparseBase(t *testing.T, root string, touched []string) map[string]struct{} {
	t.Helper()
	got := map[string]struct{}{}
	err := fsutil.WalkDir(context.Background(), root, &fsutil.FilterOpt{
		IncludePatterns: sparseIncludePatterns(touched),
	}, func(p string, _ gofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		got[filepath.ToSlash(p)] = struct{}{}
		return nil
	})
	require.NoError(t, err)
	return got
}

func writeFile(t *testing.T, root, rel string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("content of "+rel+"\n"), 0o644))
}

// TestSparseHostIncludeMatchesTouchedFile is the baseline: a plain, deeply
// nested, long-tracked file is returned by a sparse base read that includes it.
func TestSparseHostIncludeMatchesTouchedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "hack/designs/async-agents.md")
	writeFile(t, root, "hack/designs/other.md")
	writeFile(t, root, "core/schema/workspace.go")
	writeFile(t, root, "README.md")

	got := walkSparseBase(t, root, []string{"hack/designs/async-agents.md"})

	require.Contains(t, got, "hack/designs/async-agents.md")
	// the sibling "<file>/**" pattern must not drag in anything else
	require.NotContains(t, got, "hack/designs/other.md")
	require.NotContains(t, got, "README.md")
}

// TestSparseHostIncludeScale checks that a large include list - TouchedPaths
// grows monotonically over a long session - neither drops nor truncates the
// patterns. If a sparse base read degrades above some pattern count, a file
// touched early in a session would silently fall out of the diff base later on.
func TestSparseHostIncludeScale(t *testing.T) {
	t.Parallel()

	for _, n := range []int{1, 2, 16, 128, 512, 2000} {
		t.Run(fmt.Sprintf("touched=%d", n), func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			touched := make([]string, 0, n)
			for i := range n {
				rel := fmt.Sprintf("pkg%02d/dir%02d/file%04d.go", i%13, i%7, i)
				writeFile(t, root, rel)
				touched = append(touched, rel)
			}
			// decoys that are never touched
			writeFile(t, root, "untouched/a.go")
			writeFile(t, root, "untouched/b.go")

			got := walkSparseBase(t, root, touched)
			for _, p := range touched {
				require.Contains(t, got, p, "touched path missing from sparse base with %d includes", n)
			}
			require.NotContains(t, got, "untouched/a.go")
		})
	}
}

// TestSparseHostIncludeAncestorAndDescendant covers the case where the touched
// set contains both a directory and a file beneath it. The directory's "/**"
// pattern and the file's bare pattern overlap; neither may cancel the other.
func TestSparseHostIncludeAncestorAndDescendant(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "a/b/c.txt")
	writeFile(t, root, "a/b/d.txt")
	writeFile(t, root, "a/e.txt")
	writeFile(t, root, "z/keep-out.txt")

	for _, touched := range [][]string{
		{"a", "a/b/c.txt"},
		{"a/b/c.txt", "a"},
		{"a/b", "a/b/c.txt"},
		{"a/", "a/b/c.txt"},
	} {
		t.Run(strings.Join(touched, ","), func(t *testing.T) {
			got := walkSparseBase(t, root, touched)
			require.Contains(t, got, "a/b/c.txt")
			require.NotContains(t, got, "z/keep-out.txt")
		})
	}
}

// TestSparseHostIncludeGlobMetacharacters is the sharp one. Include patterns
// are globs, not literals, so a touched path whose own name contains glob
// metacharacters does not necessarily match itself. Such a path is host-present
// and explicitly included, yet absent from the resulting directory - which is
// exactly the shape of the ADDED-vs-MODIFIED defect.
func TestSparseHostIncludeGlobMetacharacters(t *testing.T) {
	t.Parallel()

	// name -> whether the pattern built from the name matches the name itself
	cases := []struct {
		rel        string
		selfMatch  bool
		reasonNote string
	}{
		{rel: "plain.txt", selfMatch: true},
		{rel: "star*.txt", selfMatch: true, reasonNote: `"*" matches any run, including the literal "*"`},
		{rel: "quest?.txt", selfMatch: true, reasonNote: `"?" matches any single char, including "?"`},
		{rel: "brace{a}.txt", selfMatch: true, reasonNote: "patternmatcher has no brace expansion"},
		{rel: "bracket[1].txt", selfMatch: false, reasonNote: `"[1]" is a character class matching "1"`},
		{rel: "caret^.txt", selfMatch: true},
	}

	for _, tc := range cases {
		t.Run(tc.rel, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeFile(t, root, "dir/"+tc.rel)

			rel := "dir/" + tc.rel
			got := walkSparseBase(t, root, []string{rel})
			if tc.selfMatch {
				require.Contains(t, got, rel, tc.reasonNote)
			} else {
				// Documented defect, not desired behaviour: an explicitly
				// included, host-present path is silently absent.
				require.NotContains(t, got, rel, tc.reasonNote)
			}
		})
	}
}

// TestSparseHostIncludeBangPrefixedPath documents that a touched path starting
// with "!" is turned into a NEGATIVE (exclusion) pattern by
// hostSchema.directory (core/schema/host.go:231), so it excludes itself.
func TestSparseHostIncludeBangPrefixedPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "!bang.txt")
	writeFile(t, root, "normal.txt")

	patterns := sparseIncludePatterns([]string{"!bang.txt"})
	require.Equal(t, []string{"!bang.txt", "!bang.txt/**"}, patterns,
		"the leading ! is stripped, then re-added as an exclusion marker")

	got := walkSparseBase(t, root, []string{"!bang.txt"})
	// Documented defect, not desired behaviour.
	require.NotContains(t, got, "!bang.txt")
}

// TestSparseHostIncludeSymlinkedAncestor covers a touched path reached through
// a symlinked ancestor directory. fsutil's walk does not descend into symlinks
// (FollowPaths, which would resolve them, is not set by sparse base reads), so
// the path is host-present, explicitly included, and absent from the result.
func TestSparseHostIncludeSymlinkedAncestor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "real/c.txt")
	require.NoError(t, os.Symlink("real", filepath.Join(root, "link")))

	got := walkSparseBase(t, root, []string{"link/c.txt"})
	// Documented defect, not desired behaviour: the read comes back EMPTY -
	// neither the file nor the symlink that leads to it.
	require.NotContains(t, got, "link/c.txt")
	require.Empty(t, got)

	// the same file reached by its real path is fine
	got = walkSparseBase(t, root, []string{"real/c.txt"})
	require.Contains(t, got, "real/c.txt")
}
