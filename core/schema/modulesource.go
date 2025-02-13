package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type moduleSourceArgs struct {
	// avoiding name "ref" due to that being a reserved word in some SDKs (e.g. Rust)
	RefString      string
	RefPin         string `default:""`
	DisableFindUp  bool   `default:"false"`
	AllowNotExists bool   `default:"false"`

	// TODO: rm
	// TODO: rm
	// TODO: rm
	Stable bool `default:"false"`
}

func (s *moduleSchema) moduleSourceCacheKey(ctx context.Context, query dagql.Instance[*core.Query], args moduleSourceArgs, origDgst digest.Digest) (digest.Digest, error) {
	if fastModuleSourceKindCheck(args.RefString, args.RefPin, args.Stable) == core.ModuleSourceKindGit {
		return origDgst, nil
	}

	return core.CachePerClient(ctx, query, args, origDgst)
}

func (s *moduleSchema) moduleSource(
	ctx context.Context,
	query dagql.Instance[*core.Query],
	args moduleSourceArgs,
) (inst dagql.Instance[*core.ModuleSource], err error) {
	bk, err := query.Self.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	parsedRef, err := parseRefString(ctx, callerDirExistsFS{bk}, args.RefString, args.RefPin, args.Stable)
	if err != nil {
		return inst, err
	}
	switch parsedRef.kind {
	case core.ModuleSourceKindLocal:
		inst, err = s.localModuleSource(ctx, query, bk, parsedRef.local.modPath, !args.DisableFindUp, args.AllowNotExists)
		if err != nil {
			return inst, err
		}
	case core.ModuleSourceKindGit:
		inst, err = s.gitModuleSource(ctx, query, parsedRef.git, args.RefPin, args.Stable)
		if err != nil {
			return inst, err
		}
	default:
		return inst, fmt.Errorf("unknown module source kind: %s", parsedRef.kind)
	}

	return inst, nil
}

