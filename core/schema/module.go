package schema

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/graphql"
	"github.com/iancoleman/strcase"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"golang.org/x/sync/errgroup"
)

type moduleSchema struct {
	*MergedSchemas
	moduleCache       *core.CacheMap[digest.Digest, *core.Module]
	dependenciesCache *core.CacheMap[digest.Digest, []*core.Module]
}

var _ ExecutableSchema = &moduleSchema{}

func (s *moduleSchema) Name() string {
	return "module"
}

func (s *moduleSchema) Schema() string {
	return strings.Join([]string{Module, Function, InternalSDK}, "\n")
}

func (s *moduleSchema) Dependencies() []ExecutableSchema {
	return nil
}

func (s *moduleSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"module":              ToResolver(s.module),
			"currentModule":       ToResolver(s.currentModule),
			"function":            ToResolver(s.function),
			"currentFunctionCall": ToResolver(s.currentFunctionCall),
			"typeDef":             ToResolver(s.typeDef),
			"generatedCode":       ToResolver(s.generatedCode),
			"moduleConfig":        ToResolver(s.moduleConfig),
		},
		"Directory": ObjectResolver{
			"asModule": ToResolver(s.directoryAsModule),
		},
		"FunctionCall": ObjectResolver{
			"returnValue": ToVoidResolver(s.functionCallReturnValue),
			"parent":      ToResolver(s.functionCallParent),
		},
	}

	ResolveIDable[core.Module](rs, "Module", ObjectResolver{
		"dependencies":  ToResolver(s.moduleDependencies),
		"objects":       ToResolver(s.moduleObjects),
		"withObject":    ToResolver(s.moduleWithObject),
		"generatedCode": ToResolver(s.moduleGeneratedCode),
		"serve":         ToVoidResolver(s.moduleServe),
	})

	ResolveIDable[core.Function](rs, "Function", ObjectResolver{
		"withDescription": ToResolver(s.functionWithDescription),
		"withArg":         ToResolver(s.functionWithArg),
	})

	ResolveIDable[core.FunctionArg](rs, "FunctionArg", ObjectResolver{})

	ResolveIDable[core.TypeDef](rs, "TypeDef", ObjectResolver{
		"kind":         ToResolver(s.typeDefKind),
		"withOptional": ToResolver(s.typeDefWithOptional),
		"withKind":     ToResolver(s.typeDefWithKind),
		"withListOf":   ToResolver(s.typeDefWithListOf),
		"withObject":   ToResolver(s.typeDefWithObject),
		"withField":    ToResolver(s.typeDefWithObjectField),
		"withFunction": ToResolver(s.typeDefWithObjectFunction),
	})

	ResolveIDable[core.GeneratedCode](rs, "GeneratedCode", ObjectResolver{
		"withVCSIgnoredPaths":   ToResolver(s.generatedCodeWithVCSIgnoredPaths),
		"withVCSGeneratedPaths": ToResolver(s.generatedCodeWithVCSGeneratedPaths),
	})

	return rs
}

func (s *moduleSchema) typeDef(ctx context.Context, _ *core.Query, args struct {
	ID   core.TypeDefID
	Kind core.TypeDefKind
}) (*core.TypeDef, error) {
	if args.ID != "" {
		return args.ID.Decode()
	}
	return &core.TypeDef{
		Kind: args.Kind,
	}, nil
}

func (s *moduleSchema) typeDefWithOptional(ctx context.Context, def *core.TypeDef, args struct {
	Optional bool
}) (*core.TypeDef, error) {
	return def.WithOptional(args.Optional), nil
}

func (s *moduleSchema) typeDefWithKind(ctx context.Context, def *core.TypeDef, args struct {
	Kind core.TypeDefKind
}) (*core.TypeDef, error) {
	return def.WithKind(args.Kind), nil
}

func (s *moduleSchema) typeDefWithListOf(ctx context.Context, def *core.TypeDef, args struct {
	ElementType core.TypeDefID
}) (*core.TypeDef, error) {
	elemType, err := args.ElementType.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithListOf(elemType), nil
}

func (s *moduleSchema) typeDefWithObject(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string
}) (*core.TypeDef, error) {
	return def.WithObject(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithObjectField(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	TypeDef     core.TypeDefID
	Description string
}) (*core.TypeDef, error) {
	fieldType, err := args.TypeDef.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithObjectField(args.Name, fieldType, args.Description)
}

func (s *moduleSchema) typeDefWithObjectFunction(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
}) (*core.TypeDef, error) {
	fn, err := args.Function.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithObjectFunction(fn)
}

func (s *moduleSchema) typeDefKind(ctx context.Context, def *core.TypeDef, args any) (string, error) {
	return def.Kind.String(), nil
}

func (s *moduleSchema) generatedCode(ctx context.Context, _ *core.Query, args struct {
	Code core.DirectoryID
}) (*core.GeneratedCode, error) {
	dir, err := args.Code.Decode()
	if err != nil {
		return nil, err
	}
	return core.NewGeneratedCode(dir), nil
}

func (s *moduleSchema) generatedCodeWithVCSIgnoredPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSIgnoredPaths(args.Paths), nil
}

func (s *moduleSchema) generatedCodeWithVCSGeneratedPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSGeneratedPaths(args.Paths), nil
}

type moduleArgs struct {
	ID core.ModuleID
}

func (s *moduleSchema) module(ctx context.Context, query *core.Query, args moduleArgs) (*core.Module, error) {
	if args.ID == "" {
		return core.NewModule(s.platform, query.PipelinePath()), nil
	}
	return args.ID.Decode()
}

type moduleConfigArgs struct {
	SourceDirectory core.DirectoryID
	Subpath         string
}

