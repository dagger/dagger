package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
)

type ModuleSourceKind string

var ModuleSourceKindEnum = dagql.NewEnum[ModuleSourceKind]()

var (
	ModuleSourceKindLocal = ModuleSourceKindEnum.Register("LOCAL_SOURCE")
	ModuleSourceKindGit   = ModuleSourceKindEnum.Register("GIT_SOURCE")
)

func (proto ModuleSourceKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSourceKind",
		NonNull:   true,
	}
}

func (proto ModuleSourceKind) TypeDescription() string {
	return "The kind of module source."
}

func (proto ModuleSourceKind) Decoder() dagql.InputDecoder {
	return ModuleSourceKindEnum
}

func (proto ModuleSourceKind) ToLiteral() call.Literal {
	return ModuleSourceKindEnum.Literal(proto)
}

type ModuleInitConfig struct {
	Merge bool
}

type ModuleSource struct {
	Query *Query

	Kind ModuleSourceKind `field:"true" name:"kind" doc:"The kind of source (e.g. local, git, etc.)"`

	AsLocalSource dagql.Nullable[*LocalModuleSource] `field:"true" doc:"If the source is of kind local, the local source representation of it."`

	AsGitSource dagql.Nullable[*GitModuleSource] `field:"true" doc:"If the source is a of kind git, the git source representation of it."`

	// Settings that can be used to initialize or override the source's configuration
	WithName                  string
	WithDependencies          []dagql.Instance[*ModuleDependency]
	WithoutDependencies       []string
	WithUpdateDependencies    []string
	WithUpdateAllDependencies bool
	WithSDK                   string
	WithInitConfig            *ModuleInitConfig
	WithSourceSubpath         string
	WithViews                 []*ModuleSourceView
}

func (src *ModuleSource) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSource",
		NonNull:   true,
	}
}

func (src *ModuleSource) TypeDescription() string {
	return "The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc."
}

func (src ModuleSource) Clone() *ModuleSource {
	cp := src

	if src.Query != nil {
		cp.Query = src.Query.Clone()
	}

	if src.AsLocalSource.Valid {
		cp.AsLocalSource.Value = src.AsLocalSource.Value.Clone()
	}

	if src.AsGitSource.Valid {
		cp.AsGitSource.Value = src.AsGitSource.Value.Clone()
	}

	if src.WithDependencies != nil {
		cp.WithDependencies = make([]dagql.Instance[*ModuleDependency], len(src.WithDependencies))
		copy(cp.WithDependencies, src.WithDependencies)
	}

	if src.WithViews != nil {
		cp.WithViews = make([]*ModuleSourceView, len(src.WithViews))
		copy(cp.WithViews, src.WithViews)
	}

	if src.WithInitConfig != nil {
		cp.WithInitConfig = new(ModuleInitConfig)
		*cp.WithInitConfig = *src.WithInitConfig
	}

	return &cp
}

func (src *ModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return src.AsLocalSource.Value.PBDefinitions(ctx)
	case ModuleSourceKindGit:
		return src.AsGitSource.Value.PBDefinitions(ctx)
	default:
		return nil, fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) Digest(ctx context.Context) (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		dir, err := src.ContextDirectory()
		if err != nil {
			return "", err
		}
		return dir.Self.Digest(ctx)
	case ModuleSourceKindGit:
		// git uses sha1 hex digests
		return "sha1:" + src.AsGitSource.Value.Commit, nil
	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) RefString() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return src.AsLocalSource.Value.RefString(), nil
	case ModuleSourceKindGit:
		return src.AsGitSource.Value.RefString(), nil
	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) Pin() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return "", nil
	case ModuleSourceKindGit:
		return src.AsGitSource.Value.Pin(), nil
	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) Symbolic() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return src.AsLocalSource.Value.Symbolic(), nil
	case ModuleSourceKindGit:
		return src.AsGitSource.Value.Symbolic(), nil
	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) SourceRootRelSubPath() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return src.AsLocalSource.Value.RelHostPath, nil
	case ModuleSourceKindGit:
		return src.AsGitSource.Value.RootSubpath, nil
	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) SourceRootSubpath() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return src.AsLocalSource.Value.RootSubpath, nil
	case ModuleSourceKindGit:
		return src.AsGitSource.Value.RootSubpath, nil
	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) SourceSubpath(ctx context.Context) (string, error) {
	rootSubpath, err := src.SourceRootSubpath()
	if err != nil {
		return "", fmt.Errorf("failed to get source root subpath: %w", err)
	}

	if src.WithSourceSubpath != "" {
		if !filepath.IsLocal(src.WithSourceSubpath) {
			return "", fmt.Errorf("source path %q contains parent directory components", src.WithSourceSubpath)
		}
		return filepath.Join(rootSubpath, src.WithSourceSubpath), nil
	}

	cfg, ok, err := src.ModuleConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("module config: %w", err)
	}
	if !ok {
		return "", nil
	}
	if cfg.Source == "" {
		return "", nil
	}

	if !filepath.IsLocal(cfg.Source) {
		return "", fmt.Errorf("source path %q contains parent directory components", cfg.Source)
	}
	return filepath.Join(rootSubpath, cfg.Source), nil
}

