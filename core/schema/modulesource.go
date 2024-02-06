package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

type moduleSourceArgs struct {
	// avoiding name "ref" due to that being a reserved word in some SDKs (e.g. Rust)
	RefString string

	Stable bool `default:"false"`
}

func (s *moduleSchema) moduleSource(ctx context.Context, query *core.Query, args moduleSourceArgs) (*core.ModuleSource, error) {
	parsed := parseRefString(args.RefString)
	modPath, modVersion, hasVersion, isGitHub := parsed.modPath, parsed.modVersion, parsed.hasVersion, parsed.isGitHub

	if !hasVersion && isGitHub && args.Stable {
		return nil, fmt.Errorf("no version provided for stable remote ref: %s", args.RefString)
	}

	src := &core.ModuleSource{
		Query: query,
		Kind:  parsed.kind,
	}

	switch src.Kind {
	case core.ModuleSourceKindLocal:
		if filepath.IsAbs(modPath) {
			return nil, fmt.Errorf("local module source path is absolute: %s", modPath)
		}
		src.RootSubpath = modPath
		src.AsLocalSource = dagql.NonNull(&core.LocalModuleSource{
			Query: query,
		})

	case core.ModuleSourceKindGit:
		if !isGitHub {
			return nil, fmt.Errorf("for now, only github.com/ paths are supported: %q", args.RefString)
		}

		src.AsGitSource = dagql.NonNull(&core.GitModuleSource{})

		segments := strings.SplitN(modPath, "/", 4)
		if len(segments) < 3 {
			return nil, fmt.Errorf("invalid github.com path: %s", modPath)
		}

		src.AsGitSource.Value.URLParent = segments[0] + "/" + segments[1] + "/" + segments[2]

		cloneURL := src.AsGitSource.Value.CloneURL()

		if !hasVersion {
			if args.Stable {
				return nil, fmt.Errorf("no version provided for stable remote ref: %s", args.RefString)
			}
			var err error
			modVersion, err = defaultBranch(ctx, cloneURL)
			if err != nil {
				return nil, fmt.Errorf("determine default branch: %w", err)
			}
		}
		src.AsGitSource.Value.Version = modVersion

		var subPath string
		if len(segments) == 4 {
			subPath = segments[3]
		} else {
			subPath = "/"
		}
		src.RootSubpath = subPath

		commitRef := modVersion
		if hasVersion && isSemver(modVersion) {
			allTags, err := gitTags(ctx, cloneURL)
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
					{Name: "url", Value: dagql.String(cloneURL)},
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

		if !filepath.IsAbs(subPath) && !filepath.IsLocal(subPath) {
			return nil, fmt.Errorf("git module source subpath points out of root: %q", subPath)
		}
		if !filepath.IsAbs(src.RootSubpath) {
			var err error
			src.RootSubpath, err = filepath.Rel("/", src.RootSubpath)
			if err != nil {
				return nil, fmt.Errorf("failed to get relative path: %w", err)
			}
		}

		// TODO:(sipsma) support sparse loading of git repos similar to how local dirs are loaded.
		// Related: https://github.com/dagger/dagger/issues/6292
		err = s.dag.Select(ctx, gitRef, &src.BaseContextDirectory,
			dagql.Selector{Field: "tree"},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load git dir: %w", err)
		}

		// git source needs rootpath on itself too for constructing urls
		src.AsGitSource.Value.RootSubpath = src.RootSubpath
	}

	return src, nil
}

type parsedRefString struct {
	modPath    string
	modVersion string
	hasVersion bool
	isGitHub   bool
	kind       core.ModuleSourceKind
}

func parseRefString(refString string) parsedRefString {
	var parsed parsedRefString
	parsed.modPath, parsed.modVersion, parsed.hasVersion = strings.Cut(refString, "@")
	parsed.isGitHub = strings.HasPrefix(parsed.modPath, "github.com/")

	if !parsed.hasVersion && !parsed.isGitHub {
		parsed.kind = core.ModuleSourceKindLocal
		return parsed
	}
	parsed.kind = core.ModuleSourceKindGit
	return parsed
}

func (s *moduleSchema) moduleSourceAsModule(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) (inst dagql.Instance[*core.Module], err error) {
	// TODO:
	// TODO:
	// TODO:
	// TODO:
	// TODO:
	name, err := src.Self.ModuleName(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get module name: %w", err)
	}
	originalName, err := src.Self.ModuleOriginalName(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get module original name: %w", err)
	}
	slog.Debug("MODULESOURCEASMODULE",
		"name", name,
		"originalName", originalName,
		"rootSubpath", src.Self.RootSubpath,
	)

	// ensure codegen has run
	err = s.dag.Select(ctx, src, &src,
		dagql.Selector{
			Field: "withGeneratedContext",
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to generate context: %w", err)
	}

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

func (s *moduleSchema) moduleSourceSubpath(ctx context.Context, src *core.ModuleSource, args struct{}) (string, error) {
	return src.ModuleSourceSubpath(ctx)
}

func (s *moduleSchema) moduleSourceAsString(ctx context.Context, src *core.ModuleSource, args struct{}) (string, error) {
	return src.RefString()
}

func (s *moduleSchema) gitModuleSourceCloneURL(
	ctx context.Context,
	ref *core.GitModuleSource,
	args struct{},
) (string, error) {
	return ref.CloneURL(), nil
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

	// invalidate generated context if set
	src.GeneratedContextDirectory = dagql.Instance[*core.Directory]{}

	src.WithName = args.Name
	return src, nil
}

func (s *moduleSchema) moduleSourceWithDependencies(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Dependencies []core.ModuleDependencyID
	},
) (*core.ModuleSource, error) {
	src = src.Clone()

	// invalidate generated context if set
	src.GeneratedContextDirectory = dagql.Instance[*core.Directory]{}

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

	// invalidate generated context if set
	src.GeneratedContextDirectory = dagql.Instance[*core.Directory]{}

	src.WithSDK = args.SDK
	return src, nil
}

func (s *moduleSchema) moduleSourceWithSourceSubdir(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Path string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()

	if !filepath.IsLocal(args.Path) {
		return nil, fmt.Errorf("source subdir path escapes parent dir: %s", args.Path)
	}

	// invalidate generated context if set
	src.GeneratedContextDirectory = dagql.Instance[*core.Directory]{}

	src.WithSourceSubdir = args.Path
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

	// TODO:
	// TODO:
	// TODO:
	// TODO:
	// TODO:
	// TODO:
	srcName, err := src.ModuleName(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get module name: %w", err)
	}
	depName, err := depSrc.Self.ModuleName(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get module name: %w", err)
	}
	slog.Debug("MODULESOURCERESOLVEDEPENDENCY",
		"srcName", srcName,
		"srcRootSubpath", src.RootSubpath,
		"depName", depName,
		"depRootSubpath", depSrc.Self.RootSubpath,
	)

	if depSrc.Self.Kind == core.ModuleSourceKindGit {
		// git deps stand on their own, no special handling needed
		return depSrc, nil
	}

	// This dep is a local path relative to a src, need to find the src's root
	// and return a source that points to the full path to this dep
	if src.BaseContextDirectory.Self == nil {
		return inst, fmt.Errorf("cannot resolve dependency for module source with no context directory")
	}

	// depSrc.RootSubpath is relative to the src.RootSubpath, i.e.
	// if src.RootSubpath is foo/bar and its dagger.json has dep on ../baz, then
	// depSrc.RootSubpath is ../baz and relative to foo/bar.
	// depSubpath is the resolved path, i.e. foo/baz.
	depSubpath := filepath.Join(src.RootSubpath, depSrc.Self.RootSubpath)

	if !filepath.IsLocal(depSubpath) {
		return inst, fmt.Errorf("module dep source path %q escapes root", depSubpath)
	}

	switch src.Kind {
	case core.ModuleSourceKindGit:
		src = src.Clone()
		src.RootSubpath = depSubpath

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
				Field: "withContext",
				Args: []dagql.NamedInput{
					{Name: "dir", Value: dagql.NewID[*core.Directory](src.BaseContextDirectory.ID())},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to load git dep: %w", err)
		}
		return newDepSrc, nil

	default:
		return inst, fmt.Errorf("unsupported module source kind: %q", src.Kind)
	}
}

func (s *moduleSchema) moduleSourceDirectory(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct {
		Path string
	},
) (dir dagql.Instance[*core.Directory], err error) {
	fullSubpath := filepath.Join("/", src.Self.RootSubpath, args.Path)
	err = s.dag.Select(ctx, src, &dir,
		dagql.Selector{
			Field: "contextDirectory",
		},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(fullSubpath)},
			},
		},
	)
	return dir, err
}