func (s *moduleSchema) localModuleSource(
	ctx context.Context,
	query dagql.Instance[*core.Query],
	bk *buildkit.Client,

	// localPath is the path the user provided to load the module, it may be relative or absolute and
	// may point to either the directory containing dagger.json or any subdirectory in the
	// filetree under the directory containing dagger.json
	localPath string,

	// TODO: doc
	doFindUp bool,
	allowNotExists bool,
) (inst dagql.Instance[*core.ModuleSource], err error) {
	if doFindUp {
		// need to check if localPath is a named module from the *default* dagger.json found-up from the cwd
		cwd, err := bk.AbsPath(ctx, ".")
		if err != nil {
			return inst, fmt.Errorf("failed to get cwd: %w", err)
		}
		defaultFindUpSourceRootDir, defaultFindUpExists, err := callerHostFindUp(ctx, bk, cwd, modules.Filename)
		if err != nil {
			return inst, fmt.Errorf("failed to find up root: %w", err)
		}
		if defaultFindUpExists {
			configPath := filepath.Join(defaultFindUpSourceRootDir, modules.Filename)
			contents, err := bk.ReadCallerHostFile(ctx, configPath)
			if err != nil {
				return inst, fmt.Errorf("failed to read module config file: %w", err)
			}
			var modCfg modules.ModuleConfigWithUserFields
			if err := json.Unmarshal(contents, &modCfg); err != nil {
				return inst, fmt.Errorf("failed to decode module config: %w", err)
			}

			namedDep, ok := modCfg.DependencyByName(localPath)
			if ok {
				parsedRef, err := parseRefString(
					ctx,
					dirExistsFunc(func(ctx context.Context, path string) (bool, error) {
						path = filepath.Join(defaultFindUpSourceRootDir, path)
						return callerDirExistsFS{bk}.dirExists(ctx, path)
					}),
					namedDep.Source,
					namedDep.Pin,
					false,
				)
				if err != nil {
					return inst, fmt.Errorf("failed to parse named dep ref string: %w", err)
				}
				switch parsedRef.kind {
				case core.ModuleSourceKindLocal:
					depModPath := filepath.Join(defaultFindUpSourceRootDir, namedDep.Source)
					return s.localModuleSource(ctx, query, bk, depModPath, false, allowNotExists)
				case core.ModuleSourceKindGit:
					return s.gitModuleSource(ctx, query, parsedRef.git, namedDep.Pin, false)
				}
			}
		}
	}

	if localPath == "" {
		localPath = "."
	}

	// make localPath absolute
	var localAbsPath string
	if allowNotExists {
		localAbsPath, err = bk.AbsPath(ctx, localPath)
		if err != nil {
			return inst, fmt.Errorf("failed to get absolute path: %w", err)
		}
	} else {
		stat, err := bk.StatCallerHostPath(ctx, localPath, true)
		if err != nil {
			return inst, fmt.Errorf("failed to stat local path: %w", err)
		}
		localAbsPath = stat.Path
	}

	const dotGit = ".git" // the context dir is the git repo root
	foundPaths, err := callerHostFindUpAll(ctx, bk, localAbsPath, map[string]struct{}{
		modules.Filename: {},
		dotGit:           {},
	})
	if err != nil {
		return inst, fmt.Errorf("failed to find up source root and context: %w", err)
	}
	contextDirPath, dotGitFound := foundPaths[dotGit]
	sourceRootPath, daggerCfgFound := foundPaths[modules.Filename]

	switch {
	case doFindUp && daggerCfgFound:
		// we found-up the dagger config, nothing to do
	case doFindUp && !daggerCfgFound:
		// default the local path as the source root if not found-up
		sourceRootPath = localAbsPath
	case !doFindUp:
		// we weren't trying to find-up the source root, so we always set the source root to the local path
		daggerCfgFound = sourceRootPath == localAbsPath // config was found if-and-only-if it was in the localAbsPath
		sourceRootPath = localAbsPath
	}
	if !dotGitFound {
		// in all cases, if there's no .git found, default the context dir to the source root
		// TODO:
		// TODO:
		// TODO:
		// TODO:
		bklog.G(ctx).Debugf("no .git found, defaulting context dir to source root %q", sourceRootPath)

		contextDirPath = sourceRootPath
	}

	sourceRootRelPath, err := pathutil.LexicalRelativePath(contextDirPath, sourceRootPath)
	if err != nil {
		return inst, fmt.Errorf("failed to get relative path from context to source root: %w", err)
	}
	if !filepath.IsLocal(sourceRootRelPath) {
		return inst, fmt.Errorf("source root path %q escapes context %q", sourceRootRelPath, contextDirPath)
	}

	localSrc := &core.ModuleSource{
		Query:             query.Self,
		ConfigExists:      daggerCfgFound,
		SourceRootSubpath: sourceRootRelPath,
		Kind:              core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: contextDirPath,
			OriginalRefString:    localPath,
		},
	}

	if !daggerCfgFound {
		// Even if dagger.json doesn't exist yet, the source root dir may exist and have contents we should load
		// (e.g. a module source file from a previous module whose dagger.json was deleted).
		var srcRootDir dagql.Instance[*core.Directory]
		_, err := bk.StatCallerHostPath(ctx, sourceRootPath, true)
		switch {
		case err == nil:
			err := s.dag.Select(ctx, s.dag.Root(), &srcRootDir,
				dagql.Selector{Field: "host"},
				dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(sourceRootPath)},
					},
				},
			)
			if err != nil {
				return inst, fmt.Errorf("failed to load local module source root: %w", err)
			}
		case codes.NotFound == status.Code(err):
			// fill in an empty dir at the source root so the context dir digest incorporates that path
			if err := s.dag.Select(ctx, s.dag.Root(), &srcRootDir, dagql.Selector{Field: "directory"}); err != nil {
				return inst, fmt.Errorf("failed to create empty directory for source root subpath: %w", err)
			}
		default:
			return inst, fmt.Errorf("failed to stat source root path: %w", err)
		}

		err = s.dag.Select(ctx, s.dag.Root(), &localSrc.ContextDirectory,
			dagql.Selector{Field: "directory"},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(localSrc.SourceRootSubpath)},
					{Name: "directory", Value: dagql.NewID[*core.Directory](srcRootDir.ID())},
				},
			},
		)
		if err != nil {
			return inst, err
		}
	} else {
		configPath := filepath.Join(sourceRootPath, modules.Filename)
		contents, err := bk.ReadCallerHostFile(ctx, configPath)
		if err != nil {
			return inst, fmt.Errorf("failed to read module config file: %w", err)
		}
		if err := s.initFromModConfig(contents, localSrc); err != nil {
			return inst, err
		}

		// load this module source's context directory, sdk and deps in parallel
		var eg errgroup.Group
		eg.Go(func() error {
			if err := s.loadModuleSourceContext(ctx, bk, localSrc); err != nil {
				return fmt.Errorf("failed to load local module source context: %w", err)
			}

			if localSrc.SDK != nil {
				localSrc.SDKImpl, err = s.sdkForModule(ctx, query.Self, localSrc.SDK, localSrc)
				if err != nil {
					return fmt.Errorf("failed to load sdk for local module source: %w", err)
				}
			}

			return nil
		})

		localSrc.Dependencies = make([]dagql.Instance[*core.ModuleSource], len(localSrc.ConfigDependencies))
		for i, depCfg := range localSrc.ConfigDependencies {
			eg.Go(func() error {
				var err error
				localSrc.Dependencies[i], err = s.resolveDepToSource(ctx, bk, localSrc, depCfg.Source, depCfg.Pin, depCfg.Name)
				if err != nil {
					return fmt.Errorf("failed to resolve dep to source: %w", err)
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return inst, err
		}
	}

	localSrc.Digest = localSrc.CalcDigest().String()

	return dagql.NewInstanceForCurrentID(ctx, s.dag, query, localSrc)
}

func (s *moduleSchema) gitModuleSource(
	ctx context.Context,
	query dagql.Instance[*core.Query],
	parsed *parsedGitRefString,
	refPin string,
	stable bool,
) (inst dagql.Instance[*core.ModuleSource], err error) {
	if stable && !parsed.hasVersion {
		return inst, fmt.Errorf("no version provided for stable remote ref: %s", parsed.cloneRef)
	}

	gitRef, modVersion, err := parsed.getGitRefAndModVersion(ctx, s.dag, refPin)
	if err != nil {
		return inst, fmt.Errorf("failed to resolve git src: %w", err)
	}
	gitCommit, err := gitRef.Self.Commit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to resolve git src to commit: %w", err)
	}

	gitSrc := &core.ModuleSource{
		Query:        query.Self,
		ConfigExists: true, // we can't load uninitialized git modules, we'll error out later if it's not there
		Kind:         core.ModuleSourceKindGit,
		Git: &core.GitModuleSource{
			HTMLRepoURL: parsed.repoRoot.Repo,
			Version:     modVersion,
			Commit:      gitCommit,
			Pin:         gitCommit,
			CloneRef:    parsed.sourceCloneRef,
		},
	}

	gitSrc.SourceRootSubpath = parsed.repoRootSubdir
	if filepath.IsAbs(gitSrc.SourceRootSubpath) {
		gitSrc.SourceRootSubpath = strings.TrimPrefix(gitSrc.SourceRootSubpath, "/")
	}
	if gitSrc.SourceRootSubpath == "" {
		gitSrc.SourceRootSubpath = "."
	}

	parsedURL, err := url.Parse(gitSrc.Git.HTMLRepoURL)
	if err != nil {
		gitSrc.Git.HTMLURL = gitSrc.Git.HTMLRepoURL + path.Join("/src", gitSrc.Git.Commit, gitSrc.SourceRootSubpath)
	} else {
		switch parsedURL.Host {
		case "github.com", "gitlab.com":
			gitSrc.Git.HTMLURL = gitSrc.Git.HTMLRepoURL + path.Join("/tree", gitSrc.Git.Commit, gitSrc.SourceRootSubpath)
		case "dev.azure.com":
			if gitSrc.SourceRootSubpath != "" {
				gitSrc.Git.HTMLURL = fmt.Sprintf("%s/commit/%s?path=/%s", gitSrc.Git.HTMLRepoURL, gitSrc.Git.Commit, gitSrc.SourceRootSubpath)
			}
			gitSrc.Git.HTMLURL = gitSrc.Git.HTMLRepoURL + path.Join("/commit", gitSrc.Git.Commit)
		default:
			gitSrc.Git.HTMLURL = gitSrc.Git.HTMLRepoURL + path.Join("/src", gitSrc.Git.Commit, gitSrc.SourceRootSubpath)
		}
	}

	// TODO:(sipsma) support sparse loading of git repos similar to how local dirs are loaded.
	// Related: https://github.com/dagger/dagger/issues/6292
	err = s.dag.Select(ctx, gitRef, &gitSrc.ContextDirectory,
		dagql.Selector{Field: "tree"},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to load git dir: %w", err)
	}
	gitSrc.Git.UnfilteredContextDir = gitSrc.ContextDirectory

	configPath := filepath.Join(gitSrc.SourceRootSubpath, modules.Filename)
	var configContents string
	err = s.dag.Select(ctx, gitSrc.ContextDirectory, &configContents,
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(configPath)},
			},
		},
		dagql.Selector{Field: "contents"},
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return inst, fmt.Errorf("git module source %q does not contain a dagger config file", gitSrc.AsString())
		}
		return inst, fmt.Errorf("failed to load git module dagger config: %w", err)
	}
	if err := s.initFromModConfig([]byte(configContents), gitSrc); err != nil {
		return inst, err
	}

	bk, err := query.Self.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	// load this module source's context directory and deps in parallel
	var eg errgroup.Group
	eg.Go(func() error {
		if err := s.loadModuleSourceContext(ctx, bk, gitSrc); err != nil {
			return fmt.Errorf("failed to load git module source context: %w", err)
		}

		if gitSrc.SDK != nil {
			gitSrc.SDKImpl, err = s.sdkForModule(ctx, query.Self, gitSrc.SDK, gitSrc)
			if err != nil {
				return fmt.Errorf("failed to load sdk for git module source: %w", err)
			}
		}

		return nil
	})

	gitSrc.Dependencies = make([]dagql.Instance[*core.ModuleSource], len(gitSrc.ConfigDependencies))
	for i, depCfg := range gitSrc.ConfigDependencies {
		eg.Go(func() error {
			var err error
			gitSrc.Dependencies[i], err = s.resolveDepToSource(ctx, bk, gitSrc, depCfg.Source, depCfg.Pin, depCfg.Name)
			if err != nil {
				return fmt.Errorf("failed to resolve dep to source: %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, err
	}

	// the directory is not necessarily content-hashed, make it so and use that as our digest
	gitSrc.ContextDirectory, err = core.MakeDirectoryContentHashed(ctx, bk, gitSrc.ContextDirectory)
	if err != nil {
		return inst, fmt.Errorf("failed to hash git context directory: %w", err)
	}

	gitSrc.Digest = gitSrc.CalcDigest().String()

	return dagql.NewInstanceForCurrentID(ctx, s.dag, query, gitSrc)
}

type directoryAsModuleArgs struct {
	SourceRootPath string `default:"."`
}

func (s *moduleSchema) directoryAsModule(
	ctx context.Context,
	contextDir dagql.Instance[*core.Directory],
	args directoryAsModuleArgs,
) (inst dagql.Instance[*core.Module], err error) {
	err = s.dag.Select(ctx, contextDir, &inst,
		dagql.Selector{
			Field: "asModuleSource",
			Args: []dagql.NamedInput{
				{Name: "sourceRootPath", Value: dagql.String(args.SourceRootPath)},
			},
		},
		dagql.Selector{
			Field: "asModule",
		},
	)
	return inst, err
}

func (s *moduleSchema) directoryAsModuleSource(
	ctx context.Context,
	contextDir dagql.Instance[*core.Directory],
	args directoryAsModuleArgs,
) (inst dagql.Instance[*core.ModuleSource], err error) {
	sourceRootSubpath := args.SourceRootPath
	if sourceRootSubpath == "" {
		sourceRootSubpath = "."
	}

	dirSrc := &core.ModuleSource{
		Query:             contextDir.Self.Query,
		ConfigExists:      true, // we can't load uninitialized dir modules, we'll error out later if it's not there
		SourceRootSubpath: sourceRootSubpath,
		ContextDirectory:  contextDir,
		Kind:              core.ModuleSourceKindDir,
	}
	if dirSrc.SourceRootSubpath == "" {
		dirSrc.SourceRootSubpath = "."
	}

	configPath := filepath.Join(dirSrc.SourceRootSubpath, modules.Filename)
	var configContents string
	err = s.dag.Select(ctx, contextDir, &configContents,
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(configPath)},
			},
		},
		dagql.Selector{Field: "contents"},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to load dir module dagger config: %w", err)
	}
	if err := s.initFromModConfig([]byte(configContents), dirSrc); err != nil {
		return inst, err
	}

	// load this module source's deps in parallel
	bk, err := contextDir.Self.Query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	var eg errgroup.Group

	if dirSrc.SDK != nil {
		eg.Go(func() error {
			if err := s.loadModuleSourceContext(ctx, bk, dirSrc); err != nil {
				return err
			}

			var err error
			dirSrc.SDKImpl, err = s.sdkForModule(ctx, contextDir.Self.Query, dirSrc.SDK, dirSrc)
			if err != nil {
				return fmt.Errorf("failed to load sdk for dir module source: %w", err)
			}

			return nil
		})
	}

	dirSrc.Dependencies = make([]dagql.Instance[*core.ModuleSource], len(dirSrc.ConfigDependencies))
	for i, depCfg := range dirSrc.ConfigDependencies {
		eg.Go(func() error {
			var err error
			dirSrc.Dependencies[i], err = s.resolveDepToSource(ctx, bk, dirSrc, depCfg.Source, depCfg.Pin, depCfg.Name)
			if err != nil {
				return fmt.Errorf("failed to resolve dep to source: %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, err
	}

	inst, err = dagql.NewInstanceForCurrentID(ctx, s.dag, contextDir, dirSrc)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}

	dirSrc.Digest = dirSrc.CalcDigest().String()
	inst = inst.WithMetadata(digest.Digest(dirSrc.Digest), true)
	return inst, nil
}

func (s *moduleSchema) initFromModConfig(configBytes []byte, src *core.ModuleSource) error {
	// sanity checks
	if src.SourceRootSubpath == "" {
		return fmt.Errorf("source root path must be set")
	}

	modCfg := &modules.ModuleConfigWithUserFields{}
	if err := json.Unmarshal(configBytes, modCfg); err != nil {
		return fmt.Errorf("failed to unmarshal module config: %w", err)
	}

	src.ModuleName = modCfg.Name
	src.ModuleOriginalName = modCfg.Name
	src.EngineVersion = modCfg.EngineVersion
	src.IncludePaths = modCfg.Include
	src.CodegenConfig = modCfg.Codegen
	src.ModuleConfigUserFields = modCfg.ModuleConfigUserFields
	src.ConfigDependencies = modCfg.Dependencies

	if modCfg.SDK != nil {
		src.SDK = &core.SDKConfig{
			Source: modCfg.SDK.Source,
		}
	}

	// figure out source subpath
	if modCfg.Source != "" && !filepath.IsLocal(modCfg.Source) {
		return fmt.Errorf("source path %q contains parent directory components", modCfg.Source)
	}

	var sdkSource string
	if modCfg.SDK != nil {
		sdkSource = modCfg.SDK.Source
	}
	switch {
	case sdkSource == "" && modCfg.Source != "":
		// invalid, can't have source when no sdk
		return fmt.Errorf("source path %q specified without sdk", modCfg.Source)
	case sdkSource == "":
		// skip setting source subpath when no sdk
	case sdkSource != "" && modCfg.Source == "":
		// sdk was set but source was not, it's implicitly "." and thus the source root
		src.SourceSubpath = src.SourceRootSubpath
	case sdkSource != "" && modCfg.Source != "":
		// sdk was set and source was too, get the full rel path under the context
		src.SourceSubpath = filepath.Join(src.SourceRootSubpath, modCfg.Source)
	}

	src.FullIncludePaths = append(src.FullIncludePaths,
		// always load the config file (currently mainly so it gets incorporated into the digest)
		src.SourceRootSubpath+"/"+modules.Filename,
	)
	if src.SourceSubpath != "" {
		// load the source dir if set
		src.FullIncludePaths = append(src.FullIncludePaths, src.SourceSubpath+"/**/*")
	} else {
		// otherwise load the source root; this supports use cases like an sdk-less module w/ a pyproject.toml
		// that's now going to be upgraded to using the python sdk and needs pyproject.toml to be loaded
		// TODO: might be better to dynamically load more when WithSourceSubpath is called
		// TODO: might be better to dynamically load more when WithSourceSubpath is called
		// TODO: might be better to dynamically load more when WithSourceSubpath is called
		src.FullIncludePaths = append(src.FullIncludePaths, src.SourceRootSubpath+"/**/*")
	}

	// add the config file includes, rebasing them from being relative to the config file
	// to being relative to the context dir
	rebasedIncludes, err := rebasePatterns(modCfg.Include, src.SourceRootSubpath)
	if err != nil {
		return err
	}
	src.FullIncludePaths = append(src.FullIncludePaths, rebasedIncludes...)

	return nil
}

func rebasePatterns(patterns []string, base string) ([]string, error) {
	rebased := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		isNegation := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		relPath := filepath.Join(base, pattern)
		if !filepath.IsLocal(relPath) {
			return nil, fmt.Errorf("include/exclude path %q escapes context", relPath)
		}
		if isNegation {
			relPath = "!" + relPath
		}
		rebased = append(rebased, relPath)
	}
	return rebased, nil
}

func (s *moduleSchema) loadModuleSourceContext(
	ctx context.Context,
	bk *buildkit.Client,
	src *core.ModuleSource,
) error {
	switch src.Kind {
	case core.ModuleSourceKindLocal:
		err := s.dag.Select(ctx, s.dag.Root(), &src.ContextDirectory,
			dagql.Selector{Field: "host"},
			dagql.Selector{
				Field: "directory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(src.Local.ContextDirectoryPath)},
					{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(src.FullIncludePaths...))},
				},
			},
		)
		if err != nil {
			return err
		}

	case core.ModuleSourceKindGit:
		err := s.dag.Select(ctx, s.dag.Root(), &src.ContextDirectory,
			dagql.Selector{Field: "directory"},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "directory", Value: dagql.NewID[*core.Directory](src.Git.UnfilteredContextDir.ID())},
					{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(src.FullIncludePaths...))},
				},
			},
		)
		if err != nil {
			return err
		}

		// the directory is not necessarily content-hashed, make it such so we can use that as our digest
		src.ContextDirectory, err = core.MakeDirectoryContentHashed(ctx, bk, src.ContextDirectory)
		if err != nil {
			return fmt.Errorf("failed to hash git context directory: %w", err)
		}
	}

	return nil
}

