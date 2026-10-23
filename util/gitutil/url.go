package gitutil

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/util/sshutil"
)

const (
	HTTPProtocol  string = "http"
	HTTPSProtocol string = "https"
	SSHProtocol   string = "ssh"
	GitProtocol   string = "git"
)

var (
	ErrUnknownProtocol = errors.New("unknown protocol")
	ErrInvalidProtocol = errors.New("invalid protocol")
)

var supportedProtos = map[string]struct{}{
	HTTPProtocol:  {},
	HTTPSProtocol: {},
	SSHProtocol:   {},
	GitProtocol:   {},
}

var protoRegexp = regexp.MustCompile(`^[a-zA-Z0-9]+://`)

// URL is a custom URL type that points to a remote Git repository.
//
// URLs can be parsed from both standard URLs (e.g.
// "https://github.com/dagger/dagger/internal/buildkit.git"), as well as SCP-like URLs (e.g.
// "git@github.com:moby/buildkit.git").
//
// See https://git-scm.com/book/en/v2/Git-on-the-Server-The-Protocols
type GitURL struct {
	// Scheme is the protocol over which the git repo can be accessed
	Scheme string

	// Host is the remote host that hosts the git repo
	Host string
	// Path is the path on the host to access the repo
	Path string
	// User is the username/password to access the host
	User *url.Userinfo
	// Fragment can contain additional metadata
	Fragment *GitURLFragment

	scpStyle bool // true if the URL is in SCP style
}

// Remote is a valid URL remote to pass into the Git CLI tooling (i.e. without the fragment metadata)
func (gitURL *GitURL) Remote() string {
	gitURLCopy := *gitURL
	gitURLCopy.Fragment = nil
	return gitURLCopy.String()
}

// RedactedRemote is Remote without any userinfo, safe to record in logs and
// profiles: HTTP(S) userinfo may carry credentials (user:token@host).
func (gitURL *GitURL) RedactedRemote() string {
	gitURLCopy := *gitURL
	gitURLCopy.Fragment = nil
	gitURLCopy.User = nil
	if gitURLCopy.scpStyle {
		// SCPStyleURL.String always renders the "user@" separator.
		return gitURLCopy.Host + ":" + gitURLCopy.Path
	}
	return gitURLCopy.String()
}

// HTMLURL returns a best-effort browser URL for the repository, without clone
// credentials or revision selectors. SSH and Git transport ports are not web
// ports, so only HTTP(S) ports are retained.
func (gitURL *GitURL) HTMLURL() string {
	u := &url.URL{
		Scheme: gitURL.Scheme,
		Host:   gitURL.Host,
		Path:   "/" + strings.TrimPrefix(strings.TrimSuffix(gitURL.Path, ".git"), "/"),
	}
	if u.Scheme != HTTPProtocol && u.Scheme != HTTPSProtocol {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		u.Scheme = HTTPSProtocol
		u.Host = host
	}
	return u.String()
}

func (gitURL *GitURL) String() string {
	if gitURL.scpStyle {
		result := sshutil.SCPStyleURL{
			User:     gitURL.User,
			Host:     gitURL.Host,
			Path:     gitURL.Path,
			Fragment: gitURL.Fragment.String(),
		}
		return result.String()
	}

	result := &url.URL{
		Scheme:   gitURL.Scheme,
		User:     gitURL.User,
		Host:     gitURL.Host,
		Path:     gitURL.Path,
		Fragment: gitURL.Fragment.String(),
	}
	return result.String()
}

