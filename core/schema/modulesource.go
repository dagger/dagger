package schema

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/hashutil"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	fsutiltypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ErrSDKRuntimeNotImplemented struct {
	SDK string
}

func (err ErrSDKRuntimeNotImplemented) Error() string {
	return fmt.Sprintf("%q SDK does not support defining and executing functions", err.SDK)
}

type ErrSDKCodegenNotImplemented struct {
	SDK string
}

func (err ErrSDKCodegenNotImplemented) Error() string {
	return fmt.Sprintf("%q SDK does not support module generation", err.SDK)
}

type ErrSDKClientGeneratorNotImplemented struct {
	SDK string
}

func (err ErrSDKClientGeneratorNotImplemented) Error() string {
	return fmt.Sprintf("%q SDK does not support client generation", err.SDK)
}

type moduleSourceSchema struct{}

var _ SchemaResolvers = &moduleSourceSchema{}

func (s *moduleSourceSchema) Install(dag *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithCacheKey("moduleSource", s.moduleSource, dagql.CachePerClient).
			Doc(`Create a new module source instance from a source ref string`).
			Args(
				dagql.Arg("refString").Doc(`The string ref representation of the module source`),
				dagql.Arg("refPin").Doc(`The pinned version of the module source`),
				dagql.Arg("disableFindUp").Doc(`If true, do not attempt to find dagger.json in a parent directory of the provided path. Only relevant for local module sources.`),
				dagql.Arg("allowNotExists").Doc(`If true, do not error out if the provided ref string is a local path and does not exist yet. Useful when initializing new modules in directories that don't exist yet.`),
				dagql.Arg("requireKind").Doc(`If set, error out if the ref string is not of the provided requireKind.`),
			),

		dagql.NodeFuncWithCacheKey("_contextDirectory", s.contextDirectory, dagql.CachePerCall).
			Doc(`Obtain a contextual directory argument for the given path, include/excludes and module.`),
		dagql.NodeFuncWithCacheKey("_contextFile", s.contextFile, dagql.CachePerCall).
			Doc(`Obtain a contextual file argument for the given path and module.`),
	}.Install(dag)

	dagql.Fields[*core.Directory]{
		dagql.NodeFunc("asModule", s.directoryAsModule).
			Doc(`Load the directory as a Dagger module source`).
			Args(
				dagql.Arg("sourceRootPath").Doc(
					`An optional subpath of the directory which contains the module's configuration file.`,
					`If not set, the module source code is loaded from the root of the directory.`),
			),
		dagql.NodeFunc("asModuleSource", s.directoryAsModuleSource).
			Doc(`Load the directory as a Dagger module source`).
			Args(
				dagql.Arg("sourceRootPath").Doc(
					`An optional subpath of the directory which contains the module's configuration file.`,
					`If not set, the module source code is loaded from the root of the directory.`),
			),
	}.Install(dag)

	dagql.Fields[*core.ModuleSource]{
		// sync is used by external dependencies like daggerverse
		Syncer[*core.ModuleSource]().
			Doc(`Forces evaluation of the module source, including any loading into the engine and associated validation.`),

		dagql.Func("sourceSubpath", s.moduleSourceSubpath).
			Doc(`The path to the directory containing the module's source code, relative to the context directory.`),

		dagql.Func("originalSubpath", s.moduleSourceOriginalSubpath).
			Doc(`The original subpath used when instantiating this module source, relative to the context directory.`),

		dagql.FuncWithCacheKey("withSourceSubpath", s.moduleSourceWithSourceSubpath, dagql.CachePerClient).
			Doc(`Update the module source with a new source subpath.`).
			Args(
				dagql.Arg("path").Doc(`The path to set as the source subpath. Must be relative to the module source's source root directory.`),
			),

		dagql.Func("withName", s.moduleSourceWithName).
			Doc(`Update the module source with a new name.`).
			Args(
				dagql.Arg("name").Doc(`The name to set.`),
			),

		dagql.FuncWithCacheKey("withIncludes", s.moduleSourceWithIncludes, dagql.CachePerClient).
			Doc(`Update the module source with additional include patterns for files+directories from its context that are required for building it`).
			Args(
				dagql.Arg("patterns").Doc(`The new additional include patterns.`),
			),

		dagql.Func("withSDK", s.moduleSourceWithSDK).
			Doc(`Update the module source with a new SDK.`).
			Args(
				dagql.Arg("source").Doc(`The SDK source to set.`),
			),

		dagql.Func("withEngineVersion", s.moduleSourceWithEngineVersion).
			Doc(`Upgrade the engine version of the module to the given value.`).
			Args(
				dagql.Arg("version").Doc(`The engine version to upgrade to.`),
			),

		dagql.Func("withDependencies", s.moduleSourceWithDependencies).
			Doc(`Append the provided dependencies to the module source's dependency list.`).
			Args(
				dagql.Arg("dependencies").Doc(`The dependencies to append.`),
			),

		dagql.NodeFunc("withUpdateDependencies", s.moduleSourceWithUpdateDependencies).
			Doc(`Update one or more module dependencies.`).
			Args(
				dagql.Arg("dependencies").Doc(`The dependencies to update.`),
			),

		dagql.Func("withoutDependencies", s.moduleSourceWithoutDependencies).
			Doc(`Remove the provided dependencies from the module source's dependency list.`).
			Args(
				dagql.Arg("dependencies").Doc(`The dependencies to remove.`),
			),

		dagql.Func("withBlueprint", s.moduleSourceWithBlueprint).
			Doc(`Set a blueprint for the module source.`).
			Args(
				dagql.Arg("blueprint").Doc(`The blueprint module to set.`),
			),

		dagql.Func("withToolchains", s.moduleSourceWithToolchains).
			Doc(`Add toolchains to the module source.`).
			Args(
				dagql.Arg("toolchains").Doc(`The toolchain modules to add.`),
			),

		dagql.NodeFunc("withUpdateToolchains", s.moduleSourceWithUpdateToolchains).
			Doc(`Update one or more toolchains.`).
			Args(
				dagql.Arg("toolchains").Doc(`The toolchains to update.`),
			),

		dagql.Func("withoutToolchains", s.moduleSourceWithoutToolchains).
			Doc(`Remove the provided toolchains from the module source.`).
			Args(
				dagql.Arg("toolchains").Doc(`The toolchains to remove.`),
			),

		dagql.NodeFunc("withUpdateBlueprint", s.moduleSourceWithUpdateBlueprint).
			Doc(`Update the blueprint module to the latest version.`),

		dagql.Func("withoutBlueprint", s.moduleSourceWithoutBlueprint).
			Doc(`Remove the current blueprint from the module source.`),

		dagql.Func("withExperimentalFeatures", s.moduleSourceWithExperimentalFeatures).
			Doc(`Enable the experimental features for the module source.`).
			Args(
				dagql.Arg("features").Doc(`The experimental features to enable.`),
			),

		dagql.Func("withoutExperimentalFeatures", s.moduleSourceWithoutExperimentalFeatures).
			Doc(`Disable experimental features for the module source.`).
			Args(
				dagql.Arg("features").Doc(`The experimental features to disable.`),
			),

		dagql.NodeFunc("generatedContextDirectory", s.moduleSourceGeneratedContextDirectory).
			Doc(`The generated files and directories made on top of the module source's context directory.`),

		dagql.Func("asString", s.moduleSourceAsString).
			Doc(`A human readable ref string representation of this module source.`),

		dagql.Func("pin", s.moduleSourcePin).
			Doc(`The pinned version of this module source.`),

		dagql.Func("localContextDirectoryPath", s.moduleSourceLocalContextDirectoryPath).
			Doc(`The full absolute path to the context directory on the caller's host filesystem that this module source is loaded from. Only valid for local module sources.`),

		dagql.NodeFunc("asModule", s.moduleSourceAsModule).
			Doc(`Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation`),

		dagql.NodeFunc("introspectionSchemaJSON", s.moduleSourceIntrospectionSchemaJSON).
			Doc(`The introspection schema JSON file for this module source.`,
				`This file represents the schema visible to the module's source code, including all core types and those from the dependencies.`,
				`Note: this is in the context of a module, so some core types may be hidden.`),

		dagql.NodeFunc("directory", s.moduleSourceDirectory).
			Doc(`The directory containing the module configuration and source code (source code may be in a subdir).`).
			Args(
				dagql.Arg(`path`).Doc(`A subpath from the source directory to select.`),
			),

		dagql.Func("cloneRef", s.moduleSourceCloneRef).
			Doc(`The ref to clone the root of the git repo from. Only valid for git sources.`),

		dagql.Func("htmlURL", s.moduleSourceHTMLURL).
			Doc(`The URL to the source's git repo in a web browser. Only valid for git sources.`),

		dagql.Func("htmlRepoURL", s.moduleSourceHTMLRepoURL).
			Doc(`The URL to access the web view of the repository (e.g., GitHub, GitLab, Bitbucket).`),

		dagql.Func("version", s.moduleSourceVersion).
			Doc(`The specified version of the git repo this source points to.`),

		dagql.Func("commit", s.moduleSourceCommit).
			Doc(`The resolved commit of the git repo this source points to.`),

		dagql.Func("repoRootPath", s.moduleSourceRepoRootPath).
			Doc(`The import path corresponding to the root of the git repo this source points to. Only valid for git sources.`),

		dagql.Func("cloneURL", s.moduleSourceCloneURL).
			View(BeforeVersion("v0.13.0")).
			Doc(`The URL to clone the root of the git repo from`).
			Deprecated("Use `cloneRef` instead. `cloneRef` supports both URL-style and SCP-like SSH references"),

		dagql.Func("withClient", s.moduleSourceWithClient).
			Doc(`Update the module source with a new client to generate.`).
			Args(
				dagql.Arg("generator").Doc(`The generator to use`),
				dagql.Arg("outputDir").Doc(`The output directory for the generated client.`),
			),

		dagql.Func("withUpdatedClients", s.moduleSourceWithUpdatedClients).
			Doc(`Update one or more clients.`).
			Args(
				dagql.Arg("clients").Doc(`The clients to update`),
			),

		dagql.Func("withoutClient", s.moduleSourceWithoutClient).
			Doc(`Remove a client from the module source.`).
			Args(
				dagql.Arg("path").Doc(`The path of the client to remove.`),
			),
	}.Install(dag)

	dagql.Fields[*core.SDKConfig]{}.Install(dag)
	dagql.Fields[*modules.ModuleConfigClient]{}.Install(dag)

	dagql.Fields[*core.GeneratedCode]{
		dagql.Func("withVCSGeneratedPaths", s.generatedCodeWithVCSGeneratedPaths).
			Doc(`Set the list of paths to mark generated in version control.`),
		dagql.Func("withVCSIgnoredPaths", s.generatedCodeWithVCSIgnoredPaths).
			Doc(`Set the list of paths to ignore in version control.`),
	}.Install(dag)
}

type moduleSourceArgs struct {
	// avoiding name "ref" due to that being a reserved word in some SDKs (e.g. Rust)
	RefString      string
	RefPin         string `default:""`
	DisableFindUp  bool   `default:"false"`
	AllowNotExists bool   `default:"false"`
	RequireKind    dagql.Optional[core.ModuleSourceKind]
}

func (s *moduleSourceSchema) moduleSource(
	ctx context.Context,
	query dagql.ObjectResult[*core.Query],
	args moduleSourceArgs,
) (inst dagql.Result[*core.ModuleSource], err error) {
	bk, err := query.Self().Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	parsedRef, err := core.ParseRefString(ctx, core.NewCallerStatFS(bk), args.RefString, args.RefPin)
	if err != nil {
		return inst, err
	}

	if args.RequireKind.Valid && parsedRef.Kind != args.RequireKind.Value {
		return inst, fmt.Errorf("module source %q kind must be %q, got %q", args.RefString, args.RequireKind.Value.HumanString(), parsedRef.Kind.HumanString())
	}

	switch parsedRef.Kind {
	case core.ModuleSourceKindLocal:
		inst, err = s.localModuleSource(ctx, query, bk, parsedRef.Local.ModPath, !args.DisableFindUp, args.AllowNotExists)
		if err != nil {
			return inst, err
		}
	case core.ModuleSourceKindGit:
		inst, err = s.gitModuleSource(ctx, query, parsedRef.Git, args.RefPin, !args.DisableFindUp)
		if err != nil {
			return inst, err
		}
	default:
		return inst, fmt.Errorf("unknown module source kind: %s", parsedRef.Kind)
	}

	return inst, nil
}