func (s *moduleSchema) moduleSourceWithContext(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Dir dagql.ID[*core.Directory]
	},
) (*core.ModuleSource, error) {
	src = src.Clone()

	dest := &src.BaseContextDirectory
	if dest.Self != nil {
		// we already loaded the base context, update GeneratedContextDirectory instead
		if src.GeneratedContextDirectory.Self == nil {
			// start out at the base context
			src.GeneratedContextDirectory = src.BaseContextDirectory
		}
		dest = &src.GeneratedContextDirectory
	}

	if dest.Self == nil {
		err := s.dag.Select(ctx, s.dag.Root(), dest,
			dagql.Selector{
				Field: "directory",
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize empty context: %w", err)
		}
	}

	additionalContextDir, err := args.Dir.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to load directory: %w", err)
	}

	err = s.dag.Select(ctx, *dest, dest,
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "directory", Value: dagql.NewID[*core.Directory](additionalContextDir.ID())},
				{Name: "path", Value: dagql.String("/")},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to add additional context: %w", err)
	}

	// TODO:
	// TODO:
	// TODO:
	// TODO:
	// TODO:
	/*
		ents, err := dest.Self.Entries(ctx, "/")
		if err != nil {
			return nil, fmt.Errorf("failed to get entries: %w", err)
		}
		slog.Debug("MODULESOURCEWITHCONTEXT", "ents", ents, "dest", destStr)
	*/

	return src, nil
}

