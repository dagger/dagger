package schema

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/zeebo/xxh3"
	"golang.org/x/sync/errgroup"
)

type moduleSchema struct {
	*MergedSchemas
	moduleCache *core.ModuleCache

	// NOTE: this should only be used to dedupe module install specifically
	installedModCache *core.CacheMap[uint64, *core.Module]
}

var _ ExecutableSchema = &moduleSchema{}

func (s *moduleSchema) Name() string {
	return "module"
}

func (s *moduleSchema) Schema() string {
	return Module
}

func (s *moduleSchema) Resolvers() Resolvers {
	return Resolvers{
		"ModuleID":   stringResolver(core.ModuleID("")),
		"FunctionID": stringResolver(core.FunctionID("")),
		"Query": ObjectResolver{
			"module":              ToResolver(s.module),
			"currentModule":       ToResolver(s.currentModule),
			"function":            ToResolver(s.function),
			"currentFunctionCall": ToResolver(s.currentFunctionCall),
		},
		"Directory": ObjectResolver{
			"asModule": ToResolver(s.directoryAsModule),
		},
		"Module": ToIDableObjectResolver(core.ModuleID.Decode, ObjectResolver{
			"id":           ToResolver(s.moduleID),
			"serve":        ToVoidResolver(s.moduleServe),
			"withFunction": ToResolver(s.moduleWithFunction),
		}),
		"Function": ToIDableObjectResolver(core.FunctionID.Decode, ObjectResolver{
			"id":   ToResolver(s.functionID),
			"call": ToResolver(s.functionCall),
		}),
		"FunctionCall": ObjectResolver{
			"inputArgs":   ToResolver(s.functionCallInputArgs),
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
	return s.moduleCache.CachedModFromContext(ctx)
}

type newFunctionArgs struct {
	ID core.FunctionID
	// input def has mostly same fields as Function object
	core.Function
}

func (s *moduleSchema) function(ctx *core.Context, _ *core.Query, args newFunctionArgs) (*core.Function, error) {
	if args.ID != "" {
		return args.ID.Decode()
	}

	return &args.Function, nil
}

func (s *moduleSchema) currentFunctionCall(ctx *core.Context, _ *core.Query, _ any) (*core.Module, error) {
}

type asModuleArgs struct {
	ConfigPath string
}

func (s *moduleSchema) directoryAsModule(ctx *core.Context, sourceDir *core.Directory, args asModuleArgs) (*core.Module, error) {
	return core.NewModule(s.platform, sourceDir.Pipeline).FromConfig(ctx, s.bk, s.progSockPath, s.moduleCache, s.installDepsCallback, sourceDir, args.ConfigPath)
}

func (s *moduleSchema) moduleID(ctx *core.Context, module *core.Module, args any) (core.ModuleID, error) {
	return module.ID()
}

func (s *moduleSchema) moduleServe(ctx *core.Context, module *core.Module, args any) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	err = s.installModuleForDigest(ctx, module, clientMetadata.ModuleDigest)
	if err != nil {
		return err
	}
	return nil
}

func (s *moduleSchema) installModuleForDigest(ctx context.Context, depMod *core.Module, dependerModDigest digest.Digest) error {
	moduleID, err := depMod.ID()
	if err != nil {
		return err
	}

	hash := xxh3.New()
	fmt.Fprintln(hash, moduleID)
	fmt.Fprintln(hash, dependerModDigest)
	cacheKey := hash.Sum64()

	_, err = s.installedModCache.GetOrInitialize(cacheKey, func() (*core.Module, error) {
		executableSchema, err := s.moduleToSchema(ctx, depMod)
		if err != nil {
			return nil, fmt.Errorf("failed to convert module to executable schema: %w", err)
		}
		if err := s.addSchemas(dependerModDigest, executableSchema); err != nil {
			return nil, fmt.Errorf("failed to install module schema: %w", err)
		}
		return depMod, nil
	})
	return err
}

func (s *moduleSchema) installDepsCallback(ctx context.Context, module *core.Module) error {
	moduleDigest, err := module.Digest()
	if err != nil {
		return err
	}

	var eg errgroup.Group
	for _, dep := range module.Dependencies {
		dep := dep
		eg.Go(func() error {
			err = s.installModuleForDigest(ctx, dep, moduleDigest)
			if err != nil {
				return fmt.Errorf("failed to install module dependency %q: %w", dep.Name, err)
			}
			return nil
		})
	}
	return eg.Wait()
}

func (s *moduleSchema) moduleToSchema(ctx context.Context, module *core.Module) (ExecutableSchema, error) {
	moduleTypeDef := &core.TypeDef{
		Kind: core.TypeDefKindObject,
		Name: module.Name,
		// TODO: description
		Fields: module.Functions,
	}

	schemaDoc := &ast.SchemaDocument{}
	newResolvers := Resolvers{}

	// FIXME: s.Resolvers() needs to include the resolvers from dependencies too (does not currently since the caller doesn't get those in their schema)
	// FIXME: Actually, the fact that we can use resolvers from other schemas also means we need to include those in this schema now too. Can just
	// include as needed rather than stitch everything in.
	moduleType, err := addTypeDefToSchema(moduleTypeDef, true, false, schemaDoc, newResolvers, s.Resolvers(), astTypeCache{})
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

func addTypeDefToSchema(
	typeDef *core.TypeDef,
	nonNull bool,
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
			NonNull:   nonNull,
		}, nil
	case core.TypeDefKindNonNull:
		if nonNull {
			return nil, fmt.Errorf("invalid doubly non-null type")
		}
		return addTypeDefToSchema(typeDef.ElementType, true, isInput, schemaDoc, newResolvers, existingResolvers, typeCache)
	case core.TypeDefKindList:
		astType, err := addTypeDefToSchema(typeDef.ElementType, false, isInput, schemaDoc, newResolvers, existingResolvers, typeCache)
		if err != nil {
			return nil, err
		}
		return &ast.Type{
			Elem:    astType,
			NonNull: nonNull,
		}, nil
	case core.TypeDefKindObject:
		objName := gqlObjectName(typeDef.Name)
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
				Description: typeDef.Description,
				Kind:        ast.Object,
			}
			if isInput {
				astDef.Kind = ast.InputObject
			}

			newObjResolver := ObjectResolver{}
			for _, fn := range typeDef.Fields {
				if err := addFunctionToSchema(astDef, newObjResolver, fn, schemaDoc, newResolvers, existingResolvers, typeCache); err != nil {
					return ast.Type{}, err
				}
			}
			newResolvers[objName] = newObjResolver

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
		astType.NonNull = nonNull
		return &astType, nil
	default:
		return nil, fmt.Errorf("unsupported type kind %q", typeDef.Kind)
	}
}

