package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
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

func (s *moduleSchema) Resolvers() Resolvers {
	return Resolvers{
		"ModuleID":   stringResolver(core.ModuleID("")),
		"FunctionID": stringResolver(core.FunctionID("")),
		"Query": ObjectResolver{
			"module":              ToResolver(s.module),
			"currentModule":       ToResolver(s.currentModule),
			"function":            ToResolver(s.function),
			"newFunction":         ToResolver(s.newFunction),
			"currentFunctionCall": ToResolver(s.currentFunctionCall),
		},
		"Directory": ObjectResolver{
			"asModule": ToResolver(s.directoryAsModule),
		},
		"Module": ToIDableObjectResolver(core.ModuleID.Decode, ObjectResolver{
			"id":           ToResolver(s.moduleID),
			"withFunction": ToResolver(s.moduleWithFunction),
			"serve":        ToVoidResolver(s.moduleServe),
		}),
		"Function": ToIDableObjectResolver(core.FunctionID.Decode, ObjectResolver{
			"id":   ToResolver(s.functionID),
			"call": ToResolver(s.functionCall),
		}),
		"FunctionCall": ObjectResolver{
			"returnValue": ToVoidResolver(s.functionCallReturnValue),
			"parent":      ToResolver(s.functionCallParent),
		},
		"TypeDef": ObjectResolver{
			"kind": ToResolver(s.typeDefKind),
		},
	}
}

func (s *moduleSchema) typeDefKind(ctx *core.Context, def *core.TypeDef, args any) (string, error) {
	return def.Kind.String(), nil
}

func (s *moduleSchema) Dependencies() []ExecutableSchema {
	return nil
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

type queryFunctionArgs struct {
	ID core.FunctionID
}

func (s *moduleSchema) function(ctx *core.Context, _ *core.Query, args queryFunctionArgs) (*core.Function, error) {
	return args.ID.Decode()
}

func (s *moduleSchema) newFunction(ctx *core.Context, _ *core.Query, args struct{ Def *core.Function }) (*core.Function, error) {
	// We can mostly use the Def as is, but need to also fill in its ModuleID.
	fnCtx, err := s.functionContextCache.FunctionContextFrom(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get function context from context: %w", err)
	}
	modID, err := fnCtx.Module.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get module ID: %w", err)
	}

	var walkTypeDef func(def *core.TypeDef)
	var setFnModuleID func(def *core.Function)
	walkTypeDef = func(def *core.TypeDef) {
		switch def.Kind {
		case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean:
			return
		case core.TypeDefKindList:
			walkTypeDef(def.AsList.ElementTypeDef)
		case core.TypeDefKindObject:
			for _, field := range def.AsObject.Fields {
				walkTypeDef(field.TypeDef)
			}
			for _, fn := range def.AsObject.Functions {
				setFnModuleID(fn)
			}
		default:
			panic(fmt.Errorf("unhandled type def kind %q", def.Kind))
		}
	}
	setFnModuleID = func(fn *core.Function) {
		fn.ModuleID = modID
		for _, arg := range fn.Args {
			walkTypeDef(arg.TypeDef)
		}
		walkTypeDef(fn.ReturnType)
	}
	setFnModuleID(args.Def)
	return args.Def, nil
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
	mod, err := core.NewModule(s.platform, sourceDir.Pipeline).FromConfig(ctx, s.bk, s.services, s.progSockPath, sourceDir, args.SourceSubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to create module from config: %w", err)
	}
	return s.loadModuleFunctions(ctx, mod)
}

