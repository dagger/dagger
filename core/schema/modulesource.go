package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/vcs"
	"github.com/tonistiigi/fsutil/types"
)

type moduleSourceArgs struct {
	// avoiding name "ref" due to that being a reserved word in some SDKs (e.g. Rust)
	RefString string

	Stable bool `default:"false"`
}

func (s *moduleSchema) moduleSource(ctx context.Context, query *core.Query, args moduleSourceArgs) (*core.ModuleSource, error) {
	parsed := parseRefString(ctx, query.Buildkit, args.RefString)

	if args.Stable && !parsed.hasVersion && parsed.kind == core.ModuleSourceKindGit {
		return nil, fmt.Errorf("no version provided for stable remote ref: %s", args.RefString)
	}

	src := &core.ModuleSource{
		Query: query,
		Kind:  parsed.kind,
	}

	switch src.Kind {
	case core.ModuleSourceKindLocal:
		if filepath.IsAbs(parsed.modPath) {
			return nil, fmt.Errorf("local module source root path is absolute: %s", parsed.modPath)
		}

		src.AsLocalSource = dagql.NonNull(&core.LocalModuleSource{
			RootSubpath: parsed.modPath,
		})

	case core.ModuleSourceKindGit:
		src.AsGitSource = dagql.NonNull(&core.GitModuleSource{})

		src.AsGitSource.Value.Root = parsed.repoRoot.Root
		src.AsGitSource.Value.CloneURL = parsed.repoRoot.Repo

		var modVersion string
		if parsed.hasVersion {
			modVersion = parsed.modVersion
		} else {
			if args.Stable {
				return nil, fmt.Errorf("no version provided for stable remote ref: %s", args.RefString)
			}
			var err error
			modVersion, err = defaultBranch(ctx, parsed.repoRoot.Repo)
			if err != nil {
				return nil, fmt.Errorf("determine default branch: %w", err)
			}
		}
		src.AsGitSource.Value.Version = modVersion

		subPath := "/"
		if parsed.repoRootSubdir != "" {
			subPath = parsed.repoRootSubdir
		}

		commitRef := modVersion
		if parsed.hasVersion && isSemver(modVersion) {
			allTags, err := gitTags(ctx, parsed.repoRoot.Repo)
			if err != nil {
				return nil, fmt.Errorf("get git tags: %w", err)
			}
			matched, err := matchVersion(allTags, modVersion, subPath)
			if err != nil {
				return nil, fmt.Errorf("matching version to tags: %w", err)
			}
			// reassign modVersion to matched tag which could be subPath/tag
			commitRef = matched
		}

		var gitRef dagql.Instance[*core.GitRef]
		err := s.dag.Select(ctx, s.dag.Root(), &gitRef,
			dagql.Selector{
				Field: "git",
				Args: []dagql.NamedInput{
					{Name: "url", Value: dagql.String(parsed.repoRoot.Repo)},
				},
			},
			dagql.Selector{
				Field: "commit",
				Args: []dagql.NamedInput{
					{Name: "id", Value: dagql.String(commitRef)},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve git src: %w", err)
		}
		gitCommit, err := gitRef.Self.Commit(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve git src to commit: %w", err)
		}
		src.AsGitSource.Value.Commit = gitCommit

		subPath = filepath.Clean(subPath)
		if !filepath.IsAbs(subPath) && !filepath.IsLocal(subPath) {
			return nil, fmt.Errorf("git module source subpath points out of root: %q", subPath)
		}
		if filepath.IsAbs(subPath) {
			subPath = strings.TrimPrefix(subPath, "/")
		}

		// TODO:(sipsma) support sparse loading of git repos similar to how local dirs are loaded.
		// Related: https://github.com/dagger/dagger/issues/6292
		err = s.dag.Select(ctx, gitRef, &src.AsGitSource.Value.ContextDirectory,
			dagql.Selector{Field: "tree"},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load git dir: %w", err)
		}

		// git source needs rootpath on itself too for constructing urls
		src.AsGitSource.Value.RootSubpath = subPath
	}

	return src, nil
}

type parsedRefString struct {
	modPath        string
	modVersion     string
	hasVersion     bool
	kind           core.ModuleSourceKind
	repoRoot       *vcs.RepoRoot
	repoRootSubdir string
}

// interface used for host interaction mocking
type BuildkitClient interface {
	StatCallerHostPath(ctx context.Context, path string, followLinks bool) (*types.Stat, error)
}

// parseRefString parses a ref string into its components
// New heuristic:
// - stat folder to see if dir is present
// - if not, try to isolate root of git repo from the ref
// - if nothing worked, fallback as local ref, as before
func parseRefString(ctx context.Context, bk BuildkitClient, refString string) parsedRefString {
	var parsed parsedRefString
	parsed.modPath, parsed.modVersion, parsed.hasVersion = strings.Cut(refString, "@")

	// We do a stat in case the mod path github.com/username is a local directory
	stat, err := bk.StatCallerHostPath(ctx, parsed.modPath, false)
	if err == nil {
		if !parsed.hasVersion && stat.IsDir() {
			parsed.kind = core.ModuleSourceKindLocal
			return parsed
		}
	}

	// we try to isolate the root of the git repo
	repoRoot, err := vcs.RepoRootForImportPath(parsed.modPath, false)
	if err == nil && repoRoot != nil && repoRoot.VCS != nil && repoRoot.VCS.Name == "Git" {
		parsed.kind = core.ModuleSourceKindGit
		parsed.repoRoot = repoRoot
		parsed.repoRootSubdir = strings.TrimPrefix(parsed.modPath, repoRoot.Root)
		return parsed
	}

	// log warning to hint that the remote ref fallbacked as a local source kind
	if err != nil {
		slog.Warn("ref %s has not been parsed as a git remote: %v", refString, err)
	}

	parsed.kind = core.ModuleSourceKindLocal
	return parsed
}

func (s *moduleSchema) moduleSourceAsModule(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) (inst dagql.Instance[*core.Module], err error) {
	err = s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "module",
		},
		dagql.Selector{
			Field: "withSource",
			Args: []dagql.NamedInput{
				{Name: "source", Value: dagql.NewID[*core.ModuleSource](src.ID())},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to create module: %w", err)
	}
	return inst, err
}

func (s *moduleSchema) moduleSourceAsString(ctx context.Context, src *core.ModuleSource, args struct{}) (string, error) {
	return src.RefString()
}

func (s *moduleSchema) gitModuleSourceCloneURL(
	ctx context.Context,
	ref *core.GitModuleSource,
	args struct{},
) (string, error) {
	return ref.CloneURL, nil
}

func (s *moduleSchema) gitModuleSourceHTMLURL(
	ctx context.Context,
	ref *core.GitModuleSource,
	args struct{},
) (string, error) {
	return ref.HTMLURL(), nil
}

func (s *moduleSchema) moduleSourceConfigExists(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (bool, error) {
	_, ok, err := src.ModuleConfig(ctx)
	return ok, err
}

func (s *moduleSchema) moduleSourceSubpath(ctx context.Context, src *core.ModuleSource, args struct{}) (string, error) {
	return src.SourceSubpath(ctx)
}

func (s *moduleSchema) moduleSourceWithSourceSubpath(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Path string
	},
) (*core.ModuleSource, error) {
	if args.Path == "" {
		return src, nil
	}
	if !filepath.IsLocal(args.Path) {
		return nil, fmt.Errorf("source subdir path %q escapes context", args.Path)
	}
	src = src.Clone()
	src.WithSourceSubpath = args.Path
	return src, nil
}

func (s *moduleSchema) moduleSourceRootSubpath(ctx context.Context, src *core.ModuleSource, args struct{}) (string, error) {
	return src.SourceRootSubpath()
}

func (s *moduleSchema) moduleSourceModuleName(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.ModuleName(ctx)
}

func (s *moduleSchema) moduleSourceModuleOriginalName(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.ModuleOriginalName(ctx)
}

func (s *moduleSchema) moduleSourceWithName(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Name string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()
	src.WithName = args.Name
	return src, nil
}

func (s *moduleSchema) moduleSourceDependencies(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) ([]dagql.Instance[*core.ModuleDependency], error) {
	modCfg, ok, err := src.Self.ModuleConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module config: %w", err)
	}

	var existingDeps []dagql.Instance[*core.ModuleDependency]
	if ok && len(modCfg.Dependencies) > 0 {
		existingDeps = make([]dagql.Instance[*core.ModuleDependency], len(modCfg.Dependencies))
		var eg errgroup.Group
		for i, depCfg := range modCfg.Dependencies {
			i, depCfg := i, depCfg
			eg.Go(func() error {
				var depSrc dagql.Instance[*core.ModuleSource]
				err := s.dag.Select(ctx, s.dag.Root(), &depSrc,
					dagql.Selector{
						Field: "moduleSource",
						Args: []dagql.NamedInput{
							{Name: "refString", Value: dagql.String(depCfg.Source)},
						},
					},
				)
				if err != nil {
					return fmt.Errorf("failed to create module source from dependency: %w", err)
				}

				var resolvedDepSrc dagql.Instance[*core.ModuleSource]
				err = s.dag.Select(ctx, src, &resolvedDepSrc,
					dagql.Selector{
						Field: "resolveDependency",
						Args: []dagql.NamedInput{
							{Name: "dep", Value: dagql.NewID[*core.ModuleSource](depSrc.ID())},
						},
					},
				)
				if err != nil {
					return fmt.Errorf("failed to resolve dependency: %w", err)
				}

				err = s.dag.Select(ctx, s.dag.Root(), &existingDeps[i],
					dagql.Selector{
						Field: "moduleDependency",
						Args: []dagql.NamedInput{
							{Name: "source", Value: dagql.NewID[*core.ModuleSource](resolvedDepSrc.ID())},
							{Name: "name", Value: dagql.String(depCfg.Name)},
						},
					},
				)
				if err != nil {
					return fmt.Errorf("failed to create module dependency: %w", err)
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, fmt.Errorf("failed to load pre-configured dependencies: %w", err)
		}
	}

	newDeps := make([]dagql.Instance[*core.ModuleDependency], len(src.Self.WithDependencies))
	var eg errgroup.Group
	for i, dep := range src.Self.WithDependencies {
		i, dep := i, dep
		eg.Go(func() error {
			var resolvedDepSrc dagql.Instance[*core.ModuleSource]
			err := s.dag.Select(ctx, src, &resolvedDepSrc,
				dagql.Selector{
					Field: "resolveDependency",
					Args: []dagql.NamedInput{
						{Name: "dep", Value: dagql.NewID[*core.ModuleSource](dep.Self.Source.ID())},
					},
				},
			)
			if err != nil {
				return fmt.Errorf("failed to resolve dependency: %w", err)
			}

			err = s.dag.Select(ctx, s.dag.Root(), &newDeps[i],
				dagql.Selector{
					Field: "moduleDependency",
					Args: []dagql.NamedInput{
						{Name: "source", Value: dagql.NewID[*core.ModuleSource](resolvedDepSrc.ID())},
						{Name: "name", Value: dagql.String(dep.Self.Name)},
					},
				},
			)
			if err != nil {
				return fmt.Errorf("failed to create module dependency: %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to resolve new dependencies: %w", err)
	}

	// figure out the set of deps, keyed by their symbolic ref string, which de-dupes
	// equivalent sources at different versions, preferring the version provided
	// in the dependencies arg here
	depSet := make(map[string]dagql.Instance[*core.ModuleDependency])
	for _, dep := range existingDeps {
		symbolic, err := dep.Self.Source.Self.Symbolic()
		if err != nil {
			return nil, fmt.Errorf("failed to get symbolic source ref: %w", err)
		}
		depSet[symbolic] = dep
	}
	for _, dep := range newDeps {
		symbolic, err := dep.Self.Source.Self.Symbolic()
		if err != nil {
			return nil, fmt.Errorf("failed to get symbolic source ref: %w", err)
		}
		depSet[symbolic] = dep
	}

	finalDeps := make([]dagql.Instance[*core.ModuleDependency], 0, len(depSet))
	for _, dep := range depSet {
		finalDeps = append(finalDeps, dep)
	}
	sort.Slice(finalDeps, func(i, j int) bool {
		return finalDeps[i].Self.Name < finalDeps[j].Self.Name
	})

	return finalDeps, nil
}

func (s *moduleSchema) moduleSourceWithDependencies(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Dependencies []core.ModuleDependencyID
	},
) (*core.ModuleSource, error) {
	src = src.Clone()
	newDeps, err := collectIDInstances(ctx, s.dag, args.Dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to load module source dependencies from ids: %w", err)
	}
	src.WithDependencies = append(src.WithDependencies, newDeps...)
	return src, nil
}

func (s *moduleSchema) moduleSourceWithSDK(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		SDK string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()
	src.WithSDK = args.SDK
	return src, nil
}

func (s *moduleSchema) moduleSourceResolveDependency(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Dep core.ModuleSourceID
	},
) (inst dagql.Instance[*core.ModuleSource], err error) {
	depSrc, err := args.Dep.Load(ctx, s.dag)
	if err != nil {
		return inst, fmt.Errorf("failed to decode module source: %w", err)
	}

	if depSrc.Self.Kind == core.ModuleSourceKindGit {
		// git deps stand on their own, no special handling needed
		return depSrc, nil
	}

	contextDir, err := src.ContextDirectory()
	if err != nil {
		return inst, fmt.Errorf("failed to get context directory: %w", err)
	}
	srcRootSubpath, err := src.SourceRootSubpath()
	if err != nil {
		return inst, fmt.Errorf("failed to get source root subpath: %w", err)
	}
	depRootSubpath, err := depSrc.Self.SourceRootSubpath()
	if err != nil {
		return inst, fmt.Errorf("failed to get source root subpath: %w", err)
	}

	// This dep is a local path relative to a src, need to find the src's root
	// and return a source that points to the full path to this dep
	if contextDir.Self == nil {
		return inst, fmt.Errorf("cannot resolve dependency for module source with no context directory")
	}

	// depSrc.RootSubpath is relative to the src.RootSubpath, i.e.
	// if src.RootSubpath is foo/bar and its dagger.json has dep on ../baz, then
	// depSrc.RootSubpath is ../baz and relative to foo/bar.
	// depSubpath is the resolved path, i.e. foo/baz.
	depSubpath := filepath.Join(srcRootSubpath, depRootSubpath)

	if !filepath.IsLocal(depSubpath) {
		return inst, fmt.Errorf("module dep source root path %q escapes root", depRootSubpath)
	}

	switch src.Kind {
	case core.ModuleSourceKindGit:
		src = src.Clone()
		src.AsGitSource.Value.RootSubpath = depSubpath

		// preserve the git metadata by just constructing a modified git source ref string
		// and using that to load the dep
		newDepRefStr, err := src.RefString()
		if err != nil {
			return inst, fmt.Errorf("failed to get module source ref string: %w", err)
		}

		var newDepSrc dagql.Instance[*core.ModuleSource]
		err = s.dag.Select(ctx, s.dag.Root(), &newDepSrc,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(newDepRefStr)},
					{Name: "stable", Value: dagql.Boolean(true)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to load git dep: %w", err)
		}
		return newDepSrc, nil

	case core.ModuleSourceKindLocal:
		var newDepSrc dagql.Instance[*core.ModuleSource]
		err = s.dag.Select(ctx, s.dag.Root(), &newDepSrc,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(depSubpath)},
				},
			},
			dagql.Selector{
				Field: "withContextDirectory",
				Args: []dagql.NamedInput{
					{Name: "dir", Value: dagql.NewID[*core.Directory](contextDir.ID())},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to load local dep: %w", err)
		}
		return newDepSrc, nil

	default:
		return inst, fmt.Errorf("unsupported module source kind: %q", src.Kind)
	}
}

func (s *moduleSchema) moduleSourceContextDirectory(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (inst dagql.Instance[*core.Directory], err error) {
	return src.ContextDirectory()
}

func (s *moduleSchema) moduleSourceWithContextDirectory(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Dir dagql.ID[*core.Directory]
	},
) (*core.ModuleSource, error) {
	if src.Kind != core.ModuleSourceKindLocal {
		return nil, fmt.Errorf("cannot set context directory for non-local module source")
	}

	src = src.Clone()
	dir, err := args.Dir.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to load context directory: %w", err)
	}
	src.AsLocalSource.Value.ContextDirectory.Value = dir
	src.AsLocalSource.Value.ContextDirectory.Valid = true
	return src, nil
}

func (s *moduleSchema) moduleSourceDirectory(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Path string
	},
) (dir dagql.Instance[*core.Directory], err error) {
	rootSubpath, err := src.SourceRootSubpath()
	if err != nil {
		return dir, fmt.Errorf("failed to get source root subpath: %w", err)
	}
	fullSubpath := filepath.Join("/", rootSubpath, args.Path)

	contextDir, err := src.ContextDirectory()
	if err != nil {
		return dir, fmt.Errorf("failed to get context directory: %w", err)
	}

	err = s.dag.Select(ctx, contextDir, &dir,
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(fullSubpath)},
			},
		},
	)
	return dir, err
}

