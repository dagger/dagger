package containerimagedns

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets"
	"github.com/moby/buildkit/session/sshforward"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	srcimg "github.com/moby/buildkit/source/containerimage"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/urlutil"
	"github.com/moby/locker"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/network"
)

var validHex = regexp.MustCompile(`^[a-f0-9]{40}$`)
var defaultBranch = regexp.MustCompile(`refs/heads/(\S+)`)

type Opt struct {
	srcimg.SourceOpt
	BaseDNSConfig *oci.DNSConfig
}

type imageSource struct {
	src *srcimg.Source

	cache  cache.Accessor
	locker *locker.Locker
	dns    *oci.DNSConfig
}

func NewSource(opt Opt) (source.Source, error) {
	src, err := srcimg.NewSource(opt.SourceOpt)
	if err != nil {
		return nil, err
	}
	gs := &imageSource{
		src:    src,
		cache:  opt.CacheAccessor,
		locker: locker.New(),
		dns:    opt.BaseDNSConfig,
	}
	return gs, nil
}

func (is *imageSource) Schemes() []string {
	return is.src.Schemes()
}

func (is *imageSource) Identifier(scheme, ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	// in gitdns, we extend the bk identifier so it can carry a namespace. the code commented below pulls that attr out and pipes it into our extended identifier
	return is.src.Identifier(scheme, ref, attrs, platform)
	// srcid, err := is.src.Identifier(scheme, ref, attrs, platform)
	// if err != nil {
	// 	return nil, err
	// }
	// id := &GitIdentifier{
	// 	GitIdentifier: *(srcid.(*srcimg.GitIdentifier)),
	// }

	// if v, ok := attrs[AttrDNSNamespace]; ok {
	// 	id.Namespace = v
	// }

	// return id, nil
}

func (is *imageSource) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager, _ solver.Vertex) (source.SourceInstance, error) {
	gitIdentifier, ok := id.(*GitIdentifier)
	if !ok {
		return nil, errors.Errorf("invalid git identifier %v", id)
	}

	return &gitSourceHandler{
		imageSource: is,
		src:         *gitIdentifier,
		sm:          sm,
	}, nil
}

// needs to be called with repo lock
func (is *imageSource) mountRemote(ctx context.Context, remote string, auth []string, g session.Group) (target string, release func(), retErr error) {
	sis, err := searchGitRemote(ctx, is.cache, remote)
	if err != nil {
		return "", nil, errors.Wrapf(err, "failed to search metadata for %s", urlutil.RedactCredentials(remote))
	}

	var remoteRef cache.MutableRef
	for _, si := range sis {
		remoteRef, err = is.cache.GetMutable(ctx, si.ID())
		if err != nil {
			if errors.Is(err, cache.ErrLocked) {
				// should never really happen as no other function should access this metadata, but lets be graceful
				bklog.G(ctx).Warnf("mutable ref for %s  %s was locked: %v", urlutil.RedactCredentials(remote), si.ID(), err)
				continue
			}
			return "", nil, errors.Wrapf(err, "failed to get mutable ref for %s", urlutil.RedactCredentials(remote))
		}
		break
	}

	initializeRepo := false
	if remoteRef == nil {
		remoteRef, err = is.cache.New(ctx, nil, g, cache.CachePolicyRetain, cache.WithDescription(fmt.Sprintf("shared git repo for %s", urlutil.RedactCredentials(remote))))
		if err != nil {
			return "", nil, errors.Wrapf(err, "failed to create new mutable for %s", urlutil.RedactCredentials(remote))
		}
		initializeRepo = true
	}

	releaseRemoteRef := func() {
		remoteRef.Release(context.TODO())
	}

	defer func() {
		if retErr != nil && remoteRef != nil {
			releaseRemoteRef()
		}
	}()

	mount, err := remoteRef.Mount(ctx, false, g)
	if err != nil {
		return "", nil, err
	}

	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	if err != nil {
		return "", nil, err
	}

	defer func() {
		if retErr != nil {
			lm.Unmount()
		}
	}()

	git, cleanup, err := newGitCLI(dir, "", "", "", auth, nil)
	if err != nil {
		return "", nil, err
	}
	defer cleanup()

	if initializeRepo {
		// Explicitly set the Git config 'init.defaultBranch' to the
		// implied default to suppress "hint:" output about not having a
		// default initial branch name set which otherwise spams unit
		// test logs.
		if _, err := git.run(ctx, "-c", "init.defaultBranch=master", "init", "--bare"); err != nil {
			return "", nil, errors.Wrapf(err, "failed to init repo at %s", dir)
		}

		if _, err := git.run(ctx, "remote", "add", "origin", remote); err != nil {
			return "", nil, errors.Wrapf(err, "failed add origin repo at %s", dir)
		}

		// save new remote metadata
		md := cacheRefMetadata{remoteRef}
		if err := md.setGitRemote(remote); err != nil {
			return "", nil, err
		}
	}
	return dir, func() {
		lm.Unmount()
		releaseRemoteRef()
	}, nil
}