func (s *moduleSchema) moduleSourceBaseContextDirectory(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) (inst dagql.Instance[*core.Directory], err error) {
	if src.Self.BaseContextDirectory.Self == nil {
		return inst, fmt.Errorf("base context directory not set")
	}
	return src.Self.BaseContextDirectory, nil
}

func (s *moduleSchema) moduleSourceContextDirectory(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) (inst dagql.Instance[*core.Directory], err error) {
	return src.Self.ContextDirectory()
}

func (s *moduleSchema) moduleSourceGeneratedContextDiff(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) (inst dagql.Instance[*core.Directory], err error) {
	baseContext := src.Self.BaseContextDirectory

	err = s.dag.Select(ctx, src, &src,
		dagql.Selector{
			Field: "withGeneratedContext", // no-op if already was generated
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to generate context: %w", err)
	}

	err = s.dag.Select(ctx, baseContext, &inst,
		dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*core.Directory](src.Self.GeneratedContextDirectory.ID())},
			},
		},
	)
	return inst, err
}

// TODO: this is obviously way too long, break apart
func (s *moduleSchema) moduleSourceWithGeneratedContext(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) (inst dagql.Instance[*core.ModuleSource], err error) {
	if src.Self.BaseContextDirectory.Self == nil {
		return inst, fmt.Errorf("base context directory not set")
	}

	if src.Self.GeneratedContextDirectory.Self != nil {
		// already generated, nothing to do. this is invalidated on mutations like withSDK/withName/etc.
		return src, nil
	}

	// start out at the loaded context
	generatedContext := src.Self.BaseContextDirectory

	modCfg, ok, err := src.Self.ModuleConfig(ctx)
	if err != nil {
		return inst, fmt.Errorf("module config: %w", err)
	}
	if !ok {
		modCfg = &modules.ModuleConfig{}
	}

	if src.Self.WithName != "" {
		modCfg.Name = src.Self.WithName
	}
	if src.Self.WithSDK != "" {
		modCfg.SDK = src.Self.WithSDK
	}
	if src.Self.WithSourceSubdir != "" {
		modCfg.SourceSubdir = src.Self.WithSourceSubdir
	}

	existingDeps := make([]*core.ModuleDependency, len(modCfg.Dependencies))
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
			existingDeps[i] = &core.ModuleDependency{
				Source: resolvedDepSrc,
				Name:   depCfg.Name,
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, fmt.Errorf("failed to load pre-configured dependencies: %w", err)
	}

	newDeps := make([]*core.ModuleDependency, len(src.Self.WithDependencies))
	eg = errgroup.Group{}
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
			newDeps[i] = &core.ModuleDependency{
				Source: resolvedDepSrc,
				Name:   dep.Self.Name,
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, fmt.Errorf("failed to resolve new dependencies: %w", err)
	}

	// figure out the set of deps, keyed by their symbolic ref string, which de-dupes
	// equivalent sources at different versions, preferring the version provided
	// in the dependencies arg here
	depSet := make(map[string]*core.ModuleDependency)
	for _, dep := range existingDeps {
		symbolic, err := dep.Source.Self.Symbolic()
		if err != nil {
			return inst, fmt.Errorf("failed to get symbolic source ref: %w", err)
		}
		depSet[symbolic] = dep
	}
	for _, dep := range newDeps {
		symbolic, err := dep.Source.Self.Symbolic()
		if err != nil {
			return inst, fmt.Errorf("failed to get symbolic source ref: %w", err)
		}
		depSet[symbolic] = dep
	}

	finalDeps := make([]*core.ModuleDependency, 0, len(depSet))
	modCfg.Dependencies = make([]*modules.ModuleConfigDependency, 0, len(depSet))
	for _, dep := range depSet {
		finalDeps = append(finalDeps, dep)

		var refStr string
		switch dep.Source.Self.Kind {
		case core.ModuleSourceKindLocal:
			depRootPath := dep.Source.Self.RootSubpath
			depRelSubpath, err := filepath.Rel(src.Self.RootSubpath, depRootPath)
			if err != nil {
				return inst, fmt.Errorf("failed to get module dep source path relative to source: %w", err)
			}
			refStr = depRelSubpath

		default:
			refStr, err = dep.Source.Self.RefString()
			if err != nil {
				return inst, fmt.Errorf("failed to get ref string for dependency: %w", err)
			}
		}
		modCfg.Dependencies = append(modCfg.Dependencies, &modules.ModuleConfigDependency{
			Name:   dep.Name,
			Source: refStr,
		})
	}
	sort.Slice(finalDeps, func(i, j int) bool {
		return modCfg.Dependencies[i].Source < modCfg.Dependencies[j].Source
	})
	sort.Slice(modCfg.Dependencies, func(i, j int) bool {
		return modCfg.Dependencies[i].Source < modCfg.Dependencies[j].Source
	})

	depMods := make([]*core.Module, len(finalDeps))
	eg = errgroup.Group{}
	for i, dep := range finalDeps {
		i, dep := i, dep
		eg.Go(func() error {
			var depMod dagql.Instance[*core.Module]
			err := s.dag.Select(ctx, dep.Source, &depMod,
				dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(dep.Name)},
					},
				},
				dagql.Selector{
					Field: "asModule",
				},
				dagql.Selector{
					Field: "initialize",
				},
			)
			if err != nil {
				return fmt.Errorf("failed to initialize dependency module: %w", err)
			}
			depMods[i] = depMod.Self
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, fmt.Errorf("failed to initialize dependency modules: %w", err)
	}
	// fill in any missing names if needed
	for i, dep := range modCfg.Dependencies {
		if dep.Name == "" {
			dep.Name = depMods[i].Name()
		}
	}

	rootSubpath := src.Self.RootSubpath

	if modCfg.Name != "" && modCfg.SDK != "" {
		sdk, err := s.sdkForModule(ctx, src.Self.Query, modCfg.SDK, src)
		if err != nil {
			return inst, fmt.Errorf("failed to load sdk for module: %w", err)
		}

		deps := core.NewModDeps(src.Self.Query, src.Self.Query.DefaultDeps.Mods)
		for _, dep := range depMods {
			deps = deps.Append(dep)
		}

		generatedCode, err := sdk.Codegen(ctx, deps, src)
		if err != nil {
			return inst, fmt.Errorf("failed to generate code: %w", err)
		}

		var diff dagql.Instance[*core.Directory]
		err = s.dag.Select(ctx, src.Self.BaseContextDirectory, &diff,
			dagql.Selector{
				Field: "diff",
				Args: []dagql.NamedInput{
					{Name: "other", Value: dagql.NewID[*core.Directory](generatedCode.Code.ID())},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to diff generated code: %w", err)
		}

		err = s.dag.Select(ctx, generatedContext, &generatedContext,
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "directory", Value: dagql.NewID[*core.Directory](diff.ID())},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to add codegen to module context directory: %w", err)
		}

		// update .gitattributes
		// (linter thinks this chunk of code is too similar to the below, but not clear abstraction is worth it)
		//nolint:dupl
		if len(generatedCode.VCSGeneratedPaths) > 0 {
			gitAttrsPath := filepath.Join(rootSubpath, ".gitattributes")
			var gitAttrsContents []byte
			gitAttrsFile, err := src.Self.BaseContextDirectory.Self.File(ctx, gitAttrsPath)
			if err == nil {
				gitAttrsContents, err = gitAttrsFile.Contents(ctx)
				if err != nil {
					return inst, fmt.Errorf("failed to get git attributes file contents: %w", err)
				}
				if !bytes.HasSuffix(gitAttrsContents, []byte("\n")) {
					gitAttrsContents = append(gitAttrsContents, []byte("\n")...)
				}
			}
			for _, fileName := range generatedCode.VCSGeneratedPaths {
				if bytes.Contains(gitAttrsContents, []byte(fileName)) {
					// already has some config for the file
					continue
				}
				gitAttrsContents = append(gitAttrsContents,
					[]byte(fmt.Sprintf("/%s linguist-generated\n", fileName))...,
				)
			}

			err = s.dag.Select(ctx, generatedContext, &generatedContext,
				dagql.Selector{
					Field: "withNewFile",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(gitAttrsPath)},
						{Name: "contents", Value: dagql.String(gitAttrsContents)},
						{Name: "permissions", Value: dagql.Int(0600)},
					},
				},
			)
			if err != nil {
				return inst, fmt.Errorf("failed to add vcs generated file: %w", err)
			}
		}

		// update .gitignore
		// (linter thinks this chunk of code is too similar to the above, but not clear abstraction is worth it)
		//nolint:dupl
		if len(generatedCode.VCSIgnoredPaths) > 0 {
			gitIgnorePath := filepath.Join(rootSubpath, ".gitignore")
			var gitIgnoreContents []byte
			gitIgnoreFile, err := src.Self.BaseContextDirectory.Self.File(ctx, gitIgnorePath)
			if err == nil {
				gitIgnoreContents, err = gitIgnoreFile.Contents(ctx)
				if err != nil {
					return inst, fmt.Errorf("failed to get .gitignore file contents: %w", err)
				}
				if !bytes.HasSuffix(gitIgnoreContents, []byte("\n")) {
					gitIgnoreContents = append(gitIgnoreContents, []byte("\n")...)
				}
			}
			for _, fileName := range generatedCode.VCSIgnoredPaths {
				if bytes.Contains(gitIgnoreContents, []byte(fileName)) {
					continue
				}
				gitIgnoreContents = append(gitIgnoreContents,
					[]byte(fmt.Sprintf("/%s\n", fileName))...,
				)
			}

			err = s.dag.Select(ctx, generatedContext, &generatedContext,
				dagql.Selector{
					Field: "withNewFile",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(gitIgnorePath)},
						{Name: "contents", Value: dagql.String(gitIgnoreContents)},
						{Name: "permissions", Value: dagql.Int(0600)},
					},
				},
			)
			if err != nil {
				return inst, fmt.Errorf("failed to add vcs ignore file: %w", err)
			}
		}
	}

	// update dagger.json last so SDKs can't intentionally or unintentionally
	// modify it during codegen in ways that would be hard to deal with
	modCfgPath := filepath.Join(rootSubpath, modules.Filename)
	updatedModCfgBytes, err := json.MarshalIndent(modCfg, "", "  ")
	if err != nil {
		return inst, fmt.Errorf("failed to encode module config: %w", err)
	}
	updatedModCfgBytes = append(updatedModCfgBytes, '\n')
	err = s.dag.Select(ctx, generatedContext, &generatedContext,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(modCfgPath)},
				{Name: "contents", Value: dagql.String(updatedModCfgBytes)},
				{Name: "permissions", Value: dagql.Int(0644)},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to update module context directory config file: %w", err)
	}

	err = s.dag.Select(ctx, src, &inst,
		dagql.Selector{
			Field: "withContext",
			Args: []dagql.NamedInput{
				{Name: "dir", Value: dagql.NewID[*core.Directory](generatedContext.ID())},
			},
		},
	)
	return inst, err
}