func (s *moduleSchema) resolveDepToSource(
	ctx context.Context,
	bk *buildkit.Client,
	parentSrc *core.ModuleSource,
	depSrcRef string,
	depPin string,
	depName string,
) (inst dagql.Instance[*core.ModuleSource], err error) {
	// sanity checks
	if parentSrc != nil {
		if parentSrc.SourceRootSubpath == "" {
			return inst, fmt.Errorf("source root path must be set")
		}
		if parentSrc.ModuleName == "" {
			return inst, fmt.Errorf("module name must be set")
		}
	}

	parsedDepRef, err := parseRefString(
		ctx,
		moduleSourceDirExistsFS{bk, parentSrc},
		depSrcRef,
		depPin,
		false,
	)
	if err != nil {
		return inst, fmt.Errorf("failed to parse dep ref string: %w", err)
	}

	switch parsedDepRef.kind {
	case core.ModuleSourceKindLocal:
		if parentSrc == nil {
			return inst, fmt.Errorf("local module dep source path %q must be relative to a parent module", depSrcRef)
		}

		if filepath.IsAbs(depSrcRef) {
			return inst, fmt.Errorf("local module dep source path %q is absolute", depSrcRef)
		}

		switch parentSrc.Kind {
		case core.ModuleSourceKindLocal:
			// parent=local, dep=local
			depPath := filepath.Join(parentSrc.Local.ContextDirectoryPath, parentSrc.SourceRootSubpath, depSrcRef)
			depRelPath, err := pathutil.LexicalRelativePath(parentSrc.Local.ContextDirectoryPath, depPath)
			if err != nil {
				return inst, fmt.Errorf("failed to get relative path from context to dep: %w", err)
			}
			if !filepath.IsLocal(depRelPath) {
				return inst, fmt.Errorf("local module dep source path %q escapes context %q", depRelPath, parentSrc.Local.ContextDirectoryPath)
			}

			selectors := []dagql.Selector{{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(depPath)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err = s.dag.Select(ctx, s.dag.Root(), &inst, selectors...)
			if err != nil {
				if errors.Is(err, dagql.ErrCacheMapRecursiveCall) {
					return inst, fmt.Errorf("module %q has a circular dependency on itself through dependency %q", parentSrc.ModuleName, depName)
				}
				return inst, fmt.Errorf("failed to load local dep: %w", err)
			}
			return inst, nil

		case core.ModuleSourceKindGit:
			// parent=git, dep=local
			refString := core.GitRefString(
				parentSrc.Git.CloneRef,
				filepath.Join(parentSrc.SourceRootSubpath, depSrcRef),
				parentSrc.Git.Version,
			)
			selectors := []dagql.Selector{{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(refString)},
					{Name: "refPin", Value: dagql.String(parentSrc.Git.Commit)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err := s.dag.Select(ctx, s.dag.Root(), &inst, selectors...)
			if err != nil {
				return inst, fmt.Errorf("failed to load local dep: %w", err)
			}
			return inst, nil

		case core.ModuleSourceKindDir:
			// parent=dir, dep=local
			depPath := filepath.Join(parentSrc.SourceRootSubpath, depSrcRef)
			selectors := []dagql.Selector{{
				Field: "asModuleSource",
				Args: []dagql.NamedInput{
					{Name: "sourceRootPath", Value: dagql.String(depPath)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err := s.dag.Select(ctx, parentSrc.ContextDirectory, &inst, selectors...)
			if err != nil {
				return inst, fmt.Errorf("failed to load local dep: %w", err)
			}
			return inst, nil

		default:
			return inst, fmt.Errorf("unsupported parent module source kind: %s", parentSrc.Kind)
		}

	case core.ModuleSourceKindGit:
		// parent=*, dep=git
		selectors := []dagql.Selector{{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(depSrcRef)},
				{Name: "refPin", Value: dagql.String(depPin)},
			},
		}}
		if depName != "" {
			selectors = append(selectors, dagql.Selector{
				Field: "withName",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(depName)},
				},
			})
		}
		err := s.dag.Select(ctx, s.dag.Root(), &inst, selectors...)
		if err != nil {
			return inst, fmt.Errorf("failed to load git dep: %w", err)
		}
		return inst, nil

	default:
		return inst, fmt.Errorf("unsupported module source kind: %s", parsedDepRef.kind)
	}
}

// TODO: DOC THAT THIS ARG IS RELATIVE TO THE SOURCE ROOT
func (s *moduleSchema) moduleSourceSubpath(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	// TODO: could just make it a field again if it stays like this
	return src.SourceSubpath, nil
}

// TODO: DOC THAT THIS ARG IS RELATIVE TO THE SOURCE ROOT
func (s *moduleSchema) moduleSourceWithSourceSubpath(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Path string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()
	src.SourceSubpath = filepath.Join(src.SourceRootSubpath, args.Path)

	contextRootPath := "/"
	if src.Kind == core.ModuleSourceKindLocal {
		contextRootPath = src.Local.ContextDirectoryPath
	}

	relPath, err := pathutil.LexicalRelativePath(
		filepath.Join(contextRootPath, src.SourceRootSubpath),
		filepath.Join(contextRootPath, src.SourceSubpath),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get relative path from source root to source subpath: %w", err)
	}
	if !filepath.IsLocal(relPath) {
		return nil, fmt.Errorf("local module source subpath %q escapes source root %q", relPath, src.SourceRootSubpath)
	}

	src.Digest = src.CalcDigest().String()
	return src, nil
}

func (s *moduleSchema) moduleSourceAsString(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.AsString(), nil
}

func (s *moduleSchema) moduleSourcePin(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.Pin(), nil
}

func (s *moduleSchema) moduleSourceWithName(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Name string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()
	src.ModuleName = args.Name
	if src.ModuleOriginalName == "" {
		src.ModuleOriginalName = args.Name
	}

	src.Digest = src.CalcDigest().String()
	return src, nil
}

func (s *moduleSchema) moduleSourceWithIncludes(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Patterns []string
	},
) (*core.ModuleSource, error) {
	if len(args.Patterns) == 0 {
		return src, nil
	}

	src = src.Clone()
	src.IncludePaths = append(src.IncludePaths, args.Patterns...)
	rebasedIncludes, err := rebasePatterns(args.Patterns, src.SourceRootSubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to rebase include paths: %w", err)
	}
	src.FullIncludePaths = append(src.FullIncludePaths, rebasedIncludes...)

	// reload context in case includes have changed it
	bk, err := src.Query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	if err := s.loadModuleSourceContext(ctx, bk, src); err != nil {
		return nil, fmt.Errorf("failed to reload module source context: %w", err)
	}

	src.Digest = src.CalcDigest().String()
	return src, nil
}

func (s *moduleSchema) moduleSourceWithSDK(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Source string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()
	if args.Source == "" {
		src.SDK = nil
		src.SDKImpl = nil

		src.Digest = src.CalcDigest().String()
		return src, nil
	}

	if src.SDK == nil {
		src.SDK = &core.SDKConfig{}
	}
	src.SDK.Source = args.Source

	var err error
	src.SDKImpl, err = s.sdkForModule(ctx, src.Query, src.SDK, src)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk for module source: %w", err)
	}

	src.Digest = src.CalcDigest().String()
	return src, nil
}

func (s *moduleSchema) moduleSourceWithEngineVersion(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Version string
	},
) (*core.ModuleSource, error) {
	// TODO: handle special strings like latest
	// TODO: handle special strings like latest
	// TODO: handle special strings like latest
	src = src.Clone()
	src.EngineVersion = args.Version

	src.Digest = src.CalcDigest().String()
	return src, nil
}

func (s *moduleSchema) moduleSourceLocalContextDirectoryPath(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindLocal {
		return "", fmt.Errorf("module source is not local")
	}
	return src.Local.ContextDirectoryPath, nil
}

func (s *moduleSchema) moduleSourceWithDependencies(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Dependencies []core.ModuleSourceID
	},
) (*core.ModuleSource, error) {
	parentSrc = parentSrc.Clone()

	newDeps, err := collectIDInstances(ctx, s.dag, args.Dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to load module source dependencies from ids: %w", err)
	}
	var allDeps []dagql.Instance[*core.ModuleSource]
	for _, newDep := range newDeps {
		switch parentSrc.Kind {
		case core.ModuleSourceKindLocal:
			switch newDep.Self.Kind {
			case core.ModuleSourceKindLocal:
				// parent=local, dep=local

				// local deps must be located in the same context as the parent, this enforces they are in the same local
				// git repo checkout and a local dep doesn't exist in a different git repo (which is what git deps are for)
				contextRelPath, err := pathutil.LexicalRelativePath(
					parentSrc.Local.ContextDirectoryPath,
					newDep.Self.Local.ContextDirectoryPath,
				)
				if err != nil {
					return nil, fmt.Errorf("failed to get relative path from parent context to dep context: %w", err)
				}
				if !filepath.IsLocal(contextRelPath) {
					return nil, fmt.Errorf("local module dependency context directory %q is not in parent context directory %q",
						newDep.Self.Local.ContextDirectoryPath, parentSrc.Local.ContextDirectoryPath)
				}
				allDeps = append(allDeps, newDep)

			case core.ModuleSourceKindGit:
				// parent=local, dep=git
				allDeps = append(allDeps, newDep)

			default:
				return nil, fmt.Errorf("unhandled module source kind: %s", parentSrc.Kind)
			}

		case core.ModuleSourceKindGit:
			switch newDep.Self.Kind {
			case core.ModuleSourceKindLocal:
				// parent=git, dep=local
				// cannot add a module source that's local to the caller as a dependency of a git module source
				return nil, fmt.Errorf("cannot add local module source as dependency of git module source")

			case core.ModuleSourceKindGit:
				// parent=git, dep=git
				allDeps = append(allDeps, newDep)

			default:
				return nil, fmt.Errorf("unhandled module source kind: %s", parentSrc.Kind)
			}

		default:
			return nil, fmt.Errorf("unhandled module source kind: %s", parentSrc.Kind)
		}
	}

	allDeps = append(allDeps, parentSrc.Dependencies...)

	symbolicDeps := make(map[string]dagql.Instance[*core.ModuleSource], len(allDeps))
	depNames := make(map[string]dagql.Instance[*core.ModuleSource], len(allDeps))
	for _, dep := range allDeps {
		var symbolicDepStr string
		switch dep.Self.Kind {
		case core.ModuleSourceKindLocal:
			symbolicDepStr = filepath.Join(dep.Self.Local.ContextDirectoryPath, dep.Self.SourceRootSubpath)
		case core.ModuleSourceKindGit:
			symbolicDepStr = dep.Self.Git.CloneRef
			if dep.Self.SourceRootSubpath != "" {
				symbolicDepStr += "/" + strings.TrimPrefix(dep.Self.SourceRootSubpath, "/")
			}
		}

		_, isDuplicateSymbolic := symbolicDeps[symbolicDepStr]
		if isDuplicateSymbolic {
			// prefer the new dep over the existing one (new deps were added to allDeps first, so we will only hit this
			// if a new dep overrides an existing one)
			continue
		}
		symbolicDeps[symbolicDepStr] = dep

		_, isDuplicateName := depNames[dep.Self.ModuleName]
		if isDuplicateName {
			return nil, fmt.Errorf("duplicate dependency name %q", dep.Self.ModuleName)
		}
		depNames[dep.Self.ModuleName] = dep
	}

	// get the final slice of deps, sorting by name for determinism
	finalDeps := make([]dagql.Instance[*core.ModuleSource], 0, len(symbolicDeps))
	for _, dep := range symbolicDeps {
		finalDeps = append(finalDeps, dep)
	}
	sort.Slice(finalDeps, func(i, j int) bool {
		return finalDeps[i].Self.ModuleName < finalDeps[j].Self.ModuleName
	})
	parentSrc.Dependencies = finalDeps

	parentSrc.Digest = parentSrc.CalcDigest().String()
	return parentSrc, nil
}

