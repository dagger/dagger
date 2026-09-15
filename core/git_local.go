package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

func (repo *LocalGitRepository) Get(ctx context.Context, ref *gitutil.Ref) (GitRefBackend, error) {
	return &LocalGitRef{
		Ref:  ref,
		repo: repo,
	}, nil
}

func (repo *LocalGitRepository) Remote(ctx context.Context) (*gitutil.Remote, error) {
	var remote *gitutil.Remote
	err := repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
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

// ResolveShortSHA expands an abbreviated commit SHA against the repository's
// own object database, which is fully available locally.
func (repo *LocalGitRepository) ResolveShortSHA(ctx context.Context, prefix string) (string, error) {
	var sha string
	err := repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
		var err error
		sha, err = git.ResolveShortSHA(ctx, prefix)
		return err
	})
	if err != nil {
		return "", err
	}
	return sha, nil
}

func (repo *LocalGitRepository) File(ctx context.Context, filename string) (*File, error) {
	var gitDir string
	err := repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
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

	return repo.Directory.Self().Subfile(ctx, repo.Directory, filepath.Join(gitDir, filename))
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
	cache := query.SnapshotManager()

	parent, err := repo.Directory.Self().Snapshot.GetOrEval(ctx, repo.Directory.Result)
	if err != nil {
		return inst, fmt.Errorf("get git directory snapshot: %w", err)
	}
	repoDirPath, err := repo.Directory.Self().Dir.GetOrEval(ctx, repo.Directory.Result)
	if err != nil {
		return inst, fmt.Errorf("get git directory path: %w", err)
	}

	bkref, err := cache.New(ctx, parent,
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
	err = MountRef(ctx, bkref, func(parentRoot string, _ *mount.Mount) error {
		src, err := fs.RootPath(parentRoot, repoDirPath)
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

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return inst, err
	}
	bkref = nil
	dir := &Directory{
		Platform: query.Platform(),
		Services: slices.Clone(repo.Directory.Self().Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue(repoDirPath)
	dir.Snapshot.setValue(snap)

	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		_ = dir.OnRelease(context.WithoutCancel(ctx))
		return inst, err
	}
	return inst, nil
}

func (repo *LocalGitRepository) mount(ctx context.Context, depth int, includeTags bool, refs []GitRefBackend, fn func(*gitutil.GitCLI) error) error {
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

	ref, err := repo.Directory.Self().Snapshot.GetOrEval(ctx, repo.Directory.Result)
	if err != nil {
		return err
	}
	repoDirPath, err := repo.Directory.Self().Dir.GetOrEval(ctx, repo.Directory.Result)
	if err != nil {
		return err
	}

	return MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		src, err := fs.RootPath(root, repoDirPath)
		if err != nil {
			return err
		}

		git := gitutil.NewGitCLI(gitutil.WithDir(src))
		return fn(git)
	}, mountRefAsReadOnly)
}

func (ref *LocalGitRef) mount(ctx context.Context, depth int, includeTags bool, fn func(*gitutil.GitCLI) error) error {
	return ref.repo.mount(ctx, depth, includeTags, []GitRefBackend{ref}, fn)
}

// readGitConfigRemotes reads the remotes configured on the repository the
// CLI is positioned in: every remote.<name>.url and remote.<name>.pushurl,
// in configuration order. A repository with no remotes (or no readable
// config) reports none.
func readGitConfigRemotes(ctx context.Context, git *gitutil.GitCLI) ([]GitRemote, error) {
	out, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "config", "-z", "--get-regexp", `^remote\.`)
	if err != nil {
		return nil, err
	}
	byName := map[string]*GitRemote{}
	var order []string
	for _, entry := range strings.Split(string(out), "\x00") {
		key, value, ok := strings.Cut(entry, "\n")
		if !ok {
			continue
		}
		rest, isRemote := strings.CutPrefix(key, "remote.")
		if !isRemote {
			continue
		}
		// Suffix-first parsing keeps remote names containing dots intact.
		var name, attr string
		if n, isURL := strings.CutSuffix(rest, ".url"); isURL {
			name, attr = n, "url"
		} else if n, isPush := strings.CutSuffix(rest, ".pushurl"); isPush {
			name, attr = n, "pushurl"
		} else {
			continue
		}
		if name == "" || value == "" {
			continue
		}
		remote, ok := byName[name]
		if !ok {
			remote = &GitRemote{Name: name}
			byName[name] = remote
			order = append(order, name)
		}
		switch attr {
		case "url":
			// Later values shadow earlier ones, like `git config --get`.
			remote.URL = value
		case "pushurl":
			remote.PushURLs = append(remote.PushURLs, value)
		}
	}
	remotes := make([]GitRemote, 0, len(order))
	for _, name := range order {
		remotes = append(remotes, *byName[name])
	}
	return remotes, nil
}

func (ref *LocalGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool, remotes []GitRemote) (_ *Directory, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "materialize local git checkout", telemetry.Internal(), trace.WithAttributes(
		attribute.Int("dagger.git.checkout.depth", depth),
		attribute.Bool("dagger.git.checkout.discard_git_dir", discardGitDir),
	))
	defer telemetry.EndWithCause(span, &rerr)
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	cache := query.SnapshotManager()

	bkref, err := cache.New(ctx, nil,
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

	err = ref.mount(ctx, depth, includeTags, func(git *gitutil.GitCLI) error {
		gitURL, err := git.URL(ctx)
		if err != nil {
			return fmt.Errorf("could not find git url: %w", err)
		}

		// The checkout is rebuilt from scratch, which would drop the source
		// repository's remotes. Carry its remote configuration over -- with
		// any remotes registered on the repository object overlaid -- so
		// remote-aware tooling (gh, git fetch) keeps resolving the repository
		// from the result; the checkout itself still fetches from the local
		// mount.
		configRemotes, err := readGitConfigRemotes(ctx, git)
		if err != nil {
			return fmt.Errorf("could not read remotes: %w", err)
		}
		checkoutRemotes := MergeGitRemotes(configRemotes, remotes)

		return MountRef(ctx, bkref, func(checkoutDir string, _ *mount.Mount) error {
			checkoutDirGit := filepath.Join(checkoutDir, ".git")
			if err := os.MkdirAll(checkoutDir, 0711); err != nil {
				return err
			}
			checkoutGit := git.New(
				gitutil.WithDir(checkoutDir),
				gitutil.WithWorkTree(checkoutDir),
				gitutil.WithGitDir(checkoutDirGit),
			)
			return doGitCheckout(ctx, checkoutGit, checkoutRemotes, gitURL, ref.Ref, depth, discardGitDir)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to checkout %s: %w", ref.Ref.Name, err)
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	dir := &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue("/")
	dir.Snapshot.setValue(snap)
	return dir, nil
}
