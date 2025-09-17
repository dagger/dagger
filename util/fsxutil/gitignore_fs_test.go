package fsxutil

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

func TestGitIgnoreBasic(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "*.log\ntemp/"`,
		`ADD foo.txt file`,
		`ADD bar.log file`,
		`ADD temp dir`,
		`ADD temp/nested.txt file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// Should include .gitignore, foo.txt but exclude bar.log and temp/
	assert.Equal(t, `file .gitignore
file foo.txt
`, b.String())
}

func TestGitIgnoreNestedGitIgnore(t *testing.T) {
	// Test that nested .gitignore files properly override parent rules
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "*.log"`,
		`ADD subdir dir`,
		`ADD subdir/.gitignore file "!important.log"`,
		`ADD subdir/test.log file`,
		`ADD subdir/important.log file`,
		`ADD root.log file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// Root .log files should be ignored, but subdir/important.log should be included due to negation
	expected := `file .gitignore
dir subdir
file subdir/.gitignore
file subdir/important.log
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreDirectoryOnly(t *testing.T) {
	// Test directory-only patterns (trailing slash)
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "build/\n*.tmp"`,
		`ADD build dir`,
		`ADD build/output.txt file`,
		`ADD build.tmp file`, // This file should be ignored by *.tmp
		`ADD buildfile file`, // This file should NOT be ignored (no trailing slash)
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// build/ directory ignored, build.tmp ignored by *.tmp, but buildfile included
	expected := `file .gitignore
file buildfile
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreNegationPrecedence(t *testing.T) {
	// Test complex negation patterns where later rules override earlier ones
	// Expected behavior: gitignore processes patterns in order, so later patterns
	// take precedence. A negation pattern (!) can un-ignore files that were
	// previously ignored.
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "*.log\n!important.log\ntemp/\n!temp/keep.txt"`,
		`ADD regular.log file`,
		`ADD important.log file`,
		`ADD temp dir`,
		`ADD temp/delete.txt file`,
		`ADD temp/keep.txt file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// *.log ignores all .log files, but !important.log brings it back
	// temp/ ignores the directory, but !temp/keep.txt should bring back that specific file
	expected := `file .gitignore
file important.log
dir temp
file temp/keep.txt
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreDoublestar(t *testing.T) {
	// Test ** patterns that match any number of directories
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "**/node_modules/\n**/*.pyc"`,
		`ADD node_modules dir`,
		`ADD node_modules/react file`,
		`ADD project dir`,
		`ADD project/node_modules dir`,
		`ADD project/node_modules/vue file`,
		`ADD script.py file`,
		`ADD script.pyc file`,
		`ADD deep dir`,
		`ADD deep/nested dir`,
		`ADD deep/nested/file.pyc file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// All node_modules directories and .pyc files should be ignored
	expected := `file .gitignore
dir deep
dir deep/nested
dir project
file script.py
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreRelativePatterns(t *testing.T) {
	// Test patterns that are relative to the gitignore file location
	// Expected behavior: patterns without leading slash are relative to the
	// gitignore file's directory, patterns with leading slash are relative
	// to the repository root.
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "/root-only.txt"`,
		`ADD absolute.txt file`,
		`ADD root-only.txt file`,
		`ADD build file`,
		`ADD subdir dir`,
		`ADD subdir/.gitignore file "build\n/absolute.txt"`,
		`ADD subdir/build file`,
		`ADD subdir/other file`,
		`ADD subdir/absolute.txt file`,
		`ADD subdir/subdir2 dir`,
		`ADD subdir/subdir2/absolute.txt file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// /root-only.txt ignored by root .gitignore
	// build ignored by root .gitignore
	// subdir/build ignored by subdir/.gitignore
	// subdir/other/build NOT ignored (subdir/.gitignore only applies to its level)
	// absolute.txt NOT ignored (/ pattern in subdir doesn't affect root)
	expected := `file .gitignore
file absolute.txt
file build
dir subdir
file subdir/.gitignore
file subdir/other
dir subdir/subdir2
file subdir/subdir2/absolute.txt
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreTrailingSlash(t *testing.T) {
	// Test that trailing slashes in patterns are handled correctly
	// Expected behavior: Patterns with trailing slashes should only match directories.
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "build*/"`,
		`ADD build-foo dir`,
		`ADD build-foo/file.txt file`,
		`ADD build-bar file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	expected := `file .gitignore
file build-bar
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreEmptyAndComments(t *testing.T) {
	// Test that empty lines and comments are properly ignored
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "# This is a comment\n\n*.log\n# Another comment\n\ntemp.txt\n\n"`,
		`ADD test.log file`,
		`ADD temp.txt file`,
		`ADD keep.txt file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	expected := `file .gitignore
file keep.txt
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreNoGitIgnoreFile(t *testing.T) {
	// Test behavior when no .gitignore file exists
	d, err := tmpDir(changeStream([]string{
		`ADD foo.txt file`,
		`ADD bar.log file`,
		`ADD subdir dir`,
		`ADD subdir/nested.txt file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// Without .gitignore, all files should be included
	expected := `file bar.log
file foo.txt
dir subdir
file subdir/nested.txt
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreOpen(t *testing.T) {
	// Test that Open() respects gitignore rules
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "*.log"`,
		`ADD allowed.txt file "content"`,
		`ADD blocked.log file "content"`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	// Should be able to open allowed file
	r, err := gfs.Open("allowed.txt")
	require.NoError(t, err)
	require.NoError(t, r.Close())

	// Should NOT be able to open blocked file
	_, err = gfs.Open("blocked.log")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestGitIgnoreComplexHierarchy(t *testing.T) {
	// Test complex directory hierarchy with multiple .gitignore files
	// Expected behavior: Each .gitignore file adds its patterns to the
	// accumulated set from parent directories. Child patterns can override
	// parent patterns using negation.
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "*.tmp\nignore/"`,
		`ADD level1 dir`,
		`ADD level1/.gitignore file "*.log\n!important.log"`,
		`ADD level1/level2 dir`,
		`ADD level1/level2/.gitignore file "*.txt\n!keep.txt"`,
		`ADD test.tmp file`,                // ignored by root
		`ADD level1/test.log file`,         // ignored by level1
		`ADD level1/important.log file`,    // NOT ignored (negated by level1)
		`ADD level1/level2/file.txt file`,  // ignored by level2
		`ADD level1/level2/keep.txt file`,  // NOT ignored (negated by level2)
		`ADD level1/level2/test.tmp file`,  // ignored by root (inherited)
		`ADD level1/level2/other.log file`, // ignored by level1 (inherited)
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// Complex inheritance and negation should work correctly
	expected := `file .gitignore
dir level1
file level1/.gitignore
file level1/important.log
dir level1/level2
file level1/level2/.gitignore
file level1/level2/keep.txt
`
	assert.Equal(t, expected, b.String())
}

func TestGitIgnoreEdgeCasePatterns(t *testing.T) {
	// Test edge case patterns that might cause issues
	// Expected behavior: Various special characters and patterns should
	// work correctly, including escaping and bracket expressions.
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "file[123].txt\n*.log\nspecial\\*file.txt\ndir with spaces/"`,
		`ADD file1.txt file`,        // ignored by bracket pattern
		`ADD file2.txt file`,        // ignored by bracket pattern
		`ADD file4.txt file`,        // NOT ignored (not in bracket range)
		`ADD test.log file`,         // ignored by *.log
		`ADD special*file.txt file`, // ignored by escaped pattern
		`ADD "dir with spaces" dir`, // ignored by dir pattern
		`ADD "dir with spaces/content.txt" file`,
		`ADD normal.txt file`, // NOT ignored
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreFS(fs, nil)
	require.NoError(t, err)

	b := &bytes.Buffer{}
	err = gfs.Walk(context.Background(), "", bufWalkDir(b))
	require.NoError(t, err)

	// Only file4.txt and normal.txt should remain
	expected := `file .gitignore
file file4.txt
file normal.txt
`
	assert.Equal(t, expected, b.String())
}
