package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
)

// CoreMod is a special implementation of Mod for our core API, which is not *technically* a true module yet
// but can be treated as one in terms of dependencies. It has no dependencies itself and is currently an
// implicit dependency of every user module.
type CoreMod struct {
	dag *dagql.Server
}

var _ core.Mod = (*CoreMod)(nil)

func (m *CoreMod) Name() string {
	return core.ModuleName
}

func (m *CoreMod) Dependencies() []core.Mod {
	return nil
}

func (m *CoreMod) Install(ctx context.Context, dag *dagql.Server) error {
	for _, schema := range []SchemaResolvers{
		&querySchema{dag},
		&directorySchema{dag},
		&fileSchema{dag},
		&gitSchema{dag},
		&containerSchema{dag},
		&cacheSchema{dag},
		&secretSchema{dag},
		&serviceSchema{dag},
		&hostSchema{dag},
		&httpSchema{dag},
		&platformSchema{dag},
		&socketSchema{dag},
		&moduleSchema{dag},
	} {
		schema.Install()
	}
	return nil
}

func (m *CoreMod) ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (core.ModType, bool, error) {
	var modType core.ModType

	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		modType = &core.PrimitiveType{Def: typeDef}

	case core.TypeDefKindList:
		underlyingType, ok, err := m.ModTypeFor(ctx, typeDef.AsList.Value.ElementTypeDef, checkDirectDeps)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		modType = &core.ListType{
			Elem:       typeDef.AsList.Value.ElementTypeDef,
			Underlying: underlyingType,
		}

	case core.TypeDefKindObject:
		_, ok := m.dag.ObjectType(typeDef.AsObject.Value.Name)
		if !ok {
			return nil, false, nil
		}
		modType = &CoreModObject{coreMod: m}

	case core.TypeDefKindInterface:
		// core does not yet defined any interfaces
		return nil, false, nil

	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}

	if typeDef.Optional {
		modType = &core.NullableType{
			InnerDef: typeDef.WithOptional(false),
			Inner:    modType,
		}
	}

	return modType, true, nil
}

func (m *CoreMod) TypeDefs(ctx context.Context) ([]*core.TypeDef, error) {
	introspectionJSON, err := schemaIntrospectionJSON(ctx, m.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection JSON: %w", err)
	}
	var schemaResp introspection.Response
	if err := json.Unmarshal([]byte(introspectionJSON), &schemaResp); err != nil {
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

			rtType, ok, err := introspectionRefToTypeDef(introspectionField.TypeRef, false, false)
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
					fnArg.DefaultValue = core.JSON(*introspectionArg.DefaultValue)
				}

				argType, ok, err := introspectionRefToTypeDef(introspectionArg.TypeRef, false, true)
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
			AsObject: dagql.NonNull(typeDef),
		})
	}
	return objTypeDefs, nil
}

// CoreModObject represents objects from core (Container, Directory, etc.)
type CoreModObject struct {
	coreMod *CoreMod
	name    string
}

var _ core.ModType = (*CoreModObject)(nil)

func (obj *CoreModObject) ConvertFromSDKResult(ctx context.Context, value any) (dagql.Typed, error) {
	if value == nil {
		// TODO remove if this is OK. Why is this not handled by a wrapping Nullable instead?
		slog.Warn("CoreModObject.ConvertFromSDKResult: got nil value")
		return nil, nil
	}
	id, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("expected string, got %T", value)
	}
	var idp idproto.ID
	if err := idp.Decode(id); err != nil {
		return nil, err
	}
	val, err := obj.coreMod.dag.Load(ctx, &idp)
	if err != nil {
		return nil, fmt.Errorf("CoreModObject.load %s: %w", idp.Display(), err)
	}
	return val, nil
}

func (obj *CoreModObject) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch x := value.(type) {
	case dagql.Input:
		return x, nil
	case dagql.Object:
		return x.ID().Encode()
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput: unknown type %T", obj, value)
	}
}

func (obj *CoreModObject) SourceMod() core.Mod {
	return obj.coreMod
}

func (obj *CoreModObject) TypeDef() *core.TypeDef {
	// TODO: to support matching core types against interfaces, we will need to actually fill
	// this out with the functions rather than just name
	return &core.TypeDef{
		Kind: core.TypeDefKindObject,
		AsObject: dagql.NonNull(&core.ObjectTypeDef{
			Name: obj.name,
		}),
	}
}

func introspectionRefToTypeDef(introspectionType *introspection.TypeRef, nonNull, isInput bool) (*core.TypeDef, bool, error) {
	switch introspectionType.Kind {
	case introspection.TypeKindNonNull:
		return introspectionRefToTypeDef(introspectionType.OfType, true, isInput)

	case introspection.TypeKindScalar:
		if isInput && strings.HasSuffix(introspectionType.Name, "ID") {
			// convert ID inputs to the actual object
			objName := strings.TrimSuffix(introspectionType.Name, "ID")
			return &core.TypeDef{
				Kind:     core.TypeDefKindObject,
				Optional: !nonNull,
				AsObject: dagql.NonNull(&core.ObjectTypeDef{
					Name: objName,
				}),
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
		elementTypeDef, ok, err := introspectionRefToTypeDef(introspectionType.OfType, false, isInput)
		if err != nil {
			return nil, false, fmt.Errorf("failed to convert list element type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		return &core.TypeDef{
			Kind:     core.TypeDefKindList,
			Optional: !nonNull,
			AsList: dagql.NonNull(&core.ListTypeDef{
				ElementTypeDef: elementTypeDef,
			}),
		}, true, nil

	case introspection.TypeKindObject:
		return &core.TypeDef{
			Kind:     core.TypeDefKindObject,
			Optional: !nonNull,
			AsObject: dagql.NonNull(&core.ObjectTypeDef{
				Name: introspectionType.Name,
			}),
		}, true, nil

	case introspection.TypeKindInputObject:
		// don't handle right now, just skip fields that reference these
		return nil, false, nil

	default:
		return nil, false, fmt.Errorf("unexpected type kind %s", introspectionType.Kind)
	}
}