type gitSourceHandler struct {
	*imageSource
	src      GitIdentifier
	cacheKey string
	sm       *session.Manager
	auth     []string
}

func (is *gitSourceHandler) shaToCacheKey(sha string) string {
	key := sha
	if is.src.KeepGitDir {
		key += ".git"
	}
	if is.src.Subdir != "" {
		key += ":" + is.src.Subdir
	}
	return key
}

type authSecret struct {
	token bool
	name  string
}

func (is *gitSourceHandler) authSecretNames() (sec []authSecret, _ error) {
	u, err := url.Parse(is.src.Remote)
	if err != nil {
		return nil, err
	}
	if is.src.AuthHeaderSecret != "" {
		sec = append(sec, authSecret{name: is.src.AuthHeaderSecret + "." + u.Host})
	}
	if is.src.AuthTokenSecret != "" {
		sec = append(sec, authSecret{name: is.src.AuthTokenSecret + "." + u.Host, token: true})
	}
	if is.src.AuthHeaderSecret != "" {
		sec = append(sec, authSecret{name: is.src.AuthHeaderSecret})
	}
	if is.src.AuthTokenSecret != "" {
		sec = append(sec, authSecret{name: is.src.AuthTokenSecret, token: true})
	}
	return sec, nil
}

func (is *gitSourceHandler) getAuthToken(ctx context.Context, g session.Group) error {
	if is.auth != nil {
		return nil
	}
	sec, err := is.authSecretNames()
	if err != nil {
		return err
	}
	return is.sm.Any(ctx, g, func(ctx context.Context, _ string, caller session.Caller) error {
		for _, s := range sec {
			dt, err := secrets.GetSecret(ctx, caller, s.name)
			if err != nil {
				if errors.Is(err, secrets.ErrNotFound) {
					continue
				}
				return err
			}
			if s.token {
				dt = []byte("basic " + base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("x-access-token:%s", dt))))
			}
			is.auth = []string{"-c", "http." + tokenScope(is.src.Remote) + ".extraheader=Authorization: " + string(dt)}
			break
		}
		return nil
	})
}

func (is *gitSourceHandler) mountSSHAuthSock(ctx context.Context, sshID string, g session.Group) (string, func() error, error) {
	var caller session.Caller
	err := is.sm.Any(ctx, g, func(ctx context.Context, _ string, c session.Caller) error {
		if err := sshforward.CheckSSHID(ctx, c, sshID); err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
				return errors.Errorf("no SSH key %q forwarded from the client", sshID)
			}

			return err
		}
		caller = c
		return nil
	})
	if err != nil {
		return "", nil, err
	}

	usr, err := user.Current()
	if err != nil {
		return "", nil, err
	}

	// best effort, default to root
	uid, _ := strconv.Atoi(usr.Uid)
	gid, _ := strconv.Atoi(usr.Gid)

	sock, cleanup, err := sshforward.MountSSHSocket(ctx, caller, sshforward.SocketOpt{
		ID:   sshID,
		UID:  uid,
		GID:  gid,
		Mode: 0700,
	})
	if err != nil {
		return "", nil, err
	}

	return sock, cleanup, nil
}