func (s *moduleSchema) moduleSourceWithUpdateDependencies(
	ctx context.Context,
	parentSrc dagql.Instance[*core.ModuleSource],
	args struct {
		Dependencies []string
	},
) (inst dagql.Instance[*core.ModuleSource], _ error) {
	type updateReq struct {
		symbolic string // either 1) a name of a dep or 2) the source minus any @version
		version  string // the version to update to, if any specified
	}
	updateReqs := make(map[updateReq]struct{}, len(args.Dependencies))
	for _, updateArg := range args.Dependencies {
		req := updateReq{}
		req.symbolic, req.version, _ = strings.Cut(updateArg, "@")
		updateReqs[req] = struct{}{}
	}

	var newUpdatedDepArgs []core.ModuleSourceID
	for _, existingDep := range parentSrc.Self.Dependencies {
		// if no update requests, implicitly update all deps
		if len(updateReqs) == 0 {
			if existingDep.Self.Kind == core.ModuleSourceKindLocal {
				// local dep, skip update
				continue
			}

			var updatedDep dagql.Instance[*core.ModuleSource]
			err := s.dag.Select(ctx, s.dag.Root(), &updatedDep,
				dagql.Selector{
					Field: "moduleSource",
					Args: []dagql.NamedInput{
						{Name: "refString", Value: dagql.String(existingDep.Self.AsString())},
					},
				},
			)
			if err != nil {
				return inst, fmt.Errorf("failed to load existing dep: %w", err)
			}

			newUpdatedDepArgs = append(newUpdatedDepArgs, dagql.NewID[*core.ModuleSource](updatedDep.ID()))
			continue
		}

		// if the existingDep is local and requested to be updated, return error, otherwise skip it
		if existingDep.Self.Kind == core.ModuleSourceKindLocal {
			for updateReq := range updateReqs {
				if updateReq.symbolic == existingDep.Self.ModuleName {
					return inst, fmt.Errorf("updating local deps is not supported")
				}

				var contextRoot string
				switch parentSrc.Self.Kind {
				case core.ModuleSourceKindLocal:
					contextRoot = parentSrc.Self.Local.ContextDirectoryPath
				case core.ModuleSourceKindGit:
					contextRoot = "/"
				default:
					return inst, fmt.Errorf("unknown module source kind: %s", parentSrc.Self.Kind)
				}

				parentSrcRoot := filepath.Join(contextRoot, parentSrc.Self.SourceRootSubpath)
				depSrcRoot := filepath.Join(contextRoot, existingDep.Self.SourceRootSubpath)
				existingSymbolic, err := pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
				if err != nil {
					return inst, fmt.Errorf("failed to get relative path: %w", err)
				}

				if updateReq.symbolic == existingSymbolic {
					return inst, fmt.Errorf("updating local deps is not supported")
				}
			}
			continue
		}

		existingName := existingDep.Self.ModuleName
		existingVersion := existingDep.Self.Git.Version
		existingSymbolic := existingDep.Self.Git.CloneRef
		if depSrcRoot := existingDep.Self.SourceRootSubpath; depSrcRoot != "" {
			existingSymbolic += "/" + strings.TrimPrefix(depSrcRoot, "/")
		}
		for updateReq := range updateReqs {
			// check whether this updateReq matches the existing dep
			if updateReq.symbolic != existingName && updateReq.symbolic != existingSymbolic {
				continue
			}
			delete(updateReqs, updateReq)

			// if a specific version was requested, use that
			// else use whatever version current version is configured to use
			updateVersion := updateReq.version
			if updateVersion == "" {
				updateVersion = existingVersion
			}
			updateRef := existingSymbolic
			if updateVersion != "" {
				updateRef += "@" + updateVersion
			}

			var updatedDep dagql.Instance[*core.ModuleSource]
			err := s.dag.Select(ctx, s.dag.Root(), &updatedDep,
				dagql.Selector{
					Field: "moduleSource",
					Args: []dagql.NamedInput{
						{Name: "refString", Value: dagql.String(updateRef)},
					},
				},
			)
			if err != nil {
				return inst, fmt.Errorf("failed to load new dep: %w", err)
			}

			newUpdatedDepArgs = append(newUpdatedDepArgs, dagql.NewID[*core.ModuleSource](updatedDep.ID()))
		}
	}

	if len(updateReqs) > 0 {
		deps := make([]string, 0, len(updateReqs))
		for updateReq := range updateReqs {
			deps = append(deps, updateReq.symbolic)
		}
		return inst, fmt.Errorf("dependency %q was requested to be updated, but it is not found in the dependencies list", strings.Join(deps, ","))
	}

	// TODO: this might make telemetry look a little confusing, could retain current instance
	// TODO: this might make telemetry look a little confusing, could retain current instance
	// TODO: this might make telemetry look a little confusing, could retain current instance
	err := s.dag.Select(ctx, parentSrc, &inst,
		dagql.Selector{
			Field: "withDependencies",
			Args: []dagql.NamedInput{{
				Name:  "dependencies",
				Value: dagql.ArrayInput[core.ModuleSourceID](newUpdatedDepArgs),
			}},
		},
	)
	return inst, err
}

