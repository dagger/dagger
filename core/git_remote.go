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

const remoteGitLockPrefix = "git-remote::"

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
	slog.Info("loaded git remote metadata", "cache_hit", hit, "cache_key", cacheKey)
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
// local mirror of the remote. Remote repositories are resolved via ls-remote,
// which only advertises refs: a prefix can only be expanded when the commit's
// objects were already fetched (e.g. by a previous tree checkout). Nothing is
// fetched to answer the expansion.
func (repo *RemoteGitRepository) ResolveShortSHA(ctx context.Context, prefix string) (string, error) {
	var sha string
	err := repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
		var err error
		sha, err = git.ResolveShortSHA(ctx, prefix)
		return err
	})
	if err != nil {
		if errors.Is(err, gitutil.ErrShortSHANotFound) {
			return "", fmt.Errorf("%w; a remote repository can only expand an abbreviated SHA against already-fetched commits: use the full SHA or a named ref", err)
		}
		return "", err
	}
	return sha, nil
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

	if repo.AuthToken.Self() != nil {
		password, err := repo.AuthToken.Self().Plaintext(ctx)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, gitutil.WithHTTPTokenAuth(repo.URL, string(password), repo.AuthUsername))
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

func (repo *RemoteGitRepository) mount(ctx context.Context, depth int, includeTags bool, refs []GitRefBackend, fn func(*gitutil.GitCLI) error) (retErr error) {
	return repo.initRemote(ctx, func(remote string) (rerr error) {
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
		err = repo.fetch(ctx, git, depth, includeTags, fetchRefs)
		if err != nil {
			return err
		}
		_, err = git.Run(ctx, "reflog", "expire", "--all", "--expire=now")
		if err != nil {
			return fmt.Errorf("failed to expire reflog for remote %s: %w", repo.URL.Remote(), err)
		}

		return fn(git)
	})
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

func (repo *RemoteGitRepository) fetch(ctx context.Context, git *gitutil.GitCLI, depth int, includeTags bool, refs []*RemoteGitRef) (rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}

	if len(refs) == 0 && !includeTags {
		// Nothing requested: avoid an implicit broad fetch from origin.
		return nil
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

	return repo.fetchObjects(ctx, git, depth, includeTags, refs)
}

// fetchObjects operates on the private mirror; service bindings and progress
// streams are supplied by fetch so the Git protocol can also be tested directly.
func (repo *RemoteGitRepository) fetchObjects(ctx context.Context, git *gitutil.GitCLI, depth int, includeTags bool, refs []*RemoteGitRef) error {
	if len(refs) == 0 && !includeTags {
		return nil
	}

	// Fetch by object SHA in the hot path (`--no-tags`), and only retry by named refs for SHA-incompatible remotes.
	logger := slog.SpanLogger(ctx, InstrumentationLibrary)

	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return err
	}

	depth, err = mirrorFetchDepth(ctx, git, gitDir, depth)
	if err != nil {
		return err
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
			if !errors.Is(err, gitutil.ErrSHAFetchUnsupported) {
				return fmt.Errorf("failed to fetch remote %s: %w", repo.URL.Remote(), err)
			}

			namedSpecs := namedFetchRefSpecs(refs)
			if len(namedSpecs) == 0 {
				return fmt.Errorf("failed to fetch remote %s: %w", repo.URL.Remote(), err)
			}
			defer cleanupScratchFetchRefs(namedSpecs)

			logger.Debug("git fetch by sha failed; retrying with named refs", "remote", repo.URL.Remote(), "refspec_count", len(namedSpecs))
			if retryErr := runFetch(namedSpecs); retryErr != nil {
				return fmt.Errorf("failed to fetch remote %s: sha fetch failed: %w; named-ref retry failed: %w", repo.URL.Remote(), err, retryErr)
			}
			if verifyErr := verifyFetchedSHAs(refs); verifyErr != nil {
				return fmt.Errorf("failed to fetch remote %s: named-ref retry verification failed: %w", repo.URL.Remote(), verifyErr)
			}
			// The named ref may have moved since resolution. Retain the verified
			// pinned SHA, not the scratch ref's potentially newer target.
			for _, ref := range refs {
				if _, pinErr := git.Run(ctx, "update-ref", fetchedGitRef(ref.SHA), ref.SHA); pinErr != nil {
					return fmt.Errorf("failed to retain fetched sha %s: %w", ref.SHA, pinErr)
				}
			}
			logger.Debug("git fetch named-ref retry succeeded", "remote", repo.URL.Remote(), "refspec_count", len(namedSpecs))
		}
	}

	if includeTags {
		if tagErr := runFetchTags(); tagErr != nil {
			return fmt.Errorf("failed to hydrate tags for remote %s: %w", repo.URL.Remote(), tagErr)
		}
	}

	return nil
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