func (s *moduleSchema) moduleConfig(ctx context.Context, query *core.Query, args moduleConfigArgs) (*modules.Config, error) {
	srcDir, err := args.SourceDirectory.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode source directory: %w", err)
	}

	_, cfg, err := core.LoadModuleConfig(ctx, s.bk, s.services, srcDir, args.Subpath)
	return cfg, err
}

func (s *moduleSchema) currentModule(ctx context.Context, _ *core.Query, _ any) (*core.Module, error) {
	// The caller should have been given a digest of the ModuleContext its executing in, which is passed along
	// as request context metadata.
	return s.MergedSchemas.currentModule(ctx)
}

func (s *moduleSchema) function(ctx context.Context, _ *core.Query, args struct {
	Name       string
	ReturnType core.TypeDefID
}) (*core.Function, error) {
	returnType, err := args.ReturnType.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode return type: %w", err)
	}
	return core.NewFunction(args.Name, returnType), nil
}

func (s *moduleSchema) currentFunctionCall(ctx context.Context, _ *core.Query, _ any) (*core.FunctionCall, error) {
	return s.MergedSchemas.currentFunctionCall(ctx)
}

type asModuleArgs struct {
	SourceSubpath string
}

func (s *moduleSchema) directoryAsModule(ctx context.Context, sourceDir *core.Directory, args asModuleArgs) (_ *core.Module, rerr error) {
	defer func() {
		if err := recover(); err != nil {
			debug.PrintStack()
			rerr = fmt.Errorf("panic in directoryAsModule: %v %s", err, string(debug.Stack()))
		}
	}()

	mod := core.NewModule(s.platform, sourceDir.Pipeline)

	mod, err := mod.FromConfig(ctx, s.bk, s.services, s.progSockPath, sourceDir, args.SourceSubpath, s.runtimeForModule)
	if err != nil {
		return nil, fmt.Errorf("failed to create module from config: %w", err)
	}

	return mod, nil
}

func (s *moduleSchema) moduleGeneratedCode(ctx context.Context, mod *core.Module, _ any) (*core.GeneratedCode, error) {
	sdk, err := s.sdkForModule(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk for module %s: %w", mod.Name, err)
	}

	return sdk.Codegen(ctx, mod)
}

func (s *moduleSchema) moduleServe(ctx context.Context, mod *core.Module, args any) (rerr error) {
	defer func() {
		if err := recover(); err != nil {
			rerr = fmt.Errorf("panic in moduleServe: %s\n%s", err, debug.Stack())
		}
	}()

	mod, err := s.loadModuleTypes(ctx, mod)
	if err != nil {
		return fmt.Errorf("failed to load module types: %w", err)
	}

	schemaView, err := s.currentSchemaView(ctx)
	if err != nil {
		return err
	}

	err = s.serveModuleToView(ctx, mod, schemaView)
	if err != nil {
		return err
	}
	return nil
}

func (s *moduleSchema) moduleObjects(ctx context.Context, mod *core.Module, _ any) ([]*core.TypeDef, error) {
	mod, err := s.loadModuleTypes(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to load module types: %w", err)
	}
	return mod.Objects, nil
}

func (s *moduleSchema) moduleWithObject(ctx context.Context, module *core.Module, args struct {
	Object core.TypeDefID
}) (_ *core.Module, rerr error) {
	def, err := args.Object.Decode()
	if err != nil {
		return nil, err
	}
	return module.WithObject(def)
}

func (s *moduleSchema) functionCallReturnValue(ctx context.Context, fnCall *core.FunctionCall, args struct{ Value any }) error {
	// TODO: error out if caller is not coming from a module

	valueBytes, err := json.Marshal(args.Value)
	if err != nil {
		return fmt.Errorf("failed to marshal function return value: %w", err)
	}

	// The return is implemented by exporting the result back to the caller's filesystem. This ensures that
	// the result is cached as part of the module function's Exec while also keeping SDKs as agnostic as possible
	// to the format + location of that result.
	return s.bk.IOReaderExport(ctx, bytes.NewReader(valueBytes), filepath.Join(core.ModMetaDirPath, core.ModMetaOutputPath), 0600)
}

func (s *moduleSchema) functionCallParent(ctx context.Context, fnCall *core.FunctionCall, _ any) (any, error) {
	if fnCall.Parent == nil {
		return struct{}{}, nil
	}
	return fnCall.Parent, nil
}

func (s *moduleSchema) functionWithDescription(ctx context.Context, fn *core.Function, args struct {
	Description string
}) (*core.Function, error) {
	return fn.WithDescription(args.Description), nil
}

func (s *moduleSchema) functionWithArg(ctx context.Context, fn *core.Function, args struct {
	Name         string
	TypeDef      core.TypeDefID
	Description  string
	DefaultValue any
}) (*core.Function, error) {
	argType, err := args.TypeDef.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}
	return fn.WithArg(args.Name, argType, args.Description, args.DefaultValue), nil
}

type functionCallArgs struct {
	Input              []*core.CallInput
	ParentOriginalName string
	Parent             any
	Module             *core.Module
	Cache              bool
}