// SourceSubpathWithDefault is the same as SourceSubpath, but it will default to the root subpath if the module has no configuration.
func (src *ModuleSource) SourceSubpathWithDefault(ctx context.Context) (string, error) {
	sourceSubpath, err := src.SourceSubpath(ctx)
	if err != nil {
		return "", err
	}
	if sourceSubpath == "" {
		return src.SourceRootSubpath()
	}
	return sourceSubpath, nil
}

func (src *ModuleSource) ModuleName(ctx context.Context) (string, error) {
	if src.WithName != "" {
		// use override name if set
		return src.WithName, nil
	}

	cfg, ok, err := src.ModuleConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("module config: %w", err)
	}
	if !ok {
		return "", nil
	}
	return cfg.Name, nil
}

func (src *ModuleSource) ModuleOriginalName(ctx context.Context) (string, error) {
	cfg, ok, err := src.ModuleConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("module config: %w", err)
	}
	if !ok || cfg.Name == "" {
		// no name for this module yet in static config, use the caller specified name if set
		// since that is what will become the original name once configuration is generated
		return src.WithName, nil
	}
	return cfg.Name, nil
}

func (src *ModuleSource) ModuleEngineVersion(ctx context.Context) (string, error) {
	cfg, ok, err := src.ModuleConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("module config: %w", err)
	}
	if !ok {
		return "", nil
	}
	return cfg.EngineVersion, nil
}

func (src *ModuleSource) SDK(ctx context.Context) (string, error) {
	if src.WithSDK != "" {
		return src.WithSDK, nil
	}
	modCfg, ok, err := src.ModuleConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("module config: %w", err)
	}
	if !ok {
		return "", nil
	}
	return modCfg.SDK, nil
}

func (src *ModuleSource) AutomaticGitignore(ctx context.Context) (*bool, error) {
	modCfg, ok, err := src.ModuleConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("module config: %w", err)
	}
	if !ok {
		return nil, nil
	}
	if modCfg.Codegen == nil {
		return nil, nil
	}
	return modCfg.Codegen.AutomaticGitignore, nil
}

// LoadContext loads a directory from the module context directory.
//
// If the module is local, it will load the directory from the local source
// directly from the host.
// In that case, the path is first resolved based on the caller's host location.
// Then if the path is absolute, it will be relative to the context directory.
// Otherwise, it will be relative to the module root directory.
//
// If the module is git, it will load the directory from the git repository
// using its context directory.
func (src *ModuleSource) LoadContext(ctx context.Context, dag *dagql.Server, path string, ignore []string) (inst dagql.Instance[*Directory], err error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		bk, err := src.Query.Buildkit(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get buildkit api: %w", err)
		}

		localSourceClientMetadata, err := src.Query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get client metadata: %w", err)
		}
		localSourceCtx := engine.ContextWithClientMetadata(ctx, localSourceClientMetadata)

		// Retrieve the absolute path to the context directory (.git or dagger.json)
		// and the module root directory (dagger.json)
		ctxPath, modPath, err := src.ResolveContextPathFromModule(localSourceCtx)
		if err != nil {
			return inst, fmt.Errorf("failed to resolve context path: %w", err)
		}

		// If path is not absolute, it's relative to the module root directory.
		// If path is absolute, it's relative to the context directory.
		if !filepath.IsAbs(path) {
			path = filepath.Join(modPath, path)
		} else {
			path = filepath.Join(ctxPath, path)
		}

		// We just check if the path is relative to the context directory,
		// if not, that means it's a path that target an outside directory
		// which is not allowed.
		relativePathToCtx, err := filepath.Rel(ctxPath, path)
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path to context: %w", err)
		}

		// If the relative path is outisde of the context directory, throw an error.
		if strings.HasPrefix(relativePathToCtx, "..") {
			return inst, fmt.Errorf("path %q is outside of context directory %q, path should be relative to the context directory", path, ctxPath)
		}

		dgst, err := bk.LocalImport(localSourceCtx, src.Query.Platform().Spec(), path, ignore, []string{})
		if err != nil {
			return inst, fmt.Errorf("failed to import local module src: %w", err)
		}

		inst, err = LoadBlob(localSourceCtx, dag, dgst)
		if err != nil {
			return inst, fmt.Errorf("failed to load local module src: %w", err)
		}

		return inst, nil
	case ModuleSourceKindGit:
		slog.Debug("moduleSource.LoadContext: loading contextual directory from git", "path", path, "kind", src.Kind, "repo", src.AsGitSource.Value.HTMLURL())

		// Use the Git context directory.
		ctxDir := src.AsGitSource.Value.ContextDirectory.Self

		if !filepath.IsAbs(path) {
			path = filepath.Join(src.AsGitSource.Value.RootSubpath, path)
		}

		dir, err := ctxDir.Directory(ctx, path)
		if err != nil {
			return inst, fmt.Errorf("failed to load contextual directory %q: %w", path, err)
		}

		loadedDir, err := dir.AsBlob(ctx, dag)
		if err != nil {
			return inst, fmt.Errorf("failed to get dir instance: %w", err)
		}

		if len(ignore) == 0 {
			return loadedDir, nil
		}

		var ignoredDir dagql.Instance[*Directory]

		err = dag.Select(ctx, dag.Root(), &ignoredDir,
			dagql.Selector{
				Field: "directory",
			},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "directory", Value: dagql.NewID[*Directory](loadedDir.ID())},
					{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(ignore...))},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to apply ignore pattern on contextual directory %q: %w", path, err)
		}

		return ignoredDir, nil
	default:
		return inst, fmt.Errorf("unsupported module src kind: %q", src.Kind)
	}
}

