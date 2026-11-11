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

	containerdfs "github.com/containerd/continuity/fs"
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
			return containedPath(root, p)
		}
		// Git reads the repository's config, and every file it includes,
		// as soon as it runs, so check what it would read from the layout
		// alone first.
		if err := checkLayoutContained(root); err != nil {
			return err
		}
		gitDir, err := git.GitDir(ctx)
		if err != nil {
			return err
		}
		gitDir, err = contained(gitDir)
		if err != nil {
			return err
		}
		common, err := git.Run(ctx, "rev-parse", "--git-common-dir")
		if err != nil {
			return err
		}
		commonDir, err := contained(strings.TrimSpace(string(common)))
		if err != nil {
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
		// Walk resolved metadata directories (including internal symlink targets)
		// once, validating alternate object databases as part of the same traversal.
		seen := map[string]bool{}
		var check func(string) error
		check = func(dir string) error {
			if seen[dir] {
				return nil
			}
			seen[dir] = true
			return filepath.WalkDir(dir, func(p string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.Type()&os.ModeSymlink != 0 {
					resolved, err := contained(p)
					if err != nil {
						return err
					}
					info, err := os.Stat(resolved)
					if err != nil {
						return err
					}
					if info.IsDir() {
						return check(resolved)
					}
				}
				if entry.Name() == "alternates" && filepath.Base(filepath.Dir(p)) == "info" {
					data, err := os.ReadFile(p)
					if err != nil {
						return err
					}
					for _, alternate := range strings.Split(strings.TrimSpace(string(data)), "\n") {
						if alternate == "" {
							continue
						}
						if !filepath.IsAbs(alternate) {
							alternate = joinPath(filepath.Dir(filepath.Dir(p)), alternate)
						}
						resolved, err := contained(alternate)
						if err != nil {
							return err
						}
						if err := check(resolved); err != nil {
							return err
						}
					}
				}
				return nil
			})
		}
		if err := check(gitDir); err != nil {
			return err
		}
		if err := check(commonDir); err != nil {
			return err
		}
		return nil
	})
}

// containedPath resolves p, relative to root unless absolute, as the kernel
// opens it, and returns the resolved path if it stays within root. Each
// component is resolved in turn, so a ".." after a link climbs from where the
// link leads, and a path that would leave root is rejected without anything
// outside it being read.
func containedPath(root, p string) (string, error) {
	p = joinPath(root, p)
	outside := fmt.Errorf("git storage must be self-contained: path %q is outside the supplied directory", p)
	rel, ok := strings.CutPrefix(p, root+string(filepath.Separator))
	if !ok {
		if p == root {
			return root, nil
		}
		return "", outside
	}
	leaves, err := leavesDir(root, filepath.ToSlash(rel))
	if err != nil {
		return "", err
	}
	if leaves {
		return "", outside
	}
	resolved, err := containerdfs.RootPath(root, rel)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// joinPath joins p to dir unless p is absolute, keeping p's components as
// they are: Git opens such paths as written, and cleaning ".." from them
// would change where they lead when a component before it is a link.
func joinPath(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return dir + string(filepath.Separator) + p
}

// parentPath is the directory part of p as written, without cleaning it.
func parentPath(p string) string {
	return p[:strings.LastIndex(p, string(filepath.Separator))]
}

// readContained reads the file at p, a path within root, if it and any link
// it is reached through stay within root.
func readContained(root, p string) ([]byte, error) {
	resolved, err := containedPath(root, p)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}

// checkLayoutContained checks, without running Git, that the repository Git
// would open at root, its common directory and the config files they include
// are all within root. Git finds the repository at root/.git, a directory,
// a link or a file naming it, or at root itself when root is bare.
func checkLayoutContained(root string) error {
	gitDir := root
	dotGit := filepath.Join(root, ".git")
	info, err := os.Lstat(dotGit)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case info.Mode().IsRegular():
		data, err := os.ReadFile(dotGit)
		if err != nil {
			return err
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		if !ok {
			return fmt.Errorf("git storage: %s is not a gitdir file", dotGit)
		}
		target = strings.TrimSpace(target)
		if gitDir, err = containedPath(root, target); err != nil {
			return err
		}
	default:
		if gitDir, err = containedPath(root, dotGit); err != nil {
			return err
		}
	}
	commonDir := gitDir
	if data, err := readContained(root, filepath.Join(gitDir, "commondir")); err == nil {
		target := strings.TrimSpace(string(data))
		if commonDir, err = containedPath(root, joinPath(gitDir, target)); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	seen := map[string]bool{}
	for _, config := range []string{
		filepath.Join(gitDir, "config"),
		filepath.Join(gitDir, "config.worktree"),
		filepath.Join(commonDir, "config"),
	} {
		if err := checkConfigIncludes(root, config, seen); err != nil {
			return err
		}
	}
	return nil
}

// checkConfigIncludes ensures that the Git config file at config, and the
// files it includes, include no file outside root, reading only files within
// root.
func checkConfigIncludes(root, config string, seen map[string]bool) error {
	if seen[config] {
		return nil
	}
	seen[config] = true
	data, err := readContained(root, config)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
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
		if strings.HasPrefix(include, "~") {
			return fmt.Errorf("git storage must be self-contained: config %q includes %q outside the supplied directory", config, include)
		}
		include = joinPath(parentPath(config), include)
		if _, err := containedPath(root, include); errors.Is(err, os.ErrNotExist) {
			// Git skips an include that doesn't exist.
			continue
		} else if err != nil {
			return err
		}
		// Git resolves the included file's own includes relative to the
		// path it included it by, not where a link there leads.
		if err := checkConfigIncludes(root, include, seen); err != nil {
			return err
		}
	}
	return nil
}