func (s *moduleSchema) functionCall(ctx context.Context, fn *core.Function, args functionCallArgs) (any, error) {
	// TODO: if return type non-null, assert on that here

	// will already be set for internal calls, which close over a fn that doesn't
	// have ModuleID set yet
	mod := args.Module

	if mod == nil {
		return nil, fmt.Errorf("function %s has no module", fn.Name)
	}

	callParams := &core.FunctionCall{
		Name:       fn.OriginalName,
		ParentName: args.ParentOriginalName,
		Parent:     args.Parent,
		InputArgs:  args.Input,
	}

	schemaView, moduleContextDigest, err := s.registerModuleFunctionCall(mod, callParams)
	if err != nil {
		return nil, fmt.Errorf("failed to handle module function call: %w", err)
	}

	ctr := mod.Runtime

	metaDir := core.NewScratchDirectory(mod.Pipeline, mod.Platform)
	ctr, err = ctr.WithMountedDirectory(ctx, s.bk, core.ModMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount mod metadata directory: %w", err)
	}

	// Mount in read-only dep module filesystems to ensure that if they change, this module's cache is
	// also invalidated. Read-only forces buildkit to always use content-based cache keys.
	deps, err := s.dependenciesOf(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to get module dependencies: %w", err)
	}
	for _, dep := range deps {
		dirMntPath := filepath.Join(core.ModMetaDirPath, core.ModMetaDepsDirPath, dep.Name, "dir")
		sourceDir, err := dep.SourceDirectory.Directory(ctx, s.bk, s.services, dep.SourceDirectorySubpath)
		if err != nil {
			return nil, fmt.Errorf("failed to mount dep directory: %w", err)
		}
		ctr, err = ctr.WithMountedDirectory(ctx, s.bk, dirMntPath, sourceDir, "", true)
		if err != nil {
			return nil, fmt.Errorf("failed to mount dep directory: %w", err)
		}
	}

	// Also mount in the function call parameters so they are part of the exec cache key
	callParamsBytes, err := json.Marshal(callParams)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}
	inputFileDir, err := core.NewScratchDirectory(mod.Pipeline, mod.Platform).WithNewFile(ctx, core.ModMetaInputPath, callParamsBytes, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create input file: %w", err)
	}
	inputFile, err := inputFileDir.File(ctx, s.bk, s.services, core.ModMetaInputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get input file: %w", err)
	}
	ctr, err = ctr.WithMountedFile(ctx, s.bk, filepath.Join(core.ModMetaDirPath, core.ModMetaInputPath), inputFile, "", true)
	if err != nil {
		return nil, fmt.Errorf("failed to mount input file: %w", err)
	}

	if !args.Cache { // TODO: allow caching for calls coming from "inside the house"
		// [shykes] inject a cachebuster before runtime exec,
		// to fix crippling mandatory memoization of all functions.
		// [sipsma] use the ServerID so that we only bust once-per-session and thus avoid exponential runtime complexity
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		busterKey := base64.StdEncoding.EncodeToString([]byte(clientMetadata.ServerID))
		busterTon := core.NewScratchDirectory(mod.Pipeline, mod.Platform)
		ctr, err = ctr.WithMountedDirectory(ctx, s.bk, "/"+busterKey, busterTon, "", true)
		if err != nil {
			return nil, fmt.Errorf("failed to inject session cache key: %s", err)
		}
	}

	// Setup the Exec for the Function call and evaluate it
	ctr, err = ctr.WithExec(ctx, s.bk, s.progSockPath, mod.Platform, core.ContainerExecOpts{
		ModuleContextDigest:           moduleContextDigest,
		ExperimentalPrivilegedNesting: true,
		NestedInSameSession:           true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec function: %w", err)
	}
	ctrOutputDir, err := ctr.Directory(ctx, s.bk, s.services, core.ModMetaDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get function output directory: %w", err)
	}

	result, err := ctrOutputDir.Evaluate(ctx, s.bk, s.services)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate function: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("function returned nil result")
	}

	// TODO: if any error happens below, we should really prune the cache of the result, otherwise
	// we can end up in a state where we have a cached result with a dependency blob that we don't
	// guarantee the continued existence of...

	/* TODO: re-add support for interpreting exit code
	exitCodeStr, err := ctr.MetaFileContents(ctx, s.bk, s.progSockPath, "exitCode")
	if err != nil {
		return nil, fmt.Errorf("failed to read function exit code: %w", err)
	}
	exitCodeUint64, err := strconv.ParseUint(exitCodeStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse function exit code: %w", err)
	}
	exitCode := uint32(exitCodeUint64)
	*/

	// Read the output of the function
	outputBytes, err := result.Ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: core.ModMetaOutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read function output file: %w", err)
	}

	var rawOutput any
	if err := json.Unmarshal(outputBytes, &rawOutput); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %s", err)
	}

	if err := s.linkDependencyBlobs(ctx, result, rawOutput, fn.ReturnType, schemaView); err != nil {
		return nil, fmt.Errorf("failed to link dependency blobs: %w", err)
	}
	return rawOutput, nil
}

// Utilities not in the schema

