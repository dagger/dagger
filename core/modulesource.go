package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
)

type ModuleSourceKind string

var ModuleSourceKindEnum = dagql.NewEnum[ModuleSourceKind]()

var (
	ModuleSourceKindLocal = ModuleSourceKindEnum.Register("LOCAL_SOURCE")
	ModuleSourceKindGit   = ModuleSourceKindEnum.Register("GIT_SOURCE")
	ModuleSourceKindDir   = ModuleSourceKindEnum.Register("DIR_SOURCE")
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

func (cfg ModuleInitConfig) Clone() *ModuleInitConfig {
	return &cfg
}

type SDKConfig struct {
	Source string `field:"true" name:"source" doc:"Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation."`
}

func (*SDKConfig) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SDKConfig",
		NonNull:   false,
	}
}

func (*SDKConfig) TypeDescription() string {
	return "The SDK config of the module."
}

func (sdk SDKConfig) Clone() *SDKConfig {
	cp := sdk
	return &cp
}

// TODO: fix all doc strings
// TODO: fix all doc strings
// TODO: fix all doc strings
// TODO: fix all doc strings

type ModuleSource struct {
	Query *Query

	ConfigExists           bool   `field:"true" name:"configExists" doc:"TODO"`
	ModuleName             string `field:"true" name:"moduleName" doc:"TODO"`
	ModuleOriginalName     string `field:"true" name:"moduleOriginalName" doc:"TODO"`
	EngineVersion          string `field:"true" name:"engineVersion" doc:"TODO"`
	CodegenConfig          *modules.ModuleCodegenConfig
	InitConfig             *ModuleInitConfig
	ModuleConfigUserFields modules.ModuleConfigUserFields

	SDK     *SDKConfig `field:"true" name:"sdk" doc:"TODO"`
	SDKImpl SDK

	// IncludePaths are the includes as read from the module's dagger.json
	IncludePaths []string
	// FullIncludePaths are the include paths with the source root subpath prepended and implicit loads of dagger.json + source dir inculded
	FullIncludePaths []string

	// ConfigDependencies are the dependencies as read from the module's dagger.json
	ConfigDependencies []*modules.ModuleConfigDependency
	// Dependencies are the loaded sources for the module's dependencies
	Dependencies []dagql.Instance[*ModuleSource] `field:"true" name:"dependencies" doc:"TODO"`

	// SourceRootSubpath is the relative path from the context dir to the dir containing the module's dagger.json
	SourceRootSubpath string `field:"true" name:"sourceRootSubpath" doc:"TODO"`
	// SourceSubpath is the relative path from the context dir to the dir containing the module's source code
	SourceSubpath string

	ContextDirectory dagql.Instance[*Directory] `field:"true" name:"contextDirectory" doc:"TODO"`

	Digest string `field:"true" name:"digest" doc:"TODO"`

	// TODO: doc why explicitly not public fields, internal-only
	Kind  ModuleSourceKind `field:"true" name:"kind" doc:"TODO"`
	Local *LocalModuleSource
	Git   *GitModuleSource
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
	if src.Query != nil {
		src.Query = src.Query.Clone()
	}

	if src.CodegenConfig != nil {
		src.CodegenConfig = src.CodegenConfig.Clone()
	}

	if src.InitConfig != nil {
		src.InitConfig = src.InitConfig.Clone()
	}

	if src.SDK != nil {
		src.SDK = src.SDK.Clone()
	}

	origIncludePaths := src.IncludePaths
	src.IncludePaths = make([]string, len(origIncludePaths))
	copy(src.IncludePaths, origIncludePaths)
	origFullIncludePaths := src.FullIncludePaths
	src.FullIncludePaths = make([]string, len(origFullIncludePaths))
	copy(src.FullIncludePaths, origFullIncludePaths)

	origConfigDependencies := src.ConfigDependencies
	src.ConfigDependencies = make([]*modules.ModuleConfigDependency, len(origConfigDependencies))
	copy(src.ConfigDependencies, origConfigDependencies)
	origDependencies := src.Dependencies
	src.Dependencies = make([]dagql.Instance[*ModuleSource], len(origDependencies))
	for i, dep := range origDependencies {
		src.Dependencies[i] = dep
		src.Dependencies[i].Self = dep.Self.Clone()
	}

	if src.ContextDirectory.Self != nil {
		src.ContextDirectory.Self = src.ContextDirectory.Self.Clone()
	}

	if src.Local != nil {
		src.Local = src.Local.Clone()
	}

	if src.Git != nil {
		src.Git = src.Git.Clone()
	}

	return &src
}

// TODO: looks weird, but works
func (src *ModuleSource) Evaluate(context.Context) (*buildkit.Result, error) {
	return nil, nil
}