func (s *moduleSchema) moduleSourceResolveContextPathFromCaller(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	contextAbsPath, _, err := s.resolveContextPathFromCaller(ctx, src)
	return contextAbsPath, err
}

func (s *moduleSchema) resolveContextPathFromCaller(
	ctx context.Context,
	src *core.ModuleSource,
) (contextRootAbsPath, sourceRootAbsPath string, _ error) {
	if src.Kind != core.ModuleSourceKindLocal {
		return "", "", fmt.Errorf("cannot resolve non-local module source from caller")
	}

	rootSubpath, err := src.SourceRootSubpath()
	if err != nil {
		return "", "", fmt.Errorf("failed to get source root subpath: %w", err)
	}

	sourceRootStat, err := src.Query.Buildkit.StatCallerHostPath(ctx, rootSubpath, true)
	if err != nil {
		return "", "", fmt.Errorf("failed to stat source root: %w", err)
	}
	sourceRootAbsPath = sourceRootStat.Path

	contextAbsPath, contextFound, err := callerHostFindUpContext(ctx, src.Query.Buildkit, sourceRootAbsPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to find up root: %w", err)
	}

	if !contextFound {
		// default to restricting to the source root dir, make it abs though for consistency
		contextAbsPath = sourceRootAbsPath
	}

	return contextAbsPath, sourceRootAbsPath, nil
}

