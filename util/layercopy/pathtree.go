package layercopy

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// PathTree is a directory tree known only from its paths, such as a Git tree
// whose files aren't on disk. It answers what a copy of the tree would select
// without reading any file.
type PathTree struct {
	// children lists each directory's entries, by the directory's path from
	// the tree's root ("" is the root).
	children map[string][]pathTreeEntry
	// dirs holds how many files are below each directory, at any depth.
	dirs map[string]int
}

type pathTreeEntry struct {
	name  string
	isDir bool
}

// NewPathTree builds a tree from the slash-separated paths of everything in it
// that isn't a directory. Their parents are its directories.
func NewPathTree(files []string) *PathTree {
	t := &PathTree{
		children: map[string][]pathTreeEntry{},
		dirs:     map[string]int{"": 0},
	}
	for _, file := range files {
		t.add(file, false)
		for dir := path.Dir(file); ; dir = path.Dir(dir) {
			if dir == "." {
				t.dirs[""]++
				break
			}
			t.dirs[dir]++
		}
	}
	return t
}

func (t *PathTree) add(p string, isDir bool) {
	if isDir {
		if _, known := t.dirs[p]; known {
			return
		}
		t.dirs[p] = 0
	}
	parent := path.Dir(p)
	if parent == "." {
		parent = ""
	}
	t.children[parent] = append(t.children[parent], pathTreeEntry{name: path.Base(p), isDir: isDir})
	if parent != "" {
		t.add(parent, true)
	}
}

// IsDir reports whether rel, a slash-separated path from the tree's root, is
// one of its directories.
func (t *PathTree) IsDir(rel string) bool {
	_, ok := t.dirs[strings.TrimPrefix(path.Clean("/"+rel), "/")]
	return ok
}

// FileCount returns how many files are below the directory rel, at any depth.
func (t *PathTree) FileCount(rel string) int {
	return t.dirs[strings.TrimPrefix(path.Clean("/"+rel), "/")]
}

// SelectedFiles returns what a Copy of the directory root with this filter
// selects other than directories, as paths from the tree's root. It visits the
// tree as the copy does, with the copy's own matcher, so the two can't disagree
// on a pattern. A gitignore filter reads files, so it can't be answered here.
func (t *PathTree) SelectedFiles(root string, filter Filter) ([]string, error) {
	if filter.Gitignore {
		return nil, fmt.Errorf("path tree can't apply a gitignore filter")
	}
	m, err := newMatcher("", filter)
	if err != nil {
		return nil, err
	}
	root = strings.TrimPrefix(path.Clean("/"+root), "/")
	var selected []string
	var walk func(rel string, parent matchState) error
	walk = func(rel string, parent matchState) error {
		for _, entry := range t.children[path.Join(root, rel)] {
			childRel := path.Join(rel, entry.name)
			include, state, err := m.includePath(childRel, "", entry.isDir, parent)
			if err != nil {
				return err
			}
			if !entry.isDir {
				if include {
					selected = append(selected, path.Join(root, childRel))
				}
				continue
			}
			if !m.shouldDescend(childRel, state) {
				continue
			}
			if err := walk(childRel, state); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk("", matchState{}); err != nil {
		return nil, err
	}
	slices.Sort(selected)
	return selected, nil
}
