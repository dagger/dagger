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

	"github.com/dagger/dagger/engine/slog"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/server/resource"
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

		dagql.NodeFunc("withUpdateBlueprint", s.moduleSourceWithUpdateBlueprint).
			Doc(`Update the blueprint module to the latest version.`),

		dagql.Func("withoutBlueprint", s.moduleSourceWithoutBlueprint).
			Doc(`Remove the current blueprint from the module source.`),

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
		defaultFindUpSourceRootDir, defaultFindUpExists, err := findUp(ctx, core.NewCallerStatFS(bk), cwd, modules.Filename)
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
	foundPaths, err := findUpAll(ctx, core.NewCallerStatFS(bk), localAbsPath, map[string]struct{}{
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
					{Name: "directory", Value: dagql.NewID[*core.Directory](srcRootDir.ID())},
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

		// load this module source's context directory, sdk and deps in parallel
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

	localSrc.Digest = localSrc.CalcDigest().String()

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

	gitRef, modVersion, err := parsed.GetGitRefAndModVersion(ctx, dag, refPin)
	if err != nil {
		return inst, fmt.Errorf("failed to resolve git src: %w", err)
	}
	gitCommit, _, err := gitRef.Self().Resolve(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to resolve git src to commit: %w", err)
	}

	gitSrc := &core.ModuleSource{
		ConfigExists: true, // we can't load uninitialized git modules, we'll error out later if it's not there
		Kind:         core.ModuleSourceKindGit,
		Git: &core.GitModuleSource{
			HTMLRepoURL:  parsed.RepoRoot.Repo,
			RepoRootPath: parsed.RepoRoot.Root,
			Version:      modVersion,
			Commit:       gitCommit,
			Pin:          gitCommit,
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

		configDir, found, err := findUp(ctx, statFS,
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

	parsedURL, err := url.Parse(gitSrc.Git.HTMLRepoURL)
	if err != nil {
		gitSrc.Git.HTMLURL = gitSrc.Git.HTMLRepoURL + path.Join("/src", gitSrc.Git.Commit, gitSrc.SourceRootSubpath)
	} else {
		switch parsedURL.Host {
		case "github.com", "gitlab.com":
			gitSrc.Git.HTMLURL = gitSrc.Git.HTMLRepoURL + path.Join("/tree", gitSrc.Git.Commit, gitSrc.SourceRootSubpath)
		case "dev.azure.com":
			if gitSrc.SourceRootSubpath != "." {
				gitSrc.Git.HTMLURL = fmt.Sprintf("%s/commit/%s?path=/%s", gitSrc.Git.HTMLRepoURL, gitSrc.Git.Commit, gitSrc.SourceRootSubpath)
			} else {
				gitSrc.Git.HTMLURL = gitSrc.Git.HTMLRepoURL + path.Join("/commit", gitSrc.Git.Commit)
			}
		default:
			gitSrc.Git.HTMLURL = gitSrc.Git.HTMLRepoURL + path.Join("/src", gitSrc.Git.Commit, gitSrc.SourceRootSubpath)
		}
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

	gitSrc.Digest = gitSrc.CalcDigest().String()

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

	// If we have a blueprint module, load it
	pcfg := src.ConfigBlueprint
	if pcfg == nil {
		return nil
	}
	blueprint, err := core.ResolveDepToSource(ctx, bk, dag, src, pcfg.Source, pcfg.Pin, pcfg.Name)
	if err != nil {
		return fmt.Errorf("failed to resolve dep to source: %w", err)
	}
	src.Blueprint = blueprint
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

	dirSrc.Digest = dirSrc.CalcDigest().String()
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

	if modCfg.SDK != nil {
		src.SDK = &core.SDKConfig{
			Source: modCfg.SDK.Source,
			Config: modCfg.SDK.Config,
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

	fullIncludePaths = append(fullIncludePaths, src.RebasedIncludePaths...)

	switch src.Kind {
	case core.ModuleSourceKindLocal:
		err := dag.Select(ctx, dag.Root(), &src.ContextDirectory,
			dagql.Selector{Field: "host"},
			dagql.Selector{
				Field: "directory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(src.Local.ContextDirectoryPath)},
					{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(fullIncludePaths...))},
				},
			},
		)
		if err != nil {
			return err
		}

	case core.ModuleSourceKindGit:
		err := dag.Select(ctx, dag.Root(), &src.ContextDirectory,
			dagql.Selector{Field: "directory"},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "directory", Value: dagql.NewID[*core.Directory](src.Git.UnfilteredContextDir.ID())},
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

	src.Digest = src.CalcDigest().String()
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

	src.Digest = src.CalcDigest().String()
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

	src.Digest = src.CalcDigest().String()
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

		src.Digest = src.CalcDigest().String()
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

	src.Digest = src.CalcDigest().String()
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

	src.Digest = src.CalcDigest().String()
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

	// do some sanity checks on the provided deps
	var allDeps []dagql.ObjectResult[*core.ModuleSource]
	for _, newDep := range newDeps {
		switch parentSrc.Kind {
		case core.ModuleSourceKindLocal:
			switch newDep.Self().Kind {
			case core.ModuleSourceKindLocal:
				// parent=local, dep=local

				// local deps must be located in the same context as the parent, this enforces they are in the same local
				// git repo checkout and a local dep doesn't exist in a different git repo (which is what git deps are for)
				contextRelPath, err := pathutil.LexicalRelativePath(
					parentSrc.Local.ContextDirectoryPath,
					newDep.Self().Local.ContextDirectoryPath,
				)
				if err != nil {
					return nil, fmt.Errorf("failed to get relative path from parent context to dep context: %w", err)
				}
				if !filepath.IsLocal(contextRelPath) {
					return nil, fmt.Errorf("local module dependency context directory %q is not in parent context directory %q",
						newDep.Self().Local.ContextDirectoryPath, parentSrc.Local.ContextDirectoryPath)
				}
				allDeps = append(allDeps, newDep)

			case core.ModuleSourceKindGit:
				// parent=local, dep=git
				allDeps = append(allDeps, newDep)

			default:
				return nil, fmt.Errorf("unhandled module source kind: %s", parentSrc.Kind)
			}

		case core.ModuleSourceKindGit:
			switch newDep.Self().Kind {
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

	// append the pre-existing deps to the slice too; they need to come later so we prefer new ones over existing ones below
	allDeps = append(allDeps, parentSrc.Dependencies...)

	// deduplicate equivalent deps at differing versions, preferring the new dep over the existing one
	symbolicDeps := make(map[string]dagql.ObjectResult[*core.ModuleSource], len(allDeps))
	depNames := make(map[string]dagql.ObjectResult[*core.ModuleSource], len(allDeps))
	for _, dep := range allDeps {
		var symbolicDepStr string
		switch dep.Self().Kind {
		case core.ModuleSourceKindLocal:
			symbolicDepStr = filepath.Join(dep.Self().Local.ContextDirectoryPath, dep.Self().SourceRootSubpath)
		case core.ModuleSourceKindGit:
			symbolicDepStr = dep.Self().Git.CloneRef
			if dep.Self().SourceRootSubpath != "" {
				symbolicDepStr += "/" + strings.TrimPrefix(dep.Self().SourceRootSubpath, "/")
			}
		}

		_, isDuplicateSymbolic := symbolicDeps[symbolicDepStr]
		if isDuplicateSymbolic {
			// prefer the new dep over the existing one (new deps were added to allDeps first, so we will only hit this
			// if a new dep overrides an existing one)
			continue
		}
		symbolicDeps[symbolicDepStr] = dep

		// duplicate names are not allowed
		_, isDuplicateName := depNames[dep.Self().ModuleName]
		if isDuplicateName {
			return nil, fmt.Errorf("duplicate dependency name %q", dep.Self().ModuleName)
		}
		depNames[dep.Self().ModuleName] = dep
	}

	// get the final slice of deps, sorting by name for determinism
	finalDeps := make([]dagql.ObjectResult[*core.ModuleSource], 0, len(symbolicDeps))
	for _, dep := range symbolicDeps {
		finalDeps = append(finalDeps, dep)
	}
	sort.Slice(finalDeps, func(i, j int) bool {
		return finalDeps[i].Self().ModuleName < finalDeps[j].Self().ModuleName
	})
	parentSrc.Dependencies = finalDeps

	parentSrc.Digest = parentSrc.CalcDigest().String()
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
	parentSrc.Blueprint = tmpSrc.Dependencies[0]
	parentSrc.ConfigBlueprint = tmpConfig.Dependencies[0]

	return parentSrc, nil
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
func (s *moduleSourceSchema) moduleSourceWithUpdateDependencies(
	ctx context.Context,
	parentSrc dagql.ObjectResult[*core.ModuleSource],
	args struct {
		Dependencies []string
	},
) (inst dagql.Result[*core.ModuleSource], _ error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

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

	// loop over the existing deps, checking each one for whether they should be updated based on the args
	// this is technically O(n^2) but not expected to matter for the relatively low values of n we deal
	// with here
	var newUpdatedDepArgs []core.ModuleSourceID
	for _, existingDep := range parentSrc.Self().Dependencies {
		// if no update requests, implicitly update all deps
		if len(updateReqs) == 0 {
			if existingDep.Self().Kind == core.ModuleSourceKindLocal {
				// local dep, skip update
				continue
			}

			var updatedDep dagql.ObjectResult[*core.ModuleSource]
			err := dag.Select(ctx, dag.Root(), &updatedDep,
				dagql.Selector{
					Field: "moduleSource",
					Args: []dagql.NamedInput{
						{Name: "refString", Value: dagql.String(existingDep.Self().AsString())},
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
		if existingDep.Self().Kind == core.ModuleSourceKindLocal {
			for updateReq := range updateReqs {
				if updateReq.symbolic == existingDep.Self().ModuleName {
					return inst, fmt.Errorf("updating local deps is not supported")
				}

				var contextRoot string
				switch parentSrc.Self().Kind {
				case core.ModuleSourceKindLocal:
					contextRoot = parentSrc.Self().Local.ContextDirectoryPath
				case core.ModuleSourceKindGit:
					contextRoot = "/"
				default:
					return inst, fmt.Errorf("unknown module source kind: %s", parentSrc.Self().Kind)
				}

				parentSrcRoot := filepath.Join(contextRoot, parentSrc.Self().SourceRootSubpath)
				depSrcRoot := filepath.Join(contextRoot, existingDep.Self().SourceRootSubpath)
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

		existingName := existingDep.Self().ModuleName
		existingVersion := existingDep.Self().Git.Version
		existingSymbolic := existingDep.Self().Git.CloneRef
		if depSrcRoot := existingDep.Self().SourceRootSubpath; depSrcRoot != "" {
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

			var updatedDep dagql.ObjectResult[*core.ModuleSource]
			err := dag.Select(ctx, dag.Root(), &updatedDep,
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

	err = dag.Select(ctx, parentSrc, &inst,
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

func (s *moduleSourceSchema) moduleSourceWithoutDependencies(
	ctx context.Context,
	parentSrc *core.ModuleSource,
	args struct {
		Dependencies []string
	},
) (*core.ModuleSource, error) {
	parentSrc = parentSrc.Clone()

	var filteredDeps []dagql.ObjectResult[*core.ModuleSource]
	// loop over the existing deps, checking each one for whether they should be removed based on the args
	// this is technically O(n^2) but not expected to matter for the relatively low values of n we deal with
	for _, existingDep := range parentSrc.Dependencies {
		existingName := existingDep.Self().ModuleName
		var existingSymbolic, existingVersion string

		switch existingDep.Self().Kind {
		case core.ModuleSourceKindLocal:
			if parentSrc.Kind != core.ModuleSourceKindLocal {
				return nil, fmt.Errorf("cannot remove local module source dependency from non-local module source kind %s", parentSrc.Kind)
			}
			parentSrcRoot := filepath.Join(parentSrc.Local.ContextDirectoryPath, parentSrc.SourceRootSubpath)
			depSrcRoot := filepath.Join(parentSrc.Local.ContextDirectoryPath, existingDep.Self().SourceRootSubpath)
			var err error
			existingSymbolic, err = pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
			if err != nil {
				return nil, fmt.Errorf("failed to get relative path: %w", err)
			}

		case core.ModuleSourceKindGit:
			existingSymbolic = existingDep.Self().Git.CloneRef
			if existingDep.Self().SourceRootSubpath != "" {
				existingSymbolic += "/" + strings.TrimPrefix(existingDep.Self().SourceRootSubpath, "/")
			}
			existingVersion = existingDep.Self().Git.Version

		default:
			return nil, fmt.Errorf("unhandled module source dep kind: %s", parentSrc.Kind)
		}

		keep := true // assume we keep it until we find a match
		for _, depArg := range args.Dependencies {
			depSymbolic, depVersion, _ := strings.Cut(depArg, "@")

			// dagger.json doesn't prefix relative paths with ./, so strip that and similar here
			depSymbolic = filepath.Clean(depSymbolic)

			if depSymbolic != existingName && depSymbolic != existingSymbolic {
				// not a match
				continue
			}
			keep = false

			if depVersion == "" {
				break
			}

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

			parsedDepGitRef, err := core.ParseGitRefString(ctx, depArg)
			if err != nil {
				return nil, fmt.Errorf("failed to parse git ref string %q: %w", depArg, err)
			}

			_, err = matchVersion([]string{existingVersion}, depVersion, parsedDepGitRef.RepoRootSubdir)
			if err != nil {
				// if the requested version has prefix of repoRootSubDir, then send the error as it is
				// but if it does not, remove the repoRootSubDir from depVersion to avoid confusion.
				depReqModVersion := parsedDepGitRef.ModVersion
				if !strings.HasPrefix(depReqModVersion, parsedDepGitRef.RepoRootSubdir) {
					depReqModVersion, _ = strings.CutPrefix(depReqModVersion, parsedDepGitRef.RepoRootSubdir+"/")
					existingVersion, _ = strings.CutPrefix(existingVersion, existingDep.Self().SourceRootSubpath+"/")
				}
				return nil, fmt.Errorf("version %q was requested to be uninstalled but the installed version is %q", depReqModVersion, existingVersion)
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

func (s *moduleSourceSchema) loadModuleSourceConfig(
	src *core.ModuleSource,
) (*modules.ModuleConfigWithUserFields, error) {
	// construct the module config based on any config read during load and any settings changed via with* APIs
	modCfg := &modules.ModuleConfigWithUserFields{
		ModuleConfigUserFields: src.ModuleConfigUserFields,
		ModuleConfig: modules.ModuleConfig{
			Name:          src.ModuleName,
			EngineVersion: src.EngineVersion,
			Include:       src.IncludePaths,
			Codegen:       src.CodegenConfig,
			Clients:       src.ConfigClients,
		},
	}

	if src.SDK != nil {
		modCfg.SDK = &modules.SDK{
			Source: src.SDK.Source,
			Config: src.SDK.Config,
		}
	}

	// Copy blueprint configuration if present
	if src.ConfigBlueprint != nil {
		modCfg.Blueprint = src.ConfigBlueprint
	}

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
				depCfg.Pin = depSrc.Self().Git.Pin

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
				if src.Git.CloneRef == depSrc.Self().Git.CloneRef && src.Git.Pin == depSrc.Self().Git.Pin {
					parentSrcRoot := filepath.Join("/", src.SourceRootSubpath)
					depSrcRoot := filepath.Join("/", depSrc.Self().SourceRootSubpath)
					depSrcRoot, err := pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
					if err != nil {
						return nil, fmt.Errorf("failed to get relative path: %w", err)
					}
					depCfg.Source = depSrcRoot
				} else {
					depCfg.Source = depSrc.Self().AsString()
					depCfg.Pin = depSrc.Self().Git.Pin
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
				depCfg.Pin = depSrc.Self().Git.Pin

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

func (s *moduleSourceSchema) runCodegen(
	ctx context.Context,
	srcInst dagql.ObjectResult[*core.ModuleSource],
) (res dagql.ObjectResult[*core.Directory], _ error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get current dag: %w", err)
	}

	// load the deps as actual Modules
	deps, err := s.loadDependencyModules(ctx, srcInst.Self())
	if err != nil {
		return res, fmt.Errorf("failed to load dependencies as modules: %w", err)
	}

	// cache the current source instance by it's digest before passing to codegen
	// this scopes the cache key of codegen calls to an exact content hash detached
	// from irrelevant details like specific host paths, specific git repos+commits, etc.
	_, err = dag.Cache.GetOrInitializeValue(ctx, digest.Digest(srcInst.Self().Digest), srcInst)
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
		// Only if the SDK implements a specific `moduleTypeDefs` function.
		// If not, we will have circular dependency issues.
		if sdkImpl, ok := srcInst.Self().SDKImpl.AsRuntime(); ok && sdkImpl.HasModuleTypeDefs() {
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
			gitAttrsContents, err = gitAttrsFile.Contents(ctx)
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
			gitIgnoreContents, err = gitIgnoreFile.Contents(ctx)
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

	deps, err := s.loadDependencyModules(ctx, srcInst.Self())
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
					Name:  "directory",
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
		dagql.HashFrom(
			srcInstContentHashed.ID().Digest().String(),
			"modInit",
		),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary module instance: %w", err)
	}
	_, err = dag.Cache.GetOrInitializeValue(ctx, tmpModInst.ID().Digest(), tmpModInst)
	if err != nil {
		return nil, fmt.Errorf("failed to get or initialize instance: %w", err)
	}
	mod.ResultID = tmpModInst.ID()

	modName := src.Self().ModuleName

	if runtimeImpl.HasModuleTypeDefs() {
		var resultInst dagql.ObjectResult[*core.Module]
		resultInst, err = runtimeImpl.TypeDefs(ctx, mod.Deps, srcInstContentHashed)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize module: %w", err)
		}
		initialized = resultInst.Self()
	} else {
		mod.Runtime, err = runtimeImpl.Runtime(ctx, mod.Deps, srcInstContentHashed)
		if err != nil {
			return nil, fmt.Errorf("failed to get module runtime: %w", err)
		}

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
				mod.Runtime,
				core.NewFunction("", &core.TypeDef{
					Kind:     core.TypeDefKindObject,
					AsObject: dagql.NonNull(core.NewObjectTypeDef("Module", "")),
				}))
			if err != nil {
				return fmt.Errorf("failed to create module definition function for module %q: %w", modName, err)
			}
			result, err := getModDefFn.Call(ctx, &core.CallOpts{
				Cache:          true,
				SkipSelfSchema: true,
				Server:         dag,
				// Don't include the digest for the current call (which is a bunch of module source stuff, including
				// APIs that are cached per-client when local sources are involved) in the cache key of this
				// function call. That would needlessly invalidate the cache more than is needed, similar to how
				// we want to scope the codegen cache keys by the content digested source instance above.
				SkipCallDigestCacheKey: true,
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

	if runtimeImpl.HasModuleTypeDefs() {
		// append module types to the module itself so self calls are possible
		mod.Deps = mod.Deps.Append(mod)
	}

	if mod.Runtime.Self() == nil {
		// mod.Runtime is required for the module to be correctly loaded and usable.
		// So set it if it doesn't yet exist (because moduleTypeDefs does not create it)
		mod.Runtime, err = runtimeImpl.Runtime(ctx, mod.Deps, srcInstContentHashed)
		if err != nil {
			return nil, err
		}
		var runtimeRes dagql.ID[*core.Container]
		// But mod.Runtime itself is not enough, it needs to be fully resolved now. It's expected not only
		// mod.Runtime exists, but it's expected the cache has been filled.
		// If the SDK is not defining moduleTypeDefs, then a function will be called against the module and creates all
		// the required objects. Here we don't want that, so just sync it so it exists and will be available later to be
		// invoked.
		if err = dag.Select(ctx, mod.Runtime, &runtimeRes, dagql.Selector{
			Field: "sync",
		}); err != nil {
			return nil, err
		}
	}

	return mod, nil
}

func (s *moduleSourceSchema) moduleSourceAsModule(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	args struct{},
) (inst dagql.Result[*core.Module], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	if src.Self().ModuleName == "" {
		return inst, fmt.Errorf("module name must be set")
	}

	engineVersion := src.Self().EngineVersion
	if !engine.CheckVersionCompatibility(engineVersion, engine.MinimumModuleVersion) {
		return inst, fmt.Errorf("module requires dagger %s, but support for that version has been removed", engineVersion)
	}
	if !engine.CheckMaxVersionCompatibility(engineVersion, engine.BaseVersion(engine.Version)) {
		return inst, fmt.Errorf("module requires dagger %s, but you have %s", engineVersion, engine.Version)
	}

	// Handle blueprint context separation
	originalSrc := src
	blueprintSrc := src.Self().Blueprint

	if blueprintSrc.Self() != nil {
		src = blueprintSrc
	}

	sdk := src.Self().SDK
	if sdk == nil {
		sdk = &core.SDKConfig{}
	}

	// Create module with blueprint source for SDK operations
	mod := &core.Module{
		Source:        src,
		ContextSource: originalSrc,

		NameField:    src.Self().ModuleName,
		OriginalName: src.Self().ModuleOriginalName,

		SDKConfig: sdk,
	}

	// load the deps as actual Modules
	deps, err := s.loadDependencyModules(ctx, src.Self())
	if err != nil {
		return inst, fmt.Errorf("failed to load dependencies as modules: %w", err)
	}
	mod.Deps = deps

	// cache the current source instance by it's digest before passing to codegen
	// this scopes the cache key of codegen calls to an exact content hash detached
	// from irrelevant details like specific host paths, specific git repos+commits, etc.
	_, err = dag.Cache.GetOrInitializeValue(ctx, digest.Digest(src.Self().Digest), src)
	if err != nil {
		return inst, fmt.Errorf("failed to get or initialize instance: %w", err)
	}
	srcInstContentHashed := src.WithObjectDigest(digest.Digest(src.Self().Digest))
	modName := src.Self().ModuleName

	if src.Self().SDKImpl != nil {
		mod, err = s.runModuleDefInSDK(ctx, src, srcInstContentHashed, mod)
		if err != nil {
			return inst, err
		}

		mod.ResultID = dagql.CurrentID(ctx)
	} else {
		// For no SDK, provide an empty stub module definition
		typeDef := &core.ObjectTypeDef{
			Name: mod.NameField,
			// needed to trigger constructor creation in ModuleObject.Install
			OriginalName: mod.OriginalName,
		}
		mod, err = mod.WithObject(ctx, &core.TypeDef{
			Kind: core.TypeDefKindObject,
			AsObject: dagql.Nullable[*core.ObjectTypeDef]{
				Value: typeDef,
				Valid: true,
			},
		})
		if err != nil {
			return inst, fmt.Errorf("failed to get module definition for no-sdk module %q: %w", modName, err)
		}
		obj := &core.ModuleObject{
			Module:  mod,
			TypeDef: typeDef,
		}
		// obj.Install() requires ResultID to be set.
		mod.ResultID = dagql.CurrentID(ctx)
		if err := obj.Install(ctx, dag); err != nil {
			return inst, fmt.Errorf("failed to install no-sdk module %q: %w", modName, err)
		}
	}

	if blueprintSrc.Self() != nil {
		// Show the downstream module name to clients, not the blueprint name
		// NOTE: we don't change OriginalName, that's used internally at runtime
		mod.NameField = originalSrc.Self().ModuleName
	}
	inst, err = dagql.NewResultForCurrentID(ctx, mod)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance for module %q: %w", modName, err)
	}

	return inst, nil
}

// load the given module source's dependencies as modules
func (s *moduleSourceSchema) loadDependencyModules(ctx context.Context, src *core.ModuleSource) (_ *core.ModDeps, rerr error) {
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
	depMods := make([]dagql.Result[*core.Module], len(src.Dependencies))
	for i, depSrc := range src.Dependencies {
		eg.Go(func() error {
			return dag.Select(ctx, depSrc, &depMods[i],
				dagql.Selector{Field: "asModule"},
			)
		})
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
	for i, depMod := range deps.Mods {
		if coreMod, ok := depMod.(*CoreMod); ok {
			// this is needed so that a module's dependency on the core
			// uses the correct schema version
			dag := *coreMod.Dag

			dag.View = dagql.View(engine.BaseVersion(engine.NormalizeVersion(src.EngineVersion)))
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

	src.ConfigClients = append(src.ConfigClients, moduleConfigClient)

	src.Digest = src.CalcDigest().String()

	return src, nil
}

func (s *moduleSourceSchema) moduleSourceWithoutClient(
	_ context.Context,
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

	src.Digest = src.CalcDigest().String()

	return src, nil
}

// find-up a given soughtName in curDirPath and its parent directories, return the dir
// it was found in, if any
func findUp(
	ctx context.Context,
	statFS core.StatFS,
	curDirPath string,
	soughtName string,
) (string, bool, error) {
	found, err := findUpAll(ctx, statFS, curDirPath, map[string]struct{}{soughtName: {}})
	if err != nil {
		return "", false, err
	}
	p, ok := found[soughtName]
	return p, ok, nil
}

// find-up a set of soughtNames in curDirPath and its parent directories return what
// was found (name -> absolute path of dir containing it)
func findUpAll(
	ctx context.Context,
	statFS core.StatFS,
	curDirPath string,
	soughtNames map[string]struct{},
) (map[string]string, error) {
	found := make(map[string]string, len(soughtNames))
	for {
		for soughtName := range soughtNames {
			stat, err := statFS.Stat(ctx, filepath.Join(curDirPath, soughtName))
			if err == nil {
				delete(soughtNames, soughtName)
				// NOTE: important that we use stat.Path here rather than curDirPath since the stat also
				// does some normalization of paths when the client is using case-insensitive filesystems
				// and we are stat'ing caller host filesystems
				found[soughtName] = filepath.Dir(stat.Path)
				continue
			}
			if !errors.Is(err, os.ErrNotExist) {
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