//nolint:gocyclo // it's already been split up where it makes sense, more would just create indirection in reading it
func (s *moduleSchema) moduleSourceResolveFromCaller(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (inst dagql.Instance[*core.ModuleSource], err error) {
	contextAbsPath, sourceRootAbsPath, err := s.resolveContextPathFromCaller(ctx, src)
	if err != nil {
		return inst, err
	}

	sourceRootRelPath, err := filepath.Rel(contextAbsPath, sourceRootAbsPath)
	if err != nil {
		return inst, fmt.Errorf("failed to get source root relative path: %w", err)
	}
	// ensure sourceRootRelPath has a local path structure
	// even when subdir relative to git source has ref structure
	// (cf. test TestRefFormat)
	sourceRootRelPath = "./" + sourceRootRelPath

	collectedDeps := dagql.NewCacheMap[string, *callerLocalDep]()
	if err := s.collectCallerLocalDeps(ctx, src.Query, contextAbsPath, sourceRootAbsPath, true, src, collectedDeps); err != nil {
		return inst, fmt.Errorf("failed to collect local module source deps: %w", err)
	}

	includeSet := map[string]struct{}{}
	excludeSet := map[string]struct{}{
		// always exclude .git dirs, we don't need them and they tend to invalidate cache a lot
		"**/.git": {},
	}
	sdkSet := map[string]core.SDK{}
	sourceRootPaths := collectedDeps.Keys()
	for _, rootPath := range sourceRootPaths {
		rootRelPath, err := filepath.Rel(contextAbsPath, rootPath)
		if err != nil {
			return inst, fmt.Errorf("failed to get source root relative path: %w", err)
		}
		if !filepath.IsLocal(rootRelPath) {
			return inst, fmt.Errorf("local module dep source path %q escapes context %q", rootRelPath, contextAbsPath)
		}

		localDep, err := collectedDeps.Get(ctx, rootPath)
		if err != nil {
			return inst, fmt.Errorf("failed to get collected local dep %s: %w", rootPath, err)
		}

		if rootPath == sourceRootAbsPath {
			// only the top-level module source is allowed to be nameless/sdkless
			if localDep.sdk != nil {
				sdkSet[localDep.sdkKey] = localDep.sdk
			}
			if localDep.modCfg == nil {
				// uninitialized top-level module source, include it's source root dir (otherwise
				// we could load everything if no other includes end up being specified)
				includeSet[sourceRootRelPath] = struct{}{}
				continue
			}
		} else {
			if localDep.modCfg == nil {
				return inst, fmt.Errorf("local module source dep %s is not initialized", rootPath)
			}
			if localDep.sdk == nil {
				return inst, fmt.Errorf("local module source dep %s has no sdk", rootPath)
			}
			sdkSet[localDep.sdkKey] = localDep.sdk
		}

		// rebase user defined include/exclude relative to context
		rebaseIncludeExclude := func(path string, set map[string]struct{}) error {
			isNegation := strings.HasPrefix(path, "!")
			path = strings.TrimPrefix(path, "!")
			absPath := filepath.Join(sourceRootAbsPath, path)
			relPath, err := filepath.Rel(contextAbsPath, absPath)
			if err != nil {
				return fmt.Errorf("failed to get relative path of config include/exclude: %w", err)
			}
			if !filepath.IsLocal(relPath) {
				return fmt.Errorf("local module dep source include/exclude path %q escapes context %q", relPath, contextAbsPath)
			}
			if isNegation {
				relPath = "!" + relPath
			}
			set[relPath] = struct{}{}
			return nil
		}
		for _, path := range localDep.modCfg.Include {
			if err := rebaseIncludeExclude(path, includeSet); err != nil {
				return inst, err
			}
		}
		for _, path := range localDep.modCfg.Exclude {
			if err := rebaseIncludeExclude(path, excludeSet); err != nil {
				return inst, err
			}
		}

		// always include the config file
		configRelPath, err := filepath.Rel(contextAbsPath, filepath.Join(rootPath, modules.Filename))
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path: %w", err)
		}
		includeSet[configRelPath] = struct{}{}

		// always include the source dir
		source := localDep.modCfg.Source
		if source == "" {
			source = "."
		}
		sourceAbsSubpath := filepath.Join(rootPath, source)
		sourceRelSubpath, err := filepath.Rel(contextAbsPath, sourceAbsSubpath)
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path: %w", err)
		}
		if !filepath.IsLocal(sourceRelSubpath) {
			return inst, fmt.Errorf("local module source path %q escapes context %q", sourceRelSubpath, contextAbsPath)
		}
		includeSet[sourceRelSubpath+"/**/*"] = struct{}{}
	}

	for _, sdk := range sdkSet {
		// NOTE: required paths are currently **-style globs that apply to the whole context subtree
		// This is a bit of a delicate assumption, if need arises for including exact paths, this
		// will need some adjustment.
		requiredPaths, err := sdk.RequiredPaths(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get sdk required paths: %w", err)
		}
		for _, path := range requiredPaths {
			includeSet[path] = struct{}{}
		}
	}

	includes := make([]string, 0, len(includeSet))
	for include := range includeSet {
		includes = append(includes, include)
	}
	excludes := make([]string, 0, len(excludeSet))
	for exclude := range excludeSet {
		excludes = append(excludes, exclude)
	}

	_, desc, err := src.Query.Buildkit.LocalImport(
		ctx,
		src.Query.Platform.Spec(),
		contextAbsPath,
		excludes,
		includes,
	)
	if err != nil {
		return inst, fmt.Errorf("failed to import local module source: %w", err)
	}
	loadedDir, err := core.LoadBlob(ctx, s.dag, desc)
	if err != nil {
		return inst, fmt.Errorf("failed to load local module source: %w", err)
	}

	return s.normalizeCallerLoadedSource(ctx, src, sourceRootRelPath, loadedDir)
}