func (s *moduleSchema) moduleSourceWithoutDependencies(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Dependencies []string
	},
) (*core.ModuleSource, error) {
	parentSrc = parentSrc.Clone()

	var filteredDeps []dagql.Instance[*core.ModuleSource]
	for _, existingDep := range parentSrc.Dependencies {
		var existingName, existingSymbolic, existingVersion string
		switch parentSrc.Kind {
		case core.ModuleSourceKindLocal:
			switch existingDep.Self.Kind {
			case core.ModuleSourceKindLocal:
				// parent=local, dep=local
				existingName = existingDep.Self.ModuleName
				parentSrcRoot := filepath.Join(parentSrc.Local.ContextDirectoryPath, parentSrc.SourceRootSubpath)
				depSrcRoot := filepath.Join(parentSrc.Local.ContextDirectoryPath, existingDep.Self.SourceRootSubpath)
				var err error
				existingSymbolic, err = pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
				if err != nil {
					return nil, fmt.Errorf("failed to get relative path: %w", err)
				}

			case core.ModuleSourceKindGit:
				// parent=local, dep=git
				existingName = existingDep.Self.ModuleName
				existingSymbolic = existingDep.Self.Git.CloneRef
				if existingDep.Self.SourceRootSubpath != "" {
					existingSymbolic += "/" + strings.TrimPrefix(existingDep.Self.SourceRootSubpath, "/")
				}
				existingVersion = existingDep.Self.Git.Version

			default:
				return nil, fmt.Errorf("unknown module source kind: %s", parentSrc.Kind)
			}

		case core.ModuleSourceKindGit:
			switch existingDep.Self.Kind {
			case core.ModuleSourceKindLocal:
				// parent=git, dep=local
				return nil, fmt.Errorf("cannot remove local module source dependency from git module source")

			case core.ModuleSourceKindGit:
				// parent=git, dep=git
				existingName = existingDep.Self.ModuleName
				existingSymbolic = existingDep.Self.Git.CloneRef
				if existingDep.Self.SourceRootSubpath != "" {
					existingSymbolic += "/" + strings.TrimPrefix(existingDep.Self.SourceRootSubpath, "/")
				}
				existingVersion = existingDep.Self.Git.Version

			default:
				return nil, fmt.Errorf("unknown module source kind: %s", parentSrc.Kind)
			}

		default:
			return nil, fmt.Errorf("unknown module source kind: %s", parentSrc.Kind)
		}

		keep := true
		for _, depArg := range args.Dependencies {
			depSymbolic, depVersion, _ := strings.Cut(depArg, "@")

			// dagger.json doesn't prefix relative paths with ./, so strip that here
			// TODO: is this robust enough?
			// TODO: is this robust enough?
			// TODO: is this robust enough?
			depSymbolic = strings.TrimPrefix(depSymbolic, "./")

			if depSymbolic != existingName && depSymbolic != existingSymbolic {
				// not a match
				continue
			}
			keep = false

			if depVersion != "" {
				// return error if the version number was specified to uninstall, but that version is not installed
				// TODO(rajatjindal): we should possibly resolve commit from a version if current dep has no version specified and
				// see if we can match Pin() with that commit. But that would mean resolving the commit here.
				if existingVersion == "" {
					return nil, fmt.Errorf(
						"version %q was requested to be uninstalled but the dependency %q was originally installed without a specific version. Try re-running the uninstall command without specifying the version number",
						depVersion,
						existingSymbolic,
					)
				}

				// TODO: don't rerun this every loop, could just use a map and delete once matched
				// TODO: don't rerun this every loop, could just use a map and delete once matched
				// TODO: don't rerun this every loop, could just use a map and delete once matched
				parsedDepGitRef, err := parseGitRefString(ctx, depArg)
				if err != nil {
					return nil, fmt.Errorf("failed to parse git ref string %q: %w", depArg, err)
				}

				_, err = matchVersion([]string{existingVersion}, depVersion, parsedDepGitRef.repoRootSubdir)
				if err != nil {
					// if the requested version has prefix of repoRootSubDir, then send the error as it is
					// but if it does not, remove the repoRootSubDir from depVersion to avoid confusion.
					depReqModVersion := parsedDepGitRef.modVersion
					if !strings.HasPrefix(depReqModVersion, parsedDepGitRef.repoRootSubdir) {
						depReqModVersion, _ = strings.CutPrefix(depReqModVersion, parsedDepGitRef.repoRootSubdir+"/")
						existingVersion, _ = strings.CutPrefix(existingVersion, existingDep.Self.SourceRootSubpath+"/")
					}
					return nil, fmt.Errorf("version %q was requested to be uninstalled but the installed version is %q", depReqModVersion, existingVersion)
				}
			}
			break
		}
		if keep {
			filteredDeps = append(filteredDeps, existingDep)
		}
	}

	parentSrc.Dependencies = filteredDeps
	parentSrc.Digest = parentSrc.CalcDigest().String()
	return parentSrc, nil
}

