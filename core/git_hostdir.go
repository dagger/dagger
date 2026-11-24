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
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
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
// The reconstruction is keyed by the checkout's ref state and the client's
// host (see Host.__gitDir), so it is shared by every session that reads that
// state. cacheKey selects which state: empty means the checkout's live state,
// read fresh per call, the right choice for a module context, resolved fresh
// per load. A caller that wants a session's view of the checkout to stay put
// -- so a checkout that advances mid-session is not silently re-read -- passes
// a stable token instead (an unsynced workspace uses a fixed session-local
// token): the first call under that token pins the live state it reads.
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
	var (
		gitDir dagql.ObjectResult[*Directory]
		err    error
	)
	if cacheKey == "" {
		gitDir, _, err = SelectHostGitDir(ctx, dag, hostPath)
	} else {
		// The session's slot pins the state it first read; the
		// reconstruction itself is shared by every session that reads that
		// state of this client's checkout.
		var pinned dagql.String
		err = dag.Select(ctx, dag.Root(), &pinned,
			dagql.Selector{Field: "host"},
			dagql.Selector{
				Field: "__gitCheckoutPin",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(hostPath)},
					{Name: "token", Value: dagql.String(cacheKey)},
				},
			},
		)
		if err == nil {
			gitDir, err = selectHostGitDirAt(ctx, dag, hostPath, pinned.String())
		}
	}
	switch {
	case errors.Is(err, gitutil.ErrGitNoRepo):
		return tree, ErrNoGitContext
	case errors.Is(err, engineutil.ErrGitPackUnsupported):
		return tree, nil
	case err != nil:
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

// SelectHostGitDir selects the canonical .git directory for the live state of
// the client checkout at hostPath, and returns that state's digest. A checkout
// whose refs move while it is packed is read again.
func SelectHostGitDir(ctx context.Context, dag *dagql.Server, hostPath string) (dagql.ObjectResult[*Directory], string, error) {
	var gitDir dagql.ObjectResult[*Directory]
	query, err := CurrentQuery(ctx)
	if err != nil {
		return gitDir, "", err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return gitDir, "", fmt.Errorf("buildkit: %w", err)
	}
	const maxStateAttempts = 3
	for attempt := 0; ; attempt++ {
		state, err := bk.GitCheckoutState(ctx, hostPath)
		if err != nil {
			return gitDir, "", fmt.Errorf("git checkout state for %q: %w", hostPath, err)
		}
		gitDir, err = selectHostGitDirAt(ctx, dag, hostPath, state)
		if err == nil {
			return gitDir, state, nil
		}
		if !errors.Is(err, engineutil.ErrGitCheckoutStateChanged) || attempt == maxStateAttempts-1 {
			return gitDir, "", err
		}
	}
}

func selectHostGitDirAt(ctx context.Context, dag *dagql.Server, hostPath, stateDigest string) (dagql.ObjectResult[*Directory], error) {
	var gitDir dagql.ObjectResult[*Directory]
	err := dag.Select(ctx, dag.Root(), &gitDir,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "__gitDir",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(hostPath)},
				{Name: "stateDigest", Value: dagql.String(stateDigest)},
			},
		},
	)
	return gitDir, err
}

// hostGitCheckouts remembers, per engine, the ref state of the last
// reconstruction made for each client checkout, so the next state of that
// checkout can be built from it. It is a hint, not an owner: the
// reconstruction lives in the dagql cache and may have been pruned since,
// which costs one refused pack request.
var hostGitCheckouts sync.Map // hostGitCheckoutKey -> state digest

type hostGitCheckoutKey struct {
	manager bkcache.SnapshotManager
	owner   string
	path    string
}

// HostGitCheckoutOwner names whose checkout a client's host path is: the
// client's host when the client has a stable identity, so that every
// invocation from one host shares reconstructions of its checkouts, and
// otherwise the client alone. Clients started inside the engine get a fresh
// stable identity, so they never share the host's.
func HostGitCheckoutOwner(md *engine.ClientMetadata) string {
	if md.ClientStableID != "" {
		return "host:" + md.ClientStableID
	}
	return "client:" + md.ClientID
}

