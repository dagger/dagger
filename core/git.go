package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/util/gitutil"
	bkcache "github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/sys/mount"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/network"
)

type GitRepository struct {
	Query   *Query
	Backend GitRepositoryBackend

	DiscardGitDir bool
}

type GitRepositoryBackend interface {
	HasPBDefinitions

	Ref(ctx context.Context, ref string) (GitRefBackend, error)
	Tags(ctx context.Context, patterns []string) (tags []string, err error)
	Branches(ctx context.Context, patterns []string) (branches []string, err error)
}

func (*GitRepository) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRepository",
		NonNull:   true,
	}
}

func (*GitRepository) TypeDescription() string {
	return "A git repository."
}

func (repo *GitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return repo.Backend.PBDefinitions(ctx)
}

func (repo *GitRepository) Ref(ctx context.Context, name string) (*GitRef, error) {
	ref, err := repo.Backend.Ref(ctx, name)
	if err != nil {
		return nil, err
	}
	return &GitRef{repo, ref}, nil
}

func (repo *GitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	return repo.Backend.Tags(ctx, patterns)
}

func (repo *GitRepository) Branches(ctx context.Context, patterns []string) ([]string, error) {
	return repo.Backend.Branches(ctx, patterns)
}

type GitRef struct {
	Repo    *GitRepository
	Backend GitRefBackend
}

type GitRefBackend interface {
	HasPBDefinitions

	Resolve(ctx context.Context) (commit string, ref string, err error)
	Tree(ctx context.Context, srv *dagql.Server, discard bool, depth int) (checkout *Directory, err error)
}

func (*GitRef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRef",
		NonNull:   true,
	}
}

func (*GitRef) TypeDescription() string {
	return "A git ref (tag, branch, or commit)."
}

func (ref *GitRef) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return ref.Backend.PBDefinitions(ctx)
}

func (ref *GitRef) Resolve(ctx context.Context) (string, string, error) {
	return ref.Backend.Resolve(ctx)
}

