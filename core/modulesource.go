package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/solver/pb"
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
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/server/resource"
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

	UserDefaults *EnvFile `field:"true" name:"userDefaults" doc:"User-defined defaults read from local .env files"`
	// Clients are the clients generated for the module.
	ConfigClients []*modules.ModuleConfigClient `field:"true" name:"configClients" doc:"The clients generated for the module."`

	// SourceRootSubpath is the relative path from the context dir to the dir containing the module's dagger.json
	SourceRootSubpath string `field:"true" name:"sourceRootSubpath" doc:"The path, relative to the context directory, that contains the module's dagger.json."`
	// SourceSubpath is the relative path from the context dir to the dir containing the module's source code
	SourceSubpath string

	OriginalSubpath string

	ContextDirectory dagql.ObjectResult[*Directory] `field:"true" name:"contextDirectory" doc:"The full directory loaded for the module source, including the source code as a subdirectory."`

	Digest string `field:"true" name:"digest" doc:"A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance."`

	Kind   ModuleSourceKind `field:"true" name:"kind" doc:"The kind of module source (currently local, git or dir)."`
	Local  *LocalModuleSource
	Git    *GitModuleSource
	DirSrc *DirModuleSource
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

func (src *ModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var pbDefs []*pb.Definition
	if src.ContextDirectory.Self() != nil {
		defs, err := src.ContextDirectory.Self().PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		pbDefs = append(pbDefs, defs...)
	}
	for _, dep := range src.Dependencies {
		if dep.Self() == nil {
			continue
		}
		defs, err := dep.Self().PBDefinitions(ctx)
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

// CalcDigest calculates a content-hash of the module source. It is used during codegen; two module
// sources with the same digest will share cache for codegen-related calls.
func (src *ModuleSource) CalcDigest(ctx context.Context) digest.Digest {
	inputs := []string{
		moduleSourceHashMix,
		src.ModuleOriginalName,
		src.SourceRootSubpath,
		src.SourceSubpath,
		src.ContextDirectory.ID().Digest().String(),
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
		inputs = append(inputs, dep.Self().Digest)
	}

	// Include blueprint in digest so changes to blueprint invalidate cache
	if src.Blueprint.Self() != nil {
		inputs = append(inputs, "blueprint:"+src.Blueprint.Self().Digest)
	}

	// Include toolchains in digest so changes to toolchains invalidate cache
	for _, toolchain := range src.Toolchains {
		if toolchain.Self() == nil {
			continue
		}
		inputs = append(inputs, "toolchain:"+toolchain.Self().Digest)
	}

	for _, client := range src.ConfigClients {
		inputs = append(inputs, client.Generator, client.Directory)
	}

	return hashutil.HashStrings(inputs...)
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

	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	mainClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}
	if err := query.AddClientResourcesFromID(ctx, &resource.ID{ID: *inst.ID()}, mainClientMetadata.ClientID, false); err != nil {
		return inst, fmt.Errorf("failed to add client resources from directory source: %w", err)
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
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit api: %w", err)
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
			if err := dag.Select(ctx, dag.Root(), &ctxDir,
				dagql.Selector{
					Field: "directory",
				},
				dagql.Selector{
					Field: "withDirectory",
					Args: append([]dagql.NamedInput{
						{Name: "path", Value: dagql.String("/")},
						{Name: "source", Value: dagql.NewID[*Directory](ctxDir.ID())},
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
			if err := dag.Select(ctx, dag.Root(), &ctxDir,
				dagql.Selector{
					Field: "directory",
				},
				dagql.Selector{
					Field: "withDirectory",
					Args: append([]dagql.NamedInput{
						{Name: "path", Value: dagql.String("/")},
						{Name: "source", Value: dagql.NewID[*Directory](ctxDir.ID())},
					}, filterInputs...),
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

	mainClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}
	if err := query.AddClientResourcesFromID(ctx, &resource.ID{ID: *inst.ID()}, mainClientMetadata.ClientID, false); err != nil {
		return inst, fmt.Errorf("failed to add client resources from directory source: %w", err)
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

func (src *GitModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	if src.UnfilteredContextDir.Self() == nil {
		return nil, nil
	}
	return src.UnfilteredContextDir.Self().PBDefinitions(ctx)
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
				if errors.Is(err, cache.ErrCacheRecursiveCall) {
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