// If the result of a Function call contains IDs of resources, we need to ensure that the cache entry for the
// Function call is linked for the cache entries of those resources if those entries aren't reproducible.
// Right now, the only unreproducible output are local dir imports, which are represented as blob:// sources.
// linkDependencyBlobs finds all such blob:// sources and adds a cache lease on that blob in the content store
// to the cacheResult of the function call.
//
// If we didn't do this, then it would be possible for Buildkit to prune the content pointed to by the blob://
// source without pruning the function call cache entry. That would result callers being able to evaluate the
// result of a function call but hitting an error about missing content.
func (s *moduleSchema) linkDependencyBlobs(ctx context.Context, cacheResult *buildkit.Result, value any, typeDef *core.TypeDef, schemaView *schemaView) error {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger,
		core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return nil
	case core.TypeDefKindList:
		listValue, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected list value, got %T", value)
		}
		for _, elem := range listValue {
			if err := s.linkDependencyBlobs(ctx, cacheResult, elem, typeDef.AsList.ElementTypeDef, schemaView); err != nil {
				return fmt.Errorf("failed to link dependency blobs: %w", err)
			}
		}
		return nil
	case core.TypeDefKindObject:
		fromID := s.createIDResolver(typeDef, schemaView)
		if fromID != nil {
			var err error
			value, err = fromID(value)
			if err != nil {
				return err
			}
		}

		if mapValue, ok := value.(map[string]any); ok {
			// This object is not a core type but we still need to check its
			// Fields for any objects that may contain core objects
			for fieldName, fieldValue := range mapValue {
				field, ok := typeDef.AsObject.FieldByName(fieldName)
				if !ok {
					continue
				}
				if err := s.linkDependencyBlobs(ctx, cacheResult, fieldValue, field.TypeDef, schemaView); err != nil {
					return fmt.Errorf("failed to link dependency blobs: %w", err)
				}
			}
			return nil
		}

		if pbDefinitioner, ok := value.(core.HasPBDefinitions); ok {
			pbDefs, err := pbDefinitioner.PBDefinitions()
			if err != nil {
				return fmt.Errorf("failed to get pb definitions: %w", err)
			}
			dependencyBlobs := map[digest.Digest]*ocispecs.Descriptor{}
			for _, pbDef := range pbDefs {
				dag, err := buildkit.DefToDAG(pbDef)
				if err != nil {
					return fmt.Errorf("failed to convert pb definition to dag: %w", err)
				}
				blobs, err := dag.BlobDependencies()
				if err != nil {
					return fmt.Errorf("failed to get blob dependencies: %w", err)
				}
				for k, v := range blobs {
					dependencyBlobs[k] = v
				}
			}

			if err := cacheResult.Ref.AddDependencyBlobs(ctx, dependencyBlobs); err != nil {
				return fmt.Errorf("failed to add dependency blob: %w", err)
			}
			return nil
		}

		// no dependency blobs to handle
		return nil
	default:
		return fmt.Errorf("unhandled type def kind %q", typeDef.Kind)
	}
}

// Check to see if the given object name is one of our core API's IDable types, returning the
// IDableObjectResolver if so.
func (s *moduleSchema) idableObjectResolver(objName string, dest *schemaView) (IDableObjectResolver, bool) {
	objName = gqlObjectName(objName)
	resolver, ok := dest.resolvers()[objName]
	if !ok {
		return nil, false
	}
	idableResolver, ok := resolver.(IDableObjectResolver)
	return idableResolver, ok
}

// Each Module gets its own isolated "schema view" where the core API plus its direct deps are served.
// serveModuleToDigest stitches in the schema for the given mod to the given schema view.
func (s *moduleSchema) serveModuleToView(ctx context.Context, mod *core.Module, schemaView *schemaView) error {
	mod, err := s.loadModuleTypes(ctx, mod)
	if err != nil {
		return fmt.Errorf("failed to load dep module functions: %w", err)
	}

	cacheKey := digest.FromString(mod.Name + "." + schemaView.viewDigest.String())

	// TODO: it makes no sense to use this cache since we don't need a core.Module, but also doesn't hurt, but make a separate one anyways for clarity
	_, err = s.moduleCache.GetOrInitialize(cacheKey, func() (*core.Module, error) {
		typeSchema, querySchema, err := s.moduleToSchemaFor(ctx, mod)
		if err != nil {
			return nil, fmt.Errorf("failed to convert module to executable schema: %w", err)
		}
		if err := schemaView.addSchemas(typeSchema, querySchema); err != nil {
			return nil, fmt.Errorf("failed to install module schema: %w", err)
		}
		return mod, nil
	})
	return err
}

// loadModuleTypes invokes the Module to ask for the Objects+Functions it defines and returns the updated
// Module object w/ those TypeDefs included.
func (s *moduleSchema) loadModuleTypes(ctx context.Context, mod *core.Module) (*core.Module, error) {
	// We use the base digest as cache key because loadModuleTypes should behave idempotently,
	// returning the same Module object whether or not its Objects+Runtime were already loaded.
	dgst, err := mod.BaseDigest()
	if err != nil {
		return nil, fmt.Errorf("failed to get module digest: %w", err)
	}

	return s.moduleCache.GetOrInitialize(dgst, func() (*core.Module, error) {
		schemaView, err := s.installDeps(ctx, mod)
		if err != nil {
			return nil, fmt.Errorf("failed to install module dependencies: %w", err)
		}

		// canned function for asking the SDK to return the module + functions it provides
		getModDefFn := core.NewFunction(
			"", // no name indicates that the SDK should return the module
			&core.TypeDef{
				Kind:     core.TypeDefKindObject,
				AsObject: core.NewObjectTypeDef("Module", ""),
			},
		)
		result, err := s.functionCall(ctx, getModDefFn, functionCallArgs{
			Module: mod,
			Cache:  true,
			// ParentName is empty to signify we're querying to get the constructed module
		})
		if err != nil {
			return nil, fmt.Errorf("failed to call module %q to get functions: %w", mod.Name, err)
		}
		idStr, ok := result.(string)
		if !ok {
			return nil, fmt.Errorf("expected string result, got %T", result)
		}
		mod, err = core.ModuleID(idStr).Decode()
		if err != nil {
			return nil, fmt.Errorf("failed to decode module: %w", err)
		}

		for _, obj := range mod.Objects {
			if err := s.validateTypeDef(obj, schemaView); err != nil {
				return nil, fmt.Errorf("failed to validate type def: %w", err)
			}

			// namespace the module objects + function extensions
			s.namespaceTypeDef(obj, mod, schemaView)
		}

		return mod, nil
	})
}

