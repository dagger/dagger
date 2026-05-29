package moduleref

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/engine/vcs"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

type Kind string

const (
	KindLocal Kind = "LOCAL_SOURCE"
	KindGit   Kind = "GIT_SOURCE"
)

type SchemeType int

const (
	NoScheme SchemeType = iota
	SchemeHTTP
	SchemeHTTPS
	SchemeSSH
	SchemeSCPLike
)

func (s SchemeType) Prefix() string {
	switch s {
	case SchemeHTTP:
		return "http://"
	case SchemeHTTPS:
		return "https://"
	case SchemeSSH:
		return "ssh://"
	default:
		return ""
	}
}

func (s SchemeType) IsSSH() bool {
	return s == SchemeSSH
}

// FastKind performs a quick heuristic check to determine whether a module ref
// string refers to a local path or a git source. It returns "" if the kind
// cannot be determined without further inspection.
func FastKind(refString string, refPin string) Kind {
	switch {
	case refPin != "":
		return KindGit
	case len(refString) > 0 && (refString[0] == '/' || refString[0] == '.'):
		return KindLocal
	case len(refString) > 1 && refString[0:2] == "..":
		return KindLocal
	case strings.HasPrefix(refString, SchemeHTTP.Prefix()):
		return KindGit
	case strings.HasPrefix(refString, SchemeHTTPS.Prefix()):
		return KindGit
	case strings.HasPrefix(refString, SchemeSSH.Prefix()):
		return KindGit
	case !strings.Contains(refString, "."):
		// technically host names can not have any dot, but we can save a lot of work
		// by assuming a dot-free ref string is a local path. Users can prefix
		// args with a scheme:// to disambiguate these obscure corner cases.
		return KindLocal
	default:
		return ""
	}
}

type ParsedGit struct {
	ModPath string

	ModVersion string
	HasVersion bool

	RepoRoot       *vcs.RepoRoot
	RepoRootSubdir string

	Scheme SchemeType

	SourceUser     string
	CloneUser      string
	SourceCloneRef string
	CloneRef       string
}

type EndpointError struct{ Err error }

func (e EndpointError) Error() string {
	return e.Err.Error()
}

func (e EndpointError) Unwrap() error {
	return e.Err
}

func ParseGit(refString string) (ParsedGit, error) {
	scheme, schemelessRef := parseScheme(refString)

	if scheme == NoScheme && isSCPLike(schemelessRef) {
		scheme = SchemeSCPLike
		// transform the ":" into a "/" to rely on a unified logic after
		// works because "git@github.com:user" is equivalent to "ssh://git@ref/user"
		schemelessRef = strings.Replace(schemelessRef, ":", "/", 1)
	}

	// Trick:
	// as we removed the scheme above with `parseScheme``, and the SCP-like refs are
	// now without ":", all refs are in such format: "[git@]github.com/user/path...@version"
	// transport.NewEndpoint parses users only for SSH refs. As HTTP refs without scheme are valid SSH refs
	// we use the "ssh://" prefix to parse properly both explicit / SCP-like and HTTP refs
	// and delegate the logic to parse the host / path and user to the library
	endpoint, err := transport.NewEndpoint("ssh://" + schemelessRef)
	if err != nil {
		return ParsedGit{}, EndpointError{Err: fmt.Errorf("failed to create git endpoint: %w", err)}
	}

	gitParsed := ParsedGit{
		ModPath: endpoint.Host + endpoint.Path,
		Scheme:  scheme,
	}

	parts := strings.SplitN(endpoint.Path, "@", 2)
	if len(parts) == 2 {
		gitParsed.ModPath = endpoint.Host + parts[0]
		gitParsed.ModVersion = parts[1]
		gitParsed.HasVersion = true
	}

	// Try to isolate the root of the git repo
	// RepoRootForImportPath does not support SCP-like ref style. In parseGitEndpoint, we made sure that all refs
	// would be compatible with this function to benefit from the repo URL and root splitting
	repoRoot, err := vcs.RepoRootForImportPath(gitParsed.ModPath, false)
	if err != nil {
		return ParsedGit{}, EndpointError{Err: fmt.Errorf("failed to get repo root for import path: %w", err)}
	}
	if repoRoot == nil || repoRoot.VCS == nil {
		return ParsedGit{}, fmt.Errorf("invalid repo root for import path: %s", gitParsed.ModPath)
	}
	if repoRoot.VCS.Name != "Git" {
		return ParsedGit{}, fmt.Errorf("repo root is not a Git repo: %s", gitParsed.ModPath)
	}

	gitParsed.RepoRoot = repoRoot

	// the extra "/" trim is important as subpath traversal such as /../ are being cleaned by filePath.Clean
	gitParsed.RepoRootSubdir = strings.TrimPrefix(strings.TrimPrefix(gitParsed.ModPath, repoRoot.Root), "/")
	if gitParsed.RepoRootSubdir == "" {
		gitParsed.RepoRootSubdir = "/"
	}
	gitParsed.RepoRootSubdir = filepath.Clean(gitParsed.RepoRootSubdir)
	if !filepath.IsAbs(gitParsed.RepoRootSubdir) && !filepath.IsLocal(gitParsed.RepoRootSubdir) {
		return ParsedGit{}, fmt.Errorf("git module source subpath points out of root: %q", gitParsed.RepoRootSubdir)
	}

	// Restore SCPLike ref format
	if gitParsed.Scheme == SchemeSCPLike {
		gitParsed.RepoRoot.Root = strings.Replace(gitParsed.RepoRoot.Root, "/", ":", 1)
	}

	gitParsed.SourceUser, gitParsed.CloneUser = endpoint.User, endpoint.User
	if gitParsed.CloneUser == "" && gitParsed.Scheme.IsSSH() {
		gitParsed.CloneUser = "git"
	}
	sourceUser := gitParsed.SourceUser
	if sourceUser != "" {
		sourceUser += "@"
	}
	cloneUser := gitParsed.CloneUser
	if cloneUser != "" {
		cloneUser += "@"
	}

	// For SSH URLs, inject port after host if it is defined: ssh://user@host:port/path
	repoRootWithPort := gitParsed.RepoRoot.Root
	if gitParsed.Scheme == SchemeSSH && endpoint.Port > 0 {
		if host, rest, ok := strings.Cut(repoRootWithPort, "/"); ok {
			repoRootWithPort = fmt.Sprintf("%s:%d/%s", host, endpoint.Port, rest)
		}
	}

	gitParsed.SourceCloneRef = gitParsed.Scheme.Prefix() + sourceUser + repoRootWithPort
	gitParsed.CloneRef = gitParsed.Scheme.Prefix() + cloneUser + repoRootWithPort

	return gitParsed, nil
}

func GitString(cloneRef, sourceRootSubpath, version string) string {
	refPath := cloneRef
	subPath := filepath.Join("/", sourceRootSubpath)
	if subPath != "/" {
		refPath += subPath
	}
	if version != "" {
		refPath += "@" + version
	}
	return refPath
}

func isSCPLike(ref string) bool {
	return strings.Contains(ref, ":") && !strings.Contains(ref, "//")
}

func parseScheme(refString string) (SchemeType, string) {
	schemes := []SchemeType{
		SchemeHTTP,
		SchemeHTTPS,
		SchemeSSH,
	}

	for _, scheme := range schemes {
		prefix := scheme.Prefix()
		if strings.HasPrefix(refString, prefix) {
			return scheme, strings.TrimPrefix(refString, prefix)
		}
	}

	return NoScheme, refString
}