// SameRepository reports whether a and b name the same repository on the same
// host, whichever transport or user each reaches it with: e.g.
// https://github.com/org/repo, git@github.com:org/repo.git and
// ssh://git@github.com/org/repo. Ports only distinguish URLs of the same
// scheme, since each transport listens on its own. A path relative to an SSH
// user's home (scp-style) is compared as written, as forges serve it. Paths
// are case-sensitive, except on forges known to route them case-insensitively
// (caseInsensitivePathHosts).
func SameRepository(a, b *GitURL) bool {
	if a == nil || b == nil {
		return false
	}
	aHost, bHost := (&url.URL{Host: a.Host}), (&url.URL{Host: b.Host})
	if !strings.EqualFold(aHost.Hostname(), bHost.Hostname()) {
		return false
	}
	if a.Scheme == b.Scheme && effectivePort(a.Scheme, aHost.Port()) != effectivePort(b.Scheme, bHost.Port()) {
		return false
	}
	return repositoryPathOn(aHost.Hostname(), a.Path) == repositoryPathOn(aHost.Hostname(), b.Path)
}

// caseInsensitivePathHosts are forges whose repository paths are
// case-insensitive: github.com/Org/Repo and github.com/org/repo are one
// repository. Anywhere else, differently cased paths may be distinct
// repositories.
var caseInsensitivePathHosts = map[string]bool{
	"github.com": true,
	"gitlab.com": true,
}

// RepositoryIdentity is the identity of the repository a clone ref names,
// whichever way it is spelled: host and path, as SameRepository compares
// them, without scheme, user or .git suffix. github.com/org/repo,
// https://github.com/org/repo.git, git@github.com:org/repo and
// ssh://git@github.com/org/repo are all github.com/org/repo. The identity
// names cache namespaces, so it never carries credentials, and it is its own
// identity. A ref that is no git URL keeps its spelling, without scheme and
// userinfo.
//
// Without a scheme a port cannot be read against the transport's default, so
// the ports git transports listen on by default are dropped whatever the
// scheme, and any other port is kept. Unlike workspace.NormalizeGitRemote,
// which keys user config, it parses a scheme-less ref the way cloning does
// and folds path case where SameRepository does.
func RepositoryIdentity(cloneRef string) string {
	urls, err := ParseCloneURL(cloneRef)
	if err != nil || len(urls) == 0 {
		return withoutUserinfo(cloneRef)
	}
	u := urls[0]
	host := &url.URL{Host: u.Host}
	identity := strings.ToLower(host.Hostname())
	// An IPv6 address keeps its brackets, so its last group is not taken
	// for a port.
	if strings.Contains(identity, ":") {
		identity = "[" + identity + "]"
	}
	if port := host.Port(); port != "" && !defaultTransportPorts[port] {
		identity += ":" + port
	}
	if path := repositoryPathOn(host.Hostname(), u.Path); path != "" {
		identity += "/" + path
	}
	return identity
}

var defaultTransportPorts = map[string]bool{"22": true, "80": true, "443": true, "9418": true}

func withoutUserinfo(ref string) string {
	if _, rest, ok := strings.Cut(ref, "://"); ok {
		ref = rest
	}
	authority, _, _ := strings.Cut(ref, "/")
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		ref = ref[at+1:]
	}
	return ref
}

func effectivePort(scheme, port string) string {
	if port != "" {
		return port
	}
	switch scheme {
	case SSHProtocol:
		return "22"
	case HTTPSProtocol:
		return "443"
	case HTTPProtocol:
		return "80"
	case GitProtocol:
		return "9418"
	}
	return ""
}

// repositoryPathOn is the path of a repository on host, folded where the
// host routes paths case-insensitively.
func repositoryPathOn(host, path string) string {
	path = strings.Trim(path, "/")
	// Every suffix, so that the result is its own repository path.
	for strings.HasSuffix(path, ".git") {
		path = strings.TrimSuffix(path, ".git")
	}
	if caseInsensitivePathHosts[strings.ToLower(host)] {
		return strings.ToLower(path)
	}
	return path
}

// GitURLFragment is the buildkit-specific metadata extracted from the fragment
// of a remote URL.
type GitURLFragment struct {
	// Ref is the git reference
	Ref string
	// Subdir is the sub-directory inside the git repository to use
	Subdir string
}