func (s *moduleSchema) moduleSourceResolveContextPathFromCaller(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindLocal {
		return "", fmt.Errorf("cannot resolve non-local module source from caller")
	}

	sourceRootStat, err := src.Query.Buildkit.StatCallerHostPath(ctx, src.RootSubpath, true)
	if err != nil {
		return "", fmt.Errorf("failed to stat source root: %w", err)
	}
	sourceRootAbsPath := sourceRootStat.Path

	contextAbsPath, contextFound, err := callerHostFindUpContext(ctx, src.Query.Buildkit, sourceRootAbsPath)
	if err != nil {
		return "", fmt.Errorf("failed to find up root: %w", err)
	}

	if !contextFound {
		// default to restricting to the source root dir, make it abs though for consistency
		contextAbsPath = sourceRootAbsPath
	}

	return contextAbsPath, nil
}

func (s *moduleSchema) moduleSourceResolveFromCaller(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (inst dagql.Instance[*core.ModuleSource], err error) {
	// TODO: de-dupe with code above
	if src.Kind != core.ModuleSourceKindLocal {
		return inst, fmt.Errorf("cannot resolve non-local module source from caller")
	}

	sourceRootStat, err := src.Query.Buildkit.StatCallerHostPath(ctx, src.RootSubpath, true)
	if err != nil {
		return inst, fmt.Errorf("failed to stat source root: %w", err)
	}
	sourceRootAbsPath := sourceRootStat.Path

	contextAbsPath, contextFound, err := callerHostFindUpContext(ctx, src.Query.Buildkit, sourceRootAbsPath)
	if err != nil {
		return inst, fmt.Errorf("failed to find up root: %w", err)
	}

	if !contextFound {
		// default to restricting to the source root dir, make it abs though for consistency
		contextAbsPath = sourceRootAbsPath
	}

	sourceRootRelPath, err := filepath.Rel(contextAbsPath, sourceRootAbsPath)
	if err != nil {
		return inst, fmt.Errorf("failed to get source root relative path: %s", err)
	}

	// TODO:
	// TODO:
	// TODO:
	slog.Debug("moduleLocalSourceResolveFromCaller",
		"contextAbsPath", contextAbsPath,
		"sourceRootAbsPath", sourceRootAbsPath,
		"sourceRootRelPath", sourceRootRelPath,
		"callerRootPath", fmt.Sprintf("%q", src.RootSubpath),
		"contextFound", contextFound,
	)

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
			return inst, fmt.Errorf("failed to get source root relative path: %s", err)
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
		for _, path := range localDep.modCfg.Include {
			absPath := filepath.Join(sourceRootAbsPath, path)
			relPath, err := filepath.Rel(contextAbsPath, absPath)
			if err != nil {
				return inst, fmt.Errorf("failed to get relative path of config include: %s", err)
			}
			if !filepath.IsLocal(relPath) {
				return inst, fmt.Errorf("local module dep source include path %q escapes context %q", relPath, contextAbsPath)
			}
			includeSet[relPath] = struct{}{}
		}
		for _, path := range localDep.modCfg.Exclude {
			absPath := filepath.Join(sourceRootAbsPath, path)
			relPath, err := filepath.Rel(contextAbsPath, absPath)
			if err != nil {
				return inst, fmt.Errorf("failed to get relative path of config exclude: %s", err)
			}
			if !filepath.IsLocal(relPath) {
				return inst, fmt.Errorf("local module dep source exclude path %q escapes context %q", relPath, contextAbsPath)
			}
			excludeSet[relPath] = struct{}{}
		}

		// always include the config file
		configRelPath, err := filepath.Rel(contextAbsPath, filepath.Join(rootPath, modules.Filename))
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path: %s", err)
		}
		includeSet[configRelPath] = struct{}{}

		// always include the source dir
		source := localDep.modCfg.SourceSubdir
		if source == "" {
			source = "."
		}
		sourceAbsSubpath := filepath.Join(rootPath, source)
		sourceRelSubpath, err := filepath.Rel(contextAbsPath, sourceAbsSubpath)
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path: %s", err)
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

	// TODO:
	// TODO:
	// TODO:
	slog.Debug("moduleLocalSourceResolveFromCaller",
		"contextAbsPath", contextAbsPath,
		"sourceRootAbsPath", sourceRootAbsPath,
		"sourceRootRelPath", sourceRootRelPath,
		"callerRootPath", fmt.Sprintf("%q", src.RootSubpath),
		"contextFound", contextFound,
		"includes", includes,
		"excludes", excludes,
	)

	pipelineName := fmt.Sprintf("load local module context %s", contextAbsPath)
	ctx, subRecorder := progrock.WithGroup(ctx, pipelineName, progrock.Weak())
	_, desc, err := src.Query.Buildkit.LocalImport(
		ctx, subRecorder, src.Query.Platform.Spec(),
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

	err = s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(sourceRootRelPath)},
			},
		},
		dagql.Selector{
			Field: "withContext",
			Args: []dagql.NamedInput{
				{Name: "dir", Value: dagql.NewID[*core.Directory](loadedDir.ID())},
			},
		},
	)

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
	if src.WithSourceSubdir != "" {
		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withSourceSubdir",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(src.WithSourceSubdir)},
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
	return inst, err
}

