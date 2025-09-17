package core

import (
	"bufio"
	"bytes"
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
	URL     dagql.Nullable[dagql.String] `field:"true" doc:"The URL of the git repository."`
	Backend GitRepositoryBackend

	DiscardGitDir bool
}

type GitRepositoryBackend interface {
	HasPBDefinitions

	// Ref returns a reference to a specific git ref (branch, tag, or commit).
	Ref(ctx context.Context, ref string) (GitRefBackend, error)
	// Tags lists tags in the repository matching the given patterns.
	Tags(ctx context.Context, patterns []string, sort string) (tags []string, err error)
	// Branches lists branches in the repository matching the given patterns.
	Branches(ctx context.Context, patterns []string, sort string) (branches []string, err error)

	mount(ctx context.Context, depth int, refs []GitRefBackend, fn func(*gitutil.GitCLI) error) error

	equivalent(GitRepositoryBackend) bool
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
	ref, err := repo.Backend.Ref(ctx, name)
	if err != nil {
		return nil, err
	}
	return &GitRef{repo, ref}, nil
}

func (repo *GitRepository) Tags(ctx context.Context, patterns []string, sort string) ([]string, error) {
	return repo.Backend.Tags(ctx, patterns, sort)
}

func (repo *GitRepository) Branches(ctx context.Context, patterns []string, sort string) ([]string, error) {
	return repo.Backend.Branches(ctx, patterns, sort)
}

type GitRef struct {
	Repo    *GitRepository
	Backend GitRefBackend
}

