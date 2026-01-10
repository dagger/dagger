package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"golang.org/x/mod/semver"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/vcs"
)

func fastModuleSourceKindCheck(
	refString string,
	refPin string,
) ModuleSourceKind {
	switch {
	case refPin != "":
		return ModuleSourceKindGit
	case len(refString) > 0 && (refString[0] == '/' || refString[0] == '.'):
		return ModuleSourceKindLocal
	case len(refString) > 1 && refString[0:2] == "..":
		return ModuleSourceKindLocal
	case strings.HasPrefix(refString, SchemeHTTP.Prefix()):
		return ModuleSourceKindGit
	case strings.HasPrefix(refString, SchemeHTTPS.Prefix()):
		return ModuleSourceKindGit
	case strings.HasPrefix(refString, SchemeSSH.Prefix()):
		return ModuleSourceKindGit
	case !strings.Contains(refString, "."):
		// technically host names can not have any dot, but we can save a lot of work
		// by assuming a dot-free ref string is a local path. Users can prefix
		// args with a scheme:// to disambiguate these obscure corner cases.
		return ModuleSourceKindLocal
	default:
		return ""
	}
}

type ParsedRefString struct {
	Kind  ModuleSourceKind
	Local *ParsedLocalRefString
	Git   *ParsedGitRefString
}

func ParseRefString(
	ctx context.Context,
	statFS StatFS,
	refString string,
	refPin string,
) (_ *ParsedRefString, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("parseRefString: %s", refString), telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)

	kind := fastModuleSourceKindCheck(refString, refPin)
	switch kind {
	case ModuleSourceKindLocal:
		return &ParsedRefString{
			Kind: kind,
			Local: &ParsedLocalRefString{
				ModPath: refString,
			},
		}, nil
	case ModuleSourceKindGit:
		parsedGitRef, err := ParseGitRefString(ctx, refString)
		if err != nil {
			return nil, fmt.Errorf("failed to parse git ref string: %w", err)
		}
		return &ParsedRefString{
			Kind: kind,
			Git:  &parsedGitRef,
		}, nil
	}

	// First, we stat ref in case the mod path github.com/username is a local directory
	if stat, err := statFS.Stat(ctx, refString); err != nil {
		slog.Debug("parseRefString stat error", "error", err)
	} else if stat.IsDir() {
		return &ParsedRefString{
			Kind: ModuleSourceKindLocal,
			Local: &ParsedLocalRefString{
				ModPath: refString,
			},
		}, nil
	}

	// Parse scheme and attempt to parse as git endpoint
	parsedGitRef, err := ParseGitRefString(ctx, refString)
	switch {
	case err == nil:
		return &ParsedRefString{
			Kind: ModuleSourceKindGit,
			Git:  &parsedGitRef,
		}, nil
	case errors.As(err, &gitEndpointError{}):
		// couldn't connect to git endpoint, fallback to local
		return &ParsedRefString{
			Kind: ModuleSourceKindLocal,
			Local: &ParsedLocalRefString{
				ModPath: refString,
			},
		}, nil
	default:
		return nil, fmt.Errorf("failed to parse ref string: %w", err)
	}
}

type ParsedLocalRefString struct {
	ModPath string
}

type ParsedGitRefString struct {
	modPath string

	ModVersion string
	hasVersion bool

	RepoRoot       *vcs.RepoRoot
	RepoRootSubdir string

	scheme SchemeType

	sourceUser     string
	cloneUser      string
	SourceCloneRef string // original user-provided username
	cloneRef       string // resolved username
}

type gitEndpointError struct{ error }

