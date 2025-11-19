package fsutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	gofs "io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil/types"
)

func TestWalkerSimple(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo file",
		"ADD foo2 file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, nil, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, `file foo
file foo2
`, b.String())
}

func TestInvalidExcludePatterns(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo file data1",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	fs, err := NewFS(d)
	assert.NoError(t, err)
	_, err = NewFilterFS(fs, &FilterOpt{ExcludePatterns: []string{"!"}})
	assert.Error(t, err)
}

func TestWalkerInclude(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD bar dir",
		"ADD bar/foo file",
		"ADD foo2 file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"bar"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo
`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"bar/foo"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo
`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"b*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo
`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"bar/f*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo
`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"bar/g*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Empty(t, b.Bytes())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"f*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, `file foo2
`, b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"b*/f*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo
`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"b*/foo"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo
`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"b*/"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo
`), b.String())
}

func TestWalkerIncludeReturnSkipDir(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo dir",
		"ADD foo/x dir",
		"ADD foo/y dir",
		"ADD foo/x/a.txt file",
		"ADD foo/y/b.txt file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	found := []string{}

	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/*.txt"},
	}, func(path string, info gofs.FileInfo, err error) error {
		found = append(found, path)
		return filepath.SkipDir
	})
	assert.NoError(t, err)

	assert.Equal(t, []string{"foo"}, found)
}

func TestWalkerExclude(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD bar file",
		"ADD foo dir",
		"ADD foo2 file",
		"ADD foo/bar2 file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{
		ExcludePatterns: []string{"foo*", "!foo/bar2"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`file bar
dir foo
file foo/bar2
`), b.String())
}

func TestWalkerFollowLinks(t *testing.T) {
	var d string
	var err error
	if runtime.GOOS == "windows" {
		d, err = tmpDir(changeStream([]string{
			"ADD bar file",
			"ADD foo dir",
			"ADD foo/l1 symlink C:/baz/one",
			"ADD foo/l2 symlink C:/baz/two",
			"ADD baz dir",
			"ADD baz/one file",
			"ADD baz/two symlink ../bax",
			"ADD bax file",
			"ADD bay file", // not included
		}))
	} else {
		d, err = tmpDir(changeStream([]string{
			"ADD bar file",
			"ADD foo dir",
			"ADD foo/l1 symlink /baz/one",
			"ADD foo/l2 symlink /baz/two",
			"ADD baz dir",
			"ADD baz/one file",
			"ADD baz/two symlink ../bax",
			"ADD bax file",
			"ADD bay file", // not included
		}))
	}
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{
		FollowPaths: []string{"foo/l*", "bar"},
	}, bufWalk(b))
	assert.NoError(t, err)

	if runtime.GOOS == "windows" {
		assert.Equal(t, filepath.FromSlash(`file bar
file bax
dir baz
file baz/one
symlink:../bax baz/two
dir foo
symlink:C:/baz/one foo/l1
symlink:C:/baz/two foo/l2
`), b.String())
	} else {
		assert.Equal(t, filepath.FromSlash(`file bar
file bax
dir baz
file baz/one
symlink:../bax baz/two
dir foo
symlink:/baz/one foo/l1
symlink:/baz/two foo/l2
`), b.String())
	}
}

func TestWalkerFollowLinksToRoot(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo symlink .",
		"ADD bar file",
		"ADD bax file",
		"ADD bay dir",
		"ADD bay/baz file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{
		FollowPaths: []string{"foo"},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`file bar
file bax
dir bay
file bay/baz
symlink:. foo
`), b.String())
}

func TestWalkerMap(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD bar file",
		"ADD foo dir",
		"ADD foo2 file",
		"ADD foo/bar2 file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{
		Map: func(_ string, s *types.Stat) MapResult {
			if strings.HasPrefix(s.Path, "foo") {
				s.Path = "_" + s.Path
				return MapResultKeep
			}
			return MapResultExclude
		},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir _foo
file _foo/bar2
file _foo2
`), b.String())
}

func TestWalkerMapSkipDir(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD excludeDir dir",
		"ADD excludeDir/a.txt file",
		"ADD includeDir dir",
		"ADD includeDir/a.txt file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	// SkipDir is a performance optimization - don't even
	// bother walking directories we don't care about.
	walked := []string{}
	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{
		Map: func(_ string, s *types.Stat) MapResult {
			walked = append(walked, s.Path)
			if strings.HasPrefix(s.Path, "excludeDir") {
				return MapResultSkipDir
			}
			if strings.HasPrefix(s.Path, "includeDir") {
				return MapResultKeep
			}
			return MapResultExclude
		},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir includeDir
file includeDir/a.txt
`), b.String())
	assert.Equal(t, []string{"excludeDir", "includeDir", filepath.FromSlash("includeDir/a.txt")}, walked)
}

func TestWalkerMapSkipDirWithPattern(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD x dir",
		"ADD x/a.txt file",
		"ADD y dir",
		"ADD y/b.txt file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/*.txt"},
		Map: func(_ string, s *types.Stat) MapResult {
			if filepath.Base(s.Path) == "x" {
				return MapResultSkipDir
			}
			return MapResultKeep
		},
	}, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir y
file y/b.txt
`), b.String())
}

func TestWalkerPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod not fully supported on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("test cannot run as root")
	}

	d, err := tmpDir(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar dir",
	}))
	assert.NoError(t, err)
	err = os.Chmod(filepath.Join(d, "foo", "bar"), 0000)
	require.NoError(t, err)
	defer func() {
		os.Chmod(filepath.Join(d, "bar"), 0700)
		os.RemoveAll(d)
	}()

	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{}, bufWalk(b))
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "permission denied")
	}

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		ExcludePatterns: []string{"**/bar"},
	}, bufWalk(b))
	assert.NoError(t, err)
	assert.Equal(t, `dir foo
`, b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		ExcludePatterns: []string{"**/bar", "!foo/bar/baz"},
	}, bufWalk(b))
	assert.NoError(t, err)
	assert.Equal(t, `dir foo
`, b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		ExcludePatterns: []string{"**/bar", "!foo/bar"},
	}, bufWalk(b))
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "permission denied")
	}

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"foo", "!**/bar"},
	}, bufWalk(b))
	assert.NoError(t, err)
	assert.Equal(t, `dir foo
`, b.String())
}

