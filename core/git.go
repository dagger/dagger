package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
)

type GitRepository struct {
	URL dagql.Nullable[dagql.String] `field:"true" doc:"The URL of the git repository."`

	Backend GitRepositoryBackend
	Remote  *gitutil.Remote

	DiscardGitDir bool
}

type GitRepositoryBackend interface {
	HasPBDefinitions

	// Get returns a reference to a specific git ref (branch, tag, or commit).
	Get(ctx context.Context, ref *gitutil.Ref) (GitRefBackend, error)
	// Remote returns information about the git remote.
	Remote(ctx context.Context) (*gitutil.Remote, error)

	mount(ctx context.Context, depth int, refs []GitRefBackend, fn func(*gitutil.GitCLI) error) error
	equivalent(GitRepositoryBackend) bool
}

type GitRef struct {
	Repo    *GitRepository
	Backend GitRefBackend
	Ref     *gitutil.Ref
}

type GitRefBackend interface {
	HasPBDefinitions

	Repo() GitRepositoryBackend
	Tree(ctx context.Context, srv *dagql.Server, discard bool, depth int) (checkout *Directory, err error)

	mount(ctx context.Context, depth int, fn func(*gitutil.GitCLI) error) error
}

func NewGitRepository(ctx context.Context, backend GitRepositoryBackend) (*GitRepository, error) {
	repo := &GitRepository{
		Backend: backend,
	}

	remote, err := backend.Remote(ctx)
	if err != nil {
		return nil, err
	}
	repo.Remote = remote

	if remoteBackend, ok := backend.(*RemoteGitRepository); ok {
		repo.URL = dagql.NonNull(dagql.String(remoteBackend.URL.String()))
	}

	return repo, nil
}

func (*GitRepository) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRepository",
		NonNull:   true,
	}
}

func (*GitRepository) TypeDescription() string {
	return "A git repository."
}

func (repo *GitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return repo.Backend.PBDefinitions(ctx)
}

func (repo *GitRepository) Ref(ctx context.Context, name string) (*GitRef, error) {
	ref, err := repo.Remote.Lookup(name)
	if err != nil {
		return nil, err
	}

	result, err := repo.Backend.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &GitRef{repo, result, ref}, nil
}

func (repo *GitRepository) Tags(patterns []string) []string {
	tags := repo.Remote.Tags()
	var tagNames []string
	for _, tag := range tags {
		tagNames = append(tagNames, strings.TrimPrefix(tag.Name, "refs/tags/"))
	}
	return filterRefs(tagNames, patterns)
}

func (repo *GitRepository) Branches(patterns []string) []string {
	branches := repo.Remote.Branches()
	var branchNames []string
	for _, branch := range branches {
		branchNames = append(branchNames, strings.TrimPrefix(branch.Name, "refs/heads/"))
	}
	return filterRefs(branchNames, patterns)
}

func filterRefs(refs []string, patterns []string) []string {
	// XXX: implement
	return refs
}

func (*GitRef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRef",
		NonNull:   true,
	}
}

func (*GitRef) TypeDescription() string {
	return "A git ref (tag, branch, or commit)."
}

func (ref *GitRef) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return ref.Backend.PBDefinitions(ctx)
}

func (ref *GitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (*Directory, error) {
	return ref.Backend.Tree(ctx, srv, ref.Repo.DiscardGitDir || discardGitDir, depth)
}

func doGitCheckout(
	ctx context.Context,
	checkoutGit *gitutil.GitCLI,
	remoteURL string,
	cloneURL string,
	fullref string, commit string,
	depth int,
	discardGitDir bool,
) error {
	checkoutDirGit, err := checkoutGit.GitDir(ctx)
	if err != nil {
		return fmt.Errorf("could not find git dir: %w", err)
	}

	pullref := fullref
	if !gitutil.IsCommitSHA(fullref) {
		pullref += ":" + pullref
	}

	args := []string{"fetch", "-u"}
	if depth > 0 {
		args = append(args, fmt.Sprintf("--depth=%d", depth))
	}
	args = append(args, cloneURL, pullref)
	_, err = checkoutGit.Run(ctx, args...)
	if err != nil {
		return err
	}
	_, err = checkoutGit.Run(ctx, "checkout", strings.TrimPrefix(fullref, "refs/heads/"))
	if err != nil {
		return fmt.Errorf("failed to checkout remote %s: %w", cloneURL, err)
	}
	_, err = checkoutGit.Run(ctx, "reset", "--hard", commit)
	if err != nil {
		return fmt.Errorf("failed to reset ref: %w", err)
	}
	if remoteURL != "" {
		_, err = checkoutGit.Run(ctx, "remote", "add", "origin", remoteURL)
		if err != nil {
			return fmt.Errorf("failed to set remote origin to %s: %w", remoteURL, err)
		}
	}
	_, err = checkoutGit.Run(ctx, "reflog", "expire", "--all", "--expire=now")
	if err != nil {
		return fmt.Errorf("failed to expire reflog: %w", err)
	}

	if err := os.Remove(filepath.Join(checkoutDirGit, "FETCH_HEAD")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove FETCH_HEAD: %w", err)
	}

	// TODO: this feels completely out-of-sync from how we do the rest
	// of the clone - caching will not be as great here
	subArgs := []string{"submodule", "update", "--init", "--recursive", "--depth=1"}
	if _, err := checkoutGit.Run(ctx, subArgs...); err != nil {
		if errors.Is(err, gitutil.ErrShallowNotSupported) {
			subArgs = slices.DeleteFunc(subArgs, func(s string) bool {
				return strings.HasPrefix(s, "--depth")
			})
			_, err = checkoutGit.Run(ctx, subArgs...)
		}
		if err != nil {
			return fmt.Errorf("failed to update submodules: %w", err)
		}
	}

	if discardGitDir {
		if err := os.RemoveAll(checkoutDirGit); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove .git: %w", err)
		}
	}

	return nil
}