//nolint:gocyclo
func (s *moduleSourceSchema) localModuleSource(
	ctx context.Context,
	query dagql.ObjectResult[*core.Query],
	bk *buildkit.Client,

	// localPath is the path the user provided to load the module, it may be relative or absolute and
	// may point to either the directory containing dagger.json or any subdirectory in the
	// filetree under the directory containing dagger.json.
	// When findUp is enabled, it can also be a name of a dependency in the default dagger.json found-up from the cwd.
	localPath string,

	// whether to search up the directory tree for a dagger.json file. additionally, when enabled if a dagger.json is found-up
	// and localPath is a named dependency in that dagger.json, the returned source will be for that dependency.
	doFindUp bool,

	// if true, tolerate the localPath not existing on the filesystem (for dagger init on directories that don't exist yet)
	allowNotExists bool,
) (inst dagql.Result[*core.ModuleSource], err error) {
	if localPath == "" {
		localPath = "."
	}

	// figure out the absolute path to the local module source
	var localAbsPath string

	// first, check if the local path exists outright
	stat, err := bk.StatCallerHostPath(ctx, localPath, true)
	switch {
	case err == nil:
		localAbsPath = stat.Path
	case status.Code(err) == codes.NotFound:
		// tolerate for now, we may not be enforcing it's existence and/or may find it as named dep in a find-up
	default:
		return inst, fmt.Errorf("failed to stat local path: %w", err)
	}

	// if localPath doesn't exist and find-up is enabled, check if it's a named dep in the default dagger.json
	if localAbsPath == "" && doFindUp {
		cwd, err := bk.AbsPath(ctx, ".")
		if err != nil {
			return inst, fmt.Errorf("failed to get cwd: %w", err)
		}
		defaultFindUpSourceRootDir, defaultFindUpExists, err := core.Host{}.FindUp(ctx, core.NewCallerStatFS(bk), cwd, modules.Filename)
		if err != nil {
			return inst, fmt.Errorf("failed to find up root: %w", err)
		}
		if defaultFindUpExists {
			configPath := filepath.Join(defaultFindUpSourceRootDir, modules.Filename)
			contents, err := bk.ReadCallerHostFile(ctx, configPath)
			if err != nil {
				return inst, fmt.Errorf("failed to read module config file: %w", err)
			}
			modCfg, err := modules.ParseModuleConfig(contents)
			if err != nil {
				return inst, fmt.Errorf("failed to parse module config: %w", err)
			}

			namedDep, ok := modCfg.DependencyByName(localPath)
			if ok {
				// found a dep in the default dagger.json with the name localPath, load it and return it
				parsedRef, err := core.ParseRefString(
					ctx,
					core.StatFSFunc(func(ctx context.Context, path string) (*fsutiltypes.Stat, error) {
						path = filepath.Join(defaultFindUpSourceRootDir, path)
						return core.NewCallerStatFS(bk).Stat(ctx, path)
					}),
					namedDep.Source,
					namedDep.Pin,
				)
				if err != nil {
					return inst, fmt.Errorf("failed to parse named dep ref string: %w", err)
				}
				switch parsedRef.Kind {
				case core.ModuleSourceKindLocal:
					depModPath := filepath.Join(defaultFindUpSourceRootDir, namedDep.Source)
					return s.localModuleSource(ctx, query, bk, depModPath, false, allowNotExists)
				case core.ModuleSourceKindGit:
					return s.gitModuleSource(ctx, query, parsedRef.Git, namedDep.Pin, false)
				}
			}
		}
	}

	switch {
	case localAbsPath != "":
		// we found it
	case allowNotExists:
		// we never found it, but we're told to tolerate that, just resolve the abs path
		localAbsPath, err = bk.AbsPath(ctx, localPath)
		if err != nil {
			return inst, fmt.Errorf("failed to get absolute path: %w", err)
		}
	default:
		return inst, fmt.Errorf("local path %q does not exist", localPath)
	}

	// We always find-up the context dir. When doFindUp is true, we also try a find-up for the source root.
	const dotGit = ".git"
	foundPaths, err := core.Host{}.FindUpAll(ctx, core.NewCallerStatFS(bk), localAbsPath, map[string]struct{}{
		modules.Filename: {}, // dagger.json, the directory containing this is the source root
		dotGit:           {}, // the context dir is the git repo root, if it exists
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
		// default the local path as the source root if nothing found-up
		sourceRootPath = localAbsPath

	default:
		// we weren't trying to find-up the source root, so we always set the source root to the local path
		daggerCfgFound = sourceRootPath == localAbsPath // config was found if-and-only-if it was in the localAbsPath dir
		sourceRootPath = localAbsPath
	}

	if !dotGitFound {
		// in all cases, if there's no .git found, default the context dir to the source root
		contextDirPath = sourceRootPath
	}

	sourceRootRelPath, err := pathutil.LexicalRelativePath(contextDirPath, sourceRootPath)
	if err != nil {
		return inst, fmt.Errorf("failed to get relative path from context to source root: %w", err)
	}
	if !filepath.IsLocal(sourceRootRelPath) {
		return inst, fmt.Errorf("source root path %q escapes context %q", sourceRootRelPath, contextDirPath)
	}

	originalRelPath, err := pathutil.LexicalRelativePath(contextDirPath, localAbsPath)
	if err != nil {
		return inst, fmt.Errorf("failed to get relative path from context to original path: %w", err)
	}

	localSrc := &core.ModuleSource{
		ConfigExists:      daggerCfgFound,
		SourceRootSubpath: sourceRootRelPath,
		OriginalSubpath:   originalRelPath,
		Kind:              core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: contextDirPath,
		},
	}

	dag, err := query.Self().Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	if !daggerCfgFound {
		// fill in an empty dir at the source root so the context dir digest incorporates that path
		var srcRootDir dagql.ObjectResult[*core.Directory]
		if err := dag.Select(ctx, dag.Root(), &srcRootDir, dagql.Selector{Field: "directory"}); err != nil {
			return inst, fmt.Errorf("failed to create empty directory for source root subpath: %w", err)
		}

		err = dag.Select(ctx, dag.Root(), &localSrc.ContextDirectory,
			dagql.Selector{Field: "directory"},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(localSrc.SourceRootSubpath)},
					{Name: "source", Value: dagql.NewID[*core.Directory](srcRootDir.ID())},
				},
			},
		)
		if err != nil {
			return inst, err
		}
	} else {
		// we found a dagger.json, load the module source using its values
		configPath := filepath.Join(sourceRootPath, modules.Filename)
		contents, err := bk.ReadCallerHostFile(ctx, configPath)
		if err != nil {
			return inst, fmt.Errorf("failed to read module config file: %w", err)
		}
		if err := s.initFromModConfig(contents, localSrc); err != nil {
			return inst, err
		}

		// load this module source's context directory, ignore patterns, sdk and deps in parallel
		var eg errgroup.Group
		eg.Go(func() error {
			if err := s.loadModuleSourceContext(ctx, localSrc); err != nil {
				return fmt.Errorf("failed to load local module source context: %w", err)
			}

			if localSrc.SDK != nil {
				localSrc.SDKImpl, err = sdk.NewLoader().SDKForModule(ctx, query.Self(), localSrc.SDK, localSrc)
				if err != nil {
					return fmt.Errorf("failed to load sdk for local module source: %w", err)
				}
			}

			return nil
		})

		// Load blueprint
		eg.Go(func() error {
			return s.loadBlueprintModule(ctx, bk, localSrc)
		})

		localSrc.Dependencies = make([]dagql.ObjectResult[*core.ModuleSource], len(localSrc.ConfigDependencies))
		for i, depCfg := range localSrc.ConfigDependencies {
			eg.Go(func() error {
				var err error
				localSrc.Dependencies[i], err = core.ResolveDepToSource(ctx, bk, dag, localSrc, depCfg.Source, depCfg.Pin, depCfg.Name)
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

	if err := localSrc.LoadUserDefaults(ctx); err != nil {
		return inst, fmt.Errorf("load user defaults: %w", err)
	}
	localSrc.Digest = localSrc.CalcDigest(ctx).String()

	return dagql.NewResultForCurrentID(ctx, localSrc)
}

func (s *moduleSourceSchema) gitModuleSource(
	ctx context.Context,
	query dagql.ObjectResult[*core.Query],
	parsed *core.ParsedGitRefString,
	refPin string,
	// whether to search up the directory tree for a dagger.json file
	doFindUp bool,
) (inst dagql.Result[*core.ModuleSource], err error) {
	dag, err := query.Self().Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	gitRef, err := parsed.GitRef(ctx, dag, refPin)
	if err != nil {
		return inst, fmt.Errorf("failed to resolve git src: %w", err)
	}

	gitSrc := &core.ModuleSource{
		ConfigExists: true, // we can't load uninitialized git modules, we'll error out later if it's not there
		Kind:         core.ModuleSourceKindGit,
		Git: &core.GitModuleSource{
			HTMLRepoURL:  parsed.RepoRoot.Repo,
			RepoRootPath: parsed.RepoRoot.Root,
			Version:      cmp.Or(gitRef.Self().Ref.ShortName(), gitRef.Self().Ref.SHA),
			Commit:       gitRef.Self().Ref.SHA,
			Ref:          gitRef.Self().Ref.Name,
			CloneRef:     parsed.SourceCloneRef,
		},
	}

	bk, err := query.Self().Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	// TODO:(sipsma) support sparse loading of git repos similar to how local dirs are loaded.
	// Related: https://github.com/dagger/dagger/issues/6292
	err = dag.Select(ctx, gitRef, &gitSrc.ContextDirectory,
		dagql.Selector{Field: "tree"},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to load git dir: %w", err)
	}
	gitSrc.Git.UnfilteredContextDir = gitSrc.ContextDirectory

	gitSrc.SourceRootSubpath = strings.TrimPrefix(parsed.RepoRootSubdir, "/")
	gitSrc.OriginalSubpath = gitSrc.SourceRootSubpath

	var configPath string
	if !doFindUp {
		configPath = filepath.Join(gitSrc.SourceRootSubpath, modules.Filename)
	} else {
		// first validate the given path exists at all, otherwise weird things like
		// `dagger -m github.com/dagger/dagger/not/a/real/dir` can succeed because
		// they find-up to a real dagger.json
		statFS := core.NewCoreDirStatFS(gitSrc.ContextDirectory.Self(), bk)
		if _, err := statFS.Stat(ctx, gitSrc.SourceRootSubpath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return inst, fmt.Errorf("path %q does not exist in git repo", gitSrc.SourceRootSubpath)
			}
			return inst, fmt.Errorf("failed to stat git module source: %w", err)
		}

		configDir, found, err := core.Host{}.FindUp(ctx, statFS,
			filepath.Join("/", gitSrc.SourceRootSubpath),
			modules.Filename,
		)
		if err != nil {
			return inst, fmt.Errorf("failed to find-up dagger.json: %w", err)
		}
		if !found {
			return inst, fmt.Errorf("git module source %q does not contain a dagger config file", gitSrc.AsString())
		}
		configPath = filepath.Join(configDir, modules.Filename)
		gitSrc.SourceRootSubpath = strings.TrimPrefix(configDir, "/")
	}
	if gitSrc.SourceRootSubpath == "" {
		gitSrc.SourceRootSubpath = "."
	}

	gitSrc.Git.Symbolic = gitSrc.Git.CloneRef
	if gitSrc.SourceRootSubpath != "." {
		gitSrc.Git.Symbolic += "/" + gitSrc.SourceRootSubpath
	}

	gitSrc.Git.HTMLURL, err = gitSrc.Git.Link(gitSrc.SourceRootSubpath, -1, -1)
	if err != nil {
		return inst, fmt.Errorf("failed to get git module source HTML URL: %w", err)
	}

	var configContents string
	err = dag.Select(ctx, gitSrc.ContextDirectory, &configContents,
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

	// load this module source's context directory and deps in parallel
	var eg errgroup.Group
	eg.Go(func() error {
		if err := s.loadModuleSourceContext(ctx, gitSrc); err != nil {
			return fmt.Errorf("failed to load git module source context: %w", err)
		}

		if gitSrc.SDK != nil {
			gitSrc.SDKImpl, err = sdk.NewLoader().SDKForModule(ctx, query.Self(), gitSrc.SDK, gitSrc)
			if err != nil {
				return fmt.Errorf("failed to load sdk for git module source: %w", err)
			}
		}

		return nil
	})

	// Load blueprint
	eg.Go(func() error {
		return s.loadBlueprintModule(ctx, bk, gitSrc)
	})

	gitSrc.Dependencies = make([]dagql.ObjectResult[*core.ModuleSource], len(gitSrc.ConfigDependencies))
	for i, depCfg := range gitSrc.ConfigDependencies {
		eg.Go(func() error {
			var err error
			gitSrc.Dependencies[i], err = core.ResolveDepToSource(ctx, bk, dag, gitSrc, depCfg.Source, depCfg.Pin, depCfg.Name)
			if err != nil {
				return fmt.Errorf("failed to resolve dep to source: %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, err
	}

	if err := gitSrc.LoadUserDefaults(ctx); err != nil {
		return inst, fmt.Errorf("load user defaults: %w", err)
	}
	gitSrc.Digest = gitSrc.CalcDigest(ctx).String()

	inst, err = dagql.NewResultForCurrentID(ctx, gitSrc)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}
	secretTransferPostCall, err := core.ResourceTransferPostCall(ctx, query.Self(), clientMetadata.ClientID, &resource.ID{
		ID: *gitSrc.ContextDirectory.ID(),
	})
	if err != nil {
		return inst, fmt.Errorf("failed to create secret transfer post call: %w", err)
	}

	return inst.ResultWithPostCall(secretTransferPostCall), nil
}

func (s *moduleSourceSchema) loadBlueprintModule(
	ctx context.Context,
	bk *buildkit.Client,
	src *core.ModuleSource) error {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to get dag server: %w", err)
	}

	// Load blueprint
	if src.ConfigBlueprint != nil {
		blueprint, err := core.ResolveDepToSource(ctx, bk, dag, src, src.ConfigBlueprint.Source, src.ConfigBlueprint.Pin, src.ConfigBlueprint.Name)
		if err != nil {
			return fmt.Errorf("failed to resolve blueprint to source: %w", err)
		}

		src.Blueprint = blueprint
	}

	// Load toolchains array
	if len(src.ConfigToolchains) > 0 {
		src.Toolchains = make([]dagql.ObjectResult[*core.ModuleSource], len(src.ConfigToolchains))
		for i, pcfg := range src.ConfigToolchains {
			toolchain, err := core.ResolveDepToSource(ctx, bk, dag, src, pcfg.Source, pcfg.Pin, pcfg.Name)
			if err != nil {
				return fmt.Errorf("failed to resolve toolchain to source: %w", err)
			}
			src.Toolchains[i] = toolchain
		}
	}

	return nil
}

type directoryAsModuleArgs struct {
	SourceRootPath string `default:"."`
}

func (s *moduleSourceSchema) directoryAsModule(
	ctx context.Context,
	contextDir dagql.ObjectResult[*core.Directory],
	args directoryAsModuleArgs,
) (inst dagql.Result[*core.Module], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	err = dag.Select(ctx, contextDir, &inst,
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

func (s *moduleSourceSchema) directoryAsModuleSource(
	ctx context.Context,
	contextDir dagql.ObjectResult[*core.Directory],
	args directoryAsModuleArgs,
) (inst dagql.Result[*core.ModuleSource], err error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	dag, err := query.Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	sourceRootSubpath := args.SourceRootPath
	if sourceRootSubpath == "" {
		sourceRootSubpath = "."
	}

	dirSrc := &core.ModuleSource{
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
	err = dag.Select(ctx, contextDir, &configContents,
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
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	var eg errgroup.Group

	if dirSrc.SDK != nil {
		eg.Go(func() error {
			if err := s.loadModuleSourceContext(ctx, dirSrc); err != nil {
				return err
			}

			var err error
			dirSrc.SDKImpl, err = sdk.NewLoader().SDKForModule(ctx, query, dirSrc.SDK, dirSrc)
			if err != nil {
				return fmt.Errorf("failed to load sdk for dir module source: %w", err)
			}

			return nil
		})
	}

	dirSrc.Dependencies = make([]dagql.ObjectResult[*core.ModuleSource], len(dirSrc.ConfigDependencies))
	for i, depCfg := range dirSrc.ConfigDependencies {
		eg.Go(func() error {
			var err error
			dirSrc.Dependencies[i], err = core.ResolveDepToSource(ctx, bk, dag, dirSrc, depCfg.Source, depCfg.Pin, depCfg.Name)
			if err != nil {
				return fmt.Errorf("failed to resolve dep to source: %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, err
	}

	inst, err = dagql.NewResultForCurrentID(ctx, dirSrc)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}

	if err := dirSrc.LoadUserDefaults(ctx); err != nil {
		return inst, fmt.Errorf("load user defaults: %w", err)
	}
	dirSrc.Digest = dirSrc.CalcDigest(ctx).String()
	return inst, nil
}

func (s *moduleSourceSchema) contextDirectory(
	ctx context.Context,
	query dagql.ObjectResult[*core.Query],
	args struct {
		Path string
		core.CopyFilter

		// the human-readable name of the module, currently just to help telemetry look nicer
		Module string

		// the content digest of the module
		Digest string
	},
) (inst dagql.ObjectResult[*core.Directory], err error) {
	// Load the module based on its content hashed key as saved in ModuleSource.asModule.
	// We can't accept an actual Module as an argument because the current caching logic
	// will result in that Module being re-loaded by clients (due to it being CachePerClient)
	// and then possibly trying to load it from the wrong context (in the case of a cached
	// result including a _contextDirectory call).
	dag, err := query.Self().Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}
	mod, err := s.getModuleFromContentDigest(ctx, dag, args.Module, args.Digest)
	if err != nil {
		return inst, err
	}

	dir, err := mod.Self().ContextSource.Value.Self().LoadContextDir(ctx, dag, args.Path, args.CopyFilter)
	if err != nil {
		return inst, fmt.Errorf("failed to load contextual directory: %w", err)
	}

	inst, err = dagql.NewObjectResultForCurrentID(ctx, dag, dir.Self())
	if err != nil {
		return inst, fmt.Errorf("failed to create directory result: %w", err)
	}
	// mix-in a constant string to avoid collisions w/ normal host dir loads, which
	// can lead function calls encountering cached results that include contextual
	// dir loads from older sessions to load from the wrong path
	// FIXME:(sipsma) this is not ideal since contextual loaded dirs will have
	// different cache keys than normally loaded host dirs. Support for multiple
	// cache keys per result should help fix this.
	dgst := hashutil.HashStrings(dir.ID().Digest().String(), "contextualDir")
	inst = inst.WithObjectDigest(dgst)
	return inst, nil
}

func (s *moduleSourceSchema) contextFile(
	ctx context.Context,
	query dagql.ObjectResult[*core.Query],
	args struct {
		Path string

		// the human-readable name of the module, currently just to help telemetry look nicer
		Module string

		// the content digest of the module
		Digest string
	},
) (inst dagql.ObjectResult[*core.File], err error) {
	dag, err := query.Self().Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}
	mod, err := s.getModuleFromContentDigest(ctx, dag, args.Module, args.Digest)
	if err != nil {
		return inst, err
	}
	f, err := mod.Self().ContextSource.Value.Self().LoadContextFile(ctx, dag, args.Path)
	if err != nil {
		return inst, fmt.Errorf("failed to load contextual directory: %w", err)
	}

	inst, err = dagql.NewObjectResultForCurrentID(ctx, dag, f.Self())
	if err != nil {
		return inst, fmt.Errorf("failed to create directory result: %w", err)
	}
	// mix-in a constant string to avoid collisions w/ normal host file loads, which
	// can lead function calls encountering cached results that include contextual
	// file loads from older sessions to load from the wrong path
	// FIXME:(sipsma) this is not ideal since contextual loaded files will have
	// different cache keys than normally loaded host files. Support for multiple
	// cache keys per result should help fix this.
	dgst := hashutil.HashStrings(f.ID().Digest().String(), "contextualFile")
	inst = inst.WithObjectDigest(dgst)
	return inst, nil
}

func (s *moduleSourceSchema) getModuleFromContentDigest(
	ctx context.Context,
	dag *dagql.Server,
	modName string,
	dgst string,
) (inst dagql.ObjectResult[*core.Module], err error) {
	// Load the module based on its content hashed key as saved in ModuleSource.asModule.
	// We can't accept an actual Module as an argument because the current caching logic
	// will result in that Module being re-loaded by clients (due to it being CachePerClient)
	// and then possibly trying to load it from the wrong context (in the case of a cached
	// result including a _contextDirectory call).
	cacheKey := cache.CacheKey[dagql.CacheKeyType]{
		CallKey: dgst,
	}
	modRes, err := dag.Cache.GetOrInitialize(ctx, cacheKey, func(ctx context.Context) (dagql.CacheValueType, error) {
		return nil, fmt.Errorf("module not found: %s", modName)
	})
	if err != nil {
		return inst, err
	}
	inst, ok := modRes.Result().(dagql.ObjectResult[*core.Module])
	if !ok {
		return inst, fmt.Errorf("cached module has unexpected type: %T", modRes.Result())
	}

	return inst, nil
}

// set values in the given src using values read from the module config file provided as bytes
func (s *moduleSourceSchema) initFromModConfig(configBytes []byte, src *core.ModuleSource) error {
	// sanity checks
	if src.SourceRootSubpath == "" {
		return fmt.Errorf("source root path must be set")
	}

	modCfg, err := modules.ParseModuleConfig(configBytes)
	if err != nil {
		return err
	}

	// blueprint is incompatible with some dagger.json fields
	if modCfg.Blueprint != nil {
		if modCfg.SDK != nil {
			return fmt.Errorf("blueprint and sdk can't both be set")
		}
		if len(modCfg.Dependencies) != 0 {
			return fmt.Errorf("blueprint and dependencies can't both be set")
		}
		if modCfg.Source != "" {
			return fmt.Errorf("blueprint and source can't both be set")
		}
	}

	src.ModuleName = modCfg.Name
	src.ModuleOriginalName = modCfg.Name
	src.IncludePaths = modCfg.Include
	src.CodegenConfig = modCfg.Codegen
	src.ModuleConfigUserFields = modCfg.ModuleConfigUserFields
	src.ConfigDependencies = modCfg.Dependencies
	src.ConfigBlueprint = modCfg.Blueprint
	src.ConfigToolchains = modCfg.Toolchains
	src.ConfigClients = modCfg.Clients

	engineVersion := modCfg.EngineVersion
	switch engineVersion {
	case "":
		// older versions of dagger might not produce an engine version -
		// so return the version that engineVersion was introduced in
		engineVersion = engine.MinimumModuleVersion
	case modules.EngineVersionLatest:
		engineVersion = engine.Version
	}
	engineVersion = engine.NormalizeVersion(engineVersion)
	src.EngineVersion = engineVersion

	canDefaultFuncCaching := engine.CheckVersionCompatibility(
		engine.BaseVersion(src.EngineVersion),
		engine.MinimumDefaultFunctionCachingModuleVersion,
	)
	switch {
	case modCfg.DisableDefaultFunctionCaching != nil:
		// explicit setting in dagger.json, use it
		src.DisableDefaultFunctionCaching = *modCfg.DisableDefaultFunctionCaching
	case canDefaultFuncCaching:
		// no explicit setting in dagger.json but module engine version supports it, enable function caching
		src.DisableDefaultFunctionCaching = false
	default:
		// no explicit setting in dagger.json and module engine version doesn't support it, disable function caching
		src.DisableDefaultFunctionCaching = true
	}

	if modCfg.SDK != nil {
		src.SDK = &core.SDKConfig{
			Source:       modCfg.SDK.Source,
			Debug:        modCfg.SDK.Debug,
			Config:       modCfg.SDK.Config,
			Experimental: modCfg.SDK.Experimental,
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

	// add the config file includes, rebasing them from being relative to the config file
	// to being relative to the context dir
	rebasedIncludes, err := rebasePatterns(modCfg.Include, src.SourceRootSubpath)
	if err != nil {
		return err
	}
	src.RebasedIncludePaths = append(src.RebasedIncludePaths, rebasedIncludes...)

	return nil
}

// load (or re-load) the context directory for the given module source
func (s *moduleSourceSchema) loadModuleSourceContext(
	ctx context.Context,
	src *core.ModuleSource,
) error {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to get dag server: %w", err)
	}

	// we load the includes specified by the user in dagger.json (if any) plus a few
	// prepended paths that are always loaded
	fullIncludePaths := []string{
		// always load the config file
		src.SourceRootSubpath + "/" + modules.Filename,
	}

	if src.SourceSubpath != "" {
		// load the source dir if set
		fullIncludePaths = append(fullIncludePaths, src.SourceSubpath+"/**/*")
	} else {
		// otherwise load the source root; this supports use cases like an sdk-less module w/ a pyproject.toml
		// that's now going to be upgraded to using the python sdk and needs pyproject.toml to be loaded
		fullIncludePaths = append(fullIncludePaths, src.SourceRootSubpath+"/**/*")
	}

	switch src.Kind {
	case core.ModuleSourceKindLocal:
		fullIncludePaths = append(fullIncludePaths, src.RebasedIncludePaths...)

		err = dag.Select(ctx, dag.Root(), &src.ContextDirectory,
			dagql.Selector{Field: "host"},
			dagql.Selector{
				Field: "directory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(src.Local.ContextDirectoryPath)},
					{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(fullIncludePaths...))},
					{Name: "gitignore", Value: dagql.NewBoolean(true)},
				},
			},
		)
		if err != nil {
			return err
		}

	case core.ModuleSourceKindGit:
		fullIncludePaths = append(fullIncludePaths, src.RebasedIncludePaths...)

		err := dag.Select(ctx, dag.Root(), &src.ContextDirectory,
			dagql.Selector{Field: "directory"},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "source", Value: dagql.NewID[*core.Directory](src.Git.UnfilteredContextDir.ID())},
					{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(fullIncludePaths...))},
				},
			},
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *moduleSourceSchema) moduleSourceSubpath(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.SourceSubpath, nil
}

func (s *moduleSourceSchema) moduleSourceOriginalSubpath(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.OriginalSubpath, nil
}

func (s *moduleSourceSchema) moduleSourceWithSourceSubpath(
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

	// reload context since the subpath impacts what we implicitly include in the load
	err = s.loadModuleSourceContext(ctx, src)
	switch {
	case err == nil:
	case codes.NotFound == status.Code(err) && src.Kind == core.ModuleSourceKindLocal:
		// corner case: dagger init can be called on a context dir that doesn't exist yet
		// (e.g. called outside a .git context and the source root dir doesn't exist because
		// it's expected to be created when exporting the generated context dir)
		// we tolerate a not found error in this case
	default:
		return nil, fmt.Errorf("failed to reload module source context: %w", err)
	}

	if err := src.LoadUserDefaults(ctx); err != nil {
		return nil, fmt.Errorf("load user defaults: %w", err)
	}
	src.Digest = src.CalcDigest(ctx).String()
	return src, nil
}

func (s *moduleSourceSchema) moduleSourceAsString(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.AsString(), nil
}

func (s *moduleSourceSchema) moduleSourcePin(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.Pin(), nil
}

func (s *moduleSourceSchema) moduleSourceWithName(
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

	// Reload user defaults with new name
	if err := src.LoadUserDefaults(ctx); err != nil {
		return nil, fmt.Errorf("load user defaults: %w", err)
	}
	src.Digest = src.CalcDigest(ctx).String()
	return src, nil
}

func (s *moduleSourceSchema) moduleSourceWithIncludes(
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
	src.RebasedIncludePaths = append(src.RebasedIncludePaths, rebasedIncludes...)

	// reload context in case includes have changed it
	err = s.loadModuleSourceContext(ctx, src)
	switch {
	case err == nil:
	case codes.NotFound == status.Code(err) && src.Kind == core.ModuleSourceKindLocal:
		// corner case: dagger init can be called on a context dir that doesn't exist yet
		// (e.g. called outside a .git context and the source root dir doesn't exist because
		// it's expected to be created when exporting the generated context dir)
		// we tolerate a not found error in this case
	default:
		return nil, fmt.Errorf("failed to reload module source context: %w", err)
	}

	src.Digest = src.CalcDigest(ctx).String()
	return src, nil
}

func (s *moduleSourceSchema) moduleSourceWithSDK(
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

		src.Digest = src.CalcDigest(ctx).String()
		return src, nil
	}

	if src.SDK == nil {
		src.SDK = &core.SDKConfig{}
	}
	src.SDK.Source = args.Source

	// reload the sdk implementation too
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	src.SDKImpl, err = sdk.NewLoader().SDKForModule(ctx, query, src.SDK, src)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk for module source: %w", err)
	}

	// New SDK means new exposed functions and types. Different .env entries might match.
	if err := src.LoadUserDefaults(ctx); err != nil {
		return nil, fmt.Errorf("load user defaults: %w", err)
	}
	src.Digest = src.CalcDigest(ctx).String()
	return src, nil
}

func (s *moduleSourceSchema) moduleSourceDirectory(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	args struct {
		Path string
	},
) (inst dagql.Result[*core.Directory], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	parentDirPath := src.Self().SourceSubpath
	if parentDirPath == "" {
		parentDirPath = src.Self().SourceRootSubpath
	}
	path := filepath.Join(parentDirPath, args.Path)

	err = dag.Select(ctx, src.Self().ContextDirectory, &inst,
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(path)},
			},
		},
	)
	return inst, err
}

func (s *moduleSourceSchema) moduleSourceCloneRef(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindGit {
		return "", fmt.Errorf("module source is not a git module: %s", src.Kind)
	}

	return src.Git.CloneRef, nil
}

func (s *moduleSourceSchema) moduleSourceCloneURL(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindGit {
		return "", fmt.Errorf("module source is not a git module: %s", src.Kind)
	}

	return src.Git.CloneRef, nil
}

func (s *moduleSourceSchema) moduleSourceHTMLURL(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindGit {
		return "", fmt.Errorf("module source is not a git module: %s", src.Kind)
	}

	return src.Git.HTMLURL, nil
}

func (s *moduleSourceSchema) moduleSourceHTMLRepoURL(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindGit {
		return "", nil
	}

	return src.Git.HTMLRepoURL, nil
}

func (s *moduleSourceSchema) moduleSourceVersion(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindGit {
		return "", nil
	}

	return src.Git.Version, nil
}

func (s *moduleSourceSchema) moduleSourceCommit(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindGit {
		return "", nil
	}

	return src.Git.Commit, nil
}

func (s *moduleSourceSchema) moduleSourceRepoRootPath(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindGit {
		return "", fmt.Errorf("module source is not a git module: %s", src.Kind)
	}

	return src.Git.RepoRootPath, nil
}

func (s *moduleSourceSchema) moduleSourceWithEngineVersion(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Version string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()

	engineVersion := args.Version
	switch engineVersion {
	case "":
		engineVersion = engine.MinimumModuleVersion
	case modules.EngineVersionLatest:
		engineVersion = engine.Version
	}
	engineVersion = engine.NormalizeVersion(engineVersion)
	src.EngineVersion = engineVersion

	src.Digest = src.CalcDigest(ctx).String()
	return src, nil
}

func (s *moduleSourceSchema) moduleSourceLocalContextDirectoryPath(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	if src.Kind != core.ModuleSourceKindLocal {
		return "", fmt.Errorf("module source is not local")
	}
	return src.Local.ContextDirectoryPath, nil
}

func (s *moduleSourceSchema) generatedCodeWithVCSGeneratedPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSGeneratedPaths(args.Paths), nil
}

func (s *moduleSourceSchema) generatedCodeWithVCSIgnoredPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSIgnoredPaths(args.Paths), nil
}

