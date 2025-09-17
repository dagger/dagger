package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/util/gitutil"
)

type LocalGitRepository struct {
	Directory dagql.ObjectResult[*Directory]
}

var _ GitRepositoryBackend = (*LocalGitRepository)(nil)

type LocalGitRef struct {
	repo *LocalGitRepository

	Ref string
}

var _ GitRefBackend = (*LocalGitRef)(nil)

func (repo *LocalGitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return repo.Directory.Self().PBDefinitions(ctx)
}

func (repo *LocalGitRepository) Ref(ctx context.Context, ref string) (GitRefBackend, error) {
	return &LocalGitRef{
		repo: repo,
		Ref:  ref,
	}, nil
}

func (repo *LocalGitRepository) Tags(ctx context.Context, patterns []string, sort string) ([]string, error) {
	tags, err := repo.lsRemote(ctx, []string{"--tags"}, patterns, sort)
	if err != nil {
		return nil, err
	}
	for i, tag := range tags {
		tags[i] = strings.TrimPrefix(tag, "refs/tags/")
	}
	return tags, nil
}

func (repo *LocalGitRepository) Branches(ctx context.Context, patterns []string, sort string) ([]string, error) {
	branches, err := repo.lsRemote(ctx, []string{"--heads"}, patterns, sort)
	if err != nil {
		return nil, err
	}
	for i, branch := range branches {
		branches[i] = strings.TrimPrefix(branch, "refs/heads/")
	}
	return branches, nil
}

func (repo *LocalGitRepository) equivalent(other GitRepositoryBackend) bool {
	localRepo, ok := other.(*LocalGitRepository)
	if !ok {
		return false
	}
	if repo.Directory.ID() != localRepo.Directory.ID() {
		return false
	}
	return true
}

func (repo *LocalGitRepository) lsRemote(ctx context.Context, args []string, patterns []string, sort string) ([]string, error) {
	results := []string{}
	err := repo.mount(ctx, 0, nil, func(git *gitutil.GitCLI) error {
		gitURL, err := git.URL(ctx)
		if err != nil {
			return err
		}
		results, err = runLsRemote(ctx, gitutil.NewGitCLI(), gitURL, args, patterns, sort)
		return err
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (repo *LocalGitRepository) mount(ctx context.Context, depth int, refs []GitRefBackend, fn func(*gitutil.GitCLI) error) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Directory.Self().Services)
	if err != nil {
		return err
	}
	defer detach()

	return mountLLB(ctx, repo.Directory.Self().LLB, func(root string) error {
		src, err := fs.RootPath(root, repo.Directory.Self().Dir)
		if err != nil {
			return err
		}

		git := gitutil.NewGitCLI(gitutil.WithDir(src))
		return fn(git)
	})
}

func (ref *LocalGitRef) mount(ctx context.Context, depth int, fn func(*gitutil.GitCLI) error) error {
	return ref.repo.mount(ctx, depth, []GitRefBackend{ref}, fn)
}

func (ref *LocalGitRef) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return ref.repo.PBDefinitions(ctx)
}

func (ref *LocalGitRef) Repo() GitRepositoryBackend {
	return ref.repo
}

func (ref *LocalGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	cache := query.BuildkitCache()

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	commit, fullref, err := ref.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	bkref, err := cache.New(ctx, nil, bkSessionGroup,
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("git local checkout (%s %s)", fullref, commit)))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = ref.mount(ctx, depth, func(git *gitutil.GitCLI) error {
		gitURL, err := git.URL(ctx)
		if err != nil {
			return fmt.Errorf("could not find git url: %w", err)
		}

		return MountRef(ctx, bkref, bkSessionGroup, func(checkoutDir string) error {
			checkoutDirGit := filepath.Join(checkoutDir, ".git")
			if err := os.MkdirAll(checkoutDir, 0711); err != nil {
				return err
			}
			checkoutGit := git.New(
				gitutil.WithDir(checkoutDir),
				gitutil.WithWorkTree(checkoutDir),
				gitutil.WithGitDir(checkoutDirGit),
			)

			return doGitCheckout(ctx, checkoutGit, "", gitURL, fullref, commit, depth, discardGitDir)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to checkout %s: %w", fullref, err)
	}

	dir := NewDirectory(nil, "/", query.Platform(), nil)
	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	dir.Result = snap
	return dir, nil
}

func (ref *LocalGitRef) Resolve(ctx context.Context) (string, string, error) {
	if gitutil.IsCommitSHA(ref.Ref) {
		return ref.Ref, ref.Ref, nil
	}

	var commit, fullref string
	err := ref.mount(ctx, 0, func(git *gitutil.GitCLI) error {
		target := ref.Ref
		if gitutil.IsCommitSHA(ref.Ref) {
			target = "HEAD"
		}

		gitURL, err := git.URL(ctx)
		if err != nil {
			return err
		}

		out, err := git.Run(ctx,
			"ls-remote",
			"--symref",
			gitURL,
			target,
			target+"^{}",
		)
		if err != nil {
			return err
		}

		if gitutil.IsCommitSHA(ref.Ref) {
			commit, fullref = ref.Ref, ref.Ref
			return nil
		}
		commit, fullref, err = parseLsRemote(ref.Ref, string(out))
		return err
	})
	if err != nil {
		return "", "", err
	}
	return commit, fullref, nil
}
