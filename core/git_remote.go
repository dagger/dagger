package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	ctdmount "github.com/containerd/containerd/v2/core/mount"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/moby/sys/mount"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/util/hashutil"
	telemetry "github.com/dagger/otel-go"
)

type RemoteGitRepository struct {
	URL *gitutil.GitURL

	SSHKnownHosts string
	SSHAuthSocket dagql.ObjectResult[*Socket]

	Services ServiceBindings
	Platform Platform

	AuthUsername string
	AuthToken    dagql.ObjectResult[*Secret]
	AuthHeader   dagql.ObjectResult[*Secret]
	Mirror       dagql.ObjectResult[*RemoteGitMirror]
}

var _ GitRepositoryBackend = (*RemoteGitRepository)(nil)

const (
	remoteGitLockPrefix         = "git-remote::"
	remoteGitBloblessLockSuffix = "::blobless"
)

type RemoteGitRef struct {
	*gitutil.Ref
	repo *RemoteGitRepository
}

var _ GitRefBackend = (*RemoteGitRef)(nil)

func (repo *RemoteGitRepository) Remote(ctx context.Context) (result *gitutil.Remote, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "git remote metadata", telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)

	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	cacheKey, err := repo.remoteCacheKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("remote git repository %q: %w", repo.URL.Remote(), err)
	}

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		slog.Info("git remote cache unavailable; running ls-remote", "cache_key", cacheKey)
		return repo.runLsRemote(ctx)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("git remote cache session metadata: %w", err)
	}

	remote, hit, err := cachedGitRemote(ctx, cache, clientMetadata.SessionID, cacheKey, repo.runLsRemote)
	if err != nil {
		return nil, err
	}
	slog.Debug("loaded git remote metadata", "cache_hit", hit, "cache_key", cacheKey)
	return remote, nil
}

// gitRemoteListing is a session's cached ls-remote advertisement of one
// remote. The arbitrary cache cannot replace an entry, so the entry is this
// mutable box instead: a live lookup refreshes it in place, and later lookups
// in the session see the newer refs.
//
// The listing is kept as JSON so that each read decodes its own copy: later
// tweaks (e.g. setting GitRepository.Remote.Head) stay scoped to that caller.
type gitRemoteListing struct {
	mu      sync.Mutex
	payload string
}

func newGitRemoteListing(remote *gitutil.Remote) (*gitRemoteListing, error) {
	listing := &gitRemoteListing{}
	if err := listing.set(remote); err != nil {
		return nil, err
	}
	return listing, nil
}

func (listing *gitRemoteListing) set(remote *gitutil.Remote) error {
	payload, err := json.Marshal(remote)
	if err != nil {
		return err
	}
	listing.mu.Lock()
	listing.payload = string(payload)
	listing.mu.Unlock()
	return nil
}

func (listing *gitRemoteListing) get() string {
	listing.mu.Lock()
	defer listing.mu.Unlock()
	return listing.payload
}

// cachedGitRemote returns the session's listing for cacheKey, running list
// the first time. Under ContextWithLiveGitRemote, a listing that was already
// cached is replaced by a fresh one (once per live lookup), so the caller and
// every later lookup in the session see the remote's current refs.
func cachedGitRemote(
	ctx context.Context,
	cache *dagql.Cache,
	sessionID string,
	cacheKey string,
	list func(context.Context) (*gitutil.Remote, error),
) (_ *gitutil.Remote, hit bool, _ error) {
	cacheRes, err := cache.GetOrInitArbitrary(ctx, sessionID, cacheKey, func(ctx context.Context) (any, error) {
		remote, err := list(ctx)
		if err != nil {
			return nil, err
		}
		return newGitRemoteListing(remote)
	})
	if err != nil {
		return nil, false, err
	}
	if cacheRes == nil {
		return nil, false, fmt.Errorf("git remote cache returned nil result for key %q", cacheKey)
	}
	hit = cacheRes.HitCache()
	if live := liveGitRemoteFromContext(ctx); live != nil {
		// An earlier listing answered: list again, unless this live lookup
		// already did (e.g. latest lists, then resolves the ref it picked).
		// A listing this call ran (or joined in flight) is fresh already.
		listing, ok := cacheRes.Value().(*gitRemoteListing)
		if !ok {
			return nil, false, fmt.Errorf("unexpected git remote cache value type %T", cacheRes.Value())
		}
		stale := hit
		if err := live.refresh(cacheKey, func() error {
			if !stale {
				return nil
			}
			remote, err := list(ctx)
			if err != nil {
				return err
			}
			return listing.set(remote)
		}); err != nil {
			return nil, false, err
		}
	}
	remote, err := remoteFromCacheResult(cacheRes.Value())
	return remote, hit, err
}

// liveGitRemote marks lookups that must list remotes afresh rather than reuse
// the session's listing, as noLock asks. It remembers which listings it
// already refreshed, so one live lookup lists each remote once.
type liveGitRemote struct {
	mu        sync.Mutex
	refreshed map[string]func() error
}

type liveGitRemoteKey struct{}

// ContextWithLiveGitRemote makes remote git lookups made with ctx list the
// remote's refs again instead of reusing the session's cached listing, which
// they then replace. Nested calls inherit the same live lookup, which lists
// each remote at most once.
func ContextWithLiveGitRemote(ctx context.Context) context.Context {
	if liveGitRemoteFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, liveGitRemoteKey{}, &liveGitRemote{
		refreshed: map[string]func() error{},
	})
}

func liveGitRemoteFromContext(ctx context.Context) *liveGitRemote {
	live, _ := ctx.Value(liveGitRemoteKey{}).(*liveGitRemote)
	return live
}

// refresh runs fn once per key for this live lookup; concurrent and later
// callers wait for and share its outcome.
func (live *liveGitRemote) refresh(key string, fn func() error) error {
	live.mu.Lock()
	once, ok := live.refreshed[key]
	if !ok {
		once = sync.OnceValue(fn)
		live.refreshed[key] = once
	}
	live.mu.Unlock()
	return once()
}

