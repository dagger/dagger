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

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
)

type GitRepository struct {
	Backend GitRepositoryBackend
	Remote  *gitutil.Remote

	DiscardGitDir bool

	// Internal per-repo pinned HEAD (ref/commit)
	PinnedHead *gitutil.Ref `internal:"true"`

	// Resolved indicates that lazy resolution has completed for this repo.
	// When true: URL scheme is explicit, auth is injected, remote metadata is initialized.
	Resolved bool `internal:"true"`
}

type GitRepositoryBackend interface {
	HasPBDefinitions

	// Remote returns information about the git remote.
	Remote(ctx context.Context) (*gitutil.Remote, error)
	// Get returns a reference to a specific git ref (branch, tag, or commit).
	Get(ctx context.Context, ref *gitutil.Ref) (GitRefBackend, error)

	// Dirty returns a Directory representing the repository in it's current state.
	Dirty(ctx context.Context) (dagql.ObjectResult[*Directory], error)
	// Cleaned returns a Directory representing the repository with all uncommitted changes discarded.
	Cleaned(ctx context.Context) (dagql.ObjectResult[*Directory], error)

	// mount mounts the repository with the provided refs and executes the given function.
	mount(ctx context.Context, depth int, refs []GitRefBackend, fn func(*gitutil.GitCLI) error) error
}

type GitRef struct {
	Repo    dagql.ObjectResult[*GitRepository]
	Backend GitRefBackend
	Ref     *gitutil.Ref

	// Resolved indicates that lazy resolution has completed for this ref.
	// When true: underlying repo is resolved, ref is canonicalized to full name + SHA, backend is initialized.
	Resolved bool `internal:"true"`
}

type GitRefBackend interface {
	HasPBDefinitions

	Tree(ctx context.Context, srv *dagql.Server, discard bool, depth int) (checkout *Directory, err error)

	mount(ctx context.Context, depth int, fn func(*gitutil.GitCLI) error) error
}

