package schema

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	dagqlintrospection "github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine/slog"
)

// CoreMod is a special implementation of Mod for our core API, which is not *technically* a true module yet
// but can be treated as one in terms of dependencies. It has no dependencies itself and is currently an
// implicit dependency of every user module.
type CoreSchemaBase struct {
	base    *dagql.Server
	rootSrv core.Server
	views   map[call.View]*coreSchemaViewState
	mu      sync.Mutex
}

type coreSchemaViewState struct {
	server *dagql.Server

	typedefs         dagql.ObjectResultArray[*core.TypeDef]
	objectDefsByName map[string]dagql.ObjectResult[*core.TypeDef]
	scalarDefsByName map[string]dagql.ObjectResult[*core.TypeDef]
	enumDefsByName   map[string]dagql.ObjectResult[*core.TypeDef]
}

type CoreMod struct {
	base *CoreSchemaBase
	view call.View
}

var _ core.Mod = (*CoreMod)(nil)

func NewCoreSchemaBase(ctx context.Context, rootSrv core.Server) (*CoreSchemaBase, error) {
	base, err := dagql.NewServer(ctx, core.NewRoot(rootSrv))
	if err != nil {
		return nil, err
	}
	base.Around(core.AroundFunc)
	coreMod := &CoreMod{}
	if err := coreMod.Install(ctx, base); err != nil {
		return nil, err
	}
	return &CoreSchemaBase{
		base:    base,
		rootSrv: rootSrv,
		views:   map[call.View]*coreSchemaViewState{},
	}, nil
}

func (base *CoreSchemaBase) CoreMod(view call.View) *CoreMod {
	return &CoreMod{
		base: base,
		view: view,
	}
}

func (base *CoreSchemaBase) Fork(ctx context.Context, root *core.Query, view call.View) (*dagql.Server, error) {
	state, err := base.viewState(ctx, view)
	if err != nil {
		return nil, err
	}
	return state.server.Fork(ctx, root)
}

func (base *CoreSchemaBase) viewState(ctx context.Context, view call.View) (*coreSchemaViewState, error) {
	base.mu.Lock()
	defer base.mu.Unlock()

	if state, ok := base.views[view]; ok {
		return state, nil
	}

	srv, err := base.base.Fork(ctx, core.NewRoot(base.rootSrv))
	if err != nil {
		return nil, fmt.Errorf("fork core schema base: %w", err)
	}
	srv.View = view

	coreMod := base.CoreMod(view)
	typedefs, err := coreMod.buildTypeDefs(ctx, srv)
	if err != nil {
		return nil, err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("load engine cache for core typedef retention: %w", err)
	}
	for _, typedef := range typedefs {
		if typedef.Self() == nil {
			continue
		}
		if err := cache.MakeResultUnpruneable(ctx, typedef); err != nil {
			return nil, fmt.Errorf("retain core typedef %q: %w", typedef.Self().Name, err)
		}
	}
	state := &coreSchemaViewState{
		server:           srv,
		typedefs:         typedefs,
		objectDefsByName: make(map[string]dagql.ObjectResult[*core.TypeDef]),
		scalarDefsByName: make(map[string]dagql.ObjectResult[*core.TypeDef]),
		enumDefsByName:   make(map[string]dagql.ObjectResult[*core.TypeDef]),
	}
	for _, typedef := range typedefs {
		if typedef.Self() == nil {
			continue
		}
		switch typedef.Self().Kind {
		case core.TypeDefKindObject:
			state.objectDefsByName[typedef.Self().AsObject.Value.Self().Name] = typedef
		case core.TypeDefKindScalar:
			state.scalarDefsByName[typedef.Self().AsScalar.Value.Self().Name] = typedef
		case core.TypeDefKindEnum:
			state.enumDefsByName[typedef.Self().AsEnum.Value.Self().Name] = typedef
		}
	}
	base.views[view] = state
	return state, nil
}

func (m *CoreMod) Name() string {
	return core.ModuleName
}

func (m *CoreMod) Same(other core.Mod) (bool, error) {
	_, ok := other.(*CoreMod)
	return ok, nil
}