func (s *moduleSchema) moduleDependencies(ctx context.Context, mod *core.Module, _ any) ([]*core.Module, error) {
	return s.dependenciesOf(ctx, mod)
}

func (s *moduleSchema) dependenciesOf(ctx context.Context, mod *core.Module) ([]*core.Module, error) {
	dgst, err := mod.BaseDigest()
	if err != nil {
		return nil, fmt.Errorf("failed to get module digest: %w", err)
	}

	return s.dependenciesCache.GetOrInitialize(dgst, func() ([]*core.Module, error) {
		var eg errgroup.Group
		deps := make([]*core.Module, len(mod.DependencyConfig))
		for i, depURL := range mod.DependencyConfig {
			i, depURL := i, depURL
			eg.Go(func() error {
				depMod, err := core.NewModule(mod.Platform, mod.Pipeline).FromRef(
					ctx, s.bk, s.services, s.progSockPath,
					mod.SourceDirectory,
					mod.SourceDirectorySubpath,
					depURL,
					s.runtimeForModule,
				)
				if err != nil {
					return fmt.Errorf("failed to get dependency mod from ref %q: %w", depURL, err)
				}
				deps[i] = depMod
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}
		return deps, nil
	})
}

// installDeps stitches in the schemas for all the deps of the given module to the module's
// schema view.
func (s *moduleSchema) installDeps(ctx context.Context, module *core.Module) (*schemaView, error) {
	schemaView, err := s.getModuleSchemaView(module)
	if err != nil {
		return nil, err
	}

	deps, err := s.dependenciesOf(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("failed to get module dependencies: %w", err)
	}

	return schemaView, s.installDepsToSchemaView(ctx, deps, schemaView)
}

func (s *moduleSchema) installDepsToSchemaView(
	ctx context.Context,
	deps []*core.Module,
	schemaView *schemaView,
) error {
	var eg errgroup.Group
	for _, dep := range deps {
		dep := dep
		eg.Go(func() error {
			if err := s.serveModuleToView(ctx, dep, schemaView); err != nil {
				return fmt.Errorf("failed to install dependency %q: %w", dep.Name, err)
			}
			return nil
		})
	}
	return eg.Wait()
}

// moduleToSchema converts a Module to an ExecutableSchema that can be stitched
// in to an existing schema. It presumes that the Module's Functions have
// already been loaded.
//
// It returns two separate ExecutableSchemas - one containing just the core
// types, and another containing any top-level extensions. This is so that we
// can stitch in our transitive dependency types, but not include the top-level
// access to them.
func (s *moduleSchema) moduleToSchemaFor(ctx context.Context, module *core.Module) (ExecutableSchema, ExecutableSchema, error) {
	schemaView, err := s.getModuleSchemaView(module)
	if err != nil {
		return nil, nil, err
	}

	typeSchemaDoc := &ast.SchemaDocument{}
	querySchemaDoc := &ast.SchemaDocument{}

	typeSchemaResolvers := Resolvers{}
	querySchemaResolver := ObjectResolver{}

	for _, def := range module.Objects {
		def := def
		objTypeDef := def.AsObject
		objName := gqlObjectName(objTypeDef.Name)

		// get the schema + resolvers for the object as a whole
		objType, err := s.typeDefToSchema(def, false)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert module to schema: %w", err)
		}

		// check whether this is a pre-existing object (from core or another module)
		_, preExistingObject := schemaView.resolvers()[objName]
		if preExistingObject {
			// modules can reference types from core/other modules as types, but they
			// can't attach any new fields or functions to them
			if len(objTypeDef.Fields) > 0 || len(objTypeDef.Functions) > 0 {
				return nil, nil, fmt.Errorf("cannot attach new fields or functions to object %q from outside module", objName)
			}
			continue
		}

		astDef := &ast.Definition{
			Name:        objName,
			Description: formatGqlDescription(objTypeDef.Description),
			Kind:        ast.Object,
		}
		astIDDef := &ast.Definition{
			Name:        objName + "ID",
			Description: formatGqlDescription("A reference to %s", objName),
			Kind:        ast.Scalar,
		}
		astLoadDef := &ast.FieldDefinition{
			Name:        fmt.Sprintf("load%sFromID", objName),
			Description: formatGqlDescription("Loads a %s from an ID", objName),
			Arguments: ast.ArgumentDefinitionList{
				&ast.ArgumentDefinition{
					Name: "id",
					Type: ast.NonNullNamedType(objName+"ID", nil),
				},
			},
			Type: ast.NonNullNamedType(objName, nil),
		}

		newObjResolver := ObjectResolver{}

		astDef.Fields = append(astDef.Fields, &ast.FieldDefinition{
			Name:        "id",
			Description: formatGqlDescription("A unique identifier for a %s", objName),
			Type:        ast.NonNullNamedType(objName+"ID", nil),
		})
		newObjResolver["id"] = func(p graphql.ResolveParams) (any, error) {
			return resourceid.EncodeModule(objName, p.Source)
		}

		for _, field := range objTypeDef.Fields {
			field := field

			fieldASTType, err := s.typeDefToSchema(field.TypeDef, false)
			if err != nil {
				return nil, nil, err
			}
			astDef.Fields = append(astDef.Fields, &ast.FieldDefinition{
				Name:        field.Name,
				Description: formatGqlDescription(field.Description),
				Type:        fieldASTType,
			})

			fromID := s.createIDResolver(field.TypeDef, schemaView)
			newObjResolver[field.Name] = func(p graphql.ResolveParams) (any, error) {
				p.Info.FieldName = field.OriginalName
				res, err := graphql.DefaultResolveFn(p)
				if err != nil {
					return nil, err
				}
				if fromID != nil {
					return fromID(res)
				}
				return res, nil
			}
		}

		for _, fn := range objTypeDef.Functions {
			resolver, err := s.functionResolver(astDef, module, fn, schemaView)
			if err != nil {
				return nil, nil, err
			}
			newObjResolver[gqlFieldName(fn.Name)] = resolver
		}

		if len(newObjResolver) > 0 {
			typeSchemaResolvers[objName] = newObjResolver
			typeSchemaResolvers[objName+"ID"] = stringResolver[string]()

			fromID := s.createIDResolver(def, schemaView)
			query, ok := typeSchemaResolvers["Query"]
			if !ok {
				query = ObjectResolver{}
				typeSchemaResolvers["Query"] = query
			}
			query.(ObjectResolver)[fmt.Sprintf("load%sFromID", objName)] = func(p graphql.ResolveParams) (any, error) {
				return fromID(p.Args["id"])
			}
		}

		if len(astDef.Fields) > 0 {
			typeSchemaDoc.Definitions = append(typeSchemaDoc.Definitions, astDef, astIDDef)
			typeSchemaDoc.Extensions = append(typeSchemaDoc.Extensions, &ast.Definition{
				Name:   "Query",
				Kind:   ast.Object,
				Fields: ast.FieldList{astLoadDef},
			})
		}

		if constructorName := gqlFieldName(def.AsObject.Name); constructorName == gqlFieldName(module.Name) {
			// stitch in the module object right under Query
			querySchemaDoc.Extensions = append(querySchemaDoc.Extensions, &ast.Definition{
				Name: "Query",
				Kind: ast.Object,
				Fields: ast.FieldList{
					{
						Name: constructorName,
						// TODO is it correct to set it here too vs. type definition?
						// Description: def.AsObject.Description,
						Type: objType,
					},
				},
			})
			querySchemaResolver[constructorName] = PassthroughResolver
		}
	}

	buf := &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(typeSchemaDoc)
	typeSchemaStr := buf.String()
	buf = &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(querySchemaDoc)
	querySchemaStr := buf.String()

	deps, err := s.dependenciesOf(ctx, module)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get module dependencies: %w", err)
	}
	depTypeSchemas := make([]ExecutableSchema, 0, len(deps))
	for _, dep := range deps {
		dep, err := s.loadModuleTypes(ctx, dep)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load dep module functions: %w", err)
		}
		typeSchema, _, err := s.moduleToSchemaFor(ctx, dep)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert module to executable schema: %w", err)
		}
		depTypeSchemas = append(depTypeSchemas, typeSchema)
	}

	typeSchema := StaticSchema(StaticSchemaParams{
		Name:         module.Name + ".types",
		Schema:       typeSchemaStr,
		Resolvers:    typeSchemaResolvers,
		Dependencies: depTypeSchemas,
	})
	querySchema := StaticSchema(StaticSchemaParams{
		Name:         module.Name + ".query",
		Schema:       querySchemaStr,
		Resolvers:    Resolvers{"Query": querySchemaResolver},
		Dependencies: []ExecutableSchema{typeSchema},
	})
	return typeSchema, querySchema, nil
}

