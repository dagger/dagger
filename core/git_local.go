package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/util/gitutil"
)

type LocalGitRepository struct {
	Directory dagql.ObjectResult[*Directory]
}

var _ GitRepositoryBackend = (*LocalGitRepository)(nil)

type LocalGitRef struct {
	*gitutil.Ref
	repo *LocalGitRepository
}

var _ GitRefBackend = (*LocalGitRef)(nil)

func (repo *LocalGitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return repo.Directory.Self().PBDefinitions(ctx)
}

func (repo *LocalGitRepository) Get(ctx context.Context, ref *gitutil.Ref) (GitRefBackend, error) {
	return &LocalGitRef{
		Ref:  ref,
		repo: repo,
	}, nil
}

func (repo *LocalGitRepository) Remote(ctx context.Context) (*gitutil.Remote, error) {
	var remote *gitutil.Remote
	err := repo.mount(ctx, 0, nil, func(git *gitutil.GitCLI) error {
		gitURL, err := git.URL(ctx)
		if err != nil {
			return err
		}
		remote, err = gitutil.NewGitCLI().LsRemote(ctx, gitURL)
		return err
	})
	if err != nil {
		return nil, err
	}
	return remote, nil
}

func (repo *LocalGitRepository) File(ctx context.Context, filename string) (*File, error) {
	var gitDir string
	err := repo.mount(ctx, 0, nil, func(git *gitutil.GitCLI) error {
		dir, err := git.GitDir(ctx)
		if err != nil {
			return err
		}
		if filepath.IsAbs(dir) {
			dir, err = filepath.Rel(dir, git.Dir())
			if err != nil {
				return err
			}
		}
		gitDir = dir
		return nil
	})
	if err != nil {
		return nil, err
	}

	return repo.Directory.Self().File(ctx, filepath.Join(gitDir, filename))
}

func (repo *LocalGitRepository) Dirty(ctx context.Context) (inst dagql.ObjectResult[*Directory], rerr error) {
	return repo.Directory, nil
}

func (repo *LocalGitRepository) Cleaned(ctx context.Context) (inst dagql.ObjectResult[*Directory], rerr error) {
	srv := dagql.CurrentDagqlServer(ctx)
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, err
	}
	cache := query.BuildkitCache()

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return inst, fmt.Errorf("no buildkit session group in context")
	}

	llb := repo.Directory.Self().LLB
	res, err := bk.Solve(ctx, bkgw.SolveRequest{Definition: llb})
	if err != nil {
		return inst, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return inst, err
	}
	parent, err := ref.CacheRef(ctx)
	if err != nil {
		return inst, err
	}

	bkref, err := cache.New(ctx, parent, bkSessionGroup,
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("git cleaned worktree"))

	if err != nil {
		return inst, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()
	skip := false
	err = MountRef(ctx, bkref, bkSessionGroup, func(parentRoot string) error {
		src, err := fs.RootPath(parentRoot, repo.Directory.Self().Dir)
		if err != nil {
			return err
		}

		git := gitutil.NewGitCLI(gitutil.WithDir(src))
		worktree, err := git.WorkTree(ctx)
		if err != nil {
			return err
		}
		if worktree == "" {
			skip = true // no worktree, no changes
			return nil
		}
		gitDir, err := git.GitDir(ctx)
		if err != nil {
			return err
		}

		idx, err := os.Open(filepath.Join(gitDir, "index"))
		if err != nil {
			return err
		}
		defer idx.Close()

		// NOTE: apply the index to a temp file because "git restore --staged"
		// re-writes the index which we don't want to show up as a changed file
		// in the final result
		tmp, err := os.CreateTemp("", "dagger-git-index-")
		if err != nil {
			return err
		}
		_, err = io.Copy(tmp, idx)
		if err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		defer os.Remove(tmp.Name())

		git = git.New(gitutil.WithIndexFile(tmp.Name()))

		// reset index to HEAD
		// NOTE: we cannot use "git reset --hard" because it writes every file,
		// which *kills* performance on overlayfs
		_, err = git.Run(ctx, "restore", "--staged", ".")
		if err != nil {
			return err
		}
		_, err = git.Run(ctx, "restore", ".")
		if err != nil {
			return err
		}
		_, err = git.Run(ctx, "clean", "-fd")
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return inst, err
	}
	if skip {
		return repo.Directory, nil
	}

	dir := NewDirectory(nil, repo.Directory.Self().Dir, query.Platform(), nil)
	snap, err := bkref.Commit(ctx)
	if err != nil {
		return inst, err
	}
	bkref = nil
	dir.Result = snap

	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
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

	bkref, err := cache.New(ctx, nil, bkSessionGroup,
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("git local checkout (%s %s)", ref.Ref.Name, ref.Ref.SHA)))
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
			return doGitCheckout(ctx, checkoutGit, "", gitURL, ref.Ref, depth, discardGitDir)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to checkout %s: %w", ref.Ref.Name, err)
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
