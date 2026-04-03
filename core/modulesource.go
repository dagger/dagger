package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/slog"
)

type ModuleSourceKind string

var ModuleSourceKindEnum = dagql.NewEnum[ModuleSourceKind]()

var (
	ModuleSourceKindLocal = ModuleSourceKindEnum.Register("LOCAL_SOURCE")
	_                     = ModuleSourceKindEnum.AliasView("LOCAL", "LOCAL_SOURCE", enumView)
	ModuleSourceKindGit   = ModuleSourceKindEnum.Register("GIT_SOURCE")
	_                     = ModuleSourceKindEnum.AliasView("GIT", "GIT_SOURCE", enumView)
	ModuleSourceKindDir   = ModuleSourceKindEnum.Register("DIR_SOURCE")
	_                     = ModuleSourceKindEnum.AliasView("DIR", "DIR_SOURCE", enumView)
)

func (proto ModuleSourceKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSourceKind",
		NonNull:   true,
	}
}

// ModuleRelationType distinguishes between dependencies and toolchains in error messages and field access
type ModuleRelationType int

const (
	ModuleRelationTypeDependency ModuleRelationType = iota
	ModuleRelationTypeToolchain
)

func (t ModuleRelationType) String() string {
	switch t {
	case ModuleRelationTypeDependency:
		return "dependency"
	case ModuleRelationTypeToolchain:
		return "toolchain"
	default:
		return "unknown"
	}
}