func bufWalk(buf *bytes.Buffer) filepath.WalkFunc {
	return func(path string, fi gofs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		stat, ok := fi.Sys().(*types.Stat)
		if !ok {
			return errors.Errorf("invalid symlink %s", path)
		}
		t := "file"
		if fi.IsDir() {
			t = "dir"
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			t = "symlink:" + stat.Linkname
		}
		fmt.Fprintf(buf, "%s %s", t, path)
		if fi.Mode()&os.ModeSymlink == 0 && stat.Linkname != "" {
			fmt.Fprintf(buf, " >%s", stat.Linkname)
		}
		fmt.Fprintln(buf)
		return nil
	}
}

func bufWalkDir(buf *bytes.Buffer) gofs.WalkDirFunc {
	return func(path string, entry gofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := entry.Info()
		if err != nil {
			return err
		}
		stat, ok := fi.Sys().(*types.Stat)
		if !ok {
			return errors.Errorf("invalid symlink %s", path)
		}
		t := "file"
		if fi.IsDir() {
			t = "dir"
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			t = "symlink:" + stat.Linkname
		}
		fmt.Fprintf(buf, "%s %s", t, path)
		if fi.Mode()&os.ModeSymlink == 0 && stat.Linkname != "" {
			fmt.Fprintf(buf, " >%s", stat.Linkname)
		}
		fmt.Fprintln(buf)
		return nil
	}
}

