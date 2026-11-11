package core

import (
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
							alternate = filepath.Join(filepath.Dir(filepath.Dir(p)), alternate)
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

// containedPath resolves p, relative to root unless absolute, and returns
// it if it is within root. A path outside root by its text is rejected
// without being read.
func containedPath(root, p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	outside := fmt.Errorf("git storage must be self-contained: path %q is outside the supplied directory", p)
	if rel, err := filepath.Rel(root, p); err != nil || !filepath.IsLocal(rel) {
		return "", outside
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(root, resolved); err != nil || !filepath.IsLocal(rel) {
		return "", outside
	}
	return resolved, nil
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
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		if gitDir, err = containedPath(root, target); err != nil {
			return err
		}
	default:
		if gitDir, err = containedPath(root, dotGit); err != nil {
			return err
		}
	}
	commonDir := gitDir
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		target := strings.TrimSpace(string(data))
		if !filepath.IsAbs(target) {
			target = filepath.Join(gitDir, target)
		}
		if commonDir, err = containedPath(root, target); err != nil {
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
	f, err := os.Open(config)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	cfg := gitconfig.New()
	if err := gitconfig.NewDecoder(f).Decode(cfg); err != nil {
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
		if !filepath.IsAbs(include) {
			include = filepath.Join(filepath.Dir(config), include)
		}
		resolved, err := containedPath(root, include)
		if errors.Is(err, os.ErrNotExist) {
			// Git skips an include that doesn't exist.
			continue
		}
		if err != nil {
			return err
		}
		if err := checkConfigIncludes(root, resolved, seen); err != nil {
			return err
		}
	}
	return nil
}
