package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/continuity/fs"
	bkcache "github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/moby/buildkit/util/progress/logs"
	"github.com/moby/sys/mount"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
)

type GitRepository struct {
	Query   *Query
	Backend GitRepositoryBackend

	DiscardGitDir bool
}

type GitRepositoryBackend interface {
	HasPBDefinitions
	ServiceBindings() ServiceBindings

	Ref(ctx context.Context, ref string) (GitRefBackend, error)
	Tags(ctx context.Context, patterns []string) (tags []string, err error)
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

func (repo *GitRepository) UseDagOp() bool {
	return true
	// _, ok := repo.Backend.(*LocalGitRepository)
	// return ok
}

func (repo *GitRepository) Ref(ctx context.Context, name string) (*GitRef, error) {
	ref, err := repo.Backend.Ref(ctx, name)
	if err != nil {
		return nil, err
	}
	return &GitRef{repo, ref}, nil
}

func (repo *GitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	svcs, err := repo.Query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, repo.Backend.ServiceBindings())
	if err != nil {
		return nil, err
	}
	defer detach()

	return repo.Backend.Tags(ctx, patterns)
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

func (ref *GitRef) UseDagOp() bool {
	return true
	// _, ok := ref.Backend.(*LocalGitRef)
	// return ok
}

func (ref *GitRef) Resolve(ctx context.Context) (string, string, error) {
	svcs, err := ref.Repo.Query.Services(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, ref.Repo.Backend.ServiceBindings())
	if err != nil {
		return "", "", err
	}
	defer detach()

	return ref.Backend.Resolve(ctx)
}

func (ref *GitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int) (*Directory, error) {
	svcs, err := ref.Repo.Query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	detach, _, err := svcs.StartBindings(ctx, ref.Repo.Backend.ServiceBindings())
	if err != nil {
		return nil, err
	}
	defer detach()

	return ref.Backend.Tree(ctx, srv, ref.Repo.DiscardGitDir || discardGitDir, depth)
}

type RemoteGitRepository struct {
	Query *Query

	URL *gitutil.GitURL

	SSHKnownHosts string
	SSHAuthSocket *Socket

	Services ServiceBindings
	Platform Platform

	AuthToken  *Secret
	AuthHeader *Secret
}

var _ GitRepositoryBackend = (*RemoteGitRepository)(nil)

func (repo *RemoteGitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return nil, nil
}

func (repo *RemoteGitRepository) ServiceBindings() ServiceBindings {
	return repo.Services
}

func (repo *RemoteGitRepository) Ref(ctx context.Context, ref string) (GitRefBackend, error) {
	return &RemoteGitRef{
		Query: repo.Query,
		Repo:  repo,
		Ref:   ref,
	}, nil
}

func (repo *RemoteGitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	queryArgs := []string{
		"ls-remote",
		"--tags", // we only want tags
		"--refs", // we don't want to include ^{} entries for annotated tags
		repo.URL.Remote,
	}
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

	tags := []string{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		// this API is to fetch tags, not refs, so we can drop the `refs/tags/`
		// prefix
		tag := strings.TrimPrefix(fields[1], "refs/tags/")

		tags = append(tags, tag)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning git output: %w", err)
	}

	return tags, nil
}

func (repo *RemoteGitRepository) setup(ctx context.Context) (_ *gitutil.GitCLI, _ func() error, rerr error) {
	var opts []gitutil.Option

	cleanups := buildkit.Cleanups{}
	defer func() {
		if rerr != nil {
			cleanups.Run()
		}
	}()

	// XXX: handle bitbucket?
	// why does this need special handling?
	if repo.AuthToken != nil {
		secretStore, err := repo.Query.Secrets(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get secret store: %w", err)
		}
		username := "x-access-token"
		password, err := secretStore.GetSecretPlaintext(ctx, repo.AuthToken.IDDigest)
		if err != nil {
			return nil, nil, err
		}
		authHeader := "basic " + base64.StdEncoding.EncodeToString(
			[]byte(fmt.Sprintf("%s:%s", username, password)),
		)
		opts = append(opts, gitutil.WithArgs(
			"-c", "http."+repo.URL.Remote+".extraheader=Authorization: "+string(authHeader),
		))
	} else if repo.AuthHeader != nil {
		secretStore, err := repo.Query.Secrets(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get secret store: %w", err)
		}
		authHeader, err := secretStore.GetSecretPlaintext(ctx, repo.AuthHeader.IDDigest)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, gitutil.WithArgs(
			"-c", "http."+repo.URL.Remote+".extraheader=Authorization: "+string(authHeader),
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

	netConf, err := repo.dnsConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	if netConf != nil {
		src, err := os.Open("/etc/resolv.conf")
		if err != nil {
			return nil, nil, err
		}
		defer src.Close()

		override, err := os.CreateTemp("", "buildkit-git-resolv")
		if err != nil {
			return nil, nil, errors.Wrap(err, "create hosts override")
		}
		resolvPath := override.Name()
		if err := mergeResolv(override, src, netConf); err != nil {
			override.Close()
			return nil, nil, err
		}
		if err := override.Close(); err != nil {
			return nil, nil, errors.Wrap(err, "close hosts override")
		}

		// XXX: race!!!
		// cleanups.Add("remove updated /etc/resolv", func() error {
		// 	return os.Remove(resolvPath)
		// })
		fmt.Println("resolvPath", resolvPath)

		opts = append(opts, gitutil.WithExec(func(ctx context.Context, cmd *exec.Cmd) error {
			return runWithStandardUmaskAndNetOverride(ctx, cmd, "", resolvPath)
		}))
	}

	return gitutil.NewGitCLI(opts...), cleanups.Run, nil
}

func (repo *RemoteGitRepository) dnsConfig(ctx context.Context) (*oci.DNSConfig, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	namespace := clientMetadata.SessionID

	clientDomains := []string{}
	clientDomains = append(clientDomains, network.SessionDomain(namespace))

	dns := *repo.Query.DNS()
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
			newDomains := append([]string{}, dns.SearchDomains...)
			newDomains = append(newDomains, oldDomains...)
			if _, err := fmt.Fprintln(dst, "search", strings.Join(newDomains, " ")); err != nil {
				return err
			}
			replacedSearch = true
		case strings.HasPrefix(srcScan.Text(), "options"):
			oldOptions := strings.Fields(srcScan.Text())[1:]
			newOptions := append([]string{}, dns.Options...)
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
	locker.Lock(gitRemoteIndex + repo.URL.Remote)
	defer locker.Unlock(gitRemoteIndex + repo.URL.Remote)

	sis, err := searchGitRemote(ctx, dagop.Cache(), repo.URL.Remote)
	if err != nil {
		return fmt.Errorf("failed to search metadata for %s: %w", repo.URL.Remote, err)
	}

	var remoteRef bkcache.MutableRef
	for _, si := range sis {
		remoteRef, err = dagop.Cache().GetMutable(ctx, si.ID())
		if err != nil {
			if errors.Is(err, bkcache.ErrLocked) {
				// should never really happen as no other function should access this metadata, but lets be graceful
				bklog.G(ctx).Warnf("mutable ref for %s  %s was locked: %v", repo.URL.Remote, si.ID(), err)
				continue
			}
			return fmt.Errorf("failed to get mutable ref for %s: %w", repo.URL.Remote, err)
		}
		break
	}

	initializeRepo := false
	if remoteRef == nil {
		remoteRef, err = dagop.Cache().New(ctx, nil, dagop.Group(),
			bkcache.CachePolicyRetain,
			bkcache.WithDescription(fmt.Sprintf("shared git repo for %s", repo.URL.Remote)))
		if err != nil {
			return fmt.Errorf("failed to create new mutable for %s: %w", repo.URL.Remote, err)
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

	git := gitCLI(gitutil.WithGitDir(dir))

	if initializeRepo {
		// Explicitly set the Git config 'init.defaultBranch' to the
		// implied default to suppress "hint:" output about not having a
		// test logs.
		// default initial branch name set which otherwise spams unit
		if _, err := git.Run(ctx, "-c", "init.defaultBranch=main", "init", "--bare", "--quiet"); err != nil {
			return fmt.Errorf("failed to init repo at %s: %w", dir, err)
		}

		if _, err := git.Run(ctx, "remote", "add", "origin", repo.URL.Remote); err != nil {
			return fmt.Errorf("failed add origin repo at %s: %w", dir, err)
		}

		// save new remote metadata
		md := cacheRefMetadata{remoteRef}
		if err := md.setGitRemote(repo.URL.Remote); err != nil {
			return err
		}
	}

	return fn(dir)
}

type RemoteGitRef struct {
	Query *Query

	Repo *RemoteGitRepository
	Ref  string
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

	commit, fullref, err := ref.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	cacheKey := dagql.CurrentID(ctx).Digest().Encoded()

	locker := ref.Query.Locker()
	locker.Lock(gitSnapshotIndex + cacheKey)
	defer locker.Unlock(gitSnapshotIndex + cacheKey)
	sis, err := searchGitSnapshot(ctx, op.Cache(), cacheKey)
	if err != nil {
		return nil, fmt.Errorf("failed to search metadata for %s: %w", cacheKey, err)
	}
	if len(sis) > 0 {
		res, err := op.Cache().Get(ctx, sis[0].ID(), nil)
		if err != nil {
			return nil, err
		}
		checkout := NewDirectory(ref.Query, nil, "/", ref.Query.Platform(), ref.Repo.Services)
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
			return err
		}

		// skip fetch if commit already exists
		doFetch := true
		if res, err := git.Run(ctx, "rev-parse", fullref); err == nil && string(res) == commit {
			doFetch = false
		}

		if doFetch {
			var refSpec string
			if isCommitSHA(fullref) {
				// TODO: may need fallback if git remote doesn't support fetching by commit
				refSpec = fullref
			} else {
				refSpec = fullref + ":" + fullref
			}
			if _, err := git.Run(ctx, "fetch", "-u", "--tags", "--force", "origin", refSpec); err != nil {
				return fmt.Errorf("failed to fetch remote %s: %w", ref.Repo.URL.Remote, err)
			}
			_, err = git.Run(ctx, "reflog", "expire", "--all", "--expire=now")
			if err != nil {
				return fmt.Errorf("failed to expire reflog for remote %s: %w", ref.Repo.URL.Remote, err)
			}
		}

		checkoutRef, err = op.Cache().New(ctx, nil, op.Group(),
			bkcache.CachePolicyRetain,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription(fmt.Sprintf("git checkout for %s (%s %s)", ref.Repo.URL.Remote, fullref, commit)))
		if err != nil {
			return err
		}

		err = op.Mount(ctx, checkoutRef, func(checkoutDir string) error {
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

			return doGitCheckout(ctx, git, checkoutGit, ref.Repo.URL.Remote, fullref, commit, depth, discardGitDir)
		})
		if err != nil {
			return err
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

	checkout := NewDirectory(ref.Query, nil, "/", ref.Query.Platform(), ref.Repo.Services)
	checkout.Result = snap
	return checkout, nil
}

func doGitCheckout(
	ctx context.Context,
	git, checkoutGit *gitutil.GitCLI,
	remote string,
	fullref string, commit string,
	depth int,
	discardGitDir bool,
) error {
	checkoutDirGit, err := checkoutGit.GitDir(ctx)
	if err != nil {
		return err
	}

	// XXX: annotated tags tests need to exist
	gitCatFileBuf, err := git.Run(ctx, "cat-file", "-t", fullref)
	if err != nil {
		return err
	}
	isAnnotatedTag := strings.TrimSpace(string(gitCatFileBuf)) == "tag"

	// XXX: also what is this?
	pullref := fullref
	if isAnnotatedTag {
		pullref += ":refs/tags/" + pullref
	} else if isCommitSHA(fullref) {
		pullref = "refs/buildkit/" + identity.NewID()
		_, err = checkoutGit.Run(ctx, "update-ref", pullref, commit)
		if err != nil {
			return err
		}
	} else {
		pullref += ":" + pullref
	}

	// XXX: checking out HEAD creates refs/heads/HEAD :thinking:

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

func (ref *RemoteGitRef) Resolve(ctx context.Context) (string, string, error) {
	if isCommitSHA(ref.Ref) {
		return ref.Ref, ref.Ref, nil
	}

	git, cleanup, err := ref.Repo.setup(ctx)
	if err != nil {
		return "", "", err
	}
	defer cleanup()

	out, err := git.Run(ctx,
		"ls-remote",
		ref.Repo.URL.Remote,
		ref.Ref,
		ref.Ref+"^{}",
	)
	if err != nil {
		return "", "", fmt.Errorf("cannot resolve %q: %w", ref.Repo.URL.Remote, err)
	}

	return parseGitRefOutput(ref.Ref, string(out))
}

type LocalGitRepository struct {
	Query *Query

	Directory *Directory
}

var _ GitRepositoryBackend = (*LocalGitRepository)(nil)

func (repo *LocalGitRepository) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return repo.Directory.PBDefinitions(ctx)
}

func (repo *LocalGitRepository) ServiceBindings() ServiceBindings {
	return repo.Directory.Services
}

func (repo *LocalGitRepository) Ref(ctx context.Context, ref string) (GitRefBackend, error) {
	return &LocalGitRef{
		Query: repo.Query,
		Repo:  repo,
		Ref:   ref,
	}, nil
}

func (repo *LocalGitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	var tags []string
	err := repo.mount(ctx, func(src string) error {
		git := gitCLI(gitutil.WithGitDir(src))
		out, err := git.Run(ctx, "tag", "-l")
		if err != nil {
			return err
		}
		tags = strings.Split(string(out), "\n")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tags, nil
}

func (repo *LocalGitRepository) mount(ctx context.Context, f func(string) error) error {
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

	commit, fullref, err := ref.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	cacheKey := dagql.CurrentID(ctx).Digest().Encoded()

	locker := ref.Query.Locker()
	locker.Lock(gitSnapshotIndex + cacheKey)
	defer locker.Unlock(gitSnapshotIndex + cacheKey)
	bkref, err := op.Cache().New(ctx, nil, op.Group(),
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(op.Name())) // XXX: better name
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = ref.Repo.mount(ctx, func(src string) error {
		git := gitCLI(gitutil.WithGitDir(src))
		if err != nil {
			return err
		}
		gitDir, err := git.GitDir(ctx)
		if err != nil {
			return err
		}

		return op.Mount(ctx, bkref, func(checkoutDir string) error {
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

			return doGitCheckout(ctx, git, checkoutGit, "file://"+gitDir, fullref, commit, depth, discardGitDir)

			// if _, err := git.Run(ctx, "init"); err != nil {
			// 	return err
			// }
			// args := []string{"fetch", "-u", "--tags", "--force"}
			// if depth != 0 {
			// 	args = append(args, fmt.Sprintf("--depth=%d", depth))
			// }
			// args = append(args, "file://"+src, ref.Ref)
			// if _, err := git.Run(ctx, args...); err != nil {
			// 	return err
			// }
			// if _, err := git.Run(ctx, "checkout", "FETCH_HEAD"); err != nil {
			// 	return err
			// }
			//
			// if discardGitDir {
			// 	if err := os.RemoveAll(filepath.Join(checkout, ".git")); err != nil {
			// 		return err
			// 	}
			// }
		})
	})
	if err != nil {
		return nil, err
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
	var commit, fullref string
	err := ref.Repo.mount(ctx, func(src string) error {
		git := gitCLI(gitutil.WithGitDir(src))
		out, err := git.Run(ctx, "show-ref", "--deref", "--head", ref.Ref, ref.Ref+"^{}")
		if err != nil {
			return err
		}
		commit, fullref, err = parseGitRefOutput(ref.Ref, string(out))
		return err
	})
	if err != nil {
		return "", "", err
	}
	return commit, fullref, nil
}

var validHex = regexp.MustCompile(`^[a-f0-9]{40}$`)

func isCommitSHA(str string) bool {
	return validHex.MatchString(str)
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

func gitCLI(opts ...gitutil.Option) *gitutil.GitCLI {
	opts = append([]gitutil.Option{
		// XXX: we can maybe do this?
		// gitutil.WithStreams(func(ctx context.Context) (stdout, stderr io.WriteCloser, flush func()) {
		// 	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
		// 	return nopCloser{stdio.Stdout}, nopCloser{stdio.Stderr}, func() {}
		// 	// return bklogs.NewLogStreams(ctx, false)
		// }),
		gitutil.WithStreams(func(ctx context.Context) (stdout, stderr io.WriteCloser, flush func()) {
			return logs.NewLogStreams(ctx, false)
		}),
	}, opts...)
	return gitutil.NewGitCLI(opts...)
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error { return nil }

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
	if err := syscall.Unshare(syscall.CLONE_FS); err != nil {
		return err
	}
	syscall.Umask(0022)
	before, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		panic(err)
	}
	fmt.Println("resolv before", string(before))
	if err := overrideNetworkConfig(hosts, resolv); err != nil {
		return errors.Wrapf(err, "failed to override network config")
	}
	after, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		panic(err)
	}
	fmt.Println("resolv after", string(after))
	return runProcessGroup(ctx, cmd)
}

func overrideNetworkConfig(hostsOverride, resolvOverride string) error {
	if hostsOverride != "" {
		if err := mount.Mount(hostsOverride, "/etc/hosts", "", "bind"); err != nil {
			return fmt.Errorf("mount hosts override %s: %w", hostsOverride, err)
		}
	}
	if resolvOverride != "" {
		here, err := os.ReadFile(resolvOverride)
		if err != nil {
			panic(err)
		}
		fmt.Println("resolv here hm", string(here))
		dirs, err := os.ReadDir("/etc")
		if err != nil {
			panic(err)
		}
		for _, dir := range dirs {
			fmt.Println(dir.Name())
		}
		if err := syscall.Mount(resolvOverride, "/etc/resolv.conf", "", syscall.MS_BIND, ""); err != nil {
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
func parseGitRefOutput(target string, out string) (commit string, ref string, err error) {
	lines := strings.Split(string(out), "\n")

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
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		lineMatch := &reference{sha: fields[0], ref: fields[1]}

		switch lineMatch.ref {
		case headRef:
			headMatch = lineMatch
		case tagRef, annotatedTagRef:
			tagMatch = lineMatch
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
		return "", "", fmt.Errorf("repository does not contain ref %q, output: %q", target, string(out))
	}
	if !isCommitSHA(match.sha) {
		return "", "", fmt.Errorf("invalid commit sha %q for %q", match.sha, match.ref)
	}

	return match.sha, match.ref, nil
}

func searchRefMetadata(ctx context.Context, store bkcache.MetadataStore, key string, idx string) ([]cacheRefMetadata, error) {
	mds, err := store.Search(ctx, idx+key, false)
	if err != nil {
		return nil, err
	}
	results := make([]cacheRefMetadata, len(mds))
	for i, md := range mds {
		results[i] = cacheRefMetadata{md}
	}
	return results, nil
}
func searchGitRemote(ctx context.Context, store bkcache.MetadataStore, remote string) ([]cacheRefMetadata, error) {
	return searchRefMetadata(ctx, store, remote, gitRemoteIndex)
}
func searchGitSnapshot(ctx context.Context, store bkcache.MetadataStore, key string) ([]cacheRefMetadata, error) {
	return searchRefMetadata(ctx, store, key, gitSnapshotIndex)
}

const keyGitRemote = "git-remote"
const gitRemoteIndex = keyGitRemote + "::"
const keyGitSnapshot = "git-snapshot"
const gitSnapshotIndex = keyGitSnapshot + "::"

type cacheRefMetadata struct {
	bkcache.RefMetadata
}

func (md cacheRefMetadata) setGitSnapshot(key string) error {
	return md.SetString(keyGitSnapshot, key, gitSnapshotIndex+key)
}
func (md cacheRefMetadata) setGitRemote(key string) error {
	return md.SetString(keyGitRemote, key, gitRemoteIndex+key)
}