func tmpDir(inp []*change) (dir string, retErr error) {
	tmpdir, err := os.MkdirTemp("", "diff")
	if err != nil {
		return "", err
	}
	defer func() {
		if retErr != nil {
			os.RemoveAll(tmpdir)
		}
	}()
	for _, c := range inp {
		if c.kind == ChangeKindAdd {
			p := filepath.Join(tmpdir, c.path)
			stat, ok := c.fi.Sys().(*types.Stat)
			if !ok {
				return "", errors.Errorf("invalid symlink change %s", p)
			}
			if c.fi.IsDir() {
				if err := os.Mkdir(p, 0700); err != nil {
					return "", err
				}
			} else if c.fi.Mode()&os.ModeSymlink != 0 {
				if err := os.Symlink(stat.Linkname, p); err != nil {
					return "", err
				}
			} else if len(stat.Linkname) > 0 {
				if err := os.Link(filepath.Join(tmpdir, stat.Linkname), p); err != nil {
					return "", err
				}
			} else if c.fi.Mode()&os.ModeSocket != 0 {
				// not closing listener because it would remove the socket file
				if _, err := net.Listen("unix", p); err != nil {
					return "", err
				}
			} else {
				f, err := os.Create(p)
				if err != nil {
					return "", err
				}

				// Make sure all files start with the same default permissions,
				// regardless of OS settings.
				err = os.Chmod(p, 0644)
				if err != nil {
					return "", err
				}

				if len(c.data) > 0 {
					if _, err := f.Write([]byte(c.data)); err != nil {
						return "", err
					}
				}
				f.Close()
			}
		}
	}
	return tmpdir, nil
}

func BenchmarkWalker(b *testing.B) {
	for _, scenario := range []struct {
		maxDepth int
		pattern  string
		exclude  string
		expected int
	}{{
		maxDepth: 1,
		pattern:  "target",
		expected: 1,
	}, {
		maxDepth: 1,
		pattern:  "**/target",
		expected: 1,
	}, {
		maxDepth: 2,
		pattern:  "*/target",
		expected: 52,
	}, {
		maxDepth: 2,
		pattern:  "**/target",
		expected: 52,
	}, {
		maxDepth: 3,
		pattern:  "*/*/target",
		expected: 1378,
	}, {
		maxDepth: 3,
		pattern:  "**/target",
		expected: 1378,
	}, {
		maxDepth: 4,
		pattern:  "*/*/*/target",
		expected: 2794,
	}, {
		maxDepth: 4,
		pattern:  "**/target",
		expected: 2794,
	}, {
		maxDepth: 5,
		pattern:  "*/*/*/*/target",
		expected: 1405,
	}, {
		maxDepth: 5,
		pattern:  "**/target",
		expected: 1405,
	}, {
		maxDepth: 6,
		pattern:  "*/*/*/*/*/target",
		expected: 2388,
	}, {
		maxDepth: 6,
		pattern:  "**/target",
		expected: 2388,
	}, {
		maxDepth: 6,
		pattern:  "**",
		exclude:  "*/*/**",
		expected: 20,
	}} {
		scenario := scenario // copy loop var
		suffix := ""
		if scenario.exclude != "" {
			suffix = fmt.Sprintf("-!%s", scenario.exclude)
		}
		b.Run(fmt.Sprintf("[%d]-%s%s", scenario.maxDepth, scenario.pattern, suffix), func(b *testing.B) {
			tmpdir, err := os.MkdirTemp("", "walk")
			if err != nil {
				b.Error(err)
			}
			defer func() {
				b.StopTimer()
				os.RemoveAll(tmpdir)
			}()
			mkBenchTree(tmpdir, scenario.maxDepth, 1)

			// don't include time to setup dirs in benchmark
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				count := 0
				walkOpt := &FilterOpt{
					IncludePatterns: []string{scenario.pattern},
				}
				if scenario.exclude != "" {
					walkOpt.ExcludePatterns = []string{scenario.exclude}
				}
				err = Walk(context.Background(), tmpdir, walkOpt,
					func(path string, fi gofs.FileInfo, err error) error {
						count++
						return nil
					})
				if err != nil {
					b.Error(err)
				}
				if count != scenario.expected {
					b.Errorf("Got count %d, expected %d", count, scenario.expected)
				}
			}
		})
	}

}