// PrimePublicRemote reuses the anonymous visibility probe's advertisement in
// the existing session-owned metadata cache. It never changes a repository
// object shared by several sessions, and never seeds a credentialed or
// service-bound lookup. Existing metadata (including an in-flight load) wins.
func (repo *RemoteGitRepository) PrimePublicRemote(ctx context.Context, remote *gitutil.Remote) error {
	if remote == nil || repo.URL == nil || repo.URL.User != nil ||
		(repo.URL.Scheme != "http" && repo.URL.Scheme != "https") ||
		repo.AuthUsername != "" || repo.AuthToken.Self() != nil ||
		repo.AuthHeader.Self() != nil || repo.SSHAuthSocket.Self() != nil || len(repo.Services) != 0 {
		return nil
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil //nolint:nilerr // Priming is optional when the context has no engine cache.
	}
	cacheKey, err := repo.remoteCacheKey(ctx)
	if err != nil {
		return err
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = cache.GetOrInitArbitrary(ctx, clientMetadata.SessionID, cacheKey, func(context.Context) (any, error) {
		return newGitRemoteListing(remote)
	})
	return err
}

func remoteFromCacheResult(cacheRes any) (*gitutil.Remote, error) {
	var payload string
	switch v := cacheRes.(type) {
	case *gitRemoteListing:
		payload = v.get()
	case string:
		payload = v
	default:
		return nil, fmt.Errorf("unexpected cache value type %T", cacheRes)
	}

	var remote gitutil.Remote
	if err := json.Unmarshal([]byte(payload), &remote); err != nil {
		return nil, fmt.Errorf("decode cached remote: %w", err)
	}
	return &remote, nil
}

func (repo *RemoteGitRepository) Get(ctx context.Context, target *gitutil.Ref) (GitRefBackend, error) {
	return &RemoteGitRef{
		repo: repo,
		Ref:  target,
	}, nil
}

// ResolveShortSHA expands an abbreviated commit SHA against the engine's
// local mirror of the remote. The Git protocol cannot ask a remote to expand
// a prefix (ls-remote only advertises ref tips, and a fetch wants full
// object names), so the mirror's objects answer first: a commit fetched
// before, e.g. by a tree checkout, resolves without contacting the remote.
// When nothing there matches, the remote's blobless mirror fetches the
// history of the refs the remote advertises and expands the prefix there
// (see fetchForShortSHA), like a clone followed by `git rev-parse` would.
// Fetching only commits and trees keeps that affordable, and the separate
// mirror leaves checkouts of the remote free to proceed meanwhile.
func (repo *RemoteGitRepository) ResolveShortSHA(ctx context.Context, prefix string) (string, error) {
	sha, err := repo.resolveShortSHA(ctx, prefix, false)
	if !errors.Is(err, gitutil.ErrShortSHANotFound) || repo.Mirror.Self().Blobless {
		return sha, err
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return "", err
	}
	blobless, err := repo.withBloblessMirror(ctx, dag)
	if err != nil {
		return "", err
	}
	return blobless.resolveShortSHA(ctx, prefix, true)
}

// resolveShortSHA expands prefix against this mirror's objects, fetching the
// remote's history into it first when they lack the commit and fetch is set.
func (repo *RemoteGitRepository) resolveShortSHA(ctx context.Context, prefix string, fetch bool) (string, error) {
	var sha string
	err := repo.mount(ctx, 0, false, nil, func(ctx context.Context, git *gitutil.GitCLI) error {
		var err error
		sha, err = git.ResolveShortSHA(ctx, prefix)
		if fetch && errors.Is(err, gitutil.ErrShortSHANotFound) {
			sha, err = repo.fetchForShortSHA(ctx, git, prefix)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	return sha, nil
}

// shortSHAFetchRefSpecs are the refs fetched, round by round, to expand an
// abbreviated SHA the mirror lacks: branches and tags first, where the
// commits people abbreviate almost always are, then every other ref the
// remote advertises (e.g. pull request heads), which can be many more. Their
// destinations are scratch refs, deleted once the prefix is resolved.
var shortSHAFetchRefSpecs = [][]string{
	{"+refs/heads/*:" + shortSHAScratchRefs + "heads/*", "+refs/tags/*:" + shortSHAScratchRefs + "tags/*"},
	{"+refs/*:" + shortSHAScratchRefs + "all/*"},
}

const shortSHAScratchRefs = "refs/dagger.shortsha/"

// fetchForShortSHA fetches the commits and trees of the remote's advertised
// refs, without file contents, into the mirror, round by round (see
// shortSHAFetchRefSpecs), until prefix expands. The expanded commit is kept
// reachable, so expanding the prefix again fetches nothing. A prefix no
// advertised ref reaches cannot be expanded at all: remotes do not serve
// unreachable commits by prefix.
func (repo *RemoteGitRepository) fetchForShortSHA(ctx context.Context, git *gitutil.GitCLI, prefix string) (_ string, rerr error) {
	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		rerr = errors.Join(rerr, deleteGitRefs(ctx, git, shortSHAScratchRefs))
	}()
	var sha string
	err = repo.withFetch(ctx, git, func(ctx context.Context, fetchGit *gitutil.GitCLI) (rerr error) {
		ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindIO, "git.mirror.fetch.shortsha", wcprof.OpOpts{Ident: remoteGitLockPrefix + repo.URL.RedactedRemote(), WorkType: wcprof.WorkTypeEngine})
		defer func() { op.EndErr(rerr) }()
		var resolveErr error
		for _, refSpecs := range shortSHAFetchRefSpecs {
			// A remote that doesn't support filters ignores this and sends
			// the contents too.
			args := []string{"fetch", "--progress", "--no-tags", "--force", "--filter=blob:none"}
			if _, err := os.Lstat(filepath.Join(gitDir, "shallow")); err == nil {
				// Commits behind a shallow boundary are absent too.
				args = append(args, "--unshallow")
			}
			args = append(args, "origin")
			if _, err := fetchGit.Run(ctx, append(args, refSpecs...)...); err != nil {
				return fmt.Errorf("fetch remote %s to expand short SHA %q: %w", repo.URL.RedactedRemote(), prefix, err)
			}
			sha, resolveErr = git.ResolveShortSHA(ctx, prefix)
			if resolveErr == nil {
				_, err := git.Run(ctx, "update-ref", fetchedGitRef(sha), sha)
				return err
			}
			if !errors.Is(resolveErr, gitutil.ErrShortSHANotFound) {
				return resolveErr
			}
		}
		return fmt.Errorf("%w in any ref %s advertises: use the full SHA of a commit it serves", resolveErr, repo.URL.RedactedRemote())
	})
	return sha, err
}

// deleteGitRefs deletes every ref under prefix, in one process however many
// there are.
func deleteGitRefs(ctx context.Context, git *gitutil.GitCLI, prefix string) error {
	out, err := git.Run(ctx, "for-each-ref", "--format=delete %(refname)", prefix)
	if err != nil {
		return fmt.Errorf("list %s refs: %w", prefix, err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	if _, err := git.RunWithStdin(ctx, bytes.NewReader(out), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("delete %s refs: %w", prefix, err)
	}
	return nil
}

func (repo *RemoteGitRepository) remoteCacheKey(ctx context.Context) (string, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}
	inputs := []string{clientMetadata.SessionID, repo.URL.Remote()}
	inputs = append(inputs, repo.remoteCacheScope()...)
	return hashutil.HashStrings(inputs...).String(), nil
}

// Pipelines can query the same remote with different credentials (for example,
// while checking a credential rotation), so scope the cache key by
// authentication configuration rather than sharing entries across methods.
func (repo *RemoteGitRepository) remoteCacheScope() []string {
	scope := make([]string, 0, 4)
	if token := repo.AuthToken; token.Self() != nil {
		if tokenHandle := token.Self().Handle; tokenHandle != "" {
			scope = append(scope, "token:"+string(tokenHandle))
		}
	}
	if header := repo.AuthHeader; header.Self() != nil {
		if headerHandle := header.Self().Handle; headerHandle != "" {
			scope = append(scope, "header:"+string(headerHandle))
		}
	}
	if repo.AuthUsername != "" {
		scope = append(scope, "username:"+repo.AuthUsername)
	}
	if sshSock := repo.SSHAuthSocket; sshSock.Self() != nil {
		if sshHandle := sshSock.Self().Handle; sshHandle != "" {
			scope = append(scope, "ssh-auth-scope:"+string(sshHandle))
		}
	}
	return scope
}

func (repo *RemoteGitRepository) runLsRemote(ctx context.Context) (*gitutil.Remote, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	git, cleanup, err := repo.setup(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	remote, err := git.LsRemote(ctx, repo.URL.Remote())
	if err != nil {
		return nil, err
	}
	return remote, nil
}

func (repo *RemoteGitRepository) Dirty(ctx context.Context) (inst dagql.ObjectResult[*Directory], _ error) {
	// git remotes are always clean
	return inst, nil
}

func (repo *RemoteGitRepository) Cleaned(ctx context.Context) (inst dagql.ObjectResult[*Directory], _ error) {
	// git remotes are always clean
	return inst, nil
}

func (repo *RemoteGitRepository) setup(ctx context.Context) (_ *gitutil.GitCLI, _ func() error, rerr error) {
	return repo.setupWithSSHAuthSock(ctx, "")
}

// sshAuthSock is an operation-local agent mount, never a repository capability.
func (repo *RemoteGitRepository) setupWithSSHAuthSock(ctx context.Context, sshAuthSock string) (_ *gitutil.GitCLI, _ func() error, rerr error) {
	if repo.URL != nil && repo.URL.Scheme == gitutil.SSHProtocol && repo.SSHAuthSocket.Self() == nil && sshAuthSock == "" {
		return nil, nil, fmt.Errorf("%w: SSH URLs are not supported without an SSH socket", gitutil.ErrGitAuthFailed)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, nil, err
	}
	// Keep automatic maintenance inside the mirror lock and mount lifetime.
	// Detached repacks can otherwise rewrite shallow during a later fetch.
	opts := []gitutil.Option{gitutil.WithConfig(map[string]string{
		"gc.autoDetach":          "false",
		"maintenance.autoDetach": "false",
	})}
	if repo.Mirror.Self() != nil && repo.Mirror.Self().Blobless {
		// Every fetch into a blobless mirror is an explicit one. Git would
		// otherwise answer the check for a commit it doesn't have yet by
		// fetching it with its whole history.
		opts = append(opts, gitutil.WithEnv("GIT_NO_LAZY_FETCH=1"))
	}
	if sshAuthSock != "" {
		opts = append(opts, gitutil.WithSSHAuthSock(sshAuthSock))
	}

	cleanups := cleanups.Cleanups{}
	defer func() {
		if rerr != nil {
			cleanups.Run()
		}
	}()

	if repo.SSHAuthSocket.Self() != nil {
		sockpath, cleanup, err := repo.SSHAuthSocket.Self().MountSSHAgent(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to mount SSH socket: %w", err)
		}
		opts = append(opts, gitutil.WithSSHAuthSock(sockpath))
		cleanups.Add("cleanup SSH socket", cleanup)
	}

	var knownHostsPath string
	if repo.SSHKnownHosts != "" {
		var err error
		knownHostsPath, err = mountKnownHosts(repo.SSHKnownHosts)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to mount known hosts: %w", err)
		}
		opts = append(opts, gitutil.WithSSHKnownHosts(knownHostsPath))
		cleanups.Add("remove known hosts", func() error {
			return os.Remove(knownHostsPath)
		})
	}

	netConf, err := DNSConfig(ctx)
	if err != nil {
		return nil, nil, err
	}

	var resolvPath string
	if netConf != nil {
		var err error
		resolvPath, err = mountResolv(netConf)
		if err != nil {
			return nil, nil, err
		}
		cleanups.Add("remove updated /etc/resolv", func() error {
			return os.Remove(resolvPath)
		})
	}

	if repo.URL != nil && repo.URL.Scheme == gitutil.EntireProtocol {
		if repo.AuthHeader.Self() != nil {
			return nil, nil, fmt.Errorf("entire:// remotes take a token, not an HTTP authorization header")
		}
		configDir, cacheHome, err := entireHelperDirs(ctx, query)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, gitutil.WithEntireHelperDirs(repo.URL, configDir, cacheHome))
	}

	if repo.AuthToken.Self() != nil {
		password, err := repo.AuthToken.Self().Plaintext(ctx)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts,
			gitutil.WithHTTPTokenAuth(repo.URL, string(password), repo.AuthUsername),
			gitutil.WithEntireTokenAuth(repo.URL, string(password)),
		)
	} else if repo.AuthHeader.Self() != nil {
		byteAuthHeader, err := repo.AuthHeader.Self().Plaintext(ctx)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, gitutil.WithHTTPAuthorizationHeader(repo.URL, string(byteAuthHeader)))
	}

	opts = append(opts, gitutil.WithExec(func(ctx context.Context, cmd *exec.Cmd) error {
		return runWithStandardUmaskAndNetOverride(ctx, cmd, "", resolvPath, query.CleanMountNS())
	}))

	return gitutil.NewGitCLI(opts...), cleanups.Run, nil
}

