package fsxutil

import (
	"context"
	gofs "io/fs"
	"os"
	"testing"

	"github.com/dagger/dagger/internal/fsutil"
	"github.com/dagger/dagger/internal/fsutil/types"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type walkEntry struct {
	kind    string
	ignored bool
}

func collectWalk(t *testing.T, fs fsutil.FS) map[string]walkEntry {
	t.Helper()

	entries := map[string]walkEntry{}
	err := fs.Walk(context.Background(), "", func(path string, entry gofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry == nil {
			return nil
		}
		fi, err := entry.Info()
		if err != nil {
			return err
		}

		kind := "file"
		if fi.IsDir() {
			kind = "dir"
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		}

		ignored := false
		if stat, ok := fi.Sys().(*types.Stat); ok {
			ignored = stat.GitIgnored
		}

		entries[path] = walkEntry{kind: kind, ignored: ignored}
		return nil
	})
	require.NoError(t, err)

	return entries
}

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

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":      {kind: "file", ignored: false},
		"foo.txt":         {kind: "file", ignored: false},
		"bar.log":         {kind: "file", ignored: true},
		"temp":            {kind: "dir", ignored: true},
		"temp/nested.txt": {kind: "file", ignored: true},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreNestedGitIgnore(t *testing.T) {
	// Test that nested .gitignore files properly override parent rules.
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

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":           {kind: "file", ignored: false},
		"subdir":               {kind: "dir", ignored: false},
		"subdir/.gitignore":    {kind: "file", ignored: false},
		"subdir/test.log":      {kind: "file", ignored: true},
		"subdir/important.log": {kind: "file", ignored: false},
		"root.log":             {kind: "file", ignored: true},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreDirectoryOnly(t *testing.T) {
	// Test directory-only patterns (trailing slash).
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "build/\n*.tmp"`,
		`ADD build dir`,
		`ADD build/output.txt file`,
		`ADD build.tmp file`,
		`ADD buildfile file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":       {kind: "file", ignored: false},
		"build":            {kind: "dir", ignored: true},
		"build/output.txt": {kind: "file", ignored: true},
		"build.tmp":        {kind: "file", ignored: true},
		"buildfile":        {kind: "file", ignored: false},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreNegationPrecedence(t *testing.T) {
	// Test complex negation patterns where later rules override earlier ones.
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

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":      {kind: "file", ignored: false},
		"regular.log":     {kind: "file", ignored: true},
		"important.log":   {kind: "file", ignored: false},
		"temp":            {kind: "dir", ignored: true},
		"temp/delete.txt": {kind: "file", ignored: true},
		"temp/keep.txt":   {kind: "file", ignored: false},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreDoublestar(t *testing.T) {
	// Test ** patterns that match any number of directories.
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

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":               {kind: "file", ignored: false},
		"node_modules":             {kind: "dir", ignored: true},
		"node_modules/react":       {kind: "file", ignored: true},
		"project":                  {kind: "dir", ignored: false},
		"project/node_modules":     {kind: "dir", ignored: true},
		"project/node_modules/vue": {kind: "file", ignored: true},
		"script.py":                {kind: "file", ignored: false},
		"script.pyc":               {kind: "file", ignored: true},
		"deep":                     {kind: "dir", ignored: false},
		"deep/nested":              {kind: "dir", ignored: false},
		"deep/nested/file.pyc":     {kind: "file", ignored: true},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreRelativePatterns(t *testing.T) {
	// Test patterns that are relative to the gitignore file location.
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

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":                  {kind: "file", ignored: false},
		"absolute.txt":                {kind: "file", ignored: false},
		"root-only.txt":               {kind: "file", ignored: true},
		"build":                       {kind: "file", ignored: false},
		"subdir":                      {kind: "dir", ignored: false},
		"subdir/.gitignore":           {kind: "file", ignored: false},
		"subdir/build":                {kind: "file", ignored: true},
		"subdir/other":                {kind: "file", ignored: false},
		"subdir/absolute.txt":         {kind: "file", ignored: true},
		"subdir/subdir2":              {kind: "dir", ignored: false},
		"subdir/subdir2/absolute.txt": {kind: "file", ignored: false},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreTrailingSlash(t *testing.T) {
	// Test that trailing slashes in patterns are handled correctly.
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

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":         {kind: "file", ignored: false},
		"build-foo":          {kind: "dir", ignored: true},
		"build-foo/file.txt": {kind: "file", ignored: true},
		"build-bar":          {kind: "file", ignored: false},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreEmptyAndComments(t *testing.T) {
	// Test that empty lines and comments are properly ignored.
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

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore": {kind: "file", ignored: false},
		"test.log":   {kind: "file", ignored: true},
		"temp.txt":   {kind: "file", ignored: true},
		"keep.txt":   {kind: "file", ignored: false},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreNoGitIgnoreFile(t *testing.T) {
	// Test behavior when no .gitignore file exists.
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

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		"foo.txt":           {kind: "file", ignored: false},
		"bar.log":           {kind: "file", ignored: false},
		"subdir":            {kind: "dir", ignored: false},
		"subdir/nested.txt": {kind: "file", ignored: false},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreOpen(t *testing.T) {
	// Test that Open() respects gitignore rules.
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "*.log"`,
		`ADD allowed.txt file "content"`,
		`ADD blocked.log file "content"`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	// Should be able to open allowed file.
	r, err := gfs.Open("allowed.txt")
	require.NoError(t, err)
	require.NoError(t, r.Close())

	// Should NOT be able to open blocked file.
	_, err = gfs.Open("blocked.log")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestGitIgnoreComplexHierarchy(t *testing.T) {
	// Test complex directory hierarchy with multiple .gitignore files.
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "*.tmp\nignore/"`,
		`ADD level1 dir`,
		`ADD level1/.gitignore file "*.log\n!important.log"`,
		`ADD level1/level2 dir`,
		`ADD level1/level2/.gitignore file "*.txt\n!keep.txt"`,
		`ADD test.tmp file`,
		`ADD level1/test.log file`,
		`ADD level1/important.log file`,
		`ADD level1/level2/file.txt file`,
		`ADD level1/level2/keep.txt file`,
		`ADD level1/level2/test.tmp file`,
		`ADD level1/level2/other.log file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":               {kind: "file", ignored: false},
		"level1":                   {kind: "dir", ignored: false},
		"level1/.gitignore":        {kind: "file", ignored: false},
		"level1/level2":            {kind: "dir", ignored: false},
		"level1/level2/.gitignore": {kind: "file", ignored: false},
		"test.tmp":                 {kind: "file", ignored: true},
		"level1/test.log":          {kind: "file", ignored: true},
		"level1/important.log":     {kind: "file", ignored: false},
		"level1/level2/file.txt":   {kind: "file", ignored: true},
		"level1/level2/keep.txt":   {kind: "file", ignored: false},
		"level1/level2/test.tmp":   {kind: "file", ignored: true},
		"level1/level2/other.log":  {kind: "file", ignored: true},
	}
	assert.Equal(t, expected, got)
}

func TestGitIgnoreEdgeCasePatterns(t *testing.T) {
	// Test edge case patterns that might cause issues.
	d, err := tmpDir(changeStream([]string{
		`ADD .gitignore file "file[123].txt\n*.log\nspecial\\*file.txt\ndir with spaces/"`,
		`ADD file1.txt file`,
		`ADD file2.txt file`,
		`ADD file4.txt file`,
		`ADD test.log file`,
		`ADD special*file.txt file`,
		`ADD "dir with spaces" dir`,
		`ADD "dir with spaces/content.txt" file`,
		`ADD normal.txt file`,
	}))
	require.NoError(t, err)
	defer os.RemoveAll(d)

	fs, err := fsutil.NewFS(d)
	require.NoError(t, err)

	gfs, err := NewGitIgnoreMarkedFS(fs, nil)
	require.NoError(t, err)

	got := collectWalk(t, gfs)
	expected := map[string]walkEntry{
		".gitignore":                  {kind: "file", ignored: false},
		"file1.txt":                   {kind: "file", ignored: true},
		"file2.txt":                   {kind: "file", ignored: true},
		"file4.txt":                   {kind: "file", ignored: false},
		"test.log":                    {kind: "file", ignored: true},
		"special*file.txt":            {kind: "file", ignored: true},
		"dir with spaces":             {kind: "dir", ignored: true},
		"dir with spaces/content.txt": {kind: "file", ignored: true},
		"normal.txt":                  {kind: "file", ignored: false},
	}
	assert.Equal(t, expected, got)
}