// get an instance of ModuleSource with the context resolved from the caller that doesn't
// encode any instructions to actually reload from the caller if the ID is loaded later, which
// is possible due to blob-ifying the local import.
func (s *moduleSchema) normalizeCallerLoadedSource(
	ctx context.Context,
	src *core.ModuleSource,
	sourceRootRelPath string,
	loadedDir dagql.Instance[*core.Directory],
) (inst dagql.Instance[*core.ModuleSource], err error) {
	err = s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(sourceRootRelPath)},
			},
		},
		dagql.Selector{
			Field: "withContextDirectory",
			Args: []dagql.NamedInput{
				{Name: "dir", Value: dagql.NewID[*core.Directory](loadedDir.ID())},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to load the context directory: %w", err)
	}

	if src.WithName != "" {
		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withName",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(src.WithName)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to set name: %w", err)
		}
	}
	if src.WithSDK != "" {
		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withSDK",
				Args: []dagql.NamedInput{
					{Name: "sdk", Value: dagql.String(src.WithSDK)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to set sdk: %w", err)
		}
	}
	if src.WithSourceSubpath != "" {
		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withSourceSubpath",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(src.WithSourceSubpath)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to set source subdir: %w", err)
		}
	}
	if len(src.WithDependencies) > 0 {
		depIDs := make([]core.ModuleDependencyID, len(src.WithDependencies))
		for i, dep := range src.WithDependencies {
			depIDs[i] = dagql.NewID[*core.ModuleDependency](dep.ID())
		}

		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withDependencies",
				Args: []dagql.NamedInput{
					{Name: "dependencies", Value: dagql.ArrayInput[core.ModuleDependencyID](depIDs)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to set dependency: %w", err)
		}
	}
	if src.WithViews != nil {
		for _, view := range src.WithViews {
			err = s.dag.Select(ctx, inst, &inst,
				dagql.Selector{
					Field: "withView",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(view.Name)},
						{Name: "patterns", Value: asArrayInput(view.Patterns, dagql.NewString)},
					},
				},
			)
			if err != nil {
				return inst, fmt.Errorf("failed to set view: %w", err)
			}
		}
	}

	return inst, err
}