// GitRemoteEntireDir holds git-remote-entire's state under the engine root.
// Nothing in it outlives the engine process: the engine removes it on start.
const GitRemoteEntireDir = "git-remote-entire"

// entireNodesCacheLimit bounds git-remote-entire's per-repository replica
// cache. The helper never drops expired entries and rewrites the whole file on
// every operation, so a bigger file costs every Entire git operation.
const entireNodesCacheLimit = 5 << 20

// entireHelperDirs returns the engine-wide directories git-remote-entire runs
// with: an empty config directory, so it never finds a saved login, and the
// cache home for its discovery cache, which holds no credentials and is shared
// by every session.
func entireHelperDirs(ctx context.Context, query *Query) (configDir, cacheHome string, _ error) {
	root := query.EngineVolumeState().RootDir
	if root == "" {
		return "", "", fmt.Errorf("engine root is not configured")
	}
	base := filepath.Join(root, GitRemoteEntireDir)
	configDir = filepath.Join(base, "config")
	cacheHome = filepath.Join(base, "cache")
	for _, dir := range []string{configDir, cacheHome} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", fmt.Errorf("create git-remote-entire directory: %w", err)
		}
	}
	if err := trimEntireNodesCache(cacheHome, entireNodesCacheLimit); err != nil {
		// Trimming only bounds a cache; the helper works with or without it.
		slog.SpanLogger(ctx, InstrumentationLibrary).Warn("failed to trim git-remote-entire cache", "error", err)
	}
	return configDir, cacheHome, nil
}