// moduleRelationTypeAccessor provides unified access to dependencies or toolchains in a ModuleSource
type moduleRelationTypeAccessor struct {
	typ core.ModuleRelationType
}

func (a moduleRelationTypeAccessor) getItems(src *core.ModuleSource) []dagql.ObjectResult[*core.ModuleSource] {
	return src.GetRelatedModules(a.typ)
}

func (a moduleRelationTypeAccessor) setItems(src *core.ModuleSource, items []dagql.ObjectResult[*core.ModuleSource]) {
	src.SetRelatedModules(a.typ, items)
}

// validateAndCollectRelatedModules validates new related modules and returns all related modules (new + existing)
func (s *moduleSourceSchema) validateAndCollectRelatedModules(
	parentSrc *core.ModuleSource,
	newRelatedModules []dagql.ObjectResult[*core.ModuleSource],
	accessor moduleRelationTypeAccessor,
) ([]dagql.ObjectResult[*core.ModuleSource], error) {
	var allRelatedModules []dagql.ObjectResult[*core.ModuleSource]

	// Validate and collect new related modules
	for _, newRelatedModule := range newRelatedModules {
		switch parentSrc.Kind {
		case core.ModuleSourceKindLocal:
			switch newRelatedModule.Self().Kind {
			case core.ModuleSourceKindLocal:
				// parent=local, item=local
				// local items must be located in the same context as the parent
				contextRelPath, err := pathutil.LexicalRelativePath(
					parentSrc.Local.ContextDirectoryPath,
					newRelatedModule.Self().Local.ContextDirectoryPath,
				)
				if err != nil {
					return nil, fmt.Errorf("failed to get relative path from parent context to %s context: %w", accessor.typ, err)
				}
				if !filepath.IsLocal(contextRelPath) {
					return nil, fmt.Errorf("local module %s context directory %q is not in parent context directory %q",
						accessor.typ, newRelatedModule.Self().Local.ContextDirectoryPath, parentSrc.Local.ContextDirectoryPath)
				}
				allRelatedModules = append(allRelatedModules, newRelatedModule)

			case core.ModuleSourceKindGit:
				// parent=local, item=git
				allRelatedModules = append(allRelatedModules, newRelatedModule)

			default:
				return nil, fmt.Errorf("unhandled module source kind: %s", newRelatedModule.Self().Kind)
			}

		case core.ModuleSourceKindGit:
			switch newRelatedModule.Self().Kind {
			case core.ModuleSourceKindLocal:
				// parent=git, item=local
				// cannot add a module source that's local to the caller as an item of a git module source
				return nil, fmt.Errorf("cannot add local module source as %s of git module source", accessor.typ)

			case core.ModuleSourceKindGit:
				// parent=git, item=git
				allRelatedModules = append(allRelatedModules, newRelatedModule)

			default:
				return nil, fmt.Errorf("unhandled module source kind: %s", newRelatedModule.Self().Kind)
			}

		default:
			return nil, fmt.Errorf("unhandled module source kind: %s", parentSrc.Kind)
		}
	}

	// Append pre-existing items; they need to come later so we prefer new ones over existing ones
	allRelatedModules = append(allRelatedModules, accessor.getItems(parentSrc)...)

	return allRelatedModules, nil
}