type callerLocalDep struct {
	sourceRootAbsPath string
	modCfg            *modules.ModuleConfig
	sdk               core.SDK
	// sdkKey is a unique identifier for the SDK, slightly different
	// from the module ref for the SDK because custom local SDKs
	// use their local path for sdkKey, which allows us to de-dupe
	// loading them across the dag of local deps.
	sdkKey string
}

func (s *moduleSchema) collectCallerLocalDeps(
	ctx context.Context,
	query *core.Query,
	contextAbsPath string,
	sourceRootAbsPath string,
	// topLevel should only be true for the module source being operated on,
	// everything else we collect is a (transitive) dep. The top level module
	// is a bit special in that it is allowed to not be initialized yet and/or
	// not have a name/sdk/etc.
	topLevel bool,
	src *core.ModuleSource,
	// cache of sourceRootAbsPath -> *callerLocalDep
	collectedDeps dagql.CacheMap[string, *callerLocalDep],
) error {
	_, _, err := collectedDeps.GetOrInitialize(ctx, sourceRootAbsPath, func(ctx context.Context) (*callerLocalDep, error) {
		sourceRootRelPath, err := filepath.Rel(contextAbsPath, sourceRootAbsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get source root relative path: %w", err)
		}
		if !filepath.IsLocal(sourceRootRelPath) {
			return nil, fmt.Errorf("local module dep source path %q escapes context %q", sourceRootRelPath, contextAbsPath)
		}

		var modCfg modules.ModuleConfig
		configPath := filepath.Join(sourceRootAbsPath, modules.Filename)
		configBytes, err := query.Buildkit.ReadCallerHostFile(ctx, configPath)
		switch {
		case err == nil:
			if err := json.Unmarshal(configBytes, &modCfg); err != nil {
				return nil, fmt.Errorf("error unmarshaling config at %s: %w", configPath, err)
			}

		// TODO: remove the strings.Contains check here (which aren't cross-platform),
		// since we now set NotFound (since v0.11.2)
		case status.Code(err) == codes.NotFound || strings.Contains(err.Error(), "no such file or directory"):
			// This is only allowed for the top-level module (which may be in the process of being newly initialized).
			// sentinel via nil modCfg unless there's WithSDK/WithDependencies/etc. to be applied
			if !topLevel {
				return nil, fmt.Errorf("missing config file %s", configPath)
			}
			if src.WithSDK == "" && len(src.WithDependencies) == 0 {
				return &callerLocalDep{sourceRootAbsPath: sourceRootAbsPath}, nil
			}

		default:
			return nil, fmt.Errorf("error reading config %s: %w", configPath, err)
		}

		if topLevel {
			if src.WithName != "" {
				modCfg.Name = src.WithName
			}
			if src.WithSDK != "" {
				modCfg.SDK = src.WithSDK
			}
			for _, dep := range src.WithDependencies {
				refString, err := dep.Self.Source.Self.RefString()
				if err != nil {
					return nil, fmt.Errorf("failed to get ref string for dependency: %w", err)
				}
				modCfg.Dependencies = append(modCfg.Dependencies, &modules.ModuleConfigDependency{
					Name:   dep.Self.Name,
					Source: refString,
				})
			}
		}

		localDep := &callerLocalDep{
			sourceRootAbsPath: sourceRootAbsPath,
			modCfg:            &modCfg,
		}

		for _, depCfg := range modCfg.Dependencies {
			parsed := parseRefString(ctx, query.Buildkit, depCfg.Source)
			if parsed.kind != core.ModuleSourceKindLocal {
				continue
			}
			depAbsPath := filepath.Join(sourceRootAbsPath, parsed.modPath)
			err = s.collectCallerLocalDeps(ctx, query, contextAbsPath, depAbsPath, false, src, collectedDeps)
			if err != nil {
				return nil, fmt.Errorf("failed to collect local module source dep: %w", err)
			}
		}

		if modCfg.SDK == "" {
			return localDep, nil
		}

		localDep.sdkKey = modCfg.SDK

		localDep.sdk, err = s.builtinSDK(ctx, query, modCfg.SDK)
		switch {
		case err == nil:
		case errors.Is(err, errUnknownBuiltinSDK):
			parsed := parseRefString(ctx, query.Buildkit, modCfg.SDK)
			switch parsed.kind {
			case core.ModuleSourceKindLocal:
				// SDK is a local custom one, it needs to be included
				sdkPath := filepath.Join(sourceRootAbsPath, parsed.modPath)

				err = s.collectCallerLocalDeps(ctx, query, contextAbsPath, sdkPath, false, src, collectedDeps)
				if err != nil {
					return nil, fmt.Errorf("failed to collect local sdk: %w", err)
				}

				// TODO: this is inefficient, leads to extra local loads, but only for case
				// of local custom SDK.
				callerCwdStat, err := query.Buildkit.StatCallerHostPath(ctx, ".", true)
				if err != nil {
					return nil, fmt.Errorf("failed to stat caller cwd: %w", err)
				}
				callerCwd := callerCwdStat.Path
				sdkCallerRelPath, err := filepath.Rel(callerCwd, sdkPath)
				if err != nil {
					return nil, fmt.Errorf("failed to get relative path of local sdk: %w", err)
				}
				var sdkMod dagql.Instance[*core.Module]
				err = s.dag.Select(ctx, s.dag.Root(), &sdkMod,
					dagql.Selector{
						Field: "moduleSource",
						Args: []dagql.NamedInput{
							{Name: "refString", Value: dagql.String(sdkCallerRelPath)},
						},
					},
					dagql.Selector{
						Field: "resolveFromCaller",
					},
					dagql.Selector{
						Field: "asModule",
					},
					dagql.Selector{
						Field: "initialize",
					},
				)
				if err != nil {
					return nil, fmt.Errorf("failed to load local sdk module source: %w", err)
				}
				localDep.sdk, err = s.newModuleSDK(ctx, query, sdkMod, dagql.Instance[*core.Directory]{})
				if err != nil {
					return nil, fmt.Errorf("failed to get local sdk: %w", err)
				}
				localDep.sdkKey = sdkPath

			case core.ModuleSourceKindGit:
				localDep.sdk, err = s.sdkForModule(ctx, query, modCfg.SDK, dagql.Instance[*core.ModuleSource]{})
				if err != nil {
					return nil, fmt.Errorf("failed to get git module sdk: %w", err)
				}
			}
		default:
			return nil, fmt.Errorf("failed to load sdk: %w", err)
		}

		return localDep, nil
	})
	if errors.Is(err, dagql.ErrCacheMapRecursiveCall) {
		return fmt.Errorf("local module at %q has a circular dependency", sourceRootAbsPath)
	}
	return err
}