// GetSource returns an empty module source
func (m *CoreMod) GetSource() *core.ModuleSource {
	return &core.ModuleSource{}
}

func (m *CoreMod) View() (call.View, bool) {
	return m.view, true
}

func (m *CoreMod) ResultCallModule(context.Context) (*dagql.ResultCallModule, error) {
	return nil, nil
}

func (m *CoreMod) ModuleResult() dagql.ObjectResult[*core.Module] {
	return dagql.ObjectResult[*core.Module]{}
}

func (m *CoreMod) ForkSchema(ctx context.Context, root *core.Query, view call.View) (*dagql.Server, error) {
	if m.base == nil {
		return nil, fmt.Errorf("core schema base is nil")
	}
	return m.base.Fork(ctx, root, view)
}

func (m *CoreMod) WithView(view call.View) *CoreMod {
	return &CoreMod{
		base: m.base,
		view: view,
	}
}

func (m *CoreMod) Install(ctx context.Context, dag *dagql.Server, _ ...core.InstallOpts) error {
	for _, schema := range []SchemaResolvers{
		&querySchema{},
		&environmentSchema{}, // install environment middleware first
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
		&moduleSourceSchema{},
		&moduleSchema{},
		&errorSchema{},
		&engineSchema{},
		&cloudSchema{},
		&llmSchema{},
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
	state, err := m.viewState(ctx)
	if err != nil {
		return nil, false, err
	}

	var modType core.ModType

	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindFloat, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		modType = &core.PrimitiveType{Def: typeDef}

	case core.TypeDefKindList:
		underlyingType, ok, err := m.ModTypeFor(ctx, typeDef.AsList.Value.Self().ElementTypeDef.Self(), checkDirectDeps)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		modType = &core.ListType{
			Elem:       typeDef.AsList.Value.Self().ElementTypeDef,
			Underlying: underlyingType,
		}

	case core.TypeDefKindScalar:
		_, ok := state.server.ScalarType(typeDef.AsScalar.Value.Self().Name)
		if !ok {
			return nil, false, nil
		}

		resolvedDef := state.scalarDefsByName[typeDef.AsScalar.Value.Self().Name]
		if resolvedDef.Self() == nil {
			return nil, false, fmt.Errorf("could not resolve scalar def %s", typeDef.AsScalar.Value.Self().Name)
		}

		modType = &CoreModScalar{coreMod: m, name: resolvedDef.Self().AsScalar.Value.Self().Name}

	case core.TypeDefKindObject:
		_, ok := state.server.ObjectType(typeDef.AsObject.Value.Self().Name)
		if !ok {
			return nil, false, nil
		}

		resolvedDef := state.objectDefsByName[typeDef.AsObject.Value.Self().Name]
		if resolvedDef.Self() == nil {
			return nil, false, fmt.Errorf("could not resolve object def %s", typeDef.AsObject.Value.Self().Name)
		}

		modType = &CoreModObject{coreMod: m, name: resolvedDef.Self().AsObject.Value.Self().Name}

	case core.TypeDefKindEnum:
		_, ok := state.server.ScalarType(typeDef.AsEnum.Value.Self().Name)
		if !ok {
			return nil, false, nil
		}

		resolvedDef := state.enumDefsByName[typeDef.AsEnum.Value.Self().Name]
		if resolvedDef.Self() == nil {
			return nil, false, fmt.Errorf("could not resolve enum def %s", typeDef.AsEnum.Value.Self().Name)
		}

		modType = &CoreModEnum{coreMod: m, typeDef: resolvedDef.Self().AsEnum.Value.Self()}

	case core.TypeDefKindInterface:
		// core does not yet define any interfaces
		return nil, false, nil

	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}

	if typeDef.Optional {
		innerDef, err := modType.TypeDef(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("resolve core nullable inner typedef: %w", err)
		}
		if innerDef.Self().Optional {
			if err := state.server.Select(ctx, innerDef, &innerDef, dagql.Selector{
				Field: "withOptional",
				Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(false)}},
			}); err != nil {
				return nil, false, fmt.Errorf("clear optional on core nullable inner typedef: %w", err)
			}
		}
		modType = &core.NullableType{
			InnerDef: innerDef,
			Inner:    modType,
		}
	}

	return modType, true, nil
}

