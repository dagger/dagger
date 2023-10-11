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
	currentSchemaView    *moduleSchemaView
	functionContextCache *FunctionContextCache
	moduleCache          *core.CacheMap[digest.Digest, *core.Module]
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
		"withObject":    ToResolver(s.moduleWithObject),
		"generatedCode": ToResolver(s.moduleGeneratedCode),
		"serve":         ToVoidResolver(s.moduleServe),
	})

	ResolveIDable[core.Function](rs, "Function", ObjectResolver{
		"withDescription": ToResolver(s.functionWithDescription),
		"withArg":         ToResolver(s.functionWithArg),
		"call":            ToResolver(s.functionCall),
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

func (s *moduleSchema) typeDef(ctx *core.Context, _ *core.Query, args struct {
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

func (s *moduleSchema) typeDefWithOptional(ctx *core.Context, def *core.TypeDef, args struct {
	Optional bool
}) (*core.TypeDef, error) {
	return def.WithOptional(args.Optional), nil
}

func (s *moduleSchema) typeDefWithKind(ctx *core.Context, def *core.TypeDef, args struct {
	Kind core.TypeDefKind
}) (*core.TypeDef, error) {
	return def.WithKind(args.Kind), nil
}

func (s *moduleSchema) typeDefWithListOf(ctx *core.Context, def *core.TypeDef, args struct {
	ElementType core.TypeDefID
}) (*core.TypeDef, error) {
	elemType, err := args.ElementType.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithListOf(elemType), nil
}

func (s *moduleSchema) typeDefWithObject(ctx *core.Context, def *core.TypeDef, args struct {
	Name        string
	Description string
}) (*core.TypeDef, error) {
	return def.WithObject(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithObjectField(ctx *core.Context, def *core.TypeDef, args struct {
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

func (s *moduleSchema) typeDefWithObjectFunction(ctx *core.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
}) (*core.TypeDef, error) {
	fn, err := args.Function.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithObjectFunction(fn)
}

func (s *moduleSchema) typeDefKind(ctx *core.Context, def *core.TypeDef, args any) (string, error) {
	return def.Kind.String(), nil
}

func (s *moduleSchema) generatedCode(ctx *core.Context, _ *core.Query, args struct {
	Code core.DirectoryID
}) (*core.GeneratedCode, error) {
	dir, err := args.Code.Decode()
	if err != nil {
		return nil, err
	}
	return core.NewGeneratedCode(dir), nil
}

func (s *moduleSchema) generatedCodeWithVCSIgnoredPaths(ctx *core.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSIgnoredPaths(args.Paths), nil
}

func (s *moduleSchema) generatedCodeWithVCSGeneratedPaths(ctx *core.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSGeneratedPaths(args.Paths), nil
}

type moduleArgs struct {
	ID core.ModuleID
}

func (s *moduleSchema) module(ctx *core.Context, query *core.Query, args moduleArgs) (*core.Module, error) {
	if args.ID == "" {
		return core.NewModule(s.platform, query.PipelinePath()), nil
	}
	return args.ID.Decode()
}

func (s *moduleSchema) currentModule(ctx *core.Context, _ *core.Query, _ any) (*core.Module, error) {
	// The caller should have been given a digest of the Module its executing in, which is passed along
	// as request context metadata.
	fnCtx, err := s.functionContextCache.FunctionContextFrom(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get function context: %w", err)
	}
	return fnCtx.Module, nil
}

func (s *moduleSchema) function(ctx *core.Context, _ *core.Query, args struct {
	Name       string
	ReturnType core.TypeDefID
}) (*core.Function, error) {
	returnType, err := args.ReturnType.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode return type: %w", err)
	}
	return core.NewFunction(args.Name, returnType), nil
}

func (s *moduleSchema) currentFunctionCall(ctx *core.Context, _ *core.Query, _ any) (*core.FunctionCall, error) {
	fnCtx, err := s.functionContextCache.FunctionContextFrom(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get function context from context: %w", err)
	}
	return fnCtx.CurrentCall, nil
}

type asModuleArgs struct {
	SourceSubpath string
}

func (s *moduleSchema) directoryAsModule(ctx *core.Context, sourceDir *core.Directory, args asModuleArgs) (_ *core.Module, rerr error) {
	defer func() {
		if err := recover(); err != nil {
			debug.PrintStack()
			rerr = fmt.Errorf("panic in directoryAsModule: %v %s", err, string(debug.Stack()))
		}
	}()

	mod := core.NewModule(s.platform, sourceDir.Pipeline)

	mod, err := mod.FromConfig(ctx, s.bk, s.services, s.progSockPath, sourceDir, args.SourceSubpath, s.runtimeForModule)
	if err != nil {
		return nil, fmt.Errorf("failed to create module from config `%s::%s`: %w", sourceDir.Dir, args.SourceSubpath, err)
	}

	return s.loadModuleTypes(ctx, mod)
}

func (s *moduleSchema) moduleGeneratedCode(ctx *core.Context, mod *core.Module, _ any) (*core.GeneratedCode, error) {
	sdk, err := s.sdkForModule(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk for module %s: %w", mod.Name, err)
	}

	return sdk.Codegen(ctx, mod.SourceDirectory, mod.SourceDirectorySubpath)
}

func (s *moduleSchema) moduleServe(ctx *core.Context, module *core.Module, args any) (rerr error) {
	defer func() {
		if err := recover(); err != nil {
			rerr = fmt.Errorf("panic in moduleServe: %s\n%s", err, debug.Stack())
		}
	}()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	err = s.serveModuleToDigest(ctx, module, clientMetadata.ModuleDigest)
	if err != nil {
		return err
	}
	return nil
}

func (s *moduleSchema) moduleWithObject(ctx *core.Context, module *core.Module, args struct {
	Object core.TypeDefID
}) (_ *core.Module, rerr error) {
	def, err := args.Object.Decode()
	if err != nil {
		return nil, err
	}
	return module.WithObject(def)
}

func (s *moduleSchema) functionCallReturnValue(ctx *core.Context, fnCall *core.FunctionCall, args struct{ Value any }) error {
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

func (s *moduleSchema) functionCallParent(ctx *core.Context, fnCall *core.FunctionCall, _ any) (any, error) {
	if fnCall.Parent == nil {
		return struct{}{}, nil
	}
	return fnCall.Parent, nil
}

func (s *moduleSchema) functionWithDescription(ctx *core.Context, fn *core.Function, args struct {
	Description string
}) (*core.Function, error) {
	return fn.WithDescription(args.Description), nil
}

func (s *moduleSchema) functionWithArg(ctx *core.Context, fn *core.Function, args struct {
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
	Input []*core.CallInput

	// Below are not in public API, used internally by Function.call api
	ParentName string
	Parent     any
	Module     *core.Module
	Cache      bool
}

func (s *moduleSchema) functionCall(ctx *core.Context, fn *core.Function, args functionCallArgs) (any, error) {
	// TODO: if return type non-null, assert on that here
	// TODO: handle setting default values, they won't be set when going through "dynamic call" codepath

	// TODO: re-add support for different exit codes
	cacheExitCode := uint32(0)

	// will already be set for internal calls, which close over a fn that doesn't
	// have ModuleID set yet
	mod := args.Module

	if mod == nil {
		// will not be set for API calls

		if fn.ModuleID == "" {
			return nil, fmt.Errorf("function %s has no module", fn.Name)
		}

		var err error
		mod, err = fn.ModuleID.Decode()
		if err != nil {
			return nil, fmt.Errorf("failed to decode module: %w", err)
		}

		if fn.ParentName != "" {
			args.ParentName = fn.ParentName
		}
	}

	if err := s.installDeps(ctx, mod); err != nil {
		return nil, fmt.Errorf("failed to install deps: %w", err)
	}

	callParams := &core.FunctionCall{
		Name:       fn.Name,
		ParentName: args.ParentName,
		Parent:     args.Parent,
		InputArgs:  args.Input,
	}

	ctx, err := s.functionContextCache.WithFunctionContext(ctx, &FunctionContext{
		Module:      mod,
		CurrentCall: callParams,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set module in context: %w", err)
	}

	ctr := mod.Runtime

	metaDir := core.NewScratchDirectory(mod.Pipeline, mod.Platform)
	ctr, err = ctr.WithMountedDirectory(ctx, s.bk, core.ModMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount mod metadata directory: %w", err)
	}

	// Mount in read-only dep module filesystems to ensure that if they change, this module's cache is
	// also invalidated. Read-only forces buildkit to always use content-based cache keys.
	for _, dep := range mod.Dependencies {
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
		ExperimentalPrivilegedNesting: true,
		CacheExitCode:                 cacheExitCode,
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
		return nil, fmt.Errorf("failed to read function `%s.%s` output file (result -> %v): %w", mod.Name, fn.Name, result, err)
	}

	var rawOutput any
	if err := json.Unmarshal(outputBytes, &rawOutput); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %s", err)
	}

	if err := s.linkDependencyBlobs(ctx, result, rawOutput, fn.ReturnType); err != nil {
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
func (s *moduleSchema) linkDependencyBlobs(ctx context.Context, cacheResult *buildkit.Result, value any, typeDef *core.TypeDef) error {
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
			if err := s.linkDependencyBlobs(ctx, cacheResult, elem, typeDef.AsList.ElementTypeDef); err != nil {
				return fmt.Errorf("failed to link dependency blobs: %w", err)
			}
		}
		return nil
	case core.TypeDefKindObject:
		_, isIDable := s.idableObjectResolver(typeDef.AsObject.Name)
		if !isIDable {
			// This object is not IDable but we still need to check its Fields for any objects that may contain
			// IDable objects
			mapValue, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("expected object value for %s, got %T", typeDef.AsObject.Name, value)
			}
			for fieldName, fieldValue := range mapValue {
				field, ok := typeDef.AsObject.FieldByName(fieldName)
				if !ok {
					continue
				}
				if err := s.linkDependencyBlobs(ctx, cacheResult, fieldValue, field.TypeDef); err != nil {
					return fmt.Errorf("failed to link dependency blobs: %w", err)
				}
			}
			return nil
		}

		// This is an IDable core type, check to see if it has any blobs:// we need to link with.
		idStr, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected string value for id result, got %T", value)
		}

		resource, err := core.ResourceFromID(idStr)
		if err != nil {
			return fmt.Errorf("failed to get resource from ID: %w", err)
		}

		pbDefinitioner, ok := resource.(core.HasPBDefinitions)
		if !ok {
			// no dependency blobs to handle
			return nil
		}

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
	default:
		return fmt.Errorf("unhandled type def kind %q", typeDef.Kind)
	}
}

// Check to see if the given object name is one of our core API's IDable types, returning the
// IDableObjectResolver if so.
func (s *moduleSchema) idableObjectResolver(objName string) (IDableObjectResolver, bool) {
	objName = gqlObjectName(objName)
	resolver, ok := s.currentSchemaView.resolvers()[objName]
	if !ok {
		return nil, false
	}
	idableResolver, ok := resolver.(IDableObjectResolver)
	return idableResolver, ok
}

// FunctionContext holds the metadata of a function call. Used to support the currentModule and
// currentFunctionCall APIs.
type FunctionContext struct {
	Module      *core.Module
	CurrentCall *core.FunctionCall
}

func (fnCtx *FunctionContext) Digest() (digest.Digest, error) {
	modDigest, err := fnCtx.Module.Digest()
	if err != nil {
		return "", fmt.Errorf("failed to get module digest: %w", err)
	}
	callDigest, err := fnCtx.CurrentCall.Digest()
	if err != nil {
		return "", fmt.Errorf("failed to get function call digest: %w", err)
	}

	return digest.FromString(modDigest.String() + callDigest.String()), nil
}

// FunctionContextCache stores the mapping of FunctionContext's digest -> FunctionContext.
// This enables us to pass just the digest along the client metadata rather than massive
// serialized objects.
type FunctionContextCache core.CacheMap[digest.Digest, *FunctionContext]

func NewFunctionContextCache() *FunctionContextCache {
	return (*FunctionContextCache)(core.NewCacheMap[digest.Digest, *FunctionContext]())
}

func (cache *FunctionContextCache) cacheMap() *core.CacheMap[digest.Digest, *FunctionContext] {
	return (*core.CacheMap[digest.Digest, *FunctionContext])(cache)
}

func (cache *FunctionContextCache) WithFunctionContext(ctx *core.Context, fnCtx *FunctionContext) (*core.Context, error) {
	fntCtxDigest, err := fnCtx.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get function context digest: %w", err)
	}
	moduleDigest, err := fnCtx.Module.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get module digest: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}

	// TODO: maintaining two fields is annoying, could avoid if server has access to s.functionContextCache?
	clientMetadata.ModuleDigest = moduleDigest
	clientMetadata.FunctionContextDigest = fntCtxDigest
	_, err = cache.cacheMap().GetOrInitialize(fntCtxDigest, func() (*FunctionContext, error) {
		return fnCtx, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to cache function context: %w", err)
	}

	ctxCp := *ctx
	ctxCp.Context = engine.ContextWithClientMetadata(ctx.Context, clientMetadata)
	return &ctxCp, nil
}

var errFunctionContextNotFound = fmt.Errorf("function context not found")

func (cache *FunctionContextCache) FunctionContextFrom(ctx context.Context) (*FunctionContext, error) {
	// TODO: make sure this errors if not from module caller
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	return cache.cacheMap().GetOrInitialize(clientMetadata.FunctionContextDigest, func() (*FunctionContext, error) {
		return nil, errFunctionContextNotFound
	})
}

// Each Module gets its own Schema where the core API plus its direct deps are served. These "schema views"
// are keyed by the digest of the module viewing them.
// serveModuleToDigest stitches in the schema for the given mod to the schema keyed by dependerModDigest.
func (s *moduleSchema) serveModuleToDigest(ctx *core.Context, mod *core.Module, dependerModDigest digest.Digest) error {
	mod, err := s.loadModuleTypes(ctx, mod)
	if err != nil {
		return fmt.Errorf("failed to load dep module functions: %w", err)
	}

	modDigest, err := mod.Digest()
	if err != nil {
		return fmt.Errorf("failed to get module digest: %w", err)
	}
	cacheKey := digest.FromString(modDigest.String() + "." + dependerModDigest.String())

	// TODO: it makes no sense to use this cache since we don't need a core.Module, but also doesn't hurt, but make a separate one anyways for clarity
	_, err = s.moduleCache.GetOrInitialize(cacheKey, func() (*core.Module, error) {
		dependerView, err := s.getModuleSchemaView(dependerModDigest)
		if err != nil {
			return nil, err
		}
		executableSchema, err := s.moduleToSchemaFor(ctx, mod, dependerView)
		if err != nil {
			return nil, fmt.Errorf("failed to convert module to executable schema: %w", err)
		}
		if err := dependerView.addSchemas(executableSchema); err != nil {
			return nil, fmt.Errorf("failed to install module schema: %w", err)
		}
		return mod, nil
	})
	return err
}

// loadModuleTypes invokes the Module to ask for the Objects+Functions it defines and returns the updated
// Module object w/ those TypeDefs included.
func (s *moduleSchema) loadModuleTypes(ctx *core.Context, mod *core.Module) (*core.Module, error) {
	// We use the digest without functions as cache key because loadModuleTypes should behave idempotently,
	// returning the same Module object whether or not its Functions were already loaded.
	// The digest without functions is stable before+after function loading.
	dgst, err := mod.DigestWithoutObjects()
	if err != nil {
		return nil, fmt.Errorf("failed to get module digest: %w", err)
	}

	return s.moduleCache.GetOrInitialize(dgst, func() (*core.Module, error) {
		if err := s.installDeps(ctx, mod); err != nil {
			return nil, fmt.Errorf("failed to install module recursive dependencies: %w", err)
		}

		modID, err := mod.ID()
		if err != nil {
			return nil, fmt.Errorf("failed to get module ID: %w", err)
		}
		// canned function for asking the SDK to return the module + functions it provides
		getModDefFn := &core.Function{
			Name: "", // no name indicates that the SDK should return the module
			ReturnType: &core.TypeDef{
				Kind: core.TypeDefKindObject,
				AsObject: &core.ObjectTypeDef{
					Name: "Module",
				},
			},
			ModuleID: modID,
		}
		result, err := s.functionCall(ctx, getModDefFn, functionCallArgs{
			Module: mod,
			// empty to signify we're querying to get the constructed module
			// ParentName: gqlObjectName(mod.Name),
			Cache: true,
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

		return mod, nil
	})
}

// installDeps stitches in the schemas for all the deps of the given module to the module's
// schema view.
func (s *moduleSchema) installDeps(ctx *core.Context, module *core.Module) error {
	moduleDigest, err := module.Digest()
	if err != nil {
		return err
	}

	var eg errgroup.Group
	for _, dep := range module.Dependencies {
		dep := dep
		eg.Go(func() error {
			if err := s.serveModuleToDigest(ctx, dep, moduleDigest); err != nil {
				return fmt.Errorf("failed to install module dependency %q: %w", dep.Name, err)
			}
			return nil
		})
	}
	return eg.Wait()
}

/* TODO: for module->schema conversion
* Need to handle IDable type as input argument (schema should accept ID as input type)
* Need to handle corner case where a single object is used as both input+output (append "Input" to name?)
* Handle case where scalar from core is returned? Might need API updates unless we hack it and say they are all strings for now...
* If an object from another non-core Module makes its way into this Module's schema, need to include the relevant schema+resolvers for that too.
 */

// moduleToSchema converts a Module to an ExecutableSchema that can be stitched in to an existing schema.
// It presumes that the Module's Functions have already been loaded.
func (s *moduleSchema) moduleToSchemaFor(ctx context.Context, module *core.Module, dest *moduleSchemaView) (ExecutableSchema, error) {
	schemaDoc := &ast.SchemaDocument{}
	newResolvers := Resolvers{}

	for _, def := range module.Objects {
		objTypeDef := def.AsObject
		objName := gqlObjectName(objTypeDef.Name)

		// get the schema + resolvers for the object as a whole
		objType, err := s.typeDefToSchema(def, false)
		if err != nil {
			return nil, fmt.Errorf("failed to convert module to schema: %w", err)
		}

		// check whether this is a pre-existing object (from core or another
		// module) being extended
		_, preExistingObject := dest.resolvers()[objName]

		astDef := &ast.Definition{
			Name:        objName,
			Description: objTypeDef.Description,
			Kind:        ast.Object,
		}

		newObjResolver := ObjectResolver{}
		for _, field := range objTypeDef.Fields {
			fieldASTType, err := s.typeDefToSchema(field.TypeDef, false)
			if err != nil {
				return nil, err
			}
			fieldName := gqlFieldName(field.Name)
			astDef.Fields = append(astDef.Fields, &ast.FieldDefinition{
				Name:        fieldName,
				Description: field.Description,
				Type:        fieldASTType,
			})

			// if this is an IDable type, add a resolver that converts the ID into
			// the real object, otherwise its schema will be called against the
			// string
			if field.TypeDef.Kind == core.TypeDefKindObject {
				newObjResolver[fieldName] = func(p graphql.ResolveParams) (any, error) {
					res, err := graphql.DefaultResolveFn(p)
					if err != nil {
						return nil, err
					}
					id, ok := res.(string)
					if !ok {
						return nil, fmt.Errorf("expected string %sID, got %T", field.TypeDef.AsObject.Name, res)
					}
					return core.ResourceFromID(id)
				}
			} else {
				// no resolver to add; fields rely on the graphql "trivial resolver"
				// where the value is just read from the parent object
				_ = 1
			}
		}

		for _, fn := range objTypeDef.Functions {
			resolver, err := s.functionResolver(astDef, module, fn)
			if err != nil {
				return nil, err
			}
			newObjResolver[gqlFieldName(fn.Name)] = resolver
		}
		if len(newObjResolver) > 0 {
			newResolvers[objName] = newObjResolver
		}

		if len(astDef.Fields) > 0 {
			if preExistingObject {
				// if there's any new functions added to an existing object from core or another module, include
				// those in the schema as extensions
				schemaDoc.Extensions = append(schemaDoc.Extensions, astDef)
			} else {
				schemaDoc.Definitions = append(schemaDoc.Definitions, astDef)
			}
		}

		if preExistingObject {
			// extending already-existing type, don't need to add a stub for
			// constructing it
			continue
		}

		constructorName := gqlFieldName(def.AsObject.Name)

		if constructorName == gqlFieldName(module.Name) {
			// stitch in the module object right under Query
			schemaDoc.Extensions = append(schemaDoc.Extensions, &ast.Definition{
				Name: "Query",
				Kind: ast.Object,
				Fields: ast.FieldList{&ast.FieldDefinition{
					Name: constructorName,
					// TODO is it correct to set it here too vs. type definition?
					// Description: def.AsObject.Description,
					Type: objType,
				}},
			})

			newResolvers["Query"] = ObjectResolver{
				constructorName: PassthroughResolver,
			}
		}
	}

	buf := &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(schemaDoc)
	schemaStr := buf.String()

	return StaticSchema(StaticSchemaParams{
		Name:      module.Name,
		Schema:    schemaStr,
		Resolvers: newResolvers,
	}), nil
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
			Description:  fnArg.Description,
			Type:         argASTType,
			DefaultValue: defaultValue,
		})
	}

	fieldDef := &ast.FieldDefinition{
		Name:        fnName,
		Description: fn.Description,
		Type:        returnASTType,
		Arguments:   argASTTypes,
	}
	parentObj.Fields = append(parentObj.Fields, fieldDef)

	// Our core "id-able" types are serialized as their ID over the wire, but need to be decoded into
	// their object here. We can identify those types since their object resolvers are wrapped in
	// ToIDableObjectResolver.
	var returnIDableObjectResolver IDableObjectResolver
	if fn.ReturnType.Kind == core.TypeDefKindObject {
		returnIDableObjectResolver, _ = s.idableObjectResolver(fn.ReturnType.AsObject.Name)
	}
	parentIDableObjectResolver, _ := s.idableObjectResolver(parentObj.Name)

	return ToResolver(func(ctx *core.Context, parent any, args map[string]any) (_ any, rerr error) {
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
			callInput = append(callInput, &core.CallInput{
				Name:  k,
				Value: v,
			})
		}
		result, err := s.functionCall(ctx, fn, functionCallArgs{
			Module:     module,
			Input:      callInput,
			ParentName: parentObj.Name,
			Parent:     parent,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to call function %q: %w", objFnName, err)
		}
		if returnIDableObjectResolver == nil {
			return result, nil
		}

		id, ok := result.(string)
		if !ok {
			return nil, fmt.Errorf("expected string ID result for %s, got %T", objFnName, result)
		}
		return returnIDableObjectResolver.FromID(id)
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

// TODO: all these should be called during creation of Function+TypeDefs, not scattered all over the place

func gqlObjectName(name string) string {
	// gql object name is capitalized camel case
	return strcase.ToCamel(name)
}

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}

func gqlArgName(name string) string {
	// gql arg name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}
