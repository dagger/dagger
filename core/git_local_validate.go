package core

import (
	"context"
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
	return repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
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
		return check(commonDir)
	})
}