func (ref *GitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (*Directory, error) {
	return ref.Backend.Tree(ctx, srv, ref.Repo.DiscardGitDir || discardGitDir, depth)
}

type RemoteGitRepository struct {
	Query *Query

	URL *gitutil.GitURL

	SSHKnownHosts string
	SSHAuthSocket *Socket

	Services ServiceBindings
	Platform Platform

	AuthToken  dagql.Instance[*Secret]
	AuthHeader dagql.Instance[*Secret]
}

var _ GitRepositoryBackend = (*RemoteGitRepository)(nil)

func (repo *RemoteGitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return nil, nil
}

func (repo *RemoteGitRepository) Ref(ctx context.Context, refstr string) (GitRefBackend, error) {
	ref := &RemoteGitRef{
		Query: repo.Query,
		Repo:  repo,
	}

	// force resolution now, since the remote might change, and we don't want inconsistencies
	var err error
	ref.Commit, ref.FullRef, err = ref.resolve(ctx, refstr)
	if err != nil {
		return nil, err
	}

	return ref, nil
}

func (repo *RemoteGitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	tags, err := repo.lsRemote(ctx, []string{"--tags"}, patterns)
	if err != nil {
		return nil, err
	}
	for i, tag := range tags {
		tags[i] = strings.TrimPrefix(tag, "refs/tags/")
	}
	return tags, nil
}

func (repo *RemoteGitRepository) Branches(ctx context.Context, patterns []string) ([]string, error) {
	branches, err := repo.lsRemote(ctx, []string{"--heads"}, patterns)
	if err != nil {
		return nil, err
	}
	for i, branch := range branches {
		branches[i] = strings.TrimPrefix(branch, "refs/heads/")
	}
	return branches, nil
}

func (repo *RemoteGitRepository) lsRemote(ctx context.Context, args []string, patterns []string) ([]string, error) {
	svcs, err := repo.Query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	queryArgs := []string{
		"ls-remote",
		"--refs", // we don't want to include ^{} entries for annotated tags
	}
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, repo.URL.Remote())
	if len(patterns) > 0 {
		queryArgs = append(queryArgs, "--")
		queryArgs = append(queryArgs, patterns...)
	}
	git, cleanup, err := repo.setup(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	out, err := git.Run(ctx, queryArgs...)
	if err != nil {
		return nil, err
	}

	results := []string{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		results = append(results, fields[1])
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning git output: %w", err)
	}

	return results, nil
}

func (repo *RemoteGitRepository) setup(ctx context.Context) (_ *gitutil.GitCLI, _ func() error, rerr error) {
	var opts []gitutil.Option

	cleanups := buildkit.Cleanups{}
	defer func() {
		if rerr != nil {
			cleanups.Run()
		}
	}()

	if repo.AuthToken.Self != nil {
		username := "x-access-token"
		if repo.URL.Host == "bitbucket.org" {
			// NOTE: bitbucket.org is picky, and needs *this* username
			username = "x-token-auth"
		}

		secretStore, err := repo.Query.Secrets(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get secret store: %w", err)
		}
		password, err := secretStore.GetSecretPlaintext(ctx, repo.AuthToken.ID().Digest())
		if err != nil {
			return nil, nil, err
		}
		authHeader := "basic " + base64.StdEncoding.EncodeToString(
			fmt.Appendf(nil, "%s:%s", username, password),
		)
		opts = append(opts, gitutil.WithArgs(
			"-c", "http."+repo.URL.Remote()+".extraheader=Authorization: "+authHeader,
		))
	} else if repo.AuthHeader.Self != nil {
		secretStore, err := repo.Query.Secrets(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get secret store: %w", err)
		}
		authHeader, err := secretStore.GetSecretPlaintext(ctx, repo.AuthHeader.ID().Digest())
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, gitutil.WithArgs(
			"-c", "http."+repo.URL.Remote()+".extraheader=Authorization: "+string(authHeader),
		))
	}

	if repo.SSHAuthSocket != nil {
		socketStore, err := repo.Query.Sockets(ctx)
		if err == nil {
			sockpath, cleanup, err := socketStore.MountSocket(ctx, repo.SSHAuthSocket.IDDigest)
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

	netConf, err := DNSConfig(ctx, repo.Query)
	if err != nil {
		return nil, nil, err
	}
	if netConf != nil {
		resolvPath, err := mountResolv(netConf)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, gitutil.WithExec(func(ctx context.Context, cmd *exec.Cmd) error {
			return runWithStandardUmaskAndNetOverride(ctx, cmd, "", resolvPath)
		}))
		cleanups.Add("remove updated /etc/resolv", func() error {
			return os.Remove(resolvPath)
		})
	}

	return gitutil.NewGitCLI(opts...), cleanups.Run, nil
}

func DNSConfig(ctx context.Context, query *Query) (*oci.DNSConfig, error) {
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

func (repo *RemoteGitRepository) mountRemote(ctx context.Context, dagop FSDagOp, fn func(string) error) (retErr error) {
	locker := repo.Query.Locker()
	locker.Lock(indexGitRemote + repo.URL.Remote())
	defer locker.Unlock(indexGitRemote + repo.URL.Remote())

	cache := repo.Query.BuildkitCache()

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
		remoteRef, err = cache.New(ctx, nil, dagop.Group(),
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

	mount, err := remoteRef.Mount(ctx, false, dagop.g)
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

type RemoteGitRef struct {
	Query *Query

	Repo *RemoteGitRepository

	FullRef string
	Commit  string
}

var _ GitRefBackend = (*RemoteGitRef)(nil)

func (ref *RemoteGitRef) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return nil, nil
}

func (ref *RemoteGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (_ *Directory, rerr error) {
	op, ok := DagOpFromContext[FSDagOp](ctx)
	if !ok {
		return nil, fmt.Errorf("no dagop")
	}
	cacheKey := dagql.CurrentID(ctx).Digest().Encoded()
	cache := ref.Query.BuildkitCache()

	locker := ref.Query.Locker()
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
		checkout := NewDirectory(ref.Query, nil, "/", ref.Query.Platform(), nil)
		checkout.Result = res
		return checkout, nil
	}

	var checkoutRef bkcache.MutableRef
	defer func() {
		if rerr != nil && checkoutRef != nil {
			checkoutRef.Release(context.WithoutCancel(ctx))
		}
	}()

	err = ref.Repo.mountRemote(ctx, op, func(remote string) error {
		git, cleanup, err := ref.Repo.setup(ctx)
		if err != nil {
			return err
		}
		defer cleanup()
		git = git.New(gitutil.WithGitDir(remote))
		gitDir, err := git.GitDir(ctx)
		if err != nil {
			return fmt.Errorf("could not find git dir: %w", err)
		}

		// skip fetch if commit already exists
		doFetch := true
		if res, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "rev-parse", "--verify", ref.FullRef+"^{commit}"); err != nil {
			return fmt.Errorf("failed to rev-parse: %w", err)
		} else if strings.TrimSpace(string(res)) == ref.Commit {
			doFetch = false
		}

		if doFetch {
			err := ref.fetchRemote(ctx, git, depth)
			if err != nil {
				return err
			}
			_, err = git.Run(ctx, "reflog", "expire", "--all", "--expire=now")
			if err != nil {
				return fmt.Errorf("failed to expire reflog for remote %s: %w", ref.Repo.URL.Remote(), err)
			}
		}

		checkoutRef, err = cache.New(ctx, nil, op.Group(),
			bkcache.CachePolicyRetain,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription(fmt.Sprintf("git checkout for %s (%s %s)", ref.Repo.URL.Remote(), ref.FullRef, ref.Commit)))
		if err != nil {
			return err
		}

		err = MountRef(ctx, checkoutRef, op.Group(), func(checkoutDir string) error {
			checkoutDirGit := filepath.Join(checkoutDir, ".git")
			if err := os.MkdirAll(checkoutDir, 0711); err != nil {
				return err
			}
			checkoutGit := git.New(gitutil.WithWorkTree(checkoutDir), gitutil.WithGitDir(checkoutDirGit))

			_, err = checkoutGit.Run(ctx, "-c", "init.defaultBranch=main", "init")
			if err != nil {
				return err
			}
			_, err = checkoutGit.Run(ctx, "remote", "add", "origin", "file://"+gitDir)
			if err != nil {
				return err
			}

			return doGitCheckout(ctx, checkoutGit, ref.Repo.URL.Remote(), ref.FullRef, ref.Commit, depth, discardGitDir)
		})
		if err != nil {
			return fmt.Errorf("failed to checkout %s in %s: %w", ref.FullRef, ref.Repo.URL.Remote(), err)
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

	checkout := NewDirectory(ref.Query, nil, "/", ref.Query.Platform(), nil)
	checkout.Result = snap
	return checkout, nil
}

func (ref *RemoteGitRef) fetchRemote(ctx context.Context, git *gitutil.GitCLI, depth int) error {
	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return err
	}

	var refSpec string
	if gitutil.IsCommitSHA(ref.FullRef) {
		// TODO: may need fallback if git remote doesn't support fetching by commit
		refSpec = ref.FullRef
	} else {
		refSpec = ref.FullRef + ":" + ref.FullRef
	}

	args := []string{
		"fetch",
		"--tags",
		"--update-head-ok",
		"--force",
	}
	if depth == 0 {
		args = append(args, "--depth="+fmt.Sprint(depth))
	} else {
		if _, err := os.Lstat(filepath.Join(gitDir, "shallow")); err == nil {
			args = append(args, "--unshallow")
		}
	}
	args = append(args, "origin", refSpec)

	svcs, err := ref.Repo.Query.Services(ctx)
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, ref.Repo.Services)
	if err != nil {
		return err
	}
	defer detach()

	if _, err := git.Run(ctx, args...); err != nil {
		if strings.Contains(err.Error(), "does not support shallow") {
			// fallback to full fetch
			args = slices.DeleteFunc(args, func(s string) bool {
				return strings.HasPrefix(s, "--depth")
			})
			_, err = git.Run(ctx, args...)
		}

		if err != nil {
			return fmt.Errorf("failed to fetch remote %s: %w", ref.Repo.URL.Remote(), err)
		}
	}

	return nil
}

func doGitCheckout(
	ctx context.Context,
	checkoutGit *gitutil.GitCLI,
	remote string,
	fullref string, commit string,
	depth int,
	discardGitDir bool,
) error {
	checkoutDirGit, err := checkoutGit.GitDir(ctx)
	if err != nil {
		return fmt.Errorf("could not find git dir: %w", err)
	}

	pullref := fullref
	if !gitutil.IsCommitSHA(fullref) {
		pullref += ":" + pullref
	}

	args := []string{"fetch", "-u"}
	if depth != 0 {
		args = append(args, fmt.Sprintf("--depth=%d", depth))
	}
	args = append(args, "origin", pullref)
	_, err = checkoutGit.Run(ctx, args...)
	if err != nil {
		return err
	}
	_, err = checkoutGit.Run(ctx, "checkout", strings.TrimPrefix(fullref, "refs/heads/"))
	if err != nil {
		return fmt.Errorf("failed to checkout remote %s: %w", remote, err)
	}
	_, err = checkoutGit.Run(ctx, "reset", "--hard", commit)
	if err != nil {
		return fmt.Errorf("failed to reset ref %s: %w", remote, err)
	}
	_, err = checkoutGit.Run(ctx, "remote", "set-url", "origin", remote)
	if err != nil {
		return fmt.Errorf("failed to set remote origin to %s: %w", remote, err)
	}
	_, err = checkoutGit.Run(ctx, "reflog", "expire", "--all", "--expire=now")
	if err != nil {
		return fmt.Errorf("failed to expire reflog for remote %s: %w", remote, err)
	}

	if err := os.Remove(filepath.Join(checkoutDirGit, "FETCH_HEAD")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove FETCH_HEAD for remote %s: %w", remote, err)
	}

	// TODO: this feels completely out-of-sync from how we do the rest
	// of the clone - caching will not be as great here
	_, err = checkoutGit.Run(ctx, "submodule", "update", "--init", "--recursive", "--depth=1")
	if err != nil {
		return fmt.Errorf("failed to update submodules for %s: %w", remote, err)
	}

	if discardGitDir {
		if err := os.RemoveAll(checkoutDirGit); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove .git for remote %s: %w", remote, err)
		}
	}

	return nil
}

func (ref *RemoteGitRef) resolve(ctx context.Context, refstr string) (commit string, fullref string, err error) {
	svcs, err := ref.Repo.Query.Services(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, ref.Repo.Services)
	if err != nil {
		return "", "", err
	}
	defer detach()

	git, cleanup, err := ref.Repo.setup(ctx)
	if err != nil {
		return "", "", err
	}
	defer cleanup()

	target := refstr
	if gitutil.IsCommitSHA(refstr) {
		// even when we already know the commit, we should still access the
		// remote ref, to confirm it's actually real
		target = "HEAD"
	}

	out, err := git.Run(ctx,
		"ls-remote",
		"--symref",
		ref.Repo.URL.Remote(),
		target,
		target+"^{}",
	)
	if err != nil {
		return "", "", fmt.Errorf("cannot resolve %q: %w", ref.Repo.URL.Remote(), err)
	}

	if gitutil.IsCommitSHA(refstr) {
		return refstr, refstr, nil
	}

	return parseGitRefOutput(refstr, string(out), "\t")
}

func (ref *RemoteGitRef) Resolve(ctx context.Context) (commit string, fullref string, _ error) {
	return ref.Commit, ref.FullRef, nil
}

type LocalGitRepository struct {
	Query *Query

	Directory *Directory
}

var _ GitRepositoryBackend = (*LocalGitRepository)(nil)

func (repo *LocalGitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return repo.Directory.PBDefinitions(ctx)
}

func (repo *LocalGitRepository) Ref(ctx context.Context, ref string) (GitRefBackend, error) {
	return &LocalGitRef{
		Query: repo.Query,
		Repo:  repo,
		Ref:   ref,
	}, nil
}

func (repo *LocalGitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	tags, err := repo.lsRemote(ctx, []string{"--tags"}, patterns)
	if err != nil {
		return nil, err
	}
	for i, tag := range tags {
		tags[i] = strings.TrimPrefix(tag, "refs/tags/")
	}
	return tags, nil
}

func (repo *LocalGitRepository) Branches(ctx context.Context, patterns []string) ([]string, error) {
	branches, err := repo.lsRemote(ctx, []string{"--heads"}, patterns)
	if err != nil {
		return nil, err
	}
	for i, branch := range branches {
		branches[i] = strings.TrimPrefix(branch, "refs/heads/")
	}
	return branches, nil
}

func (repo *LocalGitRepository) lsRemote(ctx context.Context, args []string, patterns []string) ([]string, error) {
	results := []string{}
	err := repo.mount(ctx, func(src string) error {
		queryArgs := []string{
			"ls-remote",
			"--refs", // we don't want to include ^{} entries for annotated tags
		}
		queryArgs = append(queryArgs, args...)
		queryArgs = append(queryArgs, "file://"+src)
		if len(patterns) > 0 {
			queryArgs = append(queryArgs, "--")
			queryArgs = append(queryArgs, patterns...)
		}
		git := gitutil.NewGitCLI()
		out, err := git.Run(ctx, queryArgs...)
		if err != nil {
			return err
		}

		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}

			results = append(results, fields[1])
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("error scanning git output: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (repo *LocalGitRepository) mount(ctx context.Context, f func(string) error) error {
	svcs, err := repo.Query.Services(ctx)
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Directory.Services)
	if err != nil {
		return err
	}
	defer detach()

	return repo.Directory.mount(ctx, func(root string) error {
		src, err := fs.RootPath(root, repo.Directory.Dir)
		if err != nil {
			return err
		}
		return f(src)
	})
}

type LocalGitRef struct {
	Query *Query

	Repo *LocalGitRepository
	Ref  string
}

var _ GitRefBackend = (*LocalGitRef)(nil)

func (ref *LocalGitRef) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return ref.Repo.PBDefinitions(ctx)
}

func (ref *LocalGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (_ *Directory, rerr error) {
	op, ok := DagOpFromContext[FSDagOp](ctx)
	if !ok {
		return nil, fmt.Errorf("no dagop")
	}

	cache := ref.Query.BuildkitCache()

	commit, fullref, err := ref.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	bkref, err := cache.New(ctx, nil, op.Group(),
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("git local checkout (%s %s)", fullref, commit)))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = ref.Repo.mount(ctx, func(src string) error {
		git := gitutil.NewGitCLI(gitutil.WithDir(src))
		gitDir, err := git.GitDir(ctx)
		if err != nil {
			return fmt.Errorf("could not find git dir: %w", err)
		}

		return MountRef(ctx, bkref, op.Group(), func(checkoutDir string) error {
			checkoutDirGit := filepath.Join(checkoutDir, ".git")
			if err := os.MkdirAll(checkoutDir, 0711); err != nil {
				return err
			}
			checkoutGit := git.New(
				gitutil.WithDir(checkoutDir),
				gitutil.WithWorkTree(checkoutDir),
				gitutil.WithGitDir(checkoutDirGit),
			)

			_, err = checkoutGit.Run(ctx, "-c", "init.defaultBranch=main", "init")
			if err != nil {
				return err
			}
			_, err = checkoutGit.Run(ctx, "remote", "add", "origin", "file://"+gitDir)
			if err != nil {
				return err
			}

			return doGitCheckout(ctx, checkoutGit, "file://"+gitDir, fullref, commit, depth, discardGitDir)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to checkout %s: %w", fullref, err)
	}

	dir := NewDirectory(ref.Query, nil, "/", ref.Query.Platform(), nil)
	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	dir.Result = snap
	return dir, nil
}

func (ref *LocalGitRef) Resolve(ctx context.Context) (string, string, error) {
	if gitutil.IsCommitSHA(ref.Ref) {
		return ref.Ref, ref.Ref, nil
	}

	var commit, fullref string
	err := ref.Repo.mount(ctx, func(src string) error {
		git := gitutil.NewGitCLI(gitutil.WithDir(src))

		targetref := ref.Ref
		out, err := git.Run(ctx, "symbolic-ref", targetref)
		if err == nil {
			targetref = strings.TrimSpace(string(out))
		} else if !strings.Contains(err.Error(), "is not a symbolic ref") {
			return err
		}
		out, err = git.Run(ctx, "show-ref", "--deref", "--head", targetref, targetref+"^{}")
		if err != nil {
			return err
		}
		commit, fullref, err = parseGitRefOutput(targetref, string(out), " ")
		return err
	})
	if err != nil {
		return "", "", err
	}
	return commit, fullref, nil
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

// parses output from git-show-ref and git-ls-remote to find the correctly
// matching ref and commit for a target
func parseGitRefOutput(target string, out string, separator string) (commit string, ref string, err error) {
	lines := strings.Split(out, "\n")

	symrefs := make(map[string]string)

	// simulate git-checkout semantics, and make sure to select exactly the right ref
	var (
		partialRef      = "refs/" + strings.TrimPrefix(target, "refs/")
		headRef         = "refs/heads/" + strings.TrimPrefix(target, "refs/heads/")
		tagRef          = "refs/tags/" + strings.TrimPrefix(target, "refs/tags/")
		annotatedTagRef = tagRef + "^{}"
	)
	type reference struct {
		sha string
		ref string
	}
	var match, headMatch, tagMatch *reference

	for _, line := range lines {
		fields := strings.Split(line, separator)
		if len(fields) < 2 {
			continue
		}
		lineMatch := &reference{sha: fields[0], ref: fields[1]}

		if ref, ok := strings.CutPrefix(lineMatch.sha, "ref: "); ok {
			// this is a symref, record it for later
			symrefs[lineMatch.ref] = ref
			continue
		}

		switch lineMatch.ref {
		case headRef:
			headMatch = lineMatch
		case tagRef, annotatedTagRef:
			tagMatch = lineMatch
			tagMatch.ref = tagRef
		case partialRef:
			match = lineMatch
		case target:
			match = lineMatch
		}
	}
	// git-checkout prefers branches in case of ambiguity
	if match == nil {
		match = headMatch
	}
	if match == nil {
		match = tagMatch
	}
	if match == nil {
		return "", "", fmt.Errorf("repository does not contain ref %q, output: %q", target, out)
	}
	if !gitutil.IsCommitSHA(match.sha) {
		return "", "", fmt.Errorf("invalid commit sha %q for %q", match.sha, match.ref)
	}

	// resolve symrefs to get the right ref result
	if ref, ok := symrefs[match.ref]; ok {
		match.ref = ref
	}
	return match.sha, match.ref, nil
}
