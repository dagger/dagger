package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

type InterfaceType struct {
	api       *APIServer
	sourceMod *UserMod
	typeDef   *core.InterfaceTypeDef
}

var _ ModType = (*InterfaceType)(nil)

func newModIface(ctx context.Context, mod *UserMod, typeDef *core.TypeDef) (*InterfaceType, error) {
	if typeDef.Kind != core.TypeDefKindInterface {
		return nil, fmt.Errorf("expected interface type def, got %s", typeDef.Kind)
	}
	iface := &InterfaceType{
		api:       mod.api,
		sourceMod: mod,
		typeDef:   typeDef.AsInterface,
	}
	return iface, nil
}

func (iface *InterfaceType) ConvertFromSDKResult(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		// TODO: this needs to handle core IDs too; need a common func for decoding both those and mod objects?

		objMap, modDgst, typeName, err := resourceid.DecodeModuleID(value, "")
		if err != nil {
			return nil, fmt.Errorf("failed to decode id: %w", err)
		}
		sourceMod, err := iface.api.GetModFromDagDigest(ctx, modDgst)
		if err != nil {
			return nil, fmt.Errorf("failed to get source mod %s: %w", modDgst, err)
		}

		modType, ok, err := sourceMod.ModTypeFor(ctx, &core.TypeDef{
			Kind: core.TypeDefKindObject,
			AsObject: &core.ObjectTypeDef{
				Name: typeName,
			},
		}, false)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for %s: %w", typeName, err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for %s", typeName)
		}

		// TODO: need to assert object implements interface here now

		convertedValue, err := modType.ConvertFromSDKResult(ctx, objMap)
		if err != nil {
			return nil, fmt.Errorf("failed to convert from sdk result: %w", err)
		}

		return &interfaceRuntimeValue{
			Value:          convertedValue,
			UnderlyingType: modType,
			IfaceType:      iface,
		}, nil

	case *interfaceRuntimeValue:
		return &interfaceRuntimeValue{
			Value:          value.Value,
			UnderlyingType: value.UnderlyingType,
			IfaceType:      iface,
		}, nil

	case map[string]any:
		return nil, fmt.Errorf("unexpected interface value type for conversion from sdk result %T", value)
	default:
		return nil, fmt.Errorf("unexpected interface value type for conversion from sdk result %T", value)
	}
}

func (iface *InterfaceType) ConvertToSDKInput(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		return value, nil

	case *interfaceRuntimeValue:
		// TODO: kludge to deal with inconsistency in how user mod objects are currently provided SDKs.
		// For interfaces specifically, we actually do need to provide the object ID rather than the json
		// serialization of the object directly. Otherwise we'll lose track of what the underlying object
		// is.
		userModObj, isUserModObj := value.UnderlyingType.(*UserModObject)
		if isUserModObj {
			convertedValue, err := userModObj.ConvertToID(ctx, value.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to convert to id: %w", err)
			}
			return convertedValue, nil
		}

		convertedValue, err := value.UnderlyingType.ConvertToSDKInput(ctx, value.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to sdk input: %w", err)
		}
		return convertedValue, nil

	default:
		return nil, fmt.Errorf("unexpected interface value type for conversion to sdk input %T", value)
	}
}

func (iface *InterfaceType) SourceMod() Mod {
	return iface.sourceMod
}

