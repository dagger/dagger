package core

import (
	"bufio"
	"bytes"
	"context"
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

	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/moby/sys/mount"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/network"
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
}

var _ GitRepositoryBackend = (*RemoteGitRepository)(nil)

type RemoteGitRef struct {
	*gitutil.Ref
	repo *RemoteGitRepository
}

var _ GitRefBackend = (*RemoteGitRef)(nil)

func (repo *RemoteGitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return nil, nil
}

func (repo *RemoteGitRepository) Remote(ctx context.Context) (*gitutil.Remote, error) {
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

	out, err := git.LsRemote(ctx, repo.URL.Remote())
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (repo *RemoteGitRepository) Get(ctx context.Context, target *gitutil.Ref) (GitRefBackend, error) {
	return &RemoteGitRef{
		repo: repo,
		Ref:  target,
	}, nil
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
		socketStore, err := query.Sockets(ctx)
		if err == nil {
			sockpath, cleanup, err := socketStore.MountSocket(ctx, repo.SSHAuthSocket.Self().IDDigest)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to mount SSH socket: %w", err)
			}
			opts = append(opts, gitutil.WithSSHAuthSock(sockpath))
			cleanups.Add("cleanup SSH socket", cleanup)
		}
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
		secretStore, err := query.Secrets(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get secret store: %w", err)
		}
		password, err := secretStore.GetSecretPlaintext(ctx, repo.AuthToken.ID().Digest())
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, gitutil.WithHTTPTokenAuth(repo.URL, string(password), repo.AuthUsername))
	} else if repo.AuthHeader.Self() != nil {
		secretStore, err := query.Secrets(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get secret store: %w", err)
		}
		byteAuthHeader, err := secretStore.GetSecretPlaintext(ctx, repo.AuthHeader.ID().Digest())
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, gitutil.WithHTTPAuthorizationHeader(repo.URL, string(byteAuthHeader)))
	}

	opts = append(opts, gitutil.WithExec(func(ctx context.Context, cmd *exec.Cmd) error {
		return runWithStandardUmaskAndNetOverride(ctx, cmd, "", resolvPath)
	}))

	return gitutil.NewGitCLI(opts...), cleanups.Run, nil
}

func (repo *RemoteGitRepository) mount(ctx context.Context, depth int, refs []GitRefBackend, fn func(*gitutil.GitCLI) error) (retErr error) {
	g, _ := buildkit.CurrentBuildkitSessionGroup(ctx)
	return repo.initRemote(ctx, g, func(remote string) error {
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

		err = repo.fetch(ctx, git, depth, fetchRefs)
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

func (repo *RemoteGitRepository) fetch(ctx context.Context, git *gitutil.GitCLI, depth int, refs []*RemoteGitRef) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}

	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return err
	}

	var refSpecs []string
	for _, ref := range refs {
		// fetch by sha, since we've already done tag resolution
		// TODO: may need fallback if git remote doesn't support fetching by commit
		refSpecs = append(refSpecs, ref.SHA)
	}

	args := []string{
		"fetch",
		"--tags",
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

	svcs, err := query.Services(ctx)
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Services)
	if err != nil {
		return err
	}
	defer detach()

	if _, err := git.Run(ctx, args...); err != nil {
		if errors.Is(err, gitutil.ErrShallowNotSupported) {
			// fallback to full fetch
			args = slices.DeleteFunc(args, func(s string) bool {
				return strings.HasPrefix(s, "--depth")
			})
			_, err = git.Run(ctx, args...)
		}

		if err != nil {
			return fmt.Errorf("failed to fetch remote %s: %w", repo.URL.Remote(), err)
		}
	}

	return nil
}