func (src *ModuleSource) AsString() string {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return src.Local.OriginalRefString

	case ModuleSourceKindGit:
		return GitRefString(src.Git.CloneRef, src.SourceRootSubpath, src.Git.Version)
	default:
		return ""
	}
}

func GitRefString(cloneRef, sourceRootSubpath, version string) string {
	refPath := cloneRef
	subPath := filepath.Join("/", sourceRootSubpath)
	if subPath != "/" {
		refPath += subPath
	}
	if version != "" {
		refPath += "@" + version
	}
	return refPath
}

func (src *ModuleSource) Pin() string {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return ""
	case ModuleSourceKindGit:
		return src.Git.Pin
	default:
		return ""
	}
}

func (src *ModuleSource) LoadContext(
	ctx context.Context,
	dag *dagql.Server,
	path string,
	ignore []string,
) (inst dagql.Instance[*Directory], err error) {
	bk, err := src.Query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit api: %w", err)
	}

	switch src.Kind {
	case ModuleSourceKindLocal:
		localSourceClientMetadata, err := src.Query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get client metadata: %w", err)
		}
		localSourceCtx := engine.ContextWithClientMetadata(ctx, localSourceClientMetadata)

		// Retrieve the absolute path to the context directory (.git or dagger.json)
		// and the module root directory (dagger.json)
		ctxPath := src.Local.ContextDirectoryPath
		modPath := filepath.Join(ctxPath, src.SourceRootSubpath)

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

		err = dag.Select(localSourceCtx, dag.Root(), &inst,
			dagql.Selector{
				Field: "host",
			},
			dagql.Selector{
				Field: "directory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(path)},
					{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(ignore...))},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to select directory: %w", err)
		}

		return inst, nil

	case ModuleSourceKindGit:
		slog.Debug("moduleSource.LoadContext: loading contextual directory from git", "path", path, "kind", src.Kind, "repo", src.Git.HTMLURL)

		if !filepath.IsAbs(path) {
			path = filepath.Join("/", src.SourceRootSubpath, path)
		}

		// Use the Git context directory without dagger.json includes applied.
		ctxDir := src.Git.UnfilteredContextDir

		if path != "/" {
			if err := dag.Select(ctx, ctxDir, &ctxDir,
				dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(path)},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		if len(ignore) > 0 {
			if err := dag.Select(ctx, dag.Root(), &ctxDir,
				dagql.Selector{
					Field: "directory",
				},
				dagql.Selector{
					Field: "withDirectory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String("/")},
						{Name: "directory", Value: dagql.NewID[*Directory](ctxDir.ID())},
						{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(ignore...))},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		return MakeDirectoryContentHashed(ctx, bk, ctxDir)

	case ModuleSourceKindDir:
		// TODO: 99% duped with above
		if !filepath.IsAbs(path) {
			path = filepath.Join("/", src.SourceRootSubpath, path)
		}

		// Use the Dir context directory.
		ctxDir := src.ContextDirectory

		if path != "/" {
			if err := dag.Select(ctx, ctxDir, &ctxDir,
				dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(path)},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		if len(ignore) > 0 {
			if err := dag.Select(ctx, dag.Root(), &ctxDir,
				dagql.Selector{
					Field: "directory",
				},
				dagql.Selector{
					Field: "withDirectory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String("/")},
						{Name: "directory", Value: dagql.NewID[*Directory](ctxDir.ID())},
						{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(ignore...))},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		mainClientCallerID, err := src.Query.MainClientCallerID(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to retrieve mainClientCallerID: %w", err)
		}
		if err := src.Query.AddClientResourcesFromID(ctx, &resource.ID{ID: *ctxDir.ID()}, mainClientCallerID, false); err != nil {
			return inst, fmt.Errorf("failed to add client resources from ID: %w", err)
		}

		return MakeDirectoryContentHashed(ctx, bk, ctxDir)

	default:
		return inst, fmt.Errorf("unsupported module src kind: %q", src.Kind)
	}
}

type LocalModuleSource struct {
	ContextDirectoryPath string
	// the user-provided path given to moduleSource, used in AsString
	OriginalRefString string
}

func (src LocalModuleSource) Clone() *LocalModuleSource {
	return &src
}

type GitModuleSource struct {
	CloneRef string

	// TODO: why do these both exist?
	HTMLRepoURL string
	HTMLURL     string

	Version string

	// TODO: pretty sure these are always equal, maybe dedupe
	Commit string
	Pin    string

	// TODO: doc, needed for contextual dir args
	UnfilteredContextDir dagql.Instance[*Directory]
}

func (src GitModuleSource) Clone() *GitModuleSource {
	return &src
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
