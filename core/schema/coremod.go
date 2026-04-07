package schema

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

// CoreMod is a special implementation of Mod for our core API, which is not *technically* a true module yet
// but can be treated as one in terms of dependencies. It has no dependencies itself and is currently an
// implicit dependency of every user module.
type CoreMod struct {
	Dag *dagql.Server

	cachedTypedefs   []*core.TypeDef
	cachedTypedefsMu sync.Mutex
}

var _ core.Mod = (*CoreMod)(nil)

func (m *CoreMod) Name() string {
	return core.ModuleName
}

// GetSource returns an empty module source
func (m *CoreMod) GetSource() *core.ModuleSource {
	return &core.ModuleSource{}
}

func (m *CoreMod) View() (call.View, bool) {
	return m.Dag.View, true
}

func (m *CoreMod) Install(ctx context.Context, dag *dagql.Server, _ ...core.InstallOpts) error {
	for _, schema := range []SchemaResolvers{
		&querySchema{dag},
		&environmentSchema{dag}, // install environment middleware first
		&directorySchema{},
		&fileSchema{},
		&gitSchema{},
		&containerSchema{},
		&cacheSchema{},
		&secretSchema{},
		&serviceSchema{},
		&hostSchema{},
		&httpSchema{},
		&platformSchema{},
		&socketSchema{},
		&volumeSchema{},
		&moduleSourceSchema{},
		&moduleSchema{},
		&errorSchema{},
		&engineSchema{},
		&cloudSchema{},
		&llmSchema{dag},
		&jsonvalueSchema{},
		&envfileSchema{},
		&addressSchema{},
		&checksSchema{},
		&generatorsSchema{},
		&upSchema{},
		&workspaceSchema{},
	} {
		schema.Install(dag)
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

		var resolvedDef *core.TypeDef
		defs, err := m.typedefs(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, def := range defs {
			if def.Kind == core.TypeDefKindScalar && def.AsScalar.Value.Name == typeDef.AsScalar.Value.Name {
				resolvedDef = def
				break
			}
		}
		if resolvedDef == nil {
			return nil, false, fmt.Errorf("could not resolve scalar def %s", typeDef.AsScalar.Value.Name)
		}

		modType = &CoreModScalar{coreMod: m, name: resolvedDef.AsScalar.Value.Name}

	case core.TypeDefKindObject:
		_, ok := m.Dag.ObjectType(typeDef.AsObject.Value.Name)
		if !ok {
			return nil, false, nil
		}

		var resolvedDef *core.TypeDef
		defs, err := m.typedefs(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, def := range defs {
			if def.Kind == core.TypeDefKindObject && def.AsObject.Value.Name == typeDef.AsObject.Value.Name {
				resolvedDef = def
				break
			}
		}
		if resolvedDef == nil {
			return nil, false, fmt.Errorf("could not resolve object def %s", typeDef.AsObject.Value.Name)
		}

		modType = &CoreModObject{coreMod: m, name: resolvedDef.AsObject.Value.Name}

	case core.TypeDefKindEnum:
		_, ok := m.Dag.ScalarType(typeDef.AsEnum.Value.Name)
		if !ok {
			return nil, false, nil
		}

		var resolvedDef *core.TypeDef
		defs, err := m.typedefs(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, def := range defs {
			if def.Kind == core.TypeDefKindEnum && def.AsEnum.Value.Name == typeDef.AsEnum.Value.Name {
				resolvedDef = def
				break
			}
		}
		if resolvedDef == nil {
			return nil, false, fmt.Errorf("could not resolve enum def %s", typeDef.AsEnum.Value.Name)
		}

		modType = &CoreModEnum{coreMod: m, typeDef: resolvedDef.AsEnum.Value}

	case core.TypeDefKindInterface:
		// core does not yet define any interfaces
		return nil, false, nil

	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}

	if typeDef.Optional {
		modType = &core.NullableType{
			InnerDef: modType.TypeDef().WithOptional(false),
			Inner:    modType,
		}
	}

	return modType, true, nil
}

func (m *CoreMod) typedefs(ctx context.Context) ([]*core.TypeDef, error) {
	m.cachedTypedefsMu.Lock()
	defer m.cachedTypedefsMu.Unlock()

	if m.cachedTypedefs == nil {
		typedefs, err := m.TypeDefs(ctx, m.Dag)
		if err != nil {
			return nil, err
		}
		m.cachedTypedefs = typedefs
	}
	return m.cachedTypedefs, nil
}

func (m *CoreMod) TypeDefs(ctx context.Context, dag *dagql.Server) ([]*core.TypeDef, error) {
	// Filter to only types present in the core schema (before any modules
	// are installed).
	initialSchema := m.Dag.Schema()
	return core.TypeDefsFromSchema(dag, initialSchema.Types)
}

// CoreModScalar represents scalars from core (Platform, etc)
type CoreModScalar struct {
	coreMod *CoreMod
	name    string
}

var _ core.ModType = (*CoreModScalar)(nil)

func (obj *CoreModScalar) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	s, ok := obj.coreMod.Dag.ScalarType(obj.name)
	if !ok {
		return nil, fmt.Errorf("CoreModScalar.ConvertFromSDKResult: found no scalar type")
	}

	input, err := s.DecodeInput(value)
	if err != nil {
		return nil, fmt.Errorf("CoreModScalar.ConvertFromSDKResult: failed to decode input: %w", err)
	}
	return dagql.NewResultForCurrentID(ctx, input)
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

func (obj *CoreModScalar) CollectContent(_ context.Context, value dagql.AnyResult, content *core.CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}
	return content.CollectJSONable(value.Unwrap())
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

func (obj *CoreModObject) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	if value == nil {
		// TODO remove if this is OK. Why is this not handled by a wrapping Nullable instead?
		slog.ExtraDebug("CoreModObject.ConvertFromSDKResult: got nil value")
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

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("CoreModObject.ConvertFromSDKResult: failed to get current query: %w", err)
	}
	c, err := query.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("CoreModObject.ConvertFromSDKResult: failed to get query cache: %w", err)
	}
	dag := obj.coreMod.Dag.WithCache(c)

	val, err := dag.Load(ctx, &idp)
	if err != nil {
		return nil, fmt.Errorf("CoreModObject.load %s: %w", idp.DisplaySelf(), err)
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
	case dagql.AnyResult:
		return x.ID().Encode()
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput: unknown type %T", obj, value)
	}
}

func (obj *CoreModObject) CollectContent(ctx context.Context, value dagql.AnyResult, content *core.CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}
	switch x := value.(type) {
	case dagql.Input:
		return content.CollectJSONable(x)
	case dagql.AnyResult:
		content.CollectID(*x.ID(), false)
		return nil
	default:
		return content.CollectJSONable(value.Unwrap())
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

func (enum *CoreModEnum) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	if enum, ok := value.(*core.ModuleEnum); ok {
		value = enum.Name
	}
	s, ok := enum.coreMod.Dag.ScalarType(enum.typeDef.Name)
	if !ok {
		return nil, fmt.Errorf("CoreModEnum.ConvertFromSDKResult: found no enum type")
	}

	input, err := s.DecodeInput(value)
	if err != nil {
		return nil, fmt.Errorf("CoreModEnum.ConvertFromSDKResult: failed to decode input: %w", err)
	}
	return dagql.NewResultForCurrentID(ctx, input)
}

func (enum *CoreModEnum) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	var input any = value
	if enum, ok := value.(*core.ModuleEnum); ok {
		input = enum.Name
	}
	s, ok := enum.coreMod.Dag.ScalarType(enum.typeDef.Name)
	if !ok {
		return nil, fmt.Errorf("CoreModEnum.ConvertToSDKInput: found no enum type")
	}
	return s.DecodeInput(input)
}

func (enum *CoreModEnum) CollectContent(_ context.Context, value dagql.AnyResult, content *core.CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}
	return content.CollectJSONable(value.Unwrap())
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