func (t ModuleRelationType) Plural() string {
	switch t {
	case ModuleRelationTypeDependency:
		return "dependencies"
	case ModuleRelationTypeToolchain:
		return "toolchains"
	default:
		return "unknowns"
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
	Source       string `field:"true" name:"source" doc:"Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation."`
	Debug        bool   `field:"true" name:"debug" doc:"Whether to start the SDK runtime in debug mode with an interactive terminal."`
	Config       map[string]any
	Experimental map[string]bool
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

func (sdk *SDKConfig) ExperimentalFeatureEnabled(feature ModuleSourceExperimentalFeature) bool {
	if sdk.Experimental == nil {
		return false
	}
	return sdk.Experimental[feature.String()]
}

type ModuleSource struct {
	ConfigExists                  bool   `field:"true" name:"configExists" doc:"Whether an existing dagger.json for the module was found."`
	ModuleName                    string `field:"true" name:"moduleName" doc:"The name of the module, including any setting via the withName API."`
	ModuleOriginalName            string `field:"true" name:"moduleOriginalName" doc:"The original name of the module as read from the module's dagger.json (or set for the first time with the withName API)."`
	EngineVersion                 string `field:"true" name:"engineVersion" doc:"The engine version of the module."`
	CodegenConfig                 *modules.ModuleCodegenConfig
	ModuleConfigUserFields        modules.ModuleConfigUserFields
	DisableDefaultFunctionCaching bool

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
	Dependencies dagql.ObjectResultArray[*ModuleSource] `field:"true" name:"dependencies" doc:"The dependencies of the module source."`

	// Blueprint (from `dagger init --blueprint`)
	ConfigBlueprint *modules.ModuleConfigDependency
	Blueprint       dagql.ObjectResult[*ModuleSource] `field:"true" name:"blueprint" doc:"The blueprint referenced by the module source."`

	// Toolchains (from `dagger toolchain install`)
	ConfigToolchains []*modules.ModuleConfigDependency
	Toolchains       dagql.ObjectResultArray[*ModuleSource] `field:"true" name:"toolchains" doc:"The toolchains referenced by the module source."`

	// Internal-only projection metadata used by schema helpers to load this
	// module source as a toolchain in the context of a parent module source.
	ToolchainContextSource dagql.Nullable[dagql.ObjectResult[*ModuleSource]]
	ToolchainConfigIndex   int
	ToolchainProjection    bool

	UserDefaults *EnvFile `field:"true" name:"userDefaults" doc:"User-defined defaults read from local .env files"`
	// Clients are the clients generated for the module.
	ConfigClients []*modules.ModuleConfigClient `field:"true" name:"configClients" doc:"The clients generated for the module."`

	// SourceRootSubpath is the relative path from the context dir to the dir containing the module's dagger.json
	SourceRootSubpath string `field:"true" name:"sourceRootSubpath" doc:"The path, relative to the context directory, that contains the module's dagger.json."`
	// SourceSubpath is the relative path from the context dir to the dir containing the module's source code
	SourceSubpath string

	OriginalSubpath string

	ContextDirectory dagql.ObjectResult[*Directory] `field:"true" name:"contextDirectory" doc:"The full directory loaded for the module source, including the source code as a subdirectory."`

	Kind   ModuleSourceKind `field:"true" name:"kind" doc:"The kind of module source (currently local, git or dir)."`
	Local  *LocalModuleSource
	Git    *GitModuleSource
	DirSrc *DirModuleSource

	persistedResultID uint64
}

var moduleSourceSDKLoader func(context.Context, *Query, *SDKConfig, *ModuleSource) (SDK, error)

func SetModuleSourceSDKLoader(loader func(context.Context, *Query, *SDKConfig, *ModuleSource) (SDK, error)) {
	moduleSourceSDKLoader = loader
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

func (src *ModuleSource) PersistedResultID() uint64 {
	if src == nil {
		return 0
	}
	return src.persistedResultID
}

func (src *ModuleSource) SetPersistedResultID(resultID uint64) {
	if src != nil {
		src.persistedResultID = resultID
	}
}

var _ dagql.HasDependencyResults = (*ModuleSource)(nil)
var _ dagql.PersistedObject = (*ModuleSource)(nil)
var _ dagql.PersistedObjectDecoder = (*ModuleSource)(nil)

func (src ModuleSource) Clone() *ModuleSource {
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
	src.Dependencies = make([]dagql.ObjectResult[*ModuleSource], len(origDependencies))
	copy(src.Dependencies, origDependencies)

	origConfigToolchains := src.ConfigToolchains
	src.ConfigToolchains = make([]*modules.ModuleConfigDependency, len(origConfigToolchains))
	copy(src.ConfigToolchains, origConfigToolchains)
	origToolchains := src.Toolchains
	src.Toolchains = make([]dagql.ObjectResult[*ModuleSource], len(origToolchains))
	copy(src.Toolchains, origToolchains)

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

func (src *ModuleSource) Evaluate(context.Context) error {
	return nil
}

func (src *ModuleSource) Sync(ctx context.Context) error {
	return src.Evaluate(ctx)
}

func (src *ModuleSource) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if src == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 4+len(src.Dependencies)+len(src.Toolchains))

	if src.ContextDirectory.Self() != nil {
		attached, err := attach(src.ContextDirectory)
		if err != nil {
			return nil, fmt.Errorf("attach module source context directory: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach module source context directory: unexpected result %T", attached)
		}
		src.ContextDirectory = typed
		owned = append(owned, typed)
	}

	for i, dep := range src.Dependencies {
		if dep.Self() == nil {
			continue
		}
		attached, err := attach(dep)
		if err != nil {
			return nil, fmt.Errorf("attach module source dependency %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ModuleSource])
		if !ok {
			return nil, fmt.Errorf("attach module source dependency %d: unexpected result %T", i, attached)
		}
		src.Dependencies[i] = typed
		owned = append(owned, typed)
	}

	if src.Blueprint.Self() != nil {
		attached, err := attach(src.Blueprint)
		if err != nil {
			return nil, fmt.Errorf("attach module source blueprint: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ModuleSource])
		if !ok {
			return nil, fmt.Errorf("attach module source blueprint: unexpected result %T", attached)
		}
		src.Blueprint = typed
		owned = append(owned, typed)
	}

	for i, toolchain := range src.Toolchains {
		if toolchain.Self() == nil {
			continue
		}
		attached, err := attach(toolchain)
		if err != nil {
			return nil, fmt.Errorf("attach module source toolchain %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ModuleSource])
		if !ok {
			return nil, fmt.Errorf("attach module source toolchain %d: unexpected result %T", i, attached)
		}
		src.Toolchains[i] = typed
		owned = append(owned, typed)
	}

	if src.ToolchainContextSource.Valid && src.ToolchainContextSource.Value.Self() != nil {
		attached, err := attach(src.ToolchainContextSource.Value)
		if err != nil {
			return nil, fmt.Errorf("attach module source toolchain context source: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ModuleSource])
		if !ok {
			return nil, fmt.Errorf("attach module source toolchain context source: unexpected result %T", attached)
		}
		src.ToolchainContextSource = dagql.NonNull(typed)
	}

	if src.Git != nil && src.Git.UnfilteredContextDir.Self() != nil {
		attached, err := attach(src.Git.UnfilteredContextDir)
		if err != nil {
			return nil, fmt.Errorf("attach module source git unfiltered context dir: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach module source git unfiltered context dir: unexpected result %T", attached)
		}
		src.Git.UnfilteredContextDir = typed
		owned = append(owned, typed)
	}

	if src.DirSrc != nil && src.DirSrc.OriginalContextDir.Self() != nil {
		attached, err := attach(src.DirSrc.OriginalContextDir)
		if err != nil {
			return nil, fmt.Errorf("attach module source dir original context dir: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach module source dir original context dir: unexpected result %T", attached)
		}
		src.DirSrc.OriginalContextDir = typed
		owned = append(owned, typed)
	}

	return owned, nil
}

type persistedGitModuleSourcePayload struct {
	CloneRef     string `json:"cloneRef,omitempty"`
	Symbolic     string `json:"symbolic,omitempty"`
	HTMLRepoURL  string `json:"htmlRepoURL,omitempty"`
	HTMLURL      string `json:"htmlURL,omitempty"`
	RepoRootPath string `json:"repoRootPath,omitempty"`
	Version      string `json:"version,omitempty"`
	Commit       string `json:"commit,omitempty"`
	Ref          string `json:"ref,omitempty"`
}

type persistedDirModuleSourcePayload struct {
	OriginalSourceRootSubpath  string `json:"originalSourceRootSubpath,omitempty"`
	OriginalContextDirResultID uint64 `json:"originalContextDirResultID,omitempty"`
}

type persistedModuleSourceSDKCapabilities struct {
	Runtime         bool `json:"runtime,omitempty"`
	ModuleTypes     bool `json:"moduleTypes,omitempty"`
	CodeGenerator   bool `json:"codeGenerator,omitempty"`
	ClientGenerator bool `json:"clientGenerator,omitempty"`
}

type persistedModuleSourcePayload struct {
	ConfigExists                    bool                                  `json:"configExists,omitempty"`
	ModuleName                      string                                `json:"moduleName,omitempty"`
	ModuleOriginalName              string                                `json:"moduleOriginalName,omitempty"`
	EngineVersion                   string                                `json:"engineVersion,omitempty"`
	CodegenConfig                   *modules.ModuleCodegenConfig          `json:"codegenConfig,omitempty"`
	ModuleConfigUserFields          modules.ModuleConfigUserFields        `json:"moduleConfigUserFields,omitempty"`
	DisableDefaultFunctionCaching   bool                                  `json:"disableDefaultFunctionCaching,omitempty"`
	SDK                             *SDKConfig                            `json:"sdk,omitempty"`
	IncludePaths                    []string                              `json:"includePaths,omitempty"`
	RebasedIncludePaths             []string                              `json:"rebasedIncludePaths,omitempty"`
	ConfigDependencies              []*modules.ModuleConfigDependency     `json:"configDependencies,omitempty"`
	DependencyResultIDs             []uint64                              `json:"dependencyResultIDs,omitempty"`
	ConfigBlueprint                 *modules.ModuleConfigDependency       `json:"configBlueprint,omitempty"`
	BlueprintResultID               uint64                                `json:"blueprintResultID,omitempty"`
	ConfigToolchains                []*modules.ModuleConfigDependency     `json:"configToolchains,omitempty"`
	ToolchainResultIDs              []uint64                              `json:"toolchainResultIDs,omitempty"`
	ToolchainContextSourceResultID  uint64                                `json:"toolchainContextSourceResultID,omitempty"`
	ToolchainConfigIndex            int                                   `json:"toolchainConfigIndex,omitempty"`
	ToolchainProjection             bool                                  `json:"toolchainProjection,omitempty"`
	UserDefaults                    *EnvFile                              `json:"userDefaults,omitempty"`
	ConfigClients                   []*modules.ModuleConfigClient         `json:"configClients,omitempty"`
	SourceRootSubpath               string                                `json:"sourceRootSubpath,omitempty"`
	SourceSubpath                   string                                `json:"sourceSubpath,omitempty"`
	OriginalSubpath                 string                                `json:"originalSubpath,omitempty"`
	ContextDirectoryResultID        uint64                                `json:"contextDirectoryResultID,omitempty"`
	Kind                            ModuleSourceKind                      `json:"kind"`
	Local                           *LocalModuleSource                    `json:"local,omitempty"`
	Git                             *persistedGitModuleSourcePayload      `json:"git,omitempty"`
	DirSrc                          *persistedDirModuleSourcePayload      `json:"dirSrc,omitempty"`
	GitUnfilteredContextDirResultID uint64                                `json:"gitUnfilteredContextDirResultID,omitempty"`
	SDKCapabilities                 *persistedModuleSourceSDKCapabilities `json:"sdkCapabilities,omitempty"`
}

type persistedModuleSourceLazySDK struct {
	config       *SDKConfig
	src          *ModuleSource
	capabilities persistedModuleSourceSDKCapabilities

	mu     sync.Mutex
	loaded SDK
}

var _ SDK = (*persistedModuleSourceLazySDK)(nil)

func (sdk *persistedModuleSourceLazySDK) ensure(ctx context.Context) (SDK, error) {
	if sdk == nil || sdk.config == nil {
		return nil, fmt.Errorf("load persisted module source sdk: missing sdk config")
	}

	sdk.mu.Lock()
	loaded := sdk.loaded
	sdk.mu.Unlock()
	if loaded != nil {
		return loaded, nil
	}

	if moduleSourceSDKLoader == nil {
		return nil, fmt.Errorf("load persisted module source sdk: sdk loader is not configured")
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("load persisted module source sdk query: %w", err)
	}
	loaded, err = moduleSourceSDKLoader(ctx, query, sdk.config, sdk.src)
	if err != nil {
		return nil, fmt.Errorf("load persisted module source sdk: %w", err)
	}

	sdk.mu.Lock()
	if sdk.loaded == nil {
		sdk.loaded = loaded
		if sdk.src != nil {
			sdk.src.SDKImpl = loaded
		}
	}
	loaded = sdk.loaded
	sdk.mu.Unlock()
	return loaded, nil
}

func (sdk *persistedModuleSourceLazySDK) AsRuntime() (Runtime, bool) {
	if sdk == nil || !sdk.capabilities.Runtime {
		return nil, false
	}
	return persistedModuleSourceLazyRuntime{sdk: sdk}, true
}

func (sdk *persistedModuleSourceLazySDK) AsModuleTypes() (ModuleTypes, bool) {
	if sdk == nil || !sdk.capabilities.ModuleTypes {
		return nil, false
	}
	return persistedModuleSourceLazyModuleTypes{sdk: sdk}, true
}

func (sdk *persistedModuleSourceLazySDK) AsCodeGenerator() (CodeGenerator, bool) {
	if sdk == nil || !sdk.capabilities.CodeGenerator {
		return nil, false
	}
	return persistedModuleSourceLazyCodeGenerator{sdk: sdk}, true
}

func (sdk *persistedModuleSourceLazySDK) AsClientGenerator() (ClientGenerator, bool) {
	if sdk == nil || !sdk.capabilities.ClientGenerator {
		return nil, false
	}
	return persistedModuleSourceLazyClientGenerator{sdk: sdk}, true
}

type persistedModuleSourceLazyRuntime struct {
	sdk *persistedModuleSourceLazySDK
}

var _ Runtime = persistedModuleSourceLazyRuntime{}

func (sdk persistedModuleSourceLazyRuntime) Runtime(
	ctx context.Context,
	deps *ModDeps,
	src dagql.ObjectResult[*ModuleSource],
) (ModuleRuntime, error) {
	loaded, err := sdk.sdk.ensure(ctx)
	if err != nil {
		return nil, err
	}
	runtimeSDK, ok := loaded.AsRuntime()
	if !ok {
		return nil, fmt.Errorf("persisted module source sdk does not implement runtime")
	}
	return runtimeSDK.Runtime(ctx, deps, src)
}

type persistedModuleSourceLazyModuleTypes struct {
	sdk *persistedModuleSourceLazySDK
}

var _ ModuleTypes = persistedModuleSourceLazyModuleTypes{}

func (sdk persistedModuleSourceLazyModuleTypes) ModuleTypes(
	ctx context.Context,
	deps *ModDeps,
	src dagql.ObjectResult[*ModuleSource],
	mod *Module,
) (dagql.ObjectResult[*Module], error) {
	loaded, err := sdk.sdk.ensure(ctx)
	if err != nil {
		return dagql.ObjectResult[*Module]{}, err
	}
	moduleTypesSDK, ok := loaded.AsModuleTypes()
	if !ok {
		return dagql.ObjectResult[*Module]{}, fmt.Errorf("persisted module source sdk does not implement module types")
	}
	return moduleTypesSDK.ModuleTypes(ctx, deps, src, mod)
}

type persistedModuleSourceLazyCodeGenerator struct {
	sdk *persistedModuleSourceLazySDK
}

var _ CodeGenerator = persistedModuleSourceLazyCodeGenerator{}

func (sdk persistedModuleSourceLazyCodeGenerator) Codegen(
	ctx context.Context,
	deps *ModDeps,
	src dagql.ObjectResult[*ModuleSource],
) (*GeneratedCode, error) {
	loaded, err := sdk.sdk.ensure(ctx)
	if err != nil {
		return nil, err
	}
	codegenSDK, ok := loaded.AsCodeGenerator()
	if !ok {
		return nil, fmt.Errorf("persisted module source sdk does not implement code generator")
	}
	return codegenSDK.Codegen(ctx, deps, src)
}

type persistedModuleSourceLazyClientGenerator struct {
	sdk *persistedModuleSourceLazySDK
}

var _ ClientGenerator = persistedModuleSourceLazyClientGenerator{}

func (sdk persistedModuleSourceLazyClientGenerator) RequiredClientGenerationFiles(
	ctx context.Context,
) (dagql.Array[dagql.String], error) {
	loaded, err := sdk.sdk.ensure(ctx)
	if err != nil {
		return nil, err
	}
	clientSDK, ok := loaded.AsClientGenerator()
	if !ok {
		return nil, fmt.Errorf("persisted module source sdk does not implement client generator")
	}
	return clientSDK.RequiredClientGenerationFiles(ctx)
}

func (sdk persistedModuleSourceLazyClientGenerator) GenerateClient(
	ctx context.Context,
	modSource dagql.ObjectResult[*ModuleSource],
	deps *ModDeps,
	outputDir string,
) (dagql.ObjectResult[*Directory], error) {
	loaded, err := sdk.sdk.ensure(ctx)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}
	clientSDK, ok := loaded.AsClientGenerator()
	if !ok {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("persisted module source sdk does not implement client generator")
	}
	return clientSDK.GenerateClient(ctx, modSource, deps, outputDir)
}

func (src *ModuleSource) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if src == nil {
		return nil, fmt.Errorf("encode persisted module source: nil module source")
	}
	payload := persistedModuleSourcePayload{
		ConfigExists:                  src.ConfigExists,
		ModuleName:                    src.ModuleName,
		ModuleOriginalName:            src.ModuleOriginalName,
		EngineVersion:                 src.EngineVersion,
		CodegenConfig:                 src.CodegenConfig,
		ModuleConfigUserFields:        src.ModuleConfigUserFields,
		DisableDefaultFunctionCaching: src.DisableDefaultFunctionCaching,
		SDK:                           src.SDK,
		IncludePaths:                  slices.Clone(src.IncludePaths),
		RebasedIncludePaths:           slices.Clone(src.RebasedIncludePaths),
		ConfigDependencies:            slices.Clone(src.ConfigDependencies),
		ConfigBlueprint:               src.ConfigBlueprint,
		ConfigToolchains:              slices.Clone(src.ConfigToolchains),
		ToolchainConfigIndex:          src.ToolchainConfigIndex,
		ToolchainProjection:           src.ToolchainProjection,
		UserDefaults:                  src.UserDefaults,
		ConfigClients:                 slices.Clone(src.ConfigClients),
		SourceRootSubpath:             src.SourceRootSubpath,
		SourceSubpath:                 src.SourceSubpath,
		OriginalSubpath:               src.OriginalSubpath,
		Kind:                          src.Kind,
		Local:                         src.Local,
	}
	if src.SDK != nil {
		if src.SDKImpl == nil {
			return nil, fmt.Errorf("encode persisted module source: sdk config is set but sdk impl is not initialized")
		}
		_, hasRuntime := src.SDKImpl.AsRuntime()
		_, hasModuleTypes := src.SDKImpl.AsModuleTypes()
		_, hasCodeGenerator := src.SDKImpl.AsCodeGenerator()
		_, hasClientGenerator := src.SDKImpl.AsClientGenerator()
		payload.SDKCapabilities = &persistedModuleSourceSDKCapabilities{
			Runtime:         hasRuntime,
			ModuleTypes:     hasModuleTypes,
			CodeGenerator:   hasCodeGenerator,
			ClientGenerator: hasClientGenerator,
		}
	}
	if src.ContextDirectory.Self() != nil {
		contextDirID, err := encodePersistedObjectRef(cache, src.ContextDirectory, "module source context directory")
		if err != nil {
			return nil, err
		}
		payload.ContextDirectoryResultID = contextDirID
	}
	payload.DependencyResultIDs = make([]uint64, 0, len(src.Dependencies))
	for _, dep := range src.Dependencies {
		if dep.Self() == nil {
			continue
		}
		depID, err := encodePersistedObjectRef(cache, dep, "module source dependency")
		if err != nil {
			return nil, err
		}
		payload.DependencyResultIDs = append(payload.DependencyResultIDs, depID)
	}
	if src.Blueprint.Self() != nil {
		blueprintID, err := encodePersistedObjectRef(cache, src.Blueprint, "module source blueprint")
		if err != nil {
			return nil, err
		}
		payload.BlueprintResultID = blueprintID
	}
	payload.ToolchainResultIDs = make([]uint64, 0, len(src.Toolchains))
	for _, toolchain := range src.Toolchains {
		if toolchain.Self() == nil {
			continue
		}
		toolchainID, err := encodePersistedObjectRef(cache, toolchain, "module source toolchain")
		if err != nil {
			return nil, err
		}
		payload.ToolchainResultIDs = append(payload.ToolchainResultIDs, toolchainID)
	}
	if src.ToolchainContextSource.Valid && src.ToolchainContextSource.Value.Self() != nil {
		toolchainContextSourceID, err := encodePersistedObjectRef(cache, src.ToolchainContextSource.Value, "module source toolchain context source")
		if err != nil {
			return nil, err
		}
		payload.ToolchainContextSourceResultID = toolchainContextSourceID
	}
	if src.Git != nil {
		payload.Git = &persistedGitModuleSourcePayload{
			CloneRef:     src.Git.CloneRef,
			Symbolic:     src.Git.Symbolic,
			HTMLRepoURL:  src.Git.HTMLRepoURL,
			HTMLURL:      src.Git.HTMLURL,
			RepoRootPath: src.Git.RepoRootPath,
			Version:      src.Git.Version,
			Commit:       src.Git.Commit,
			Ref:          src.Git.Ref,
		}
		if src.Git.UnfilteredContextDir.Self() != nil {
			unfilteredID, err := encodePersistedObjectRef(cache, src.Git.UnfilteredContextDir, "module source git unfiltered context dir")
			if err != nil {
				return nil, err
			}
			payload.GitUnfilteredContextDirResultID = unfilteredID
		}
	}
	if src.DirSrc != nil {
		payload.DirSrc = &persistedDirModuleSourcePayload{
			OriginalSourceRootSubpath: src.DirSrc.OriginalSourceRootSubpath,
		}
		if src.DirSrc.OriginalContextDir.Self() != nil {
			originalContextDirID, err := encodePersistedObjectRef(cache, src.DirSrc.OriginalContextDir, "module source dir original context dir")
			if err != nil {
				return nil, err
			}
			payload.DirSrc.OriginalContextDirResultID = originalContextDirID
		}
	}
	return json.Marshal(payload)
}

func (*ModuleSource) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedModuleSourcePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted module source payload: %w", err)
	}
	contextDirectory, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ContextDirectoryResultID, "module source context directory")
	if err != nil {
		return nil, err
	}
	dependencies := make([]dagql.ObjectResult[*ModuleSource], 0, len(persisted.DependencyResultIDs))
	for _, depID := range persisted.DependencyResultIDs {
		depRes, err := loadPersistedObjectResultByResultID[*ModuleSource](ctx, dag, depID, "module source dependency")
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, depRes)
	}
	blueprint, err := loadPersistedObjectResultByResultID[*ModuleSource](ctx, dag, persisted.BlueprintResultID, "module source blueprint")
	if err != nil {
		return nil, err
	}
	toolchains := make([]dagql.ObjectResult[*ModuleSource], 0, len(persisted.ToolchainResultIDs))
	for _, toolchainID := range persisted.ToolchainResultIDs {
		toolchainRes, err := loadPersistedObjectResultByResultID[*ModuleSource](ctx, dag, toolchainID, "module source toolchain")
		if err != nil {
			return nil, err
		}
		toolchains = append(toolchains, toolchainRes)
	}
	toolchainContextSource, err := loadPersistedObjectResultByResultID[*ModuleSource](ctx, dag, persisted.ToolchainContextSourceResultID, "module source toolchain context source")
	if err != nil {
		return nil, err
	}
	src := &ModuleSource{
		ConfigExists:                  persisted.ConfigExists,
		ModuleName:                    persisted.ModuleName,
		ModuleOriginalName:            persisted.ModuleOriginalName,
		EngineVersion:                 persisted.EngineVersion,
		CodegenConfig:                 persisted.CodegenConfig,
		ModuleConfigUserFields:        persisted.ModuleConfigUserFields,
		DisableDefaultFunctionCaching: persisted.DisableDefaultFunctionCaching,
		SDK:                           persisted.SDK,
		IncludePaths:                  slices.Clone(persisted.IncludePaths),
		RebasedIncludePaths:           slices.Clone(persisted.RebasedIncludePaths),
		ConfigDependencies:            slices.Clone(persisted.ConfigDependencies),
		Dependencies:                  dependencies,
		ConfigBlueprint:               persisted.ConfigBlueprint,
		Blueprint:                     blueprint,
		ConfigToolchains:              slices.Clone(persisted.ConfigToolchains),
		Toolchains:                    toolchains,
		ToolchainConfigIndex:          persisted.ToolchainConfigIndex,
		ToolchainProjection:           persisted.ToolchainProjection,
		UserDefaults:                  persisted.UserDefaults,
		ConfigClients:                 slices.Clone(persisted.ConfigClients),
		SourceRootSubpath:             persisted.SourceRootSubpath,
		SourceSubpath:                 persisted.SourceSubpath,
		OriginalSubpath:               persisted.OriginalSubpath,
		ContextDirectory:              contextDirectory,
		Kind:                          persisted.Kind,
		Local:                         persisted.Local,
	}
	if toolchainContextSource.Self() != nil {
		src.ToolchainContextSource = dagql.NonNull(toolchainContextSource)
	}
	if persisted.Git != nil {
		src.Git = &GitModuleSource{
			CloneRef:     persisted.Git.CloneRef,
			Symbolic:     persisted.Git.Symbolic,
			HTMLRepoURL:  persisted.Git.HTMLRepoURL,
			HTMLURL:      persisted.Git.HTMLURL,
			RepoRootPath: persisted.Git.RepoRootPath,
			Version:      persisted.Git.Version,
			Commit:       persisted.Git.Commit,
			Ref:          persisted.Git.Ref,
		}
		if persisted.GitUnfilteredContextDirResultID != 0 {
			unfilteredContextDir, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.GitUnfilteredContextDirResultID, "module source git unfiltered context directory")
			if err != nil {
				return nil, err
			}
			src.Git.UnfilteredContextDir = unfilteredContextDir
		}
	}
	if persisted.DirSrc != nil {
		src.DirSrc = &DirModuleSource{
			OriginalSourceRootSubpath: persisted.DirSrc.OriginalSourceRootSubpath,
		}
		if persisted.DirSrc.OriginalContextDirResultID != 0 {
			originalContextDir, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.DirSrc.OriginalContextDirResultID, "module source dir original context directory")
			if err != nil {
				return nil, err
			}
			src.DirSrc.OriginalContextDir = originalContextDir
		}
	}
	if src.SDK != nil {
		if persisted.SDKCapabilities == nil {
			return nil, fmt.Errorf("decode persisted module source: missing persisted sdk capabilities")
		}
		src.SDKImpl = &persistedModuleSourceLazySDK{
			config:       src.SDK,
			src:          src,
			capabilities: *persisted.SDKCapabilities,
		}
	}
	return src, nil
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
		return src.Git.Commit
	default:
		return ""
	}
}

// GetRelatedModules returns the related modules (dependencies or toolchains) based on the type
func (src *ModuleSource) GetRelatedModules(typ ModuleRelationType) []dagql.ObjectResult[*ModuleSource] {
	if typ == ModuleRelationTypeDependency {
		return src.Dependencies
	}
	return src.Toolchains
}

// SetRelatedModules sets the related modules (dependencies or toolchains) based on the type
func (src *ModuleSource) SetRelatedModules(typ ModuleRelationType, modules []dagql.ObjectResult[*ModuleSource]) {
	if typ == ModuleRelationTypeDependency {
		src.Dependencies = modules
	} else {
		src.Toolchains = modules
	}
}

func (src *ModuleSource) innerEnvFile(ctx context.Context) (*EnvFile, string, error) {
	// We only allow loading an env file from local modules, for safety
	if src.Kind != ModuleSourceKindLocal {
		return nil, "", nil
	}

	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, "", err
	}

	// FIXME: .env must be at the root of the module directory
	// If the user calls dagger from a subdirectory of the module, and that subdirectory contains a more
	//  specialized .env, that will be ignored. To fix this, we need access to current workdir on the host,
	// so that we can findup from there.
	moduleDirPath := path.Join(
		src.Local.ContextDirectoryPath, // path of the module's git root, on the host
		src.SourceRootSubpath,          // path of the module directory, relative to its git root
	)
	// Check if the env file exists
	var envFileExists bool
	if err := dag.Select(ctx, dag.Root(), &envFileExists,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(moduleDirPath)},
				{Name: "include", Value: dagql.ArrayInput[dagql.String]{".env"}},
			},
		},
		dagql.Selector{
			Field: "exists",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(".env")},
			},
		},
	); status.Code(err) == codes.NotFound {
		// It's possible that the module directory *doesn't exist yet*
		// (ie. we are called from `dagger init ./FOO` and `FOO` will be populated after we return)
		// Therefore: if parent directory doesn't exist, just return "no result" without error
		return nil, "", nil
	} else if err != nil {
		return nil, "", fmt.Errorf("failed to check for inner env file in %s: %w",
			moduleDirPath, err)
	}
	if !envFileExists {
		return nil, "", nil
	}
	envFilePath := path.Join(moduleDirPath, ".env")
	var envFile *EnvFile
	if err := dag.Select(ctx, dag.Root(), &envFile,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(envFilePath)},
			},
		},
		dagql.Selector{
			Field: "asEnvFile",
			Args: []dagql.NamedInput{
				{Name: "expand", Value: dagql.Opt(dagql.NewBoolean(true))},
			},
		},
	); err != nil {
		return nil, "", fmt.Errorf("failed to load inner env file in %s: %w", moduleDirPath, err)
	}
	return envFile, envFilePath, nil
}