func ParseGitRefString(ctx context.Context, refString string) (_ ParsedGitRefString, rerr error) {
	_, span := Tracer(ctx).Start(ctx, fmt.Sprintf("parseGitRefString: %s", refString), telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)

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
		return ParsedGitRefString{}, gitEndpointError{fmt.Errorf("failed to create git endpoint: %w", err)}
	}

	gitParsed := ParsedGitRefString{
		modPath: endpoint.Host + endpoint.Path,
		scheme:  scheme,
	}

	parts := strings.SplitN(endpoint.Path, "@", 2)
	if len(parts) == 2 {
		gitParsed.modPath = endpoint.Host + parts[0]
		gitParsed.ModVersion = parts[1]
		gitParsed.hasVersion = true
	}

	// Try to isolate the root of the git repo
	// RepoRootForImportPath does not support SCP-like ref style. In parseGitEndpoint, we made sure that all refs
	// would be compatible with this function to benefit from the repo URL and root splitting
	repoRoot, err := vcs.RepoRootForImportPath(gitParsed.modPath, false)
	if err != nil {
		return ParsedGitRefString{}, gitEndpointError{fmt.Errorf("failed to get repo root for import path: %w", err)}
	}
	if repoRoot == nil || repoRoot.VCS == nil {
		return ParsedGitRefString{}, fmt.Errorf("invalid repo root for import path: %s", gitParsed.modPath)
	}
	if repoRoot.VCS.Name != "Git" {
		return ParsedGitRefString{}, fmt.Errorf("repo root is not a Git repo: %s", gitParsed.modPath)
	}

	gitParsed.RepoRoot = repoRoot

	// the extra "/" trim is important as subpath traversal such as /../ are being cleaned by filePath.Clean
	gitParsed.RepoRootSubdir = strings.TrimPrefix(strings.TrimPrefix(gitParsed.modPath, repoRoot.Root), "/")
	if gitParsed.RepoRootSubdir == "" {
		gitParsed.RepoRootSubdir = "/"
	}
	gitParsed.RepoRootSubdir = filepath.Clean(gitParsed.RepoRootSubdir)
	if !filepath.IsAbs(gitParsed.RepoRootSubdir) && !filepath.IsLocal(gitParsed.RepoRootSubdir) {
		return ParsedGitRefString{}, fmt.Errorf("git module source subpath points out of root: %q", gitParsed.RepoRootSubdir)
	}

	// Restore SCPLike ref format
	if gitParsed.scheme == SchemeSCPLike {
		gitParsed.RepoRoot.Root = strings.Replace(gitParsed.RepoRoot.Root, "/", ":", 1)
	}

	gitParsed.sourceUser, gitParsed.cloneUser = endpoint.User, endpoint.User
	if gitParsed.cloneUser == "" && gitParsed.scheme.IsSSH() {
		gitParsed.cloneUser = "git"
	}
	sourceUser := gitParsed.sourceUser
	if sourceUser != "" {
		sourceUser += "@"
	}
	cloneUser := gitParsed.cloneUser
	if cloneUser != "" {
		cloneUser += "@"
	}

	// For SSH URLs, inject port after host if it is defined: ssh://user@host:port/path
	repoRootWithPort := gitParsed.RepoRoot.Root
	if endpoint.Port > 0 && gitParsed.scheme == SchemeSSH {
		if idx := strings.Index(repoRootWithPort, "/"); idx != -1 {
			repoRootWithPort = fmt.Sprintf("%s:%d%s", repoRootWithPort[:idx], endpoint.Port, repoRootWithPort[idx:])
		}
	}

	gitParsed.SourceCloneRef = gitParsed.scheme.Prefix() + sourceUser + repoRootWithPort
	gitParsed.cloneRef = gitParsed.scheme.Prefix() + cloneUser + repoRootWithPort

	return gitParsed, nil
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

func (p *ParsedGitRefString) GitRef(
	ctx context.Context,
	dag *dagql.Server,
	pinCommitRef string, // "" if none
) (inst dagql.ObjectResult[*GitRef], rerr error) {
	var modTag string
	if p.hasVersion && semver.IsValid(p.ModVersion) {
		var tags dagql.Array[dagql.String]
		err := dag.Select(ctx, dag.Root(), &tags,
			dagql.Selector{
				Field: "git",
				Args: []dagql.NamedInput{
					{Name: "url", Value: dagql.String(p.cloneRef)},
				},
			},
			dagql.Selector{
				Field: "tags",
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to resolve git tags: %w", err)
		}

		allTags := make([]string, len(tags))
		for i, tag := range tags {
			allTags[i] = tag.String()
		}

		matched, err := matchVersion(allTags, p.ModVersion, p.RepoRootSubdir)
		if err != nil {
			return inst, fmt.Errorf("matching version to tags: %w", err)
		}
		modTag = matched
	}

	repoSelector := dagql.Selector{
		Field: "git",
		Args: []dagql.NamedInput{
			{Name: "url", Value: dagql.String(p.cloneRef)},
		},
	}
	if pinCommitRef != "" {
		repoSelector.Args = append(repoSelector.Args, dagql.NamedInput{Name: "commit", Value: dagql.String(pinCommitRef)})
	}

	var refSelector dagql.Selector
	switch {
	case modTag != "":
		refSelector = dagql.Selector{
			Field: "tag",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(modTag)},
			},
		}
		if pinCommitRef != "" {
			refSelector.Args = append(refSelector.Args, dagql.NamedInput{Name: "commit", Value: dagql.String(pinCommitRef)})
		}
	case p.hasVersion:
		refSelector = dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(p.ModVersion)},
			},
		}
		if pinCommitRef != "" {
			refSelector.Args = append(refSelector.Args, dagql.NamedInput{Name: "commit", Value: dagql.String(pinCommitRef)})
		}
	default:
		refSelector = dagql.Selector{
			Field: "head",
		}
	}
	var gitRef dagql.ObjectResult[*GitRef]
	err := dag.Select(ctx, dag.Root(), &gitRef, repoSelector, refSelector)
	if err != nil {
		return inst, fmt.Errorf("failed to resolve git src: %w", err)
	}

	return gitRef, nil
}

// Match a version string in a list of versions with optional subPath
// e.g. github.com/foo/daggerverse/mod@mod/v1.0.0
// e.g. github.com/foo/mod@v1.0.0
// TODO smarter matching logic, e.g. v1 == v1.0.0
func matchVersion(versions []string, match, subPath string) (string, error) {
	// If theres a subPath, first match on {subPath}/{match} for monorepo tags
	if subPath != "/" {
		rawSubPath, _ := strings.CutPrefix(subPath, "/")
		matched, err := matchVersion(versions, fmt.Sprintf("%s/%s", rawSubPath, match), "/")
		// no error means there's a match with subpath/match
		if err == nil {
			return matched, nil
		}
	}

	for _, v := range versions {
		if v == match {
			return v, nil
		}
	}
	return "", fmt.Errorf("unable to find version %s", match)
}