func (s *moduleSchema) moduleSourceGeneratedContextDirectory(
	ctx context.Context,
	srcInst dagql.Instance[*core.ModuleSource],
	args struct{},
) (genDirInst dagql.Instance[*core.Directory], err error) {
	src := srcInst.Self
	modCfg := &modules.ModuleConfigWithUserFields{
		ModuleConfigUserFields: src.ModuleConfigUserFields,
		ModuleConfig: modules.ModuleConfig{
			Name:          src.ModuleName,
			EngineVersion: src.EngineVersion,
			Include:       src.IncludePaths,
			Codegen:       src.CodegenConfig,
		},
	}

	if src.SDK != nil {
		modCfg.SDK = &modules.SDK{
			Source: src.SDK.Source,
		}
	}

	switch modCfg.EngineVersion {
	case "":
		// older versions of dagger might not produce an engine version -
		// so return the version that engineVersion was introduced in
		modCfg.EngineVersion = engine.MinimumModuleVersion
	case modules.EngineVersionLatest:
		modCfg.EngineVersion = engine.Version
	}
	modCfg.EngineVersion = engine.NormalizeVersion(modCfg.EngineVersion)
	if !engine.CheckVersionCompatibility(modCfg.EngineVersion, engine.MinimumModuleVersion) {
		return genDirInst, fmt.Errorf("module requires dagger %s, but support for that version has been removed", modCfg.EngineVersion)
	}
	if !engine.CheckMaxVersionCompatibility(modCfg.EngineVersion, engine.BaseVersion(engine.Version)) {
		return genDirInst, fmt.Errorf("module requires dagger %s, but you have %s", modCfg.EngineVersion, engine.Version)
	}

	switch srcInst.Self.SourceSubpath {
	case "":
		// leave unset
	default:
		var err error
		modCfg.Source, err = pathutil.LexicalRelativePath(
			filepath.Join("/", src.SourceRootSubpath),
			filepath.Join("/", src.SourceSubpath),
		)
		if err != nil {
			return genDirInst, fmt.Errorf("failed to get relative path from source root to source: %w", err)
		}
		// if source is ".", leave it unset in dagger.json as that's the default
		if modCfg.Source == "." {
			modCfg.Source = ""
		}
	}

	modCfg.Dependencies = make([]*modules.ModuleConfigDependency, len(src.Dependencies))
	for i, depSrc := range src.Dependencies {
		depCfg := &modules.ModuleConfigDependency{
			Name: depSrc.Self.ModuleName,
		}
		modCfg.Dependencies[i] = depCfg

		switch srcInst.Self.Kind {
		case core.ModuleSourceKindLocal:
			switch depSrc.Self.Kind {
			case core.ModuleSourceKindLocal:
				// parent=local, dep=local
				parentSrcRoot := filepath.Join(src.Local.ContextDirectoryPath, src.SourceRootSubpath)
				depSrcRoot := filepath.Join(depSrc.Self.Local.ContextDirectoryPath, depSrc.Self.SourceRootSubpath)
				depSrcRoot, err := pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
				if err != nil {
					return genDirInst, fmt.Errorf("failed to get relative path: %w", err)
				}
				depCfg.Source = depSrcRoot

			case core.ModuleSourceKindGit:
				// parent=local, dep=git
				depCfg.Source = depSrc.Self.AsString()
				depCfg.Pin = depSrc.Self.Git.Pin

			default:
				return genDirInst, fmt.Errorf("unhandled module source kind: %s", srcInst.Self.Kind)
			}

		case core.ModuleSourceKindGit:
			switch depSrc.Self.Kind {
			case core.ModuleSourceKindLocal:
				// parent=git, dep=local
				return genDirInst, fmt.Errorf("cannot add local module source as dependency of git module source")

			case core.ModuleSourceKindGit:
				// parent=git, dep=git
				// check if the dep is the same git repo + pin as the parent, if so make it a local dep
				if srcInst.Self.Git.CloneRef == depSrc.Self.Git.CloneRef && srcInst.Self.Git.Pin == depSrc.Self.Git.Pin {
					parentSrcRoot := filepath.Join("/", src.SourceRootSubpath)
					depSrcRoot := filepath.Join("/", depSrc.Self.SourceRootSubpath)
					depSrcRoot, err := pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
					if err != nil {
						return genDirInst, fmt.Errorf("failed to get relative path: %w", err)
					}
					depCfg.Source = depSrcRoot
				} else {
					depCfg.Source = depSrc.Self.AsString()
					depCfg.Pin = depSrc.Self.Git.Pin
				}

			default:
				return genDirInst, fmt.Errorf("unhandled module source kind: %s", srcInst.Self.Kind)
			}

		default:
			return genDirInst, fmt.Errorf("unhandled module source kind: %s", srcInst.Self.Kind)
		}
	}

	// run codegen too if we have a name and SDK
	genDirInst = src.ContextDirectory
	if modCfg.Name != "" && modCfg.SDK != nil && modCfg.SDK.Source != "" {
		var eg errgroup.Group
		depMods := make([]dagql.Instance[*core.Module], len(src.Dependencies))
		for i, depSrc := range srcInst.Self.Dependencies {
			eg.Go(func() error {
				return s.dag.Select(ctx, depSrc, &depMods[i],
					dagql.Selector{Field: "asModule"},
				)
			})
		}
		if err := eg.Wait(); err != nil {
			return genDirInst, fmt.Errorf("failed to load module dependencies: %w", err)
		}

		defaultDeps, err := srcInst.Self.Query.DefaultDeps(ctx)
		if err != nil {
			return genDirInst, fmt.Errorf("failed to get default dependencies: %w", err)
		}
		deps := core.NewModDeps(srcInst.Self.Query, defaultDeps.Mods)
		for _, depMod := range depMods {
			deps = deps.Append(depMod.Self)
		}
		for i, depMod := range deps.Mods {
			if coreMod, ok := depMod.(*CoreMod); ok {
				// this is needed so that a module's dependency on the core
				// uses the correct schema version
				dag := *coreMod.Dag
				dag.View = engine.BaseVersion(engine.NormalizeVersion(modCfg.EngineVersion))
				deps.Mods[i] = &CoreMod{Dag: &dag}
			}
		}

		// TODO: wrap up in nicer looking util/interface
		_, _, err = s.dag.Cache.GetOrInitialize(ctx, digest.Digest(srcInst.Self.Digest), func(context.Context) (dagql.Typed, error) {
			return srcInst, nil
		})
		if err != nil {
			return genDirInst, fmt.Errorf("failed to get or initialize instance: %w", err)
		}
		srcInstContentHashed := srcInst.WithMetadata(digest.Digest(srcInst.Self.Digest), true)

		generatedCode, err := srcInst.Self.SDKImpl.Codegen(ctx, deps, srcInstContentHashed)
		if err != nil {
			return genDirInst, fmt.Errorf("failed to generate code: %w", err)
		}
		genDirInst = generatedCode.Code

		// update .gitattributes
		// (linter thinks this chunk of code is too similar to the below, but not clear abstraction is worth it)
		//nolint:dupl
		if len(generatedCode.VCSGeneratedPaths) > 0 {
			gitAttrsPath := filepath.Join(srcInst.Self.SourceSubpath, ".gitattributes")
			var gitAttrsContents []byte
			gitAttrsFile, err := srcInst.Self.ContextDirectory.Self.File(ctx, gitAttrsPath)
			if err == nil {
				gitAttrsContents, err = gitAttrsFile.Contents(ctx)
				if err != nil {
					return genDirInst, fmt.Errorf("failed to get git attributes file contents: %w", err)
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
				fileName := strings.TrimPrefix(fileName, "/")
				gitAttrsContents = append(gitAttrsContents,
					[]byte(fmt.Sprintf("/%s linguist-generated\n", fileName))...,
				)
			}

			err = s.dag.Select(ctx, genDirInst, &genDirInst,
				dagql.Selector{
					Field: "withNewFile",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(gitAttrsPath)},
						{Name: "contents", Value: dagql.String(gitAttrsContents)},
						{Name: "permissions", Value: dagql.Int(0o600)},
					},
				},
			)
			if err != nil {
				return genDirInst, fmt.Errorf("failed to add vcs generated file: %w", err)
			}
		}

		// update .gitignore
		writeGitignore := true // default to true if not set
		if srcInst.Self.CodegenConfig != nil && srcInst.Self.CodegenConfig.AutomaticGitignore != nil {
			writeGitignore = *srcInst.Self.CodegenConfig.AutomaticGitignore
		}
		// (linter thinks this chunk of code is too similar to the above, but not clear abstraction is worth it)
		//nolint:dupl
		if writeGitignore && len(generatedCode.VCSIgnoredPaths) > 0 {
			gitIgnorePath := filepath.Join(srcInst.Self.SourceSubpath, ".gitignore")
			var gitIgnoreContents []byte
			gitIgnoreFile, err := srcInst.Self.ContextDirectory.Self.File(ctx, gitIgnorePath)
			if err == nil {
				gitIgnoreContents, err = gitIgnoreFile.Contents(ctx)
				if err != nil {
					return genDirInst, fmt.Errorf("failed to get .gitignore file contents: %w", err)
				}
				if !bytes.HasSuffix(gitIgnoreContents, []byte("\n")) {
					gitIgnoreContents = append(gitIgnoreContents, []byte("\n")...)
				}
			}
			for _, fileName := range generatedCode.VCSIgnoredPaths {
				if bytes.Contains(gitIgnoreContents, []byte(fileName)) {
					continue
				}
				fileName := strings.TrimPrefix(fileName, "/")
				gitIgnoreContents = append(gitIgnoreContents,
					[]byte(fmt.Sprintf("/%s\n", fileName))...,
				)
			}

			err = s.dag.Select(ctx, genDirInst, &genDirInst,
				dagql.Selector{
					Field: "withNewFile",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(gitIgnorePath)},
						{Name: "contents", Value: dagql.String(gitIgnoreContents)},
						{Name: "permissions", Value: dagql.Int(0o600)},
					},
				},
			)
			if err != nil {
				return genDirInst, fmt.Errorf("failed to add vcs ignore file: %w", err)
			}
		}
	}

	modCfgBytes, err := json.MarshalIndent(modCfg, "", "  ")
	if err != nil {
		return genDirInst, fmt.Errorf("failed to encode module config: %w", err)
	}
	modCfgBytes = append(modCfgBytes, '\n')
	modCfgPath := filepath.Join(src.SourceRootSubpath, modules.Filename)
	err = s.dag.Select(ctx, genDirInst, &genDirInst,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(modCfgPath)},
				{Name: "contents", Value: dagql.String(modCfgBytes)},
				{Name: "permissions", Value: dagql.Int(0o644)},
			},
		},
	)
	if err != nil {
		return genDirInst, fmt.Errorf("failed to add updated dagger.json to context dir: %w", err)
	}

	err = s.dag.Select(ctx, src.ContextDirectory, &genDirInst,
		dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*core.Directory](genDirInst.ID())},
			},
		},
	)
	if err != nil {
		return genDirInst, fmt.Errorf("failed to get context dir diff: %w", err)
	}

	return genDirInst, nil
}

