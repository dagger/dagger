// Package gitref holds the pure, dependency-light logic for classifying and
// parsing module reference strings (local path vs git, scheme detection, git
// repo-root resolution). It deliberately avoids importing the engine "core"
// package (and the Linux-only engine code that comes with it) so that the CLI
// can reuse this logic and still cross-compile for darwin/windows.
//
// The richer, dagql-aware wrappers (ModuleSourceKind enum, ParsedGitRefString
// with its GitRef resolution) live in package core and delegate here.
package gitref

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/vcs"
	telemetry "github.com/dagger/otel-go"
)

// Kind is a quick classification of a module ref string.
type Kind int

const (
	// KindUnknown means the kind could not be determined by a fast heuristic
	// and further inspection (e.g. statting the filesystem) is required.
	KindUnknown Kind = iota
	KindLocal
	KindGit
)

// SchemeType is the URL scheme of a git ref string.
type SchemeType int

const (
	NoScheme SchemeType = iota
	SchemeHTTP
	SchemeHTTPS
	SchemeGit
	SchemeSSH
	SchemeSCPLike
)

// SelectorType describes how a ref selects a revision and source subpath.
type SelectorType int

const (
	NoSelector SelectorType = iota
	// ModuleVersionSelector is the Go-like import path form: path/to/module@ref.
	// SemVer-shaped refs in this form may be resolved as version queries.
	ModuleVersionSelector
	// GitRefSelector is the Git URL form: protocol://repo#ref[:subpath]. Its ref
	// is always resolved literally, even when it looks like a SemVer query.
	GitRefSelector
)

func (s SchemeType) Prefix() string {
	switch s {
	case SchemeHTTP:
		return "http://"
	case SchemeHTTPS:
		return "https://"
	case SchemeGit:
		return "git://"
	case SchemeSSH:
		return "ssh://"
	default:
		return ""
	}
}

func (s SchemeType) IsSSH() bool {
	return s == SchemeSSH
}

// RefString builds a module ref string from a clone ref, an optional source
// root subpath and an optional version.
func RefString(cloneRef, sourceRootSubpath, version string) string {
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

// GitURLRefString builds the explicit Git URL form protocol://repo#ref[:subpath].
func GitURLRefString(cloneRef, sourceRootSubpath, version string) string {
	subpath := filepath.ToSlash(filepath.Clean(sourceRootSubpath))
	subpath = strings.TrimPrefix(subpath, "/")
	if subpath == "" || subpath == "." {
		return cloneRef + "#" + version
	}
	return cloneRef + "#" + version + ":" + subpath
}

// FastKindCheck performs a quick heuristic check to determine whether a module
// ref string refers to a local path or a git source. Returns KindUnknown if the
// kind cannot be determined without further inspection.
func FastKindCheck(refString, refPin string) Kind {
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
	case strings.HasPrefix(refString, SchemeGit.Prefix()):
		return KindGit
	case strings.HasPrefix(refString, SchemeSSH.Prefix()):
		return KindGit
	case !strings.Contains(refString, "."):
		// technically host names can not have any dot, but we can save a lot of work
		// by assuming a dot-free ref string is a local path. Users can prefix
		// args with a scheme:// to disambiguate these obscure corner cases.
		return KindLocal
	default:
		return KindUnknown
	}
}

// Parsed holds the parsed components of a git ref string.
type Parsed struct {
	ModPath string

	ModVersion string
	HasVersion bool
	Selector   SelectorType

	RepoRoot       *vcs.RepoRoot
	RepoRootSubdir string

	Scheme SchemeType

	SourceUser     string
	CloneUser      string
	SourceCloneRef string // original user-provided username
	CloneRef       string // resolved username
}

// EndpointError indicates the ref could not be parsed/resolved as a git
// endpoint (callers may choose to fall back to treating it as a local path).
type EndpointError struct{ error }

func (e EndpointError) Unwrap() error { return e.error }

