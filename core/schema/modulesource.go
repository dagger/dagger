package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger/telemetry"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/vcs"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/tonistiigi/fsutil/types"
)

type moduleSourceArgs struct {
	// avoiding name "ref" due to that being a reserved word in some SDKs (e.g. Rust)
	RefString string
	RefPin    string `default:""`

	Stable bool `default:"false"`

	// relHostPath is the relative path to the module root from the host directory.
	// This should only be used internally.
	RelHostPath string `default:""`
}

func (s *moduleSchema) moduleSource(ctx context.Context, query *core.Query, args moduleSourceArgs) (*core.ModuleSource, error) {
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	parsed := parseRefString(ctx, bk, args.RefString)

	src := &core.ModuleSource{
		Query: query,
		Kind:  parsed.kind,
	}

	switch src.Kind {
	case core.ModuleSourceKindLocal:
		if filepath.IsAbs(parsed.modPath) {
			cwdStat, err := bk.StatCallerHostPath(ctx, ".", true)
			if err != nil {
				return nil, fmt.Errorf("failed to stat caller's current working directory: %w", err)
			}

			relPath, err := client.LexicalRelativePath(cwdStat.Path, parsed.modPath)
			if err != nil {
				return nil, err
			}

			parsed.modPath = relPath
		}

		src.AsLocalSource = dagql.NonNull(&core.LocalModuleSource{
			RootSubpath: parsed.modPath,
			RelHostPath: args.RelHostPath,
		})

	case core.ModuleSourceKindGit:
		src.AsGitSource = dagql.NonNull(&core.GitModuleSource{})

		src.AsGitSource.Value.Root = parsed.repoRoot.Root
		src.AsGitSource.Value.HTMLRepoURL = parsed.repoRoot.Repo

		// Determine usernames for source reference and actual cloning
		sourceUser, cloneUser := parsed.sshusername, parsed.sshusername
		if cloneUser == "" && parsed.scheme.IsSSH() {
			cloneUser = "git"
		}

		if sourceUser != "" {
			sourceUser += "@"
		}

		if cloneUser != "" {
			cloneUser += "@"
		}

		// Construct the source reference (preserves original input)
		src.AsGitSource.Value.CloneRef = parsed.scheme.Prefix() + sourceUser + parsed.repoRoot.Root

		// Construct the reference for actual cloning (ensures username for SSH)
		cloneRef := parsed.scheme.Prefix() + cloneUser + parsed.repoRoot.Root

		subPath := "/"
		if parsed.repoRootSubdir != "" {
			subPath = parsed.repoRootSubdir
		}

		commitRef := args.RefPin
		if parsed.hasVersion {
			modVersion := parsed.modVersion
			if isSemver(modVersion) {
				var tags dagql.Array[dagql.String]
				err := s.dag.Select(ctx, s.dag.Root(), &tags,
					dagql.Selector{
						Field: "git",
						Args: []dagql.NamedInput{
							{Name: "url", Value: dagql.String(cloneRef)},
						},
					},
					dagql.Selector{
						Field: "tags",
					},
				)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve git tags: %w", err)
				}

				allTags := make([]string, len(tags))
				for i, tag := range tags {
					allTags[i] = tag.String()
				}

				matched, err := matchVersion(allTags, modVersion, subPath)
				if err != nil {
					return nil, fmt.Errorf("matching version to tags: %w", err)
				}
				modVersion = matched
			}
			src.AsGitSource.Value.Version = modVersion
			if commitRef == "" {
				commitRef = modVersion
			}
		}

		var commitRefSelector dagql.Selector
		if commitRef == "" {
			if args.Stable && !parsed.hasVersion {
				return nil, fmt.Errorf("no version provided for stable remote ref: %s", args.RefString)
			}
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
		err := s.dag.Select(ctx, s.dag.Root(), &gitRef,
			dagql.Selector{
				Field: "git",
				Args: []dagql.NamedInput{
					{Name: "url", Value: dagql.String(cloneRef)},
				},
			},
			commitRefSelector,
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
	scheme         core.SchemeType
	sshusername    string
}

func (ref parsedRefString) String() string {
	s := ref.modPath
	if ref.scheme == core.SchemeSCPLike {
		s = strings.Replace(s, "/", ":", 1)
	}

	if ref.sshusername != "" {
		s = ref.sshusername + "@" + s
	}
	if ref.hasVersion {
		s = s + "@" + ref.modVersion
	}

	s = ref.scheme.Prefix() + s
	return s
}

// interface used for host interaction mocking
type buildkitClient interface {
	StatCallerHostPath(ctx context.Context, path string, followLinks bool) (*types.Stat, error)
}

// parseRefString parses a ref string into its components
// New heuristic:
// - stat folder to see if dir is present
// - if not, try to isolate root of git repo from the ref
// - if nothing worked, fallback as local ref, as before
func parseRefString(ctx context.Context, bk buildkitClient, refString string) parsedRefString {
	ctx, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("parseRefString: %s", refString), telemetry.Internal())
	defer span.End()
	localParsed := parsedRefString{
		modPath: refString,
		kind:    core.ModuleSourceKindLocal,
		scheme:  core.NoScheme,
	}

	// if the refString is a relative path, we can short-circuit here as we
	// don't really about the `stat` return as the refString will always
	// be local in this case.
	if strings.HasPrefix(refString, ".") {
		return localParsed
	}

	// First, we stat ref in case the mod path github.com/username is a local directory
	stat, err := bk.StatCallerHostPath(ctx, refString, false)
	if err == nil && stat.IsDir() {
		return localParsed
	} else if err != nil {
		slog.Debug("parseRefString stat error", "error", err)
	}

	// Parse scheme and attempt to parse as git endpoint
	gitParsed, err := parseGitEndpoint(refString)
	if err == nil {
		// Try to isolate the root of the git repo
		// RepoRootForImportPath does not support SCP-like ref style. In parseGitEndpoint, we made sure that all refs
		// would be compatible with this function to benefit from the repo URL and root splitting
		repoRoot, err := vcs.RepoRootForImportPath(gitParsed.modPath, false)
		if err == nil && repoRoot != nil && repoRoot.VCS != nil && repoRoot.VCS.Name == "Git" {
			gitParsed.repoRoot = repoRoot

			// the extra "/" trim is important as subpath traversal such as /../ are being cleaned by filePath.Clean
			gitParsed.repoRootSubdir = strings.TrimPrefix(strings.TrimPrefix(gitParsed.modPath, repoRoot.Root), "/")

			// Restore SCPLike ref format
			if gitParsed.scheme == core.SchemeSCPLike {
				gitParsed.repoRoot.Root = strings.Replace(gitParsed.repoRoot.Root, "/", ":", 1)
			}
			return gitParsed
		}
	}

	// Fallback to local reference
	return localParsed
}

func parseGitEndpoint(refString string) (parsedRefString, error) {
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
		return parsedRefString{}, err
	}

	gitParsed := parsedRefString{
		modPath:     endpoint.Host + endpoint.Path,
		kind:        core.ModuleSourceKindGit,
		scheme:      scheme,
		sshusername: endpoint.User,
	}

	parts := strings.SplitN(endpoint.Path, "@", 2)
	if len(parts) == 2 {
		gitParsed.modPath = endpoint.Host + parts[0]
		gitParsed.modVersion = parts[1]
		gitParsed.hasVersion = true
	}

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

func (s *moduleSchema) moduleSourceAsModule(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct {
		EngineVersion dagql.Optional[dagql.String]
	},
) (inst dagql.Instance[*core.Module], err error) {
	withSourceInputs := []dagql.NamedInput{
		{Name: "source", Value: dagql.NewID[*core.ModuleSource](src.ID())},
	}
	if args.EngineVersion.Valid {
		withSourceInputs = append(withSourceInputs, dagql.NamedInput{Name: "engineVersion", Value: args.EngineVersion})
	}
	err = s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "module",
		},
		dagql.Selector{
			Field: "withSource",
			Args:  withSourceInputs,
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

func (s *moduleSchema) moduleSourcePin(ctx context.Context, src *core.ModuleSource, args struct{}) (string, error) {
	return src.Pin()
}

func (s *moduleSchema) gitModuleSourceHTMLURL(
	ctx context.Context,
	ref *core.GitModuleSource,
	args struct{},
) (string, error) {
	return ref.HTMLURL(), nil
}

func (s *moduleSchema) gitModuleSourceCloneURL(
	ctx context.Context,
	ref *core.GitModuleSource,
	args struct{},
) (string, error) {
	return ref.CloneRef, nil
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

func (s *moduleSchema) filterUnInstalledDeps(ctx context.Context, bk *buildkit.Client, currentDeps []*modules.ModuleConfigDependency, filterDeps []string) ([]*modules.ModuleConfigDependency, error) {
	if len(filterDeps) == 0 {
		return currentDeps, nil
	}

	filterDepsMap := map[string]parsedRefString{}
	for _, filterDep := range filterDeps {
		filterDepParsed := parseRefString(ctx, bk, filterDep)

		// for scenario where user tries to uninstall using relative path
		var cleanDepPath = filepath.Clean(filterDepParsed.modPath)

		filterDepsMap[cleanDepPath] = filterDepParsed
	}

	effectiveDependencies := []*modules.ModuleConfigDependency{}
	for _, currentDep := range currentDeps {
		currentDepParsed := parseRefString(ctx, bk, currentDep.Source)

		uninstalled := false
		for cleanDepPath, filterDepParsed := range filterDepsMap {
			// filter by mod path (git source dependency or local path) or dependency name as configured in dagger.json
			if currentDepParsed.modPath != filterDepParsed.modPath && currentDep.Name != cleanDepPath {
				continue
			}

			// return error if the version number was specified to uninstall, but that version is not installed
			// TODO(rajatjindal): we should possibly resolve commit from a version if current dep has no version specified and
			// see if we can match Pin() with that commit. But that would mean resolving the commit here.
			if filterDepParsed.hasVersion && !currentDepParsed.hasVersion {
				return nil, fmt.Errorf("version %q was requested to be uninstalled but the dependency %q was originally installed without a specific version. Try re-running the uninstall command without specifying the version number", filterDepParsed.modVersion, currentDepParsed.modPath)
			}

			if filterDepParsed.hasVersion {
				_, err := matchVersion([]string{currentDepParsed.modVersion}, filterDepParsed.modVersion, filterDepParsed.repoRootSubdir)
				if err != nil {
					// if the requested version has prefix of repoRootSubDir, then send the error as it is
					// but if it does not, remove the repoRootSubDir from currentDepParsed.modVersion to avoid confusion.
					currentModVersion := currentDepParsed.modVersion
					if !strings.HasPrefix(filterDepParsed.modVersion, filterDepParsed.repoRootSubdir) {
						currentModVersion, _ = strings.CutPrefix(currentModVersion, filterDepParsed.repoRootSubdir+"/")
					}
					return nil, fmt.Errorf("version %q was requested to be uninstalled but the installed version is %q", filterDepParsed.modVersion, currentModVersion)
				}
			}

			uninstalled = true
			continue
		}

		if !uninstalled {
			effectiveDependencies = append(effectiveDependencies, currentDep)
		}
	}

	return effectiveDependencies, nil
}

func (s *moduleSchema) applyDepUpdate(ctx context.Context, bk *buildkit.Client, currentDep *modules.ModuleConfigDependency, toBeUpdatedMap map[string]parsedRefString) (string, *modules.ModuleConfigDependency, bool, error) {
	currentDepParsed := parseRefString(ctx, bk, currentDep.Source)
	for toBeUpdatedDepKey, toBeUpdatedDepParsed := range toBeUpdatedMap {
		// to support scenarios such as "dagger update <just-name>@<version>"
		toBeUpdatedName, toBeUpdatedVersion, _ := strings.Cut(toBeUpdatedDepKey, "@")
		if currentDepParsed.modPath == toBeUpdatedDepParsed.modPath || currentDep.Name == toBeUpdatedName {
			// if the currentDep is local and requested to be updated, return error
			if currentDepParsed.kind == core.ModuleSourceKindLocal {
				return "", nil, false, fmt.Errorf("updating local deps is not supported")
			}

			source := currentDepParsed.modPath
			// if a specific version was requested, use that
			// else use whatever version current version is configured to use
			if toBeUpdatedDepParsed.hasVersion {
				source += "@" + toBeUpdatedDepParsed.modVersion
			} else if toBeUpdatedVersion != "" {
				source += "@" + toBeUpdatedVersion
			} else if currentDepParsed.hasVersion {
				source += "@" + currentDepParsed.modVersion
			}

			return toBeUpdatedDepKey, &modules.ModuleConfigDependency{
				Name:   currentDep.Name,
				Source: source,
				Pin:    "",
			}, true, nil
		}
	}

	return "", nil, false, nil
}

func (s *moduleSchema) applyDepUpdates(ctx context.Context, bk *buildkit.Client, currentDeps []*modules.ModuleConfigDependency, updateList []string, updateAll bool) ([]*modules.ModuleConfigDependency, error) {
	if len(updateList) == 0 && !updateAll {
		return currentDeps, nil
	}

	updatedDependencies := []*modules.ModuleConfigDependency{}
	// if updateAll is true, then just clearup the pin and return
	if updateAll {
		for _, currentDep := range currentDeps {
			updatedDependencies = append(updatedDependencies, &modules.ModuleConfigDependency{
				Name:   currentDep.Name,
				Source: currentDep.Source,
				Pin:    "",
			})
		}

		return updatedDependencies, nil
	}

	toBeUpdatedMap := map[string]parsedRefString{}
	for _, dep := range updateList {
		depParsed := parseRefString(ctx, bk, dep)
		toBeUpdatedMap[depParsed.modPath] = depParsed
	}

	for _, currentDep := range currentDeps {
		updatedDepKey, updatedDep, isUpdated, err := s.applyDepUpdate(ctx, bk, currentDep, toBeUpdatedMap)
		if err != nil {
			return nil, err
		}

		if isUpdated {
			updatedDependencies = append(updatedDependencies, updatedDep)
			delete(toBeUpdatedMap, updatedDepKey)
		} else {
			updatedDependencies = append(updatedDependencies, currentDep)
		}
	}

	// error out if there are dependencies which were requested to be updated
	// but not found in the current list of dependencies
	if len(toBeUpdatedMap) > 0 {
		deps := []string{}
		for _, v := range toBeUpdatedMap {
			deps = append(deps, v.modPath)
		}
		return nil, fmt.Errorf("dependency %q was requested to be updated, but it is not found in the dependencies list", strings.Join(deps, ","))
	}

	return updatedDependencies, nil
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

	resolveDep := func(ctx context.Context, depName string, depSrc dagql.Instance[*core.ModuleSource]) (inst dagql.Instance[*core.ModuleDependency], err error) {
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
			return inst, fmt.Errorf("failed to resolve dependency: %w", err)
		}

		if depName == "" {
			// this happens if installing a new module without an explicit
			// name or upgrading from an old config that doesn't have a name set
			depName, err = resolvedDepSrc.Self.ModuleName(ctx)
			if err != nil {
				return inst, fmt.Errorf("failed to load module name: %w", err)
			}
		}

		err = s.dag.Select(ctx, s.dag.Root(), &inst,
			dagql.Selector{
				Field: "moduleDependency",
				Args: []dagql.NamedInput{
					{Name: "source", Value: dagql.NewID[*core.ModuleSource](resolvedDepSrc.ID())},
					{Name: "name", Value: dagql.String(depName)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to create module dependency: %w", err)
		}
		return inst, nil
	}

	var existingDeps []dagql.Instance[*core.ModuleDependency]
	if ok {
		bk, err := src.Self.Query.Buildkit(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get buildkit client: %w", err)
		}

		filteredDeps, err := s.filterUnInstalledDeps(ctx, bk, modCfg.Dependencies, src.Self.WithoutDependencies)
		if err != nil {
			return nil, err
		}

		updatedDeps, err := s.applyDepUpdates(ctx, bk, filteredDeps, src.Self.WithUpdateDependencies, src.Self.WithUpdateAllDependencies)
		if err != nil {
			return nil, err
		}

		existingDeps = make([]dagql.Instance[*core.ModuleDependency], len(updatedDeps))
		var eg errgroup.Group
		for i, depCfg := range updatedDeps {
			eg.Go(func() error {
				var depSrc dagql.Instance[*core.ModuleSource]
				err := s.dag.Select(ctx, s.dag.Root(), &depSrc,
					dagql.Selector{
						Field: "moduleSource",
						Args: []dagql.NamedInput{
							{Name: "refString", Value: dagql.String(depCfg.Source)},
							{Name: "refPin", Value: dagql.String(depCfg.Pin)},
						},
					},
				)
				if err != nil {
					return fmt.Errorf("failed to create module source from dependency: %w", err)
				}

				existingDeps[i], err = resolveDep(ctx, depCfg.Name, depSrc)
				return err
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, fmt.Errorf("failed to load pre-configured dependencies: %w", err)
		}
	}

	newDeps := make([]dagql.Instance[*core.ModuleDependency], len(src.Self.WithDependencies))
	var eg errgroup.Group
	for i, dep := range src.Self.WithDependencies {
		eg.Go(func() error {
			newDeps[i], err = resolveDep(ctx, dep.Self.Name, dep.Self.Source)
			return err
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

	uniqueNameMap := map[string]struct{}{}
	finalDeps := make([]dagql.Instance[*core.ModuleDependency], 0, len(depSet))
	for _, dep := range depSet {
		if _, exists := uniqueNameMap[dep.Self.Name]; dep.Self.Name != "" && exists {
			return nil, fmt.Errorf("two or more dependencies are trying to use the same name %q", dep.Self.Name)
		}
		uniqueNameMap[dep.Self.Name] = struct{}{}
		finalDeps = append(finalDeps, dep)
	}

	sort.Slice(finalDeps, func(i, j int) bool {
		return finalDeps[i].Self.Name < finalDeps[j].Self.Name
	})

	return finalDeps, nil
}

func (s *moduleSchema) moduleSourceWithUpdateDependencies(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Dependencies []string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()

	src.WithUpdateDependencies = args.Dependencies
	if len(src.WithUpdateDependencies) == 0 {
		src.WithUpdateAllDependencies = true
	}

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
	newDeps, err := collectIDInstances(ctx, s.dag, args.Dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to load module source dependencies from ids: %w", err)
	}
	src.WithDependencies = append(src.WithDependencies, newDeps...)
	return src, nil
}

func (s *moduleSchema) moduleSourceWithoutDependencies(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Dependencies []string
	},
) (*core.ModuleSource, error) {
	src = src.Clone()
	src.WithoutDependencies = args.Dependencies
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

type ModuleSourceWithInitConfigArgs struct {
	Merge bool `default:"false"`
}

func (s *moduleSchema) moduleSourceWithInit(
	ctx context.Context,
	src *core.ModuleSource,
	args ModuleSourceWithInitConfigArgs,
) (*core.ModuleSource, error) {
	src.WithInitConfig = &core.ModuleInitConfig{
		Merge: args.Merge,
	}
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
		newPin, err := src.Pin()
		if err != nil {
			return inst, fmt.Errorf("failed to get module source pin: %w", err)
		}

		var newDepSrc dagql.Instance[*core.ModuleSource]
		err = s.dag.Select(ctx, s.dag.Root(), &newDepSrc,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(newDepRefStr)},
					{Name: "refPin", Value: dagql.String(newPin)},
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
	contextAbsPath, _, err := src.ResolveContextPathFromCaller(ctx)
	return contextAbsPath, err
}

//nolint:gocyclo // it's already been split up where it makes sense, more would just create indirection in reading it
func (s *moduleSchema) moduleSourceResolveFromCaller(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (inst dagql.Instance[*core.ModuleSource], err error) {
	contextAbsPath, sourceRootAbsPath, err := src.ResolveContextPathFromCaller(ctx)
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
	if sourceRootRelPath != "." {
		sourceRootRelPath = "./" + sourceRootRelPath
	}

	collectedDeps := dagql.NewCacheMap[string, *callerLocalDep]()
	if err := s.collectCallerLocalDeps(ctx, src.Query, contextAbsPath, sourceRootAbsPath, true, src, collectedDeps); err != nil {
		return inst, fmt.Errorf("failed to collect local module source deps: %w", err)
	}

	var includeSet core.SliceSet[string] = []string{}
	// always exclude .git dirs, we don't need them and they tend to invalidate cache a lot
	var excludeSet core.SliceSet[string] = []string{"**/.git"}

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
				includeSet.Append(sourceRootAbsPath)
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

	bk, err := src.Query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	dgst, err := bk.LocalImport(
		ctx,
		src.Query.Platform().Spec(),
		contextAbsPath,
		excludes,
		includes,
	)
	if err != nil {
		return inst, fmt.Errorf("failed to import local module source: %w", err)
	}
	loadedDir, err := core.LoadBlob(ctx, s.dag, dgst)
	if err != nil {
		return inst, fmt.Errorf("failed to load local module source: %w", err)
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

	if src.WithInitConfig != nil {
		err = s.dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withInit",
				Args: []dagql.NamedInput{
					{Name: "merge", Value: dagql.Boolean(src.WithInitConfig.Merge)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to set init config: %w", err)
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

//nolint:gocyclo // it's already been split up where it makes sense, more would just create indirection in reading it
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

		if modCfg.SDK == "" {
			return localDep, nil
		}

		localDep.sdkKey = modCfg.SDK

		localDep.sdk, err = s.builtinSDK(ctx, query, modCfg.SDK)
		switch {
		case err == nil:
		case errors.Is(err, errUnknownBuiltinSDK):
			parsed := parseRefString(ctx, bk, modCfg.SDK)
			switch parsed.kind {
			case core.ModuleSourceKindLocal:
				// SDK is a local custom one, it needs to be included
				sdkPath := filepath.Join(sourceRootAbsPath, parsed.modPath)

				// this check here enable us to send more specific error
				// if the sdk provided by user is neither an inbuiltsdk,
				// nor a valid sdk available on local path.
				_, err = bk.StatCallerHostPath(ctx, sdkPath, true)
				if err != nil {
					return nil, getInvalidBuiltinSDKError(modCfg.SDK)
				}

				err = s.collectCallerLocalDeps(ctx, query, contextAbsPath, sdkPath, false, src, collectedDeps)
				if err != nil {
					return nil, fmt.Errorf("failed to collect local sdk: %w", err)
				}

				// TODO: this is inefficient, leads to extra local loads, but only for case
				// of local custom SDK.
				callerCwdStat, err := bk.StatCallerHostPath(ctx, ".", true)
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

func (s *moduleSchema) moduleSourceResolveDirectoryFromCaller(
	ctx context.Context,
	src *core.ModuleSource,
	args struct {
		Path     string
		ViewName *string
		Ignore   []string `default:"[]"`
	},
) (inst dagql.Instance[*core.Directory], err error) {
	path := args.Path

	bk, err := src.Query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	stat, err := bk.StatCallerHostPath(ctx, path, true)
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

	// If there's no view configured, we can apply ignore patterns.
	if args.ViewName == nil && len(args.Ignore) > 0 {
		excludes = append(excludes, args.Ignore...)
	}

	dgst, err := bk.LocalImport(
		ctx, src.Query.Platform().Spec(),
		path,
		excludes,
		includes,
	)
	if err != nil {
		return inst, fmt.Errorf("failed to import local directory module arg: %w", err)
	}
	return core.LoadBlob(ctx, s.dag, dgst)
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

func (s *moduleSchema) moduleSourceDigest(
	ctx context.Context,
	src *core.ModuleSource,
	args struct{},
) (string, error) {
	return src.Digest(ctx)
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