// deduplicateAndSortItems deduplicates items by symbolic name, validates name uniqueness, and sorts by name
func (s *moduleSourceSchema) deduplicateAndSortItems(
	items []dagql.ObjectResult[*core.ModuleSource],
	accessor moduleRelationTypeAccessor,
) ([]dagql.ObjectResult[*core.ModuleSource], error) {
	// Deduplicate equivalent items at differing versions, preferring the new item over the existing one
	symbolicItems := make(map[string]dagql.ObjectResult[*core.ModuleSource], len(items))
	itemNames := make(map[string]dagql.ObjectResult[*core.ModuleSource], len(items))

	for _, item := range items {
		var symbolicItemStr string
		switch item.Self().Kind {
		case core.ModuleSourceKindLocal:
			symbolicItemStr = filepath.Join(item.Self().Local.ContextDirectoryPath, item.Self().SourceRootSubpath)
		case core.ModuleSourceKindGit:
			symbolicItemStr = item.Self().Git.CloneRef
			if item.Self().SourceRootSubpath != "" {
				symbolicItemStr += "/" + strings.TrimPrefix(item.Self().SourceRootSubpath, "/")
			}
		}

		_, isDuplicateSymbolic := symbolicItems[symbolicItemStr]
		if isDuplicateSymbolic {
			// prefer the new item over the existing one (new items were added first, so we only hit this
			// if a new item overrides an existing one)
			continue
		}
		symbolicItems[symbolicItemStr] = item

		// duplicate names are not allowed
		_, isDuplicateName := itemNames[item.Self().ModuleName]
		if isDuplicateName {
			return nil, fmt.Errorf("duplicate %s name %q", accessor.typ, item.Self().ModuleName)
		}
		itemNames[item.Self().ModuleName] = item
	}

	// Get the final slice of items, sorting by name for determinism
	finalItems := make([]dagql.ObjectResult[*core.ModuleSource], 0, len(symbolicItems))
	for _, item := range symbolicItems {
		finalItems = append(finalItems, item)
	}
	sort.Slice(finalItems, func(i, j int) bool {
		return finalItems[i].Self().ModuleName < finalItems[j].Self().ModuleName
	})

	return finalItems, nil
}

// moduleSourceUpdateItems processes update requests for items (dependencies or toolchains)
func (s *moduleSourceSchema) moduleSourceUpdateItems(
	ctx context.Context,
	parentSrc dagql.ObjectResult[*core.ModuleSource],
	updateArgs []string,
	accessor moduleRelationTypeAccessor,
) ([]core.ModuleSourceID, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	type updateReq struct {
		symbolic string // either 1) a name of a dep or 2) the source minus any @version
		version  string // the version to update to, if any specified
	}
	updateReqs := make(map[updateReq]struct{}, len(updateArgs))
	for _, updateArg := range updateArgs {
		req := updateReq{}
		req.symbolic, req.version, _ = strings.Cut(updateArg, "@")
		updateReqs[req] = struct{}{}
	}

	// loop over the existing deps, checking each one for whether they should be updated based on the args
	// this is technically O(n^2) but not expected to matter for the relatively low values of n we deal
	// with here
	var newUpdatedArgs []core.ModuleSourceID
	for _, existingItem := range accessor.getItems(parentSrc.Self()) {
		// If no update requests, implicitly update all items
		if len(updateReqs) == 0 {
			if existingItem.Self().Kind == core.ModuleSourceKindLocal {
				// local dep, skip update
				continue
			}

			var updatedItem dagql.ObjectResult[*core.ModuleSource]
			err := dag.Select(ctx, dag.Root(), &updatedItem,
				dagql.Selector{
					Field: "moduleSource",
					Args: []dagql.NamedInput{
						{Name: "refString", Value: dagql.String(existingItem.Self().AsString())},
					},
				},
			)
			if err != nil {
				return nil, fmt.Errorf("failed to load existing %s: %w", accessor.typ, err)
			}

			newUpdatedArgs = append(newUpdatedArgs, dagql.NewID[*core.ModuleSource](updatedItem.ID()))
			continue
		}

		// if the existingDep is local and requested to be updated, return error, otherwise skip it
		if existingItem.Self().Kind == core.ModuleSourceKindLocal {
			for updateReq := range updateReqs {
				if updateReq.symbolic == existingItem.Self().ModuleName {
					return nil, fmt.Errorf("updating local %s is not supported", accessor.typ.Plural())
				}

				var contextRoot string
				switch parentSrc.Self().Kind {
				case core.ModuleSourceKindLocal:
					contextRoot = parentSrc.Self().Local.ContextDirectoryPath
				case core.ModuleSourceKindGit:
					contextRoot = "/"
				default:
					return nil, fmt.Errorf("unknown module source kind: %s", parentSrc.Self().Kind)
				}

				parentSrcRoot := filepath.Join(contextRoot, parentSrc.Self().SourceRootSubpath)
				itemSrcRoot := filepath.Join(contextRoot, existingItem.Self().SourceRootSubpath)
				existingSymbolic, err := pathutil.LexicalRelativePath(parentSrcRoot, itemSrcRoot)
				if err != nil {
					return nil, fmt.Errorf("failed to get relative path: %w", err)
				}

				if updateReq.symbolic == existingSymbolic {
					return nil, fmt.Errorf("updating local %s is not supported", accessor.typ.Plural())
				}
			}
			continue
		}

		existingName := existingItem.Self().ModuleName
		existingVersion := existingItem.Self().Git.Version
		existingSymbolic := existingItem.Self().Git.CloneRef
		if itemSrcRoot := existingItem.Self().SourceRootSubpath; itemSrcRoot != "" {
			existingSymbolic += "/" + strings.TrimPrefix(itemSrcRoot, "/")
		}

		for updateReq := range updateReqs {
			if updateReq.symbolic != existingName && updateReq.symbolic != existingSymbolic {
				continue
			}
			delete(updateReqs, updateReq)

			updateVersion := updateReq.version
			if updateVersion == "" {
				updateVersion = existingVersion
			}
			updateRef := existingSymbolic
			if updateVersion != "" {
				updateRef += "@" + updateVersion
			}

			var updatedItem dagql.ObjectResult[*core.ModuleSource]
			err := dag.Select(ctx, dag.Root(), &updatedItem,
				dagql.Selector{
					Field: "moduleSource",
					Args: []dagql.NamedInput{
						{Name: "refString", Value: dagql.String(updateRef)},
					},
				},
			)
			if err != nil {
				return nil, fmt.Errorf("failed to load updated %s: %w", accessor.typ, err)
			}

			newUpdatedArgs = append(newUpdatedArgs, dagql.NewID[*core.ModuleSource](updatedItem.ID()))
		}
	}

	if len(updateReqs) > 0 {
		items := make([]string, 0, len(updateReqs))
		for updateReq := range updateReqs {
			items = append(items, updateReq.symbolic)
		}
		return nil, fmt.Errorf("%s %q was requested to be updated, but it is not found in the %s list", accessor.typ, strings.Join(items, ","), accessor.typ.Plural())
	}

	return newUpdatedArgs, nil
}

// moduleSourceRemoveItems processes removal requests for items (dependencies or toolchains)
func (s *moduleSourceSchema) moduleSourceRemoveItems(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	removeArgs []string,
	accessor moduleRelationTypeAccessor,
) (*core.ModuleSource, error) {
	parentSrc = parentSrc.Clone()

	var filteredItems []dagql.ObjectResult[*core.ModuleSource]
	var filteredConfigItems []*modules.ModuleConfigDependency

	for i, existingItem := range accessor.getItems(parentSrc) {
		existingName := existingItem.Self().ModuleName
		var existingSymbolic, existingVersion string

		switch existingItem.Self().Kind {
		case core.ModuleSourceKindLocal:
			if parentSrc.Kind != core.ModuleSourceKindLocal {
				return nil, fmt.Errorf("cannot remove local %s from non-local module source kind %s", accessor.typ, parentSrc.Kind)
			}
			parentSrcRoot := filepath.Join(parentSrc.Local.ContextDirectoryPath, parentSrc.SourceRootSubpath)
			itemSrcRoot := filepath.Join(parentSrc.Local.ContextDirectoryPath, existingItem.Self().SourceRootSubpath)
			var err error
			existingSymbolic, err = pathutil.LexicalRelativePath(parentSrcRoot, itemSrcRoot)
			if err != nil {
				return nil, fmt.Errorf("failed to get relative path: %w", err)
			}

		case core.ModuleSourceKindGit:
			existingSymbolic = existingItem.Self().Git.CloneRef
			if existingItem.Self().SourceRootSubpath != "" {
				existingSymbolic += "/" + strings.TrimPrefix(existingItem.Self().SourceRootSubpath, "/")
			}
			existingVersion = existingItem.Self().Git.Version

		default:
			return nil, fmt.Errorf("unhandled %s kind: %s", accessor.typ, existingItem.Self().Kind)
		}

		keep := true
		for _, removeArg := range removeArgs {
			argSymbolic, argVersion, _ := strings.Cut(removeArg, "@")
			argSymbolic = filepath.Clean(argSymbolic)

			if argSymbolic != existingName && argSymbolic != existingSymbolic {
				continue
			}
			keep = false

			if argVersion == "" {
				break
			}

			if existingVersion == "" {
				return nil, fmt.Errorf(
					"version %q was requested to be uninstalled but the %s %q was installed without a specific version. Try re-running without specifying the version number",
					argVersion,
					accessor.typ,
					existingSymbolic,
				)
			}

			parsedGitRef, err := core.ParseGitRefString(ctx, removeArg)
			if err != nil {
				return nil, fmt.Errorf("failed to parse git ref string %q: %w", removeArg, err)
			}

			_, err = matchVersion([]string{existingVersion}, argVersion, parsedGitRef.RepoRootSubdir)
			if err != nil {
				reqModVersion := parsedGitRef.ModVersion
				if !strings.HasPrefix(reqModVersion, parsedGitRef.RepoRootSubdir) {
					reqModVersion, _ = strings.CutPrefix(reqModVersion, parsedGitRef.RepoRootSubdir+"/")
					existingVersion, _ = strings.CutPrefix(existingVersion, existingItem.Self().SourceRootSubpath+"/")
				}
				return nil, fmt.Errorf(
					"version %q was requested to be uninstalled but the %s %q was installed with %q. Try re-running without specifying the version number",
					reqModVersion,
					accessor.typ,
					existingSymbolic,
					existingVersion,
				)
			}

			break
		}

		if keep {
			filteredItems = append(filteredItems, existingItem)
			// Keep config items for toolchains
			if accessor.typ == core.ModuleRelationTypeToolchain && i < len(parentSrc.ConfigToolchains) {
				filteredConfigItems = append(filteredConfigItems, parentSrc.ConfigToolchains[i])
			}
		}
	}

	accessor.setItems(parentSrc, filteredItems)
	if accessor.typ == core.ModuleRelationTypeToolchain {
		parentSrc.ConfigToolchains = filteredConfigItems
	}
	parentSrc.Digest = parentSrc.CalcDigest(ctx).String()
	return parentSrc, nil
}

