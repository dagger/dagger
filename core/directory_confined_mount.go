package core

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	containerdfs "github.com/containerd/continuity/fs"

	"github.com/dagger/dagger/dagql"
)

// MountConfined mounts dir read-only for f, like Mount, for a caller that
// reads its files by path, so the host's own path resolution follows its
// symlinks. A symlink that is absolute or climbs above dir would then lead out
// of it, to its surroundings in the snapshot or to the engine's filesystem,
// where every Directory operation resolves it within dir (see ResolveInDir).
// So if dir has any such symlink, f gets a copy of dir in which each of them
// points where it resolves within dir; otherwise f gets the mount itself.
//
// Only symlinks are confined: f must not itself reach above the path it is
// given, by "..", an absolute path, or by searching parent directories. The
// Dang runtime, its caller, installs its import configs before evaluating, so
// the dang library doesn't search parent directories for a project config.
func (dir *Directory) MountConfined(ctx context.Context, self dagql.ObjectResult[*Directory], f func(string) error) error {
	return dir.Mount(ctx, self, func(base string) error {
		return withLinksConfined(base, f)
	})
}

// withLinksConfined calls f with the directory at base, or with a copy of it
// whose symlinks that lead out of it point where they resolve within it.
func withLinksConfined(base string, f func(string) error) error {
	leaving, err := linksLeavingDir(base)
	if err != nil {
		return err
	}
	if len(leaving) == 0 {
		return f(base)
	}
	confined, err := os.MkdirTemp("", "dagger-confined-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(confined)
	if err := copyTreeKeepingLinks(base, confined); err != nil {
		return fmt.Errorf("copy directory to confine its links: %w", err)
	}
	for _, rel := range leaving {
		// Resolve against the original, not the copy being rewritten.
		resolved, err := containerdfs.RootPath(base, rel)
		if err != nil {
			// A link that can't be resolved, such as one in a cycle, can't
			// be followed in the copy either.
			if err := os.Remove(filepath.Join(confined, rel)); err != nil {
				return err
			}
			continue
		}
		within, err := filepath.Rel(base, resolved)
		if err != nil {
			return err
		}
		link := filepath.Join(confined, rel)
		target, err := filepath.Rel(filepath.Dir(link), filepath.Join(confined, within))
		if err != nil {
			return err
		}
		if err := os.Remove(link); err != nil {
			return err
		}
		if err := os.Symlink(target, link); err != nil {
			return err
		}
	}
	return f(confined)
}

// linksLeavingDir returns the paths, relative to base, of the symlinks in the
// directory at base that lead out of it (see leavesDir).
func linksLeavingDir(base string) ([]string, error) {
	var leaving []string
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink == 0 {
			return err
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		leaves, err := leavesDir(base, filepath.ToSlash(rel))
		if err != nil {
			// A link that can't be resolved within dir, such as one in a
			// cycle, leads nowhere; following it fails as it would anyway.
			return nil //nolint:nilerr
		}
		if leaves {
			leaving = append(leaving, rel)
		}
		return nil
	})
	return leaving, err
}

// copyTreeKeepingLinks copies the directories, regular files and symlinks
// under src to dest, which must exist, keeping symlinks as they are.
func copyTreeKeepingLinks(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		out := filepath.Join(dest, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			// Owner access lets the copy be filled and removed.
			return os.Mkdir(out, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, out)
		case d.Type().IsRegular():
			return copyRegularFile(p, out, info.Mode().Perm())
		}
		return nil
	})
}

func copyRegularFile(src, dest string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
