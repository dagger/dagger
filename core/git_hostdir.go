package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
// token instead (an unsynced workspace uses a fixed session-local token). The token only selects a cache entry; the pack itself is
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

	const maxStateAttempts = 3
	var gitDir dagql.ObjectResult[*Directory]
	for attempt := 0; attempt < maxStateAttempts; attempt++ {
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
		// pinned it to a stable token of its own. Only a live digest is also an
		// expected pack state; a session-pinned workspace accepts whichever
		// stable state is first materialized in its cache slot.
		stateDigest := state
		validateState := true
		if cacheKey != "" {
			stateDigest = cacheKey
			validateState = false
		}

		err = dag.Select(ctx, dag.Root(), &gitDir,
			dagql.Selector{
				Field: "host",
			},
			dagql.Selector{
				Field: "__gitDir",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(hostPath)},
					{Name: "stateDigest", Value: dagql.String(stateDigest)},
					{Name: "validateState", Value: dagql.Boolean(validateState)},
				},
			},
		)
		if err == nil {
			break
		}
		if !errors.Is(err, engineutil.ErrGitCheckoutStateChanged) || attempt == maxStateAttempts-1 {
			return tree, fmt.Errorf("materialize git dir for %q: %w", hostPath, err)
		}
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
// originURL, when nonempty, is recorded as the reconstruction's origin remote
// so remote-aware tooling reading the result (gh, git fetch) can still resolve
// which repository the checkout came from; the raw host config is otherwise
// never copied.
//
// A pack with no HeadSHA (a repository with no commits yet) reconstructs an
// empty repository on the same unborn branch.
func MaterializeGitCheckoutPack(ctx context.Context, pack *engineutil.GitCheckoutPack, originURL string) (_ *Directory, rerr error) {
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
		return reconstructGitDir(ctx, root, pack, originURL)
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
	dir.SetPath("/.git")
	dir.SetSnapshot(snap)
	return dir, nil
}

// MaterializeWorkspaceSnapshotPack fetches a client-produced synthetic commit
// through negotiated upload-pack (or the compatibility bundle) and checks its
// tree out into an immutable engine snapshot. The temporary repository is
// removed before the snapshot is committed.
func MaterializeWorkspaceSnapshotPack(ctx context.Context, pack *engineutil.WorkspaceSnapshotPack, remoteRepo *RemoteGitRepository) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bkref, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("git workspace snapshot"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = MountRef(ctx, bkref, func(root string, _ *mount.Mount) error {
		initArgs := []string{"init", "-q", "--initial-branch=main"}
		if pack.ObjectFormat != "" && pack.ObjectFormat != "sha1" {
			initArgs = append(initArgs, "--object-format="+pack.ObjectFormat)
		}
		if _, err := runGitEnv(ctx, root, initArgs...); err != nil {
			return fmt.Errorf("initialize workspace snapshot repository: %w", err)
		}
		if pack.BaseSHA != "" {
			if remoteRepo == nil {
				return fmt.Errorf("workspace snapshot base %s has no remote repository", pack.BaseSHA)
			}
			base, err := remoteRepo.Get(ctx, &gitutil.Ref{Name: pack.BaseRef, SHA: pack.BaseSHA})
			if err != nil {
				return fmt.Errorf("fetch workspace snapshot base %s: %w", pack.BaseSHA, err)
			}
			if err := remoteRepo.mount(ctx, 1, false, []GitRefBackend{base}, func(source *gitutil.GitCLI) error {
				sourceURL, err := source.URL(ctx)
				if err != nil {
					return err
				}
				_, err = runGitEnv(ctx, root, "fetch", "--quiet", "--depth=1", "--no-tags", sourceURL, pack.BaseSHA+":refs/dagger/workspace-base")
				return err
			}); err != nil {
				return fmt.Errorf("materialize workspace snapshot base %s: %w", pack.BaseSHA, err)
			}
		}
		if pack.UploadPack != nil {
			if err := fetchWorkspaceUploadPack(ctx, root, pack); err != nil {
				return fmt.Errorf("fetch workspace snapshot from client: %w", err)
			}
		} else if err := fetchGitBundleRefspecs(ctx, root, pack.BundlePath, []string{"+refs/dagger/workspace:refs/dagger/workspace"}); err != nil {
			return fmt.Errorf("fetch workspace snapshot bundle: %w", err)
		}
		resolved, err := runGitEnv(ctx, root, "rev-parse", "refs/dagger/workspace^{commit}")
		if err != nil {
			return fmt.Errorf("resolve workspace snapshot commit: %w", err)
		}
		if strings.TrimSpace(resolved) != pack.CommitSHA {
			return fmt.Errorf("workspace snapshot resolved to %s, expected %s", strings.TrimSpace(resolved), pack.CommitSHA)
		}
		if _, err := runGitEnv(ctx, root, "read-tree", pack.CommitSHA); err != nil {
			return fmt.Errorf("read workspace snapshot tree: %w", err)
		}
		if _, err := runGitEnv(ctx, root, "checkout-index", "-a", "-f"); err != nil {
			return fmt.Errorf("check out workspace snapshot tree: %w", err)
		}
		if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
			return fmt.Errorf("remove workspace snapshot repository: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
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

// fetchWorkspaceUploadPack exposes one loopback git:// connection and bridges
// it to the client's upload-pack stream. Native git performs the negotiation
// and writes the received pack directly into the materialization repository.
func fetchWorkspaceUploadPack(ctx context.Context, root string, pack *engineutil.WorkspaceSnapshotPack) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for workspace upload-pack: %w", err)
	}
	defer listener.Close()

	proxyDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			proxyDone <- err
			return
		}
		defer conn.Close()

		// git:// begins with a daemon service request. The client-side process
		// is already an upload-pack for the selected checkout, so consume that
		// routing packet before transparently proxying the protocol.
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil {
			proxyDone <- fmt.Errorf("read git daemon request header: %w", err)
			return
		}
		size, err := strconv.ParseUint(string(header), 16, 16)
		if err != nil || size < 4 || size > 64*1024 {
			proxyDone <- fmt.Errorf("invalid git daemon request length %q", header)
			return
		}
		request := make([]byte, int(size)-4)
		if _, err := io.ReadFull(conn, request); err != nil {
			proxyDone <- fmt.Errorf("read git daemon request: %w", err)
			return
		}
		if !strings.HasPrefix(string(request), "git-upload-pack /workspace\x00") {
			proxyDone <- fmt.Errorf("unexpected git daemon service request")
			return
		}

		errCh := make(chan error, 2)
		go func() {
			_, err := io.Copy(pack.UploadPack, conn)
			_ = pack.UploadPack.Close()
			errCh <- err
		}()
		go func() {
			_, err := io.Copy(conn, pack.UploadPack)
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			}
			errCh <- err
		}()
		first := <-errCh
		second := <-errCh
		proxyDone <- errors.Join(first, second)
	}()

	addr := listener.Addr().(*net.TCPAddr)
	remote := fmt.Sprintf("git://127.0.0.1:%d/workspace", addr.Port)
	if _, err := runGitEnv(ctx, root, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", remote, "+refs/dagger/workspace:refs/dagger/workspace"); err != nil {
		_ = pack.UploadPack.Close()
		return err
	}
	select {
	case err := <-proxyDone:
		return err
	case <-ctx.Done():
		_ = pack.UploadPack.Close()
		return context.Cause(ctx)
	}
}