func (s *moduleSourceSchema) moduleSourceWithDependencies(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Dependencies []core.ModuleSourceID
	},
) (*core.ModuleSource, error) {
	parentSrc = parentSrc.Clone()

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	newDeps, err := collectIDObjectResults(ctx, dag, args.Dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to load module source dependencies from ids: %w", err)
	}

	accessor := moduleRelationTypeAccessor{typ: core.ModuleRelationTypeDependency}

	// Validate and collect all items (new + existing)
	allDeps, err := s.validateAndCollectRelatedModules(parentSrc, newDeps, accessor)
	if err != nil {
		return nil, err
	}

	// Deduplicate and sort
	finalDeps, err := s.deduplicateAndSortItems(allDeps, accessor)
	if err != nil {
		return nil, err
	}

	accessor.setItems(parentSrc, finalDeps)
	parentSrc.Digest = parentSrc.CalcDigest(ctx).String()
	return parentSrc, nil
}

func (s *moduleSourceSchema) moduleSourceWithBlueprint(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Blueprint core.ModuleSourceID
	},
) (*core.ModuleSource, error) {
	// Validate blueprint compatibility
	if parentSrc.SDK != nil {
		return nil, fmt.Errorf("cannot set blueprint on module that already has SDK")
	}
	if parentSrc.Dependencies.Len() > 0 {
		return nil, fmt.Errorf("cannot set blueprint on module that has dependencies")
	}
	tmpArgs := struct{ Dependencies []core.ModuleSourceID }{
		Dependencies: []core.ModuleSourceID{args.Blueprint},
	}
	tmpSrc := parentSrc.Clone()
	tmpSrc.Dependencies = nil
	tmpSrc, err := s.moduleSourceWithDependencies(ctx, parentSrc, tmpArgs)
	if err != nil {
		return nil, err
	}
	tmpConfig, err := s.loadModuleSourceConfig(tmpSrc)
	if err != nil {
		return nil, err
	}
	// The blueprint is the last dependency added
	// (dependencies are added LIFO)
	parentSrc = parentSrc.Clone()

	// Set the blueprint field (for `dagger init --blueprint`)
	parentSrc.ConfigBlueprint = tmpConfig.Dependencies[0]
	parentSrc.Blueprint = tmpSrc.Dependencies[0]

	return parentSrc, nil
}

func (s *moduleSourceSchema) moduleSourceWithToolchains(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Toolchains []core.ModuleSourceID
	},
) (*core.ModuleSource, error) {
	parentSrc = parentSrc.Clone()

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	newToolchains, err := collectIDObjectResults(ctx, dag, args.Toolchains)
	if err != nil {
		return nil, fmt.Errorf("failed to load module source toolchains from ids: %w", err)
	}

	accessor := moduleRelationTypeAccessor{typ: core.ModuleRelationTypeToolchain}

	// Validate and collect all items (new + existing)
	allToolchains, err := s.validateAndCollectRelatedModules(parentSrc, newToolchains, accessor)
	if err != nil {
		return nil, err
	}

	// Deduplicate and sort
	finalToolchains, err := s.deduplicateAndSortItems(allToolchains, accessor)
	if err != nil {
		return nil, err
	}

	accessor.setItems(parentSrc, finalToolchains)

	// Load the config for all toolchains to populate ConfigToolchains
	// We need to convert each toolchain into a config entry by loading it as a dependency
	configToolchains := make([]*modules.ModuleConfigDependency, len(finalToolchains))
	for i, toolchain := range finalToolchains {
		// Load as a dependency to get the proper config format
		tmpArgs := struct{ Dependencies []core.ModuleSourceID }{
			Dependencies: []core.ModuleSourceID{dagql.NewID[*core.ModuleSource](toolchain.ID())},
		}
		tmpSrc := parentSrc.Clone()
		tmpSrc.Dependencies = nil
		tmpSrc, err := s.moduleSourceWithDependencies(ctx, tmpSrc, tmpArgs)
		if err != nil {
			return nil, fmt.Errorf("failed to load toolchain config: %w", err)
		}
		tmpConfig, err := s.loadModuleSourceConfig(tmpSrc)
		if err != nil {
			return nil, fmt.Errorf("failed to load toolchain config: %w", err)
		}
		if len(tmpConfig.Dependencies) > 0 {
			configToolchains[i] = tmpConfig.Dependencies[0]
		}
	}
	parentSrc.ConfigToolchains = configToolchains

	parentSrc.Digest = parentSrc.CalcDigest(ctx).String()
	return parentSrc, nil
}

func (s *moduleSourceSchema) moduleSourceWithUpdateToolchains(
	ctx context.Context,
	parentSrc dagql.ObjectResult[*core.ModuleSource],
	args struct {
		Toolchains []string
	},
) (inst dagql.Result[*core.ModuleSource], _ error) {
	accessor := moduleRelationTypeAccessor{typ: core.ModuleRelationTypeToolchain}
	newUpdatedArgs, err := s.moduleSourceUpdateItems(ctx, parentSrc, args.Toolchains, accessor)
	if err != nil {
		return inst, err
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	err = dag.Select(ctx, parentSrc, &inst,
		dagql.Selector{
			Field: "withToolchains",
			Args: []dagql.NamedInput{{
				Name:  "toolchains",
				Value: dagql.ArrayInput[core.ModuleSourceID](newUpdatedArgs),
			}},
		},
	)
	return inst, err
}

func (s *moduleSourceSchema) moduleSourceWithoutToolchains(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Toolchains []string
	},
) (*core.ModuleSource, error) {
	accessor := moduleRelationTypeAccessor{typ: core.ModuleRelationTypeToolchain}
	return s.moduleSourceRemoveItems(ctx, parentSrc, args.Toolchains, accessor)
}

func (s *moduleSourceSchema) moduleSourceWithoutBlueprint(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct{},
) (*core.ModuleSource, error) {
	parentSrc = parentSrc.Clone()
	parentSrc.Blueprint = dagql.ObjectResult[*core.ModuleSource]{}
	parentSrc.ConfigBlueprint = nil
	return parentSrc, nil
}
func (s *moduleSourceSchema) moduleSourceWithUpdateBlueprint(
	ctx context.Context,
	parentSrc dagql.ObjectResult[*core.ModuleSource],
	args struct{},
) (inst dagql.Result[*core.ModuleSource], _ error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	// If no blueprint is set, return without error
	if parentSrc.Self().Blueprint.Self() == nil {
		return parentSrc.Result, nil
	}

	bpSrc := parentSrc.Self().Blueprint.Self()

	// Only update git sources
	if bpSrc.Kind != core.ModuleSourceKindGit {
		return parentSrc.Result, nil
	}

	// Update the blueprint by loading it fresh
	var bpUpdated dagql.ObjectResult[*core.ModuleSource]
	err = dag.Select(ctx, dag.Root(), &bpUpdated,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(bpSrc.AsString())},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to load updated blueprint: %w", err)
	}

	// Set the updated blueprint on the parent source
	err = dag.Select(ctx, parentSrc, &inst,
		dagql.Selector{
			Field: "withBlueprint",
			Args: []dagql.NamedInput{{
				Name:  "blueprint",
				Value: dagql.NewID[*core.ModuleSource](bpUpdated.ID()),
			}},
		},
	)
	return inst, err
}

func (s *moduleSourceSchema) moduleSourceWithExperimentalFeatures(
	_ context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Features []core.ModuleSourceExperimentalFeature
	},
) (*core.ModuleSource, error) {
	if parentSrc.SDK == nil {
		return nil, fmt.Errorf("module source has no SDK")
	}
	tmpSrc := parentSrc.Clone()
	if len(args.Features) > 0 {
		if tmpSrc.SDK.Experimental == nil {
			tmpSrc.SDK.Experimental = make(map[string]bool)
		}
		for _, feature := range args.Features {
			tmpSrc.SDK.Experimental[feature.String()] = true
		}
	}
	return tmpSrc, nil
}

func (s *moduleSourceSchema) moduleSourceWithoutExperimentalFeatures(
	_ context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Features []core.ModuleSourceExperimentalFeature
	},
) (*core.ModuleSource, error) {
	if parentSrc.SDK == nil {
		return nil, fmt.Errorf("module source has no SDK")
	}
	tmpSrc := parentSrc.Clone()
	if len(args.Features) > 0 {
		if tmpSrc.SDK.Experimental == nil {
			tmpSrc.SDK.Experimental = make(map[string]bool)
		}
		for _, feature := range args.Features {
			tmpSrc.SDK.Experimental[feature.String()] = false
		}
	} else {
		tmpSrc.SDK.Experimental = make(map[string]bool)
	}
	return tmpSrc, nil
}

func (s *moduleSourceSchema) moduleSourceWithUpdateDependencies(
	ctx context.Context,
	parentSrc dagql.ObjectResult[*core.ModuleSource],
	args struct {
		Dependencies []string
	},
) (inst dagql.Result[*core.ModuleSource], _ error) {
	accessor := moduleRelationTypeAccessor{typ: core.ModuleRelationTypeDependency}
	newUpdatedArgs, err := s.moduleSourceUpdateItems(ctx, parentSrc, args.Dependencies, accessor)
	if err != nil {
		return inst, err
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	err = dag.Select(ctx, parentSrc, &inst,
		dagql.Selector{
			Field: "withDependencies",
			Args: []dagql.NamedInput{{
				Name:  "dependencies",
				Value: dagql.ArrayInput[core.ModuleSourceID](newUpdatedArgs),
			}},
		},
	)
	return inst, err
}

func (s *moduleSourceSchema) moduleSourceWithoutDependencies(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Dependencies []string
	},
) (*core.ModuleSource, error) {
	accessor := moduleRelationTypeAccessor{typ: core.ModuleRelationTypeDependency}
	return s.moduleSourceRemoveItems(ctx, parentSrc, args.Dependencies, accessor)
}

func (s *moduleSourceSchema) loadModuleSourceConfig(
	src *core.ModuleSource,
) (*modules.ModuleConfigWithUserFields, error) {
	// construct the module config based on any config read during load and any settings changed via with* APIs
	modCfg := &modules.ModuleConfigWithUserFields{
		ModuleConfigUserFields: src.ModuleConfigUserFields,
		ModuleConfig: modules.ModuleConfig{
			Name:          src.ModuleOriginalName,
			EngineVersion: src.EngineVersion,
			Include:       src.IncludePaths,
			Codegen:       src.CodegenConfig,
			Clients:       src.ConfigClients,
		},
	}

	if src.DisableDefaultFunctionCaching {
		modCfg.DisableDefaultFunctionCaching = ptr(true)
	}

	if src.SDK != nil {
		modCfg.SDK = &modules.SDK{
			Source:       src.SDK.Source,
			Debug:        src.SDK.Debug,
			Config:       src.SDK.Config,
			Experimental: src.SDK.Experimental,
		}
	}

	// Copy blueprint and toolchains configuration
	if src.ConfigBlueprint != nil {
		modCfg.Blueprint = src.ConfigBlueprint
	}
	modCfg.Toolchains = src.ConfigToolchains

	// Check version compatibility.
	if !engine.CheckVersionCompatibility(modCfg.EngineVersion, engine.MinimumModuleVersion) {
		return nil, fmt.Errorf("module requires dagger %s, but support for that version has been removed", modCfg.EngineVersion)
	}
	if !engine.CheckMaxVersionCompatibility(modCfg.EngineVersion, engine.BaseVersion(engine.Version)) {
		return nil, fmt.Errorf("module requires dagger %s, but you have %s", modCfg.EngineVersion, engine.Version)
	}

	// Load the module config source based on sourcePath.
	switch src.SourceSubpath {
	case "":
		// leave unset
	default:
		var err error
		modCfg.Source, err = pathutil.LexicalRelativePath(
			filepath.Join("/", src.SourceRootSubpath),
			filepath.Join("/", src.SourceSubpath),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to get relative path from source root to source: %w", err)
		}
		// if source is ".", leave it unset in dagger.json as that's the default
		if modCfg.Source == "." {
			modCfg.Source = ""
		}
	}

	// Load configuration for each dependencies.
	modCfg.Dependencies = make([]*modules.ModuleConfigDependency, len(src.Dependencies))
	for i, depSrc := range src.Dependencies {
		depCfg := &modules.ModuleConfigDependency{
			Name: depSrc.Self().ModuleName,
		}
		modCfg.Dependencies[i] = depCfg

		switch src.Kind {
		case core.ModuleSourceKindLocal:
			switch depSrc.Self().Kind {
			case core.ModuleSourceKindLocal:
				// parent=local, dep=local
				parentSrcRoot := filepath.Join(src.Local.ContextDirectoryPath, src.SourceRootSubpath)
				depSrcRoot := filepath.Join(depSrc.Self().Local.ContextDirectoryPath, depSrc.Self().SourceRootSubpath)
				depSrcRoot, err := pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
				if err != nil {
					return nil, fmt.Errorf("failed to get relative path: %w", err)
				}
				depCfg.Source = depSrcRoot

			case core.ModuleSourceKindGit:
				// parent=local, dep=git
				depCfg.Source = depSrc.Self().AsString()
				depCfg.Pin = depSrc.Self().Git.Commit

			default:
				return nil, fmt.Errorf("unhandled module source kind: %s", src.Kind.HumanString())
			}

		case core.ModuleSourceKindGit:
			switch depSrc.Self().Kind {
			case core.ModuleSourceKindLocal:
				// parent=git, dep=local
				return nil, fmt.Errorf("cannot add local module source as dependency of git module source")

			case core.ModuleSourceKindGit:
				// parent=git, dep=git
				// check if the dep is the same git repo + pin as the parent, if so make it a local dep
				if src.Git.CloneRef == depSrc.Self().Git.CloneRef && src.Git.Commit == depSrc.Self().Git.Commit {
					parentSrcRoot := filepath.Join("/", src.SourceRootSubpath)
					depSrcRoot := filepath.Join("/", depSrc.Self().SourceRootSubpath)
					depSrcRoot, err := pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
					if err != nil {
						return nil, fmt.Errorf("failed to get relative path: %w", err)
					}
					depCfg.Source = depSrcRoot
				} else {
					depCfg.Source = depSrc.Self().AsString()
					depCfg.Pin = depSrc.Self().Git.Commit
				}

			default:
				return nil, fmt.Errorf("unhandled module source kind: %s", src.Kind.HumanString())
			}

		case core.ModuleSourceKindDir:
			switch depSrc.Self().Kind {
			case core.ModuleSourceKindDir:
				// parent=dir, dep=dir
				// This is a bit subtle, but we can assume that any dependencies of kind dir were sourced from the same
				// context directory as the parent. This is because module sources of type dir only load dependencies
				// from a pre-existing dagger.json; they cannot *currently* have more deps added via the withDependencies
				// API.
				parentSrcRoot := filepath.Join("/", src.SourceRootSubpath)
				depSrcRoot := filepath.Join("/", depSrc.Self().SourceRootSubpath)
				depSrcRoot, err := pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
				if err != nil {
					return nil, fmt.Errorf("failed to get relative path: %w", err)
				}
				depCfg.Source = depSrcRoot

			case core.ModuleSourceKindGit:
				// parent=dir, dep=git
				depCfg.Source = depSrc.Self().AsString()
				depCfg.Pin = depSrc.Self().Git.Commit

			default:
				// Local not supported since there's nothing we could plausibly put in the dagger.json for
				// a Dir-kind module source to depend on a Local-kind module source
				return nil, fmt.Errorf("parent module source kind %s cannot have dependency of kind %s",
					src.Kind.HumanString(),
					depSrc.Self().Kind.HumanString(),
				)
			}

		default:
			return nil, fmt.Errorf("unhandled module source kind: %s", src.Kind.HumanString())
		}
	}

	return modCfg, nil
}

