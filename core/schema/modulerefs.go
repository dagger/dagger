package schema

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/vcs"
)

func fastModuleSourceKindCheck(
	refString string,
	refPin string,
) core.ModuleSourceKind {
	switch {
	case refPin != "":
		return core.ModuleSourceKindGit
	case len(refString) > 0 && (refString[0] == '/' || refString[0] == '.'):
		return core.ModuleSourceKindLocal
	case len(refString) > 1 && refString[0:2] == "..":
		return core.ModuleSourceKindLocal
	case strings.HasPrefix(refString, core.SchemeHTTP.Prefix()):
		return core.ModuleSourceKindGit
	case strings.HasPrefix(refString, core.SchemeHTTPS.Prefix()):
		return core.ModuleSourceKindGit
	case strings.HasPrefix(refString, core.SchemeSSH.Prefix()):
		return core.ModuleSourceKindGit
	case !strings.Contains(refString, "."):
		// technically host names can not have any dot, but we can save a lot of work
		// by assuming a dot-free ref string is a local path. Users can prefix
		// args with a scheme:// to disambiguate these obscure corner cases.
		return core.ModuleSourceKindLocal
	default:
		return ""
	}
}

type parsedRefString struct {
	kind  core.ModuleSourceKind
	local *parsedLocalRefString
	git   *parsedGitRefString
}

func parseRefString(
	ctx context.Context,
	statFS statFS,
	refString string,
	refPin string,
) (_ *parsedRefString, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("parseRefString: %s", refString), telemetry.Internal())
	defer telemetry.End(span, func() error { return rerr })

	kind := fastModuleSourceKindCheck(refString, refPin)
	switch kind {
	case core.ModuleSourceKindLocal:
		return &parsedRefString{
			kind: kind,
			local: &parsedLocalRefString{
				modPath: refString,
			},
		}, nil
	case core.ModuleSourceKindGit:
		parsedGitRef, err := parseGitRefString(ctx, refString)
		if err != nil {
			return nil, fmt.Errorf("failed to parse git ref string: %w", err)
		}
		return &parsedRefString{
			kind: kind,
			git:  &parsedGitRef,
		}, nil
	}

	// First, we stat ref in case the mod path github.com/username is a local directory
	if stat, err := statFS.stat(ctx, refString); err != nil {
		slog.Debug("parseRefString stat error", "error", err)
	} else if stat.IsDir() {
		return &parsedRefString{
			kind: core.ModuleSourceKindLocal,
			local: &parsedLocalRefString{
				modPath: refString,
			},
		}, nil
	}

	// Parse scheme and attempt to parse as git endpoint
	parsedGitRef, err := parseGitRefString(ctx, refString)
	switch {
	case err == nil:
		return &parsedRefString{
			kind: core.ModuleSourceKindGit,
			git:  &parsedGitRef,
		}, nil
	case errors.As(err, &gitEndpointError{}):
		// couldn't connect to git endpoint, fallback to local
		return &parsedRefString{
			kind: core.ModuleSourceKindLocal,
			local: &parsedLocalRefString{
				modPath: refString,
			},
		}, nil
	default:
		return nil, fmt.Errorf("failed to parse ref string: %w", err)
	}
}

type parsedLocalRefString struct {
	modPath string
}

type parsedGitRefString struct {
	modPath string

	modVersion string
	hasVersion bool

	repoRoot       *vcs.RepoRoot
	repoRootSubdir string

	scheme core.SchemeType

	sourceUser     string
	cloneUser      string
	sourceCloneRef string // original user-provided username
	cloneRef       string // resolved username
}

type gitEndpointError struct{ error }