func (is *gitSourceHandler) mountKnownHosts() (string, func() error, error) {
	if is.src.KnownSSHHosts == "" {
		return "", nil, errors.Errorf("no configured known hosts forwarded from the client")
	}
	knownHosts, err := os.CreateTemp("", "")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error {
		return os.Remove(knownHosts.Name())
	}
	_, err = knownHosts.Write([]byte(is.src.KnownSSHHosts))
	if err != nil {
		cleanup()
		return "", nil, err
	}
	err = knownHosts.Close()
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return knownHosts.Name(), cleanup, nil
}

func (is *gitSourceHandler) dnsConfig() *oci.DNSConfig {
	clientDomains := []string{}
	if is.src.Namespace != "" {
		clientDomains = append(clientDomains, network.SessionDomain(is.src.Namespace))
	}
	dns := *is.dns
	dns.SearchDomains = append(clientDomains, dns.SearchDomains...)
	return &dns
}

func (is *gitSourceHandler) CacheKey(ctx context.Context, g session.Group, index int) (string, string, solver.CacheOpts, bool, error) {
	remote := is.src.Remote
	is.locker.Lock(remote)
	defer is.locker.Unlock(remote)

	if ref := is.src.Ref; ref != "" && isCommitSHA(ref) {
		cacheKey := is.shaToCacheKey(ref)
		is.cacheKey = cacheKey
		return cacheKey, ref, nil, true, nil
	}

	is.getAuthToken(ctx, g)

	gitDir, unmountGitDir, err := is.mountRemote(ctx, remote, is.auth, g)
	if err != nil {
		return "", "", nil, false, err
	}
	defer unmountGitDir()

	var sock string
	if is.src.MountSSHSock != "" {
		var unmountSock func() error
		sock, unmountSock, err = is.mountSSHAuthSock(ctx, is.src.MountSSHSock, g)
		if err != nil {
			return "", "", nil, false, err
		}
		defer unmountSock()
	}

	var knownHosts string
	if is.src.KnownSSHHosts != "" {
		var unmountKnownHosts func() error
		knownHosts, unmountKnownHosts, err = is.mountKnownHosts()
		if err != nil {
			return "", "", nil, false, err
		}
		defer unmountKnownHosts()
	}

	netConf := is.dnsConfig()

	git, cleanup, err := newGitCLI(gitDir, "", sock, knownHosts, is.auth, netConf)
	if err != nil {
		return "", "", nil, false, err
	}
	defer cleanup()

	ref := is.src.Ref
	if ref == "" {
		ref, err = getDefaultBranch(ctx, git, is.src.Remote)
		if err != nil {
			return "", "", nil, false, err
		}
	}

	// TODO: should we assume that remote tag is immutable? add a timer?

	buf, err := git.run(ctx, "ls-remote", "origin", ref, ref+"^{}")
	if err != nil {
		return "", "", nil, false, errors.Wrapf(err, "failed to fetch remote %s", urlutil.RedactCredentials(remote))
	}
	lines := strings.Split(buf.String(), "\n")

	// simulate git-checkout semantics, and make sure to select exactly the right ref
	var (
		partialRef      = "refs/" + strings.TrimPrefix(ref, "refs/")
		headRef         = "refs/heads/" + strings.TrimPrefix(ref, "refs/heads/")
		tagRef          = "refs/tags/" + strings.TrimPrefix(ref, "refs/tags/")
		annotatedTagRef = tagRef + "^{}"
	)
	var sha, headSha, tagSha string
	for _, line := range lines {
		lineSha, lineRef, _ := strings.Cut(line, "\t")
		switch lineRef {
		case headRef:
			headSha = lineSha
		case tagRef, annotatedTagRef:
			tagSha = lineSha
		case partialRef:
			sha = lineSha
		}
	}

	// git-checkout prefers branches in case of ambiguity
	if sha == "" {
		sha = headSha
	}
	if sha == "" {
		sha = tagSha
	}
	if sha == "" {
		return "", "", nil, false, errors.Errorf("repository does not contain ref %s, output: %q", ref, buf.String())
	}
	if !isCommitSHA(sha) {
		return "", "", nil, false, errors.Errorf("invalid commit sha %q", sha)
	}

	cacheKey := is.shaToCacheKey(sha)
	is.cacheKey = cacheKey
	return cacheKey, sha, nil, true, nil
}