type callerLocalDep struct {
	sourceRootAbsPath string
	modCfg            *modules.ModuleConfig
	sdk               core.SDK
	sdkKey            string // TODO :explain..
}

func (s *moduleSchema) collectCallerLocalDeps(
	ctx context.Context,
	query *core.Query,
	contextAbsPath string,
	sourceRootAbsPath string,
	topLevel bool, // TODO: doc
	src *core.ModuleSource,
	// cache of sourceRootAbsPath -> *callerLocalDep
	collectedDeps dagql.CacheMap[string, *callerLocalDep],
) error {
	_, err := collectedDeps.GetOrInitialize(ctx, sourceRootAbsPath, func(ctx context.Context) (*callerLocalDep, error) {
		sourceRootRelPath, err := filepath.Rel(contextAbsPath, sourceRootAbsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get source root relative path: %s", err)
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
				return nil, fmt.Errorf("error unmarshaling config at %s: %s", configPath, err)
			}

		case strings.Contains(err.Error(), "no such file or directory"):
			// This is only allowed for the top-level module (which may be in the process of being newly initialized).
			// sentinel via nil modCfg unless there's WithSDK/WithDependencies/etc. to be applied
			if !topLevel {
				return nil, fmt.Errorf("missing config file %s", configPath)
			}
			if src.WithSDK == "" && len(src.WithDependencies) == 0 {
				return &callerLocalDep{sourceRootAbsPath: sourceRootAbsPath}, nil
			}

		default:
			return nil, fmt.Errorf("error reading config %s: %s", configPath, err)
		}

		if topLevel {
			modCfg.Name = src.WithName
			modCfg.SDK = src.WithSDK
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
			parsed := parseRefString(depCfg.Source)
			if parsed.kind != core.ModuleSourceKindLocal {
				continue
			}
			depAbsPath := filepath.Join(sourceRootAbsPath, parsed.modPath)
			// TODO:
			// TODO:
			// TODO:
			// TODO:
			// TODO:
			// TODO:
			slog.Debug("collectCallerLocalDeps", "dep", parsed.modPath, "depAbsPath", depAbsPath, "sourceRootAbsPath", sourceRootAbsPath)

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
			parsed := parseRefString(modCfg.SDK)
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
					return nil, fmt.Errorf("failed to get relative path of local sdk: %s", err)
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
				// TODO: this codepath is completely untested atm
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
	if !filepath.IsAbs(curDirPath) {
		return "", false, fmt.Errorf("path is not absolute: %s", curDirPath)
	}
	_, err := bk.StatCallerHostPath(ctx, filepath.Join(curDirPath, ".git"), false)
	if err == nil {
		return curDirPath, true, nil
	}
	if !strings.Contains(err.Error(), "no such file or directory") {
		return "", false, fmt.Errorf("failed to lstat .git: %s", err)
	}

	if curDirPath == "/" {
		return "", false, nil
	}
	return callerHostFindUpContext(ctx, bk, filepath.Dir(curDirPath))
}

func pathEscapes(parentAbsPath, childAbsPath string) bool {
	relPath, err := filepath.Rel(parentAbsPath, childAbsPath)
	if err != nil {
		return true
	}
	return !filepath.IsLocal(relPath)
}