// MaterializeGitUncommittedPack applies a client-produced binary patch of
// uncommitted changes (tracked modifications plus untracked files) to a
// canonical HEAD checkout and recreates lightweight markers for omitted
// untracked nested repositories. The result keeps HEAD's canonical .git and a
// dirty working tree suitable for LocalGitRepository.uncommitted.
func MaterializeGitUncommittedPack(ctx context.Context, tree dagql.ObjectResult[*Directory], pack *engineutil.GitUncommittedPack) (inst dagql.ObjectResult[*Directory], rerr error) {
	srv := dagql.CurrentDagqlServer(ctx)
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	parent, err := tree.Self().Snapshot.GetOrEval(ctx, tree.Result)
	if err != nil {
		return inst, fmt.Errorf("get canonical checkout snapshot: %w", err)
	}
	treePath, err := tree.Self().Dir.GetOrEval(ctx, tree.Result)
	if err != nil {
		return inst, fmt.Errorf("get canonical checkout path: %w", err)
	}

	bkref, err := query.SnapshotManager().New(ctx, parent,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("git uncommitted changes"))
	if err != nil {
		return inst, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = MountRef(ctx, bkref, func(root string, _ *mount.Mount) error {
		checkoutDir, err := fs.RootPath(root, treePath)
		if err != nil {
			return err
		}
		head, err := runGitEnv(ctx, checkoutDir, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("read canonical checkout HEAD: %w", err)
		}
		if strings.TrimSpace(head) != pack.HeadSHA {
			return fmt.Errorf("canonical checkout HEAD %s does not match uncommitted patch %s", strings.TrimSpace(head), pack.HeadSHA)
		}

		if pack.PatchPath != "" {
			if _, err := runGitEnv(ctx, checkoutDir, "apply", "--binary", "--whitespace=nowarn", pack.PatchPath); err != nil {
				return fmt.Errorf("apply uncommitted patch: %w", err)
			}
		}

		for _, nested := range pack.NestedRepositories {
			clean := filepath.Clean(filepath.FromSlash(nested))
			if clean == "." || clean == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("invalid nested repository path %q", nested)
			}
			nestedRoot, err := fs.RootPath(checkoutDir, clean)
			if err != nil {
				return fmt.Errorf("resolve nested repository path %q: %w", nested, err)
			}
			if err := os.MkdirAll(nestedRoot, 0o755); err != nil {
				return fmt.Errorf("create nested repository boundary %q: %w", nested, err)
			}
			if _, err := runGitEnv(ctx, nestedRoot, "init", "-q"); err != nil {
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
	dir.SetPath(treePath)
	dir.SetSnapshot(snap)
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		_ = dir.OnRelease(context.WithoutCancel(ctx))
		return inst, err
	}
	return inst, nil
}

func reconstructGitDir(ctx context.Context, root string, pack *engineutil.GitCheckoutPack, originURL string) error {
	initArgs := []string{"init", "-q", "--initial-branch=main"}
	if pack.ObjectFormat != "" && pack.ObjectFormat != "sha1" {
		initArgs = append(initArgs, "--object-format="+pack.ObjectFormat)
	}
	if _, err := runGitEnv(ctx, root, initArgs...); err != nil {
		return err
	}

	// Point HEAD where the checkout's HEAD points. For an unborn branch this
	// is all there is to reconstruct.
	if pack.HeadRef != "" {
		if _, err := runGitEnv(ctx, root, "symbolic-ref", "HEAD", pack.HeadRef); err != nil {
			return err
		}
	}

	if pack.HeadSHA != "" {
		if pack.BundlePath == "" {
			return fmt.Errorf("checkout pack for %s is missing its bundle file", pack.HeadSHA)
		}

		// The bundle carries every branch and tag; fetching validates object
		// connectivity along the way, so a torn or truncated pack fails here
		// rather than surfacing later as a subtly broken repository.
		// --update-head-ok (from the shared helper) lets the fetch advance the
		// branch HEAD symbolically points at (set just above): this is a
		// scratch reconstruction with no meaningful work tree, so git's
		// "refusing to fetch into checked-out branch" guard does not apply.
		// HEAD is also advertised by PackCheckout and can be the only ref in
		// a detached checkout, such as a materialized Workspace.git.directory.
		// Fetch it explicitly so its objects exist before restoring HEAD below.
		if err := fetchGitBundleRefspecs(ctx, root, pack.BundlePath, []string{"+refs/*:refs/*", "HEAD"}); err != nil {
			return fmt.Errorf("fetch checkout pack: %w", err)
		}

		switch {
		case pack.HeadRef != "":
			// The symbolic HEAD normally lands with the fetched branches; a
			// HEAD outside refs/heads (which --branches does not pack) is
			// created at the packed HEAD commit.
			if _, err := runGitEnv(ctx, root, "rev-parse", "-q", "--verify", pack.HeadRef); err != nil {
				if _, err := runGitEnv(ctx, root, "update-ref", pack.HeadRef, pack.HeadSHA); err != nil {
					return err
				}
			}
		default:
			// Detached HEAD.
			if _, err := runGitEnv(ctx, root, "update-ref", "--no-deref", "HEAD", pack.HeadSHA); err != nil {
				return err
			}
		}

		// The index is derived state; rebuild it from HEAD with stat data
		// zeroed so identical ref states reconstruct identical bytes.
		if _, err := runGitEnv(ctx, root, "read-tree", "HEAD"); err != nil {
			return err
		}
		if _, err := runGitEnv(ctx, root, "pack-refs", "--all"); err != nil {
			return err
		}
	}

	// Record where the checkout's repository was loaded from, so consumers
	// mounting the reconstruction (Workspace.git.directory) keep working with
	// remote-aware tooling like gh, which resolves the repository from the
	// origin remote.
	if originURL != "" {
		if _, err := runGitEnv(ctx, root, "remote", "add", "origin", originURL); err != nil {
			return fmt.Errorf("set origin remote: %w", err)
		}
	}

	// Strip state that is mutable, host-specific, or scratch: none of it is
	// part of "the repository at this ref state", and keeping it would make
	// otherwise identical reconstructions diverge. Bundle imports normalize
	// their canonical repositories with the same helper.
	return normalizeCanonicalGitDir(filepath.Join(root, ".git"))
}

// DropRootGitPointerFile returns dir without a `.git` regular file at its
// root, leaving a `.git` directory (or its absence) untouched.
//
// Checkouts created by `git worktree` and `git submodule` have a .git pointer
// file whose gitdir target lives on the client host; inside the engine that
// pointer is dangling by construction, and its only effect is to break git's
// repository discovery for anything that runs near the synced tree. Module
// contexts and workspaces represent a checkout's work tree -- git-ness is
// provided canonically via MaterializeHostGitCheckout -- so the raw pointer
// is dropped at load time to prevent discovery failures in the engine.
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