func (s *moduleSchema) moduleSourceAsModule(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) (inst dagql.Instance[*core.Module], err error) {
	if src.Self.ModuleName == "" || src.Self.SDK == nil || src.Self.SDK.Source == "" {
		return inst, fmt.Errorf("module name and SDK must be set")
	}

	mod := &core.Module{
		Query: src.Self.Query,

		Source: src,

		NameField:    src.Self.ModuleName,
		OriginalName: src.Self.ModuleOriginalName,

		SDKConfig: src.Self.SDK,
	}

	// TODO: wrap up in nicer looking util/interface
	_, _, err = s.dag.Cache.GetOrInitialize(ctx, digest.Digest(src.Self.Digest), func(context.Context) (dagql.Typed, error) {
		return src, nil
	})
	if err != nil {
		return inst, fmt.Errorf("failed to get or initialize instance: %w", err)
	}
	srcInstContentHashed := src.WithMetadata(digest.Digest(src.Self.Digest), true)

	loadDepModsCtx, loadDepModsSpan := core.Tracer(ctx).Start(ctx, "asModule load deps + sdk", telemetry.Internal())

	var eg errgroup.Group

	depMods := make([]dagql.Instance[*core.Module], len(src.Self.Dependencies))
	for i, depSrc := range src.Self.Dependencies {
		eg.Go(func() error {
			return s.dag.Select(loadDepModsCtx, depSrc, &depMods[i],
				dagql.Selector{Field: "asModule"},
			)
		})
	}

	if err := eg.Wait(); err != nil {
		loadDepModsSpan.End()
		return inst, fmt.Errorf("failed to load module dependencies: %w", err)
	}
	loadDepModsSpan.End()

	defaultDeps, err := src.Self.Query.DefaultDeps(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get default dependencies: %w", err)
	}
	deps := core.NewModDeps(src.Self.Query, defaultDeps.Mods)
	for _, depMod := range depMods {
		deps = deps.Append(depMod.Self)
	}
	for i, depMod := range deps.Mods {
		if coreMod, ok := depMod.(*CoreMod); ok {
			// this is needed so that a module's dependency on the core
			// uses the correct schema version
			dag := *coreMod.Dag

			// TODO: dedupe, cleanup
			// TODO: dedupe, cleanup
			// TODO: dedupe, cleanup
			engineVersion := src.Self.EngineVersion
			switch engineVersion {
			case "":
				// older versions of dagger might not produce an engine version -
				// so return the version that engineVersion was introduced in
				engineVersion = engine.MinimumModuleVersion
			case modules.EngineVersionLatest:
				engineVersion = engine.Version
			}
			engineVersion = engine.NormalizeVersion(engineVersion)
			if !engine.CheckVersionCompatibility(engineVersion, engine.MinimumModuleVersion) {
				return inst, fmt.Errorf("module requires dagger %s, but support for that version has been removed", engineVersion)
			}
			if !engine.CheckMaxVersionCompatibility(engineVersion, engine.BaseVersion(engine.Version)) {
				return inst, fmt.Errorf("module requires dagger %s, but you have %s", engineVersion, engine.Version)
			}

			dag.View = engine.BaseVersion(engine.NormalizeVersion(engineVersion))
			deps.Mods[i] = &CoreMod{Dag: &dag}
		}
	}
	mod.Deps = deps

	runtimeCtx, runtimeSpan := core.Tracer(ctx).Start(ctx, "asModule runtime", telemetry.Internal())

	mod.Runtime, err = src.Self.SDKImpl.Runtime(runtimeCtx, mod.Deps, srcInstContentHashed)
	if err != nil {
		runtimeSpan.End()
		return inst, fmt.Errorf("failed to get module runtime: %w", err)
	}
	runtimeSpan.End()

	getModDefCtx, getModDefSpan := core.Tracer(ctx).Start(ctx, "asModule getModDef", telemetry.Internal())

	// construct a special function with no object or function name, which tells
	// the SDK to return the module's definition (in terms of objects, fields and
	// functions)
	modName := src.Self.ModuleName
	getModDefFn, err := core.NewModFunction(
		getModDefCtx,
		src.Self.Query,
		mod,
		nil,
		mod.Runtime,
		core.NewFunction("", &core.TypeDef{
			Kind:     core.TypeDefKindObject,
			AsObject: dagql.NonNull(core.NewObjectTypeDef("Module", "")),
		}))
	if err != nil {
		getModDefSpan.End()
		return inst, fmt.Errorf("failed to create module definition function for module %q: %w", modName, err)
	}

	result, err := getModDefFn.Call(getModDefCtx, &core.CallOpts{
		Cache:                  true,
		SkipSelfSchema:         true,
		Server:                 s.dag,
		SkipCallDigestCacheKey: true,
	})
	if err != nil {
		getModDefSpan.End()
		return inst, fmt.Errorf("failed to call module %q to get functions: %w", modName, err)
	}
	resultInst, ok := result.(dagql.Instance[*core.Module])
	if !ok {
		getModDefSpan.End()
		return inst, fmt.Errorf("expected Module result, got %T", result)
	}
	getModDefSpan.End()

	mod.Description = resultInst.Self.Description
	for _, obj := range resultInst.Self.ObjectDefs {
		mod, err = mod.WithObject(ctx, obj)
		if err != nil {
			return inst, fmt.Errorf("failed to add object to module %q: %w", modName, err)
		}
	}
	for _, iface := range resultInst.Self.InterfaceDefs {
		mod, err = mod.WithInterface(ctx, iface)
		if err != nil {
			return inst, fmt.Errorf("failed to add interface to module %q: %w", modName, err)
		}
	}
	for _, enum := range resultInst.Self.EnumDefs {
		mod, err = mod.WithEnum(ctx, enum)
		if err != nil {
			return inst, fmt.Errorf("failed to add enum to module %q: %w", mod.Name(), err)
		}
	}

	mod.InstanceID = dagql.CurrentID(ctx)

	// TODO: srcInstContentHashed here???
	// TODO: srcInstContentHashed here???
	// TODO: srcInstContentHashed here???
	// TODO: srcInstContentHashed here???
	inst, err = dagql.NewInstanceForCurrentID(ctx, s.dag, src, mod)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance for module %q: %w", modName, err)
	}

	// TODO: UPDATE DIGEST ON MODULESOURCE ANYWHERE TO INCLUDE GENERATED STUFF???
	// TODO: UPDATE DIGEST ON MODULESOURCE ANYWHERE TO INCLUDE GENERATED STUFF???
	// TODO: UPDATE DIGEST ON MODULESOURCE ANYWHERE TO INCLUDE GENERATED STUFF???
	// TODO: UPDATE DIGEST ON MODULESOURCE ANYWHERE TO INCLUDE GENERATED STUFF???
	// TODO: UPDATE DIGEST ON MODULESOURCE ANYWHERE TO INCLUDE GENERATED STUFF???
	// TODO: UPDATE DIGEST ON MODULESOURCE ANYWHERE TO INCLUDE GENERATED STUFF???

	return inst, nil
}

