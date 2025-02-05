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
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/vcs"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	fsutiltypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type moduleSourceArgs struct {
	// avoiding name "ref" due to that being a reserved word in some SDKs (e.g. Rust)
	RefString string
	RefPin    string `default:""`
	Stable    bool   `default:"false"`
}

func (s *moduleSchema) moduleSourceCacheKey(ctx context.Context, query dagql.Instance[*core.Query], args moduleSourceArgs, origDgst digest.Digest) (digest.Digest, error) {
	if fastModuleSourceKindCheck(args) == core.ModuleSourceKindGit {
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
	parsedRef, err := parseRefString(ctx, bk, args)
	if err != nil {
		return inst, err
	}
	switch parsedRef.kind {
	case core.ModuleSourceKindLocal:
		inst, err = s.localModuleSource(ctx, query, bk, parsedRef.local.modPath, true)
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

	// whether to run findUp logic that checks if the localPath is a named module in the *default* dagger.json
	doNamedDepFindUp bool,
) (inst dagql.Instance[*core.ModuleSource], err error) {
	if doNamedDepFindUp {
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
			var modCfg modules.ModuleConfig
			if err := json.Unmarshal(contents, &modCfg); err != nil {
				return inst, fmt.Errorf("failed to decode module config: %w", err)
			}

			namedDep, ok := modCfg.DependencyByName(localPath)
			if ok {
				// TODO: relying on Pin to determine local vs. git is a bit hacky
				if namedDep.Pin != "" {
					// git ref
					// TODO:
					// TODO:
					// TODO:
					// TODO:
					// TODO:
					// TODO:
				} else {
					// local ref
					depModPath := filepath.Join(defaultFindUpSourceRootDir, namedDep.Source)
					return s.localModuleSource(ctx, query, bk, depModPath, false)
				}
			}
		}
	}

	// make localPath absolute
	if localPath == "" {
		localPath = "."
	}
	localPath, err = bk.AbsPath(ctx, localPath)
	if err != nil {
		return inst, fmt.Errorf("failed to get absolute path: %w", err)
	}

	const dotGit = ".git" // the context dir is the git repo root
	foundPaths, err := callerHostFindUpAll(ctx, bk, localPath, map[string]struct{}{
		modules.Filename: {},
		dotGit:           {},
	})
	if err != nil {
		return inst, fmt.Errorf("failed to find up source root and context: %w", err)
	}

	contextDirPath, dotGitFound := foundPaths[dotGit]
	sourceRootPath, daggerCfgFound := foundPaths[modules.Filename]
	if !daggerCfgFound {
		sourceRootPath = localPath
	}
	if !dotGitFound {
		// TODO:
		// TODO:
		// TODO:
		// TODO:
		bklog.G(ctx).Debugf("no .git found, defaulting context dir to source root %q", sourceRootPath)

		// if there's no .git found, default the context dir to the source root
		contextDirPath = sourceRootPath
	}
	sourceRootRelPath, err := pathutil.LexicalRelativePath(contextDirPath, sourceRootPath)
	if err != nil {
		return inst, fmt.Errorf("failed to get relative path from context to source root: %w", err)
	}
	if !filepath.IsLocal(sourceRootRelPath) {
		return inst, fmt.Errorf("source path %q contains parent directory components", sourceRootRelPath)
	}

	localSrc := &core.ModuleSource{
		Query:             query.Self,
		ConfigExists:      daggerCfgFound,
		SourceRootSubpath: sourceRootRelPath,
		Kind:              core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: contextDirPath,
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
					{Name: "path", Value: dagql.String(sourceRootRelPath)},
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
		modCfg := &modules.ModuleConfig{}
		if err := json.Unmarshal(contents, modCfg); err != nil {
			return inst, fmt.Errorf("failed to decode module config: %w", err)
		}

		sourceRelSubpath := filepath.Join(sourceRootRelPath, modCfg.Source)
		localSrc.SourceSubpath = sourceRelSubpath

		localSrc.ModuleName = modCfg.Name
		localSrc.ModuleOriginalName = modCfg.Name
		localSrc.EngineVersion = modCfg.EngineVersion
		localSrc.SDK = modCfg.SDK
		localSrc.IncludePaths = modCfg.Include
		localSrc.CodegenConfig = modCfg.Codegen

		// TODO: incorporate Exclude or deprecate it
		includes := []string{
			// always load the source dir
			sourceRelSubpath + "/**" + "/*",
			// always load the config file (currently mainly so it gets incorporated into the digest)
			sourceRootRelPath + "/" + modules.Filename,
		}
		// add the config file includes, rebasing them from being relative to the config file
		// to being relative to the context dir
		for _, pattern := range modCfg.Include {
			isNegation := strings.HasPrefix(pattern, "!")
			pattern = strings.TrimPrefix(pattern, "!")
			absPath := filepath.Join(sourceRootPath, pattern)
			relPath, err := pathutil.LexicalRelativePath(contextDirPath, absPath)
			if err != nil {
				return inst, fmt.Errorf("failed to get relative path from context to include: %w", err)
			}
			if !filepath.IsLocal(relPath) {
				return inst, fmt.Errorf("local module dep source include/exclude path %q escapes context %q", relPath, contextDirPath)
			}
			if isNegation {
				relPath = "!" + relPath
			}
			includes = append(includes, relPath)
		}

		// load this module source's context directory and deps in parallel
		var eg errgroup.Group
		eg.Go(func() error {
			err := s.dag.Select(ctx, s.dag.Root(), &localSrc.ContextDirectory,
				dagql.Selector{Field: "host"},
				dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(contextDirPath)},
						{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(includes...))},
					},
				},
			)
			if err != nil {
				return fmt.Errorf("failed to load local module source context directory: %w", err)
			}
			return nil
		})
		localSrc.Dependencies = make([]dagql.Instance[*core.ModuleSource], len(modCfg.Dependencies))
		for i, depCfg := range modCfg.Dependencies {
			eg.Go(func() error {
				if depCfg.Pin != "" {
					// git dep
					err := s.dag.Select(ctx, s.dag.Root(), &localSrc.Dependencies[i],
						dagql.Selector{
							Field: "moduleSource",
							Args: []dagql.NamedInput{
								{Name: "refString", Value: dagql.String(depCfg.Source)},
								{Name: "refPin", Value: dagql.String(depCfg.Pin)},
							},
						},
						dagql.Selector{
							Field: "withName",
							Args: []dagql.NamedInput{
								{Name: "name", Value: dagql.String(depCfg.Name)},
							},
						},
					)
					if err != nil {
						return fmt.Errorf("failed to load git dep: %w", err)
					}
					return nil
				} else {
					// local dep
					depPath := filepath.Join(contextDirPath, sourceRootRelPath, depCfg.Source)
					err := s.dag.Select(ctx, s.dag.Root(), &localSrc.Dependencies[i],
						dagql.Selector{
							Field: "moduleSource",
							Args: []dagql.NamedInput{
								{Name: "refString", Value: dagql.String(depPath)},
							},
						},
						dagql.Selector{
							Field: "withName",
							Args: []dagql.NamedInput{
								{Name: "name", Value: dagql.String(depCfg.Name)},
							},
						},
					)
					if err != nil {
						return fmt.Errorf("failed to load local dep: %w", err)
					}
					return nil
				}
			})
		}
		if err := eg.Wait(); err != nil {
			return inst, err
		}
	}

	dgst := core.HashFrom(
		// our id is tied to the context dir, so we use its digest
		localSrc.ContextDirectory.ID().Digest().String(),
		// to ensure we don't have the exact same digest as the context dir
		// TODO: const
		"moduleSource",
	)
	localSrc.Digest = dgst.String()

	return dagql.NewInstanceForCurrentID(ctx, s.dag, query, localSrc)
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

	refPath := gitSrc.Git.CloneRef
	refSubPath := filepath.Join("/", gitSrc.SourceRootSubpath)
	if refSubPath != "/" {
		refPath += refSubPath
	}
	if gitSrc.Git.Version != "" {
		refPath += "@" + gitSrc.Git.Version
	}
	gitSrc.Git.AsString = refPath

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
			return inst, fmt.Errorf("git module source %q does not contain a dagger config file", refPath)
		}
		return inst, fmt.Errorf("failed to load git module dagger config: %w", err)
	}

	// TODO: some of this logic is a bit dupe'd with local module source, could consolidate
	modCfg := &modules.ModuleConfig{}
	if err := json.Unmarshal([]byte(configContents), modCfg); err != nil {
		return inst, fmt.Errorf("failed to unmarshal module config: %w", err)
	}

	if !filepath.IsLocal(modCfg.Source) {
		return inst, fmt.Errorf("source path %q contains parent directory components", modCfg.Source)
	}
	sourceRelSubpath := filepath.Join(gitSrc.SourceRootSubpath, modCfg.Source)
	gitSrc.SourceSubpath = sourceRelSubpath

	gitSrc.ModuleName = modCfg.Name
	gitSrc.ModuleOriginalName = modCfg.Name
	gitSrc.EngineVersion = modCfg.EngineVersion
	gitSrc.SDK = modCfg.SDK
	gitSrc.IncludePaths = modCfg.Include
	gitSrc.CodegenConfig = modCfg.Codegen

	// TODO: incorporate Exclude or deprecate it
	includes := []string{
		// always load the source dir
		sourceRelSubpath + "/**" + "/*",
		// always load the config file (currently mainly so it gets incorporated into the digest)
		gitSrc.SourceRootSubpath + "/" + modules.Filename,
	}
	// add the config file includes, rebasing them from being relative to the config file
	// to being relative to the context dir
	for _, pattern := range modCfg.Include {
		isNegation := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		relPath := filepath.Join(gitSrc.SourceRootSubpath, pattern)
		if !filepath.IsLocal(relPath) {
			return inst, fmt.Errorf("git module dep source include/exclude path %q escapes context", relPath)
		}
		if isNegation {
			relPath = "!" + relPath
		}
		includes = append(includes, relPath)
	}

	// load this module source's context directory and deps in parallel
	var eg errgroup.Group
	eg.Go(func() error {
		err = s.dag.Select(ctx, s.dag.Root(), &gitSrc.ContextDirectory,
			dagql.Selector{Field: "directory"},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "directory", Value: dagql.NewID[*core.Directory](gitSrc.ContextDirectory.ID())},
					// update the context dir to apply the includes, this makes it consistent with
					// local module source equivalents and ensures we use a correctly scoped digest
					{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(includes...))},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("failed to load git module source context directory: %w", err)
		}
		return nil
	})
	gitSrc.Dependencies = make([]dagql.Instance[*core.ModuleSource], len(modCfg.Dependencies))
	for i, depCfg := range modCfg.Dependencies {
		eg.Go(func() error {
			if depCfg.Pin != "" {
				// git dep
				err := s.dag.Select(ctx, s.dag.Root(), &gitSrc.Dependencies[i],
					dagql.Selector{
						Field: "moduleSource",
						Args: []dagql.NamedInput{
							{Name: "refString", Value: dagql.String(depCfg.Source)},
							{Name: "refPin", Value: dagql.String(depCfg.Pin)},
						},
					},
					dagql.Selector{
						Field: "withName",
						Args: []dagql.NamedInput{
							{Name: "name", Value: dagql.String(depCfg.Name)},
						},
					},
				)
				if err != nil {
					return fmt.Errorf("failed to load git dep: %w", err)
				}
				return nil
			} else {
				// local dep
				refString := gitSrc.Git.CloneRef
				subPath := filepath.Join("/", gitSrc.SourceRootSubpath, depCfg.Source)
				if subPath != "/" {
					refString += subPath
				}
				if gitSrc.Git.Version != "" {
					refString += "@" + gitSrc.Git.Version
				}
				err := s.dag.Select(ctx, s.dag.Root(), &gitSrc.Dependencies[i],
					dagql.Selector{
						Field: "moduleSource",
						Args: []dagql.NamedInput{
							{Name: "refString", Value: dagql.String(refString)},
							{Name: "refPin", Value: dagql.String(gitSrc.Git.Commit)},
						},
					},
					dagql.Selector{
						Field: "withName",
						Args: []dagql.NamedInput{
							{Name: "name", Value: dagql.String(depCfg.Name)},
						},
					},
				)
				if err != nil {
					return fmt.Errorf("failed to load local dep: %w", err)
				}
				return nil
			}
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, err
	}

	// the directory is not necessarily content-hashed, make it so and use that as our digest
	bk, err := query.Self.Buildkit(ctx)
	gitSrc.ContextDirectory, err = core.MakeDirectoryContentHashed(ctx, bk, gitSrc.ContextDirectory)
	if err != nil {
		return inst, fmt.Errorf("failed to hash git context directory: %w", err)
	}

	dgst := core.HashFrom(
		// our id is tied to the context dir, so we use its digest
		gitSrc.ContextDirectory.ID().Digest().String(),
		// to ensure we don't have the exact same digest as the context dir
		// TODO: const
		"moduleSource",
	)
	gitSrc.Digest = dgst.String()

	return dagql.NewInstanceForCurrentID(ctx, s.dag, query, gitSrc)
}

func fastModuleSourceKindCheck(args moduleSourceArgs) core.ModuleSourceKind {
	switch {
	case args.RefPin != "":
		return core.ModuleSourceKindGit
	case args.Stable:
		return core.ModuleSourceKindGit
	case len(args.RefString) > 0 && (args.RefString[0] == '/' || args.RefString[0] == '.'):
		return core.ModuleSourceKindLocal
	case strings.HasPrefix(args.RefString, core.SchemeHTTP.Prefix()):
		return core.ModuleSourceKindGit
	case strings.HasPrefix(args.RefString, core.SchemeHTTPS.Prefix()):
		return core.ModuleSourceKindGit
	case strings.HasPrefix(args.RefString, core.SchemeSSH.Prefix()):
		return core.ModuleSourceKindGit
	case !strings.Contains(args.RefString, "."):
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

// used to support mocks in test
type buildkitClient interface {
	StatCallerHostPath(context.Context, string, bool) (*fsutiltypes.Stat, error)
}

func parseRefString(ctx context.Context, bk buildkitClient, args moduleSourceArgs) (*parsedRefString, error) {
	kind := fastModuleSourceKindCheck(args)
	switch kind {
	case core.ModuleSourceKindLocal:
		return &parsedRefString{
			kind: kind,
			local: &parsedLocalRefString{
				modPath: args.RefString,
			},
		}, nil
	case core.ModuleSourceKindGit:
		parsedGitRef, err := parseGitRefString(ctx, args.RefString)
		if err != nil {
			return nil, fmt.Errorf("failed to parse git ref string: %w", err)
		}
		return &parsedRefString{
			kind: kind,
			git:  &parsedGitRef,
		}, nil
	}

	// First, we stat ref in case the mod path github.com/username is a local directory
	stat, err := bk.StatCallerHostPath(ctx, args.RefString, false)
	if err == nil && stat.IsDir() {
		return &parsedRefString{
			kind: core.ModuleSourceKindLocal,
			local: &parsedLocalRefString{
				modPath: args.RefString,
			},
		}, nil
	} else if err != nil {
		slog.Debug("parseRefString stat error", "error", err)
	}

	// Parse scheme and attempt to parse as git endpoint
	parsedGitRef, err := parseGitRefString(ctx, args.RefString)
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
				modPath: args.RefString,
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

func parseGitRefString(ctx context.Context, refString string) (parsedGitRefString, error) {
	ctx, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("parseGitRefString: %s", refString), telemetry.Internal())
	defer span.End()

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

// TODO: consider just doing this in parseGitRefString if it's always called after
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

	configPath := filepath.Join(sourceRootSubpath, modules.Filename)
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
	modCfg := &modules.ModuleConfig{}
	if err := json.Unmarshal([]byte(configContents), modCfg); err != nil {
		return inst, fmt.Errorf("failed to unmarshal module config: %w", err)
	}

	sourceRelSubpath := filepath.Join(sourceRootSubpath, modCfg.Source)
	dirSrc.SourceSubpath = sourceRelSubpath

	dirSrc.ModuleName = modCfg.Name
	dirSrc.ModuleOriginalName = modCfg.Name
	dirSrc.EngineVersion = modCfg.EngineVersion
	dirSrc.SDK = modCfg.SDK
	dirSrc.IncludePaths = modCfg.Include
	dirSrc.CodegenConfig = modCfg.Codegen

	// TODO: incorporate Exclude or deprecate it
	includes := []string{
		// always load the source dir
		sourceRelSubpath + "/**" + "/*",
		// always load the config file (currently mainly so it gets incorporated into the digest)
		sourceRootSubpath + "/" + modules.Filename,
	}
	// add the config file includes, rebasing them from being relative to the config file
	// to being relative to the context dir
	for _, pattern := range modCfg.Include {
		isNegation := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		absPath := filepath.Join(sourceRootSubpath, pattern)
		relPath, err := pathutil.LexicalRelativePath("/", absPath)
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path from context to include: %w", err)
		}
		if !filepath.IsLocal(relPath) {
			return inst, fmt.Errorf("dir module dep source include/exclude path %q escapes context %q", relPath, "/")
		}
		if isNegation {
			relPath = "!" + relPath
		}
		includes = append(includes, relPath)
	}

	// load this module source's deps in parallel
	var eg errgroup.Group
	dirSrc.Dependencies = make([]dagql.Instance[*core.ModuleSource], len(modCfg.Dependencies))
	for i, depCfg := range modCfg.Dependencies {
		eg.Go(func() error {
			if depCfg.Pin != "" {
				// git dep
				err := s.dag.Select(ctx, s.dag.Root(), &dirSrc.Dependencies[i],
					dagql.Selector{
						Field: "moduleSource",
						Args: []dagql.NamedInput{
							{Name: "refString", Value: dagql.String(depCfg.Source)},
							{Name: "refPin", Value: dagql.String(depCfg.Pin)},
						},
					},
					dagql.Selector{
						Field: "withName",
						Args: []dagql.NamedInput{
							{Name: "name", Value: dagql.String(depCfg.Name)},
						},
					},
				)
				if err != nil {
					return fmt.Errorf("failed to load git dep: %w", err)
				}
				return nil
			} else {
				// local dep
				depPath := filepath.Join(sourceRootSubpath, depCfg.Source)
				err := s.dag.Select(ctx, contextDir, &dirSrc.Dependencies[i],
					dagql.Selector{
						Field: "asModuleSource",
						Args: []dagql.NamedInput{
							{Name: "sourceRootPath", Value: dagql.String(depPath)},
						},
					},
					dagql.Selector{
						Field: "withName",
						Args: []dagql.NamedInput{
							{Name: "name", Value: dagql.String(depCfg.Name)},
						},
					},
				)
				if err != nil {
					return fmt.Errorf("failed to load local dep: %w", err)
				}
				return nil
			}
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, err
	}

	dgst := core.HashFrom(
		// our id is tied to the context dir, so we use its digest
		dirSrc.ContextDirectory.ID().Digest().String(),
		// to ensure we don't have the exact same digest as the context dir
		// TODO: const
		"moduleSource",
	)
	dirSrc.Digest = dgst.String()

	inst, err = dagql.NewInstanceForCurrentID(ctx, s.dag, contextDir, dirSrc)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}

	inst = inst.WithMetadata(digest.Digest(dirSrc.Digest), true)
	return inst, nil
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
	src.SDK = args.SDK
	return src, nil
}

func (s *moduleSchema) moduleSourceWithInit(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Merge bool
	},
) (*core.ModuleSource, error) {
	src = src.Clone()
	if src.InitConfig == nil {
		src.InitConfig = &core.ModuleInitConfig{}
	}
	src.InitConfig.Merge = args.Merge
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
			depRef := existingDep.Self.Git.CloneRef
			if existingDep.Self.SourceRootSubpath != "" {
				depRef += "/" + strings.TrimPrefix(existingDep.Self.SourceRootSubpath, "/")
			}
			if existingDep.Self.Git.Version != "" {
				depRef += "@" + existingDep.Self.Git.Version
			}
<<<<<<< HEAD
			sdkSet[localDep.sdkKey] = localDep.sdk
		}

		// rebase user defined include/exclude relative to context
		rebaseIncludeExclude := func(baseAbsPath, pattern string, set *core.SliceSet[string]) error {
			isNegation := strings.HasPrefix(pattern, "!")
			pattern = strings.TrimPrefix(pattern, "!")
			absPath := filepath.Join(baseAbsPath, pattern)
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
			set.Append(relPath)
			return nil
		}
		for _, pattern := range localDep.modCfg.Include {
			if err := rebaseIncludeExclude(localDep.sourceRootAbsPath, pattern, &includeSet); err != nil {
				return inst, err
			}
		}
		for _, pattern := range localDep.modCfg.Exclude {
			if err := rebaseIncludeExclude(localDep.sourceRootAbsPath, pattern, &excludeSet); err != nil {
				return inst, err
			}
		}

		// always include the config file
		configRelPath, err := filepath.Rel(contextAbsPath, filepath.Join(rootPath, modules.Filename))
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path: %w", err)
		}
		includeSet.Append(configRelPath)

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
		includeSet.Append(sourceRelSubpath + "/**/*")
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
			includeSet.Append(path)
		}
	}

	includes := make([]string, 0, len(includeSet))
	for _, include := range includeSet {
		includes = append(includes, include)
	}
	excludes := make([]string, 0, len(excludeSet))
	for _, exclude := range excludeSet {
		excludes = append(excludes, exclude)
	}

	var loadedDir dagql.Instance[*core.Directory]
	err = s.dag.Select(ctx, s.dag.Root(), &loadedDir,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(contextAbsPath)},
				{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(excludes...))},
				{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(includes...))},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to create context directory: %w", err)
	}

	rootSubPath, err := src.SourceRootSubpath()
	if err != nil {
		return inst, fmt.Errorf("failed to get source root subpath: %w", err)
	}

	return s.normalizeCallerLoadedSource(ctx, src, sourceRootRelPath, rootSubPath, loadedDir)
}