func (src *ModuleSource) outerEnvFile(ctx context.Context) (*EnvFile, string, error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, "", err
	}
	var envFilePath dagql.String
	if err := dag.Select(ctx, dag.Root(), &envFilePath,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "findUp",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString(".env")},
			},
		},
	); err != nil {
		return nil, "", fmt.Errorf("failed to find-up outer .env: %s", err.Error())
	}
	if envFilePath == "" {
		return &EnvFile{}, "", nil
	}
	var envFile *EnvFile
	if err := dag.Select(ctx, dag.Root(), &envFile,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: envFilePath},
			},
		},
		dagql.Selector{Field: "asEnvFile",
			Args: []dagql.NamedInput{
				{Name: "expand", Value: dagql.Opt(dagql.NewBoolean(true))},
			},
		},
	); err != nil {
		return nil, envFilePath.String(), fmt.Errorf("failed to load outer env file from %q: %s", envFilePath.String(), err.Error())
	}
	return envFile, envFilePath.String(), nil
}

// LoadUserDefaults loads and merges environment files for local module defaults.
// It combines the inner .env file (from the module's source root) with relevant
// entries from the outer .env file (found via find-up from the host), filtered
// by the module name and original module name as prefixes.
//
// Example:
// Inner .env (mymodule/.env): FOO=bar
// Outer .env (found via find-up): MYMODULE_BAZ=qux, OTHER_VAR=ignored
// Result: FOO=bar, BAZ=qux (prefix "MYMODULE_" removed from outer entries)
func (src *ModuleSource) LoadUserDefaults(ctx context.Context) error {
	// For local module sources, ensure we have the right client context for filesystem access.
	// Modules run as their own clients, but need access to the original caller's filesystem.
	// NonModuleParentClientMetadata is idempotent, so this is safe to call multiple times.
	if src.Kind == ModuleSourceKindLocal {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return fmt.Errorf("failed to get current query: %w", err)
		}
		localSourceClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return fmt.Errorf("failed to get client metadata: %w", err)
		}
		ctx = engine.ContextWithClientMetadata(ctx, localSourceClientMetadata)
	}
	innerEnvFile, _, err := src.innerEnvFile(ctx)
	if err != nil {
		return err
	}
	outerEnvFile, _, err := src.outerEnvFile(ctx)
	if err != nil {
		return err
	}
	outerForName, err := outerEnvFile.Namespace(ctx, src.ModuleName)
	if err != nil {
		return err
	}
	outerForOriginalName, err := outerEnvFile.Namespace(ctx, src.ModuleOriginalName)
	if err != nil {
		return err
	}
	src.UserDefaults = NewEnvFile(true).WithEnvFiles(innerEnvFile, outerForName, outerForOriginalName)
	return nil
}