// trimEntireNodesCache removes git-remote-entire's replica cache once it
// exceeds limit bytes. The helper rebuilds entries from the cluster's
// redirects as repositories are used again. The removal takes the lock the
// helper holds for its read-modify-write, and skips the trim when a helper is
// mid-update rather than waiting for it.
func trimEntireNodesCache(cacheHome string, limit int64) error {
	cache := filepath.Join(cacheHome, "entire", "nodes.json")
	info, err := os.Stat(cache)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.Size() <= limit) {
		return nil
	}
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(cache+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	// Closing the lock file releases the lock.
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil
		}
		return fmt.Errorf("lock %s: %w", cache, err)
	}
	if err := os.Remove(cache); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (repo *RemoteGitRepository) mount(ctx context.Context, depth int, includeTags bool, refs []GitRefBackend, fn func(context.Context, *gitutil.GitCLI) error) (retErr error) {
	return repo.mountFetched(ctx, depth, includeTags, refs, func(ctx context.Context, git *gitutil.GitCLI, _ *gitMirrorFetch) error {
		return fn(ctx, git)
	})
}

// mountFetched is mount, also giving fn what the hold's fetch brought when
// it filled a mirror the hold created (see gitMirrorFetchedPack), or nil.
func (repo *RemoteGitRepository) mountFetched(ctx context.Context, depth int, includeTags bool, refs []GitRefBackend, fn func(context.Context, *gitutil.GitCLI, *gitMirrorFetch) error) (retErr error) {
	return repo.initRemote(ctx, func(ctx context.Context, remote string, created bool) (rerr error) {
		git, cleanup, err := repo.setup(ctx)
		if err != nil {
			return err
		}
		defer func() { rerr = errors.Join(rerr, cleanup()) }()
		git = git.New(gitutil.WithGitDir(remote))
		remoteRefs := make([]*RemoteGitRef, len(refs))
		for i, ref := range refs {
			remoteRefs[i] = ref.(*RemoteGitRef)
		}
		fetchRefs, err := gitRefsToFetch(ctx, git, depth, remoteRefs)
		if err != nil {
			return err
		}
		retried, err := repo.fetch(ctx, git, depth, includeTags, fetchRefs)
		if err != nil {
			return err
		}
		_, err = git.Run(ctx, "reflog", "expire", "--all", "--expire=now")
		if err != nil {
			return fmt.Errorf("failed to expire reflog for remote %s: %w", repo.URL.Remote(), err)
		}

		fetched, err := gitMirrorFetchedForCheckout(remote, created, depth, includeTags, fetchRefs, retried)
		if err != nil {
			return err
		}
		return fn(ctx, git, fetched)
	})
}

// gitMirrorFetch is what one fetch brought into a mirror that held nothing
// before it: one commit's history to a depth, as one pack.
type gitMirrorFetch struct {
	sha   string
	depth int
	// gitDir is the mirror's, which holds the fetch's shallow file.
	gitDir string
	// pack is the pack's path without its extension; its index and reverse
	// index sit beside it.
	pack string
}

// gitMirrorFetchedForCheckout returns what the hold's fetch of refs brought
// into the mirror at gitDir, when it is exactly one commit's history at depth
// a checkout of that commit at that depth would fetch out of the mirror: the
// hold created the mirror, and fetched one commit by itself, without tags.
// A retry by the commit's named ref fetched the ref's tip instead, whose
// history can hold more. Otherwise, or when the fetch left more than one
// pack, it returns nil.
func gitMirrorFetchedForCheckout(gitDir string, created bool, depth int, includeTags bool, refs []*RemoteGitRef, retried bool) (*gitMirrorFetch, error) {
	if !created || depth <= 0 || includeTags || len(refs) != 1 || retried {
		return nil, nil
	}
	return gitMirrorFetchedPack(gitDir, refs[0].SHA, depth)
}

// gitMirrorFetchedPack returns what the fetch of sha at depth brought into
// the mirror at gitDir, which held nothing before it: then its one pack holds
// exactly that history, and its shallow file exactly that fetch's
// boundaries, as a fetch of the same commit at the same depth from the mirror
// would write them. It returns nil when the fetch left anything but one pack:
// git unpacks a small fetch into loose objects instead.
func gitMirrorFetchedPack(gitDir, sha string, depth int) (*gitMirrorFetch, error) {
	objects := filepath.Join(gitDir, "objects")
	packs, err := filepath.Glob(filepath.Join(objects, "pack", "pack-*.pack"))
	if err != nil {
		return nil, err
	}
	if len(packs) != 1 {
		return nil, nil
	}
	entries, err := os.ReadDir(objects)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 2 {
			loose, err := os.ReadDir(filepath.Join(objects, entry.Name()))
			if err != nil {
				return nil, err
			}
			if len(loose) > 0 {
				return nil, nil
			}
		}
	}
	return &gitMirrorFetch{sha: sha, depth: depth, gitDir: gitDir, pack: strings.TrimSuffix(packs[0], ".pack")}, nil
}

// gitRefsToFetch checks mirror coverage and retains existing commits, including
// those fetched by older engines which left their objects only in FETCH_HEAD.
func gitRefsToFetch(ctx context.Context, git *gitutil.GitCLI, depth int, refs []*RemoteGitRef) ([]*RemoteGitRef, error) {
	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not find git dir: %w", err)
	}

	var fetchRefs []*RemoteGitRef
	for _, ref := range refs {
		// skip fetch if commit already exists
		doFetch := true
		if res, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "rev-parse", "--verify", ref.SHA+"^{commit}"); err != nil {
			return nil, fmt.Errorf("failed to rev-parse: %w", err)
		} else if strings.TrimSpace(string(res)) == ref.SHA {
			doFetch = false
			if _, err := git.Run(ctx, "update-ref", fetchedGitRef(ref.SHA), ref.SHA); err != nil {
				return nil, fmt.Errorf("failed to retain fetched sha %s: %w", ref.SHA, err)
			}

			if _, err := os.Lstat(filepath.Join(gitDir, "shallow")); err == nil {
				// if shallow, check we have enough depth
				if depth <= 0 {
					doFetch = true
				} else {
					// HACK: this is a pretty terrible way to guess the depth,
					// since it only traces *one* path.
					res, err := git.New().Run(ctx, "rev-list", "--first-parent", "--count", ref.SHA)
					if err != nil {
						return nil, fmt.Errorf("failed to rev-list: %w", err)
					}
					res = bytes.TrimSpace(res)
					count, err := strconv.Atoi(string(res))
					if err != nil {
						return nil, fmt.Errorf("failed to parse rev-list output: %w", err)
					}
					if count < depth {
						doFetch = true
					}
				}
			}
		}

		// TODO: should set doFetch if a tag in ls-remote has been updated?
		if doFetch {
			fetchRefs = append(fetchRefs, ref)
		}
	}
	return fetchRefs, nil
}