func (m *CoreMod) viewState(ctx context.Context) (*coreSchemaViewState, error) {
	if m.base == nil {
		return nil, fmt.Errorf("core schema base is nil")
	}
	return m.base.viewState(ctx, m.view)
}

func (m *CoreMod) TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*core.TypeDef], error) {
	_ = dag

	state, err := m.viewState(ctx)
	if err != nil {
		return nil, err
	}
	return state.typedefs, nil
}

func (m *CoreMod) buildTypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*core.TypeDef], error) {
	dagqlSchema := dagqlintrospection.WrapSchema(dag.Schema())
	schema := &introspection.Schema{}
	if queryName := dagqlSchema.QueryType().Name(); queryName != nil {
		schema.QueryType.Name = *queryName
	}
	for _, dagqlType := range dagqlSchema.Types() {
		codeGenType, err := core.DagqlToCodegenType(dagqlType)
		if err != nil {
			return nil, err
		}
		schema.Types = append(schema.Types, codeGenType)
	}
	directives, err := dagqlSchema.Directives()
	if err != nil {
		return nil, err
	}
	for _, dagqlDirective := range directives {
		dd, err := core.DagqlToCodegenDirectiveDef(dagqlDirective)
		if err != nil {
			return nil, err
		}
		schema.Directives = append(schema.Directives, dd)
	}

	typeDefs := make(dagql.ObjectResultArray[*core.TypeDef], 0, len(schema.Types))
	for _, introspectionType := range schema.Types {
		switch introspectionType.Kind {
		case introspection.TypeKindObject:
			var obj dagql.ObjectResult[*core.ObjectTypeDef]
			if err := dag.Select(ctx, dag.Root(), &obj, dagql.Selector{
				Field: "__objectTypeDef",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(introspectionType.Name)},
					{Name: "description", Value: dagql.String(introspectionType.Description)},
				},
			}); err != nil {
				return nil, err
			}

			isIdable := false
			for _, introspectionField := range introspectionType.Fields {
				if introspectionField.Name == "id" {
					isIdable = true
					continue
				}

				rtType, ok, err := introspectionRefToTypeDef(ctx, dag, introspectionField.TypeRef, false, false)
				if err != nil {
					return nil, fmt.Errorf("failed to convert return type: %w", err)
				}
				if !ok {
					continue
				}
				rtTypeID, err := core.ResultIDInput(rtType)
				if err != nil {
					return nil, err
				}
				var fn dagql.ObjectResult[*core.Function]
				if err := dag.Select(ctx, dag.Root(), &fn, dagql.Selector{
					Field: "__function",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(introspectionField.Name)},
						{Name: "returnType", Value: rtTypeID},
					},
				}); err != nil {
					return nil, err
				}
				if introspectionField.Description != "" {
					if err := dag.Select(ctx, fn, &fn, dagql.Selector{
						Field: "withDescription",
						Args:  []dagql.NamedInput{{Name: "description", Value: dagql.String(introspectionField.Description)}},
					}); err != nil {
						return nil, err
					}
				}
				if introspectionField.DeprecationReason != nil {
					if err := dag.Select(ctx, fn, &fn, dagql.Selector{
						Field: "withDeprecated",
						Args:  []dagql.NamedInput{{Name: "reason", Value: core.OptString(introspectionField.DeprecationReason)}},
					}); err != nil {
						return nil, err
					}
				}

				for _, introspectionArg := range introspectionField.Args {
					argType, ok, err := introspectionRefToTypeDef(ctx, dag, introspectionArg.TypeRef, false, true)
					if err != nil {
						return nil, fmt.Errorf("failed to convert argument type: %w", err)
					}
					if !ok {
						continue
					}
					argTypeID, err := core.ResultIDInput(argType)
					if err != nil {
						return nil, err
					}
					var defaultValue core.JSON
					if introspectionArg.DefaultValue != nil {
						defaultValue = core.JSON(*introspectionArg.DefaultValue)
					}
					var fnArg dagql.ObjectResult[*core.FunctionArg]
					if err := dag.Select(ctx, dag.Root(), &fnArg, dagql.Selector{
						Field: "__functionArg",
						Args: []dagql.NamedInput{
							{Name: "name", Value: dagql.String(introspectionArg.Name)},
							{Name: "typeDef", Value: argTypeID},
							{Name: "description", Value: dagql.String(introspectionArg.Description)},
							{Name: "defaultValue", Value: defaultValue},
							{Name: "deprecated", Value: core.OptString(introspectionArg.DeprecationReason)},
						},
					}); err != nil {
						return nil, err
					}
					fnArgID, err := core.ResultIDInput(fnArg)
					if err != nil {
						return nil, err
					}
					if err := dag.Select(ctx, fn, &fn, dagql.Selector{
						Field: "__withArg",
						Args:  []dagql.NamedInput{{Name: "arg", Value: fnArgID}},
					}); err != nil {
						return nil, err
					}
				}

				fnID, err := core.ResultIDInput(fn)
				if err != nil {
					return nil, err
				}
				if err := dag.Select(ctx, obj, &obj, dagql.Selector{
					Field: "__withFunction",
					Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
				}); err != nil {
					return nil, err
				}
			}

			if !isIdable && introspectionType.Name != "Query" {
				continue
			}
			objID, err := core.ResultIDInput(obj)
			if err != nil {
				return nil, err
			}
			typeDef, err := core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "__withObjectTypeDef",
				Args:  []dagql.NamedInput{{Name: "objectTypeDef", Value: objID}},
			})
			if err != nil {
				return nil, err
			}
			typeDefs = append(typeDefs, typeDef)

		case introspection.TypeKindInputObject:
			var input dagql.ObjectResult[*core.InputTypeDef]
			if err := dag.Select(ctx, dag.Root(), &input, dagql.Selector{
				Field: "__inputTypeDef",
				Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(introspectionType.Name)}},
			}); err != nil {
				return nil, err
			}

			for _, introspectionField := range introspectionType.InputFields {
				fieldType, ok, err := introspectionRefToTypeDef(ctx, dag, introspectionField.TypeRef, false, false)
				if err != nil {
					return nil, fmt.Errorf("failed to convert return type: %w", err)
				}
				if !ok {
					continue
				}
				fieldTypeID, err := core.ResultIDInput(fieldType)
				if err != nil {
					return nil, err
				}
				var field dagql.ObjectResult[*core.FieldTypeDef]
				if err := dag.Select(ctx, dag.Root(), &field, dagql.Selector{
					Field: "__fieldTypeDef",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(introspectionField.Name)},
						{Name: "typeDef", Value: fieldTypeID},
						{Name: "description", Value: dagql.String(introspectionField.Description)},
						{Name: "deprecated", Value: core.OptString(introspectionField.DeprecationReason)},
					},
				}); err != nil {
					return nil, err
				}
				fieldID, err := core.ResultIDInput(field)
				if err != nil {
					return nil, err
				}
				if err := dag.Select(ctx, input, &input, dagql.Selector{
					Field: "__withField",
					Args:  []dagql.NamedInput{{Name: "field", Value: fieldID}},
				}); err != nil {
					return nil, err
				}
			}
			inputID, err := core.ResultIDInput(input)
			if err != nil {
				return nil, err
			}
			typeDef, err := core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "__withInputTypeDef",
				Args:  []dagql.NamedInput{{Name: "inputTypeDef", Value: inputID}},
			})
			if err != nil {
				return nil, err
			}
			typeDefs = append(typeDefs, typeDef)

		case introspection.TypeKindScalar:
			var (
				typeDef dagql.ObjectResult[*core.TypeDef]
				err     error
			)
			switch introspectionType.Name {
			case string(introspection.ScalarString):
				typeDef, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
					Field: "withKind",
					Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindString}},
				})
			case string(introspection.ScalarInt):
				typeDef, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
					Field: "withKind",
					Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindInteger}},
				})
			case string(introspection.ScalarFloat):
				typeDef, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
					Field: "withKind",
					Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindFloat}},
				})
			case string(introspection.ScalarBoolean):
				typeDef, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
					Field: "withKind",
					Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindBoolean}},
				})
			case string(introspection.ScalarVoid):
				typeDef, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
					Field: "withKind",
					Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindVoid}},
				})
			default:
				typeDef, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
					Field: "withScalar",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(introspectionType.Name)},
						{Name: "description", Value: dagql.String(introspectionType.Description)},
					},
				})
			}
			if err != nil {
				return nil, err
			}
			typeDefs = append(typeDefs, typeDef)

		case introspection.TypeKindEnum:
			var enum dagql.ObjectResult[*core.EnumTypeDef]
			if err := dag.Select(ctx, dag.Root(), &enum, dagql.Selector{
				Field: "__enumTypeDef",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(introspectionType.Name)},
					{Name: "description", Value: dagql.String(introspectionType.Description)},
				},
			}); err != nil {
				return nil, err
			}

			for _, value := range introspectionType.EnumValues {
				var member dagql.ObjectResult[*core.EnumMemberTypeDef]
				if err := dag.Select(ctx, dag.Root(), &member, dagql.Selector{
					Field: "__enumMemberTypeDef",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(value.Name)},
						{Name: "value", Value: dagql.String(value.Directives.EnumValue())},
						{Name: "description", Value: dagql.String(value.Description)},
						{Name: "deprecated", Value: core.OptString(value.DeprecationReason)},
					},
				}); err != nil {
					return nil, err
				}
				memberID, err := core.ResultIDInput(member)
				if err != nil {
					return nil, err
				}
				if err := dag.Select(ctx, enum, &enum, dagql.Selector{
					Field: "__withMember",
					Args:  []dagql.NamedInput{{Name: "member", Value: memberID}},
				}); err != nil {
					return nil, err
				}
			}
			enumID, err := core.ResultIDInput(enum)
			if err != nil {
				return nil, err
			}
			typeDef, err := core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "__withEnumTypeDef",
				Args:  []dagql.NamedInput{{Name: "enumTypeDef", Value: enumID}},
			})
			if err != nil {
				return nil, err
			}
			typeDefs = append(typeDefs, typeDef)

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

