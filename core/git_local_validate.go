package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
		for _, config := range []string{
			filepath.Join(gitDir, "config"),
			filepath.Join(gitDir, "config.worktree"),
			filepath.Join(commonDir, "config"),
		} {
			if err := checkConfigIncludes(ctx, git, root, config, map[string]bool{}); err != nil {
				return err
			}
		}
		return nil
	})
}

// checkConfigIncludes ensures that the Git config file at config, and the
// files it includes, include no file outside root. Git reads an included file
// as part of the config, so one outside would be read too. A path that leaves
// root by its text is rejected without being read.
func checkConfigIncludes(ctx context.Context, git *gitutil.GitCLI, root, config string, seen map[string]bool) error {
	if seen[config] {
		return nil
	}
	seen[config] = true
	if _, err := os.Lstat(config); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	out, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "config", "--file", config, "--no-includes", "-z", "--get-regexp", `^include\.path$|^includeif\..*\.path$`)
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		_, include, ok := strings.Cut(entry, "\n")
		if !ok {
			continue
		}
		if strings.HasPrefix(include, "~") {
			return fmt.Errorf("git storage must be self-contained: config %q includes %q outside the supplied directory", config, include)
		}
		if !filepath.IsAbs(include) {
			include = filepath.Join(filepath.Dir(config), include)
		}
		rel, err := filepath.Rel(root, include)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("git storage must be self-contained: config %q includes %q outside the supplied directory", config, include)
		}
		resolved, err := filepath.EvalSymlinks(include)
		if errors.Is(err, os.ErrNotExist) {
			// Git skips an include that doesn't exist.
			continue
		}
		if err != nil {
			return err
		}
		rel, err = filepath.Rel(root, resolved)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("git storage must be self-contained: config %q includes %q outside the supplied directory", config, include)
		}
		if err := checkConfigIncludes(ctx, git, root, resolved, seen); err != nil {
			return err
		}
	}
	return nil
}
