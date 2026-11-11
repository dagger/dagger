package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	gitconfig "github.com/go-git/go-git/v5/plumbing/format/config"

	"github.com/dagger/dagger/util/gitutil"
)

// ValidateSelfContained ensures that reopening supplied storage cannot depend
// on a worktree pointer, common directory, or object store outside its root.
func (repo *LocalGitRepository) ValidateSelfContained(ctx context.Context) error {
	return repo.mount(ctx, 0, false, nil, func(ctx context.Context, git *gitutil.GitCLI) error {
		root, err := filepath.EvalSymlinks(git.Dir())
		if err != nil {
			return err
		}
		contained := func(p string) (string, error) {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(root, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("git storage must be self-contained: path %q is outside the supplied directory", p)
			}
			return resolved, nil
		}
		// Git reads the repository's config, and every file it includes,
		// as soon as it runs, so check what it would read first.
		if err := checkLayoutContained(root); err != nil {
			return err
		}
		gitDir, err := git.GitDir(ctx)
		if err != nil {
			return err
		}
		if _, err := contained(gitDir); err != nil {
			return err
		}
		common, err := git.Run(ctx, "rev-parse", "--git-common-dir")
		if err != nil {
			return err
		}
		if _, err := contained(strings.TrimSpace(string(common))); err != nil {
			return err
		}
		worktree, err := git.WorkTree(ctx)
		if err != nil {
			return err
		}
		if worktree != "" {
			if _, err := contained(worktree); err != nil {
				return err
			}
		}
		return nil
	})
}

// checkLayoutContained checks, without running Git, that the files Git
// reads in the repository at root stay within root: the repository it finds
// at root/.git, a directory, a link or a file naming it, or at root itself
// when root is bare; its common directory; their config files and the files
// those include; the object stores they borrow from; and every link in the
// repository's metadata. Each is opened beneath root (see os.Root), so one
// that a ".." or a link would lead out of root is rejected, and nothing
// outside root is read.
func checkLayoutContained(root string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	gitDir := "."
	info, err := r.Lstat(".git")
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case info.Mode().IsRegular():
		data, err := r.ReadFile(".git")
		if err != nil {
			return err
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		if !ok {
			return fmt.Errorf("git storage: .git is not a gitdir file")
		}
		if gitDir, err = beneath(root, ".", strings.TrimSpace(target)); err != nil {
			return err
		}
		if _, err := r.Stat(gitDir); err != nil {
			return selfContainedErr(gitDir, err)
		}
	default:
		if _, err := r.Stat(".git"); err != nil {
			return selfContainedErr(".git", err)
		}
		gitDir = ".git"
	}
	commonDir := gitDir
	data, err := r.ReadFile(gitDir + "/commondir")
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return selfContainedErr(gitDir+"/commondir", err)
	default:
		if commonDir, err = beneath(root, gitDir, strings.TrimSpace(string(data))); err != nil {
			return err
		}
	}
	if _, err := r.Stat(gitDir + "/HEAD"); errors.Is(err, os.ErrNotExist) {
		// Not a repository: Git finds none here, and reads nothing more.
		return nil
	}
	var walked []fs.FileInfo
	for _, dir := range []string{gitDir, commonDir} {
		if err := checkLinksContained(r, root, dir, &walked); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, config := range []string{gitDir + "/config", gitDir + "/config.worktree", commonDir + "/config"} {
		if err := checkConfigIncludes(r, root, config, seen); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, objects := range []string{gitDir + "/objects", commonDir + "/objects"} {
		if err := checkAlternates(r, root, objects, seen); err != nil {
			return err
		}
	}
	return nil
}

// checkLinksContained checks that every link in the directory at dir, a
// path beneath root, and in the directories its links lead to, stays within
// root: Git reads refs, objects and other metadata through them.
func checkLinksContained(r *os.Root, root, dir string, walked *[]fs.FileInfo) error {
	info, err := r.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return selfContainedErr(dir, err)
	}
	for _, other := range *walked {
		if os.SameFile(other, info) {
			return nil
		}
	}
	*walked = append(*walked, info)
	// dir stays within root, so its real path does too; walk it by that.
	real, err := filepath.EvalSymlinks(filepath.Join(root, dir))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil {
		return err
	}
	return fs.WalkDir(r.FS(), filepath.ToSlash(rel), func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&fs.ModeSymlink == 0 {
			return err
		}
		target, err := r.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return selfContainedErr(p, err)
		}
		if target.IsDir() {
			return checkLinksContained(r, root, p, walked)
		}
		return nil
	})
}

// checkAlternates checks that the object stores the object store at objects,
// a path beneath root as Git opens it, borrows from stay within root.
func checkAlternates(r *os.Root, root, objects string, seen map[string]bool) error {
	if seen[objects] {
		return nil
	}
	seen[objects] = true
	data, err := r.ReadFile(objects + "/info/alternates")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return selfContainedErr(objects+"/info/alternates", err)
	}
	for _, alternate := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if alternate == "" {
			continue
		}
		alternate, err := beneath(root, objects, alternate)
		if err != nil {
			return err
		}
		if _, err := r.Stat(alternate); err != nil {
			return selfContainedErr(alternate, err)
		}
		if err := checkAlternates(r, root, alternate, seen); err != nil {
			return err
		}
	}
	return nil
}

// checkConfigIncludes checks that the Git config file at config, a path
// beneath root as Git opens it, and the files it includes, stay within root.
func checkConfigIncludes(r *os.Root, root, config string, seen map[string]bool) error {
	if seen[config] {
		return nil
	}
	seen[config] = true
	data, err := r.ReadFile(config)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return selfContainedErr(config, err)
	}
	cfg := gitconfig.New()
	if err := gitconfig.NewDecoder(bytes.NewReader(data)).Decode(cfg); err != nil {
		return fmt.Errorf("git config %s: %w", config, err)
	}
	includes := cfg.Section("include").Options.GetAll("path")
	for _, sub := range cfg.Section("includeIf").Subsections {
		includes = append(includes, sub.OptionAll("path")...)
	}
	for _, include := range includes {
		// Git resolves an include relative to the path it opened the
		// including file by, as written.
		include, err := beneath(root, parentDir(config), include)
		if err != nil {
			return err
		}
		if _, err := r.Stat(include); errors.Is(err, os.ErrNotExist) {
			// Git skips an include that doesn't exist.
			continue
		} else if err != nil {
			return selfContainedErr(include, err)
		}
		if err := checkConfigIncludes(r, root, include, seen); err != nil {
			return err
		}
	}
	return nil
}

// beneath returns p, a path Git reads relative to dir, a path beneath root,
// as a path beneath root, without cleaning it: Git opens it as written. An
// absolute p must be in root, and one Git expands, starting with "~" or
// "%(prefix)", is outside it.
func beneath(root, dir, p string) (string, error) {
	switch {
	case strings.HasPrefix(p, "~"), strings.HasPrefix(p, "%("):
		return "", selfContainedErr(p, errors.New("Git expands it to a path outside it"))
	case filepath.IsAbs(p):
		rel, ok := strings.CutPrefix(p, root+"/")
		if !ok {
			return "", selfContainedErr(p, errors.New("an absolute path outside it"))
		}
		return rel, nil
	default:
		return dir + "/" + p, nil
	}
}

// parentDir is the directory part of p, a path as written.
func parentDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

func selfContainedErr(p string, err error) error {
	return fmt.Errorf("git storage must be self-contained: %q can't be read within the supplied directory: %w", p, err)
}