// ResolveContextPathFromModule returns the absolute path to the module's context directory
// based on the caller's host location.
// It's necessary to use this function and not `ResolveContextPathFromCaller` because
// the `SourceRootSubpath` is relative to the module root source dir and not the caller after module's
// initialization which may leads to invalid paths.
//
// For example, if the module is in a subdirectory (/root/ctx/mod), the `SourceRootSubpath` will be
// relative to the source module's root directory (./mod), but the path from the caller location would be `./ctx/mod`.
//
// This function returns both:
// - the path to the context directory (location of the .git or `dagger.json“ file if it doesn't exist)
// - the path to the source root directory (location of the `dagger.json` file)
//
// NOTE: this function is only valid for local module sources.
func (src *ModuleSource) ResolveContextPathFromModule(ctx context.Context) (contextRootAbsPath, moduleRootAbsPath string, err error) {
	if src.Kind != ModuleSourceKindLocal {
		return "", "", fmt.Errorf("cannot resolve non-local module source from caller")
	}

	relHostPath, err := src.SourceRootRelSubPath()
	if err != nil {
		return "", "", fmt.Errorf("failed to get source root subpath: %w", err)
	}

	bk, err := src.Query.Buildkit(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get buildkit client: %w", err)
	}

	sourceRootStat, err := bk.StatCallerHostPath(ctx, relHostPath, true)
	if err != nil {
		return "", "", fmt.Errorf("failed to stat source root: %w", err)
	}
	moduleRootAbsPath = sourceRootStat.Path

	contextAbsPath, contextFound, err := callerHostFindUpContext(ctx, bk, moduleRootAbsPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to find up root: %w", err)
	}

	if !contextFound {
		// default to restricting to the source root dir, make it abs though for consistency
		contextAbsPath = moduleRootAbsPath
	} else {
		// If context is found, we can create the module root path by joining the
		// context path with the module root subpath
		moduleRootAbsPath = filepath.Join(contextAbsPath, src.AsLocalSource.Value.RootSubpath)
	}

	return contextAbsPath, moduleRootAbsPath, nil
}

// resolveContextPaths returns the context path to the .git directory
// if it exists. Otherwise, it returns the source root directory.
func (src *ModuleSource) ResolveContextPathFromCaller(ctx context.Context) (contextRootAbsPath, sourceRootAbsPath string, _ error) {
	if src.Kind != ModuleSourceKindLocal {
		return "", "", fmt.Errorf("cannot resolve non-local module source from caller")
	}

	rootSubpath, err := src.SourceRootSubpath()
	if err != nil {
		return "", "", fmt.Errorf("failed to get source root subpath: %w", err)
	}

	bk, err := src.Query.Buildkit(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get buildkit client: %w", err)
	}
	sourceRootStat, err := bk.StatCallerHostPath(ctx, rootSubpath, true)
	if err != nil {
		return "", "", fmt.Errorf("failed to stat source root: %w", err)
	}
	sourceRootAbsPath = sourceRootStat.Path

	contextAbsPath, contextFound, err := callerHostFindUpContext(ctx, bk, sourceRootAbsPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to find up root: %w", err)
	}

	if !contextFound {
		// default to restricting to the source root dir, make it abs though for consistency
		contextAbsPath = sourceRootAbsPath
	}

	return contextAbsPath, sourceRootAbsPath, nil
}

