package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	functionContextCache *FunctionContextCache

	// NOTE: this should only be used to dedupe module load+install specifically
	moduleCache *core.CacheMap[digest.Digest, *core.Module]
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
		},
	}
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

func (s *moduleSchema) newFunction(ctx *core.Context, _ *core.Query, fnDef *core.Function) (*core.Function, error) {
	// TODO: make sure this errors if not from module caller
	fnCtx, err := s.functionContextCache.FunctionContextFrom(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get function context from context: %w", err)
	}
	modID, err := fnCtx.Module.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get module ID: %w", err)
	}
	fnDef.ModuleID = modID
	return fnDef, nil
}

func (s *moduleSchema) currentFunctionCall(ctx *core.Context, _ *core.Query, _ any) (*core.FunctionCall, error) {
	// TODO: make sure this errors if not from module caller
	fnCtx, err := s.functionContextCache.FunctionContextFrom(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get function context from context: %w", err)
	}
	return fnCtx.CurrentCall, nil
}

type asModuleArgs struct {
	ConfigPath string
}

func (s *moduleSchema) directoryAsModule(ctx *core.Context, sourceDir *core.Directory, args asModuleArgs) (*core.Module, error) {
	mod, err := core.NewModule(s.platform, sourceDir.Pipeline).FromConfig(ctx, s.bk, s.progSockPath, sourceDir, args.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create module from config: %w", err)
	}
	return s.loadModuleFunctions(ctx, mod)
}

func (s *moduleSchema) moduleID(ctx *core.Context, module *core.Module, args any) (core.ModuleID, error) {
	return module.ID()
}