// withBloblessMirror returns this repository reading from its blobless mirror,
// with the same remote and credentials.
func (repo *RemoteGitRepository) withBloblessMirror(ctx context.Context, dag *dagql.Server) (*RemoteGitRepository, error) {
	blobless := *repo
	if err := dag.Select(ctx, dag.Root(), &blobless.Mirror, dagql.Selector{
		Field: "_remoteGitMirror",
		Args: []dagql.NamedInput{
			{Name: "remoteURL", Value: dagql.String(repo.URL.Remote())},
			{Name: "blobless", Value: dagql.Boolean(true)},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to select blobless git mirror: %w", err)
	}
	return &blobless, nil
}

// fetchBlobs fetches file contents by object ID into a blobless mirror, in one
// request, the way Git asks a promisor remote for a batch of them.
func (repo *RemoteGitRepository) fetchBlobs(ctx context.Context, git *gitutil.GitCLI, oids io.Reader) (rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindIO, "git.mirror.fetch.blobs", wcprof.OpOpts{Ident: remoteGitLockPrefix + repo.URL.RedactedRemote(), WorkType: wcprof.WorkTypeEngine})
	defer func() { op.EndErr(rerr) }()
	svcs, err := query.Services(ctx)
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Services)
	if err != nil {
		return err
	}
	defer detach()
	_, err = git.RunWithStdin(ctx, oids,
		"-c", "fetch.negotiationAlgorithm=noop",
		"fetch", "origin",
		"--no-tags",
		"--no-write-fetch-head",
		"--recurse-submodules=no",
		"--filter=blob:none",
		"--stdin",
	)
	return err
}

// fetch fetches refs into the mirror; retried says the remote refused a
// commit and its named ref was fetched instead (see fetchObjects).
func (repo *RemoteGitRepository) fetch(ctx context.Context, git *gitutil.GitCLI, depth int, includeTags bool, refs []*RemoteGitRef) (retried bool, rerr error) {
	if len(refs) == 0 && !includeTags {
		// Nothing requested: avoid an implicit broad fetch from origin.
		return false, nil
	}

	rerr = repo.withFetch(ctx, git, func(ctx context.Context, git *gitutil.GitCLI) error {
		var err error
		retried, err = repo.fetchObjects(ctx, git, depth, includeTags, refs)
		return err
	})
	return retried, rerr
}

// withFetch runs fn with what fetching from the remote needs: its service
// bindings started, and git reporting transfer progress on a "fetching
// <remote>" span.
func (repo *RemoteGitRepository) withFetch(ctx context.Context, git *gitutil.GitCLI, fn func(context.Context, *gitutil.GitCLI) error) (rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}

	// Encapsulated like the resolver's "pulling" span: hidden unless it
	// fails, surfacing as a labeled progress row only when objects actually
	// transfer (an up-to-date repo skips fetching entirely).
	span, ctx := tracing.StartSpan(ctx, "fetching "+repo.URL.Remote(), telemetry.Encapsulated(), telemetry.Encapsulate())
	defer func() {
		tracing.FinishWithError(span, rerr)
	}()
	git = git.New(gitutil.WithStreams(gitFetchProgressStreams(ctx)))

	svcs, err := query.Services(ctx)
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Services)
	if err != nil {
		return err
	}
	defer detach()

	return fn(ctx, git)
}

// fetchObjects operates on the private mirror; service bindings and progress
// streams are supplied by fetch so the Git protocol can also be tested directly.
// retried says the remote refused a commit, and its named ref was fetched
// instead. The fetch is recorded as a git.mirror.fetch op, under the mirror's
// hold when the caller holds it, so a profile tells it from the rest of the hold.
func (repo *RemoteGitRepository) fetchObjects(ctx context.Context, git *gitutil.GitCLI, depth int, includeTags bool, refs []*RemoteGitRef) (retried bool, rerr error) {
	if len(refs) == 0 && !includeTags {
		return false, nil
	}
	ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindIO, "git.mirror.fetch", wcprof.OpOpts{Ident: remoteGitLockPrefix + repo.URL.RedactedRemote(), WorkType: wcprof.WorkTypeEngine})
	defer func() { op.EndErr(rerr) }()

	// Fetch by object SHA in the hot path (`--no-tags`), and only retry by named refs for SHA-incompatible remotes.
	logger := slog.SpanLogger(ctx, InstrumentationLibrary)

	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return false, err
	}

	depth, err = mirrorFetchDepth(ctx, git, gitDir, depth)
	if err != nil {
		return false, err
	}

	shaRefSpecs := make([]string, len(refs))
	for i, ref := range refs {
		// FETCH_HEAD alone is not a negotiation tip. Keep each pinned commit
		// reachable so subsequent fetches advertise its objects as "have",
		// without populating the public branch or tag namespaces.
		shaRefSpecs[i] = ref.SHA + ":" + fetchedGitRef(ref.SHA)
	}

	runFetch := func(refSpecs []string) error {
		args := []string{
			"fetch",
			// stderr is not a tty, so sideband transfer progress (parsed by
			// gitProgressWriter) needs asking for
			"--progress",
			"--no-tags",
			"--update-head-ok",
			"--force",
		}
		if depth <= 0 {
			if _, err := os.Lstat(filepath.Join(gitDir, "shallow")); err == nil {
				args = append(args, "--unshallow")
			}
		} else {
			args = append(args, "--depth="+fmt.Sprint(depth))
		}
		args = append(args, "origin")
		args = append(args, refSpecs...)

		if _, err := git.Run(ctx, args...); err != nil {
			if errors.Is(err, gitutil.ErrShallowNotSupported) {
				// fallback to full fetch
				args = slices.DeleteFunc(args, func(s string) bool {
					return strings.HasPrefix(s, "--depth")
				})
				_, err = git.Run(ctx, args...)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}

	runFetchTags := func() error {
		// Keep the hot path tag-free and only hydrate local tag refs when explicitly requested.
		return runFetch([]string{"refs/tags/*:refs/tags/*"})
	}

	cleanupScratchFetchRefs := func(refSpecs []string) {
		cleanupGit := git.New(
			gitutil.WithIgnoreError(),
			gitutil.WithGitDir(gitDir),
		)
		for _, refSpec := range refSpecs {
			_, dst, ok := strings.Cut(refSpec, ":")
			if !ok || dst == "" {
				continue
			}
			_, _ = cleanupGit.Run(ctx, "update-ref", "-d", dst)
		}
	}

	verifyFetchedSHAs := func(expectedRefs []*RemoteGitRef) error {
		for _, ref := range expectedRefs {
			if ref == nil || ref.SHA == "" {
				continue
			}
			res, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "rev-parse", "--verify", ref.SHA+"^{commit}")
			if err != nil {
				return fmt.Errorf("failed to verify fetched sha %s: %w", ref.SHA, err)
			}
			if strings.TrimSpace(string(res)) != ref.SHA {
				return fmt.Errorf("named-ref retry did not materialize expected sha %s for %q", ref.SHA, ref.Name)
			}
		}
		return nil
	}

	if len(shaRefSpecs) > 0 {
		err = runFetch(shaRefSpecs)
		if err != nil {
			// A server that serves only advertised tips refuses other
			// commits as not found, so a named ref may still fetch them.
			if !errors.Is(err, gitutil.ErrSHAFetchUnsupported) && !errors.Is(err, gitutil.ErrCommitNotFound) {
				return false, fmt.Errorf("failed to fetch remote %s: %w", repo.URL.Remote(), err)
			}

			namedSpecs := namedFetchRefSpecs(refs)
			if len(namedSpecs) == 0 {
				return false, fmt.Errorf("failed to fetch remote %s: %w: %s", repo.URL.Remote(), err, joinRefSHAs(refs))
			}
			defer cleanupScratchFetchRefs(namedSpecs)

			logger.Debug("git fetch by sha failed; retrying with named refs", "remote", repo.URL.Remote(), "refspec_count", len(namedSpecs))
			retried = true
			if retryErr := runFetch(namedSpecs); retryErr != nil {
				return retried, fmt.Errorf("failed to fetch remote %s: sha fetch failed: %w; named-ref retry failed: %w", repo.URL.Remote(), err, retryErr)
			}
			if verifyErr := verifyFetchedSHAs(refs); verifyErr != nil {
				return retried, fmt.Errorf("failed to fetch remote %s: named-ref retry verification failed: %w", repo.URL.Remote(), verifyErr)
			}
			// The named ref may have moved since resolution. Retain the verified
			// pinned SHA, not the scratch ref's potentially newer target.
			for _, ref := range refs {
				if _, pinErr := git.Run(ctx, "update-ref", fetchedGitRef(ref.SHA), ref.SHA); pinErr != nil {
					return retried, fmt.Errorf("failed to retain fetched sha %s: %w", ref.SHA, pinErr)
				}
			}
			logger.Debug("git fetch named-ref retry succeeded", "remote", repo.URL.Remote(), "refspec_count", len(namedSpecs))
		}
	}

	if includeTags {
		if tagErr := runFetchTags(); tagErr != nil {
			return retried, fmt.Errorf("failed to hydrate tags for remote %s: %w", repo.URL.Remote(), tagErr)
		}
	}

	return retried, nil
}

