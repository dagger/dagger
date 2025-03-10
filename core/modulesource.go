package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
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

func (proto ModuleSourceKind) HumanString() string {
	switch proto {
	case ModuleSourceKindLocal:
		return "local"
	case ModuleSourceKindGit:
		return "git"
	case ModuleSourceKindDir:
		return "directory"
	default:
		return string(proto)
	}
}

type SDKConfig struct {
	Source string                 `field:"true" name:"source" doc:"Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation."`
	Config map[string]interface{} `name:"config" doc:"sdk specific config"`
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

type ModuleSource struct {
	Query *Query

	ConfigExists           bool   `field:"true" name:"configExists" doc:"Whether an existing dagger.json for the module was found."`
	ModuleName             string `field:"true" name:"moduleName" doc:"The name of the module, including any setting via the withName API."`
	ModuleOriginalName     string `field:"true" name:"moduleOriginalName" doc:"The original name of the module as read from the module's dagger.json (or set for the first time with the withName API)."`
	EngineVersion          string `field:"true" name:"engineVersion" doc:"The engine version of the module."`
	CodegenConfig          *modules.ModuleCodegenConfig
	ModuleConfigUserFields modules.ModuleConfigUserFields

	// The SDK configuration of the module as read from the module's dagger.json or set by withSDK
	SDK *SDKConfig `field:"true" name:"sdk" doc:"The SDK configuration of the module."`
	// The implementation of the SDK with codegen and related operations. Reloaded when SDK changes.
	SDKImpl SDK

	// IncludePaths are the includes as read from the module's dagger.json
	IncludePaths []string
	// RebasedIncludePaths are the include paths with the source root subpath prepended
	RebasedIncludePaths []string

	// ConfigDependencies are the dependencies as read from the module's dagger.json
	// NOTE: this is currently not updated by withDependencies and related APIs, only Dependencies will be updated
	ConfigDependencies []*modules.ModuleConfigDependency
	// Dependencies are the loaded sources for the module's dependencies
	Dependencies []dagql.Instance[*ModuleSource] `field:"true" name:"dependencies" doc:"The dependencies of the module source."`

	// Clients are the clients generated for the module.
	ConfigClients []*modules.ModuleConfigClient `field:"true" name:"configClients" doc:"The clients generated for the module."`

	// SourceRootSubpath is the relative path from the context dir to the dir containing the module's dagger.json
	SourceRootSubpath string `field:"true" name:"sourceRootSubpath" doc:"The path, relative to the context directory, that contains the module's dagger.json."`
	// SourceSubpath is the relative path from the context dir to the dir containing the module's source code
	SourceSubpath string

	OriginalSubpath string

	ContextDirectory dagql.Instance[*Directory] `field:"true" name:"contextDirectory" doc:"The full directory loaded for the module source, including the source code as a subdirectory."`

	Digest string `field:"true" name:"digest" doc:"A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance."`

	Kind  ModuleSourceKind `field:"true" name:"kind" doc:"The kind of module source (currently local, git or dir)."`
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

	if src.SDK != nil {
		src.SDK = src.SDK.Clone()
	}

	origIncludePaths := src.IncludePaths
	src.IncludePaths = make([]string, len(origIncludePaths))
	copy(src.IncludePaths, origIncludePaths)
	origFullIncludePaths := src.RebasedIncludePaths
	src.RebasedIncludePaths = make([]string, len(origFullIncludePaths))
	copy(src.RebasedIncludePaths, origFullIncludePaths)

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

	oriConfigClients := src.ConfigClients
	src.ConfigClients = make([]*modules.ModuleConfigClient, len(oriConfigClients))
	copy(src.ConfigClients, oriConfigClients)

	return &src
}

func (src *ModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var pbDefs []*pb.Definition
	if src.ContextDirectory.Self != nil {
		defs, err := src.ContextDirectory.Self.PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		pbDefs = append(pbDefs, defs...)
	}
	for _, dep := range src.Dependencies {
		if dep.Self == nil {
			continue
		}
		defs, err := dep.Self.PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		pbDefs = append(pbDefs, defs...)
	}
	return pbDefs, nil
}

func (src *ModuleSource) Evaluate(context.Context) (*buildkit.Result, error) {
	return nil, nil
}

func (src *ModuleSource) AsString() string {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return filepath.Join(src.Local.ContextDirectoryPath, src.SourceRootSubpath)

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

// we mix this into digest hashes to ensure they don't accidentally collide
// with any others
const moduleSourceHashMix = "moduleSource"

// CalcDigest calculates a content-hash of the module source. It is used during codegen; two module
// sources with the same digest will share cache for codegen-related calls.
func (src *ModuleSource) CalcDigest() digest.Digest {
	inputs := []string{
		moduleSourceHashMix,
		src.ModuleOriginalName,
		src.SourceRootSubpath,
		src.SourceSubpath,
		src.ContextDirectory.ID().Digest().String(),
	}

	if src.SDK != nil {
		inputs = append(inputs, src.SDK.Source)
	}

	inputs = append(inputs, src.IncludePaths...)

	for _, dep := range src.Dependencies {
		if dep.Self == nil {
			continue
		}
		inputs = append(inputs, dep.Self.Digest)
	}

	for _, client := range src.ConfigClients {
		inputs = append(inputs, client.Generator, client.Directory)
		if client.Dev != nil {
			inputs = append(inputs, fmt.Sprintf("%t", *client.Dev))
		}
	}

	return dagql.HashFrom(inputs...)
}

// LoadContext loads addition files+directories from the module source's context, including those that
// may have not been included in the original module source load.
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

		inst, err = MakeDirectoryContentHashed(ctx, bk, ctxDir)
		if err != nil {
			return inst, err
		}

	case ModuleSourceKindDir:
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

		inst, err = MakeDirectoryContentHashed(ctx, bk, ctxDir)
		if err != nil {
			return inst, err
		}

	default:
		return inst, fmt.Errorf("unsupported module src kind: %q", src.Kind)
	}

	mainClientMetadata, err := src.Query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}
	if err := src.Query.AddClientResourcesFromID(ctx, &resource.ID{ID: *inst.ID()}, mainClientMetadata.ClientID, false); err != nil {
		return inst, fmt.Errorf("failed to add client resources from directory source: %w", err)
	}

	return inst, nil
}

type LocalModuleSource struct {
	ContextDirectoryPath string
}

func (src LocalModuleSource) Clone() *LocalModuleSource {
	return &src
}

type GitModuleSource struct {
	// The ref to clone the root of the git repo from
	CloneRef string

	// Symbolic is the CloneRef plus the SourceRootSubpath (no version)
	Symbolic string

	// The URL to the source's git repo in a web browser, at the root of the repo
	HTMLRepoURL string

	// The URL to the source's git repo in a web browser, including to the source root subpath
	HTMLURL string

	// The import path corresponding to the root of the git repo this source points to
	RepoRootPath string

	// The version of the source; may be a branch, tag, or commit hash
	Version string

	// The resolved commit hash of the source
	Commit string
	Pin    string

	// The full git repo for the module source without any include filtering
	UnfilteredContextDir dagql.Instance[*Directory]
}

func (src GitModuleSource) Clone() *GitModuleSource {
	if src.UnfilteredContextDir.Self != nil {
		src.UnfilteredContextDir.Self = src.UnfilteredContextDir.Self.Clone()
	}
	return &src
}

func (src *GitModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	if src.UnfilteredContextDir.Self == nil {
		return nil, nil
	}
	return src.UnfilteredContextDir.Self.PBDefinitions(ctx)
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
