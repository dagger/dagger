package telemetry

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"
)

// openGitWithWorktreeConfig works around go-git v5 rejecting the valid
// worktreeConfig extension. Git resolves both directories so linked worktrees
// retain their own HEAD while reading shared objects, refs, and config.
func openGitWithWorktreeConfig(workdir string) (*git.Repository, error) {
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("resolve git directories: %w", err)
	}
	dirs := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(dirs) != 2 {
		return nil, fmt.Errorf("expected two git directories, got %q", out)
	}
	fs := dotgit.NewRepositoryFilesystem(osfs.New(dirs[0]), osfs.New(dirs[1]))
	st := filesystem.NewStorage(fs, cache.NewObjectLRUDefault())
	// Labels only read objects and refs; no worktree filesystem is needed.
	return git.Open(worktreeConfigStorer{st}, nil)
}

type worktreeConfigStorer struct {
	storage.Storer
}

func (s worktreeConfigStorer) Config() (*config.Config, error) {
	cfg, err := s.Storer.Config()
	if err != nil {
		return nil, err
	}
	// Filter only the in-memory config used by go-git. Keep all other extension
	// and repository format validation, and never rewrite the user's config.
	if cfg.Raw != nil && cfg.Raw.HasSection("extensions") {
		cfg.Raw.Section("extensions").RemoveOption("worktreeConfig")
	}
	return cfg, nil
}