func (obj *CoreModScalar) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	state, err := obj.coreMod.viewState(ctx)
	if err != nil {
		return nil, err
	}
	s, ok := state.server.ScalarType(obj.name)
	if !ok {
		return nil, fmt.Errorf("CoreModScalar.ConvertFromSDKResult: found no scalar type")
	}

	input, err := s.DecodeInput(value)
	if err != nil {
		return nil, fmt.Errorf("CoreModScalar.ConvertFromSDKResult: failed to decode input: %w", err)
	}
	return dagql.NewResultForCurrentCall(ctx, input)
}

func (obj *CoreModScalar) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	state, err := obj.coreMod.viewState(ctx)
	if err != nil {
		return nil, err
	}
	s, ok := state.server.ScalarType(obj.name)
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

func (obj *CoreModScalar) CollectContent(ctx context.Context, value dagql.AnyResult, content *core.CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}
	return content.CollectJSONable(value.Unwrap())
}

func (obj *CoreModScalar) SourceMod() core.Mod {
	return obj.coreMod
}

func (obj *CoreModScalar) TypeDef(ctx context.Context) (dagql.ObjectResult[*core.TypeDef], error) {
	state, err := obj.coreMod.viewState(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, err
	}
	return core.SelectTypeDefWithServer(ctx, state.server, dagql.Selector{
		Field: "withScalar",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(obj.name)},
		},
	})
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

	if res, ok := value.(dagql.AnyResult); ok {
		return res, nil
	}

	id, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("expected string, got %T", value)
	}
	var idp call.ID
	if err := idp.Decode(id); err != nil {
		return nil, err
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("current dagql server: %w", err)
	}
	val, err := dag.Load(ctx, &idp)
	if err != nil {
		return nil, fmt.Errorf("CoreModObject.load: %w", err)
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
		id, err := x.ID()
		if err != nil {
			return nil, err
		}
		if id == nil {
			return nil, nil
		}
		return id.Encode()
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput: unknown type %T", obj, value)
	}
}