func callerHostFindUp(
	ctx context.Context,
	bk *buildkit.Client,
	curDirPath string,
	soughtName string,
) (string, bool, error) {
	found, err := callerHostFindUpAll(ctx, bk, curDirPath, map[string]struct{}{soughtName: {}})
	if err != nil {
		return "", false, err
	}
	p, ok := found[soughtName]
	return p, ok, nil
}

func callerHostFindUpAll(
	ctx context.Context,
	bk *buildkit.Client,
	curDirPath string,
	soughtNames map[string]struct{},
) (map[string]string, error) {
	found := make(map[string]string, len(soughtNames))
	for {
		for soughtName := range soughtNames {
			stat, err := bk.StatCallerHostPath(ctx, filepath.Join(curDirPath, soughtName), true)
			if err == nil {
				delete(soughtNames, soughtName)
				// NOTE: important that we use stat.Path here rather than curDirPath since the stat also
				// does some normalization of paths when the client is using case-insensitive filesystems
				found[soughtName] = filepath.Dir(stat.Path)
				continue
			}
			// TODO: remove the strings.Contains check here (which aren't cross-platform),
			// since we now set NotFound (since v0.11.2)
			if status.Code(err) != codes.NotFound && !strings.Contains(err.Error(), "no such file or directory") {
				return nil, fmt.Errorf("failed to lstat %s: %w", soughtName, err)
			}
		}

		if len(soughtNames) == 0 {
			// found everything
			break
		}

		nextDirPath := filepath.Dir(curDirPath)
		if curDirPath == nextDirPath {
			// hit root, nowhere else to look
			break
		}
		curDirPath = nextDirPath
	}

	return found, nil
}

type dirExistsFS interface {
	dirExists(ctx context.Context, path string) (bool, error)
}

type dirExistsFunc func(ctx context.Context, path string) (bool, error)

func (f dirExistsFunc) dirExists(ctx context.Context, path string) (bool, error) {
	return f(ctx, path)
}

type callerDirExistsFS struct {
	bk *buildkit.Client
}

func (fs callerDirExistsFS) dirExists(ctx context.Context, path string) (bool, error) {
	stat, err := fs.bk.StatCallerHostPath(ctx, path, false)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, err
	}
	return stat.IsDir(), nil
}

type coreDirExistsFS struct {
	dir *core.Directory
	bk  *buildkit.Client
}

func (fs coreDirExistsFS) dirExists(ctx context.Context, path string) (bool, error) {
	stat, err := fs.dir.Stat(ctx, fs.bk, nil, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return stat.IsDir(), nil
}

type moduleSourceDirExistsFS struct {
	bk  *buildkit.Client
	src *core.ModuleSource
}

// path is assumed to be relative to source root
func (fs moduleSourceDirExistsFS) dirExists(ctx context.Context, path string) (bool, error) {
	if fs.src == nil {
		return false, nil
	}

	switch fs.src.Kind {
	case core.ModuleSourceKindLocal:
		path = filepath.Join(fs.src.Local.ContextDirectoryPath, fs.src.SourceRootSubpath, path)
		return callerDirExistsFS{fs.bk}.dirExists(ctx, path)
	case core.ModuleSourceKindGit:
		path = filepath.Join("/", fs.src.SourceRootSubpath, path)
		return coreDirExistsFS{
			dir: fs.src.Git.UnfilteredContextDir.Self,
			bk:  fs.bk,
		}.dirExists(ctx, path)
	case core.ModuleSourceKindDir:
		path = filepath.Join("/", fs.src.SourceRootSubpath, path)
		return coreDirExistsFS{
			dir: fs.src.ContextDirectory.Self,
			bk:  fs.bk,
		}.dirExists(ctx, path)
	default:
		return false, fmt.Errorf("unsupported module source kind: %s", fs.src.Kind)
	}
}
