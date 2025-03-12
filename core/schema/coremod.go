package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

// CoreMod is a special implementation of Mod for our core API, which is not *technically* a true module yet
// but can be treated as one in terms of dependencies. It has no dependencies itself and is currently an
// implicit dependency of every user module.
type CoreMod struct {
	Dag *dagql.Server
}

var _ core.Mod = (*CoreMod)(nil)

func (m *CoreMod) Name() string {
	return core.ModuleName
}

// GetSource returns an empty module source
func (m *CoreMod) GetSource() *core.ModuleSource {
	return &core.ModuleSource{}
}

func (m *CoreMod) View() (string, bool) {
	return m.Dag.View, true
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
		&moduleSourceSchema{dag},
		&moduleSchema{dag},
		&errorSchema{dag},
		&engineSchema{dag},
	} {
		schema.Install()
	}
	return nil
}

func (m *CoreMod) ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (core.ModType, bool, error) {
	var modType core.ModType

	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindFloat, core.TypeDefKindBoolean, core.TypeDefKindVoid:
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

	case core.TypeDefKindScalar:
		_, ok := m.Dag.ScalarType(typeDef.AsScalar.Value.Name)
		if !ok {
			return nil, false, nil
		}
		modType = &CoreModScalar{coreMod: m, name: typeDef.AsScalar.Value.Name}

	case core.TypeDefKindObject:
		_, ok := m.Dag.ObjectType(typeDef.AsObject.Value.Name)
		if !ok {
			return nil, false, nil
		}
		modType = &CoreModObject{coreMod: m, name: typeDef.AsObject.Value.Name}
	case core.TypeDefKindEnum:
		_, ok := m.Dag.ScalarType(typeDef.AsEnum.Value.Name)
		if !ok {
			return nil, false, nil
		}

		modType = &CoreModEnum{coreMod: m, typeDef: typeDef.AsEnum.Value}

	case core.TypeDefKindInterface:
		// core does not yet define any interfaces
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
	introspectionJSON, err := SchemaIntrospectionJSON(ctx, m.Dag)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection JSON for core: %w", err)
	}
	var schemaResp introspection.Response
	if err := json.Unmarshal([]byte(introspectionJSON), &schemaResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal introspection JSON: %w", err)
	}
	schema := schemaResp.Schema

	typeDefs := make([]*core.TypeDef, 0, len(schema.Types))
	for _, introspectionType := range schema.Types {
		switch introspectionType.Kind {
		case introspection.TypeKindObject:
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

			if !isIdable && typeDef.Name != "Query" {
				continue
			}

			typeDefs = append(typeDefs, &core.TypeDef{
				Kind:     core.TypeDefKindObject,
				AsObject: dagql.NonNull(typeDef),
			})

		case introspection.TypeKindInputObject:
			typeDef := &core.InputTypeDef{
				Name: introspectionType.Name,
			}

			for _, introspectionField := range introspectionType.InputFields {
				field := &core.FieldTypeDef{
					Name:        introspectionField.Name,
					Description: introspectionField.Description,
				}
				fieldType, ok, err := introspectionRefToTypeDef(introspectionField.TypeRef, false, false)
				if err != nil {
					return nil, fmt.Errorf("failed to convert return type: %w", err)
				}
				if !ok {
					continue
				}
				field.TypeDef = fieldType
				typeDef.Fields = append(typeDef.Fields, field)
			}

			typeDefs = append(typeDefs, &core.TypeDef{
				Kind:    core.TypeDefKindInput,
				AsInput: dagql.NonNull(typeDef),
			})
		case introspection.TypeKindEnum:
			typedef := &core.EnumTypeDef{
				Name:        introspectionType.Name,
				Description: introspectionType.Description,
			}

			for _, value := range introspectionType.EnumValues {
				typedef.Values = append(typedef.Values, &core.EnumValueTypeDef{
					Name:        value.Name,
					Description: value.Description,
				})
			}

			typeDefs = append(typeDefs, &core.TypeDef{
				Kind:   core.TypeDefKindEnum,
				AsEnum: dagql.NonNull(typedef),
			})

		default:
			continue
		}
	}
	return typeDefs, nil
}

// CoreModScalar represents scalars from core (Platform, etc)
type CoreModScalar struct {
	coreMod *CoreMod
	name    string
}

var _ core.ModType = (*CoreModScalar)(nil)