// context path is the parent dir containing .git
func callerHostFindUpContext(
	ctx context.Context,
	bk *buildkit.Client,
	curDirPath string,
) (string, bool, error) {
	stat, err := bk.StatCallerHostPath(ctx, filepath.Join(curDirPath, ".git"), true)
	if err == nil {
		// NOTE: important that we use stat.Path here rather than curDirPath since the stat also
		// does some normalization of paths when the client is using case-insensitive filesystems
		return filepath.Dir(stat.Path), true, nil
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

func (src *ModuleSource) ContextDirectory() (inst dagql.Instance[*Directory], err error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		if !src.AsLocalSource.Valid {
			return inst, fmt.Errorf("local src not set")
		}
		if !src.AsLocalSource.Value.ContextDirectory.Valid {
			return inst, fmt.Errorf("local src context directory not set")
		}
		return src.AsLocalSource.Value.ContextDirectory.Value, nil
	case ModuleSourceKindGit:
		if !src.AsGitSource.Valid {
			return inst, fmt.Errorf("git src not set")
		}
		return src.AsGitSource.Value.ContextDirectory, nil
	default:
		return inst, fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) ModuleConfigWithUserFields(ctx context.Context) (*modules.ModuleConfigWithUserFields, bool, error) {
	contextDir, err := src.ContextDirectory()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get context directory: %w", err)
	}
	if contextDir.Self == nil {
		return nil, false, nil
	}

	rootSubpath, err := src.SourceRootSubpath()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get source root subpath: %w", err)
	}

	var modCfgWithUserFields modules.ModuleConfigWithUserFields
	configFile, err := contextDir.Self.File(ctx, filepath.Join(rootSubpath, modules.Filename))
	if err != nil {
		// no configuration for this module yet, so no name
		return nil, false, nil //nolint:nilerr
	}
	configBytes, err := configFile.Contents(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read module config file: %w", err)
	}

	if err := json.Unmarshal(configBytes, &modCfgWithUserFields); err != nil {
		return nil, false, fmt.Errorf("failed to decode module config: %w", err)
	}

	return &modCfgWithUserFields, true, nil
}

func (src *ModuleSource) ModuleConfig(ctx context.Context) (*modules.ModuleConfig, bool, error) {
	moduleConfigWithUserFields, exists, err := src.ModuleConfigWithUserFields(ctx)

	if moduleConfigWithUserFields != nil {
		return &moduleConfigWithUserFields.ModuleConfig, exists, err
	}
	return nil, exists, err
}

func (src *ModuleSource) Views(ctx context.Context) ([]*ModuleSourceView, error) {
	existingViews := map[string]int{}
	cfg, cfgExists, err := src.ModuleConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("module config: %w", err)
	}

	var views []*ModuleSourceView
	if cfgExists {
		for i, view := range cfg.Views {
			existingViews[view.Name] = i
			views = append(views, &ModuleSourceView{view})
		}
	}

	for _, view := range src.WithViews {
		if i, ok := existingViews[view.Name]; ok {
			views[i] = view
		} else {
			views = append(views, view)
		}
	}

	slices.SortFunc(views, func(a, b *ModuleSourceView) int {
		return strings.Compare(a.Name, b.Name)
	})
	return views, nil
}

func (src *ModuleSource) ViewByName(ctx context.Context, viewName string) (*ModuleSourceView, error) {
	for i := range src.WithViews {
		view := src.WithViews[len(src.WithViews)-1-i]
		if view.Name == viewName {
			return view, nil
		}
	}

	cfg, ok, err := src.ModuleConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("module config: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("module config not found")
	}

	for _, view := range cfg.Views {
		if view.Name == viewName {
			return &ModuleSourceView{view}, nil
		}
	}

	return nil, fmt.Errorf("view %q not found", viewName)
}

type LocalModuleSource struct {
	RootSubpath string `field:"true" doc:"The path to the root of the module source under the context directory. This directory contains its configuration file. It also contains its source code (possibly as a subdirectory)."`

	RelHostPath string `field:"true" doc:"The relative path to the module root from the host directory"`

	ContextDirectory dagql.Nullable[dagql.Instance[*Directory]] `field:"true" doc:"The directory containing everything needed to load load and use the module."`
}

func (src *LocalModuleSource) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LocalModuleSource",
		NonNull:   true,
	}
}