func (is *gitSourceHandler) Snapshot(ctx context.Context, g session.Group) (out cache.ImmutableRef, retErr error) { //nolint: gocyclo
	cacheKey := is.cacheKey
	if cacheKey == "" {
		var err error
		cacheKey, _, _, _, err = is.CacheKey(ctx, g, 0)
		if err != nil {
			return nil, err
		}
	}

	is.getAuthToken(ctx, g)

	snapshotKey := cacheKey + ":" + is.src.Subdir
	is.locker.Lock(snapshotKey)
	defer is.locker.Unlock(snapshotKey)

	sis, err := searchGitSnapshot(ctx, is.cache, snapshotKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to search metadata for %s", snapshotKey)
	}
	if len(sis) > 0 {
		return is.cache.Get(ctx, sis[0].ID(), nil)
	}

	is.locker.Lock(is.src.Remote)
	defer is.locker.Unlock(is.src.Remote)
	gitDir, unmountGitDir, err := is.mountRemote(ctx, is.src.Remote, is.auth, g)
	if err != nil {
		return nil, err
	}
	defer unmountGitDir()

	var sock string
	if is.src.MountSSHSock != "" {
		var unmountSock func() error
		sock, unmountSock, err = is.mountSSHAuthSock(ctx, is.src.MountSSHSock, g)
		if err != nil {
			return nil, err
		}
		defer unmountSock()
	}

	var knownHosts string
	if is.src.KnownSSHHosts != "" {
		var unmountKnownHosts func() error
		knownHosts, unmountKnownHosts, err = is.mountKnownHosts()
		if err != nil {
			return nil, err
		}
		defer unmountKnownHosts()
	}

	netConf := is.dnsConfig()

	git, cleanup, err := newGitCLI(gitDir, "", sock, knownHosts, is.auth, netConf)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ref := is.src.Ref
	if ref == "" {
		ref, err = getDefaultBranch(ctx, git, is.src.Remote)
		if err != nil {
			return nil, err
		}
	}

	doFetch := true
	if isCommitSHA(ref) {
		// skip fetch if commit already exists
		if _, err := git.run(ctx, "cat-file", "-e", ref+"^{commit}"); err == nil {
			doFetch = false
		}
	}

	if doFetch {
		// make sure no old lock files have leaked
		os.RemoveAll(filepath.Join(gitDir, "shallow.lock"))

		args := []string{"fetch"}
		if !isCommitSHA(ref) { // TODO: find a branch from ls-remote?
			args = append(args, "--depth=1", "--no-tags")
		} else {
			if _, err := os.Lstat(filepath.Join(gitDir, "shallow")); err == nil {
				args = append(args, "--unshallow")
			}
		}
		args = append(args, "origin")
		if !isCommitSHA(ref) {
			args = append(args, "--force", ref+":tags/"+ref)
			// local refs are needed so they would be advertised on next fetches. Force is used
			// in case the ref is a branch and it now points to a different commit sha
			// TODO: is there a better way to do this?
		}
		if _, err := git.run(ctx, args...); err != nil {
			return nil, errors.Wrapf(err, "failed to fetch remote %s", urlutil.RedactCredentials(is.src.Remote))
		}
		_, err = git.run(ctx, "reflog", "expire", "--all", "--expire=now")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to expire reflog for remote %s", urlutil.RedactCredentials(is.src.Remote))
		}
	}

	checkoutRef, err := is.cache.New(ctx, nil, g, cache.WithRecordType(client.UsageRecordTypeGitCheckout), cache.WithDescription(fmt.Sprintf("git snapshot for %s#%s", is.src.Remote, ref)))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create new mutable for %s", urlutil.RedactCredentials(is.src.Remote))
	}

	defer func() {
		if retErr != nil && checkoutRef != nil {
			checkoutRef.Release(context.TODO())
		}
	}()

	mount, err := checkoutRef.Mount(ctx, false, g)
	if err != nil {
		return nil, err
	}
	lm := snapshot.LocalMounter(mount)
	checkoutDir, err := lm.Mount()
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil && lm != nil {
			lm.Unmount()
		}
	}()

	subdir := path.Clean(is.src.Subdir)
	if subdir == "/" {
		subdir = "."
	}

	if is.src.KeepGitDir && subdir == "." {
		checkoutDirGit := filepath.Join(checkoutDir, ".git")
		if err := os.MkdirAll(checkoutDir, 0711); err != nil {
			return nil, err
		}
		checkoutGit := git.withinDir(checkoutDirGit, checkoutDir)
		_, err = checkoutGit.run(ctx, "-c", "init.defaultBranch=master", "init")
		if err != nil {
			return nil, err
		}
		// Defense-in-depth: clone using the file protocol to disable local-clone
		// optimizations which can be abused on some versions of Git to copy unintended
		// host files into the build context.
		_, err = checkoutGit.run(ctx, "remote", "add", "origin", "file://"+gitDir)
		if err != nil {
			return nil, err
		}

		gitCatFileBuf, err := git.run(ctx, "cat-file", "-t", ref)
		if err != nil {
			return nil, err
		}
		isAnnotatedTag := strings.TrimSpace(gitCatFileBuf.String()) == "tag"

		pullref := ref
		switch {
		case isAnnotatedTag:
			pullref += ":refs/tags/" + pullref
		case isCommitSHA(ref):
			pullref = "refs/buildkit/" + identity.NewID()
			_, err = git.run(ctx, "update-ref", pullref, ref)
			if err != nil {
				return nil, err
			}
		default:
			pullref += ":" + pullref
		}
		_, err = checkoutGit.run(ctx, "fetch", "-u", "--depth=1", "origin", pullref)
		if err != nil {
			return nil, err
		}
		_, err = checkoutGit.run(ctx, "checkout", "FETCH_HEAD")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to checkout remote %s", urlutil.RedactCredentials(is.src.Remote))
		}
		_, err = checkoutGit.run(ctx, "remote", "set-url", "origin", urlutil.RedactCredentials(is.src.Remote))
		if err != nil {
			return nil, errors.Wrapf(err, "failed to set remote origin to %s", urlutil.RedactCredentials(is.src.Remote))
		}
		_, err = checkoutGit.run(ctx, "reflog", "expire", "--all", "--expire=now")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to expire reflog for remote %s", urlutil.RedactCredentials(is.src.Remote))
		}
		if err := os.Remove(filepath.Join(checkoutDirGit, "FETCH_HEAD")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, errors.Wrapf(err, "failed to remove FETCH_HEAD for remote %s", urlutil.RedactCredentials(is.src.Remote))
		}
		gitDir = checkoutDirGit
	} else {
		cd := checkoutDir
		if subdir != "." {
			cd, err = os.MkdirTemp(cd, "checkout")
			if err != nil {
				return nil, errors.Wrapf(err, "failed to create temporary checkout dir")
			}
		}
		_, err = git.withinDir(gitDir, cd).run(ctx, "checkout", ref, "--", ".")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to checkout remote %s", urlutil.RedactCredentials(is.src.Remote))
		}
		if subdir != "." {
			d, err := os.Open(filepath.Join(cd, subdir))
			if err != nil {
				return nil, errors.Wrapf(err, "failed to open subdir %v", subdir)
			}
			defer func() {
				if d != nil {
					d.Close()
				}
			}()
			names, err := d.Readdirnames(0)
			if err != nil {
				return nil, err
			}
			for _, n := range names {
				if err := os.Rename(filepath.Join(cd, subdir, n), filepath.Join(checkoutDir, n)); err != nil {
					return nil, err
				}
			}
			if err := d.Close(); err != nil {
				return nil, err
			}
			d = nil // reset defer
			if err := os.RemoveAll(cd); err != nil {
				return nil, err
			}
		}
	}

	_, err = git.withinDir(gitDir, checkoutDir).run(ctx, "submodule", "update", "--init", "--recursive", "--depth=1")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to update submodules for %s", urlutil.RedactCredentials(is.src.Remote))
	}

	if idmap := mount.IdentityMapping(); idmap != nil {
		u := idmap.RootPair()
		err := filepath.WalkDir(gitDir, func(p string, _ os.DirEntry, _ error) error {
			return os.Lchown(p, u.UID, u.GID)
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to remap git checkout")
		}
	}

	lm.Unmount()
	lm = nil

	snap, err := checkoutRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	checkoutRef = nil

	defer func() {
		if retErr != nil {
			snap.Release(context.TODO())
		}
	}()

	md := cacheRefMetadata{snap}
	if err := md.setGitSnapshot(snapshotKey); err != nil {
		return nil, err
	}
	return snap, nil
}