func isSelfCallsEnabled(src dagql.ObjectResult[*core.ModuleSource]) bool {
	return src.Self().SDK.ExperimentalFeatureEnabled(core.ModuleSourceExperimentalFeatureSelfCalls)
}

func (s *moduleSourceSchema) runCodegen(
	ctx context.Context,
	srcInst dagql.ObjectResult[*core.ModuleSource],
) (res dagql.ObjectResult[*core.Directory], _ error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get current dag: %w", err)
	}

	// load the deps as actual Modules
	deps, err := s.loadDependencyModules(ctx, srcInst)
	if err != nil {
		return res, fmt.Errorf("failed to load dependencies as modules: %w", err)
	}

	// cache the current source instance by it's digest before passing to codegen
	// this scopes the cache key of codegen calls to an exact content hash detached
	// from irrelevant details like specific host paths, specific git repos+commits, etc.
	cacheKey := cache.CacheKey[dagql.CacheKeyType]{
		CallKey: srcInst.Self().Digest,
	}
	_, err = dag.Cache.GetOrInitializeValue(ctx, cacheKey, srcInst)
	if err != nil {
		return res, fmt.Errorf("failed to get or initialize instance: %w", err)
	}
	srcInstContentHashed := srcInst.WithObjectDigest(digest.Digest(srcInst.Self().Digest))

	generatedCodeImpl, ok := srcInst.Self().SDKImpl.AsCodeGenerator()
	if !ok {
		return res, ErrSDKCodegenNotImplemented{SDK: srcInst.Self().SDK.Source}
	}

	// If possible, add the types defined by the module itself to the "deps" so that they can be
	// part of the code generation.
	// This is not really a dependency as it's the module itself, but that will allow to generate
	// the types.
	if srcInst.Self().SDK != nil {
		// Only if the SDK implements a specific function to get module type definitions.
		// If not, we will have circular dependency issues.
		if _, ok := srcInst.Self().SDKImpl.AsModuleTypes(); ok && isSelfCallsEnabled(srcInst) {
			var mod dagql.ObjectResult[*core.Module]
			err = dag.Select(ctx, srcInst, &mod, dagql.Selector{
				Field: "asModule",
			})
			if err != nil {
				return res, fmt.Errorf("failed to transform module source into module: %w", err)
			}

			deps = mod.Self().Deps.Append(mod.Self())
		}
	}

	// run codegen to get the generated context directory
	generatedCode, err := generatedCodeImpl.Codegen(ctx, deps, srcInstContentHashed)
	if err != nil {
		return res, fmt.Errorf("failed to generate code: %w", err)
	}
	genDirInst := generatedCode.Code

	// update .gitattributes in the generated context directory
	// (linter thinks this chunk of code is too similar to the below, but not clear abstraction is worth it)
	//nolint:dupl
	if len(generatedCode.VCSGeneratedPaths) > 0 {
		gitAttrsPath := filepath.Join(srcInst.Self().SourceSubpath, ".gitattributes")
		var gitAttrsContents []byte
		gitAttrsFile, err := srcInst.Self().ContextDirectory.Self().File(ctx, gitAttrsPath)
		if err == nil {
			gitAttrsContents, err = gitAttrsFile.Contents(ctx, nil, nil)
			if err != nil {
				return res, fmt.Errorf("failed to get git attributes file contents: %w", err)
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
				fmt.Appendf(nil, "/%s linguist-generated\n", fileName)...,
			)
		}

		err = dag.Select(ctx, genDirInst, &genDirInst,
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
			return res, fmt.Errorf("failed to add vcs generated file: %w", err)
		}
	}

	// update .gitignore in the generated context directory
	writeGitignore := true // default to true if not set
	if srcInst.Self().CodegenConfig != nil && srcInst.Self().CodegenConfig.AutomaticGitignore != nil {
		writeGitignore = *srcInst.Self().CodegenConfig.AutomaticGitignore
	}
	// (linter thinks this chunk of code is too similar to the above, but not clear abstraction is worth it)
	//nolint:dupl
	if writeGitignore && len(generatedCode.VCSIgnoredPaths) > 0 {
		gitIgnorePath := filepath.Join(srcInst.Self().SourceSubpath, ".gitignore")
		var gitIgnoreContents []byte
		gitIgnoreFile, err := srcInst.Self().ContextDirectory.Self().File(ctx, gitIgnorePath)
		if err == nil {
			gitIgnoreContents, err = gitIgnoreFile.Contents(ctx, nil, nil)
			if err != nil {
				return res, fmt.Errorf("failed to get .gitignore file contents: %w", err)
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
				fmt.Appendf(nil, "/%s\n", fileName)...,
			)
		}

		err = dag.Select(ctx, genDirInst, &genDirInst,
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
			return res, fmt.Errorf("failed to add vcs ignore file: %w", err)
		}
	}

	return genDirInst, nil
}

func (s *moduleSourceSchema) runClientGenerator(
	ctx context.Context,
	srcInst dagql.ObjectResult[*core.ModuleSource],
	genDirInst dagql.ObjectResult[*core.Directory],
	clientGeneratorConfig *modules.ModuleConfigClient,
) (dagql.ObjectResult[*core.Directory], error) {
	src := srcInst.Self()

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return genDirInst, err
	}
	dag, err := query.Server.Server(ctx)
	if err != nil {
		return genDirInst, fmt.Errorf("failed to get dag server: %w", err)
	}

	sdk, err := sdk.NewLoader().SDKForModule(
		ctx,
		query,
		&core.SDKConfig{
			Source: clientGeneratorConfig.Generator,
		},
		src,
	)
	if err != nil {
		return genDirInst, fmt.Errorf("failed to load generator module %s: %w", clientGeneratorConfig.Generator, err)
	}

	clientGeneratorImpl, ok := sdk.AsClientGenerator()
	if !ok {
		return genDirInst, ErrSDKClientGeneratorNotImplemented{SDK: srcInst.Self().SDK.Source}
	}

	requiredClientGenerationFiles, err := clientGeneratorImpl.RequiredClientGenerationFiles(ctx)
	if err != nil {
		return genDirInst, fmt.Errorf("failed to get required client generation files: %w", err)
	}

	// Add extra files required to correctly generate the client if there are any.
	var source dagql.ObjectResult[*core.ModuleSource]
	err = dag.Select(ctx, srcInst, &source, dagql.Selector{
		Field: "withIncludes",
		Args: []dagql.NamedInput{
			{
				Name:  "patterns",
				Value: dagql.ArrayInput[dagql.String](requiredClientGenerationFiles),
			},
		},
	})
	if err != nil {
		return genDirInst, fmt.Errorf("failed to add module source required files: %w", err)
	}

	deps, err := s.loadDependencyModules(ctx, srcInst)
	if err != nil {
		return genDirInst, fmt.Errorf("failed to load dependencies of this modules: %w", err)
	}

	// If the current module source has sources and its SDK implements the `Runtime` interface,
	// we can transform it into a module to generate self bindings.
	if srcInst.Self().SDK != nil {
		// We must make sure to first check SDK to avoid checking a nil pointer on `SDKImpl`.
		if _, ok := srcInst.Self().SDKImpl.AsRuntime(); ok {
			var mod dagql.ObjectResult[*core.Module]
			err = dag.Select(ctx, srcInst, &mod, dagql.Selector{
				Field: "asModule",
			})
			if err != nil {
				return genDirInst, fmt.Errorf("failed to transform module source into module: %w", err)
			}

			deps = mod.Self().Deps.Append(mod.Self())
		}
	}

	generatedClientDir, err := clientGeneratorImpl.GenerateClient(
		ctx,
		source,
		deps,
		clientGeneratorConfig.Directory,
	)
	if err != nil {
		return genDirInst, fmt.Errorf("failed to generate clients: %w", err)
	}

	// Merge the generated client to the current generated instance
	err = dag.Select(ctx, genDirInst, &genDirInst,
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String("/"),
				},
				{
					Name:  "source",
					Value: dagql.NewID[*core.Directory](generatedClientDir.ID()),
				},
			},
		})
	if err != nil {
		return genDirInst, fmt.Errorf("failed to add client generated to generated directory: %w", err)
	}

	return genDirInst, nil
}

func (s *moduleSourceSchema) moduleSourceGeneratedContextDirectory(
	ctx context.Context,
	srcInst dagql.ObjectResult[*core.ModuleSource],
	args struct{},
) (res dagql.ObjectResult[*core.Directory], _ error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get dag server: %w", err)
	}

	modCfg, err := s.loadModuleSourceConfig(srcInst.Self())
	if err != nil {
		return res, fmt.Errorf("failed to load module source config: %w", err)
	}

	// run codegen too if we have a name and SDK
	genDirInst := srcInst.Self().ContextDirectory
	if modCfg.Name != "" && modCfg.SDK != nil && modCfg.SDK.Source != "" {
		updatedGenDirInst, err := s.runCodegen(ctx, srcInst)
		var missingImplErr ErrSDKCodegenNotImplemented
		if err != nil && !errors.As(err, &missingImplErr) {
			return res, fmt.Errorf("failed to run codegen: %w", err)
		}
		if err == nil {
			genDirInst = updatedGenDirInst
		}
	}

	// Generate clients
	for _, client := range modCfg.Clients {
		genDirInst, err = s.runClientGenerator(ctx, srcInst, genDirInst, client)
		if err != nil {
			return res, fmt.Errorf("failed to generate client %s: %w", client.Generator, err)
		}
	}

	// write dagger.json to the generated context directory
	modCfgBytes, err := json.MarshalIndent(modCfg, "", "  ")
	if err != nil {
		return res, fmt.Errorf("failed to encode module config: %w", err)
	}
	modCfgBytes = append(modCfgBytes, '\n')
	modCfgPath := filepath.Join(srcInst.Self().SourceRootSubpath, modules.Filename)
	err = dag.Select(ctx, genDirInst, &genDirInst,
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
		return res, fmt.Errorf("failed to add updated dagger.json to context dir: %w", err)
	}

	// return just the diff of what we generated relative to the original context directory
	err = dag.Select(ctx, srcInst.Self().ContextDirectory, &genDirInst,
		dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*core.Directory](genDirInst.ID())},
			},
		},
	)
	if err != nil {
		return res, fmt.Errorf("failed to get context dir diff: %w", err)
	}

	return genDirInst, nil
}

func (s *moduleSourceSchema) runModuleDefInSDK(ctx context.Context, src, srcInstContentHashed dagql.ObjectResult[*core.ModuleSource], mod *core.Module) (*core.Module, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	runtimeImpl, ok := src.Self().SDKImpl.AsRuntime()
	if !ok {
		return nil, ErrSDKRuntimeNotImplemented{SDK: src.Self().SDK.Source}
	}

	var initialized *core.Module

	// temporary instance ID to support CurrentModule calls made during the function, it will
	// be finalized at the end of `asModule`
	tmpModInst, err := dagql.NewResultForID(mod, dagql.CurrentID(ctx).WithDigest(
		hashutil.HashStrings(
			srcInstContentHashed.ID().Digest().String(),
			"modInit",
		),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary module instance: %w", err)
	}
	cacheKey := cache.CacheKey[dagql.CacheKeyType]{
		CallKey: string(tmpModInst.ID().Digest()),
	}
	_, err = dag.Cache.GetOrInitializeValue(ctx, cacheKey, tmpModInst)
	if err != nil {
		return nil, fmt.Errorf("failed to get or initialize instance: %w", err)
	}
	mod.ResultID = tmpModInst.ID()

	modName := src.Self().ModuleName

	typeDefsImpl, typeDefsEnabled := src.Self().SDKImpl.AsModuleTypes()
	if typeDefsEnabled {
		var resultInst dagql.ObjectResult[*core.Module]
		resultInst, err = typeDefsImpl.ModuleTypes(ctx, mod.Deps, srcInstContentHashed, mod.ResultID)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize module: %w", err)
		}
		initialized = resultInst.Self()
	} else {
		runtime, err := runtimeImpl.Runtime(ctx, mod.Deps, srcInstContentHashed)
		if err != nil {
			return nil, fmt.Errorf("failed to get module runtime: %w", err)
		}
		mod.Runtime = dagql.NonNull(runtime)

		// construct a special function with no object or function name, which tells
		// the SDK to return the module's definition (in terms of objects, fields and
		// functions)

		err = (func() (rerr error) {
			ctx, span := core.Tracer(ctx).Start(ctx, "asModule getModDef", telemetry.Internal())
			defer telemetry.End(span, func() error { return rerr })
			getModDefFn, err := core.NewModFunction(
				ctx,
				mod,
				nil,
				core.NewFunction("", &core.TypeDef{
					Kind:     core.TypeDefKindObject,
					AsObject: dagql.NonNull(core.NewObjectTypeDef("Module", "", nil)),
				}))
			if err != nil {
				return fmt.Errorf("failed to create module definition function for module %q: %w", modName, err)
			}
			result, err := getModDefFn.Call(ctx, &core.CallOpts{
				SkipSelfSchema: true,
				Server:         dag,
				// Don't use the digest for the current call (which is a bunch of module source stuff, including
				// APIs that are cached per-client when local sources are involved) in the cache key of this
				// function call. That would needlessly invalidate the cache more than is needed, similar to how
				// we want to scope the codegen cache keys by the content digested source instance above.
				OverrideStorageKey: tmpModInst.ID().Digest().String(),
			})
			if err != nil {
				return fmt.Errorf("failed to call module %q to get functions: %w", modName, err)
			}
			postCall := result.GetPostCall()
			if postCall != nil {
				if err := postCall(ctx); err != nil {
					return fmt.Errorf("failed to run post-call for module %q: %w", modName, err)
				}
			}

			resultInst, ok := result.(dagql.Result[*core.Module])
			if !ok {
				return fmt.Errorf("expected Module result, got %T", result)
			}
			initialized = resultInst.Self()
			return nil
		})()
		if err != nil {
			return nil, err
		}
	}

	// update the module's types with what was returned from the call above
	mod.Description = initialized.Description
	for _, obj := range initialized.ObjectDefs {
		slog.ExtraDebug("ObjectDefs", "mod.Name", mod.Name(), "sourceModuleName", obj.AsObject.Value.SourceModuleName, "originalName", obj.AsObject.Value.OriginalName, "name", obj.AsObject.Value.Name)
		mod, err = mod.WithObject(ctx, obj)
		if err != nil {
			return nil, fmt.Errorf("failed to add object to module %q: %w", modName, err)
		}
	}
	for _, iface := range initialized.InterfaceDefs {
		mod, err = mod.WithInterface(ctx, iface)
		if err != nil {
			return nil, fmt.Errorf("failed to add interface to module %q: %w", modName, err)
		}
	}
	for _, enum := range initialized.EnumDefs {
		mod, err = mod.WithEnum(ctx, enum)
		if err != nil {
			return nil, fmt.Errorf("failed to add enum to module %q: %w", modName, err)
		}
	}
	err = mod.Patch()
	if err != nil {
		return nil, fmt.Errorf("failed to patch module %q: %w", modName, err)
	}

	if typeDefsEnabled && isSelfCallsEnabled(srcInstContentHashed) {
		// append module types to the module itself so self calls are possible
		mod.Deps = mod.Deps.Append(mod)
	}

	return mod, nil
}