func parseGitRefString(ctx context.Context, refString string) (_ parsedGitRefString, rerr error) {
	_, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("parseGitRefString: %s", refString), telemetry.Internal())
	defer telemetry.End(span, func() error { return rerr })

	scheme, schemelessRef := parseScheme(refString)

	if scheme == core.NoScheme && isSCPLike(schemelessRef) {
		scheme = core.SchemeSCPLike
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
		return parsedGitRefString{}, gitEndpointError{fmt.Errorf("failed to create git endpoint: %w", err)}
	}

	gitParsed := parsedGitRefString{
		modPath: endpoint.Host + endpoint.Path,
		scheme:  scheme,
	}

	parts := strings.SplitN(endpoint.Path, "@", 2)
	if len(parts) == 2 {
		gitParsed.modPath = endpoint.Host + parts[0]
		gitParsed.modVersion = parts[1]
		gitParsed.hasVersion = true
	}

	// Try to isolate the root of the git repo
	// RepoRootForImportPath does not support SCP-like ref style. In parseGitEndpoint, we made sure that all refs
	// would be compatible with this function to benefit from the repo URL and root splitting
	repoRoot, err := vcs.RepoRootForImportPath(gitParsed.modPath, false)
	if err != nil {
		return parsedGitRefString{}, gitEndpointError{fmt.Errorf("failed to get repo root for import path: %w", err)}
	}
	if repoRoot == nil || repoRoot.VCS == nil {
		return parsedGitRefString{}, fmt.Errorf("invalid repo root for import path: %s", gitParsed.modPath)
	}
	if repoRoot.VCS.Name != "Git" {
		return parsedGitRefString{}, fmt.Errorf("repo root is not a Git repo: %s", gitParsed.modPath)
	}

	gitParsed.repoRoot = repoRoot

	// the extra "/" trim is important as subpath traversal such as /../ are being cleaned by filePath.Clean
	gitParsed.repoRootSubdir = strings.TrimPrefix(strings.TrimPrefix(gitParsed.modPath, repoRoot.Root), "/")
	if gitParsed.repoRootSubdir == "" {
		gitParsed.repoRootSubdir = "/"
	}
	gitParsed.repoRootSubdir = filepath.Clean(gitParsed.repoRootSubdir)
	if !filepath.IsAbs(gitParsed.repoRootSubdir) && !filepath.IsLocal(gitParsed.repoRootSubdir) {
		return parsedGitRefString{}, fmt.Errorf("git module source subpath points out of root: %q", gitParsed.repoRootSubdir)
	}

	// Restore SCPLike ref format
	if gitParsed.scheme == core.SchemeSCPLike {
		gitParsed.repoRoot.Root = strings.Replace(gitParsed.repoRoot.Root, "/", ":", 1)
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

	gitParsed.sourceCloneRef = gitParsed.scheme.Prefix() + sourceUser + gitParsed.repoRoot.Root
	gitParsed.cloneRef = gitParsed.scheme.Prefix() + cloneUser + gitParsed.repoRoot.Root

	return gitParsed, nil
}

func isSCPLike(ref string) bool {
	return strings.Contains(ref, ":") && !strings.Contains(ref, "//")
}

func parseScheme(refString string) (core.SchemeType, string) {
	schemes := []core.SchemeType{
		core.SchemeHTTP,
		core.SchemeHTTPS,
		core.SchemeSSH,
	}

	for _, scheme := range schemes {
		prefix := scheme.Prefix()
		if strings.HasPrefix(refString, prefix) {
			return scheme, strings.TrimPrefix(refString, prefix)
		}
	}

	return core.NoScheme, refString
}

func (p *parsedGitRefString) getGitRefAndModVersion(
	ctx context.Context,
	dag *dagql.Server,
	pinCommitRef string, // "" if none
) (inst dagql.Instance[*core.GitRef], _ string, rerr error) {
	commitRef := pinCommitRef
	var modVersion string
	if p.hasVersion {
		modVersion = p.modVersion
		if isSemver(modVersion) {
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
				return inst, "", fmt.Errorf("failed to resolve git tags: %w", err)
			}

			allTags := make([]string, len(tags))
			for i, tag := range tags {
				allTags[i] = tag.String()
			}

			matched, err := matchVersion(allTags, modVersion, p.repoRootSubdir)
			if err != nil {
				return inst, "", fmt.Errorf("matching version to tags: %w", err)
			}
			modVersion = matched
		}
		if commitRef == "" {
			commitRef = modVersion
		}
	}

	var commitRefSelector dagql.Selector
	if commitRef == "" {
		commitRefSelector = dagql.Selector{
			Field: "head",
		}
	} else {
		commitRefSelector = dagql.Selector{
			Field: "commit",
			Args: []dagql.NamedInput{
				// reassign modVersion to matched tag which could be subPath/tag
				{Name: "id", Value: dagql.String(commitRef)},
			},
		}
	}

	var gitRef dagql.Instance[*core.GitRef]
	err := dag.Select(ctx, dag.Root(), &gitRef,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(p.cloneRef)},
			},
		},
		commitRefSelector,
	)
	if err != nil {
		return inst, "", fmt.Errorf("failed to resolve git src: %w", err)
	}

	return gitRef, modVersion, nil
}