// mirrorFetchDepth returns the depth to fetch into the mirror at gitDir.
//
// A depth limit only saves transfer on an empty or already-shallow mirror.
// Fetching a new tip with --depth into a complete mirror records shallow
// boundaries over history it already has, and removing them again for the
// next full-history read can resend that entire history. Fetching the tip
// completely costs only the commits it adds.
func mirrorFetchDepth(ctx context.Context, git *gitutil.GitCLI, gitDir string, depth int) (int, error) {
	if depth <= 0 {
		return depth, nil
	}
	complete, err := gitMirrorIsComplete(ctx, git, gitDir)
	if err != nil {
		return 0, err
	}
	if complete {
		return 0, nil
	}
	return depth, nil
}

// gitMirrorIsComplete reports whether the mirror holds history and none of it
// is shallow. Mirrors written by older engines may only reference their
// objects from FETCH_HEAD.
func gitMirrorIsComplete(ctx context.Context, git *gitutil.GitCLI, gitDir string) (bool, error) {
	if _, err := os.Lstat(filepath.Join(gitDir, "shallow")); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "FETCH_HEAD")); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	out, err := git.Run(ctx, "for-each-ref", "--count=1", "--format=%(refname)")
	if err != nil {
		return false, fmt.Errorf("list mirror refs: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// fetchedGitRef is private to the mutable mirror, never a checkout refspec.
func fetchedGitRef(sha string) string {
	return "refs/dagger.fetched/" + sha
}

func joinRefSHAs(refs []*RemoteGitRef) string {
	shas := make([]string, len(refs))
	for i, ref := range refs {
		shas[i] = ref.SHA
	}
	return strings.Join(shas, ", ")
}

// namedFetchRefSpecs builds the bounded fallback refspec set used when SHA fetch is unsupported.
// Destinations are deterministic scratch refs (`refs/dagger.fetch/...`) so retries don't mutate local branch/tag refs.
func namedFetchRefSpecs(refs []*RemoteGitRef) []string {
	refSpecs := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref == nil || ref.Name == "" || gitutil.IsCommitSHA(ref.Name) {
			continue
		}
		// Deterministic scratch destination keeps retries isolated from local branch/tag namespaces.
		stableName := hashutil.HashStrings(ref.Name, ref.SHA).Encoded()
		refSpecs = append(refSpecs, ref.Name+":refs/dagger.fetch/"+stableName)
	}
	return refSpecs
}

// initRemote holds the mirror's lock and runs fn in the mounted mirror;
// created says the mirror was empty and initialized for this hold.
func (repo *RemoteGitRepository) initRemote(ctx context.Context, fn func(ctx context.Context, dir string, created bool) error) (retErr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	locker := query.Locker()
	if repo.Mirror.Self() == nil {
		return fmt.Errorf("remote git mirror is nil for %s", repo.URL.Remote())
	}
	lockKey := remoteGitLockPrefix + repo.URL.Remote()
	if repo.Mirror.Self().Blobless {
		lockKey += remoteGitBloblessLockSuffix
	}
	// Traces and profiles are shared: identify the lock without the URL's
	// userinfo, which may carry credentials.
	lockIdent := remoteGitLockPrefix + repo.URL.RedactedRemote()
	var profWait *wcprof.Wait
	if wcprof.Enabled(ctx) {
		profWait = wcprof.BeginWaitIdent(ctx, lockIdent, wcprof.WaitReasonLock)
	}
	_, waitSpan := Tracer(ctx).Start(ctx, "waiting for git mirror lock: "+repo.URL.RedactedRemote(), telemetry.Internal())
	locker.Lock(lockKey)
	waitSpan.End()
	profWait.End()
	defer locker.Unlock(lockKey)

	// Everything until the unlock holds the mirror for every other consumer
	// of this remote, across sessions: callers keep only the work that reads
	// or updates the mirror inside fn, and finish in their private copy after.
	//
	// fn runs in the holding ctx, so the fetch and the caller's mirror work
	// nest under the hold span and the git.mirror.locked op instead of
	// beside them. The span is passthrough rather than internal: dagui prunes
	// an internal span's whole subtree, which would hide the "fetching" span
	// and its clone progress.
	ctx, holdSpan := Tracer(ctx).Start(ctx, "holding git mirror lock: "+repo.URL.RedactedRemote(), telemetry.Passthrough())
	defer telemetry.EndWithCause(holdSpan, &retErr)
	ctx, holdOp := wcprof.BeginOp(ctx, wcprof.OpKindIO, "git.mirror.locked", wcprof.OpOpts{Ident: lockIdent, WorkType: wcprof.WorkTypeEngine})
	defer func() { holdOp.EndErr(retErr) }()

	if err := EnsureBackingSnapshot(ctx, repo.Mirror); err != nil {
		return err
	}
	acquireCtx, acquireSpan := Tracer(ctx).Start(ctx, "acquiring git mirror: "+repo.URL.RedactedRemote(), telemetry.Internal())
	remoteRef, releaseMirror, err := repo.Mirror.Self().acquire(acquireCtx, query)
	telemetry.EndWithCause(acquireSpan, &err)
	if err != nil {
		return err
	}
	defer releaseMirror()

	mountCtx, mountSpan := Tracer(ctx).Start(ctx, "mounting git mirror: "+repo.URL.RedactedRemote(), telemetry.Internal())
	mount, err := remoteRef.Mount(mountCtx, false)
	if err != nil {
		telemetry.EndWithCause(mountSpan, &err)
		return err
	}

	lm := bkcache.LocalMounter(mount)
	dir, err := lm.Mount()
	telemetry.EndWithCause(mountSpan, &err)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, lm.Unmount())
	}()

	git := gitutil.NewGitCLI(gitutil.WithGitDir(dir))
	initializeRepo := false
	if _, err := os.Lstat(filepath.Join(dir, "HEAD")); errors.Is(err, os.ErrNotExist) {
		initializeRepo = true
	} else if err != nil {
		return err
	}
	if initializeRepo {
		// Explicitly set the Git config 'init.defaultBranch' to the
		// implied default to suppress "hint:" output about not having a
		// default initial branch name set, which otherwise spams unit
		// test logs.
		if _, err := git.Run(ctx, "-c", "init.defaultBranch=main", "init", "--bare", "--quiet"); err != nil {
			return fmt.Errorf("failed to init repo at %s: %w", dir, err)
		}

		if _, err := git.Run(ctx, "remote", "add", "origin", repo.URL.Remote()); err != nil {
			return fmt.Errorf("failed add origin repo at %s: %w", dir, err)
		}

		if repo.Mirror.Self().Blobless {
			// A remote that doesn't support filters ignores this and sends
			// everything, so such a mirror simply holds it all.
			for _, config := range [][2]string{
				{"extensions.partialClone", "origin"},
				{"remote.origin.promisor", "true"},
				{"remote.origin.partialCloneFilter", "blob:none"},
			} {
				if _, err := git.Run(ctx, "config", config[0], config[1]); err != nil {
					return fmt.Errorf("failed to configure blobless mirror at %s: %w", dir, err)
				}
			}
		}
	}

	return fn(ctx, dir, initializeRepo)
}

