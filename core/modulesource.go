package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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

type ModuleSource struct {
	Query *Query

	Kind ModuleSourceKind `field:"true" name:"kind" doc:"The kind of source (e.g. local, git, etc.)"`

	AsLocalSource dagql.Nullable[*LocalModuleSource] `field:"true" doc:"If the source is of kind local, the local source representation of it."`

	AsGitSource dagql.Nullable[*GitModuleSource] `field:"true" doc:"If the source is a of kind git, the git source representation of it."`

	// Settings that can be used to initialize or override the source's configuration
	WithName          string
	WithDependencies  []dagql.Instance[*ModuleDependency]
	WithSDK           string
	WithSourceSubpath string
	WithViews         []*ModuleSourceView
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

func (src *ModuleSource) ModuleConfig(ctx context.Context) (*modules.ModuleConfig, bool, error) {
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

	var modCfg modules.ModuleConfig
	configFile, err := contextDir.Self.File(ctx, filepath.Join(rootSubpath, modules.Filename))
	if err != nil {
		// no configuration for this module yet, so no name
		return nil, false, nil
	}
	configBytes, err := configFile.Contents(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read module config file: %w", err)
	}

	if err := json.Unmarshal(configBytes, &modCfg); err != nil {
		return nil, false, fmt.Errorf("failed to decode module config: %w", err)
	}

	return &modCfg, true, nil
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

type GitModuleSource struct {
	Root        string `field:"true" doc:"The clean module name of the root of the module"`
	RootSubpath string `field:"true" doc:"The path to the root of the module source under the context directory. This directory contains its configuration file. It also contains its source code (possibly as a subdirectory)."`

	Version string `field:"true" doc:"The specified version of the git repo this source points to."`
	Commit  string `field:"true" doc:"The resolved commit of the git repo this source points to."`

	CloneURL string `field:"true" doc:"The URL to clone the root of the git repo from"`

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
	refPath := src.Root
	subPath := filepath.Join("/", src.RootSubpath)
	if subPath != "/" {
		refPath += subPath
	}
	return fmt.Sprintf("%s@%s", refPath, src.Commit)
}

func (src *GitModuleSource) Symbolic() string {
	// ignore error since ref is validated upon module initialization
	p, _ := url.JoinPath(src.CloneURL, src.RootSubpath)
	return p
}

func (src *GitModuleSource) HTMLURL() string {
	u := "https://" + src.Root + "/tree/" + src.Commit
	if subPath := src.RootSubpath; subPath != "" {
		u += "/" + subPath
	}
	return u
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