func hostGitCheckoutKeyFor(ctx context.Context, hostPath string) (hostGitCheckoutKey, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return hostGitCheckoutKey{}, err
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return hostGitCheckoutKey{}, err
	}
	return hostGitCheckoutKey{manager: query.SnapshotManager(), owner: HostGitCheckoutOwner(md), path: hostPath}, nil
}

// HostGitCheckoutBase returns the reconstruction of the checkout at hostPath
// last made on this engine, if it is for another state than stateDigest and
// still cached, with the objects its refs and HEAD point at. A checkout pack
// relative to those builds on it (see MaterializeGitCheckoutPack). The
// returned directory must stay held while its snapshot is used.
func HostGitCheckoutBase(ctx context.Context, dag *dagql.Server, hostPath, stateDigest string) (base dagql.ObjectResult[*Directory], snapshot bkcache.ImmutableRef, haves []string, _ error) {
	key, err := hostGitCheckoutKeyFor(ctx, hostPath)
	if err != nil {
		return base, nil, nil, err
	}
	last, ok := hostGitCheckouts.Load(key)
	if !ok || last.(string) == stateDigest {
		return base, nil, nil, nil
	}
	base, err = selectHostGitDirAt(ctx, dag, hostPath, last.(string))
	if err != nil {
		if ctx.Err() != nil {
			return base, nil, nil, ctx.Err()
		}
		// No longer cached, and the checkout has left that state, so the
		// client refused to pack it again: build from scratch.
		hostGitCheckouts.CompareAndDelete(key, last)
		return dagql.ObjectResult[*Directory]{}, nil, nil, nil
	}
	snapshot, err = base.Self().Snapshot.GetOrEval(ctx, base.Result)
	if err != nil {
		return base, nil, nil, fmt.Errorf("get git checkout base snapshot: %w", err)
	}
	err = MountRef(ctx, snapshot, func(root string, m *mount.Mount) error {
		if err := checkNativeSnapshotDepth(m); err != nil {
			return err
		}
		refs, err := runGitEnv(ctx, root, "for-each-ref", "--format=%(objectname)")
		if err != nil {
			return err
		}
		haves = strings.Fields(refs)
		if head, err := runGitEnv(ctx, root, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
			haves = append(haves, strings.TrimSpace(head))
		}
		return nil
	})
	if nativeCommitFallback(err) {
		// Each state of a checkout adds a layer; past the limit, start over.
		return dagql.ObjectResult[*Directory]{}, nil, nil, nil
	}
	if err != nil {
		return base, nil, nil, fmt.Errorf("read git checkout base refs: %w", err)
	}
	slices.Sort(haves)
	return base, snapshot, slices.Compact(haves), nil
}