func (s *moduleSourceSchema) moduleSourceIntrospectionSchemaJSON(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	args struct{},
) (inst dagql.Result[*core.File], rerr error) {
	deps, err := s.loadDependencyModules(ctx, src)
	if err != nil {
		return inst, err
	}
	file, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, err
	}
	return file, nil
}

// toolchainContext holds information about toolchain handling for a module
type toolchainContext struct {
	originalSrc dagql.ObjectResult[*core.ModuleSource]
	src         dagql.ObjectResult[*core.ModuleSource]
}

// createBaseModule creates the initial module structure with dependencies
func (s *moduleSourceSchema) createBaseModule(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	tcCtx toolchainContext,
) (*core.Module, error) {
	sdk := src.Self().SDK
	if sdk == nil {
		sdk = &core.SDKConfig{}
	}

	mod := &core.Module{
		Source:                        dagql.NonNull(src),
		ContextSource:                 dagql.NonNull(tcCtx.originalSrc),
		NameField:                     tcCtx.originalSrc.Self().ModuleName,
		OriginalName:                  src.Self().ModuleOriginalName,
		SDKConfig:                     sdk,
		DisableDefaultFunctionCaching: src.Self().DisableDefaultFunctionCaching,
		ToolchainModules:              make(map[string]*core.Module),
		ToolchainArgumentConfigs:      make(map[string][]*modules.ModuleConfigArgument),
	}

	// Load toolchain argument configurations from the original source
	for _, tcCfg := range tcCtx.originalSrc.Self().ConfigToolchains {
		if len(tcCfg.Arguments) > 0 {
			mod.ToolchainArgumentConfigs[tcCfg.Name] = tcCfg.Arguments
		}
	}

	// Load dependencies as modules
	deps, err := s.loadDependencyModules(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("failed to load dependencies as modules: %w", err)
	}
	mod.Deps = deps

	return mod, nil
}

// initializeSDKModule initializes a module with SDK implementation
func (s *moduleSourceSchema) initializeSDKModule(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	mod *core.Module,
	dag *dagql.Server,
) (*core.Module, error) {
	// Cache the source instance by digest
	cacheKey := cache.CacheKey[dagql.CacheKeyType]{
		CallKey: src.Self().Digest,
	}
	_, err := dag.Cache.GetOrInitializeValue(ctx, cacheKey, src)
	if err != nil {
		return nil, fmt.Errorf("failed to get or initialize instance: %w", err)
	}
	srcInstContentHashed := src.WithObjectDigest(digest.Digest(src.Self().Digest))

	// Run SDK codegen
	mod, err = s.runModuleDefInSDK(ctx, src, srcInstContentHashed, mod)
	if err != nil {
		return nil, err
	}

	if _, ok := src.Self().SDKImpl.AsModuleTypes(); ok && isSelfCallsEnabled(src) {
		mod.Deps = mod.Deps.Append(mod)
	}

	mod.ResultID = dagql.CurrentID(ctx)
	return mod, nil
}

// createStubModule creates an empty module definition (no SDK, no blueprints)
func createStubModule(ctx context.Context, mod *core.Module, dag *dagql.Server) (*core.Module, error) {
	typeDef := &core.ObjectTypeDef{
		Name:         mod.NameField,
		OriginalName: mod.OriginalName,
	}

	mod, err := mod.WithObject(ctx, &core.TypeDef{
		Kind: core.TypeDefKindObject,
		AsObject: dagql.Nullable[*core.ObjectTypeDef]{
			Value: typeDef,
			Valid: true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get module definition for no-sdk module %q: %w", mod.NameField, err)
	}

	obj := &core.ModuleObject{
		Module:  mod,
		TypeDef: typeDef,
	}
	mod.ResultID = dagql.CurrentID(ctx)
	if err := obj.Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("failed to install no-sdk module %q: %w", mod.NameField, err)
	}

	return mod, nil
}

// extractToolchainModules finds all blueprint modules from dependencies
func extractToolchainModules(mod *core.Module) []*core.Module {
	var toolchainMods []*core.Module
	for _, depMod := range mod.Deps.Mods {
		if userMod, ok := depMod.(*core.Module); ok && userMod.IsToolchain {
			toolchainMods = append(toolchainMods, userMod)
		}
	}
	return toolchainMods
}

// applyArgumentConfigToFunction applies argument configuration overrides to a function
func applyArgumentConfigToFunction(fn *core.Function, argConfigs []*modules.ModuleConfigArgument, functionChain []string) *core.Function {
	fn = fn.Clone()

	// Apply configs that match the function chain (or empty for constructor)
	for _, argCfg := range argConfigs {
		// Check if this config applies to this function
		// Empty function chain in config means constructor
		if len(argCfg.Function) == 0 && len(functionChain) == 0 {
			// This is for the constructor
		} else if len(argCfg.Function) > 0 && len(functionChain) > 0 {
			// Check if function chains match
			if len(argCfg.Function) != len(functionChain) {
				continue
			}
			match := true
			for i, fnName := range argCfg.Function {
				if !strings.EqualFold(fnName, functionChain[i]) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		} else {
			// One is constructor, one is not - skip
			continue
		}

		// Find the matching argument and apply overrides
		for _, arg := range fn.Args {
			if strings.EqualFold(arg.Name, argCfg.Name) || strings.EqualFold(arg.OriginalName, argCfg.Name) {
				// Apply default value if specified
				if argCfg.Default != "" {
					arg.DefaultValue = core.JSON([]byte(fmt.Sprintf("%q", argCfg.Default)))
				}

				// Apply defaultPath if specified
				if argCfg.DefaultPath != "" {
					arg.DefaultPath = argCfg.DefaultPath
				}

				// Apply ignore patterns if specified
				if len(argCfg.Ignore) > 0 {
					arg.Ignore = argCfg.Ignore
				}
				break
			}
		}
	}

	return fn
}

// applyArgumentConfigsToObjectFunctions applies argument configurations to all functions in an object
func applyArgumentConfigsToObjectFunctions(objDef *core.TypeDef, argConfigs []*modules.ModuleConfigArgument) *core.TypeDef {
	if !objDef.AsObject.Valid {
		return objDef
	}

	objDef = objDef.Clone()
	obj := objDef.AsObject.Value

	// Apply to constructor if it exists
	if obj.Constructor.Valid {
		obj.Constructor.Value = applyArgumentConfigToFunction(obj.Constructor.Value, argConfigs, []string{})
	}

	// Apply to all regular functions
	for i, fn := range obj.Functions {
		// Function chain is just the function's original name for direct functions
		obj.Functions[i] = applyArgumentConfigToFunction(fn, argConfigs, []string{fn.OriginalName})
	}

	return objDef
}

// applyArgumentConfigsToModule applies argument configurations to all functions in a module, including chained functions
func applyArgumentConfigsToModule(mod *core.Module, argConfigs []*modules.ModuleConfigArgument) {
	// Group configs by chain length for efficiency
	directConfigs := []*modules.ModuleConfigArgument{}
	chainedConfigs := []*modules.ModuleConfigArgument{}

	for _, cfg := range argConfigs {
		if len(cfg.Function) <= 1 {
			directConfigs = append(directConfigs, cfg)
		} else {
			chainedConfigs = append(chainedConfigs, cfg)
		}
	}

	// Apply direct configs (constructor and single-level functions)
	for objIdx, objDef := range mod.ObjectDefs {
		mod.ObjectDefs[objIdx] = applyArgumentConfigsToObjectFunctions(objDef, directConfigs)
	}

	// Apply chained configs
	for _, cfg := range chainedConfigs {
		applyChainedArgumentConfig(mod, cfg)
	}
}

// applyChainedArgumentConfig applies a configuration to a function in a chain
func applyChainedArgumentConfig(mod *core.Module, cfg *modules.ModuleConfigArgument) {
	if len(cfg.Function) < 2 {
		return // Not a chain
	}

	// Find the starting object that has the first function in the chain
	for _, objDef := range mod.ObjectDefs {
		if !objDef.AsObject.Valid {
			continue
		}

		obj := objDef.AsObject.Value

		// Find the first function in the chain
		firstFn := findFunction(obj, cfg.Function[0])
		if firstFn == nil {
			continue
		}

		// Follow the chain to find the target function
		targetObj, targetFnIdx := followFunctionChain(mod, firstFn.ReturnType, cfg.Function[1:])
		if targetObj == nil {
			continue
		}

		// Apply the configuration to the target function
		targetFn := targetObj.Functions[targetFnIdx]
		updatedFn := applyArgumentConfigToFunction(targetFn, []*modules.ModuleConfigArgument{cfg}, cfg.Function)
		targetObj.Functions[targetFnIdx] = updatedFn
	}
}

// findFunction finds a function by name (case-insensitive) in an object
func findFunction(obj *core.ObjectTypeDef, name string) *core.Function {
	for _, fn := range obj.Functions {
		if strings.EqualFold(fn.OriginalName, name) {
			return fn
		}
	}
	return nil
}

// findObjectInModule finds an object in mod.ObjectDefs by OriginalName
func findObjectInModule(mod *core.Module, originalName string) *core.ObjectTypeDef {
	for _, objDef := range mod.ObjectDefs {
		if objDef.AsObject.Valid && objDef.AsObject.Value.OriginalName == originalName {
			return objDef.AsObject.Value
		}
	}
	return nil
}

// followFunctionChain traverses a chain of functions through return types
// Returns the target object and function index if the chain is valid, nil otherwise
func followFunctionChain(mod *core.Module, startType *core.TypeDef, chain []string) (*core.ObjectTypeDef, int) {
	currentType := startType

	for i, fnName := range chain {
		// Unwrap the type to get to the object
		currentType = unwrapType(currentType)
		if currentType == nil || !currentType.AsObject.Valid {
			return nil, -1
		}

		// Get the actual module object (not the cloned return type instance)
		obj := findObjectInModule(mod, currentType.AsObject.Value.OriginalName)
		if obj == nil {
			return nil, -1
		}

		// Find the next function in the chain
		fnIdx := findFunctionIndex(obj, fnName)
		if fnIdx < 0 {
			return nil, -1
		}

		// If this is the last function in the chain, we found our target
		if i == len(chain)-1 {
			return obj, fnIdx
		}

		// Otherwise, continue following the chain
		currentType = obj.Functions[fnIdx].ReturnType
	}

	return nil, -1
}

// unwrapType unwraps optional and list types to get to the underlying type
func unwrapType(t *core.TypeDef) *core.TypeDef {
	if t == nil {
		return nil
	}

	// Unwrap optional
	if t.Optional {
		unwrapped := t.Clone()
		unwrapped.Optional = false
		t = unwrapped
	}

	// Unwrap list
	if t.AsList.Valid {
		return unwrapType(t.AsList.Value.ElementTypeDef)
	}

	return t
}

// findFunctionIndex finds the index of a function by name (case-insensitive) in an object
func findFunctionIndex(obj *core.ObjectTypeDef, name string) int {
	for i, fn := range obj.Functions {
		if strings.EqualFold(fn.OriginalName, name) {
			return i
		}
	}
	return -1
}

// addToolchainFieldsToObject adds toolchain fields/functions to an existing TypeDef
func addToolchainFieldsToObject(
	objectDef *core.TypeDef,
	toolchainMods []*core.Module,
	mod *core.Module,
) (*core.TypeDef, error) {
	objectDef = objectDef.Clone()

	for _, tcMod := range toolchainMods {
		for _, obj := range tcMod.ObjectDefs {
			if obj.AsObject.Value.Name == strcase.ToCamel(tcMod.NameField) {
				// Use the original name (with hyphens) as the map key,
				// but use camelCase for the GraphQL field name
				originalName := tcMod.NameField
				fieldName := strcase.ToLowerCamel(tcMod.NameField)

				// Always add toolchains as functions (treating them as zero-argument constructors
				// if they don't have an explicit constructor). This ensures consistent behavior
				// and proper routing to the blueprint module's runtime.
				var constructor *core.Function
				if obj.AsObject.Value.Constructor.Valid {
					constructor = obj.AsObject.Value.Constructor.Value.Clone()
				} else {
					// Create a zero-argument function for toolchains without constructors
					constructor = &core.Function{
						Args: []*core.FunctionArg{},
					}
				}

				// Apply argument configuration overrides if present
				if argConfigs, ok := mod.ToolchainArgumentConfigs[originalName]; ok {
					constructor = applyArgumentConfigToFunction(constructor, argConfigs, []string{})
				}

				constructor.Name = fieldName
				constructor.OriginalName = originalName
				constructor.Description = fmt.Sprintf("toolchain '%s': %s", originalName, tcMod.Description)
				constructor.ReturnType = obj

				var err error
				objectDef, err = objectDef.WithFunction(constructor)
				if err != nil {
					return nil, fmt.Errorf("failed to add toolchain function %q: %w", fieldName, err)
				}

				mod.ToolchainModules[originalName] = tcMod
				break
			}
		}
	}

	return objectDef, nil
}

// mergeToolchainsWithSDK merges toolchain fields into SDK's main object
func mergeToolchainsWithSDK(
	mod *core.Module,
	toolchainMods []*core.Module,
) error {
	mainModuleObjectName := strcase.ToCamel(mod.NameField)

	for i, obj := range mod.ObjectDefs {
		if obj.AsObject.Valid && obj.AsObject.Value.Name == mainModuleObjectName {
			mergedObj, err := addToolchainFieldsToObject(obj, toolchainMods, mod)
			if err != nil {
				return err
			}
			mod.ObjectDefs[i] = mergedObj
			return nil
		}
	}

	return fmt.Errorf("main module object %q not found", mainModuleObjectName)
}

// createShadowModuleForToolchains creates a new module object to hold toolchain fields
func createShadowModuleForToolchains(
	ctx context.Context,
	mod *core.Module,
	toolchainMods []*core.Module,
	dag *dagql.Server,
) (*core.Module, error) {
	shadowTypeDef := &core.ObjectTypeDef{
		Name:         mod.NameField,
		OriginalName: mod.OriginalName,
	}

	shadowModule := &core.TypeDef{
		Kind: core.TypeDefKindObject,
		AsObject: dagql.Nullable[*core.ObjectTypeDef]{
			Value: shadowTypeDef,
			Valid: true,
		},
	}

	shadowModule, err := addToolchainFieldsToObject(shadowModule, toolchainMods, mod)
	if err != nil {
		return nil, err
	}

	mod, err = mod.WithObject(ctx, shadowModule)
	if err != nil {
		return nil, fmt.Errorf("failed to add toolchains to module: %w", err)
	}

	obj := &core.ModuleObject{
		Module:  mod,
		TypeDef: shadowTypeDef,
	}
	mod.ResultID = dagql.CurrentID(ctx)
	if err := obj.Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("failed to install no-sdk module with toolchains %q: %w", mod.NameField, err)
	}

	return mod, nil
}

// integrateToolchains adds toolchain modules as fields to the main module
func (s *moduleSourceSchema) integrateToolchains(
	ctx context.Context,
	mod *core.Module,
	dag *dagql.Server,
) (*core.Module, error) {
	toolchainMods := extractToolchainModules(mod)
	if len(toolchainMods) == 0 {
		return mod, nil
	}

	// Initialize toolchain modules map
	if mod.ToolchainModules == nil {
		mod.ToolchainModules = make(map[string]*core.Module)
	}

	// Check if we have an SDK module (has object definitions)
	hasSDK := len(mod.ObjectDefs) > 0

	var err error
	if hasSDK {
		// Merge toolchain fields into SDK's main object
		err = mergeToolchainsWithSDK(mod, toolchainMods)
	} else {
		// No SDK - create shadow module to hold toolchain fields
		mod, err = createShadowModuleForToolchains(ctx, mod, toolchainMods, dag)
	}

	if err != nil {
		return nil, err
	}

	// Ensure ResultID is set
	if mod.ResultID == nil {
		mod.ResultID = dagql.CurrentID(ctx)
	}

	return mod, nil
}

func (s *moduleSourceSchema) moduleSourceAsModule(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	args struct {
		// This internal-only flag allows us to force SDK modules to enable default function
		// caching even when they are on older modules, which ensures they don't see a regression
		// right after function caching is enabled. It can be removed after SDKs have been updated
		// to latest engine versions.
		ForceDefaultFunctionCaching bool `internal:"true" default:"false"`
	},
) (inst dagql.ObjectResult[*core.Module], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	if src.Self().ModuleName == "" {
		return inst, fmt.Errorf("module name must be set")
	}

	// Check engine version compatibility
	engineVersion := src.Self().EngineVersion
	if !engine.CheckVersionCompatibility(engineVersion, engine.MinimumModuleVersion) {
		return inst, fmt.Errorf("module requires dagger %s, but support for that version has been removed", engineVersion)
	}
	if !engine.CheckMaxVersionCompatibility(engineVersion, engine.BaseVersion(engine.Version)) {
		return inst, fmt.Errorf("module requires dagger %s, but you have %s", engineVersion, engine.Version)
	}

	// Set up toolchain context
	originalSrc := src
	// Blueprint mode is ONLY when we have the Blueprint field set (from `dagger init --blueprint`)
	isBlueprintMode := src.Self().Blueprint.Self() != nil

	tcCtx := toolchainContext{
		originalSrc: originalSrc,
		src:         src,
	}

	// In blueprint mode, use the blueprint as the main source
	// This must happen before creating the module so the SDK loads from blueprint source
	if isBlueprintMode {
		src = src.Self().Blueprint
		tcCtx.src = src
		// Copy toolchains from original source to blueprint source
		var sourceIDs []core.ModuleSourceID
		for _, src := range tcCtx.originalSrc.Self().Toolchains {
			sourceIDs = append(sourceIDs, dagql.NewID[*core.ModuleSource](src.ID()))
		}
		err := dag.Select(ctx, tcCtx.src, &tcCtx.src, dagql.Selector{
			Field: "withToolchains",
			Args: []dagql.NamedInput{
				{
					Name:  "toolchains",
					Value: dagql.ArrayInput[core.ModuleSourceID](sourceIDs),
				},
			},
		})
		if err != nil {
			return inst, fmt.Errorf("unable to set toolchains with blueprint: %w", err)
		}
		// Update src to use the modified blueprint with toolchains
		src = tcCtx.src
	}

	// Create base module with dependencies
	mod, err := s.createBaseModule(ctx, src, tcCtx)
	if err != nil {
		return inst, err
	}

	// Apply ForceDefaultFunctionCaching if requested
	if args.ForceDefaultFunctionCaching {
		mod.DisableDefaultFunctionCaching = false
	}

	// Initialize module based on SDK presence
	if src.Self().SDKImpl != nil {
		mod, err = s.initializeSDKModule(ctx, src, mod, dag)
		if err != nil {
			return inst, err
		}
	} else if len(originalSrc.Self().Toolchains) == 0 {
		// No SDK, no toolchains, and no blueprint - create stub module
		mod, err = createStubModule(ctx, mod, dag)
		if err != nil {
			return inst, err
		}
	}

	// Integrate toolchain modules as fields
	mod, err = s.integrateToolchains(ctx, mod, dag)
	if err != nil {
		return inst, err
	}

	inst, err = dagql.NewObjectResultForCurrentID(ctx, dag, mod)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance for module %q: %w", src.Self().ModuleName, err)
	}

	// save a result for the final module based on its content hash, currently used in the _contextDirectory API
	contentCacheKey := mod.ContentDigestCacheKey()
	contentHashedInst := inst.WithObjectDigest(digest.Digest(contentCacheKey.CallKey))
	_, err = dag.Cache.GetOrInitializeValue(ctx, contentCacheKey, contentHashedInst)
	if err != nil {
		return inst, fmt.Errorf("failed to get or initialize instance: %w", err)
	}

	return inst, nil
}