func (iface *InterfaceType) Schema(ctx context.Context) (*ast.SchemaDocument, Resolvers, error) {
	ifaceTypeDef := iface.typeDef
	ifaceName := gqlObjectName(ifaceTypeDef.Name)

	typeSchemaDoc := &ast.SchemaDocument{}
	queryResolver := ObjectResolver{}
	ifaceResolver := ObjectResolver{}
	typeSchemaResolvers := Resolvers{
		"Query":   queryResolver,
		ifaceName: ifaceResolver,
	}

	astIDDef := &ast.Definition{
		Name:        ifaceName + "ID",
		Description: formatGqlDescription("%s identifier", ifaceName),
		Kind:        ast.Scalar,
	}
	typeSchemaDoc.Definitions = append(typeSchemaDoc.Definitions, astIDDef)
	idResolver := stringResolver[string]()
	typeSchemaResolvers[astIDDef.Name] = idResolver

	astLoadDef := &ast.FieldDefinition{
		Name:        fmt.Sprintf("load%sFromID", ifaceName),
		Description: formatGqlDescription("Loads a %s from an ID", ifaceName),
		Arguments: ast.ArgumentDefinitionList{
			&ast.ArgumentDefinition{
				Name: "id",
				Type: ast.NonNullNamedType(astIDDef.Name, nil),
			},
		},
		Type: ast.NonNullNamedType(ifaceName, nil),
	}
	typeSchemaDoc.Extensions = append(typeSchemaDoc.Extensions, &ast.Definition{
		Name:   "Query",
		Kind:   ast.Object,
		Fields: ast.FieldList{astLoadDef},
	})
	queryResolver[astLoadDef.Name] = func(p graphql.ResolveParams) (any, error) {
		return iface.ConvertFromSDKResult(p.Context, p.Args["id"])
	}

	astIfaceDef := &ast.Definition{
		Name:        ifaceName,
		Description: formatGqlDescription(ifaceTypeDef.Description),
		Kind:        ast.Object,
	}
	typeSchemaDoc.Definitions = append(typeSchemaDoc.Definitions, astIfaceDef)

	astIDFieldDef := &ast.FieldDefinition{
		Name:        "id",
		Description: formatGqlDescription("A unique identifier for this %s", ifaceName),
		Type:        ast.NonNullNamedType(astIDDef.Name, nil),
	}
	astIfaceDef.Fields = append(astIfaceDef.Fields, astIDFieldDef)
	ifaceResolver[astIDFieldDef.Name] = func(p graphql.ResolveParams) (any, error) {
		return iface.ConvertToSDKInput(p.Context, p.Source)
	}

	for _, fnTypeDef := range iface.typeDef.Functions {
		fnTypeDef := fnTypeDef
		fnName := gqlFieldName(fnTypeDef.Name)

		returnASTType, err := typeDefToASTType(fnTypeDef.ReturnType, false)
		if err != nil {
			return nil, nil, err
		}
		// TODO: validate return type use of non-core concrete objects?

		fieldDef := &ast.FieldDefinition{
			Name:        fnName,
			Description: formatGqlDescription(fnTypeDef.Description),
			Type:        returnASTType,
		}

		argTypeDefsByName := map[string]*core.TypeDef{}
		for _, argMetadata := range fnTypeDef.Args {
			argMetadata := argMetadata
			argTypeDefsByName[argMetadata.Name] = argMetadata.TypeDef
			argASTType, err := typeDefToASTType(argMetadata.TypeDef, true)
			if err != nil {
				return nil, nil, err
			}

			// TODO: default values? Or should that not apply to interfaces?

			// TODO: validate arg type use of non-core concrete objects?

			argDef := &ast.ArgumentDefinition{
				Name:        gqlArgName(argMetadata.Name),
				Description: formatGqlDescription(argMetadata.Description),
				Type:        argASTType,
			}
			fieldDef.Arguments = append(fieldDef.Arguments, argDef)
		}

		astIfaceDef.Fields = append(astIfaceDef.Fields, fieldDef)

		ifaceResolver[fieldDef.Name] = func(p graphql.ResolveParams) (any, error) {
			ctx := p.Context
			runtimeVal, ok := p.Source.(*interfaceRuntimeValue)
			if !ok {
				return nil, fmt.Errorf("unexpected source type %T for interface resolver %s.%s", p.Source, ifaceName, fieldDef.Name)
			}

			// TODO: support core types too
			userModObj, ok := runtimeVal.UnderlyingType.(*UserModObject)
			if !ok {
				return nil, fmt.Errorf("unexpected underlying type %T for interface resolver %s.%s", runtimeVal.UnderlyingType, ifaceName, fieldDef.Name)
			}

			callable, err := userModObj.GetCallable(ctx, fieldDef.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to get callable for %s.%s: %w", ifaceName, fieldDef.Name, err)
			}

			var callInputs []*core.CallInput
			for k, rawArgVal := range p.Args {
				k, rawArgVal := k, rawArgVal
				callableArgType, err := callable.ArgType(k)
				if err != nil {
					return nil, fmt.Errorf("failed to get underlying arg type for %s.%s arg %s: %w", ifaceName, fieldDef.Name, k, err)
				}

				// if the arg type of the underlying object's function is an interface or list of interfaces, we may need to wrap
				// this arg value in that interfaceRuntimeValue here (due to support for contravariant matching on arg types)
				argVal := rawArgVal
				ifaceArgTypeDef, ok := argTypeDefsByName[k]
				if !ok {
					return nil, fmt.Errorf("failed to find arg type def for %s.%s arg %s", ifaceName, fieldDef.Name, k)
				}
				ifaceArgType, ok, err := iface.sourceMod.ModTypeFor(ctx, ifaceArgTypeDef, true)
				if err != nil {
					return nil, fmt.Errorf("failed to get mod type for arg %s: %w", k, err)
				}
				if !ok {
					return nil, fmt.Errorf("failed to find mod type for arg %s", k)
				}
				switch callableArgType := callableArgType.(type) {
				case *InterfaceType:
					argVal = &interfaceRuntimeValue{
						Value:          rawArgVal,
						UnderlyingType: ifaceArgType,
						IfaceType:      callableArgType,
					}

				case *ListType:
					ifaceType, ok := callableArgType.underlying.(*InterfaceType)
					if !ok {
						break
					}
					rawArgList, ok := rawArgVal.([]any)
					if !ok {
						return nil, fmt.Errorf("expected list arg, got %T", rawArgVal)
					}
					argList := make([]any, len(rawArgList))
					for i, rawArgItem := range rawArgList {
						argList[i] = &interfaceRuntimeValue{
							Value:          rawArgItem,
							UnderlyingType: ifaceArgType,
							IfaceType:      ifaceType,
						}
					}
					argVal = argList
				}

				callInputs = append(callInputs, &core.CallInput{
					Name:  k,
					Value: argVal,
				})
			}

			res, err := callable.Call(ctx, &CallOpts{
				Inputs:    callInputs,
				ParentVal: runtimeVal.Value,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to call interface function %s.%s: %w", ifaceName, fieldDef.Name, err)
			}

			if fnTypeDef.ReturnType.Underlying().Kind != core.TypeDefKindInterface {
				return res, nil
			}

			// if the return type of this function is an interface or list of interface, we may need to wrap the
			// return value of the underlying object's function (due to support for covariant matching on return types)

			returnType, ok, err := iface.sourceMod.ModTypeFor(ctx, fnTypeDef.ReturnType.Underlying(), true)
			if err != nil {
				return nil, fmt.Errorf("failed to get return mod type: %w", err)
			}
			if !ok {
				return nil, fmt.Errorf("failed to find return mod type")
			}
			ifaceReturnType, ok := returnType.(*InterfaceType)
			if !ok {
				return nil, fmt.Errorf("expected return interface type, got %T", returnType)
			}

			objReturnType, err := callable.ReturnType()
			if err != nil {
				return nil, fmt.Errorf("failed to get object return type for %s.%s: %w", ifaceName, fieldDef.Name, err)
			}

			switch objReturnType := objReturnType.(type) {
			case *InterfaceType, *UserModObject:
				return &interfaceRuntimeValue{
					Value:          res,
					UnderlyingType: objReturnType,
					IfaceType:      ifaceReturnType,
				}, nil
			case *ListType:
				rawResList, ok := res.([]any)
				if !ok {
					return nil, fmt.Errorf("expected list return, got %T", res)
				}
				resList := make([]any, len(rawResList))
				for i, rawResItem := range rawResList {
					rawResItem := rawResItem
					resList[i] = &interfaceRuntimeValue{
						Value:          rawResItem,
						UnderlyingType: objReturnType.underlying,
						IfaceType:      ifaceReturnType,
					}
				}
				return resList, nil
			default:
				return nil, fmt.Errorf("unexpected object return type %T for %s.%s", objReturnType, ifaceName, fieldDef.Name)
			}
		}
	}

	return typeSchemaDoc, typeSchemaResolvers, nil
}

type interfaceRuntimeValue struct {
	Value          any
	UnderlyingType ModType
	IfaceType      *InterfaceType
}

// allow default graphql resolver to use this object transparently:
// https://github.com/dagger/graphql/blob/bc781b6f799136194783ccb09d27d07590704b32/executor.go#L930-L945
func (v *interfaceRuntimeValue) Resolve(p graphql.ResolveParams) (any, error) {
	p.Source = v.Value
	return graphql.DefaultResolveFn(p)
}

func (v *interfaceRuntimeValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.Value)
}

func (v *interfaceRuntimeValue) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &v.Value)
}