func (ref *RemoteGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool, remotes []GitRemote, upstreamRemote *string) (_ *Directory, rerr error) {
	if discardGitDir {
		var finish func(path, detail string, skipped []string, err error)
		ctx, finish = startGitSourceTree(ctx)
		defer func() { finish("remote", "", nil, rerr) }()
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	cache := query.SnapshotManager()

	var checkoutRef bkcache.MutableRef
	defer func() {
		if rerr != nil && checkoutRef != nil {
			checkoutRef.Release(context.WithoutCancel(ctx))
		}
	}()

	// Keep checkout credentials and network setup alive independently of the
	// mirror mount: only the object-copy phase needs the mirror's lock, and
	// submodule clones after it still need auth, known hosts and DNS.
	git, cleanup, err := ref.repo.setup(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	checkoutRef, err = cache.New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeGitCheckout),
		bkcache.WithDescription(fmt.Sprintf("git checkout for %s (%s %s)", ref.repo.URL.Remote(), ref.Name, ref.SHA)))
	if err != nil {
		return nil, err
	}

	err = MountRef(ctx, checkoutRef, func(checkoutDir string, _ *ctdmount.Mount) error {
		checkoutDirGit := filepath.Join(checkoutDir, ".git")
		if err := os.MkdirAll(checkoutDir, 0711); err != nil {
			return err
		}
		checkoutGit := git.New(gitutil.WithWorkTree(checkoutDir), gitutil.WithGitDir(checkoutDirGit))

		var tmpref string
		err := ref.repo.mountFetched(ctx, depth, includeTags, []GitRefBackend{ref}, func(ctx context.Context, mirrorGit *gitutil.GitCLI, fetched *gitMirrorFetch) error {
			var err error
			tmpref, _, err = copyGitCheckout(ctx, checkoutGit, mirrorGit, checkoutDirGit, ref.Ref, depth, fetched)
			return err
		})
		if err != nil {
			return err
		}

		// The checkout owns its objects now: checking out files, slow
		// submodule clones and timestamp normalization must not hold the mirror.
		ctx, span := Tracer(ctx).Start(ctx, "finishing git checkout (mirror released)", telemetry.Internal())
		defer telemetry.EndWithCause(span, &err)

		// The clone URL is the remote itself, so it doubles as the
		// checkout's origin; registered remotes overlay it.
		checkoutRemotes := MergeGitRemotes(
			[]GitRemote{{Name: "origin", URL: ref.repo.URL.Remote(), Implicit: true}},
			remotes,
		)
		if upstreamRemote != nil {
			checkoutRemotes = MergeGitRemotes(nil, remotes)
		}
		if err = finishGitCheckout(ctx, checkoutGit, checkoutRemotes, ref.repo.URL.Remote(), ref.Ref, discardGitDir, tmpref, gitCheckoutFresh); err != nil {
			return err
		}
		if !discardGitDir && upstreamRemote != nil {
			err = writeGitRemoteSelection(ctx, checkoutGit, checkoutRemotes, *upstreamRemote)
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to checkout %s in %s: %w", ref.Name, ref.repo.URL.Remote(), err)
	}

	snap, err := checkoutRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	checkoutRef = nil
	defer func() {
		if rerr != nil {
			snap.Release(context.WithoutCancel(ctx))
		}
	}()
	dir := &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.SetPath("/")
	dir.SetSnapshot(snap)
	return dir, nil
}

// remoteTreeWithoutGitDir makes a remote ref's tree without .git from its
// tree with .git at the same depth, selected through the call cache, as a
// child that removes .git: a workspace and a module loaded from one commit
// then share one checkout, where each checked the commit out on its own. A
// checkout without .git copies the objects and writes .git as one with .git
// does, removing it last (finishGitCheckout), so this costs no more for a
// tree without .git alone. As there, the root's times are normalized after
// .git goes, so the tree is the one a checkout without .git makes.
func remoteTreeWithoutGitDir(ctx context.Context, srv *dagql.Server, dst *Directory, ref dagql.ObjectResult[*GitRef], depth int) (rerr error) {
	var withGitDir dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, ref, &withGitDir, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{
		{Name: "depth", Value: dagql.NewInt(depth)},
	}}); err != nil {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, withGitDir); err != nil {
		return err
	}
	parent, err := withGitDir.Self().Snapshot.GetOrEval(ctx, withGitDir.Result)
	if err != nil {
		return err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	child, err := query.SnapshotManager().New(ctx, parent, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("git tree without .git for %s (%s)", ref.Self().Repo.Self().Backend.(*RemoteGitRepository).URL.Remote(), ref.Self().Ref.SHA)))
	if err != nil {
		return err
	}
	defer func() {
		if rerr != nil && child != nil {
			child.Release(context.WithoutCancel(ctx))
		}
	}()
	if err := MountRef(ctx, child, func(root string, _ *ctdmount.Mount) error {
		if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
			return err
		}
		return unix.UtimesNanoAt(unix.AT_FDCWD, root, gitCheckoutTimes(), unix.AT_SYMLINK_NOFOLLOW)
	}); err != nil {
		return err
	}
	snap, err := child.Commit(ctx)
	if err != nil {
		return err
	}
	child = nil
	dst.SetPath("/")
	dst.SetSnapshot(snap)
	return nil
}

