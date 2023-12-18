package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/opencontainers/go-digest"
)

// CoreMod is a special implementation of Mod for our core API, which is not *technically* a true module yet
// but can be treated as one in terms of dependencies. It has no dependencies itself and is currently an
// implicit dependency of every user module.
type CoreMod struct {
	compiledSchema    *CompiledSchema
	introspectionJSON string
}

var _ Mod = (*CoreMod)(nil)

func (m *CoreMod) Name() string {
	return coreModuleName
}

func (m *CoreMod) DagDigest() digest.Digest {
	// core is always a leaf, so we just return a static digest
	return digest.FromString(coreModuleName)
}

func (m *CoreMod) Dependencies() []Mod {
	return nil
}

func (m *CoreMod) Schema(_ context.Context) ([]SchemaResolvers, error) {
	return []SchemaResolvers{m.compiledSchema.SchemaResolvers}, nil
}

func (m *CoreMod) SchemaIntrospectionJSON(_ context.Context) (string, error) {
	return m.introspectionJSON, nil
}

func (m *CoreMod) ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return &PrimitiveType{}, true, nil

	case core.TypeDefKindList:
		underlyingType, ok, err := m.ModTypeFor(ctx, typeDef.AsList.ElementTypeDef, checkDirectDeps)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		return &ListType{underlying: underlyingType}, true, nil

	case core.TypeDefKindObject:
		typeName := gqlObjectName(typeDef.AsObject.Name)
		resolver, ok := m.compiledSchema.Resolvers()[typeName]
		if !ok {
			return nil, false, nil
		}
		idableResolver, ok := resolver.(IDableObjectResolver)
		if !ok {
			return nil, false, nil
		}
		return &CoreModObject{coreMod: m, resolver: idableResolver}, true, nil

	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}
}

func (m *CoreMod) TypeDefs(ctx context.Context) ([]*core.TypeDef, error) {
	var schemaResp introspection.Response
	if err := json.Unmarshal([]byte(m.introspectionJSON), &schemaResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal introspection JSON: %w", err)
	}
	schema := schemaResp.Schema

	objTypeDefs := make([]*core.TypeDef, 0, len(schema.Types))
	for _, introspectionType := range schema.Types {
		if introspectionType.Kind != introspection.TypeKindObject {
			continue
		}

		typeDef := &core.ObjectTypeDef{
			Name:        introspectionType.Name,
			Description: introspectionType.Description,
		}

		isIdable := false
		for _, introspectionField := range introspectionType.Fields {
			if introspectionField.Name == "id" {
				isIdable = true
				continue
			}

			fn := &core.Function{
				Name:        introspectionField.Name,
				Description: introspectionField.Description,
			}

			rtType, ok, err := introspectionRefToTypeDef(introspectionField.TypeRef, false)
			if err != nil {
				return nil, fmt.Errorf("failed to convert return type: %w", err)
			}
			if !ok {
				continue
			}
			fn.ReturnType = rtType

			for _, introspectionArg := range introspectionField.Args {
				fnArg := &core.FunctionArg{
					Name:        introspectionArg.Name,
					Description: introspectionArg.Description,
				}

				if introspectionArg.DefaultValue != nil {
					fnArg.DefaultValue = *introspectionArg.DefaultValue
				}

				argType, ok, err := introspectionRefToTypeDef(introspectionArg.TypeRef, false)
				if err != nil {
					return nil, fmt.Errorf("failed to convert argument type: %w", err)
				}
				if !ok {
					continue
				}
				fnArg.TypeDef = argType

				fn.Args = append(fn.Args, fnArg)
			}

			typeDef.Functions = append(typeDef.Functions, fn)
		}

		if !isIdable {
			continue
		}

		objTypeDefs = append(objTypeDefs, &core.TypeDef{
			Kind:     core.TypeDefKindObject,
			AsObject: typeDef,
		})
	}
	return objTypeDefs, nil
}

// CoreModObject represents objects from core (Container, Directory, etc.)
type CoreModObject struct {
	coreMod  *CoreMod
	resolver IDableObjectResolver
}

var _ ModType = (*CoreModObject)(nil)

func (obj *CoreModObject) ConvertFromSDKResult(_ context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	id, ok := value.(string)
	if !ok {
		return value, nil
	}
	return obj.resolver.FromID(id)
}

func (obj *CoreModObject) ConvertToSDKInput(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	return obj.resolver.ToID(value)
}

func (obj *CoreModObject) SourceMod() Mod {
	return obj.coreMod
}

func introspectionRefToTypeDef(introspectionType *introspection.TypeRef, nonNull bool) (*core.TypeDef, bool, error) {
	switch introspectionType.Kind {
	case introspection.TypeKindNonNull:
		return introspectionRefToTypeDef(introspectionType.OfType, true)

	case introspection.TypeKindScalar:
		if strings.HasSuffix(introspectionType.Name, "ID") {
			// convert ID inputs to the actual object
			objName := strings.TrimSuffix(introspectionType.Name, "ID")
			return &core.TypeDef{
				Kind:     core.TypeDefKindObject,
				Optional: !nonNull,
				AsObject: &core.ObjectTypeDef{
					Name: objName,
				},
			}, true, nil
		}

		typeDef := &core.TypeDef{
			Optional: !nonNull,
		}
		switch introspectionType.Name {
		case string(introspection.ScalarString):
			typeDef.Kind = core.TypeDefKindString
		case string(introspection.ScalarInt):
			typeDef.Kind = core.TypeDefKindInteger
		case string(introspection.ScalarBoolean):
			typeDef.Kind = core.TypeDefKindBoolean
		default:
			// default to saying it's a string for now
			typeDef.Kind = core.TypeDefKindString
		}

		return typeDef, true, nil

	case introspection.TypeKindEnum:
		return &core.TypeDef{
			// just call it a string for now
			Kind:     core.TypeDefKindString,
			Optional: !nonNull,
		}, true, nil

	case introspection.TypeKindList:
		elementTypeDef, ok, err := introspectionRefToTypeDef(introspectionType.OfType, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to convert list element type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		return &core.TypeDef{
			Kind:     core.TypeDefKindList,
			Optional: !nonNull,
			AsList: &core.ListTypeDef{
				ElementTypeDef: elementTypeDef,
			},
		}, true, nil

	case introspection.TypeKindObject:
		return &core.TypeDef{
			Kind:     core.TypeDefKindObject,
			Optional: !nonNull,
			AsObject: &core.ObjectTypeDef{
				Name: introspectionType.Name,
			},
		}, true, nil

	case introspection.TypeKindInputObject:
		// don't handle right now, just skip fields that reference these
		return nil, false, nil

	default:
		return nil, false, fmt.Errorf("unexpected type kind %s", introspectionType.Kind)
	}
}