func (s *moduleSchema) moduleID(ctx *core.Context, module *core.Module, args any) (_ core.ModuleID, rerr error) {
	return module.ID()
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

type withFunctionArgs struct {
	ID core.FunctionID
}

func (s *moduleSchema) moduleWithFunction(ctx *core.Context, module *core.Module, args withFunctionArgs) (_ *core.Module, rerr error) {
	defer func() {
		if err := recover(); err != nil {
			rerr = fmt.Errorf("panic in moduleWithFunction: %s\n%s", err, debug.Stack())
		}
	}()

	fn, err := args.ID.Decode()
	if err != nil {
		return nil, err
	}
	return module.WithFunction(fn)
}

func (s *moduleSchema) functionID(ctx *core.Context, fn *core.Function, _ any) (core.FunctionID, error) {
	return fn.ID()
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

type functionCallArgs struct {
	Input []*core.CallInput

	// Below are not in public API, used internally by Function.call api
	ParentName string
	Parent     any
}

func (s *moduleSchema) functionCall(ctx *core.Context, fn *core.Function, args functionCallArgs) (any, error) {
	// TODO: if return type non-null, assert on that here
	// TODO: handle setting default values, they won't be set when going through "dynamic call" codepath

	// TODO: re-add support for different exit codes
	cacheExitCode := uint32(0)

	mod, err := fn.ModuleID.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode module: %w", err)
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
	ctx, err = s.functionContextCache.WithFunctionContext(ctx, &FunctionContext{
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
		ctr, err = ctr.WithMountedDirectory(ctx, s.bk, dirMntPath, dep.SourceDirectory, "", true)
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
		return nil, fmt.Errorf("failed to read function output file: %w", err)
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
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean:
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

	ctx.Context = engine.ContextWithClientMetadata(ctx.Context, clientMetadata)
	return ctx, nil
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
	mod, err := s.loadModuleFunctions(ctx, mod)
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
		executableSchema, err := s.moduleToSchema(ctx, mod)
		if err != nil {
			return nil, fmt.Errorf("failed to convert module to executable schema: %w", err)
		}
		if err := s.addSchemas(dependerModDigest, executableSchema); err != nil {
			return nil, fmt.Errorf("failed to install module schema: %w", err)
		}
		return mod, nil
	})
	return err
}

// loadModuleFunctions invokes the Module to ask for the Functions it defines and returns the updated
// Module object w/ those Functions included.
func (s *moduleSchema) loadModuleFunctions(ctx *core.Context, mod *core.Module) (*core.Module, error) {
	// We use the digest without functions as cache key because loadModuleFunctions should behave idempotently,
	// returning the same Module object whether or not its Functions were already loaded.
	// The digest without functions is stable before+after function loading.
	dgst, err := mod.DigestWithoutFunctions()
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
			ReturnType: &core.TypeDef{
				Kind: core.TypeDefKindObject,
				AsObject: &core.ObjectTypeDef{
					Name: "Module",
				},
			},
			ModuleID: modID,
		}
		result, err := s.functionCall(ctx, getModDefFn, functionCallArgs{
			ParentName: gqlObjectName(mod.Name),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to call module to get functions: %w", err)
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
func (s *moduleSchema) moduleToSchema(ctx context.Context, module *core.Module) (ExecutableSchema, error) {
	schemaDoc := &ast.SchemaDocument{}
	newResolvers := Resolvers{}

	// get the schema + resolvers for the module as a whole
	moduleType, err := s.addTypeDefToSchema(&core.TypeDef{
		Kind: core.TypeDefKindObject,
		AsObject: &core.ObjectTypeDef{
			Name:        module.Name,
			Description: module.Description,
			Functions:   module.Functions,
		},
	}, false, schemaDoc, newResolvers)
	if err != nil {
		return nil, fmt.Errorf("failed to convert module to schema: %w", err)
	}

	// stitch in the module object right under Query
	schemaDoc.Extensions = append(schemaDoc.Extensions, &ast.Definition{
		Name: "Query",
		Kind: ast.Object,
		Fields: ast.FieldList{&ast.FieldDefinition{
			Name: module.Name,
			// TODO: Description
			Type: moduleType,
		}},
	})
	newResolvers["Query"] = ObjectResolver{
		module.Name: PassthroughResolver,
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

func (s *moduleSchema) addTypeDefToSchema(
	typeDef *core.TypeDef,
	isInput bool,
	schemaDoc *ast.SchemaDocument,
	newResolvers Resolvers,
) (*ast.Type, error) {
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
	case core.TypeDefKindList:
		if typeDef.AsList == nil {
			return nil, fmt.Errorf("expected list type def, got nil")
		}
		astType, err := s.addTypeDefToSchema(typeDef.AsList.ElementTypeDef, isInput, schemaDoc, newResolvers)
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

		// check whether this is a pre-existing object (from core or another module) being extended
		_, preExistingObject := s.currentSchemaView.resolvers()[objName]
		// also check whether it's specifically an idable object from the core API
		idableObjectResolver, _ := s.idableObjectResolver(objName)

		astDef := &ast.Definition{
			Name:        objName,
			Description: objTypeDef.Description,
			Kind:        ast.Object,
		}
		if isInput && idableObjectResolver == nil {
			astDef.Kind = ast.InputObject
		}

		for _, field := range objTypeDef.Fields {
			fieldASTType, err := s.addTypeDefToSchema(field.TypeDef, isInput, schemaDoc, newResolvers)
			if err != nil {
				return nil, err
			}
			astDef.Fields = append(astDef.Fields, &ast.FieldDefinition{
				Name:        gqlFieldName(field.Name),
				Description: field.Description,
				Type:        fieldASTType,
			})
			// no resolver to add; fields rely on the graphql "trivial resolver" where the value is just read from the parent object
		}

		newObjResolver := ObjectResolver{}
		for _, fn := range objTypeDef.Functions {
			if err := s.addFunctionToSchema(astDef, newObjResolver, fn, schemaDoc, newResolvers); err != nil {
				return nil, err
			}
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

		if isInput && idableObjectResolver != nil {
			// idable types use their ID scalar as the input value
			return &ast.Type{NamedType: astDef.Name + "ID", NonNull: !typeDef.Optional}, nil
		}
		return &ast.Type{NamedType: astDef.Name, NonNull: !typeDef.Optional}, nil
	default:
		return nil, fmt.Errorf("unsupported type kind %q", typeDef.Kind)
	}
}

func (s *moduleSchema) addFunctionToSchema(
	parentObj *ast.Definition,
	parentObjResolver ObjectResolver,
	fn *core.Function,
	schemaDoc *ast.SchemaDocument,
	newResolvers Resolvers,
) error {
	fnName := gqlFieldName(fn.Name)
	objFnName := fmt.Sprintf("%s.%s", parentObj.Name, fnName)

	returnASTType, err := s.addTypeDefToSchema(fn.ReturnType, false, schemaDoc, newResolvers)
	if err != nil {
		return err
	}

	var argASTTypes ast.ArgumentDefinitionList
	for _, fnArg := range fn.Args {
		argASTType, err := s.addTypeDefToSchema(fnArg.TypeDef, true, schemaDoc, newResolvers)
		if err != nil {
			return err
		}
		defaultValue, err := astDefaultValue(fnArg.TypeDef, fnArg.DefaultValue)
		if err != nil {
			return err
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

	parentObjResolver[fnName] = ToResolver(func(ctx *core.Context, parent any, args map[string]any) (_ any, rerr error) {
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
	})
	return nil
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
		intVal, ok := val.(int)
		if !ok {
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
