package core

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"

	containerdfs "github.com/containerd/continuity/fs"

	"github.com/dagger/dagger/util/hashutil"
)

// A few operations on a subdirectory read files outside it, in the rest of
// its snapshot: resolving a named owner reads the snapshot's account
// databases, and gitignore filtering and search apply the ignore files of the
// directories above it. A subdirectory that is identified by its content must
// carry those files in its identity too: equal subdirectories are
// interchangeable, so an operation on one can run on another, with the other's
// surroundings. Other files around it stay out of its identity, so it is still
// shared across parents that differ elsewhere.

// accountFiles are the account databases resolveDirectoryOwner reads.
var accountFiles = []string{"/etc/passwd", "/etc/group"}

// ignoreFiles are the files gitignore filtering and ripgrep read in each
// directory above the one they read: the ignore files, and the repository
// that decides whether ripgrep applies .gitignore.
var ignoreFiles = []string{".gitignore", ".ignore", ".rgignore", ".git", ".git/info/exclude"}

// outsideInputsDigest digests what operations on the directory at base, in
// the filesystem mounted at root, read outside it: the account databases, the
// ignore files above it and, if there are any, where it is, since their
// patterns match by path; and what its own ignore files read when they are
// symlinks leading out of it. A file that is absent contributes a fixed
// marker, so the digest is the same for every snapshot without these files.
func outsideInputsDigest(root, base string) (string, error) {
	var parts []string
	for _, p := range accountFiles {
		resolved, err := containerdfs.RootPath(root, p)
		if err != nil {
			return "", err
		}
		state, err := fileState(resolved)
		if err != nil {
			return "", err
		}
		parts = append(parts, p, state)
	}

	rel, err := filepath.Rel(root, base)
	if err != nil {
		return "", err
	}
	dirPath := path.Join("/", filepath.ToSlash(rel))
	anyIgnores := false
	var ignores []string
	// Read them as the matchers do: by path from the mount.
	for ancestor := path.Dir(dirPath); ; ancestor = path.Dir(ancestor) {
		for _, name := range ignoreFiles {
			p := path.Join(ancestor, name)
			state, err := fileState(filepath.Join(root, filepath.FromSlash(p)))
			if err != nil {
				return "", err
			}
			anyIgnores = anyIgnores || state != absentState
			ignores = append(ignores, p, state)
		}
		if ancestor == "/" {
			break
		}
	}
	if anyIgnores {
		parts = append(parts, "path", dirPath)
		parts = append(parts, ignores...)
	}

	linked, err := ignoreLinksLeavingDir(base)
	if err != nil {
		return "", err
	}
	for _, rel := range linked {
		state, err := fileState(filepath.Join(base, rel))
		if err != nil {
			return "", err
		}
		parts = append(parts, "link", filepath.ToSlash(rel), state)
	}
	return hashutil.HashStrings(parts...).String(), nil
}

// ignoreLinksLeavingDir returns the ignore files in the directory at base that
// are symlinks leading out of it: the matchers read them through the link,
// so what they apply is not in the directory's content.
func ignoreLinksLeavingDir(base string) ([]string, error) {
	var linked []string
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink == 0 || !isIgnoreFileName(d.Name()) {
			return err
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		leaves, err := leavesDir(base, filepath.ToSlash(rel))
		if err != nil || !leaves {
			return err
		}
		linked = append(linked, rel)
		return nil
	})
	return linked, err
}

func isIgnoreFileName(name string) bool {
	switch name {
	case ".gitignore", ".ignore", ".rgignore", ".git":
		return true
	}
	return false
}

const absentState = "absent"

// fileState describes what a reader of p finds there: nothing, a directory,
// or a file and its contents.
func fileState(p string) (string, error) {
	info, err := os.Stat(p)
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return absentState, nil
	case err != nil:
		return "", err
	case info.IsDir():
		return "directory", nil
	}
	contents, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return "file:" + hashutil.HashStrings(string(contents)).String(), nil
}