// get an instance of ModuleSource with the context resolved from the caller that doesn't
// encode any instructions to actually reload from the caller if the ID is loaded later, which
// is possible due to blob-ifying the local import.
func (s *moduleSchema) normalizeCallerLoadedSource(
	ctx context.Context,
	src *core.ModuleSource,
	sourceRootRelPath string,
	relHostPath string,
	loadedDir dagql.Instance[*core.Directory],
) (inst dagql.Instance[*core.ModuleSource], err error) {
	err = s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(sourceRootRelPath)},
				{Name: "relHostPath", Value: dagql.String(relHostPath)},
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
	if src.WithSDK.Source != "" {
		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withSDK",
				Args: []dagql.NamedInput{
					{Name: "source", Value: dagql.String(src.WithSDK.Source)},
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

	if len(src.WithUpdateDependencies) > 0 || src.WithUpdateAllDependencies {
		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withUpdateDependencies",
				Args: []dagql.NamedInput{
					{Name: "dependencies", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(src.WithUpdateDependencies...))},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to set update dependency: %w", err)
		}
	}

	if len(src.WithoutDependencies) > 0 {
		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withoutDependencies",
				Args: []dagql.NamedInput{
					{Name: "dependencies", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(src.WithoutDependencies...))},
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
=======
			err := s.dag.Select(ctx, s.dag.Root(), &updatedDep,
>>>>>>> b2002af0d (wip)
				dagql.Selector{
					Field: "moduleSource",
					Args: []dagql.NamedInput{
						{Name: "refString", Value: dagql.String(depRef)},
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
		existingSymbolic := existingDep.Self.Git.CloneRef
		existingVersion := existingDep.Self.Git.Version
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

<<<<<<< HEAD
		bk, err := query.Buildkit(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get buildkit client: %w", err)
		}
		var modCfg modules.ModuleConfig
		configPath := filepath.Join(sourceRootAbsPath, modules.Filename)
		configBytes, err := bk.ReadCallerHostFile(ctx, configPath)
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
			if src.WithSDK.Source == "" && len(src.WithDependencies) == 0 {
				return &callerLocalDep{sourceRootAbsPath: sourceRootAbsPath}, nil
			}

		default:
			return nil, fmt.Errorf("error reading config %s: %w", configPath, err)
		}

		if topLevel {
			if src.WithName != "" {
				modCfg.Name = src.WithName
			}
			if src.WithSDK.Source != "" {
				modCfg.SDK = &modules.SDK{
					Source: src.WithSDK.Source,
				}
			}
			for _, dep := range src.WithDependencies {
				refString, err := dep.Self.Source.Self.RefString()
				if err != nil {
					return nil, fmt.Errorf("failed to get ref string for dependency: %w", err)
				}
				pin, err := dep.Self.Source.Self.Pin()
				if err != nil {
					return nil, fmt.Errorf("failed to get ref string for dependency: %w", err)
				}
				modCfg.Dependencies = append(modCfg.Dependencies, &modules.ModuleConfigDependency{
					Name:   dep.Self.Name,
					Source: refString,
					Pin:    pin,
				})
			}
		}

		localDep := &callerLocalDep{
			sourceRootAbsPath: sourceRootAbsPath,
			modCfg:            &modCfg,
		}

		for _, depCfg := range modCfg.Dependencies {
			parsed := parseRefString(ctx, bk, depCfg.Source)
			if parsed.kind != core.ModuleSourceKindLocal {
				continue
			}

			// dont load dependency module source during uninstallation
			// as it may have been removed before calling the uninstall
			uninstallRequested := false
			for _, removedDep := range src.WithoutDependencies {
				var cleanPath = filepath.Clean(removedDep)

				// ignore the dependency that we are currently uninstalling
				if depCfg.Source == cleanPath || depCfg.Name == cleanPath {
					uninstallRequested = true
					break
				}
			}

			// this dependency has been requested to be uninstalled.
			// skip loading it
			if uninstallRequested {
				continue
			}

			depAbsPath := filepath.Join(sourceRootAbsPath, parsed.modPath)
			err = s.collectCallerLocalDeps(ctx, query, contextAbsPath, depAbsPath, false, src, collectedDeps)
			if err != nil {
				return nil, fmt.Errorf("failed to collect local module source dep: %w", err)
			}
		}

		if modCfg.SDK == nil || modCfg.SDK.Source == "" {
			return localDep, nil
		}

		localDep.sdkKey = modCfg.SDK.Source

		localDep.sdk, err = s.builtinSDK(ctx, query, &core.SDKConfig{Source: modCfg.SDK.Source})
		switch {
		case err == nil:
		case errors.Is(err, errUnknownBuiltinSDK):
			parsed := parseRefString(ctx, bk, modCfg.SDK.Source)
			switch parsed.kind {
=======
	var filteredDeps []dagql.Instance[*core.ModuleSource]
	for _, existingDep := range parentSrc.Dependencies {
		var existingName, existingSymbolic, existingVersion string
		switch parentSrc.Kind {
		case core.ModuleSourceKindLocal:
			switch existingDep.Self.Kind {
>>>>>>> b2002af0d (wip)
			case core.ModuleSourceKindLocal:
				// parent=local, dep=local
				existingName = existingDep.Self.ModuleName
				parentSrcRoot := filepath.Join(parentSrc.Local.ContextDirectoryPath, parentSrc.SourceRootSubpath)
				depSrcRoot := filepath.Join(parentSrc.Local.ContextDirectoryPath, existingDep.Self.SourceRootSubpath)
				var err error
				existingSymbolic, err = pathutil.LexicalRelativePath(parentSrcRoot, depSrcRoot)
				if err != nil {
<<<<<<< HEAD
					return nil, getInvalidBuiltinSDKError(modCfg.SDK.Source)
=======
					return nil, fmt.Errorf("failed to get relative path: %w", err)
>>>>>>> b2002af0d (wip)
				}

			case core.ModuleSourceKindGit:
<<<<<<< HEAD
				localDep.sdk, err = s.sdkForModule(ctx, query, &core.SDKConfig{Source: modCfg.SDK.Source}, dagql.Instance[*core.ModuleSource]{})
=======
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
						depArg,
						existingSymbolic,
					)
				}

				// TODO: don't rerun this every loop, could just use a map and delete once matched
				// TODO: don't rerun this every loop, could just use a map and delete once matched
				// TODO: don't rerun this every loop, could just use a map and delete once matched
				parsedDepGitRef, err := parseGitRefString(ctx, depArg)
>>>>>>> b2002af0d (wip)
				if err != nil {
					return nil, fmt.Errorf("failed to parse git ref string %q: %w", depArg, err)
				}

				_, err = matchVersion([]string{existingVersion}, depVersion, parsedDepGitRef.repoRootSubdir)
				if err != nil {
					// if the requested version has prefix of repoRootSubDir, then send the error as it is
					// but if it does not, remove the repoRootSubDir from depVersion to avoid confusion.
					currentModVersion := parsedDepGitRef.modVersion
					if !strings.HasPrefix(parsedDepGitRef.modVersion, parsedDepGitRef.repoRootSubdir) {
						currentModVersion, _ = strings.CutPrefix(currentModVersion, parsedDepGitRef.repoRootSubdir+"/")
					}
					return nil, fmt.Errorf("version %q was requested to be uninstalled but the installed version is %q", parsedDepGitRef.modVersion, currentModVersion)
				}
			}
			break
		}
		if keep {
			filteredDeps = append(filteredDeps, existingDep)
		}
	}

	parentSrc.Dependencies = filteredDeps
	return parentSrc, nil
}

func (s *moduleSchema) moduleSourceGeneratedContextDirectory(
	ctx context.Context,
	srcInst dagql.Instance[*core.ModuleSource],
	args struct{},
) (genDirInst dagql.Instance[*core.Directory], err error) {
	src := srcInst.Self
	modCfg := &modules.ModuleConfig{
		Name:          src.ModuleName,
		EngineVersion: src.EngineVersion,
		SDK:           src.SDK,
		Include:       src.IncludePaths,
		Source:        src.SourceSubpath,
		Codegen:       src.CodegenConfig,
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

	switch modCfg.Source {
	case "":
	case ".":
		modCfg.Source = ""
	default:
		var err error
		modCfg.Source, err = pathutil.LexicalRelativePath(
			filepath.Join("/", src.SourceRootSubpath),
			filepath.Join("/", src.SourceSubpath),
		)
		if err != nil {
			return genDirInst, fmt.Errorf("failed to get relative path from source root to source: %w", err)
		}
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
				depCfg.Source = depSrc.Self.Git.CloneRef
				if depSrc.Self.SourceRootSubpath != "" {
					depCfg.Source += "/" + strings.TrimPrefix(depSrc.Self.SourceRootSubpath, "/")
				}
				if depSrc.Self.Git.Version != "" {
					depCfg.Source += "@" + depSrc.Self.Git.Version
				}
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
					depCfg.Source = depSrc.Self.Git.CloneRef
					if depSrc.Self.SourceRootSubpath != "" {
						depCfg.Source += "/" + strings.TrimPrefix(depSrc.Self.SourceRootSubpath, "/")
					}
					if depSrc.Self.Git.Version != "" {
						depCfg.Source += "@" + depSrc.Self.Git.Version
					}
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
	if modCfg.Name != "" && modCfg.SDK != "" {
		if srcInst.Self.InitConfig != nil &&
			srcInst.Self.InitConfig.Merge &&
			srcInst.Self.SDK != string(SDKGo) {
			return genDirInst, fmt.Errorf("merge is only supported for Go SDKs")
		}

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
		sdk, err := s.sdkForModule(ctx, src.Query, modCfg.SDK, srcInstContentHashed)
		if err != nil {
			return genDirInst, fmt.Errorf("failed to get SDK for module: %w", err)
		}

		generatedCode, err := sdk.Codegen(ctx, deps, srcInstContentHashed)
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
	if src.Self.ModuleName == "" || src.Self.SDK == "" {
		return inst, fmt.Errorf("module name and SDK must be set")
	}

	mod := &core.Module{
		Query: src.Self.Query,

		Source: src,

		NameField:    src.Self.ModuleName,
		OriginalName: src.Self.ModuleOriginalName,

		SDKConfig: src.Self.SDK,
	}

	// TODO: dedupe w/ generatedContextDiff
	var eg errgroup.Group
	depMods := make([]dagql.Instance[*core.Module], len(src.Self.Dependencies))
	for i, depSrc := range src.Self.Dependencies {
		eg.Go(func() error {
			return s.dag.Select(ctx, depSrc, &depMods[i],
				dagql.Selector{Field: "asModule"},
			)
		})
	}
	if err := eg.Wait(); err != nil {
		return inst, fmt.Errorf("failed to load module dependencies: %w", err)
	}
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
			// TODO: USE CORRECT ENGINE VERSION!!!!!
			// TODO: USE CORRECT ENGINE VERSION!!!!!
			// TODO: USE CORRECT ENGINE VERSION!!!!!
			// TODO: USE CORRECT ENGINE VERSION!!!!!
			// TODO: USE CORRECT ENGINE VERSION!!!!!
			dag.View = engine.BaseVersion(engine.NormalizeVersion(src.Self.EngineVersion))
			deps.Mods[i] = &CoreMod{Dag: &dag}
		}
	}
	mod.Deps = deps

	// TODO: wrap up in nicer looking util/interface
	_, _, err = s.dag.Cache.GetOrInitialize(ctx, digest.Digest(src.Self.Digest), func(context.Context) (dagql.Typed, error) {
		return src, nil
	})
	if err != nil {
		return inst, fmt.Errorf("failed to get or initialize instance: %w", err)
	}
	srcInstContentHashed := src.WithMetadata(digest.Digest(src.Self.Digest), true)

	// TODO: parallelize sdkForModule w/ above too?
	sdk, err := s.sdkForModule(ctx, src.Self.Query, src.Self.SDK, srcInstContentHashed)
	if err != nil {
		return inst, fmt.Errorf("failed to get SDK for module: %w", err)
	}
	mod.Runtime, err = sdk.Runtime(ctx, mod.Deps, srcInstContentHashed)
	if err != nil {
		return inst, fmt.Errorf("failed to get module runtime: %w", err)
	}

	// construct a special function with no object or function name, which tells
	// the SDK to return the module's definition (in terms of objects, fields and
	// functions)
	modName := src.Self.ModuleName
	getModDefFn, err := core.NewModFunction(
		ctx,
		src.Self.Query,
		mod,
		nil,
		mod.Runtime,
		core.NewFunction("", &core.TypeDef{
			Kind:     core.TypeDefKindObject,
			AsObject: dagql.NonNull(core.NewObjectTypeDef("Module", "")),
		}))
	if err != nil {
		return inst, fmt.Errorf("failed to create module definition function for module %q: %w", modName, err)
	}

	result, err := getModDefFn.Call(ctx, &core.CallOpts{Cache: true, SkipSelfSchema: true, Server: s.dag})
	if err != nil {
		return inst, fmt.Errorf("failed to call module %q to get functions: %w", modName, err)
	}
	resultInst, ok := result.(dagql.Instance[*core.Module])
	if !ok {
		return inst, fmt.Errorf("expected Module result, got %T", result)
	}

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