// splitGitFragment splits a git URL fragment into its respective git
// reference and subdirectory components.
func splitGitFragment(fragment string) *GitURLFragment {
	if fragment == "" {
		return nil
	}
	ref, subdir, _ := strings.Cut(fragment, ":")
	return &GitURLFragment{Ref: ref, Subdir: subdir}
}

func (fragment *GitURLFragment) String() string {
	if fragment == nil {
		return ""
	}
	if fragment.Subdir == "" {
		return fragment.Ref
	}
	return fragment.Ref + ":" + fragment.Subdir
}

// ParseURL parses a BuildKit-style Git URL (that may contain additional
// fragment metadata) and returns a parsed GitURL object.
func ParseURL(remote string) (*GitURL, error) {
	if proto := protoRegexp.FindString(remote); proto != "" {
		proto = strings.ToLower(strings.TrimSuffix(proto, "://"))
		if _, ok := supportedProtos[proto]; !ok {
			return nil, fmt.Errorf("%w %q", ErrInvalidProtocol, proto)
		}
		url, err := url.Parse(remote)
		if err != nil {
			return nil, err
		}
		return fromURL(url), nil
	}

	if url, err := sshutil.ParseSCPStyleURL(remote); err == nil {
		return fromSCPStyleURL(url), nil
	}

	return nil, ErrUnknownProtocol
}

// cloneURLFallbackProtocols is the priority order for resolving a clone ref
// that omits its scheme (e.g. "github.com/foo/bar"). Callers iterate the
// candidates and try the next one when authentication fails.
var cloneURLFallbackProtocols = []string{HTTPSProtocol, SSHProtocol}

// ParseCloneURL parses a clone reference into one or more candidate GitURLs,
// in priority order.
//
// Module sources may omit the scheme (e.g. "github.com/foo/bar") since the
// transport is decided at clone time, not parse time. For those, ParseCloneURL
// returns multiple candidates with common protocols prepended; callers iterate
// and try the next one on authentication failure. For fully-qualified URLs,
// it returns a single-element slice.
func ParseCloneURL(remote string) ([]*GitURL, error) {
	u, err := ParseURL(remote)
	if err == nil {
		return []*GitURL{u}, nil
	}
	if !errors.Is(err, ErrUnknownProtocol) {
		return nil, err
	}
	candidates := make([]*GitURL, 0, len(cloneURLFallbackProtocols))
	for _, proto := range cloneURLFallbackProtocols {
		if u, err := ParseURL(proto + "://" + remote); err == nil {
			candidates = append(candidates, u)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProtocol, remote)
	}
	return candidates, nil
}

func IsGitTransport(remote string) bool {
	if proto := protoRegexp.FindString(remote); proto != "" {
		proto = strings.ToLower(strings.TrimSuffix(proto, "://"))
		_, ok := supportedProtos[proto]
		return ok
	}
	return sshutil.IsImplicitSSHTransport(remote)
}

func fromURL(url *url.URL) *GitURL {
	path, fragment := splitGitVersion(url.Path, url.Fragment)
	return &GitURL{
		Scheme:   url.Scheme,
		User:     url.User,
		Host:     url.Host,
		Path:     path,
		Fragment: fragment,
	}
}

func fromSCPStyleURL(url *sshutil.SCPStyleURL) *GitURL {
	path, fragment := splitGitVersion(url.Path, url.Fragment)
	return &GitURL{
		Scheme:   SSHProtocol,
		User:     url.User,
		Host:     url.Host,
		Path:     path,
		Fragment: fragment,
		scpStyle: true,
	}
}

// splitGitVersion accepts the module-source "@ref" spelling in addition to
// the BuildKit-style "#ref" fragment. An explicit fragment takes precedence.
func splitGitVersion(path, fragment string) (string, *GitURLFragment) {
	if fragment == "" {
		if repoPath, ref, ok := strings.Cut(path, "@"); ok {
			return repoPath, splitGitFragment(ref)
		}
	}
	return path, splitGitFragment(fragment)
}