func (src *LocalModuleSource) TypeDescription() string {
	return "Module source that that originates from a path locally relative to an arbitrary directory."
}

func (src LocalModuleSource) Clone() *LocalModuleSource {
	cp := src

	if src.ContextDirectory.Valid {
		cp.ContextDirectory.Value.Self = cp.ContextDirectory.Value.Self.Clone()
	}

	return &cp
}

func (src *LocalModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	if !src.ContextDirectory.Valid {
		return nil, nil
	}
	return src.ContextDirectory.Value.Self.PBDefinitions(ctx)
}

func (src *LocalModuleSource) RefString() string {
	srcPath := src.RootSubpath
	if filepath.IsAbs(srcPath) {
		srcPath = strings.TrimPrefix(filepath.Clean(srcPath), "/")
	}
	return srcPath
}

func (src *LocalModuleSource) Symbolic() string {
	return src.RefString()
}

type SchemeType int

const (
	NoScheme SchemeType = iota
	SchemeHTTP
	SchemeHTTPS
	SchemeSSH
	SchemeSCPLike
)

func (s SchemeType) Prefix() string {
	switch s {
	case SchemeHTTP:
		return "http://"
	case SchemeHTTPS:
		return "https://"
	case SchemeSSH:
		return "ssh://"
	default:
		return ""
	}
}

func (s SchemeType) IsSSH() bool {
	return s == SchemeSSH
}

type GitModuleSource struct {
	Root        string `field:"true" doc:"The clean module name of the root of the module"`
	RootSubpath string `field:"true" doc:"The path to the root of the module source under the context directory. This directory contains its configuration file. It also contains its source code (possibly as a subdirectory)."`

	Version string `field:"true" doc:"The specified version of the git repo this source points to."`
	Commit  string `field:"true" doc:"The resolved commit of the git repo this source points to."`

	CloneRef string `field:"true" name:"cloneRef" doc:"The ref to clone the root of the git repo from"`

	HTMLRepoURL string `field:"true" name:"htmlRepoURL" doc:"The URL to access the web view of the repository (e.g., GitHub, GitLab, Bitbucket)"`

	ContextDirectory dagql.Instance[*Directory] `field:"true" doc:"The directory containing everything needed to load load and use the module."`
}

func (src *GitModuleSource) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitModuleSource",
		NonNull:   true,
	}
}

func (src *GitModuleSource) TypeDescription() string {
	return "Module source originating from a git repo."
}

func (src GitModuleSource) Clone() *GitModuleSource {
	cp := src
	if src.ContextDirectory.Self != nil {
		cp.ContextDirectory.Self = src.ContextDirectory.Self.Clone()
	}
	return &src
}

func (src *GitModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return src.ContextDirectory.Self.PBDefinitions(ctx)
}

func (src *GitModuleSource) RefString() string {
	refPath := src.CloneRef
	subPath := filepath.Join("/", src.RootSubpath)
	if subPath != "/" {
		refPath += subPath
	}
	if src.Version != "" {
		refPath += "@" + src.Version
	}
	return refPath
}

func (src *GitModuleSource) Pin() string {
	return src.Commit
}

func (src *GitModuleSource) Symbolic() string {
	// ignore error since ref is validated upon module initialization
	if src.RootSubpath == "" || src.RootSubpath == "/" {
		return src.CloneRef
	}
	return fmt.Sprintf("%s/%s", src.CloneRef, strings.TrimPrefix(src.RootSubpath, "/"))
}

func (src *GitModuleSource) HTMLURL() string {
	parsedURL, err := url.Parse(src.HTMLRepoURL)
	if err != nil {
		return src.HTMLRepoURL + path.Join("/src", src.Commit, src.RootSubpath)
	}

	switch parsedURL.Host {
	case "github.com", "gitlab.com":
		return src.HTMLRepoURL + path.Join("/tree", src.Commit, src.RootSubpath)
	case "dev.azure.com":
		if src.RootSubpath != "" {
			return fmt.Sprintf("%s/commit/%s?path=/%s", src.HTMLRepoURL, src.Commit, src.RootSubpath)
		}
		return src.HTMLRepoURL + path.Join("/commit", src.Commit)
	default:
		return src.HTMLRepoURL + path.Join("/src", src.Commit, src.RootSubpath)
	}
}

type ModuleSourceView struct {
	*modules.ModuleConfigView
}

func (v *ModuleSourceView) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSourceView",
		NonNull:   true,
	}
}

func (v *ModuleSourceView) TypeDescription() string {
	return "A named set of path filters that can be applied to directory arguments provided to functions."
}