// we mix this into digest hashes to ensure they don't accidentally collide
// with any others
const moduleSourceHashMix = "moduleSource"

// SourceImplementationDigest calculates a content-hash of the module source's
// implementation. Two module sources with the same digest should share
// implementation-scoped cache identity for SDK operations and module function
// calls even if they came from different client-specific sources.
func (src *ModuleSource) SourceImplementationDigest(ctx context.Context) (digest.Digest, error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get dag server: %w", err)
	}

	var contextDigest string
	if err := dag.Select(ctx, src.ContextDirectory, &contextDigest, dagql.Selector{Field: "digest"}); err != nil {
		return "", fmt.Errorf("failed to get module source context directory digest: %w", err)
	}

	inputs := []string{
		moduleSourceHashMix,
		src.ModuleOriginalName,
		src.SourceRootSubpath,
		src.SourceSubpath,
		contextDigest,
	}

	if src.SDK != nil && src.SDK.Debug {
		inputs = append(inputs, rand.Text())
	}

	// Include user defaults in digest so changes to env files invalidate cache
	if src.UserDefaults != nil {
		vars, err := src.UserDefaults.Variables(ctx, false)
		if err != nil {
			// If user defaults fail to load, log the error and skip them from digest calculation
			// FIXME: change the signature to bubble up the error?
			slog.Error("failed to load user defaults for module source",
				"module_name", src.ModuleName,
				"error", err,
			)
		} else {
			// Sort by variable name for better digest stability
			sort.Slice(vars, func(i, j int) bool {
				return vars[i].Name < vars[j].Name
			})
			for _, v := range vars {
				inputs = append(inputs, fmt.Sprintf("env:%s=%s", v.Name, v.Value))
			}
		}
	}

	if src.SDK != nil {
		inputs = append(inputs, src.SDK.Source)
	}

	inputs = append(inputs, src.IncludePaths...)

	for _, dep := range src.Dependencies {
		if dep.Self() == nil {
			continue
		}
		var depDigest string
		if err := dag.Select(ctx, dep, &depDigest, dagql.Selector{Field: "digest"}); err != nil {
			return "", fmt.Errorf("failed to get dependency digest: %w", err)
		}
		inputs = append(inputs, depDigest)
	}

	// Include blueprint in digest so changes to blueprint invalidate cache
	if src.Blueprint.Self() != nil {
		var blueprintDigest string
		if err := dag.Select(ctx, src.Blueprint, &blueprintDigest, dagql.Selector{Field: "digest"}); err != nil {
			return "", fmt.Errorf("failed to get blueprint digest: %w", err)
		}
		inputs = append(inputs, "blueprint:"+blueprintDigest)
	}

	// Include toolchains in digest so changes to toolchains invalidate cache
	for _, toolchain := range src.Toolchains {
		if toolchain.Self() == nil {
			continue
		}
		var toolchainDigest string
		if err := dag.Select(ctx, toolchain, &toolchainDigest, dagql.Selector{Field: "digest"}); err != nil {
			return "", fmt.Errorf("failed to get toolchain digest: %w", err)
		}
		inputs = append(inputs, "toolchain:"+toolchainDigest)
	}

	for _, client := range src.ConfigClients {
		inputs = append(inputs, client.Generator, client.Directory)
	}

	return hashutil.HashStrings(inputs...), nil
}