/*
This formats comments in the schema as:
"""
comment
"""

Which avoids corner cases where the comment ends in a `"`.
*/
func formatGqlDescription(desc string, args ...any) string {
	return "\n" + strings.TrimSpace(fmt.Sprintf(desc, args...)) + "\n"
}

func (s *moduleSchema) typeDefToSchema(typeDef *core.TypeDef, isInput bool) (*ast.Type, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString:
		return &ast.Type{
			NamedType: "String",
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindInteger:
		return &ast.Type{
			NamedType: "Int",
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindBoolean:
		return &ast.Type{
			NamedType: "Boolean",
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindVoid:
		return &ast.Type{
			NamedType: "Void",
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindList:
		if typeDef.AsList == nil {
			return nil, fmt.Errorf("expected list type def, got nil")
		}
		astType, err := s.typeDefToSchema(typeDef.AsList.ElementTypeDef, isInput)
		if err != nil {
			return nil, err
		}
		return &ast.Type{
			Elem:    astType,
			NonNull: !typeDef.Optional,
		}, nil
	case core.TypeDefKindObject:
		if typeDef.AsObject == nil {
			return nil, fmt.Errorf("expected object type def, got nil")
		}
		objTypeDef := typeDef.AsObject
		objName := gqlObjectName(objTypeDef.Name)
		if isInput {
			// idable types use their ID scalar as the input value
			return &ast.Type{NamedType: objName + "ID", NonNull: !typeDef.Optional}, nil
		}
		return &ast.Type{NamedType: objName, NonNull: !typeDef.Optional}, nil
	default:
		return nil, fmt.Errorf("unsupported type kind %q", typeDef.Kind)
	}
}

func (s *moduleSchema) functionResolver(
	parentObj *ast.Definition,
	module *core.Module,
	fn *core.Function,
	schemaView *schemaView,
) (graphql.FieldResolveFn, error) {
	fnName := gqlFieldName(fn.Name)
	objFnName := fmt.Sprintf("%s.%s", parentObj.Name, fnName)

	returnASTType, err := s.typeDefToSchema(fn.ReturnType, false)
	if err != nil {
		return nil, err
	}

	var argASTTypes ast.ArgumentDefinitionList
	for _, fnArg := range fn.Args {
		argASTType, err := s.typeDefToSchema(fnArg.TypeDef, true)
		if err != nil {
			return nil, err
		}
		defaultValue, err := astDefaultValue(fnArg.TypeDef, fnArg.DefaultValue)
		if err != nil {
			return nil, err
		}
		argASTTypes = append(argASTTypes, &ast.ArgumentDefinition{
			Name:         gqlArgName(fnArg.Name),
			Description:  formatGqlDescription(fnArg.Description),
			Type:         argASTType,
			DefaultValue: defaultValue,
		})
	}

	argNames := make(map[string]string, len(fn.Args))
	argFromIDs := make(map[string]func(any) (any, error), len(fn.Args))
	for _, arg := range fn.Args {
		argNames[arg.Name] = arg.OriginalName

		// decode args for types that are in this module
		// modules only understand IDs for external types, they only see JSON
		// representations of their own types, so we need to decode those here
		if obj := arg.TypeDef.Underlying(); obj.Kind == core.TypeDefKindObject {
			if _, ok := schemaView.resolvers()[obj.AsObject.Name]; !ok {
				argFromIDs[arg.Name] = s.createIDResolver(arg.TypeDef, schemaView)
			}
		}
	}

	fieldDef := &ast.FieldDefinition{
		Name:        fnName,
		Description: formatGqlDescription(fn.Description),
		Type:        returnASTType,
		Arguments:   argASTTypes,
	}
	parentObj.Fields = append(parentObj.Fields, fieldDef)

	// Our core "id-able" types are serialized as their ID over the wire, but need to be decoded into
	// their object here. We can identify those types since their object resolvers are wrapped in
	// ToIDableObjectResolver.
	returnFromID := s.createIDResolver(fn.ReturnType, schemaView)
	parentIDableObjectResolver, _ := s.idableObjectResolver(parentObj.Name, schemaView)

	return ToResolver(func(ctx context.Context, parent any, args map[string]any) (_ any, rerr error) {
		defer func() {
			if r := recover(); r != nil {
				rerr = fmt.Errorf("panic in %s: %s %s", objFnName, r, string(debug.Stack()))
			}
		}()
		if parentIDableObjectResolver != nil {
			id, err := parentIDableObjectResolver.ToID(parent)
			if err != nil {
				return nil, fmt.Errorf("failed to get parent ID: %w", err)
			}
			parent = id
		}

		var callInput []*core.CallInput
		for k, v := range args {
			name, ok := argNames[k]
			if !ok {
				continue
			}

			if argFromID := argFromIDs[k]; argFromID != nil {
				v, err = argFromID(v)
				if err != nil {
					return nil, fmt.Errorf("failed to decode arg %q: %w", k, err)
				}
			}

			callInput = append(callInput, &core.CallInput{
				Name:  name,
				Value: v,
			})
		}
		result, err := s.functionCall(ctx, fn, functionCallArgs{
			Module:             module,
			Input:              callInput,
			ParentOriginalName: fn.ParentOriginalName,
			Parent:             parent,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to call function %q: %w", objFnName, err)
		}
		if returnFromID != nil {
			return returnFromID(result)
		}
		return result, nil
	}), nil
}

// relevant ast code we need to work with here:
// https://github.com/vektah/gqlparser/blob/35199fce1fa1b73c27f23c84f4430f47ac93329e/ast/value.go#L44
func astDefaultValue(typeDef *core.TypeDef, val any) (*ast.Value, error) {
	if val == nil {
		// no default value for this arg
		return nil, nil
	}
	switch typeDef.Kind {
	case core.TypeDefKindString:
		strVal, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("expected string default value, got %T", val)
		}
		return &ast.Value{
			Kind: ast.StringValue,
			Raw:  strVal,
		}, nil
	case core.TypeDefKindInteger:
		var intVal int
		switch val := val.(type) {
		case int:
			intVal = val
		case float64: // JSON unmarshaling to `any'
			intVal = int(val)
		default:
			return nil, fmt.Errorf("expected integer default value, got %T", val)
		}
		return &ast.Value{
			Kind: ast.IntValue,
			Raw:  strconv.Itoa(intVal),
		}, nil
	case core.TypeDefKindBoolean:
		boolVal, ok := val.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool default value, got %T", val)
		}
		return &ast.Value{
			Kind: ast.BooleanValue,
			Raw:  strconv.FormatBool(boolVal),
		}, nil
	case core.TypeDefKindVoid:
		if val != nil {
			return nil, fmt.Errorf("expected nil value, got %T", val)
		}
		return &ast.Value{
			Kind: ast.NullValue,
			Raw:  "null",
		}, nil
	case core.TypeDefKindList:
		astVal := &ast.Value{Kind: ast.ListValue}
		// val is coming from deserializing a json string (see jsonResolver), so it should be []any
		listVal, ok := val.([]any)
		if !ok {
			return nil, fmt.Errorf("expected list default value, got %T", val)
		}
		for _, elemVal := range listVal {
			elemASTVal, err := astDefaultValue(typeDef.AsList.ElementTypeDef, elemVal)
			if err != nil {
				return nil, fmt.Errorf("failed to get default value for list element: %w", err)
			}
			astVal.Children = append(astVal.Children, &ast.ChildValue{
				Value: elemASTVal,
			})
		}
		return astVal, nil
	case core.TypeDefKindObject:
		astVal := &ast.Value{Kind: ast.ObjectValue}
		// val is coming from deserializing a json string (see jsonResolver), so it should be map[string]any
		mapVal, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected object default value, got %T", val)
		}
		for name, val := range mapVal {
			name = gqlFieldName(name)
			field, ok := typeDef.AsObject.FieldByName(name)
			if !ok {
				return nil, fmt.Errorf("object field %s.%s not found", typeDef.AsObject.Name, name)
			}
			fieldASTVal, err := astDefaultValue(field.TypeDef, val)
			if err != nil {
				return nil, fmt.Errorf("failed to get default value for object field %q: %w", name, err)
			}
			astVal.Children = append(astVal.Children, &ast.ChildValue{
				Name:  name,
				Value: fieldASTVal,
			})
		}
		return astVal, nil
	default:
		return nil, fmt.Errorf("unsupported type kind %q", typeDef.Kind)
	}
}

func (s *moduleSchema) validateTypeDef(typeDef *core.TypeDef, schemaView *schemaView) error {
	switch typeDef.Kind {
	case core.TypeDefKindList:
		return s.validateTypeDef(typeDef.AsList.ElementTypeDef, schemaView)
	case core.TypeDefKindObject:
		obj := typeDef.AsObject
		baseObjName := gqlObjectName(obj.Name)

		// check whether this is a pre-existing object from core or another module
		_, preExistingObject := schemaView.resolvers()[baseObjName]
		if preExistingObject {
			// already validated, skip
			return nil
		}

		for _, field := range obj.Fields {
			if gqlFieldName(field.Name) == "id" {
				return fmt.Errorf("cannot define field with reserved name %q on object %q", field.Name, obj.Name)
			}
			if err := s.validateTypeDef(field.TypeDef, schemaView); err != nil {
				return err
			}
		}

		for _, fn := range obj.Functions {
			if gqlFieldName(fn.Name) == "id" {
				return fmt.Errorf("cannot define function with reserved name %q on object %q", fn.Name, obj.Name)
			}
			if err := s.validateTypeDef(fn.ReturnType, schemaView); err != nil {
				return err
			}

			for _, arg := range fn.Args {
				if gqlArgName(arg.Name) == "id" {
					return fmt.Errorf("cannot define argument with reserved name %q on function %q", arg.Name, fn.Name)
				}
				if err := s.validateTypeDef(arg.TypeDef, schemaView); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// TODO: Should we handle trying to namespace the object in the doc strings? Or would that require too much magic to accomplish consistently?
func (s *moduleSchema) namespaceTypeDef(typeDef *core.TypeDef, mod *core.Module, schemaView *schemaView) {
	switch typeDef.Kind {
	case core.TypeDefKindList:
		s.namespaceTypeDef(typeDef.AsList.ElementTypeDef, mod, schemaView)
	case core.TypeDefKindObject:
		obj := typeDef.AsObject
		baseObjName := gqlObjectName(obj.Name)

		// check whether this is a pre-existing object from core or another module
		_, preExistingObject := schemaView.resolvers()[baseObjName]

		// only namespace objects defined in this module
		if !preExistingObject {
			obj.Name = namespaceObject(obj.Name, mod.Name)
		}

		for _, field := range obj.Fields {
			s.namespaceTypeDef(field.TypeDef, mod, schemaView)
		}

		for _, fn := range obj.Functions {
			s.namespaceTypeDef(fn.ReturnType, mod, schemaView)

			for _, arg := range fn.Args {
				s.namespaceTypeDef(arg.TypeDef, mod, schemaView)
			}
		}
	}
}

// createIDResolver returns a function that can be used to convert an ID to
// the object it represents.
//
// However, unlike just calling the resolver directly, this function will also
// attempt convering lists of ids into lists of objects (which is a trickier
// case, because lists are composite data types, lists of lists are possible,
// etc.)
func (s *moduleSchema) createIDResolver(typeDef *core.TypeDef, schemaView *schemaView) func(any) (any, error) {
	switch typeDef.Kind {
	case core.TypeDefKindObject:
		resolver, _ := s.idableObjectResolver(typeDef.AsObject.Name, schemaView)
		if resolver != nil {
			return func(a any) (any, error) {
				s, ok := a.(string)
				if !ok {
					return nil, fmt.Errorf("expected string %sID, got %T", typeDef.AsObject.Name, a)
				}
				return resolver.FromID(s)
			}
		} else {
			return func(a any) (any, error) {
				id, ok := a.(string)
				if !ok {
					if _, ok := a.(map[string]any); ok {
						return a, nil
					}
					return nil, fmt.Errorf("expected map %s, or string %sID, got %T", typeDef.AsObject.Name, typeDef.AsObject.Name, a)
				}

				typeName, value, err := resourceid.DecodeModule(id)
				if err != nil {
					return a, nil
				}
				if typeName != typeDef.AsObject.Name {
					// if we hit this, the module is misbehaving - it's
					// declared it returns a different module type than it's
					// actually returned
					return nil, fmt.Errorf("invalid type name %q, expected %q", typeName, typeDef.AsObject.Name)
				}
				return value, nil
			}
		}
	case core.TypeDefKindList:
		fromID := s.createIDResolver(typeDef.AsList.ElementTypeDef, schemaView)
		if fromID != nil {
			return func(a any) (any, error) {
				li, ok := a.([]any)
				if !ok {
					return nil, fmt.Errorf("expected slice, got %T", a)
				}

				res := make([]any, len(li))
				for i, elem := range li {
					fromIDVal, err := fromID(elem)
					if err != nil {
						return nil, err
					}
					res[i] = fromIDVal
				}
				return res, nil
			}
		}
	}

	return nil
}

// TODO: all these should be called during creation of Function+TypeDefs, not scattered all over the place

func gqlObjectName(name string) string {
	// gql object name is capitalized camel case
	return strcase.ToCamel(name)
}

func namespaceObject(objName, namespace string) string {
	// don't namespace the main module object itself (already is named after the module)
	if gqlObjectName(objName) == gqlObjectName(namespace) {
		return objName
	}
	return gqlObjectName(namespace + "_" + objName)
}

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}

func gqlArgName(name string) string {
	// gql arg name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}
