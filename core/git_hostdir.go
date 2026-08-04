package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/gitutil"
)

// ErrNoGitContext reports that a checkout has no git repository at all: no
// .git entry at its root. Unlike a .git that exists but is unusable (which is
// a broken environment and fails the call), absence is a legitimate state --
// `dagger init` before `git init`, an exported source tree -- so contextual
// GitRepository/GitRef args degrade to null on it.
var ErrNoGitContext = errors.New("module context has no git checkout")

// The engine's view of a host checkout's repository is canonical, not copied:
// the client's own git packs the repository (CheckoutState + PackCheckout,
// engine/session/git) and the engine reconstructs a standalone .git from the
// pack. The engine never reads a host checkout's raw .git layout -- worktree
// and submodule pointer files, commondirs, separate git dirs, alternates and
// partial clones are all the client git's business -- and the result is
// byte-identical for a given ref state regardless of how the host checkout
// is laid out.

// MaterializeHostGitCheckout returns tree with a canonical .git directory for
// the client checkout at hostPath, replacing whatever .git the synced tree
// carries. ctx must carry the owning client's metadata (it routes both the
// session RPCs and the Host.__gitDir selection).
//
// A checkout that is not a git repository reports ErrNoGitContext with the
// tree unchanged. A client that cannot pack checkouts (predates the RPCs, or
// has no git binary) degrades to the tree as synced: a plain .git directory
// keeps working as before, anything else fails downstream with git's own
// diagnostics.
func MaterializeHostGitCheckout(
	ctx context.Context,
	dag *dagql.Server,
	tree dagql.ObjectResult[*Directory],
	hostPath string,
) (dagql.ObjectResult[*Directory], error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return tree, err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return tree, fmt.Errorf("buildkit: %w", err)
	}

	state, err := bk.GitCheckoutState(ctx, hostPath)
	switch {
	case errors.Is(err, gitutil.ErrGitNoRepo):
		return tree, ErrNoGitContext
	case errors.Is(err, engineutil.ErrGitPackUnsupported):
		return tree, nil
	case err != nil:
		return tree, fmt.Errorf("git checkout state for %q: %w", hostPath, err)
	}

	var gitDir dagql.ObjectResult[*Directory]
	if err := dag.Select(ctx, dag.Root(), &gitDir,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "__gitDir",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(hostPath)},
				{Name: "stateDigest", Value: dagql.String(state)},
			},
		},
	); err != nil {
		return tree, fmt.Errorf("materialize git dir for %q: %w", hostPath, err)
	}
	gitDirID, err := gitDir.ID()
	if err != nil {
		return tree, fmt.Errorf("git dir ID: %w", err)
	}

	// Replace whatever .git the synced tree carries (a plain checkout's raw
	// .git directory, or nothing) with the canonical one.
	sels := []dagql.Selector{}
	switch st, err := tree.Self().Stat(ctx, tree, dag, ".git", true); {
	case err == nil && st.FileType == FileTypeRegular:
		sels = append(sels, dagql.Selector{
			Field: "withoutFile",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(".git")}},
		})
	case err == nil:
		sels = append(sels, dagql.Selector{
			Field: "withoutDirectory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(".git")}},
		})
	case errors.Is(err, fs.ErrNotExist):
		// Nothing to replace.
	default:
		return tree, err
	}
	sels = append(sels, dagql.Selector{
		Field: "withDirectory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(".git")},
			{Name: "source", Value: dagql.NewID[*Directory](gitDirID)},
		},
	})

	var composed dagql.ObjectResult[*Directory]
	if err := dag.Select(ctx, tree, &composed, sels...); err != nil {
		return tree, fmt.Errorf("compose canonical .git: %w", err)
	}
	return composed, nil
}