func (obj *CoreModObject) CollectContent(ctx context.Context, value dagql.AnyResult, content *core.CollectedContent) error {
	if value == nil {
		if err := content.CollectJSONable(nil); err != nil {
			return fmt.Errorf("collect_jsonable nil core object %q: %w", obj.name, err)
		}
		return nil
	}
	switch x := value.(type) {
	case dagql.Input:
		if err := content.CollectJSONable(x); err != nil {
			return fmt.Errorf("collect_jsonable input core object %q (%T): %w", obj.name, x, err)
		}
		return nil
	case dagql.AnyResult:
		id, err := x.ID()
		if err != nil {
			return fmt.Errorf("resolve result ID core object %q (%T): %w", obj.name, x, err)
		}
		if err := content.CollectID(ctx, id, false); err != nil {
			return fmt.Errorf("collect_id core object %q (%T): %w", obj.name, x, err)
		}
		return nil
	default:
		unwrapped := value.Unwrap()
		if err := content.CollectJSONable(unwrapped); err != nil {
			return fmt.Errorf("collect_jsonable unwrapped core object %q (%T): %w", obj.name, unwrapped, err)
		}
		return nil
	}
}

func (obj *CoreModObject) SourceMod() core.Mod {
	return obj.coreMod
}

func (obj *CoreModObject) TypeDef(ctx context.Context) (dagql.ObjectResult[*core.TypeDef], error) {
	// TODO: to support matching core types against interfaces, we will need to actually fill
	// this out with the functions rather than just name
	state, err := obj.coreMod.viewState(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, err
	}
	return core.SelectTypeDefWithServer(ctx, state.server, dagql.Selector{
		Field: "withObject",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(obj.name)},
		},
	})
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
	state, err := enum.coreMod.viewState(ctx)
	if err != nil {
		return nil, err
	}
	s, ok := state.server.ScalarType(enum.typeDef.Name)
	if !ok {
		return nil, fmt.Errorf("CoreModEnum.ConvertFromSDKResult: found no enum type")
	}

	input, err := s.DecodeInput(value)
	if err != nil {
		return nil, fmt.Errorf("CoreModEnum.ConvertFromSDKResult: failed to decode input: %w", err)
	}
	return dagql.NewResultForCurrentCall(ctx, input)
}

