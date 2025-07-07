package core

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/vektah/gqlparser/v2/ast"
)

// ModuleObject represents an object for a module
type ModuleObject struct {
	Module  *Module
	TypeDef *TypeDef
	Fields  map[string]any
}

var _ dagql.Object = (*ModuleObject)(nil)

func (obj *ModuleObject) TypeName() string {
	return obj.TypeDef.Name()
}

func (obj *ModuleObject) TypeDescription() string {
	return obj.TypeDef.Description
}

func (obj *ModuleObject) ObjectType() dagql.ObjectType {
	return obj.TypeDef.ObjectType()
}

func (obj *ModuleObject) Type() *ast.Type {
	return obj.TypeDef.Type()
}

func (obj *ModuleObject) ID() *dagql.ID[*ModuleObject] {
	return &dagql.ID[*ModuleObject]{
		Type:  obj.TypeDef,
		Keys:  obj.Fields,
		Module: obj.Module.IDModule(),
	}
}

func (obj *ModuleObject) CloneWithFields(fields map[string]any) *ModuleObject {
	clone := &ModuleObject{
		Module:  obj.Module,
		TypeDef: obj.TypeDef,
		Fields:  map[string]any{},
	}
	for k, v := range obj.Fields {
		clone.Fields[k] = v
	}
	for k, v := range fields {
		clone.Fields[k] = v
	}
	return clone
}

func (obj *ModuleObject) SetField(name string, value any) {
	obj.Fields[name] = value
}

func (obj *ModuleObject) GetField(name string) (any, bool) {
	value, found := obj.Fields[name]
	return value, found
}

func (obj *ModuleObject) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return obj.TypeDef.PBDefinitions(ctx)
}

func (obj *ModuleObject) install(ctx context.Context, dag *dagql.Server) error {
	objDef := obj.TypeDef
	mod := obj.Module

	class := dagql.NewClass(objDef.Name(), objDef.Description, obj)
	class.KeysType = objDef.FieldsType()

	if objDef.SourceMap != nil {
		class.Directives = append(class.Directives, objDef.SourceMap.TypeDirective())
	}

	if gqlObjectName(objDef.OriginalName) == gqlObjectName(mod.OriginalName) {
		if err := obj.installConstructor(ctx, dag); err != nil {
			return fmt.Errorf("failed to install constructor: %w", err)
		}
	}
	fields := obj.fields()

	funs, err := obj.functions(ctx, dag)
	if err != nil {
		return err
	}
	fields = append(fields, funs...)

	class.Install(fields...)
	dag.InstallObject(class)

	return nil
}

func (obj *ModuleObject) installConstructor(ctx context.Context, dag *dagql.Server) error {
	objDef := obj.TypeDef
	mod := obj.Module

	// if no constructor defined, install a basic one that initializes an empty object
	if !objDef.Constructor.Valid {
		spec := dagql.FieldSpec{
			Name: gqlFieldName(mod.Name()),
			// Description: "TODO", // XXX(vito)
			Type:   obj,
			Module: obj.Module.IDModule(),
		}

		if objDef.SourceMap != nil {
			spec.Directives = append(spec.Directives, objDef.SourceMap.TypeDirective())
		}

		dag.Root().ObjectType().Extend(
			spec,
			func(ctx context.Context, self dagql.Object, _ map[string]dagql.Input) (dagql.Typed, error) {
				return &ModuleObject{
					Module:  mod,
					TypeDef: objDef,
					Fields:  map[string]any{},
				}, nil
			},
			dagql.CacheSpec{
				GetCacheConfig: mod.CacheConfigForCall,
			},
		)
		return nil
	}

	// use explicit user-defined constructor if provided
	fnTypeDef := objDef.Constructor.Value
	if fnTypeDef.ReturnType.Kind != TypeDefKindObject {
		return fmt.Errorf("constructor function for object %s must return that object", objDef.OriginalName)
	}
	if fnTypeDef.ReturnType.AsObject.Value.OriginalName != objDef.OriginalName {
		return fmt.Errorf("constructor function for object %s must return that object", objDef.OriginalName)
	}

	fn, err := NewModFunction(ctx, mod, objDef, mod.Runtime, fnTypeDef)
	if err != nil {
		return fmt.Errorf("failed to create function: %w", err)
	}

	spec, err := fn.metadata.FieldSpec(ctx, mod)
	if err != nil {
		return fmt.Errorf("failed to get field spec: %w", err)
	}
	spec.Name = gqlFieldName(mod.Name())
	spec.Module = obj.Module.IDModule()

	dag.Root().ObjectType().Extend(
		spec,
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			// Load .env.dagger.json config if available
			envConfig, err := LoadEnvDaggerConfig(ctx, mod.sourceDir)
			if err != nil {
				return nil, fmt.Errorf("failed to load .env.dagger.json: %w", err)
			}

			// Merge env config with call arguments (call args take precedence)
			mergedArgs := make(map[string]dagql.Input)
			
			// First, add env config arguments if they exist
			if envConfig != nil {
				argParser := NewServerArgumentParser(mod.Query)
				
				// Get constructor function arguments to match env config keys
				for _, arg := range fn.metadata.Args {
					if envValue, hasEnvValue := (*envConfig)[arg.Name]; hasEnvValue {
						// Parse the string value from env config into the proper type
						parsedValue, err := argParser.ParseArgument(ctx, arg, envValue)
						if err != nil {
							slog.ExtraDebug("failed to parse env config value", "key", arg.Name, "value", envValue, "error", err)
							continue // Skip invalid values, don't fail the whole call
						}
						mergedArgs[arg.Name] = parsedValue
						slog.ExtraDebug("merged env config argument", "key", arg.Name, "value", envValue)
					}
				}
			}

			// Then, add call arguments (these override env config)
			for k, v := range args {
				mergedArgs[k] = v
			}

			// Convert merged args to CallInput format
			var callInput []CallInput
			for k, v := range mergedArgs {
				callInput = append(callInput, CallInput{
					Name:  k,
					Value: v,
				})
			}

			return fn.Call(ctx, &CallOpts{
				Inputs:       callInput,
				ParentTyped:  nil,
				ParentFields: nil,
				Cache:        dagql.IsInternal(ctx),
				Server:       dag,
			})
		},
		dagql.CacheSpec{
			GetCacheConfig: fn.CacheConfigForCall,
		},
	)

	return nil
}