func (repo *RemoteGitRepository) initRemote(ctx context.Context, g bksession.Group, fn func(string) error) (retErr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	locker := query.Locker()
	locker.Lock(indexGitRemote + repo.URL.Remote())
	defer locker.Unlock(indexGitRemote + repo.URL.Remote())

	cache := query.BuildkitCache()

	sis, err := searchGitRemote(ctx, cache, repo.URL.Remote())
	if err != nil {
		return fmt.Errorf("failed to search metadata for %s: %w", repo.URL.Remote(), err)
	}

	var remoteRef bkcache.MutableRef
	for _, si := range sis {
		remoteRef, err = cache.GetMutable(ctx, si.ID())
		if err != nil {
			if errors.Is(err, bkcache.ErrLocked) {
				// should never really happen as no other function should access this metadata, but lets be graceful
				slog.Warn("mutable ref for %s  %s was locked: %v", repo.URL.Remote(), si.ID(), err)
				continue
			}
			return fmt.Errorf("failed to get mutable ref for %s: %w", repo.URL.Remote(), err)
		}
		break
	}

	initializeRepo := false
	if remoteRef == nil {
		remoteRef, err = cache.New(ctx, nil, g,
			bkcache.CachePolicyRetain,
			bkcache.WithDescription(fmt.Sprintf("shared git repo for %s", repo.URL.Remote())))
		if err != nil {
			return fmt.Errorf("failed to create new mutable for %s: %w", repo.URL.Remote(), err)
		}
		initializeRepo = true
	}
	defer func() {
		err := remoteRef.Release(context.WithoutCancel(ctx))
		if retErr == nil {
			retErr = err
		}
	}()

	mount, err := remoteRef.Mount(ctx, false, g)
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

	if initializeRepo {
		// Explicitly set the Git config 'init.defaultBranch' to the
		// implied default to suppress "hint:" output about not having a
		// test logs.
		// default initial branch name set which otherwise spams unit
		if _, err := git.Run(ctx, "-c", "init.defaultBranch=main", "init", "--bare", "--quiet"); err != nil {
			return fmt.Errorf("failed to init repo at %s: %w", dir, err)
		}

		if _, err := git.Run(ctx, "remote", "add", "origin", repo.URL.Remote()); err != nil {
			return fmt.Errorf("failed add origin repo at %s: %w", dir, err)
		}

		// save new remote metadata
		md := cacheRefMetadata{remoteRef}
		if err := md.setGitRemote(repo.URL.Remote()); err != nil {
			return err
		}
	}

	return fn(dir)
}

func (ref *RemoteGitRef) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return nil, nil
}

func (ref *RemoteGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (_ *Directory, rerr error) {
	cacheKey := dagql.CurrentID(ctx).Digest().Encoded()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	cache := query.BuildkitCache()

	locker := query.Locker()
	locker.Lock(indexGitSnapshot + cacheKey)
	defer locker.Unlock(indexGitSnapshot + cacheKey)
	sis, err := searchGitSnapshot(ctx, cache, cacheKey)
	if err != nil {
		return nil, fmt.Errorf("failed to search metadata for %s: %w", cacheKey, err)
	}
	if len(sis) > 0 {
		res, err := cache.Get(ctx, sis[0].ID(), nil)
		if err != nil {
			return nil, err
		}
		checkout := NewDirectory(nil, "/", query.Platform(), nil)
		checkout.Result = res
		return checkout, nil
	}

	var checkoutRef bkcache.MutableRef
	defer func() {
		if rerr != nil && checkoutRef != nil {
			checkoutRef.Release(context.WithoutCancel(ctx))
		}
	}()

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}
	err = ref.mount(ctx, depth, func(git *gitutil.GitCLI) error {
		gitURL, err := git.URL(ctx)
		if err != nil {
			return fmt.Errorf("could not find git dir: %w", err)
		}

		checkoutRef, err = cache.New(ctx, nil, bkSessionGroup,
			bkcache.CachePolicyRetain,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription(fmt.Sprintf("git checkout for %s (%s %s)", ref.repo.URL.Remote(), ref.Name, ref.SHA)))
		if err != nil {
			return err
		}

		err = MountRef(ctx, checkoutRef, bkSessionGroup, func(checkoutDir string) error {
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

	md := cacheRefMetadata{snap}
	if err := md.setGitSnapshot(cacheKey); err != nil {
		return nil, err
	}

	checkout := NewDirectory(nil, "/", query.Platform(), nil)
	checkout.Result = snap
	return checkout, nil
}

func (ref *RemoteGitRef) mount(ctx context.Context, depth int, fn func(*gitutil.GitCLI) error) error {
	return ref.repo.mount(ctx, depth, []GitRefBackend{ref}, fn)
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

func runWithStandardUmaskAndNetOverride(ctx context.Context, cmd *exec.Cmd, hosts, resolv string) error {
	errCh := make(chan error)

	go func() {
		defer close(errCh)
		runtime.LockOSThread()

		if err := unshareAndRun(ctx, cmd, hosts, resolv); err != nil {
			errCh <- err
		}
	}()

	return <-errCh
}

// unshareAndRun needs to be called in a locked thread.
func unshareAndRun(ctx context.Context, cmd *exec.Cmd, hosts, resolv string) error {
	if err := syscall.Unshare(syscall.CLONE_FS | syscall.CLONE_NEWNS); err != nil {
		return err
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