func (enum *CoreModEnum) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	var input any = value
	if enum, ok := value.(*core.ModuleEnum); ok {
		input = enum.Name
	}
	state, err := enum.coreMod.viewState(ctx)
	if err != nil {
		return nil, err
	}
	s, ok := state.server.ScalarType(enum.typeDef.Name)
	if !ok {
		return nil, fmt.Errorf("CoreModEnum.ConvertToSDKInput: found no enum type")
	}
	return s.DecodeInput(input)
}

func (enum *CoreModEnum) CollectContent(ctx context.Context, value dagql.AnyResult, content *core.CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}
	return content.CollectJSONable(value.Unwrap())
}

func (enum *CoreModEnum) SourceMod() core.Mod {
	return enum.coreMod
}

func (enum *CoreModEnum) TypeDef(ctx context.Context) (dagql.ObjectResult[*core.TypeDef], error) {
	var sourceMap dagql.Optional[dagql.ID[*core.SourceMap]]
	var err error
	if enum.typeDef.SourceMap.Valid {
		sourceMap, err = core.OptionalResultIDInput(enum.typeDef.SourceMap.Value)
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, err
		}
	}
	state, err := enum.coreMod.viewState(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, err
	}
	return core.SelectTypeDefWithServer(ctx, state.server, dagql.Selector{
		Field: "withEnum",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(enum.typeDef.OriginalName)},
			{Name: "description", Value: dagql.String(enum.typeDef.Description)},
			{Name: "sourceMap", Value: sourceMap},
			{Name: "sourceModuleName", Value: core.OptSourceModuleName(enum.typeDef.SourceModuleName)},
		},
	})
}