func (obj *CoreModScalar) ConvertFromSDKResult(ctx context.Context, value any) (dagql.Typed, error) {
	s, ok := obj.coreMod.Dag.ScalarType(obj.name)
	if !ok {
		return nil, fmt.Errorf("CoreModScalar.ConvertFromSDKResult: found no scalar type")
	}
	return s.DecodeInput(value)
}

func (obj *CoreModScalar) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	s, ok := obj.coreMod.Dag.ScalarType(obj.name)
	if !ok {
		return nil, fmt.Errorf("CoreModScalar.ConvertToSDKInput: found no scalar type")
	}
	val, ok := value.(dagql.Scalar[dagql.String])
	if !ok {
		// we assume all core scalars are strings
		return nil, fmt.Errorf("CoreModScalar.ConvertToSDKInput: core scalar should be string")
	}
	return s.DecodeInput(string(val.Value))
}

func (obj *CoreModScalar) CollectCoreIDs(context.Context, dagql.Typed, map[digest.Digest]*resource.ID) error {
	return nil
}

func (obj *CoreModScalar) SourceMod() core.Mod {
	return obj.coreMod
}

func (obj *CoreModScalar) TypeDef() *core.TypeDef {
	return &core.TypeDef{
		Kind: core.TypeDefKindScalar,
		AsScalar: dagql.NonNull(&core.ScalarTypeDef{
			Name: obj.name,
		}),
	}
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
	var idp call.ID
	if err := idp.Decode(id); err != nil {
		return nil, err
	}
	val, err := obj.coreMod.Dag.Load(ctx, &idp)
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

func (obj *CoreModObject) CollectCoreIDs(ctx context.Context, value dagql.Typed, ids map[digest.Digest]*resource.ID) error {
	if value == nil {
		return nil
	}
	switch x := value.(type) {
	case dagql.Input:
		return nil
	case dagql.Object:
		ids[x.ID().Digest()] = &resource.ID{ID: *x.ID()}
		return nil
	default:
		return fmt.Errorf("%T.CollectCoreIDs: unknown type %T", obj, value)
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

type CoreModEnum struct {
	coreMod *CoreMod
	typeDef *core.EnumTypeDef
}

var _ core.ModType = (*CoreModEnum)(nil)

func (enum *CoreModEnum) ConvertFromSDKResult(ctx context.Context, value any) (dagql.Typed, error) {
	s, ok := enum.coreMod.Dag.ScalarType(enum.typeDef.Name)
	if !ok {
		return nil, fmt.Errorf("CoreModEnum.ConvertFromSDKResult: found no enum type")
	}
	return s.DecodeInput(value)
}

func (enum *CoreModEnum) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	s, ok := enum.coreMod.Dag.ScalarType(enum.typeDef.Name)
	if !ok {
		return nil, fmt.Errorf("CoreModEnum.ConvertToSDKInput: found no enum type")
	}
	return s.DecodeInput(value)
}

func (enum *CoreModEnum) CollectCoreIDs(ctx context.Context, value dagql.Typed, ids map[digest.Digest]*resource.ID) error {
	return nil
}

func (enum *CoreModEnum) SourceMod() core.Mod {
	return enum.coreMod
}

func (enum *CoreModEnum) TypeDef() *core.TypeDef {
	return &core.TypeDef{
		Kind:   core.TypeDefKindEnum,
		AsEnum: dagql.NonNull(enum.typeDef),
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
		case string(introspection.ScalarFloat):
			typeDef.Kind = core.TypeDefKindFloat
		case string(introspection.ScalarBoolean):
			typeDef.Kind = core.TypeDefKindBoolean
		case string(introspection.ScalarVoid):
			typeDef.Kind = core.TypeDefKindVoid
		default:
			// assume that all core scalars are strings
			typeDef.Kind = core.TypeDefKindScalar
			typeDef.AsScalar = dagql.NonNull(core.NewScalarTypeDef(introspectionType.Name, ""))
		}

		return typeDef, true, nil

	case introspection.TypeKindEnum:
		return &core.TypeDef{
			Kind:     core.TypeDefKindEnum,
			Optional: !nonNull,
			AsEnum: dagql.NonNull(&core.EnumTypeDef{
				Name: introspectionType.Name,
			}),
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
		return &core.TypeDef{
			Kind:     core.TypeDefKindInput,
			Optional: !nonNull,
			AsInput: dagql.NonNull(&core.InputTypeDef{
				Name: introspectionType.Name,
			}),
		}, true, nil

	default:
		return nil, false, fmt.Errorf("unexpected type kind %s", introspectionType.Kind)
	}
}
