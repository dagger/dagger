package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
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

func (proto ModuleSourceKind) ToLiteral() *idproto.Literal {
	return ModuleSourceKindEnum.Literal(proto)
}

type ModuleSource struct {
	Query *Query

	Kind ModuleSourceKind `field:"true" name:"kind" doc:"The kind of source (e.g. local, git, etc.)"`

	RootSubpath string `field:"true" doc:"The path to the root of the module source under the context directory. This directory contains its configuration file. It also contains its source code (possibly as a subdirectory)."`

	// TODO: doc
	WithName         string
	WithDependencies []dagql.Instance[*ModuleDependency]
	WithSDK          string

	// the unmodifiied directory the source was was loaded from
	BaseContextDirectory dagql.Instance[*Directory]
	// the context directory plus any modifications made to config files/codegen
	GeneratedContextDirectory dagql.Instance[*Directory]

	AsLocalSource dagql.Nullable[*LocalModuleSource] `field:"true" doc:"If the source is of kind local, the local source representation of it."`

	AsGitSource dagql.Nullable[*GitModuleSource] `field:"true" doc:"If the source is a of kind git, the git source representation of it."`
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

var _ HasPBDefinitions = (*ModuleSource)(nil)

func (src *ModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var defs []*pb.Definition
	baseDefs, err := src.BaseContextDirectory.Self.PBDefinitions(ctx)
	if err != nil {
		return nil, fmt.Errorf("base context directory: %w", err)
	}
	defs = append(defs, baseDefs...)
	genDefs, err := src.GeneratedContextDirectory.Self.PBDefinitions(ctx)
	if err != nil {
		return nil, fmt.Errorf("generated context directory: %w", err)
	}
	defs = append(defs, genDefs...)
	return defs, nil
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

	if src.BaseContextDirectory.Self != nil {
		cp.BaseContextDirectory.Self = src.BaseContextDirectory.Self.Clone()
	}

	if src.GeneratedContextDirectory.Self != nil {
		cp.GeneratedContextDirectory.Self = src.GeneratedContextDirectory.Self.Clone()
	}

	if src.WithDependencies != nil {
		cp.WithDependencies = make([]dagql.Instance[*ModuleDependency], len(src.WithDependencies))
		copy(cp.WithDependencies, src.WithDependencies)
	}

	return &cp
}

func (src *ModuleSource) RefString() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		srcPath := src.RootSubpath
		if filepath.IsAbs(srcPath) {
			var err error
			srcPath, err = filepath.Rel("/", srcPath)
			if err != nil {
				return "", fmt.Errorf("get relative path: %w", err)
			}
		}
		return srcPath, nil
	case ModuleSourceKindGit:
		gitSrc := src.AsGitSource.Value
		refPath := gitSrc.URLParent
		if src.RootSubpath != "/" {
			refPath += src.RootSubpath
		}
		return fmt.Sprintf("%s@%s", refPath, gitSrc.Commit), nil
	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) Symbolic() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		if !src.AsLocalSource.Valid {
			return "", fmt.Errorf("local src not set")
		}
		return src.RefString()

	case ModuleSourceKindGit:
		if !src.AsGitSource.Valid {
			return "", fmt.Errorf("git src not set")
		}
		gitSrc := src.AsGitSource.Value
		return filepath.Join(gitSrc.CloneURL(), src.RootSubpath), nil

	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) ContextDirectory() (inst dagql.Instance[*Directory], err error) {
	if src.GeneratedContextDirectory.Self != nil {
		return src.GeneratedContextDirectory, nil
	}
	if src.BaseContextDirectory.Self == nil {
		return inst, fmt.Errorf("base context directory not set")
	}
	return src.BaseContextDirectory, nil
}

func (src *ModuleSource) ModuleSourceSubpath(ctx context.Context) (string, error) {
	cfg, ok, err := src.ModuleConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("module config: %w", err)
	}
	if !ok {
		// default to the root subpath for unintialized modules
		return src.RootSubpath, nil
	}

	return filepath.Join(src.RootSubpath, cfg.Source), nil
}

func (src *ModuleSource) ModuleSourceRelSubpath(ctx context.Context) (string, error) {
	sourceSubpath, err := src.ModuleSourceSubpath(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get module source subpath: %w", err)
	}
	return filepath.Rel(src.RootSubpath, sourceSubpath)
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
	cfg, ok, err := src.OriginalModuleConfig(ctx)
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

func (src *ModuleSource) ModuleConfig(ctx context.Context) (*modules.ModuleConfig, bool, error) {
	contextDir := src.GeneratedContextDirectory.Self
	if contextDir == nil {
		contextDir = src.BaseContextDirectory.Self
	}
	return src.moduleConfigFromContext(ctx, contextDir)
}

func (src *ModuleSource) OriginalModuleConfig(ctx context.Context) (*modules.ModuleConfig, bool, error) {
	return src.moduleConfigFromContext(ctx, src.BaseContextDirectory.Self)
}

func (src *ModuleSource) moduleConfigFromContext(ctx context.Context, contextDir *Directory) (*modules.ModuleConfig, bool, error) {
	if contextDir == nil {
		return nil, false, nil
	}

	var modCfg modules.ModuleConfig
	configFile, err := contextDir.File(ctx, filepath.Join(src.RootSubpath, modules.Filename))
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

type LocalModuleSource struct {
	Query *Query
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

	if src.Query != nil {
		cp.Query = src.Query.Clone()
	}

	return &cp
}

type GitModuleSource struct {
	Version string `field:"true" doc:"The specified version of the git repo this source points to."`

	Commit string `field:"true" doc:"The resolved commit of the git repo this source points to."`

	URLParent string

	RootSubpath string
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
	return &src
}

func (src *GitModuleSource) CloneURL() string {
	return "https://" + src.URLParent
}

func (src *GitModuleSource) HTMLURL() string {
	return "https://" + src.URLParent + "/tree/" + src.Commit + src.RootSubpath
}
