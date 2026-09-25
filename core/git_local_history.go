package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
)

func (repo *LocalGitRepository) validateHistorySource(ctx context.Context) error {
	if repo.HistorySource.Self() == nil {
		return nil
	}
	source := repo.HistorySource.Self()
	remote, ok := source.Backend.(*RemoteGitRef)
	if !ok || source.Repo.Self() == nil || source.Repo.Self().Backend != remote.repo || source.Ref == nil || len(source.Ref.SHA) != 40 || !IsFullGitSHA(source.Ref.SHA) {
		return fmt.Errorf("owned shallow history requires an exact remote SHA-1 source")
	}
	if repo.CheckoutBase != nil && repo.CheckoutBase.Parent.Self() != nil {
		expected := repo.CheckoutBase.Parent
		if parent, ok := expected.Self().Repo.Self().Backend.(*LocalGitRepository); ok {
			expected = parent.HistorySource
		}
		if expected.Self() == nil {
			return fmt.Errorf("owned shallow history source is not inherited from its parent")
		}
		want, err := expected.RecipeDigest(ctx)
		if err != nil {
			return err
		}
		got, err := repo.HistorySource.RecipeDigest(ctx)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("owned shallow history source has a different parent capability")
		}
	}
	return nil
}

func (repo *LocalGitRepository) nativeGitDir(ctx context.Context, root string) (string, error) {
	if err := repo.validateHistorySource(ctx); err != nil {
		return "", err
	}
	dir, err := nativeCommitGitDirWithShallow(ctx, root, repo.HistorySource.Self() != nil)
	if err != nil {
		return "", err
	}
	if repo.HistorySource.Self() != nil {
		if _, err := ownedShallowBoundary(dir, repo.HistorySource.Self().Ref.SHA); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func ownedShallowBoundary(gitDir, anchor string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(gitDir, "shallow"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, nil
	}
	if string(data) != anchor+"\n" {
		return false, fmt.Errorf("owned shallow history boundary does not match its source")
	}
	return true, nil
}

// Raw mounts intentionally do not call this. Complete-history consumers select
// one replayable, owned union of the authorized source closure and local work.
func (repo *LocalGitRepository) fullHistory(ctx context.Context) (*LocalGitRepository, error) {
	if repo.HistorySource.Self() == nil {
		return repo, nil
	}
	if err := repo.validateHistorySource(ctx); err != nil {
		return nil, err
	}
	shallow := false
	err := repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
		dir, err := repo.nativeGitDir(ctx, git.Dir())
		if err != nil {
			return err
		}
		shallow, err = ownedShallowBoundary(dir, repo.HistorySource.Self().Ref.SHA)
		return err
	})
	if err != nil || !shallow {
		return repo, err
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	// Runtime IDs can resolve to a content-equivalent representative before
	// dynamic inputs run. Carry the producer recipe for this owned storage;
	// hydration's structural-input key can then preserve its exact provenance.
	id, err := repo.Directory.RecipeID(ctx)
	if err != nil {
		return nil, err
	}
	var dir dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, repo.HistorySource, &dir, dagql.Selector{Field: "__hydrateRepository", Args: []dagql.NamedInput{{Name: "directory", Value: dagql.NewID[*Directory](id)}}}); err != nil {
		return nil, err
	}
	return &LocalGitRepository{Directory: dir}, nil
}

// HydrateGitRepository never changes the shallow input or the remote mirror.
// A successful complete pack proves the source closure is available before the
// boundary can be removed. Snapshot ancestry pins both object stores; only local
// metadata is published, and no alternate paths are persisted.
func HydrateGitRepository(ctx context.Context, source dagql.ObjectResult[*GitRef], local dagql.ObjectResult[*Directory]) (_ *Directory, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "git hydrate owned history", telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)
	repo := &LocalGitRepository{Directory: local, HistorySource: source}
	if err := repo.validateHistorySource(ctx); err != nil {
		return nil, err
	}
	err := repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error { _, err := repo.nativeGitDir(ctx, git.Dir()); return err })
	if err != nil {
		return nil, err
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var complete dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, source, &complete, dagql.Selector{Field: "__nativeCommitBase", Args: []dagql.NamedInput{{Name: "depth", Value: dagql.Int(0)}}}); err != nil {
		return nil, err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]bkcache.ImmutableRef, 2)
	for i, dir := range []dagql.ObjectResult[*Directory]{complete, local} {
		selector, err := dir.Self().Dir.GetOrEval(ctx, dir.Result)
		if err != nil {
			return nil, err
		}
		if filepath.Clean(selector) != "/" {
			return nil, fmt.Errorf("owned shallow history must be a root bare repository")
		}
		refs[i], err = dir.Self().Snapshot.GetOrEval(ctx, dir.Result)
		if err != nil {
			return nil, err
		}
	}
	merged, err := query.SnapshotManager().Merge(ctx, refs)
	if err != nil {
		return nil, err
	}
	child, err := query.SnapshotManager().New(ctx, merged)
	if err != nil {
		return nil, errors.Join(err, merged.Release(context.WithoutCancel(ctx)))
	}
	var result *Directory
	defer func() {
		if child != nil {
			rerr = errors.Join(rerr, child.Release(context.WithoutCancel(ctx)))
		}
		rerr = errors.Join(rerr, merged.Release(context.WithoutCancel(ctx)), ctx.Err())
		if rerr != nil && result != nil {
			rerr = errors.Join(rerr, result.OnRelease(context.WithoutCancel(ctx)))
		}
	}()
	if err := MountRef(ctx, child, func(root string, _ *mount.Mount) error {
		err := os.Remove(filepath.Join(root, "shallow"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}); err != nil {
		return nil, err
	}
	snap, err := child.Commit(ctx)
	if err != nil {
		return nil, err
	}
	child = nil
	result = &Directory{Platform: query.Platform(), Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
	result.SetPath("/")
	result.SetSnapshot(snap)
	return result, nil
}

// Private alternate views must carry shallow boundaries explicitly. Git never
// reads the alternate repository's shallow file on our behalf.
func copyGitShallowBoundary(sourceGitDir, destGitDir string) error {
	data, err := os.ReadFile(filepath.Join(sourceGitDir, "shallow"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return os.WriteFile(filepath.Join(destGitDir, "shallow"), data, 0600)
}

func (ref *LocalGitRef) mountHistory(ctx context.Context, depth int, includeTags bool, fn func(*gitutil.GitCLI) error) error {
	if ref.repo.HistorySource.Self() == nil {
		return ref.repo.mount(ctx, depth, includeTags, nil, fn)
	}
	need := depth <= 0
	if !need {
		err := ref.repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
			if _, err := ref.repo.nativeGitDir(ctx, git.Dir()); err != nil {
				return err
			}
			// Probe without swallowing real process errors or cancellation. An
			// absent historical object is a request to hydrate, not a false root.
			out, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "cat-file", "-t", ref.SHA)
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(out)) != "commit" {
				need = true
				return nil
			}
			out, err = git.Run(ctx, "rev-list", fmt.Sprintf("--max-count=%d", depth), ref.SHA)
			if err != nil {
				return err
			}
			need, err = gitLogReachesShallowBoundary(ctx, git, strings.Fields(string(out)), depth)
			return err
		})
		if err != nil {
			return err
		}
	}
	repo := ref.repo
	if need {
		var err error
		repo, err = repo.fullHistory(ctx)
		if err != nil {
			return err
		}
	}
	return repo.mount(ctx, depth, includeTags, nil, fn)
}