type GitRefBackend interface {
	HasPBDefinitions

	Repo() GitRepositoryBackend

	Resolve(ctx context.Context) (commit string, ref string, err error)
	Tree(ctx context.Context, srv *dagql.Server, discard bool, depth int) (checkout *Directory, err error)

	mount(ctx context.Context, depth int, fn func(*gitutil.GitCLI) error) error
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

func (ref *GitRef) Resolve(ctx context.Context) (string, string, error) {
	return ref.Backend.Resolve(ctx)
}

func (ref *GitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (*Directory, error) {
	return ref.Backend.Tree(ctx, srv, ref.Repo.DiscardGitDir || discardGitDir, depth)
}

// doGitCheckout performs a git checkout using the given git helper.
//
// The provided git dir should *always* be empty.
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

	_, err = checkoutGit.Run(ctx, "-c", "init.defaultBranch=main", "init")
	if err != nil {
		return err
	}

	destref := fullref
	if gitutil.IsCommitSHA(fullref) {
		// we need to create a temporary ref to fetch into!
		// this ensures that we actually get the "default" tags behavior (only
		// fetching tags that are part of the history)
		destref = "refs/dagger.tmp/" + identity.NewID()
	}

	args := []string{"fetch", "-u"}
	if depth > 0 {
		args = append(args, fmt.Sprintf("--depth=%d", depth))
	}
	args = append(args, cloneURL, fullref+":"+destref)
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
	if strings.HasPrefix(destref, "refs/dagger.tmp/") {
		_, err = checkoutGit.Run(ctx, "update-ref", "-d", destref)
		if err != nil {
			return fmt.Errorf("failed to delete tmp ref: %w", err)
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

func MergeBase(ctx context.Context, ref1 GitRefBackend, ref2 GitRefBackend) (GitRefBackend, error) {
	if ref1.Repo().equivalent(ref2.Repo()) { // fast-path, just grab both refs from the same repo
		repo := ref1.Repo()
		commit1, _, err := ref1.Resolve(ctx)
		if err != nil {
			return nil, err
		}
		commit2, _, err := ref2.Resolve(ctx)
		if err != nil {
			return nil, err
		}
		var mergeBase string
		err = repo.mount(ctx, 0, []GitRefBackend{ref1, ref2}, func(git *gitutil.GitCLI) error {
			out, err := git.Run(ctx, "merge-base", commit1, commit2)
			if err != nil {
				return fmt.Errorf("git merge-base failed: %w", err)
			}
			mergeBase = strings.TrimSpace(string(out))
			return nil
		})
		if err != nil {
			return nil, err
		}

		return repo.Ref(ctx, mergeBase)
	}

	git, commits, cleanup, err := refJoin(ctx, []GitRefBackend{ref1, ref2})
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out, err := git.Run(ctx, append([]string{"merge-base"}, commits...)...)
	if err != nil {
		return nil, fmt.Errorf("git merge-base failed: %w", err)
	}
	mergeBase := strings.TrimSpace(string(out))
	return ref1.Repo().Ref(ctx, mergeBase)
}

// refJoin creates a temporary git repository, adds the given refs as remotes,
// fetches them, and returns a GitCLI instance.
func refJoin(ctx context.Context, refs []GitRefBackend) (_ *gitutil.GitCLI, _ []string, _ func() error, rerr error) {
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
			commit, _, err := ref.Resolve(egCtx)
			if err != nil {
				return err
			}
			commits[i] = commit
			return ref.mount(egCtx, 0, func(gitN *gitutil.GitCLI) error {
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
				if _, err := git.Run(egCtx, "fetch", "--no-tags", remoteName, commit); err != nil {
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

// run git-ls-remote on a git repo with a target remote
func runLsRemote(ctx context.Context, git *gitutil.GitCLI, remote string, args []string, patterns []string, sort string) ([]string, error) {
	queryArgs := []string{
		"ls-remote",
		"--refs", // we don't want to include ^{} entries for annotated tags
	}
	if sort != "" {
		queryArgs = append(queryArgs, "--sort="+sort)
	}
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, remote)
	if len(patterns) > 0 {
		queryArgs = append(queryArgs, "--")
		queryArgs = append(queryArgs, patterns...)
	}

	out, err := git.Run(ctx, queryArgs...)
	if err != nil {
		return nil, err
	}

	results := []string{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		results = append(results, fields[1])
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning git output: %w", err)
	}

	return results, nil
}

// parseLsRemote parses output from git-ls-remote to find the correctly
// matching ref and commit for a target
func parseLsRemote(target string, out string) (commit string, ref string, err error) {
	lines := strings.Split(out, "\n")

	symrefs := make(map[string]string)

	// simulate git-checkout semantics, and make sure to select exactly the right ref
	var (
		partialRef      = "refs/" + strings.TrimPrefix(target, "refs/")
		headRef         = "refs/heads/" + strings.TrimPrefix(target, "refs/heads/")
		tagRef          = "refs/tags/" + strings.TrimPrefix(target, "refs/tags/")
		annotatedTagRef = tagRef + "^{}"
	)
	type reference struct {
		sha string
		ref string
	}
	var match, headMatch, tagMatch *reference

	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		lineMatch := &reference{sha: fields[0], ref: fields[1]}

		if ref, ok := strings.CutPrefix(lineMatch.sha, "ref: "); ok {
			// this is a symref, record it for later
			symrefs[lineMatch.ref] = ref
			continue
		}

		switch lineMatch.ref {
		case headRef:
			headMatch = lineMatch
		case tagRef, annotatedTagRef:
			tagMatch = lineMatch
			tagMatch.ref = tagRef
		case partialRef:
			match = lineMatch
		case target:
			match = lineMatch
		}
	}
	// git-checkout prefers branches in case of ambiguity
	if match == nil {
		match = headMatch
	}
	if match == nil {
		match = tagMatch
	}
	if match == nil {
		return "", "", fmt.Errorf("repository does not contain ref %q, output: %q", target, out)
	}
	if !gitutil.IsCommitSHA(match.sha) {
		return "", "", fmt.Errorf("invalid commit sha %q for %q", match.sha, match.ref)
	}

	// resolve symrefs to get the right ref result
	if ref, ok := symrefs[match.ref]; ok {
		match.ref = ref
	}
	return match.sha, match.ref, nil
}