func (repo *RemoteGitRepository) initRemote(ctx context.Context, fn func(string) error) (retErr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	locker := query.Locker()
	lockKey := remoteGitLockPrefix + repo.URL.Remote()
	var profWait *wcprof.Wait
	if wcprof.Enabled(ctx) {
		// Profiles are dumped and shared: identify the lock without the
		// URL's userinfo, which may carry credentials.
		profWait = wcprof.BeginWaitIdent(ctx, remoteGitLockPrefix+repo.URL.RedactedRemote(), wcprof.WaitReasonLock)
	}
	locker.Lock(lockKey)
	profWait.End()
	defer locker.Unlock(lockKey)

	if repo.Mirror.Self() == nil {
		return fmt.Errorf("remote git mirror is nil for %s", repo.URL.Remote())
	}
	if err := EnsureBackingSnapshot(ctx, repo.Mirror); err != nil {
		return err
	}
	remoteRef, releaseMirror, err := repo.Mirror.Self().acquire(ctx, query)
	if err != nil {
		return err
	}
	defer releaseMirror()

	mount, err := remoteRef.Mount(ctx, false)
	if err != nil {
		return err
	}

	lm := bkcache.LocalMounter(mount)
	dir, err := lm.Mount()
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
	}

	return fn(dir)
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

	err = ref.mount(ctx, depth, includeTags, func(git *gitutil.GitCLI) error {
		gitURL, err := git.URL(ctx)
		if err != nil {
			return fmt.Errorf("could not find git dir: %w", err)
		}

		checkoutRef, err = cache.New(ctx, nil,
			bkcache.WithRecordType(bkclient.UsageRecordTypeGitCheckout),
			bkcache.WithDescription(fmt.Sprintf("git checkout for %s (%s %s)", ref.repo.URL.Remote(), ref.Name, ref.SHA)))
		if err != nil {
			return err
		}

		err = MountRef(ctx, checkoutRef, func(checkoutDir string, _ *ctdmount.Mount) error {
			checkoutDirGit := filepath.Join(checkoutDir, ".git")
			if err := os.MkdirAll(checkoutDir, 0711); err != nil {
				return err
			}
			checkoutGit := git.New(gitutil.WithWorkTree(checkoutDir), gitutil.WithGitDir(checkoutDirGit))

			// The clone URL is the remote itself, so it doubles as the
			// checkout's origin; registered remotes overlay it.
			checkoutRemotes := MergeGitRemotes(
				[]GitRemote{{Name: "origin", URL: ref.repo.URL.Remote(), Implicit: true}},
				remotes,
			)
			if upstreamRemote != nil {
				checkoutRemotes = MergeGitRemotes(nil, remotes)
			}
			if err := doGitCheckout(ctx, checkoutGit, checkoutRemotes, gitURL, ref.Ref, depth, discardGitDir); err != nil {
				return err
			}
			if !discardGitDir && upstreamRemote != nil {
				return writeGitRemoteSelection(ctx, checkoutGit, checkoutRemotes, *upstreamRemote)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to checkout %s in %s: %w", ref.Name, ref.repo.URL.Remote(), err)
		}

		return nil
	})
	if err != nil {
		return nil, err
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

func (ref *RemoteGitRef) mount(ctx context.Context, depth int, includeTags bool, fn func(*gitutil.GitCLI) error) error {
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