// context path is the parent dir containing .git
func callerHostFindUpContext(
	ctx context.Context,
	bk *buildkit.Client,
	curDirPath string,
) (string, bool, error) {
	_, err := bk.StatCallerHostPath(ctx, filepath.Join(curDirPath, ".git"), false)
	if err == nil {
		return curDirPath, true, nil
	}
	// TODO: remove the strings.Contains check here (which aren't cross-platform),
	// since we now set NotFound (since v0.11.2)
	if status.Code(err) != codes.NotFound && !strings.Contains(err.Error(), "no such file or directory") {
		return "", false, fmt.Errorf("failed to lstat .git: %w", err)
	}

	nextDirPath := filepath.Dir(curDirPath)
	if curDirPath == nextDirPath {
		return "", false, nil
	}
	return callerHostFindUpContext(ctx, bk, nextDirPath)
}

func (s *moduleSchema) moduleSourceResolveDirectoryFromCaller(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Path     string
		ViewName *string
	},
) (inst dagql.Instance[*core.Directory], err error) {
	path := args.Path
	stat, err := src.Query.Buildkit.StatCallerHostPath(ctx, path, true)
	if err != nil {
		return inst, fmt.Errorf("failed to stat caller path: %w", err)
	}
	path = stat.Path

	var includes []string
	var excludes []string
	if args.ViewName != nil {
		view, err := src.ViewByName(ctx, *args.ViewName)
		if err != nil {
			return inst, fmt.Errorf("failed to get view: %w", err)
		}
		for _, p := range view.Patterns {
			if strings.HasPrefix(p, "!") {
				excludes = append(excludes, p[1:])
			} else {
				includes = append(includes, p)
			}
		}
	}

	_, desc, err := src.Query.Buildkit.LocalImport(
		ctx, src.Query.Platform.Spec(),
		path,
		excludes,
		includes,
	)
	if err != nil {
		return inst, fmt.Errorf("failed to import local directory module arg: %w", err)
	}
	return core.LoadBlob(ctx, s.dag, desc)
}