// RecordHostGitCheckout notes that the checkout at hostPath was last
// reconstructed at stateDigest. See HostGitCheckoutBase.
func RecordHostGitCheckout(ctx context.Context, hostPath, stateDigest string) error {
	key, err := hostGitCheckoutKeyFor(ctx, hostPath)
	if err != nil {
		return err
	}
	hostGitCheckouts.Store(key, stateDigest)
	return nil
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
//
// A pack relative to the ref targets of an earlier reconstruction is applied
// to a copy-on-write child of base, that reconstruction's snapshot, so only
// what the checkout gained since is fetched.
func MaterializeGitCheckoutPack(ctx context.Context, pack *engineutil.GitCheckoutPack, originURL string, base bkcache.ImmutableRef) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	cache := query.SnapshotManager()

	reconstruct := reconstructGitDir
	if pack.RelativeToHaves {
		if base == nil {
			return nil, fmt.Errorf("checkout pack is relative to a reconstruction that was not given")
		}
		reconstruct = reconstructGitDirIncremental
	} else {
		base = nil
	}
	bkref, err := cache.New(ctx, base,
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
		return reconstruct(ctx, root, pack, originURL)
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
	if err := initCanonicalGitDir(ctx, root, pack); err != nil {
		return err
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
		if err := finishCanonicalGitHead(ctx, root, pack); err != nil {
			return err
		}
	}
	return finishCanonicalGitDir(ctx, root, pack, originURL)
}

// reconstructGitDirIncremental brings a copy of an earlier reconstruction of
// the same checkout to pack's state. The pack holds only the objects the
// earlier reconstruction lacks, and pack.Refs every branch and tag; refs are
// set to exactly those, and the configuration is written afresh.
func reconstructGitDirIncremental(ctx context.Context, root string, pack *engineutil.GitCheckoutPack, originURL string) error {
	if pack.HeadSHA == "" {
		return fmt.Errorf("relative checkout pack has no HEAD")
	}
	// The earlier state's remotes and selection live in the config.
	if err := os.Remove(filepath.Join(root, ".git", "config")); err != nil {
		return fmt.Errorf("reset reconstructed git config: %w", err)
	}
	if err := initCanonicalGitDir(ctx, root, pack); err != nil {
		return err
	}
	if pack.BundlePath != "" {
		// Unbundling checks that the bundle's prerequisites are present and
		// indexes its objects, without touching any ref.
		if _, err := runGitEnv(ctx, root, "bundle", "unbundle", pack.BundlePath); err != nil {
			return fmt.Errorf("unbundle checkout pack: %w", err)
		}
	}

	want := make(map[string]string, len(pack.Refs))
	for _, ref := range pack.Refs {
		want[ref.Name] = ref.Sha
	}
	have, err := runGitEnv(ctx, root, "for-each-ref", "--format=%(refname)")
	if err != nil {
		return err
	}
	// Deletions go first, in their own transaction: one transaction cannot
	// delete a ref and create another that its name contains or extends
	// (topic, topic/sub).
	var deletes, updates strings.Builder
	for _, name := range strings.Fields(have) {
		if _, ok := want[name]; !ok {
			deletes.WriteString("delete " + name + "\n")
		}
	}
	for _, ref := range pack.Refs {
		updates.WriteString("update " + ref.Name + " " + ref.Sha + "\n")
	}
	git := gitutil.NewGitCLI(gitutil.WithDir(root))
	if deletes.Len() > 0 {
		if _, err := git.RunWithStdin(ctx, strings.NewReader(deletes.String()), "update-ref", "--stdin"); err != nil {
			return fmt.Errorf("delete checkout refs: %w", err)
		}
	}
	// update-ref refuses a ref to an object the repository does not have.
	if _, err := git.RunWithStdin(ctx, strings.NewReader(updates.String()), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("set checkout refs: %w", err)
	}
	if err := finishCanonicalGitHead(ctx, root, pack); err != nil {
		return err
	}
	return finishCanonicalGitDir(ctx, root, pack, originURL)
}

func initCanonicalGitDir(ctx context.Context, root string, pack *engineutil.GitCheckoutPack) error {
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
	return nil
}

// finishCanonicalGitHead points HEAD at the packed commit, once its objects
// and the checkout's refs are in place, and rebuilds the derived state.
func finishCanonicalGitHead(ctx context.Context, root string, pack *engineutil.GitCheckoutPack) error {
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
	return nil
}

func finishCanonicalGitDir(ctx context.Context, root string, pack *engineutil.GitCheckoutPack, originURL string) error {
	// Preserve named routing and captured selection for consumers mounting
	// Workspace.git.directory. Older clients supply only an origin URL.
	if pack.HasRemoteMetadata {
		remotes := make([]GitRemote, 0, len(pack.Remotes))
		for _, remote := range pack.Remotes {
			entry := GitRemote{Name: remote.Name, URL: remote.Url, PushURL: remote.PushUrl}
			remotes = append(remotes, entry)
			if err := writeGitCheckoutRemote(ctx, gitutil.NewGitCLI(gitutil.WithDir(root)), entry); err != nil {
				return err
			}
		}
		if err := writeGitRemoteSelection(ctx, gitutil.NewGitCLI(gitutil.WithDir(root)), remotes, pack.UpstreamRemote); err != nil {
			return err
		}
	} else if originURL != "" {
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
