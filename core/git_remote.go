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
	"syscall"
	"time"

	ctdmount "github.com/containerd/containerd/v2/core/mount"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	snapshot "github.com/dagger/dagger/engine/snapshots/snapshotter"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/moby/sys/mount"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
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

	cacheRes, err := cache.GetOrInitArbitrary(ctx, clientMetadata.SessionID, cacheKey, func(ctx context.Context) (any, error) {
		remote, err := repo.runLsRemote(ctx)
		if err != nil {
			return nil, err
		}

		slog.Info("caching git remote metadata", "cache_key", cacheKey)
		// Serialize to JSON so the cache can persist a plain string payload without
		// requiring gitutil.Remote to satisfy dagql's typing interfaces. The decode
		// step also hands each repo its own copy, so later tweaks (e.g. setting
		// repo.Remote.Head) stay scoped to that caller
		payload, err := json.Marshal(remote)
		if err != nil {
			return nil, err
		}

		return string(payload), nil
	})
	if err != nil {
		return nil, err
	}
	if cacheRes == nil {
		return nil, fmt.Errorf("git remote cache returned nil result for key %q", cacheKey)
	}

	slog.Info("loaded git remote metadata", "cache_hit", cacheRes.HitCache(), "cache_key", cacheKey)

	return remoteFromCacheResult(cacheRes.Value())
}

func remoteFromCacheResult(cacheRes any) (*gitutil.Remote, error) {
	payload, ok := cacheRes.(string)
	if !ok {
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

func (repo *RemoteGitRepository) remoteCacheKey(ctx context.Context) (string, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}
	inputs := []string{clientMetadata.SessionID, repo.URL.Remote()}
	inputs = append(inputs, repo.remoteCacheScope()...)
	return hashutil.HashStrings(inputs...).String(), nil
}

// Pipelines could query the same remote with different creds (e.g. a pipeline checking that creds were properly rotated)
// instead of being too smart, we just scope the cache key to the auth configuration: less chance of cache poisoning
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
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, nil, err
	}
	var opts []gitutil.Option

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
	return repo.initRemote(ctx, func(remote string) error {
		git, cleanup, err := repo.setup(ctx)
		if err != nil {
			return err
		}
		defer cleanup()
		git = git.New(gitutil.WithGitDir(remote))
		gitDir, err := git.GitDir(ctx)
		if err != nil {
			return fmt.Errorf("could not find git dir: %w", err)
		}

		var fetchRefs []*RemoteGitRef
		for _, ref := range refs {
			ref := ref.(*RemoteGitRef)

			// skip fetch if commit already exists
			doFetch := true
			if res, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "rev-parse", "--verify", ref.SHA+"^{commit}"); err != nil {
				return fmt.Errorf("failed to rev-parse: %w", err)
			} else if strings.TrimSpace(string(res)) == ref.SHA {
				doFetch = false

				if _, err := os.Lstat(filepath.Join(gitDir, "shallow")); err == nil {
					// if shallow, check we have enough depth
					if depth <= 0 {
						doFetch = true
					} else {
						// HACK: this is a pretty terrible way to guess the depth,
						// since it only traces *one* path.
						res, err := git.New().Run(ctx, "rev-list", "--first-parent", "--count", ref.SHA)
						if err != nil {
							return fmt.Errorf("failed to rev-list: %w", err)
						}
						res = bytes.TrimSpace(res)
						count, err := strconv.Atoi(string(res))
						if err != nil {
							return fmt.Errorf("failed to parse rev-list output: %w", err)
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

func (repo *RemoteGitRepository) fetch(ctx context.Context, git *gitutil.GitCLI, depth int, includeTags bool, refs []*RemoteGitRef) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}

	if len(refs) == 0 && !includeTags {
		// Nothing requested: avoid an implicit broad fetch from origin.
		return nil
	}

	// Fetch by object SHA in the hot path (`--no-tags`), and only retry by named refs for SHA-incompatible remotes.
	logger := slog.SpanLogger(ctx, InstrumentationLibrary)

	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return err
	}

	shaRefSpecs := make([]string, len(refs))
	for i, ref := range refs {
		// Default hot path: fetch exact objects by SHA; ref names are already resolved via ls-remote.
		shaRefSpecs[i] = ref.SHA
	}

	runFetch := func(refSpecs []string) error {
		args := []string{
			"fetch",
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

	svcs, err := query.Services(ctx)
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Services)
	if err != nil {
		return err
	}
	defer detach()

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
	locker.Lock(indexGitRemote + repo.URL.Remote())
	defer locker.Unlock(indexGitRemote + repo.URL.Remote())

	if repo.Mirror.Self() == nil {
		return fmt.Errorf("remote git mirror is nil for %s", repo.URL.Remote())
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

	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	if err != nil {
		return err
	}
	defer func() {
		err := lm.Unmount()
		if retErr == nil {
			retErr = err
		}
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

func (ref *RemoteGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	curCall := dagql.CurrentCall(ctx)
	if curCall == nil {
		return nil, fmt.Errorf("current call is nil")
	}

	inputs := []string{
		ref.Name,
		ref.SHA,
		ref.repo.URL.Remote(),
		fmt.Sprintf("discard git: %t", discardGitDir),
		fmt.Sprintf("depth: %d", depth),
		fmt.Sprintf("tags: %t", includeTags),
	}
	cacheKey := hashutil.HashStrings(inputs...).String()
	cache := query.SnapshotManager()
	locker := query.Locker()
	locker.Lock(indexGitSnapshot + cacheKey)
	defer locker.Unlock(indexGitSnapshot + cacheKey)
	sis, err := searchGitSnapshot(ctx, cache, cacheKey)
	if err != nil {
		return nil, fmt.Errorf("search git snapshot %s: %w", cacheKey, err)
	}
	if len(sis) > 0 {
		snap, err := cache.GetBySnapshotID(ctx, sis[0].SnapshotID(), bkcache.NoUpdateLastUsed)
		if err != nil {
			return nil, err
		}
		dir := &Directory{
			Platform: query.Platform(),
			Dir:      new(LazyAccessor[string, *Directory]),
			Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
		}
		dir.Dir.setValue("/")
		dir.Snapshot.setValue(snap)
		return dir, nil
	}

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
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
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

			return doGitCheckout(ctx, checkoutGit, ref.repo.URL.Remote(), gitURL, ref.Ref, depth, discardGitDir)
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
	mdRef, ok := any(snap).(bkcache.RefMetadata)
	if !ok {
		return nil, fmt.Errorf("git checkout cache metadata: unexpected ref type %T", snap)
	}
	md := cacheRefMetadata{mdRef}
	if err := md.setGitSnapshot(cacheKey); err != nil {
		return nil, err
	}
	dir := &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue("/")
	dir.Snapshot.setValue(snap)
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

func runProcessGroup(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &unix.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: unix.SIGTERM,
	}
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
