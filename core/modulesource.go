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
	Kind ModuleSourceKind `field:"true" name:"kind" doc:"The kind of source (e.g. local, git, etc.)"`

	AsLocalSource dagql.Nullable[*LocalModuleSource] `field:"true" doc:"If the source is of kind local, the local source representation of it."`
	AsGitSource   dagql.Nullable[*GitModuleSource]   `field:"true" doc:"If the source is a of kind git, the git source representation of it."`

	// The root directory that was loaded for this module source, with any re-rooting
	// based on the discovered path to the actual module configuration file.
	RootDirectory dagql.Instance[*Directory] `field:"true" doc:"The root directory of the module source that contains its configuration and source code (which may be in a subdirectory of this root)."`
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
	return src.RootDirectory.Self.PBDefinitions(ctx)
}

func (src ModuleSource) Clone() *ModuleSource {
	cp := src

	if src.AsLocalSource.Valid {
		cp.AsLocalSource.Value = src.AsLocalSource.Value.Clone()
	}

	if src.AsGitSource.Valid {
		cp.AsGitSource.Value = src.AsGitSource.Value.Clone()
	}

	if src.RootDirectory.Self != nil {
		cp.RootDirectory.Self = src.RootDirectory.Self.Clone()
	}

	return &cp
}

func (src *ModuleSource) Subpath() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		if !src.AsLocalSource.Valid {
			return "", fmt.Errorf("local src not set")
		}
		return src.AsLocalSource.Value.Subpath, nil

	case ModuleSourceKindGit:
		if !src.AsGitSource.Valid {
			return "", fmt.Errorf("git src not set")
		}
		return src.AsGitSource.Value.Subpath, nil

	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) RefString() (string, error) {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return src.AsLocalSource.Value.String()
	case ModuleSourceKindGit:
		return src.AsGitSource.Value.String(), nil
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
		return src.AsLocalSource.Value.Symbolic()

	case ModuleSourceKindGit:
		if !src.AsGitSource.Valid {
			return "", fmt.Errorf("git src not set")
		}
		return src.AsGitSource.Value.Symbolic(), nil

	default:
		return "", fmt.Errorf("unknown module src kind: %q", src.Kind)
	}
}

func (src *ModuleSource) ModuleName(ctx context.Context) (string, error) {
	if src.RootDirectory.Self == nil {
		return "", nil
	}

	sourceSubpath, err := src.Subpath()
	if err != nil {
		return "", fmt.Errorf("failed to get module source path: %w", err)
	}

	var modCfg modules.ModuleConfig
	configFile, err := src.RootDirectory.Self.File(ctx, filepath.Join("/", sourceSubpath, modules.Filename))
	if err != nil {
		// no configuration for this module yet, so no name
		return "", nil
	}
	configBytes, err := configFile.Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to read module config file: %w", err)
	}

	if err := json.Unmarshal(configBytes, &modCfg); err != nil {
		return "", fmt.Errorf("failed to decode module config: %w", err)
	}
	if err := modCfg.Validate(); err != nil {
		return "", fmt.Errorf("invalid module config: %w", err)
	}

	return modCfg.Name, nil
}

type LocalModuleSource struct {
	Subpath string `field:"true" name:"sourceSubpath" doc:"The path to the module source code dir specified by this source."`
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
	return &src
}

func (src *LocalModuleSource) String() (string, error) {
	srcPath := src.Subpath
	if filepath.IsAbs(srcPath) {
		var err error
		srcPath, err = filepath.Rel("/", srcPath)
		if err != nil {
			return "", fmt.Errorf("get relative path: %w", err)
		}
	}
	return srcPath, nil
}

func (src *LocalModuleSource) Symbolic() (string, error) {
	return src.String()
}

type GitModuleSource struct {
	Subpath string `field:"true" name:"sourceSubpath" doc:"The path to the module source code dir specified by this source relative to the source's root directory."`

	Version string `field:"true" doc:"The specified version of the git repo this source points to."`

	Commit string `field:"true" doc:"The resolved commit of the git repo this source points to."`

	URLParent string
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

func (src *GitModuleSource) String() string {
	return fmt.Sprintf("%s/%s@%s", src.URLParent, src.Subpath, src.Version)
}

func (src *GitModuleSource) Symbolic() string {
	return filepath.Join(src.CloneURL(), src.Subpath)
}

func (src *GitModuleSource) CloneURL() string {
	return "https://" + src.URLParent
}

func (src *GitModuleSource) HTMLURL() string {
	return "https://" + src.URLParent + "/tree" + src.Version + "/" + src.Subpath
}