func isCommitSHA(str string) bool {
	return validHex.MatchString(str)
}

func tokenScope(remote string) string {
	// generally we can only use the token for fetching main remote but in case of github.com we do best effort
	// to try reuse same token for all github.com remotes. This is the same behavior actions/checkout uses
	for _, pfx := range []string{"https://github.com/", "https://www.github.com/"} {
		if strings.HasPrefix(remote, pfx) {
			return pfx
		}
	}
	return remote
}

// getDefaultBranch gets the default branch of a repository using ls-remote
func getDefaultBranch(ctx context.Context, git *gitCLI, remoteURL string) (string, error) {
	buf, err := git.run(ctx, "ls-remote", "--symref", remoteURL, "HEAD")
	if err != nil {
		return "", errors.Wrapf(err, "error fetching default branch for repository %s", urlutil.RedactCredentials(remoteURL))
	}

	ss := defaultBranch.FindAllStringSubmatch(buf.String(), -1)
	if len(ss) == 0 || len(ss[0]) != 2 {
		return "", errors.Errorf("could not find default branch for repository: %s", urlutil.RedactCredentials(remoteURL))
	}
	return ss[0][1], nil
}

const keyGitRemote = "git-remote"
const gitRemoteIndex = keyGitRemote + "::"
const keyGitSnapshot = "git-snapshot"
const gitSnapshotIndex = keyGitSnapshot + "::"

func search(ctx context.Context, store cache.MetadataStore, key string, idx string) ([]cacheRefMetadata, error) {
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

func searchGitRemote(ctx context.Context, store cache.MetadataStore, remote string) ([]cacheRefMetadata, error) {
	return search(ctx, store, remote, gitRemoteIndex)
}

func searchGitSnapshot(ctx context.Context, store cache.MetadataStore, key string) ([]cacheRefMetadata, error) {
	return search(ctx, store, key, gitSnapshotIndex)
}

type cacheRefMetadata struct {
	cache.RefMetadata
}

func (md cacheRefMetadata) setGitSnapshot(key string) error {
	return md.SetString(keyGitSnapshot, key, gitSnapshotIndex+key)
}

func (md cacheRefMetadata) setGitRemote(key string) error {
	return md.SetString(keyGitRemote, key, gitRemoteIndex+key)
}