func (obj *ModuleObject) fields() (fields []dagql.Field[*ModuleObject]) {
	objDef := obj.TypeDef

	for _, field := range objDef.Fields {
		field := field
		fields = append(fields, dagql.Field[*ModuleObject]{
			Name:        gqlFieldName(field.Name),
			Description: field.Description,
			Type:        field.TypeDef,
			Module:      obj.Module.IDModule(),
			Directives:  field.Directives(),
			Resolver: func(ctx context.Context, self *ModuleObject, args map[string]dagql.Input) (dagql.Typed, error) {
				val, found := self.GetField(field.Name)
				if !found {
					return nil, fmt.Errorf("field %s not found", field.Name)
				}
				return val.(dagql.Typed), nil
			},
		})
	}

	return fields
}

func (obj *ModuleObject) functions(ctx context.Context, dag *dagql.Server) ([]dagql.Field[*ModuleObject], error) {
	objDef := obj.TypeDef

	var fields []dagql.Field[*ModuleObject]
	for _, fnDef := range objDef.Functions {
		fnDef := fnDef
		if fnDef.ReturnType.Kind == TypeDefKindVoid {
			continue
		}

		fn, err := NewModFunction(ctx, obj.Module, objDef, obj.Module.Runtime, fnDef)
		if err != nil {
			return nil, fmt.Errorf("failed to create function %s: %w", fnDef.Name, err)
		}

		spec, err := fn.metadata.FieldSpec(ctx, obj.Module)
		if err != nil {
			return nil, fmt.Errorf("failed to get field spec for %s: %w", fnDef.Name, err)
		}
		spec.Module = obj.Module.IDModule()

		fields = append(fields, dagql.Field[*ModuleObject]{
			Name:        spec.Name,
			Description: spec.Description,
			Type:        spec.Type,
			Args:        spec.Args,
			Module:      spec.Module,
			Directives:  spec.Directives,
			Resolver: func(ctx context.Context, self *ModuleObject, args map[string]dagql.Input) (dagql.Typed, error) {
				var callInput []CallInput
				for k, v := range args {
					callInput = append(callInput, CallInput{
						Name:  k,
						Value: v,
					})
				}
				return fn.Call(ctx, &CallOpts{
					Inputs:       callInput,
					ParentTyped:  self,
					ParentFields: self.Fields,
					Cache:        dagql.IsInternal(ctx),
					Server:       dag,
				})
			},
		})
	}

	return fields, nil
}

func (obj *ModuleObject) AsModule() *Module {
	return obj.Module
}

func (obj *ModuleObject) AsService() *Service {
	return &Service{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func (obj *ModuleObject) AsDirectory() *Directory {
	return &Directory{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func (obj *ModuleObject) AsFile() *File {
	return &File{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func (obj *ModuleObject) AsContainer() *Container {
	return &Container{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func (obj *ModuleObject) AsSecret() *Secret {
	return &Secret{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func (obj *ModuleObject) AsSocket() *Socket {
	return &Socket{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func (obj *ModuleObject) AsListObject() *ListObject {
	return &ListObject{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func (obj *ModuleObject) AsEnumObject() *EnumObject {
	return &EnumObject{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func (obj *ModuleObject) AsInterfaceObject() *InterfaceObject {
	return &InterfaceObject{
		ObjectType: obj.TypeDef,
		Module:     obj.Module,
		Fields:     obj.Fields,
	}
}

func gqlObjectName(name string) string {
	if name == "" {
		return ""
	}

	// Remove non-alphanumeric characters and capitalize first letter
	var result strings.Builder
	capitalize := true
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if capitalize {
				result.WriteRune(r)
				capitalize = false
			} else {
				result.WriteRune(r)
			}
		} else {
			capitalize = true
		}
	}
	return result.String()
}

func gqlFieldName(name string) string {
	if name == "" {
		return ""
	}

	// Convert to camelCase
	var result strings.Builder
	capitalize := false
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if i == 0 {
				// First character should be lowercase
				result.WriteRune(r)
			} else if capitalize {
				result.WriteRune(r)
				capitalize = false
			} else {
				result.WriteRune(r)
			}
		} else {
			capitalize = true
		}
	}
	return result.String()
}

// sortFields sorts fields by name for consistent output
func sortFields(fields []dagql.Field[*ModuleObject]) {
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].Name < fields[j].Name
	})
}