// Parse parses a git ref string into its components.
func Parse(ctx context.Context, refString string) (_ Parsed, rerr error) {
	tracer := trace.SpanFromContext(ctx).TracerProvider().Tracer("dagger.io/core/gitref")
	_, span := tracer.Start(ctx, fmt.Sprintf("parseGitRefString: %s", DisplayRef(refString)), telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)

	scheme, schemelessRef := parseScheme(refString)
	fragmentRef, fragmentSubdir, hasFragment, err := parseGitFragmentSelector(scheme, schemelessRef)
	if err != nil {
		return Parsed{}, err
	}
	if hasFragment {
		schemelessRef, _, _ = strings.Cut(schemelessRef, "#")
	}

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
		return Parsed{}, EndpointError{fmt.Errorf("failed to create git endpoint: %w", err)}
	}

	gitParsed := Parsed{
		ModPath: endpoint.Host + endpoint.Path,
		Scheme:  scheme,
	}

	if modulePath, version, ok := strings.Cut(endpoint.Path, "@"); ok {
		if version == "" || strings.Contains(version, ":") {
			return Parsed{}, fmt.Errorf("invalid module version selector %q", version)
		}
		gitParsed.ModPath = endpoint.Host + modulePath
		gitParsed.ModVersion = version
		gitParsed.HasVersion = true
		gitParsed.Selector = ModuleVersionSelector
	} else if hasFragment {
		gitParsed.ModVersion = fragmentRef
		gitParsed.HasVersion = true
		gitParsed.Selector = GitRefSelector
	}

	// Go-like module refs use import-path discovery to split the repository from
	// its module subpath. The explicit #ref:subpath form already provides that
	// boundary, so it can accept any Git URL without go-import metadata.
	var repoRoot *vcs.RepoRoot
	if hasFragment {
		repoRoot = explicitGitRepoRoot(gitParsed.ModPath, scheme, endpoint)
		if knownRoot, staticErr := vcs.RepoRootForImportPathStatic(gitParsed.ModPath, ""); staticErr == nil {
			if knownRoot.Root != gitParsed.ModPath {
				return Parsed{}, fmt.Errorf(
					"git URL selector requires a repository URL before #: got %q, repository root is %q",
					gitParsed.ModPath,
					knownRoot.Root,
				)
			}
			repoRoot = knownRoot
		}
	} else {
		repoRoot, err = vcs.RepoRootForImportPath(gitParsed.ModPath, false)
		if err != nil {
			return Parsed{}, EndpointError{fmt.Errorf("failed to get repo root for import path: %w", err)}
		}
	}
	if repoRoot == nil || repoRoot.VCS == nil {
		return Parsed{}, fmt.Errorf("invalid repo root for import path: %s", gitParsed.ModPath)
	}
	if repoRoot.VCS.Name != "Git" {
		return Parsed{}, fmt.Errorf("repo root is not a Git repo: %s", gitParsed.ModPath)
	}

	gitParsed.RepoRoot = repoRoot

	// the extra "/" trim is important as subpath traversal such as /../ are being cleaned by filePath.Clean
	if hasFragment {
		gitParsed.RepoRootSubdir = fragmentSubdir
	} else {
		gitParsed.RepoRootSubdir = strings.TrimPrefix(strings.TrimPrefix(gitParsed.ModPath, repoRoot.Root), "/")
	}
	if gitParsed.RepoRootSubdir == "" {
		gitParsed.RepoRootSubdir = "/"
	}
	gitParsed.RepoRootSubdir = filepath.Clean(gitParsed.RepoRootSubdir)
	if !filepath.IsAbs(gitParsed.RepoRootSubdir) && !filepath.IsLocal(gitParsed.RepoRootSubdir) {
		return Parsed{}, fmt.Errorf("git module source subpath points out of root: %q", gitParsed.RepoRootSubdir)
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

func isSCPLike(ref string) bool {
	colon := strings.IndexByte(ref, ':')
	if colon < 0 || strings.Contains(ref, "//") {
		return false
	}
	slash := strings.IndexByte(ref, '/')
	return slash < 0 || colon < slash
}

func parseGitFragmentSelector(scheme SchemeType, ref string) (gitRef, subdir string, ok bool, err error) {
	_, fragment, ok := strings.Cut(ref, "#")
	if !ok {
		return "", "", false, nil
	}
	if scheme == NoScheme {
		return "", "", false, errors.New("git URL selector requires an explicit protocol")
	}
	if strings.Contains(fragment, "#") {
		return "", "", false, errors.New("git URL selector contains multiple # delimiters")
	}
	gitRef, subdir, hasSubdir := strings.Cut(fragment, ":")
	if gitRef == "" {
		return "", "", false, errors.New("git URL selector requires a ref after #")
	}
	if hasSubdir && subdir == "" {
		return "", "", false, errors.New("git URL selector has an empty subpath after colon")
	}
	return gitRef, subdir, true, nil
}

func explicitGitRepoRoot(modPath string, scheme SchemeType, endpoint *transport.Endpoint) *vcs.RepoRoot {
	webScheme := scheme
	if webScheme != SchemeHTTP && webScheme != SchemeHTTPS {
		webScheme = SchemeHTTPS
	}
	host := endpoint.Host
	if endpoint.Port > 0 && (scheme == SchemeHTTP || scheme == SchemeHTTPS) {
		host = fmt.Sprintf("%s:%d", host, endpoint.Port)
	}
	return &vcs.RepoRoot{
		VCS:  vcs.ByCmd("git"),
		Root: modPath,
		Repo: webScheme.Prefix() + host + strings.TrimSuffix(endpoint.Path, ".git"),
	}
}

// Scheme classifies the transport scheme of a ref string, including SCP-like
// ("git@host:path") refs. It is a pure, network-free helper so callers can
// decide eligibility (e.g. for the https-only dagger-get redirect probe)
// without importing the engine.
func Scheme(refString string) SchemeType {
	scheme, schemeless := parseScheme(refString)
	if scheme == NoScheme && isSCPLike(schemeless) {
		return SchemeSCPLike
	}
	return scheme
}

func parseScheme(refString string) (SchemeType, string) {
	schemes := []SchemeType{
		SchemeHTTP,
		SchemeHTTPS,
		SchemeGit,
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