func ImplementationScopedModuleSource(
	ctx context.Context,
	src dagql.ObjectResult[*ModuleSource],
) (dagql.ObjectResult[*ModuleSource], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*ModuleSource]{}, fmt.Errorf("implementation-scoped module source: current dagql server: %w", err)
	}

	var scoped dagql.ObjectResult[*ModuleSource]
	if err := dag.Select(ctx, src, &scoped, dagql.Selector{Field: "_implementationScoped"}); err != nil {
		return dagql.ObjectResult[*ModuleSource]{}, fmt.Errorf("implementation-scoped module source: select field: %w", err)
	}
	return scoped, nil
}

// LoadContextDir loads addition files+directories from the module source's context, including those that
// may have not been included in the original module source load.
func (src *ModuleSource) LoadContextDir(
	ctx context.Context,
	dag *dagql.Server,
	path string,
	filter CopyFilter,
) (inst dagql.ObjectResult[*Directory], err error) {
	filterInputs := []dagql.NamedInput{}
	if len(filter.Include) > 0 {
		filterInputs = append(filterInputs, dagql.NamedInput{
			Name:  "include",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(filter.Include...)),
		})
	}
	if len(filter.Exclude) > 0 {
		filterInputs = append(filterInputs, dagql.NamedInput{
			Name:  "exclude",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(filter.Exclude...)),
		})
	}
	if filter.Gitignore {
		filterInputs = append(filterInputs, dagql.NamedInput{
			Name:  "gitignore",
			Value: dagql.NewBoolean(true),
		})
	}

	// Check if there's an Env - if so, use its workspace as the context for
	// defaultPath arguments.
	//
	// NOTE: this applies unilaterally, whether the module was loaded from Host,
	// Git, or a Directory.
	if envID, ok := EnvIDFromContext(ctx); ok {
		inst, err = src.loadContextFromEnv(ctx, dag, envID, path, filterInputs)
	} else {
		inst, err = src.loadContextFromSource(ctx, dag, path, filterInputs)
	}
	if err != nil {
		return inst, err
	}
	instID, err := inst.ID()
	if err != nil {
		return inst, fmt.Errorf("context directory ID: %w", err)
	}
	instCall, err := inst.ResultCall()
	if err != nil {
		return inst, fmt.Errorf("context directory call: %w", err)
	}
	if instID != nil && instCall.ContentDigest() == "" {
		inst, err = MakeDirectoryContentHashed(ctx, inst)
		if err != nil {
			return inst, fmt.Errorf("failed to content-hash contextual directory: %w", err)
		}
	}

	return inst, nil
}