func (s *moduleSchema) moduleSourceViews(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) ([]*core.ModuleSourceView, error) {
	return src.Views(ctx)
}

func (s *moduleSchema) moduleSourceView(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Name string
	},
) (*core.ModuleSourceView, error) {
	return src.ViewByName(ctx, args.Name)
}

func (s *moduleSchema) moduleSourceWithView(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Name     string
		Patterns []string
	},
) (*core.ModuleSource, error) {
	for _, p := range args.Patterns {
		p = strings.TrimSpace(p)
		if filepath.IsAbs(p) {
			return nil, fmt.Errorf("include path %q cannot be absolute", p)
		}
	}
	view := &core.ModuleSourceView{
		ModuleConfigView: &modules.ModuleConfigView{
			Name:     args.Name,
			Patterns: args.Patterns,
		},
	}
	src.WithViews = append(src.WithViews, view)
	return src, nil
}

func (s *moduleSchema) moduleSourceViewName(
	ctx context.Context,
	view *core.ModuleSourceView,
	args struct{},
) (string, error) {
	return view.Name, nil
}

func (s *moduleSchema) moduleSourceViewPatterns(
	ctx context.Context,
	view *core.ModuleSourceView,
	args struct{},
) ([]string, error) {
	return view.Patterns, nil
}
