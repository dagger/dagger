package core

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	bkcache "github.com/dagger/dagger/engine/snapshots"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/hashutil"
)

// A few operations on a directory read files outside it, in the rest of its
// snapshot: resolving a named owner reads the snapshot's account databases,
// and gitignore filtering and search apply the ignore files of the directories
// above it. A directory is identified by its own content, so two directories
// with equal files can stand for each other while what surrounds them differs.
// Those operations add what they read there to their own identity instead,
// through the digests below, and leave the directory's alone.

// OwnerAccountsInput is the implicit input that keys an operation resolving a
// named owner by the account databases it reads.
const OwnerAccountsInput = "ownerAccounts"

// AncestorIgnoresInput is the implicit input that keys a gitignore-filtered
// read or a search by the ignore files above the directory it reads.
const AncestorIgnoresInput = "ancestorIgnores"

// GitignoreFiles are the ignore files a gitignore-filtered copy applies.
var GitignoreFiles = []string{".gitignore"}

// SearchIgnoreFiles are what a search that skips ignored files reads in
// each directory above the one it searches: ripgrep's ignore files, and the
// repository that decides whether .gitignore applies.
var SearchIgnoreFiles = []string{".gitignore", ".ignore", ".rgignore", ".git", ".git/info/exclude"}

// OwnerNamesAccount reports whether owner, a user[:group], names a user or a
// group, so resolving it reads the account databases, rather than giving ids.
func OwnerNamesAccount(owner string) bool {
	if owner == "" {
		return false
	}
	user, group, hasGroup := strings.Cut(owner, ":")
	if _, err := parseUID(user); err != nil {
		return true
	}
	if hasGroup {
		if _, err := parseUID(group); err != nil {
			return true
		}
	}
	return false
}

// OwnerAccountsDigest returns a digest of the account databases resolving
// owner reads in dir's snapshot, or "" when owner gives ids, or dir is its
// whole snapshot, so the databases are part of its content.
func (dir *Directory) OwnerAccountsDigest(ctx context.Context, self dagql.ObjectResult[*Directory], owner string) (string, error) {
	if !OwnerNamesAccount(owner) {
		return "", nil
	}
	snapshot, dirPath, err := evaluatedDirectory(ctx, self)
	if err != nil || snapshot == nil {
		return "", err
	}
	var dgst string
	err = MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		base, err := containerdfs.RootPath(root, dirPath)
		if err != nil || base == root {
			return err
		}
		dgst, err = accountsDigest(root)
		return err
	}, mountRefAsReadOnly)
	return dgst, err
}

// OwnerAccountsDigest returns a digest of the account databases resolving
// owner reads in file's snapshot, or "" when owner gives ids. A file is
// identified by its own content, so the databases are always outside it.
func (file *File) OwnerAccountsDigest(ctx context.Context, self dagql.ObjectResult[*File], owner string) (string, error) {
	if !OwnerNamesAccount(owner) {
		return "", nil
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return "", err
	}
	if err := cache.Evaluate(ctx, self); err != nil {
		return "", err
	}
	snapshot, err := file.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil || snapshot == nil {
		return "", err
	}
	var dgst string
	err = MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		dgst, err = accountsDigest(root)
		return err
	}, mountRefAsReadOnly)
	return dgst, err
}

// AncestorIgnoreDigest returns a digest of the files named in names in each
// directory above dir in its snapshot, or "" when dir is its whole snapshot,
// so there are none.
func (dir *Directory) AncestorIgnoreDigest(ctx context.Context, self dagql.ObjectResult[*Directory], names []string) (string, error) {
	snapshot, dirPath, err := evaluatedDirectory(ctx, self)
	if err != nil || snapshot == nil {
		return "", err
	}
	var dgst string
	err = MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		base, err := viewBase(root, dirPath)
		if err != nil || base == "/" {
			return err
		}
		var parts []string
		// Read them as the matchers do: by path from the mount, following
		// symlinks.
		for ancestor := path.Dir(base); ; ancestor = path.Dir(ancestor) {
			for _, name := range names {
				p := path.Join(ancestor, name)
				state, err := fileState(filepath.Join(root, filepath.FromSlash(p)))
				if err != nil {
					return err
				}
				parts = append(parts, p, state)
			}
			if ancestor == "/" {
				break
			}
		}
		dgst = hashutil.HashStrings(parts...).String()
		return nil
	}, mountRefAsReadOnly)
	return dgst, err
}

func evaluatedDirectory(ctx context.Context, self dagql.ObjectResult[*Directory]) (bkcache.ImmutableRef, string, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, "", err
	}
	if err := cache.Evaluate(ctx, self); err != nil {
		return nil, "", err
	}
	snapshot, err := self.Self().Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, "", err
	}
	dirPath, err := self.Self().Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, "", err
	}
	return snapshot, dirPath, nil
}

// accountsDigest digests the account databases resolveDirectoryOwner reads in
// the filesystem mounted at root.
func accountsDigest(root string) (string, error) {
	var parts []string
	for _, p := range []string{"/etc/passwd", "/etc/group"} {
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
	return hashutil.HashStrings(parts...).String(), nil
}

// fileState describes what a reader of p finds there: nothing, a directory,
// or a file and its contents.
func fileState(p string) (string, error) {
	info, err := os.Stat(p)
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return "absent", nil
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
