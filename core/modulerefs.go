package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
)

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
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("parseRefString: %s", refString), telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)

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
	parsedGitRef, err := ParseGitRefString(ctx, refString)
	switch {
	case err == nil:
		return &ParsedRefString{
			Kind: ModuleSourceKindGit,
			Git:  &parsedGitRef,
		}, nil
	case errors.As(err, &gitref.EndpointError{}):
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

// ParsedGitRefString pairs the pure parsed git-ref data (gitref.Parsed) with
// the dagql-aware GitRef resolution that needs the engine schema.
type ParsedGitRefString struct {
	gitref.Parsed
}

func ParseGitRefString(ctx context.Context, refString string) (ParsedGitRefString, error) {
	parsed, err := gitref.Parse(ctx, refString)
	return ParsedGitRefString{parsed}, err
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

	var modTag string
	if p.HasVersion && semver.IsValid(p.ModVersion) {
		var tags dagql.Array[dagql.String]
		err := dag.Select(ctx, dag.Root(), &tags,
			dagql.Selector{
				Field: "git",
				Args: []dagql.NamedInput{
					{Name: "url", Value: dagql.String(p.CloneRef)},
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
			{Name: "url", Value: dagql.String(p.CloneRef)},
		},
	}
	repoSelector = withCommitArg(repoSelector)

	refSelector := dagql.Selector{Field: "head"}
	switch {
	case modTag != "":
		refSelector = withCommitArg(dagql.Selector{
			Field: "tag",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(modTag)},
			},
		})
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