func (ref *RemoteGitRef) mount(ctx context.Context, depth int, includeTags bool, fn func(context.Context, *gitutil.GitCLI) error) error {
	return ref.repo.mount(ctx, depth, includeTags, []GitRefBackend{ref}, fn)
}

func DNSConfig(ctx context.Context) (*oci.DNSConfig, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	namespace := clientMetadata.SessionID

	clientDomains := []string{}
	clientDomains = append(clientDomains, network.SessionDomain(namespace))

	dns := *query.DNS()
	dns.SearchDomains = append(clientDomains, dns.SearchDomains...)
	return &dns, nil
}

func mergeResolv(dst *os.File, src io.Reader, dns *oci.DNSConfig) error {
	srcScan := bufio.NewScanner(src)

	var replacedSearch bool
	var replacedOptions bool

	for _, ns := range dns.Nameservers {
		if _, err := fmt.Fprintln(dst, "nameserver", ns); err != nil {
			return err
		}
	}

	for srcScan.Scan() {
		switch {
		case strings.HasPrefix(srcScan.Text(), "search"):
			oldDomains := strings.Fields(srcScan.Text())[1:]
			newDomains := slices.Clone(dns.SearchDomains)
			newDomains = append(newDomains, oldDomains...)
			if _, err := fmt.Fprintln(dst, "search", strings.Join(newDomains, " ")); err != nil {
				return err
			}
			replacedSearch = true
		case strings.HasPrefix(srcScan.Text(), "options"):
			oldOptions := strings.Fields(srcScan.Text())[1:]
			newOptions := slices.Clone(dns.Options)
			newOptions = append(newOptions, oldOptions...)
			if _, err := fmt.Fprintln(dst, "options", strings.Join(newOptions, " ")); err != nil {
				return err
			}
			replacedOptions = true
		case strings.HasPrefix(srcScan.Text(), "nameserver"):
			if len(dns.Nameservers) == 0 {
				// preserve existing nameservers
				if _, err := fmt.Fprintln(dst, srcScan.Text()); err != nil {
					return err
				}
			}
		default:
			if _, err := fmt.Fprintln(dst, srcScan.Text()); err != nil {
				return err
			}
		}
	}

	if !replacedSearch {
		if _, err := fmt.Fprintln(dst, "search", strings.Join(dns.SearchDomains, " ")); err != nil {
			return err
		}
	}

	if !replacedOptions {
		if _, err := fmt.Fprintln(dst, "options", strings.Join(dns.Options, " ")); err != nil {
			return err
		}
	}

	return nil
}

func runWithStandardUmaskAndNetOverride(ctx context.Context, cmd *exec.Cmd, hosts, resolv string, cleanMntNS *os.File) error {
	errCh := make(chan error)

	go func() {
		defer close(errCh)
		runtime.LockOSThread()

		if err := unshareAndRun(ctx, cmd, hosts, resolv, cleanMntNS); err != nil {
			errCh <- err
		}
	}()

	return <-errCh
}

// unshareAndRun needs to be called in a locked thread.
func unshareAndRun(ctx context.Context, cmd *exec.Cmd, hosts, resolv string, cleanMntNS *os.File) error {
	// avoid leaking mounts from the engine by using an isolated clean mount namespace (see container start code,
	// currently in engine/engineutil/executor_spec.go, for more details)
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return fmt.Errorf("unshare fs attrs: %w", err)
	}
	if err := unix.Setns(int(cleanMntNS.Fd()), unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("setns clean mount namespace: %w", err)
	}
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("unshare new mount namespace: %w", err)
	}

	syscall.Umask(0022)
	if err := overrideNetworkConfig(hosts, resolv); err != nil {
		return fmt.Errorf("failed to override network config: %w", err)
	}
	return runProcessGroup(ctx, cmd)
}

func overrideNetworkConfig(hostsOverride, resolvOverride string) error {
	if hostsOverride != "" {
		if err := mount.Mount(hostsOverride, "/etc/hosts", "", "bind"); err != nil {
			return fmt.Errorf("mount hosts override %s: %w", hostsOverride, err)
		}
	}
	if resolvOverride != "" {
		if err := mount.Mount(resolvOverride, "/etc/resolv.conf", "", "bind"); err != nil {
			return fmt.Errorf("mount resolv override %s: %w", resolvOverride, err)
		}
	}

	return nil
}

// runProcessGroup runs cmd in its own process group so a cancelled context
// tears down every helper it spawned, and asks the kernel to SIGTERM it if the
// engine dies. Pdeathsig fires when the OS thread that forked the child exits,
// not when the process does, and Go retires threads whenever a goroutine that
// locked one exits. Pin this goroutine to its thread for the child's lifetime:
// a locked thread only goes away with its goroutine, after Wait has returned,
// so the child never sees a spurious SIGTERM from an unrelated thread's exit.
func runProcessGroup(ctx context.Context, cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &unix.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = unix.SIGTERM
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := cmd.Start(); err != nil {
		return err
	}
	waitDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = unix.Kill(-cmd.Process.Pid, unix.SIGTERM)
			go func() {
				select {
				case <-waitDone:
				case <-time.After(10 * time.Second):
					_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
				}
			}()
		case <-waitDone:
		}
	}()
	err := cmd.Wait()
	close(waitDone)
	return err
}

func mountKnownHosts(knownHosts string) (string, error) {
	tempFile, err := os.CreateTemp("", "known_hosts")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary known_hosts file: %w", err)
	}

	_, err = tempFile.WriteString(knownHosts)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to write known_hosts content: %w", err)
	}

	err = tempFile.Close()
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to close temporary known_hosts file: %w", err)
	}

	return tempFile.Name(), nil
}

func mountResolv(dns *oci.DNSConfig) (string, error) {
	src, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return "", err
	}
	defer src.Close()

	tempFile, err := os.CreateTemp("", "dagger-git-resolv")
	if err != nil {
		return "", fmt.Errorf("create resolv.conf override: %w", err)
	}

	if err := mergeResolv(tempFile, src, dns); err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	err = tempFile.Close()
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to close temporary resolv.conf file: %w", err)
	}

	if err := os.Chmod(tempFile.Name(), 0644); err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	return tempFile.Name(), nil
}