func MergeBase(ctx context.Context, ref1 *GitRef, ref2 *GitRef) (*gitutil.Ref, GitRefBackend, error) {
	if ref1.Backend.Repo().equivalent(ref2.Backend.Repo()) { // fast-path, just grab both refs from the same repo
		repo := ref1.Repo.Backend
		var mergeBase string
		err := repo.mount(ctx, 0, []GitRefBackend{ref1.Backend, ref2.Backend}, func(git *gitutil.GitCLI) error {
			out, err := git.Run(ctx, "merge-base", ref1.Ref.SHA, ref2.Ref.SHA)
			if err != nil {
				return fmt.Errorf("git merge-base failed: %w", err)
			}
			mergeBase = strings.TrimSpace(string(out))
			return nil
		})
		if err != nil {
			return nil, nil, err
		}

		ref := &gitutil.Ref{SHA: mergeBase, Name: mergeBase}
		backend, err := repo.Get(ctx, ref)
		if err != nil {
			return nil, nil, err
		}
		return ref, backend, nil
	}

	git, commits, cleanup, err := refJoin(ctx, []*GitRef{ref1, ref2})
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	out, err := git.Run(ctx, append([]string{"merge-base"}, commits...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("git merge-base failed: %w", err)
	}
	mergeBase := strings.TrimSpace(string(out))

	ref := &gitutil.Ref{SHA: mergeBase, Name: mergeBase}
	backend, err := ref1.Repo.Backend.Get(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	return ref, backend, nil
}

// refJoin creates a temporary git repository, adds the given refs as remotes,
// fetches them, and returns a GitCLI instance.
func refJoin(ctx context.Context, refs []*GitRef) (_ *gitutil.GitCLI, _ []string, _ func() error, rerr error) {
	tmpDir, err := os.MkdirTemp("", "dagger-mergebase")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	cleanup := func() error {
		return os.RemoveAll(tmpDir)
	}
	defer func() {
		if rerr != nil {
			cleanup()
		}
	}()
	git := gitutil.NewGitCLI(
		gitutil.WithDir(tmpDir),
		gitutil.WithGitDir(filepath.Join(tmpDir, ".git")),
	)
	if _, err := git.Run(ctx, "-c", "init.defaultBranch=main", "init"); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to init temp repo: %w", err)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	mu := sync.Mutex{} // cannot simultaneously add+fetch remotes
	commits := make([]string, len(refs))

	for i, ref := range refs {
		eg.Go(func() error {
			commits[i] = ref.Ref.SHA
			return ref.Backend.mount(egCtx, 0, func(gitN *gitutil.GitCLI) error {
				remoteURL, err := gitN.URL(egCtx)
				if err != nil {
					return err
				}
				remoteName := fmt.Sprintf("origin%d", i+1)
				mu.Lock()
				defer mu.Unlock()
				if _, err := git.Run(egCtx, "remote", "add", remoteName, remoteURL); err != nil {
					return fmt.Errorf("failed to add remote %s: %w", remoteName, err)
				}
				if _, err := git.Run(egCtx, "fetch", "--no-tags", remoteName, ref.Ref.SHA); err != nil {
					return fmt.Errorf("failed to fetch ref %d: %w", i+1, err)
				}
				return nil
			})
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, nil, nil, err
	}
	return git, commits, cleanup, nil
}