func TestWalkerDoublestarInclude(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD a dir",
		"ADD a/b dir",
		"ADD a/b/baz dir",
		"ADD a/b/bar dir ",
		"ADD a/b/bar/foo file",
		"ADD a/b/bar/fop file",
		"ADD bar dir",
		"ADD bar/foo file",
		"ADD baz dir",
		"ADD foo2 file",
		"ADD foo dir",
		"ADD foo/bar dir",
		"ADD foo/bar/bee file",
	}))

	assert.NoError(t, err)
	defer os.RemoveAll(d)
	b := &bytes.Buffer{}
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir a
		dir a/b
		dir a/b/bar
		file a/b/bar/foo
		file a/b/bar/fop
		dir a/b/baz
		dir bar
		file bar/foo
		dir baz
		dir foo
		dir foo/bar
		file foo/bar/bee
		file foo2
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/bar"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir a
		dir a/b
		dir a/b/bar
		file a/b/bar/foo
		file a/b/bar/fop
		dir bar
		file bar/foo
		dir foo
		dir foo/bar
		file foo/bar/bee
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/bar/foo"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir a
		dir a/b
		dir a/b/bar
		file a/b/bar/foo
		dir bar
		file bar/foo
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/b*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir a
		dir a/b
		dir a/b/bar
		file a/b/bar/foo
		file a/b/bar/fop
		dir a/b/baz
		dir bar
		file bar/foo
		dir baz
		dir foo
		dir foo/bar
		file foo/bar/bee
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/bar/f*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
			dir a
			dir a/b
			dir a/b/bar
			file a/b/bar/foo
			file a/b/bar/fop
			dir bar
			file bar/foo
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/bar/g*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, ``, b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/f*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir a
		dir a/b
		dir a/b/bar
		file a/b/bar/foo
		file a/b/bar/fop
		dir bar
		file bar/foo
		dir foo
		dir foo/bar
		file foo/bar/bee
		file foo2
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/b*/f*"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir a
		dir a/b
		dir a/b/bar
		file a/b/bar/foo
		file a/b/bar/fop
		dir bar
		file bar/foo
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/b*/foo"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir a
		dir a/b
		dir a/b/bar
		file a/b/bar/foo
		dir bar
		file bar/foo
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/foo/**"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir foo
		dir foo/bar
		file foo/bar/bee
	`), b.String())

	b.Reset()
	err = Walk(context.Background(), d, &FilterOpt{
		IncludePatterns: []string{"**/baz"},
	}, bufWalk(b))
	assert.NoError(t, err)

	trimEqual(t, filepath.FromSlash(`
		dir a
		dir a/b
		dir a/b/baz
		dir baz
	`), b.String())
}

func TestFSWalk(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo file",
		"ADD bar dir",
		"ADD bar/foo2 file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	f, err := NewFS(d)
	assert.NoError(t, err)

	b := &bytes.Buffer{}
	err = f.Walk(context.Background(), "", bufWalkDir(b))
	assert.NoError(t, err)
	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo2
file foo
`), b.String())

	b = &bytes.Buffer{}
	err = f.Walk(context.Background(), "foo", bufWalkDir(b))
	assert.NoError(t, err)
	assert.Equal(t, `file foo
`, b.String())

	b = &bytes.Buffer{}
	err = f.Walk(context.Background(), "bar", bufWalkDir(b))
	assert.NoError(t, err)
	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo2
`), b.String())
}

func TestFSWalkNested(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	f, err := NewFS(d)
	assert.NoError(t, err)

	f2, err := NewFilterFS(f, &FilterOpt{
		ExcludePatterns: []string{"foo", "!foo/bar"},
	})
	assert.NoError(t, err)
	b := &bytes.Buffer{}
	err = f2.Walk(context.Background(), "", bufWalkDir(b))
	assert.NoError(t, err)
	assert.Equal(t, filepath.FromSlash(`dir foo
file foo/bar
`), b.String())

	f2, err = NewFilterFS(f, &FilterOpt{
		ExcludePatterns: []string{"!foo/bar"},
	})
	assert.NoError(t, err)
	f2, err = NewFilterFS(f2, &FilterOpt{
		ExcludePatterns: []string{"foo"},
	})
	assert.NoError(t, err)
	b = &bytes.Buffer{}
	err = f2.Walk(context.Background(), "", bufWalkDir(b))
	assert.NoError(t, err)
	assert.Equal(t, ``, b.String())

	f2, err = NewFilterFS(f, &FilterOpt{
		ExcludePatterns: []string{"foo"},
	})
	assert.NoError(t, err)
	f2, err = NewFilterFS(f2, &FilterOpt{
		ExcludePatterns: []string{"!foo/bar"},
	})
	assert.NoError(t, err)
	b = &bytes.Buffer{}
	err = f2.Walk(context.Background(), "", bufWalkDir(b))
	assert.NoError(t, err)
	assert.Equal(t, ``, b.String())
}