func addFunctionToSchema(
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
	isInput := parentObj.Kind == ast.InputObject
	if isInput {
		// TODO: kind of feels like there should be a Field object in the graphql schema that can either be a Function or Static
		if !fn.IsStatic {
			return fmt.Errorf("input object field %q must be static", objFnName)
		}
	}
	if fn.IsStatic && len(fn.Args) > 0 {
		return fmt.Errorf("static function %q must have no args", objFnName)
	}

	_, err := typeCache.GetOrInitialize(objFnName, func() (ast.Type, error) {
		returnASTType, err := addTypeDefToSchema(fn.ReturnType, false, isInput, schemaDoc, newResolvers, existingResolvers, typeCache)
		if err != nil {
			return ast.Type{}, err
		}

		var argASTTypes ast.ArgumentDefinitionList
		for _, fnArg := range fn.Args {
			argASTType, err := addTypeDefToSchema(fnArg.TypeDef, false, true, schemaDoc, newResolvers, existingResolvers, typeCache)
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
		var idConvertFunc func(string) (any, error)
		if fn.ReturnType.Kind == core.TypeDefKindObject {
			returnObjName := gqlObjectName(fn.ReturnType.Name)
			if resolver, ok := existingResolvers[returnObjName]; ok {
				if resolver, ok := resolver.(IDableObjectResolver); ok {
					idConvertFunc = resolver.FromID
				}
			}
		}

		parentObjResolver[fnName] = ToResolver(func(ctx *core.Context, parent any, args map[string]any) (any, error) {
			// TODO: also need to handle converting parent object to ID string if this is extending a core type

			// TODO: make sure you use the *right* s here...
			result, err := s.functionCall(ctx, fn, functionCallArgs{Parent: parent, Input: args})
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
	case core.TypeDefKindNonNull:
		return astDefaultValue(typeDef.ElementType, val)
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
			elemASTVal, err := astDefaultValue(typeDef.ElementType, elemVal)
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
		for fieldName, fieldVal := range mapVal {
			fieldName = gqlFieldName(fieldName)
			fn, ok := typeDef.FieldByName(fieldName)
			if !ok {
				return nil, fmt.Errorf("object field %s.%s not found", typeDef.Name, fieldName)
			}
			fieldASTVal, err := astDefaultValue(fn.ReturnType, fieldVal)
			if err != nil {
				return nil, fmt.Errorf("failed to get default value for object field %q: %w", fieldName, err)
			}
			astVal.Children = append(astVal.Children, &ast.ChildValue{
				Name:  fieldName,
				Value: fieldASTVal,
			})
		}
		return astVal, nil
	default:
		return nil, fmt.Errorf("unsupported type kind %q", typeDef.Kind)
	}
}

type withFunctionArgs struct {
	ID core.FunctionID
}

func (s *moduleSchema) moduleWithFunction(ctx *core.Context, module *core.Module, args withFunctionArgs) (_ *core.Module, rerr error) {
	fn, err := args.ID.Decode()
	if err != nil {
		return nil, err
	}
	return module.WithFunction(fn, s.moduleCache)
}

func (s *moduleSchema) functionID(ctx *core.Context, fn *core.Function, _ any) (core.FunctionID, error) {
	return fn.ID()
}

type functionCallArgs struct {
	Parent any // TODO: not in public api right now, should it be?
	// TODO: this means that if there are no args, the caller still has to set `{}` (I think), which would be annoying
	Input map[string]any
}

func (s *moduleSchema) functionCall(ctx *core.Context, fn *core.Function, args functionCallArgs) (any, error) {
	// handle graphql "trivial resolver" case
	if fn.IsStatic {
		// parent must be a custom module object in this case (map[string]any)
		parentMap, ok := args.Parent.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unexpected parent type %T", args.Parent)
		}
		return parentMap[gqlFieldName(fn.Name)], nil
	}

	return fn.Call(ctx, s.bk, s.progSockPath, s.moduleCache, s.installDepsCallback, args.Parent, args.Input)
}

func (s *moduleSchema) functionCallInputArgs(ctx *core.Context, fnCall *core.FunctionCall, _ any) (map[string]any, error) {
	return fnCall.InputArgs(ctx, s.bk)
}

func (s *moduleSchema) functionCallReturnValue(ctx *core.Context, fnCall *core.FunctionCall, args struct{ Value any }) error {
	return fnCall.ReturnValue(ctx, s.bk, args.Value)
}

// TODO: all these should be called during creation of Function+TypeDef, not scattered all over the place

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