func NewGitRepository(ctx context.Context, backend GitRepositoryBackend) (*GitRepository, error) {
	return &GitRepository{
		Backend: backend,
	}, nil
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

// Resolve fetches remote metadata. Mutates in place: only call from `__resolve`.
func (repo *GitRepository) Resolve(ctx context.Context) error {
	if repo.Remote == nil {
		remote, err := repo.Backend.Remote(ctx)
		if err != nil {
			return err
		}
		repo.Remote = remote
	}
	return nil
}

func (repo *GitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return repo.Backend.PBDefinitions(ctx)
}

// Clone returns a shallow copy of the GitRepository.
// Use this before mutating to preserve dagql's immutability invariant.
func (repo *GitRepository) Clone() *GitRepository {
	cp := *repo
	if repo.PinnedHead != nil {
		pinnedHeadCopy := *repo.PinnedHead
		cp.PinnedHead = &pinnedHeadCopy
	}
	return &cp
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
	if ref.Backend == nil {
		return nil, nil
	}
	return ref.Backend.PBDefinitions(ctx)
}

// Clone returns a shallow copy of the GitRef.
// Use this before mutating to preserve dagql's immutability invariant.
func (ref *GitRef) Clone() *GitRef {
	cp := *ref
	if ref.Ref != nil {
		refCopy := *ref.Ref
		cp.Ref = &refCopy
	}
	return &cp
}

func (ref *GitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (*Directory, error) {
	return ref.Backend.Tree(ctx, srv, ref.Repo.Self().DiscardGitDir || discardGitDir, depth)
}

// Resolve canonicalizes SHA/name and initializes backend. Mutates in place: only call from `__resolve`.
func (ref *GitRef) Resolve(ctx context.Context) error {
	repo := ref.Repo.Self()

	if err := repo.Resolve(ctx); err != nil {
		return err
	}

	if ref.Ref.Name != "" {
		resolvedRefInfo, err := repo.Remote.Lookup(ref.Ref.Name)
		if err != nil {
			return err
		}
		if ref.Ref.SHA == "" {
			ref.Ref.SHA = resolvedRefInfo.SHA
		}
		if resolvedRefInfo.Name != "" {
			ref.Ref.Name = resolvedRefInfo.Name
		}
	}

	if ref.Backend == nil {
		refBackend, err := repo.Backend.Get(ctx, ref.Ref)
		if err != nil {
			return err
		}
		ref.Backend = refBackend
	}

	return nil
}

// doGitCheckout performs a git checkout using the given git helper.
//
// The provided git dir should *always* be empty.
func doGitCheckout(
	ctx context.Context,
	checkoutGit *gitutil.GitCLI,
	remoteURL string,
	cloneURL string,
	ref *gitutil.Ref,
	depth int,
	discardGitDir bool,
) error {
	checkoutDirGit, err := checkoutGit.GitDir(ctx)
	if err != nil {
		return fmt.Errorf("could not find git dir: %w", err)
	}

	_, err = checkoutGit.Run(ctx, "-c", "init.defaultBranch=main", "init")
	if err != nil {
		return err
	}

	tmpref := "refs/dagger.tmp/" + identity.NewID()

	// TODO: maybe this should use --no-tags by default, but that's a breaking change :(
	// also, we currently don't do any special work to ensure that the fetched
	// tags are consistent with the GitRepository.Remote (oops)
	args := []string{"fetch", "-u"}
	if depth > 0 {
		args = append(args, fmt.Sprintf("--depth=%d", depth))
	}
	args = append(args, cloneURL)
	args = append(args, ref.SHA+":"+tmpref)
	_, err = checkoutGit.Run(ctx, args...)
	if err != nil {
		return err
	}
	if ref.Name == "" {
		_, err = checkoutGit.Run(ctx, "checkout", ref.SHA)
		if err != nil {
			return fmt.Errorf("failed to checkout remote %s: %w", cloneURL, err)
		}
	} else {
		_, err = checkoutGit.Run(ctx, "update-ref", ref.Name, ref.SHA)
		if err != nil {
			return fmt.Errorf("failed to checkout remote %s: %w", cloneURL, err)
		}
		_, err = checkoutGit.Run(ctx, "checkout", strings.TrimPrefix(ref.Name, "refs/heads/"))
		if err != nil {
			return fmt.Errorf("failed to checkout remote %s: %w", cloneURL, err)
		}
		_, err = checkoutGit.Run(ctx, "reset", "--hard", ref.SHA)
		if err != nil {
			return fmt.Errorf("failed to reset ref: %w", err)
		}
	}
	if remoteURL != "" {
		_, err = checkoutGit.Run(ctx, "remote", "add", "origin", remoteURL)
		if err != nil {
			return fmt.Errorf("failed to set remote origin to %s: %w", remoteURL, err)
		}
	}
	_, err = checkoutGit.Run(ctx, "update-ref", "-d", tmpref)
	if err != nil {
		return fmt.Errorf("failed to delete tmp ref: %w", err)
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

func MergeBase(ctx context.Context, ref1 *GitRef, ref2 *GitRef) (*GitRef, error) {
	if ref1.Repo.ID() == ref2.Repo.ID() { // fast-path, just grab both refs from the same repo
		var mergeBase string
		err := ref1.Repo.Self().Backend.mount(ctx, 0, []GitRefBackend{ref1.Backend, ref2.Backend}, func(git *gitutil.GitCLI) error {
			out, err := git.Run(ctx, "merge-base", ref1.Ref.SHA, ref2.Ref.SHA)
			if err != nil {
				return fmt.Errorf("git merge-base failed: %w", err)
			}
			mergeBase = strings.TrimSpace(string(out))
			return nil
		})
		if err != nil {
			return nil, err
		}

		ref := &gitutil.Ref{SHA: mergeBase}
		backend, err := ref1.Repo.Self().Backend.Get(ctx, ref)
		if err != nil {
			return nil, err
		}
		return &GitRef{Repo: ref1.Repo, Backend: backend, Ref: ref}, nil
	}

	git, commits, cleanup, err := refJoin(ctx, []*GitRef{ref1, ref2})
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out, err := git.Run(ctx, append([]string{"merge-base"}, commits...)...)
	if err != nil {
		return nil, fmt.Errorf("git merge-base failed: %w", err)
	}
	mergeBase := strings.TrimSpace(string(out))

	ref := &gitutil.Ref{SHA: mergeBase}
	backend, err := ref1.Repo.Self().Backend.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &GitRef{Repo: ref1.Repo, Backend: backend, Ref: ref}, nil
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