func TestFilteredOpen(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo file",
		"ADD bar file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	f, err := NewFS(d)
	assert.NoError(t, err)

	f, err = NewFilterFS(f, &FilterOpt{
		ExcludePatterns: []string{"bar"},
	})
	assert.NoError(t, err)

	b := &bytes.Buffer{}
	err = f.Walk(context.Background(), "", bufWalkDir(b))
	assert.NoError(t, err)
	assert.Equal(t, `file foo
`, b.String())

	r, err := f.Open("foo")
	assert.NoError(t, err)
	defer r.Close()
	dt, err := io.ReadAll(r)
	assert.NoError(t, err)
	assert.Equal(t, "", string(dt))

	_, err = f.Open("bar")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestFilteredOpenWildcard(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD baz file",
		"ADD bar dir",
		"ADD bar2 file",
		"ADD bar/foo file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	f, err := NewFS(d)
	assert.NoError(t, err)
	f, err = NewFilterFS(f, &FilterOpt{
		IncludePatterns: []string{"bar*"},
	})
	assert.NoError(t, err)

	_, err = f.Open("baz")
	assert.ErrorIs(t, err, os.ErrNotExist)

	r, err := f.Open("bar2")
	assert.NoError(t, err)
	assert.NoError(t, r.Close())

	r, err = f.Open("bar/foo")
	assert.NoError(t, err)
	assert.NoError(t, r.Close())
}

func TestFilteredOpenInvert(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar dir",
		"ADD foo/bar/baz dir",
		"ADD foo/bar/baz/x file",
		"ADD foo/bar/baz/y file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	f, err := NewFS(d)
	assert.NoError(t, err)
	f, err = NewFilterFS(f, &FilterOpt{
		ExcludePatterns: []string{"foo", "!foo/bar", "foo/bar/baz", "!foo/bar/baz/x"},
	})
	assert.NoError(t, err)

	r, err := f.Open("foo/bar/baz/x")
	assert.NoError(t, err)
	assert.NoError(t, r.Close())

	_, err = f.Open("foo/bar/baz/y")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func trimEqual(t assert.TestingT, expected, actual string, msgAndArgs ...interface{}) bool {
	lines := []string{}
	for _, line := range strings.Split(expected, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	lines = append(lines, "") // we expect a trailing newline
	expected = strings.Join(lines, "\n")

	return assert.Equal(t, expected, actual, msgAndArgs)
}

// mkBenchTree will create directories named a-z recursively
// up to 3 layers deep.  If maxDepth is > 3 we will shorten
// the last letter to prevent the generated inodes going over
// 25k. The final directory in the tree will contain only files.
// Additionally there is a single file named `target`
// in each leaf directory.
func mkBenchTree(dir string, maxDepth, depth int) error {
	end := 'z'
	switch maxDepth {
	case 1, 2, 3:
		end = 'z' // max 19682 inodes
	case 4:
		end = 'k' // max 19030 inodes
	case 5:
		end = 'e' // max 12438 inodes
	case 6:
		end = 'd' // max 8188 inodes
	case 7, 8:
		end = 'c' // max 16398 inodes
	case 9, 10, 11, 12:
		end = 'b' // max 16378 inodes
	default:
		panic("depth cannot be > 12, would create too many files")
	}

	if depth == maxDepth {
		fd, err := os.Create(filepath.Join(dir, "target"))
		if err != nil {
			return err
		}
		fd.Close()
	}
	for r := 'a'; r <= end; r++ {
		p := filepath.Join(dir, string(r))
		if depth == maxDepth {
			fd, err := os.Create(p)
			if err != nil {
				return err
			}
			fd.Close()
		} else {
			err := os.Mkdir(p, 0755)
			if err != nil {
				return err
			}
			err = mkBenchTree(p, maxDepth, depth+1)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