// MaterializeGitCheckoutPack reconstructs a canonical git directory from a
// client checkout pack: init, fetch the bundle's refs, set HEAD (symbolic or
// detached), rebuild the index from HEAD, pack refs, and strip everything
// mutable or host-specific (reflogs, hooks, FETCH_HEAD). The returned
// Directory is the git directory itself -- HEAD, objects and refs at its
// root -- ready to be mounted at some tree's .git.
//
// A pack with no HeadSHA (a repository with no commits yet) reconstructs an
// empty repository on the same unborn branch.
func MaterializeGitCheckoutPack(ctx context.Context, pack *engineutil.GitCheckoutPack) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	cache := query.SnapshotManager()

	bkref, err := cache.New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("git checkout pack repository"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = MountRef(ctx, bkref, func(root string, _ *mount.Mount) error {
		return reconstructGitDir(ctx, root, pack)
	})
	if err != nil {
		return nil, fmt.Errorf("reconstruct git dir: %w", err)
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
	dir.Dir.setValue("/.git")
	dir.Snapshot.setValue(snap)
	return dir, nil
}

func reconstructGitDir(ctx context.Context, root string, pack *engineutil.GitCheckoutPack) error {
	initArgs := []string{"init", "-q", "--initial-branch=main"}
	if pack.ObjectFormat != "" && pack.ObjectFormat != "sha1" {
		initArgs = append(initArgs, "--object-format="+pack.ObjectFormat)
	}
	if _, err := runGitEnv(ctx, root, nil, initArgs...); err != nil {
		return err
	}

	// Point HEAD where the checkout's HEAD points. For an unborn branch this
	// is all there is to reconstruct.
	if pack.HeadRef != "" {
		if _, err := runGitEnv(ctx, root, nil, "symbolic-ref", "HEAD", pack.HeadRef); err != nil {
			return err
		}
	}

	if pack.HeadSHA != "" {
		bundle, err := os.CreateTemp("", "dagger-checkout-pack")
		if err != nil {
			return fmt.Errorf("create bundle temp file: %w", err)
		}
		defer os.Remove(bundle.Name())
		if _, err := bundle.Write(pack.Bundle); err != nil {
			bundle.Close()
			return fmt.Errorf("write bundle: %w", err)
		}
		if err := bundle.Close(); err != nil {
			return err
		}

		// The bundle carries every branch and tag; fetching validates object
		// connectivity along the way, so a torn or truncated pack fails here
		// rather than surfacing later as a subtly broken repository.
		if _, err := runGitEnv(ctx, root, nil, "fetch", "--quiet", "--no-tags", bundle.Name(), "+refs/*:refs/*"); err != nil {
			return fmt.Errorf("fetch checkout pack: %w", err)
		}

		switch {
		case pack.HeadRef != "":
			// The symbolic HEAD normally lands with the fetched branches; a
			// HEAD outside refs/heads (which --branches does not pack) is
			// created at the packed HEAD commit.
			if _, err := runGitEnv(ctx, root, nil, "rev-parse", "-q", "--verify", pack.HeadRef); err != nil {
				if _, err := runGitEnv(ctx, root, nil, "update-ref", pack.HeadRef, pack.HeadSHA); err != nil {
					return err
				}
			}
		default:
			// Detached HEAD.
			if _, err := runGitEnv(ctx, root, nil, "update-ref", "--no-deref", "HEAD", pack.HeadSHA); err != nil {
				return err
			}
		}

		// The index is derived state; rebuild it from HEAD with stat data
		// zeroed, exactly as staged-commit repositories are normalized.
		if _, err := runGitEnv(ctx, root, nil, "read-tree", "HEAD"); err != nil {
			return err
		}
		if _, err := runGitEnv(ctx, root, nil, "pack-refs", "--all"); err != nil {
			return err
		}
	}

	// Strip state that is mutable, host-specific, or scratch: none of it is
	// part of "the repository at this ref state", and keeping it would make
	// otherwise identical reconstructions diverge.
	gitDir := filepath.Join(root, ".git")
	for _, p := range []string{"logs", "hooks", "branches", "description", "FETCH_HEAD", "COMMIT_EDITMSG"} {
		if err := os.RemoveAll(filepath.Join(gitDir, p)); err != nil {
			return fmt.Errorf("normalize .git/%s: %w", p, err)
		}
	}
	return nil
}

// DropRootGitPointerFile returns dir without a `.git` regular file at its
// root, leaving a `.git` directory (or its absence) untouched.
//
// Checkouts created by `git worktree` and `git submodule` have a .git pointer
// file whose gitdir target lives on the client host; inside the engine that
// pointer is dangling by construction, and its only effect is to poison git's
// repository discovery for anything that runs near the synced tree. Module
// contexts and workspaces represent a checkout's work tree -- git-ness is
// provided canonically via MaterializeHostGitCheckout -- so the raw pointer
// is dropped at load time rather than shipped as a landmine.
func DropRootGitPointerFile(
	ctx context.Context,
	dag *dagql.Server,
	dir dagql.ObjectResult[*Directory],
) (dagql.ObjectResult[*Directory], error) {
	st, err := dir.Self().Stat(ctx, dir, dag, ".git", true)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return dir, nil
		}
		return dir, err
	}
	if st.FileType != FileTypeRegular {
		return dir, nil
	}
	var cleaned dagql.ObjectResult[*Directory]
	if err := dag.Select(ctx, dir, &cleaned, dagql.Selector{
		Field: "withoutFile",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(".git")}},
	}); err != nil {
		return dir, fmt.Errorf("drop .git pointer file: %w", err)
	}
	return cleaned, nil
}

var _ = slices.Clone[[]string] // placate imports during staged edits
var _ = strings.TrimSpace