func (s *moduleSchema) moduleServe(ctx *core.Context, module *core.Module, args any) error {
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
	valueBytes, err := json.Marshal(args.Value)
	if err != nil {
		return fmt.Errorf("failed to marshal function return value: %w", err)
	}

	// TODO: doc what's going on here and why
	return s.bk.IOReaderExport(ctx, bytes.NewReader(valueBytes), filepath.Join(core.ModMetaDirPath, core.ModMetaOutputPath), 0600)
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
	inputFile, err := inputFileDir.File(ctx, s.bk, core.ModMetaInputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get input file: %w", err)
	}
	ctr, err = ctr.WithMountedFile(ctx, s.bk, filepath.Join(core.ModMetaDirPath, core.ModMetaInputPath), inputFile, "", true)
	if err != nil {
		return nil, fmt.Errorf("failed to mount input file: %w", err)
	}

	ctr, err = ctr.WithExec(ctx, s.bk, s.progSockPath, mod.Platform, core.ContainerExecOpts{
		ExperimentalPrivilegedNesting: true,
		CacheExitCode:                 cacheExitCode,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec entrypoint: %w", err)
	}
	ctrOutputDir, err := ctr.Directory(ctx, s.bk, core.ModMetaDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get entrypoint output directory: %w", err)
	}

	result, err := ctrOutputDir.Evaluate(ctx, s.bk)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate entrypoint: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("entrypoint returned nil result")
	}

	// TODO: if any error happens below, we should really prune the cache of the result, otherwise
	// we can end up in a state where we have a cached result with a dependency blob that we don't
	// guarantee the continued existence of...

	/* TODO: re-add support for interpreting exit code
	exitCodeStr, err := ctr.MetaFileContents(ctx, s.bk, s.progSockPath, "exitCode")
	if err != nil {
		return nil, fmt.Errorf("failed to read entrypoint exit code: %w", err)
	}
	exitCodeUint64, err := strconv.ParseUint(exitCodeStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse entrypoint exit code: %w", err)
	}
	exitCode := uint32(exitCodeUint64)
	*/

	outputBytes, err := result.Ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: core.ModMetaOutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read entrypoint output file: %w", err)
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
		_, isIDable := s.idConverter(typeDef)
		if !isIDable {
			mapValue, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("expected object value, got %T", value)
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

func (s *moduleSchema) idConverter(typeDef *core.TypeDef) (func(string) (any, error), bool) {
	if typeDef.Kind != core.TypeDefKindObject {
		return nil, false
	}
	returnObjName := gqlObjectName(typeDef.AsObject.Name)
	resolver, ok := s.Resolvers()[returnObjName]
	if !ok {
		return nil, false
	}
	idableResolver, ok := resolver.(IDableObjectResolver)
	if !ok {
		return nil, false
	}
	return idableResolver.FromID, true
}

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

	ctx.Context = engine.ContextWithClientMetadata(ctx, clientMetadata)
	return ctx, nil
}

var functionContextNotFoundErr = fmt.Errorf("function context not found")

func (cache *FunctionContextCache) FunctionContextFrom(ctx context.Context) (*FunctionContext, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	return cache.cacheMap().GetOrInitialize(clientMetadata.FunctionContextDigest, func() (*FunctionContext, error) {
		return nil, functionContextNotFoundErr
	})
}

func (s *moduleSchema) serveModuleToDigest(ctx *core.Context, mod *core.Module, dependerModDigest digest.Digest) error {
	mod, err := s.loadModuleFunctions(ctx, mod)
	if err != nil {
		return fmt.Errorf("failed to load dep module functions: %w", err)
	}

	modDigest, err := mod.Digest()
	if err != nil {
		return fmt.Errorf("failed to get module digest: %w", err)
	}
	cacheKey := digest.FromString(modDigest.String() + dependerModDigest.String())

	// TODO: it makes no sense to use this cache since we don't need a core.Module, but also doesn't hurt, but make a separate one
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

func (s *moduleSchema) loadModuleFunctions(ctx *core.Context, mod *core.Module) (*core.Module, error) {
	modDigest, err := mod.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get module digest: %w", err)
	}

	// TODO: make sure this is the right cache key, what if you provide the digest of the module returned here to loadModuleFunctions? Maybe doesn't matter, but double check
	return s.moduleCache.GetOrInitialize(modDigest, func() (*core.Module, error) {
		if err := s.installDeps(ctx, mod); err != nil {
			return nil, fmt.Errorf("failed to install module recursive dependencies: %w", err)
		}

		// canned function for asking the SDK to return the module + functions it provides
		getModDefFn := &core.Function{
			ReturnType: &core.TypeDef{
				Kind: core.TypeDefKindObject,
				AsObject: &core.ObjectTypeDef{
					Name: mod.Name,
				},
			},
		}
		result, err := s.functionCall(ctx, getModDefFn, functionCallArgs{
			ParentName: mod.Name,
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

func (s *moduleSchema) moduleToSchema(ctx context.Context, module *core.Module) (ExecutableSchema, error) {
	schemaDoc := &ast.SchemaDocument{}
	newResolvers := Resolvers{}

	// FIXME: s.Resolvers() needs to include the resolvers from dependencies too (does not currently since the caller doesn't get those in their schema)
	// FIXME: Actually, the fact that we can use resolvers from other schemas also means we need to include those in this schema now too. Can just
	// include as needed rather than stitch everything in.
	moduleType, err := s.addTypeDefToSchema(&core.TypeDef{
		Kind: core.TypeDefKindObject,
		AsObject: &core.ObjectTypeDef{
			Name:        module.Name,
			Description: module.Description,
			Functions:   module.Functions,
		},
	}, false, schemaDoc, newResolvers, s.Resolvers(), astTypeCache{})
	if err != nil {
		return nil, fmt.Errorf("failed to convert module to schema: %w", err)
	}

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

// name -> ast type, used to dedupe walk of type DAG
type astTypeCache = core.CacheMap[string, ast.Type]

/* TODO:
* Need to handle corner case where a single object is used as both input+output (append "Input" to name?)
* Handle case where scalar from core is returned? Might need API updates unless we hack it and say they are all strings for now...
 */

func (s *moduleSchema) addTypeDefToSchema(
	typeDef *core.TypeDef,
	isInput bool,
	schemaDoc *ast.SchemaDocument,
	newResolvers Resolvers,
	existingResolvers Resolvers,
	typeCache astTypeCache,
) (*ast.Type, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean:
		return &ast.Type{
			NamedType: typeDef.Kind.String(),
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindList:
		if typeDef.AsList == nil {
			return nil, fmt.Errorf("expected list type def, got nil")
		}
		astType, err := s.addTypeDefToSchema(typeDef.AsList.ElementTypeDef, isInput, schemaDoc, newResolvers, existingResolvers, typeCache)
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
		var existingObjResolver ObjectResolver
		if resolver, ok := existingResolvers[objName]; ok {
			objResolver, ok := resolver.(ObjectResolver)
			if !ok {
				// technically this would get caught during schema merge later, but may as well error out early
				return nil, fmt.Errorf("existing resolver for %q is not an object resolver", objName)
			}
			existingObjResolver = objResolver
		}

		astType, err := typeCache.GetOrInitialize(objName, func() (ast.Type, error) {
			astDef := &ast.Definition{
				Name:        objName,
				Description: objTypeDef.Description,
				Kind:        ast.Object,
			}
			if isInput {
				astDef.Kind = ast.InputObject
			}

			for _, field := range objTypeDef.Fields {
				fieldASTType, err := s.addTypeDefToSchema(field.TypeDef, isInput, schemaDoc, newResolvers, existingResolvers, typeCache)
				if err != nil {
					return ast.Type{}, err
				}
				astDef.Fields = append(astDef.Fields, &ast.FieldDefinition{
					Name:        gqlFieldName(field.Name),
					Description: field.Description,
					Type:        fieldASTType,
				})
				// no resolver to add; fields rely on the graphql "trivial resolver" where the value is just read from the parent object
			}

			if !isInput {
				// NOTE: currently, we ignore any functions defined on input objects. This simplifies SDK implementation since they
				// don't need to do special handling if, e.g., there happens to be a method defined on some object being used as an
				// input.
				newObjResolver := ObjectResolver{}
				for _, fn := range objTypeDef.Functions {
					if err := s.addFunctionToSchema(astDef, newObjResolver, fn, schemaDoc, newResolvers, existingResolvers, typeCache); err != nil {
						return ast.Type{}, err
					}
				}
				newResolvers[objName] = newObjResolver
			}

			if existingObjResolver != nil {
				schemaDoc.Extensions = append(schemaDoc.Extensions, astDef)
			} else {
				schemaDoc.Definitions = append(schemaDoc.Definitions, astDef)
			}
			return ast.Type{NamedType: astDef.Name}, nil
		})
		if err != nil {
			return nil, err
		}
		astType.NonNull = !typeDef.Optional
		return &astType, nil
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
	existingResolvers Resolvers,
	typeCache astTypeCache,
) error {
	fnName := gqlFieldName(fn.Name)
	objFnName := fmt.Sprintf("%s.%s", parentObj.Name, fnName)

	_, err := typeCache.GetOrInitialize(objFnName, func() (ast.Type, error) {
		returnASTType, err := s.addTypeDefToSchema(fn.ReturnType, false, schemaDoc, newResolvers, existingResolvers, typeCache)
		if err != nil {
			return ast.Type{}, err
		}

		var argASTTypes ast.ArgumentDefinitionList
		for _, fnArg := range fn.Args {
			argASTType, err := s.addTypeDefToSchema(fnArg.TypeDef, true, schemaDoc, newResolvers, existingResolvers, typeCache)
			if err != nil {
				return ast.Type{}, err
			}
			defaultValue, err := astDefaultValue(fnArg.TypeDef, fnArg.DefaultValue)
			if err != nil {
				return ast.Type{}, err
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
		idConvertFunc, _ := s.idConverter(fn.ReturnType)

		parentObjResolver[fnName] = ToResolver(func(ctx *core.Context, parent any, args map[string]any) (any, error) {
			// TODO: also need to handle converting parent object to ID string if this is extending a core type

			var callInput []*core.CallInput
			for k, v := range args {
				callInput = append(callInput, &core.CallInput{
					Name:  k,
					Value: v,
				})
			}
			// TODO: make sure you use the *right* s here...
			result, err := s.functionCall(ctx, fn, functionCallArgs{
				Input:      callInput,
				ParentName: parentObj.Name,
				Parent:     parent,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to call function %q: %w", objFnName, err)
			}
			if idConvertFunc == nil {
				return result, nil
			}
			id, ok := result.(string)
			if !ok {
				return nil, fmt.Errorf("expected string ID result for %s, got %T", objFnName, result)
			}
			return idConvertFunc(id)
		})
		return ast.Type{}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

// relevent ast code we need to work with here:
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