// load the given module source's dependencies as modules
func (s *moduleSourceSchema) loadDependencyModules(ctx context.Context, src dagql.ObjectResult[*core.ModuleSource]) (_ *core.ModDeps, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "load dep modules", telemetry.Internal())
	defer telemetry.End(span, func() error { return rerr })

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	dag, err := query.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	var eg errgroup.Group
	depMods := make([]dagql.Result[*core.Module], len(src.Self().Dependencies))
	for i, depSrc := range src.Self().Dependencies {
		eg.Go(func() error {
			return dag.Select(ctx, depSrc, &depMods[i],
				dagql.Selector{Field: "asModule"},
			)
		})
	}

	// Load all toolchains as dependencies
	tcMods := make([]dagql.Result[*core.Module], len(src.Self().Toolchains))
	if len(src.Self().Toolchains) > 0 {
		for i, tcSrc := range src.Self().Toolchains {
			eg.Go(func() error {
				err := dag.Select(ctx, tcSrc, &tcMods[i],
					dagql.Selector{Field: "asModule"},
				)
				return err
			})
		}
	}

	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to load module dependencies: %w", err)
	}

	defaultDeps, err := query.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default dependencies: %w", err)
	}
	deps := core.NewModDeps(query, defaultDeps.Mods)
	for _, depMod := range depMods {
		deps = deps.Append(depMod.Self())
	}
	for _, tcMod := range tcMods {
		clone := tcMod.Self().Clone()
		clone.IsToolchain = true
		clone.ContextSource = dagql.NonNull(src)

		// Apply argument configurations from the parent module's toolchain config
		// Find matching config by toolchain name
		for _, tcCfg := range src.Self().ConfigToolchains {
			if tcCfg.Name == clone.OriginalName && len(tcCfg.Arguments) > 0 {
				// Apply configurations to the toolchain module's functions, including chained functions
				applyArgumentConfigsToModule(clone, tcCfg.Arguments)
				break
			}
		}

		deps = deps.Append(clone)
	}
	for i, depMod := range deps.Mods {
		if coreMod, ok := depMod.(*CoreMod); ok {
			// this is needed so that a module's dependency on the core
			// uses the correct schema version
			dag := *coreMod.Dag

			dag.View = call.View(engine.BaseVersion(engine.NormalizeVersion(src.Self().EngineVersion)))
			deps.Mods[i] = &CoreMod{Dag: &dag}
		}
	}

	return deps, nil
}

func (s *moduleSourceSchema) moduleSourceWithClient(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Generator dagql.String
		OutputDir dagql.String
	},
) (*core.ModuleSource, error) {
	src = src.Clone()

	if src.ConfigClients == nil {
		src.ConfigClients = []*modules.ModuleConfigClient{}
	}

	moduleConfigClient := &modules.ModuleConfigClient{
		Generator: args.Generator.String(),
		Directory: args.OutputDir.String(),
	}

	for _, client := range src.ConfigClients {
		if filepath.Clean(client.Directory) == filepath.Clean(moduleConfigClient.Directory) {
			return nil, fmt.Errorf("a client is already generated in the %s directory", client.Directory)
		}
	}

	// Verify that the generator can be loaded as a module and clean
	// the generator path if it's a local path.
	if !sdk.IsModuleSDKBuiltin(moduleConfigClient.Generator) {
		dag, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get dag server: %w", err)
		}

		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get current query: %w", err)
		}

		bk, err := query.Buildkit(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get buildkit client: %w", err)
		}

		clientModule, err := core.ResolveDepToSource(ctx, bk, dag, src, moduleConfigClient.Generator, "", "")
		if err != nil {
			return nil, fmt.Errorf("failed to resolve client module source: %w", err)
		}

		if clientModule.Self().Kind == core.ModuleSourceKindLocal {
			moduleConfigClient.Generator = filepath.Clean(moduleConfigClient.Generator)
		}
	}

	src.ConfigClients = append(src.ConfigClients, moduleConfigClient)

	src.Digest = src.CalcDigest(ctx).String()

	return src, nil
}

func (s *moduleSourceSchema) moduleSourceWithUpdatedClients(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Clients []string
	},
) (*core.ModuleSource, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current query: %w", err)
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	updateReqs := make(map[string]string, len(args.Clients))
	for _, updateArg := range args.Clients {
		source, version, _ := strings.Cut(updateArg, "@")
		updateReqs[source] = version
	}

	src = src.Clone()
	newClientConfig := make([]*modules.ModuleConfigClient, len(src.ConfigClients))
	for i, client := range src.ConfigClients {
		clientGeneratorSource, _, _ := strings.Cut(client.Generator, "@")

		// If the client is a builtin SDK, the version is tied to the engine so we skip it.
		if sdk.IsModuleSDKBuiltin(client.Generator) {
			newClientConfig[i] = client.Clone()
			continue
		}

		// If there is an update request but the client is not in the input list, skip it
		if _, ok := updateReqs[clientGeneratorSource]; !ok && len(updateReqs) > 0 {
			newClientConfig[i] = client.Clone()
			continue
		}

		// At that point, we know that the client must be updated either with a given version
		// or for a global update.
		var clientModule dagql.ObjectResult[*core.ModuleSource]
		clientModule, err = core.ResolveDepToSource(ctx, bk, dag, src, client.Generator, "", "")
		if err != nil {
			return nil, fmt.Errorf("failed to resolve client module source: %w", err)
		}

		// Ignore local dependency except if it's in the list of update then we throw an error
		if clientModule.Self().Kind == core.ModuleSourceKindLocal {
			if _, ok := updateReqs[filepath.Clean(clientGeneratorSource)]; ok {
				return nil, fmt.Errorf("cannot update local client %s", clientGeneratorSource)
			}

			newClientConfig[i] = client.Clone()

			continue
		}

		// If the client generator is a git module, we fetch the latest commit and
		// reconstruct the git ref with it or use the given version.
		repo := clientModule.Self().Git.CloneRef
		var updatedVersion string

		if version, ok := updateReqs[clientGeneratorSource]; ok && version != "" {
			updatedVersion = version
		} else {
			var latestCommit dagql.String
			err = dag.Select(ctx, dag.Root(), &latestCommit,
				dagql.Selector{
					Field: "git",
					Args: []dagql.NamedInput{
						{Name: "url", Value: dagql.String(repo)},
					},
				},
				dagql.Selector{
					Field: "head",
				},
				dagql.Selector{
					Field: "commit",
				},
			)
			if err != nil {
				return nil, fmt.Errorf("failed to get git module (%s) latest commit: %w", repo, err)
			}

			updatedVersion = latestCommit.String()
		}

		newClientConfig[i] = client.Clone()
		newClientConfig[i].Generator = fmt.Sprintf("%s@%s", clientModule.Self().Git.Symbolic, updatedVersion)

		// Remove the update request from the map
		delete(updateReqs, clientGeneratorSource)
	}

	// Verify that all updateReq has been processed, otherwise there's a
	// invalid update request.
	if len(updateReqs) > 0 {
		deps := slices.Collect(maps.Keys(updateReqs))

		return nil, fmt.Errorf("client(s) %q were requested to be updated, but were not found in the clients list", strings.Join(deps, ","))
	}

	src.ConfigClients = newClientConfig

	return src, nil
}

func (s *moduleSourceSchema) moduleSourceWithoutClient(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Path string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()

	var configClients []*modules.ModuleConfigClient
	for _, client := range src.ConfigClients {
		if filepath.Clean(client.Directory) != filepath.Clean(args.Path) {
			configClients = append(configClients, client)
		}
	}

	if len(configClients) == len(src.ConfigClients) {
		return nil, fmt.Errorf("no client found at path %s", args.Path)
	}

	src.ConfigClients = configClients

	src.Digest = src.CalcDigest(ctx).String()

	return src, nil
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