func (src *ModuleSource) loadContextFromEnv(
	ctx context.Context,
	dag *dagql.Server,
	envID *call.ID,
	path string,
	filterInputs []dagql.NamedInput,
) (inst dagql.ObjectResult[*Directory], err error) {
	// If path is not absolute, it's relative to the module root directory.
	// If path is absolute, it's relative to the context directory.
	if !filepath.IsAbs(path) {
		path = filepath.Join(src.SourceRootSubpath, path)
	}
	envRes, err := dag.Load(ctx, envID)
	if err != nil {
		return inst, fmt.Errorf("failed to load current env: %w", err)
	}
	sels := []dagql.Selector{
		{
			Field: "workspace",
		},
	}
	if path != "." {
		sels = append(sels, dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(path)},
			},
		})
	}
	if len(filterInputs) > 0 {
		sels = append(sels, dagql.Selector{
			Field: "filter",
			Args:  filterInputs,
		})
	}
	err = dag.Select(ctx, envRes, &inst, sels...)
	if err != nil {
		return inst, fmt.Errorf("failed to select env directory: %w", err)
	}
	return inst, nil
}

func (src *ModuleSource) loadContextFromSource(
	ctx context.Context,
	dag *dagql.Server,
	path string,
	filterInputs []dagql.NamedInput,
) (inst dagql.ObjectResult[*Directory], err error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	switch src.Kind {
	case ModuleSourceKindLocal:
		localSourceClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
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
				Args: append([]dagql.NamedInput{
					{Name: "path", Value: dagql.String(path)},
					{Name: "noCache", Value: dagql.Boolean(true)},
				}, filterInputs...),
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to select host directory: %w", err)
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

		if len(filterInputs) > 0 {
			ctxDirID, err := ctxDir.ID()
			if err != nil {
				return inst, fmt.Errorf("context directory ID for filtering: %w", err)
			}
			if err := dag.Select(ctx, dag.Root(), &ctxDir,
				dagql.Selector{
					Field: "directory",
				},
				dagql.Selector{
					Field: "withDirectory",
					Args: append([]dagql.NamedInput{
						{Name: "path", Value: dagql.String("/")},
						{Name: "source", Value: dagql.NewID[*Directory](ctxDirID)},
					}, filterInputs...),
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		inst = ctxDir

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

		if len(filterInputs) > 0 {
			ctxDirID, err := ctxDir.ID()
			if err != nil {
				return inst, fmt.Errorf("context directory ID for filtering: %w", err)
			}
			if err := dag.Select(ctx, dag.Root(), &ctxDir,
				dagql.Selector{
					Field: "directory",
				},
				dagql.Selector{
					Field: "withDirectory",
					Args: append([]dagql.NamedInput{
						{Name: "path", Value: dagql.String("/")},
						{Name: "source", Value: dagql.NewID[*Directory](ctxDirID)},
					}, filterInputs...),
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		inst, err = MakeDirectoryContentHashed(ctx, ctxDir)
		if err != nil {
			return inst, err
		}

	default:
		return inst, fmt.Errorf("unsupported module src kind: %q", src.Kind)
	}

	return inst, nil
}

func (src *ModuleSource) LoadContextFile(
	ctx context.Context,
	dag *dagql.Server,
	path string,
) (inst dagql.ObjectResult[*File], err error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}

	switch src.Kind {
	case ModuleSourceKindLocal:
		localSourceClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
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
				Field: "file",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(path)},
					{Name: "noCache", Value: dagql.Boolean(true)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to select file: %w", err)
		}

	case ModuleSourceKindGit:
		slog.Debug("moduleSource.LoadContext: loading contextual file from git", "path", path, "kind", src.Kind, "repo", src.Git.HTMLURL)

		if !filepath.IsAbs(path) {
			path = filepath.Join("/", src.SourceRootSubpath, path)
		}

		// Use the Git context directory without dagger.json includes applied.
		ctxDir := src.Git.UnfilteredContextDir
		if err := dag.Select(ctx, ctxDir, &inst,
			dagql.Selector{
				Field: "file",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(path)},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
		}

	case ModuleSourceKindDir:
		if !filepath.IsAbs(path) {
			path = filepath.Join("/", src.SourceRootSubpath, path)
		}

		// Use the Dir context directory.
		ctxDir := src.ContextDirectory

		if err := dag.Select(ctx, ctxDir, &inst,
			dagql.Selector{
				Field: "file",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(path)},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
		}

	default:
		return inst, fmt.Errorf("unsupported module src kind: %q", src.Kind)
	}

	return inst, nil
}

func (src *ModuleSource) LoadContextGit(
	ctx context.Context,
	dag *dagql.Server,
) (inst dagql.ObjectResult[*GitRepository], err error) {
	if src.Kind == ModuleSourceKindGit {
		// easy, we're running a git repo
		err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{
				Field: "git",
				Args: []dagql.NamedInput{
					{Name: "url", Value: dagql.String(src.Git.CloneRef)},
					// NOTE: pin HEAD to the module's git commit and ref
					// this matches the behavior of calling a checked out local source module
					{Name: "commit", Value: dagql.String(src.Git.Commit)},
					{Name: "ref", Value: dagql.String(src.Git.Ref)},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to load contextual git repository: %w", err)
		}
		return inst, nil
	}

	// bit harder, this is actually a local directory
	dir, err := src.LoadContextDir(ctx, dag, "/", CopyFilter{
		Gitignore: true,
	})
	if err != nil {
		return inst, fmt.Errorf("failed to load contextual git: %w", err)
	}

	err = dag.Select(ctx, dir, &inst,
		dagql.Selector{
			Field: "asGit",
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to load contextual git repository: %w", err)
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
	// The fully resolved git ref string of the source
	Ref string

	// The full git repo for the module source without any include filtering
	UnfilteredContextDir dagql.ObjectResult[*Directory]
}

func (src GitModuleSource) Link(filepath string, line int, column int) (string, error) {
	parsedURL, err := url.Parse(src.HTMLRepoURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse git repo URL %q: %w", src.HTMLRepoURL, err)
	}

	switch parsedURL.Host {
	case "github.com", "gitlab.com":
		result := src.HTMLRepoURL + path.Join("/tree", src.Commit, filepath)
		if line > 0 {
			result += fmt.Sprintf("#L%d", line)
		}
		return result, nil
	case "dev.azure.com":
		result := src.HTMLRepoURL + path.Join("/commit", src.Commit)
		if filepath != "." {
			result += "?path=/" + filepath
		}
		return result, nil
	default:
		return src.HTMLRepoURL + path.Join("/src", src.Commit, filepath), nil
	}
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

type DirModuleSource struct {
	// the original dir that AsModuleSource was called on
	OriginalContextDir dagql.ObjectResult[*Directory]
	// the original source root subpath provided to AsModuleSource
	OriginalSourceRootSubpath string
}

// ResolveDepToSource given a parent module source, load a dependency of it
// from the given depSrcRef, depPin and depName.
func ResolveDepToSource(
	ctx context.Context,
	bk *buildkit.Client,
	dag *dagql.Server,
	parentSrc *ModuleSource,
	depSrcRef string,
	depPin string,
	depName string,
) (inst dagql.ObjectResult[*ModuleSource], err error) {
	// sanity checks
	if parentSrc != nil {
		if parentSrc.SourceRootSubpath == "" {
			return inst, fmt.Errorf("source root path must be set")
		}
		if parentSrc.ModuleName == "" {
			return inst, fmt.Errorf("module name must be set")
		}
	}

	parsedDepRef, err := ParseRefString(
		ctx,
		ModuleSourceStatFS{bk, parentSrc},
		depSrcRef,
		depPin,
	)
	if err != nil {
		return inst, fmt.Errorf("failed to parse dep ref string: %w", err)
	}

	switch parsedDepRef.Kind {
	case ModuleSourceKindLocal:
		if parentSrc == nil {
			// it's okay if there's no parent when the dep is git, but we can't find a local dep relative to nothing
			return inst, fmt.Errorf("local module dep source path %q must be relative to a parent module", depSrcRef)
		}

		if filepath.IsAbs(depSrcRef) {
			// they need to be relative to the parent module's source root
			return inst, fmt.Errorf("local module dep source path %q is absolute", depSrcRef)
		}

		switch parentSrc.Kind {
		case ModuleSourceKindLocal:
			// parent=local, dep=local
			// load the dep relative to the parent's source root, from the caller's filesystem
			depPath := filepath.Join(parentSrc.Local.ContextDirectoryPath, parentSrc.SourceRootSubpath, depSrcRef)
			depRelPath, err := pathutil.LexicalRelativePath(parentSrc.Local.ContextDirectoryPath, depPath)
			if err != nil {
				return inst, fmt.Errorf("failed to get relative path from context to dep: %w", err)
			}
			if !filepath.IsLocal(depRelPath) {
				return inst, fmt.Errorf("local module dep source path %q escapes context %q", depRelPath, parentSrc.Local.ContextDirectoryPath)
			}

			selectors := []dagql.Selector{{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(depPath)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err = dag.Select(ctx, dag.Root(), &inst, selectors...)
			if err != nil {
				if errors.Is(err, dagql.ErrCacheRecursiveCall) {
					return inst, fmt.Errorf("module %q has a circular dependency on itself through dependency %q", parentSrc.ModuleName, depName)
				}
				return inst, err
			}
			return inst, nil

		case ModuleSourceKindGit:
			// parent=git, dep=local
			// load the dep relative to the parent's source root, from the parent source's git repo
			refString := GitRefString(
				parentSrc.Git.CloneRef,
				filepath.Join(parentSrc.SourceRootSubpath, depSrcRef),
				parentSrc.Git.Version,
			)
			selectors := []dagql.Selector{{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(refString)},
					{Name: "refPin", Value: dagql.String(parentSrc.Git.Commit)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err := dag.Select(ctx, dag.Root(), &inst, selectors...)
			if err != nil {
				return inst, err
			}
			return inst, nil

		case ModuleSourceKindDir:
			// parent=dir, dep=local
			depPath := filepath.Join(parentSrc.SourceRootSubpath, depSrcRef)
			selectors := []dagql.Selector{{
				Field: "asModuleSource",
				Args: []dagql.NamedInput{
					{Name: "sourceRootPath", Value: dagql.String(depPath)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err := dag.Select(ctx, parentSrc.ContextDirectory, &inst, selectors...)
			if err != nil {
				return inst, err
			}
			return inst, nil

		default:
			return inst, fmt.Errorf("unsupported parent module source kind: %s", parentSrc.Kind)
		}

	case ModuleSourceKindGit:
		// parent=*, dep=git
		selectors := []dagql.Selector{{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(depSrcRef)},
				{Name: "refPin", Value: dagql.String(depPin)},
			},
		}}
		if depName != "" {
			selectors = append(selectors, dagql.Selector{
				Field: "withName",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(depName)},
				},
			})
		}
		err := dag.Select(ctx, dag.Root(), &inst, selectors...)
		if err != nil {
			return inst, fmt.Errorf("failed to load git dep: %w", err)
		}
		return inst, nil

	default:
		return inst, fmt.Errorf("unsupported module source kind: %s", parsedDepRef.Kind)
	}
}

type StatFS interface {
	Stat(ctx context.Context, path string) (string, *Stat, error)
}

type StatFSFunc func(ctx context.Context, path string) (string, *Stat, error)

func (f StatFSFunc) Stat(ctx context.Context, path string) (string, *Stat, error) {
	return f(ctx, path)
}

type CallerStatFS struct {
	bk *buildkit.Client
}

func NewCallerStatFS(bk *buildkit.Client) *CallerStatFS {
	return &CallerStatFS{bk}
}

func (csfs CallerStatFS) Stat(ctx context.Context, path string) (string, *Stat, error) {
	bkStat, err := csfs.bk.StatCallerHostPath(ctx, path, true)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", nil, os.ErrNotExist
		}
		return "", nil, err
	}

	// Note that the mkstat func (from fsutils) returns a relative path; however, the Stat
	// struct only stores the basename, so we also return the relative dir path.
	pathDir := filepath.Dir(bkStat.Path)
	pathBase := filepath.Base(bkStat.Path)

	fileMode := fs.FileMode(bkStat.Mode)
	return pathDir, &Stat{
		Name:        pathBase,
		Size:        int(bkStat.Size_),
		Permissions: int(fileMode.Perm()),
		FileType:    FileModeToFileType(fileMode),
	}, nil
}

type ModuleSourceStatFS struct {
	bk  *buildkit.Client
	src *ModuleSource
}

func CallDirStat(ctx context.Context, dir dagql.ObjectResult[*Directory], path string) (string, *Stat, error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return "", nil, err
	}

	var info *Stat
	err = dag.Select(ctx, dir, &info,
		dagql.Selector{
			Field: "stat",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(path)},
			},
		},
	)
	if err != nil {
		return "", nil, err
	}
	return filepath.Dir(path), info, nil
}

func (fs ModuleSourceStatFS) Stat(ctx context.Context, path string) (string, *Stat, error) {
	if fs.src == nil {
		return "", nil, os.ErrNotExist
	}

	switch fs.src.Kind {
	case ModuleSourceKindLocal:
		path = filepath.Join(fs.src.Local.ContextDirectoryPath, fs.src.SourceRootSubpath, path)
		return CallerStatFS{fs.bk}.Stat(ctx, path)
	case ModuleSourceKindGit:
		path = filepath.Join("/", fs.src.SourceRootSubpath, path)
		return CallDirStat(ctx, fs.src.Git.UnfilteredContextDir, path)
	case ModuleSourceKindDir:
		path = filepath.Join("/", fs.src.SourceRootSubpath, path)
		return CallDirStat(ctx, fs.src.ContextDirectory, path)
	default:
		return "", nil, fmt.Errorf("unsupported module source kind: %s", fs.src.Kind)
	}
}

type ModuleSourceExperimentalFeature string

func (f ModuleSourceExperimentalFeature) String() string { return string(f) }

var ModuleSourceExperimentalFeatures = dagql.NewEnum[ModuleSourceExperimentalFeature]()

var (
	ModuleSourceExperimentalFeatureSelfCalls = ModuleSourceExperimentalFeatures.Register("SELF_CALLS", "Self calls")
)

func (f ModuleSourceExperimentalFeature) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSourceExperimentalFeature",
		NonNull:   true,
	}
}

func (f ModuleSourceExperimentalFeature) TypeDescription() string {
	return `Experimental features of a module`
}

func (f ModuleSourceExperimentalFeature) Decoder() dagql.InputDecoder {
	return ModuleSourceExperimentalFeatures
}

func (f ModuleSourceExperimentalFeature) ToLiteral() call.Literal {
	return ModuleSourceExperimentalFeatures.Literal(f)
}
