package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
)

// ErrModuleVersionNotFound indicates that a requested module version does not
// match any tag in its Git repository.
var ErrModuleVersionNotFound = errors.New("module version not found")

// FastModuleSourceKindCheck performs a quick heuristic check to determine
// whether a module ref string refers to a local path or a git source.
// Returns "" if the kind cannot be determined without further inspection.
func FastModuleSourceKindCheck(
	refString string,
	refPin string,
) ModuleSourceKind {
	switch gitref.FastKindCheck(refString, refPin) {
	case gitref.KindLocal:
		return ModuleSourceKindLocal
	case gitref.KindGit:
		return ModuleSourceKindGit
	default:
		return ""
	}
}

// GitRefString builds a module ref string from a clone ref, an optional source
// root subpath and an optional version.
func GitRefString(cloneRef, sourceRootSubpath, version string) string {
	return gitref.RefString(cloneRef, sourceRootSubpath, version)
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
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("parseRefString: %s", gitref.DisplayRef(refString)), telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)
	originalRef := refString

	kind := FastModuleSourceKindCheck(refString, refPin)
	switch kind {
	case ModuleSourceKindLocal:
		return &ParsedRefString{
			Kind: kind,
			Local: &ParsedLocalRefString{
				ModPath: refString,
			},
		}, nil
	case ModuleSourceKindGit:
		refString, err := ResolveDaggerGetRedirect(ctx, refString)
		if err != nil {
			return nil, fmt.Errorf("resolve remote module %q: %w", gitref.DisplayRef(originalRef), err)
		}
		parsedGitRef, err := ParseGitRefString(ctx, refString)
		if err != nil {
			return nil, fmt.Errorf("resolve remote module %q: %w", gitref.DisplayRef(originalRef), err)
		}
		return &ParsedRefString{
			Kind: kind,
			Git:  &parsedGitRef,
		}, nil
	}

	// First, we stat ref in case the mod path github.com/username is a local directory
	if _, stat, err := statFS.Stat(ctx, refString); err != nil {
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
	refString, err := ResolveDaggerGetRedirect(ctx, refString)
	if err != nil {
		return nil, fmt.Errorf("resolve remote module %q: %w", gitref.DisplayRef(originalRef), err)
	}
	parsedGitRef, err := ParseGitRefString(ctx, refString)
	switch {
	case err == nil:
		return &ParsedRefString{
			Kind: ModuleSourceKindGit,
			Git:  &parsedGitRef,
		}, nil
	case errors.As(err, &gitref.EndpointError{}) && !gitref.LooksRemote(originalRef):
		// couldn't connect to git endpoint, fallback to local
		return &ParsedRefString{
			Kind: ModuleSourceKindLocal,
			Local: &ParsedLocalRefString{
				ModPath: originalRef,
			},
		}, nil
	default:
		return nil, fmt.Errorf("resolve remote module %q: %w", gitref.DisplayRef(originalRef), err)
	}
}

type ParsedLocalRefString struct {
	ModPath string
}

// ParsedGitRefString pairs the pure parsed git-ref data (gitref.Parsed) with
// the dagql-aware GitRef resolution that needs the engine schema.
type ParsedGitRefString struct {
	gitref.Parsed
}

func ParseGitRefString(ctx context.Context, refString string) (ParsedGitRefString, error) {
	parsed, err := gitref.Parse(ctx, refString)
	return ParsedGitRefString{parsed}, err
}

// SetVersion sets a separate version query on a parsed Git module reference.
func (p *ParsedGitRefString) SetVersion(version string) error {
	if version == "" {
		return nil
	}
	if p.HasVersion {
		return fmt.Errorf(
			"version query %q cannot be used because the module source ref already has version %q",
			version,
			p.ModVersion,
		)
	}
	if _, err := parseReleaseVersionQuery(version); err != nil {
		return err
	}
	p.ModVersion = version
	p.HasVersion = true
	p.Selector = gitref.ModuleVersionSelector
	return nil
}

func (p *ParsedGitRefString) GitRef(
	ctx context.Context,
	dag *dagql.Server,
	pinCommitRef string, // "" if none
) (inst dagql.ObjectResult[*GitRef], rerr error) {
	pinIsSHA := gitutil.IsCommitSHA(pinCommitRef)

	withCommitArg := func(selector dagql.Selector) dagql.Selector {
		if pinIsSHA {
			selector.Args = append(selector.Args, dagql.NamedInput{Name: "commit", Value: dagql.String(pinCommitRef)})
		}
		return selector
	}

	versionQuery := p.versionQuery(ctx)

	repoSelector := dagql.Selector{
		Field: "git",
		Args: []dagql.NamedInput{
			{Name: "url", Value: dagql.String(p.CloneRef)},
		},
	}
	repoSelector = withCommitArg(repoSelector)

	refSelector := moduleGitDefaultRefSelector(ctx, p)
	switch {
	case versionQuery != "":
		args := []dagql.NamedInput{
			{Name: "version", Value: dagql.String(versionQuery)},
		}
		if p.RepoRootSubdir != "/" {
			args = append(args, dagql.NamedInput{
				Name:  "tagPrefix",
				Value: dagql.String(strings.Trim(p.RepoRootSubdir, "/")),
			})
		}
		refSelector = dagql.Selector{Field: "latest", Args: args}
	case p.HasVersion:
		refSelector = withCommitArg(dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(p.ModVersion)},
			},
		})
	case pinCommitRef != "" && !pinIsSHA:
		refSelector = dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(pinCommitRef)},
			},
		}
	case pinIsSHA:
		refSelector = withCommitArg(dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String("HEAD")},
			},
		})
	}

	var gitRef dagql.ObjectResult[*GitRef]
	err := dag.Select(ctx, dag.Root(), &gitRef, repoSelector, refSelector)
	if err != nil {
		return inst, fmt.Errorf("failed to resolve git src: %w", err)
	}
	if versionQuery != "" && pinIsSHA && gitRef.Self().Ref.SHA != pinCommitRef {
		// A normal load must satisfy both the version resolution and the module
		// pin. It cannot rewrite either value. In contrast, dagger update is an
		// explicit request to refresh the lock, so it accepts a moved tag after
		// it warns the user.
		return inst, fmt.Errorf(
			"version query %q resolved to Git ref %q at commit %q, but the requested pin is %q",
			versionQuery,
			gitRef.Self().Ref.Name,
			gitRef.Self().Ref.SHA,
			pinCommitRef,
		)
	}

	return gitRef, nil
}

func (p *ParsedGitRefString) versionQuery(ctx context.Context) string {
	if p.Selector == gitref.ModuleVersionSelector &&
		Supports(ctx, workspace.VersionQueriesVersion) &&
		IsReleaseVersionQuery(p.ModVersion) {
		return p.ModVersion
	}
	return ""
}

func moduleGitDefaultRefSelector(
	ctx context.Context,
	p *ParsedGitRefString,
) dagql.Selector {
	if !Supports(ctx, workspace.LatestReleaseVersion) {
		return dagql.Selector{Field: "head"}
	}

	selector := dagql.Selector{Field: "latest"}
	if p.RepoRootSubdir != "/" {
		selector.Args = append(selector.Args, dagql.NamedInput{
			Name:  "tagPrefix",
			Value: dagql.String(strings.Trim(p.RepoRootSubdir, "/")),
		})
	}
	return selector
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
	return "", fmt.Errorf("%w: %s", ErrModuleVersionNotFound, match)
}
