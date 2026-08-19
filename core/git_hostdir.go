package core

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/continuity/fs"
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
// cacheKey keys the reconstruction. Empty means "key it to the checkout's live
// ref state" (a fresh reconstruction whenever the refs move): the right choice
// for a module context, resolved fresh per load. A caller that wants the
// reconstruction pinned to a session's cached view of the checkout -- so a
// checkout that advances mid-session is not silently re-read -- passes a stable
// token instead (a workspace passes its read epoch, which bumps on
// export/reload). The token only selects a cache entry; the pack itself is
// always taken from the live checkout when a new entry is computed.
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
	cacheKey string,
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

	// The live ref-state digest keys the reconstruction unless the caller
	// pinned it to a stable token of its own.
	stateDigest := state
	if cacheKey != "" {
		stateDigest = cacheKey
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
				{Name: "stateDigest", Value: dagql.String(stateDigest)},
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
	case errors.Is(err, iofs.ErrNotExist):
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

// MaterializeGitWorktreePack applies a client-produced binary worktree patch
// to a canonical HEAD checkout and recreates lightweight markers for omitted
// untracked nested repositories. The result keeps HEAD's canonical .git and a
// dirty worktree suitable for LocalGitRepository.uncommitted.
func MaterializeGitWorktreePack(ctx context.Context, tree dagql.ObjectResult[*Directory], pack *engineutil.GitWorktreePack) (inst dagql.ObjectResult[*Directory], rerr error) {
	srv := dagql.CurrentDagqlServer(ctx)
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	parent, err := tree.Self().Snapshot.PeekOrEval(ctx, tree.Result)
	if err != nil {
		return inst, fmt.Errorf("get canonical checkout snapshot: %w", err)
	}
	treePath, err := tree.Self().Dir.PeekOrEval(ctx, tree.Result)
	if err != nil {
		return inst, fmt.Errorf("get canonical checkout path: %w", err)
	}

	bkref, err := query.SnapshotManager().New(ctx, parent,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("git packed worktree"))
	if err != nil {
		return inst, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = MountRef(ctx, bkref, func(root string, _ *mount.Mount) error {
		worktree, err := fs.RootPath(root, treePath)
		if err != nil {
			return err
		}
		head, err := runGitEnv(ctx, worktree, nil, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("read canonical checkout HEAD: %w", err)
		}
		if strings.TrimSpace(head) != pack.HeadSHA {
			return fmt.Errorf("canonical checkout HEAD %s does not match worktree patch %s", strings.TrimSpace(head), pack.HeadSHA)
		}

		if len(pack.Patch) > 0 {
			patch, err := os.CreateTemp("", "dagger-worktree-patch")
			if err != nil {
				return fmt.Errorf("create worktree patch file: %w", err)
			}
			patchPath := patch.Name()
			defer os.Remove(patchPath)
			if _, err := patch.Write(pack.Patch); err != nil {
				patch.Close()
				return fmt.Errorf("write worktree patch: %w", err)
			}
			if err := patch.Close(); err != nil {
				return err
			}
			if _, err := runGitEnv(ctx, worktree, nil, "apply", "--binary", "--whitespace=nowarn", patchPath); err != nil {
				return fmt.Errorf("apply worktree patch: %w", err)
			}
		}

		for _, nested := range pack.NestedRepositories {
			clean := filepath.Clean(filepath.FromSlash(nested))
			if clean == "." || clean == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("invalid nested repository path %q", nested)
			}
			nestedRoot, err := fs.RootPath(worktree, clean)
			if err != nil {
				return fmt.Errorf("resolve nested repository path %q: %w", nested, err)
			}
			if err := os.MkdirAll(nestedRoot, 0o755); err != nil {
				return fmt.Errorf("create nested repository boundary %q: %w", nested, err)
			}
			if _, err := runGitEnv(ctx, nestedRoot, nil, "init", "-q"); err != nil {
				return fmt.Errorf("create nested repository boundary %q: %w", nested, err)
			}
		}
		return nil
	})
	if err != nil {
		return inst, err
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return inst, err
	}
	bkref = nil
	dir := &Directory{
		Platform: query.Platform(),
		Services: slices.Clone(tree.Self().Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue(treePath)
	dir.Snapshot.setValue(snap)
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		_ = dir.OnRelease(context.WithoutCancel(ctx))
		return inst, err
	}
	return inst, nil
}

// MaterializeGitCheckpointBundle imports a prerequisite bundle into an exact
// remote-base checkout and moves its canonical repository to the checkpoint's
// logical local HEAD. The bundle is permitted to omit objects reachable from
// baseSHA, but nothing else: git bundle verify/fetch check prerequisite and
// connectivity before the snapshot is committed.
func MaterializeGitCheckpointBundle(
	ctx context.Context,
	base dagql.ObjectResult[*Directory],
	baseSHA, headSHA, objectFormat, bundleRef string,
	bundle []byte,
) (inst dagql.ObjectResult[*Directory], rerr error) {
	if baseSHA == "" || headSHA == "" {
		return inst, fmt.Errorf("workspace checkpoint bundle requires base and head SHAs")
	}
	if len(bundle) == 0 {
		return inst, fmt.Errorf("workspace checkpoint bundle is empty")
	}
	if bundleRef == "" {
		return inst, fmt.Errorf("workspace checkpoint bundle ref is required")
	}

	srv := dagql.CurrentDagqlServer(ctx)
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	parent, err := base.Self().Snapshot.PeekOrEval(ctx, base.Result)
	if err != nil {
		return inst, fmt.Errorf("get checkpoint base snapshot: %w", err)
	}
	basePath, err := base.Self().Dir.PeekOrEval(ctx, base.Result)
	if err != nil {
		return inst, fmt.Errorf("get checkpoint base path: %w", err)
	}
	bkref, err := query.SnapshotManager().New(ctx, parent,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("workspace checkpoint git bundle"))
	if err != nil {
		return inst, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = MountRef(ctx, bkref, func(root string, _ *mount.Mount) error {
		worktree, err := fs.RootPath(root, basePath)
		if err != nil {
			return err
		}
		actualBase, err := runGitEnv(ctx, worktree, nil, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return fmt.Errorf("read checkpoint base HEAD: %w", err)
		}
		if strings.TrimSpace(actualBase) != baseSHA {
			return fmt.Errorf("remote checkpoint base is %s, expected %s", strings.TrimSpace(actualBase), baseSHA)
		}
		actualFormat, err := runGitEnv(ctx, worktree, nil, "rev-parse", "--show-object-format")
		if err != nil {
			return fmt.Errorf("read checkpoint object format: %w", err)
		}
		if strings.TrimSpace(actualFormat) != objectFormat {
			return fmt.Errorf("remote checkpoint object format is %s, expected %s", strings.TrimSpace(actualFormat), objectFormat)
		}
		if bundleRef != "HEAD" {
			if _, err := runGitEnv(ctx, worktree, nil, "check-ref-format", bundleRef); err != nil {
				return fmt.Errorf("invalid checkpoint bundle ref %q: %w", bundleRef, err)
			}
		}

		bundleFile, err := os.CreateTemp("", "dagger-workspace-checkpoint-bundle")
		if err != nil {
			return fmt.Errorf("create checkpoint bundle file: %w", err)
		}
		bundlePath := bundleFile.Name()
		defer os.Remove(bundlePath)
		if _, err := bundleFile.Write(bundle); err != nil {
			bundleFile.Close()
			return fmt.Errorf("write checkpoint bundle: %w", err)
		}
		if err := bundleFile.Close(); err != nil {
			return err
		}
		if err := verifyGitBundleInRepo(ctx, worktree, bundlePath); err != nil {
			return fmt.Errorf("verify checkpoint bundle: %w", err)
		}
		heads, err := runGitEnv(ctx, worktree, nil, "bundle", "list-heads", bundlePath, bundleRef)
		if err != nil {
			return fmt.Errorf("list checkpoint bundle heads: %w", err)
		}
		fields := strings.Fields(strings.TrimSpace(heads))
		if len(fields) != 2 || fields[0] != headSHA || fields[1] != bundleRef {
			return fmt.Errorf("checkpoint bundle ref %s does not resolve to logical HEAD %s", bundleRef, headSHA)
		}

		const importedRef = "refs/dagger/checkpoint/imported"
		if err := fetchGitBundleRefspecs(ctx, worktree, bundlePath, []string{"+" + bundleRef + ":" + importedRef}); err != nil {
			return fmt.Errorf("import checkpoint bundle: %w", err)
		}
		defer runGitEnv(context.WithoutCancel(ctx), worktree, nil, "update-ref", "-d", importedRef) //nolint:errcheck
		if _, err := runGitEnv(ctx, worktree, nil, "merge-base", "--is-ancestor", baseSHA, headSHA); err != nil {
			return fmt.Errorf("checkpoint logical HEAD %s does not descend from base %s: %w", headSHA, baseSHA, err)
		}
		if _, err := runGitEnv(ctx, worktree, nil, "update-ref", "--no-deref", "HEAD", headSHA); err != nil {
			return fmt.Errorf("set checkpoint HEAD: %w", err)
		}
		if _, err := runGitEnv(ctx, worktree, nil, "reset", "--hard", "--quiet", headSHA); err != nil {
			return fmt.Errorf("check out checkpoint HEAD: %w", err)
		}
		if _, err := runGitEnv(ctx, worktree, nil, "update-ref", "-d", importedRef); err != nil {
			return fmt.Errorf("remove checkpoint import ref: %w", err)
		}
		if _, err := runGitEnv(ctx, worktree, nil, "pack-refs", "--all"); err != nil {
			return fmt.Errorf("normalize checkpoint refs: %w", err)
		}
		return normalizeCanonicalGitDir(filepath.Join(worktree, ".git"))
	})
	if err != nil {
		return inst, err
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return inst, err
	}
	bkref = nil
	dir := &Directory{
		Platform: query.Platform(),
		Services: slices.Clone(base.Self().Services),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue(basePath)
	dir.Snapshot.setValue(snap)
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		_ = dir.OnRelease(context.WithoutCancel(ctx))
		return inst, err
	}
	return inst, nil
}

// WorkspaceGitTreeHash computes a stable Git tree object for the effective
// worktree, including ordinary untracked files. It uses a temporary index, so
// the normalized checkpoint index remains exactly HEAD.
func WorkspaceGitTreeHash(ctx context.Context, tree dagql.ObjectResult[*Directory]) (string, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	parent, err := tree.Self().Snapshot.PeekOrEval(ctx, tree.Result)
	if err != nil {
		return "", err
	}
	treePath, err := tree.Self().Dir.PeekOrEval(ctx, tree.Result)
	if err != nil {
		return "", err
	}
	bkref, err := query.SnapshotManager().New(ctx, parent,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("workspace checkpoint tree hash"))
	if err != nil {
		return "", err
	}
	defer bkref.Release(context.WithoutCancel(ctx))

	var treeSHA string
	err = MountRef(ctx, bkref, func(root string, _ *mount.Mount) error {
		worktree, err := fs.RootPath(root, treePath)
		if err != nil {
			return err
		}
		index, err := os.CreateTemp("", "dagger-workspace-checkpoint-index")
		if err != nil {
			return err
		}
		indexPath := index.Name()
		if err := index.Close(); err != nil {
			return err
		}
		if err := os.Remove(indexPath); err != nil {
			return err
		}
		defer os.Remove(indexPath)
		env := []string{"GIT_INDEX_FILE=" + indexPath}
		if _, err := runGitEnv(ctx, worktree, env, "read-tree", "HEAD"); err != nil {
			return err
		}
		if _, err := runGitEnv(ctx, worktree, env, "add", "-A", "-f", "--", "."); err != nil {
			return err
		}
		out, err := runGitEnv(ctx, worktree, env, "write-tree")
		if err != nil {
			return err
		}
		treeSHA = strings.TrimSpace(out)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("compute checkpoint worktree tree: %w", err)
	}
	return treeSHA, nil
}

// ValidateWorkspaceGitCheckpointHistory proves that the imported graph is the
// linear captured local history described by the manifest and that its
// user-visible metadata and changed-path scopes were not forged independently
// of the bundle.
func ValidateWorkspaceGitCheckpointHistory(
	ctx context.Context,
	repo dagql.ObjectResult[*Directory],
	manifest *WorkspaceGitCheckpointManifest,
) error {
	if manifest == nil {
		return fmt.Errorf("workspace checkpoint manifest is required")
	}
	return (&LocalGitRepository{Directory: repo}).mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
		out, err := git.Run(ctx, "rev-list", "--reverse", manifest.BaseSHA+".."+manifest.HeadSHA)
		if err != nil {
			return fmt.Errorf("enumerate checkpoint commits: %w", err)
		}
		actualSHAs := strings.Fields(string(out))
		if len(actualSHAs) != len(manifest.Commits) {
			return fmt.Errorf("checkpoint history contains %d commits, manifest describes %d", len(actualSHAs), len(manifest.Commits))
		}
		expectedParent := manifest.BaseSHA
		for i, commit := range manifest.Commits {
			if commit.SHA == "" || commit.SHA != actualSHAs[i] {
				return fmt.Errorf("checkpoint commit %d is %s, expected %s", i, actualSHAs[i], commit.SHA)
			}
			raw, err := git.Run(ctx, "cat-file", "commit", commit.SHA)
			if err != nil {
				return fmt.Errorf("read checkpoint commit %d (%s): %w", i, commit.SHA, err)
			}
			meta, err := parseGitCommitMetadata(commit.SHA, string(raw))
			if err != nil {
				return fmt.Errorf("parse checkpoint commit %d (%s): %w", i, commit.SHA, err)
			}
			if len(meta.ParentSHAs) != 1 || meta.ParentSHAs[0] != expectedParent {
				return fmt.Errorf("checkpoint commit %d (%s) is not the next linear child of %s", i, commit.SHA, expectedParent)
			}
			if meta.Message != commit.Message || meta.AuthoredDate != commit.Date || meta.AuthorName != commit.AuthorName || meta.AuthorEmail != commit.AuthorEmail {
				return fmt.Errorf("checkpoint commit %d (%s) metadata does not match manifest", i, commit.SHA)
			}
			pathsOut, err := git.Run(ctx, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", expectedParent, commit.SHA)
			if err != nil {
				return fmt.Errorf("read checkpoint commit %d (%s) paths: %w", i, commit.SHA, err)
			}
			actualPaths := strings.Split(strings.TrimSuffix(string(pathsOut), "\x00"), "\x00")
			if len(actualPaths) == 1 && actualPaths[0] == "" {
				actualPaths = nil
			}
			slices.Sort(actualPaths)
			expectedPaths := slices.Clone(commit.Paths)
			for j, p := range expectedPaths {
				clean := filepath.ToSlash(filepath.Clean(p))
				if p == "" || filepath.IsAbs(p) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != p {
					return fmt.Errorf("checkpoint commit %d (%s) has invalid path %q", i, commit.SHA, p)
				}
				expectedPaths[j] = clean
			}
			slices.Sort(expectedPaths)
			if !slices.Equal(actualPaths, expectedPaths) {
				return fmt.Errorf("checkpoint commit %d (%s) paths do not match manifest", i, commit.SHA)
			}
			expectedParent = commit.SHA
		}
		if expectedParent != manifest.HeadSHA {
			return fmt.Errorf("checkpoint captured history ends at %s, expected logical HEAD %s", expectedParent, manifest.HeadSHA)
		}
		if len(manifest.Commits) == 0 && manifest.HeadSHA != manifest.BaseSHA {
			return fmt.Errorf("checkpoint with no captured local commits must have identical base and head")
		}
		return nil
	})
}

func normalizeCanonicalGitDir(gitDir string) error {
	for _, p := range []string{"logs", "hooks", "branches", "description", "FETCH_HEAD", "COMMIT_EDITMSG"} {
		if err := os.RemoveAll(filepath.Join(gitDir, p)); err != nil {
			return fmt.Errorf("normalize .git/%s: %w", p, err)
		}
	}
	return nil
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
		// --update-head-ok lets the fetch advance the branch HEAD symbolically
		// points at (set just above): this is a scratch reconstruction with no
		// meaningful work tree, so git's "refusing to fetch into checked-out
		// branch" guard does not apply.
		if _, err := runGitEnv(ctx, root, nil, "fetch", "--quiet", "--no-tags", "--update-head-ok", bundle.Name(), "+refs/*:refs/*"); err != nil {
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

	return normalizeCanonicalGitDir(filepath.Join(root, ".git"))
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
		if errors.Is(err, iofs.ErrNotExist) {
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