func introspectionRefToTypeDef(ctx context.Context, dag *dagql.Server, introspectionType *introspection.TypeRef, nonNull, isInput bool) (dagql.ObjectResult[*core.TypeDef], bool, error) {
	maybeOptional := func(inst dagql.ObjectResult[*core.TypeDef]) (dagql.ObjectResult[*core.TypeDef], error) {
		if nonNull {
			return inst, nil
		}
		if err := dag.Select(ctx, inst, &inst, dagql.Selector{
			Field: "withOptional",
			Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
		}); err != nil {
			return inst, fmt.Errorf("make typedef optional: %w", err)
		}
		return inst, nil
	}

	switch introspectionType.Kind {
	case introspection.TypeKindNonNull:
		return introspectionRefToTypeDef(ctx, dag, introspectionType.OfType, true, isInput)

	case introspection.TypeKindScalar:
		if isInput && strings.HasSuffix(introspectionType.Name, "ID") {
			inst, err := core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "withObject",
				Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(strings.TrimSuffix(introspectionType.Name, "ID"))}},
			})
			if err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, false, err
			}
			inst, err = maybeOptional(inst)
			return inst, true, err
		}

		var (
			inst dagql.ObjectResult[*core.TypeDef]
			err  error
		)
		switch introspectionType.Name {
		case string(introspection.ScalarString):
			inst, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindString}},
			})
		case string(introspection.ScalarInt):
			inst, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindInteger}},
			})
		case string(introspection.ScalarFloat):
			inst, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindFloat}},
			})
		case string(introspection.ScalarBoolean):
			inst, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindBoolean}},
			})
		case string(introspection.ScalarVoid):
			inst, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindVoid}},
			})
		default:
			inst, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
				Field: "withScalar",
				Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(introspectionType.Name)}},
			})
		}
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, err
		}
		inst, err = maybeOptional(inst)
		return inst, true, err

	case introspection.TypeKindEnum:
		inst, err := core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
			Field: "withEnum",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(introspectionType.Name)}},
		})
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, err
		}
		inst, err = maybeOptional(inst)
		return inst, true, err

	case introspection.TypeKindList:
		elementTypeDef, ok, err := introspectionRefToTypeDef(ctx, dag, introspectionType.OfType, false, isInput)
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, fmt.Errorf("failed to convert list element type: %w", err)
		}
		if !ok {
			return dagql.ObjectResult[*core.TypeDef]{}, false, nil
		}
		elementTypeDefID, err := core.ResultIDInput(elementTypeDef)
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, err
		}
		inst, err := core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
			Field: "withListOf",
			Args:  []dagql.NamedInput{{Name: "elementType", Value: elementTypeDefID}},
		})
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, err
		}
		inst, err = maybeOptional(inst)
		return inst, true, err

	case introspection.TypeKindObject:
		inst, err := core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
			Field: "withObject",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(introspectionType.Name)}},
		})
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, err
		}
		inst, err = maybeOptional(inst)
		return inst, true, err

	case introspection.TypeKindInputObject:
		var inst dagql.ObjectResult[*core.TypeDef]
		var input dagql.ObjectResult[*core.InputTypeDef]
		if err := dag.Select(ctx, dag.Root(), &input, dagql.Selector{
			Field: "__inputTypeDef",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(introspectionType.Name)}},
		}); err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, err
		}
		inputID, err := core.ResultIDInput(input)
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, err
		}
		inst, err = core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
			Field: "__withInputTypeDef",
			Args:  []dagql.NamedInput{{Name: "inputTypeDef", Value: inputID}},
		})
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, false, err
		}
		inst, err = maybeOptional(inst)
		return inst, true, err

	default:
		return dagql.ObjectResult[*core.TypeDef]{}, false, fmt.Errorf("unexpected type kind %s", introspectionType.Kind)
	}
}
