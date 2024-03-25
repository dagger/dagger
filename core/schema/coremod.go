package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
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

func (m *CoreMod) Digest() digest.Digest {
	return digest.FromString(m.Name())
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
		objType, ok := m.Dag.ObjectType(typeDef.AsObject.Value.Name)
		if !ok || objType.SourceModule() != nil {
			// SourceModule is nil for core types
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
	typeDefs := make(map[string]*core.TypeDef)
	typeDefStubs := make(map[string]*core.TypeDef)

	var getStub func(*ast.Type, bool) *core.TypeDef
	getStub = func(t *ast.Type, isArg bool) *core.TypeDef {
		switch {
		case t.NamedType != "":
			stubName := t.String() // includes ! when non-null
			if isArg && strings.HasSuffix(t.NamedType, "ID") {
				// convert ID arguments to the actual object
				stubName = strings.TrimSuffix(t.NamedType, "ID")
				if t.NonNull {
					stubName += "!"
				}
			}

			stub, ok := typeDefStubs[stubName]
			if !ok {
				stub = &core.TypeDef{
					Optional: !t.NonNull,
				}
				typeDefStubs[stubName] = stub
			}
			return stub
		case t.Elem != nil:
			return &core.TypeDef{
				Kind:     core.TypeDefKindList,
				Optional: !t.NonNull,
				AsList: dagql.NonNull(&core.ListTypeDef{
					ElementTypeDef: getStub(t.Elem, isArg),
				}),
			}
		}
		return nil
	}

	objTypes := m.Dag.ObjectTypes()
	for _, objType := range objTypes {
		if objType.SourceModule() != nil {
			// not a core type
			continue
		}
		astDef := objType.TypeDefinition()
		objTypeDef := core.NewObjectTypeDef(astDef.Name, astDef.Description)

		// check that this is an idable object quick first so we don't recurse to stubs for e.g. introspection types
		isIdable := false
		for _, field := range astDef.Fields {
			if field.Name == "id" {
				isIdable = true
				break
			}
		}
		if !isIdable {
			continue
		}

		for _, field := range astDef.Fields {
			if field.Name == "id" {
				continue
			}
			fn := &core.Function{
				Name:        field.Name,
				Description: field.Description,
				ReturnType:  getStub(field.Type, false),
			}
			for _, arg := range field.Arguments {
				fnArg := &core.FunctionArg{
					Name:        arg.Name,
					Description: arg.Description,
					TypeDef:     getStub(arg.Type, true),
				}
				if arg.DefaultValue != nil {
					anyVal, err := arg.DefaultValue.Value(nil)
					if err != nil {
						return nil, fmt.Errorf("failed to get default value: %w", err)
					}
					bs, err := json.Marshal(anyVal)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal default value: %w", err)
					}
					fnArg.DefaultValue = core.JSON(bs)
				}
				fn.Args = append(fn.Args, fnArg)
			}
			objTypeDef.Functions = append(objTypeDef.Functions, fn)
		}
		typeDefs[objTypeDef.Name] = &core.TypeDef{
			Kind:     core.TypeDefKindObject,
			AsObject: dagql.NonNull(objTypeDef),
		}
	}

	inputTypes := m.Dag.TypeDefs()
	for _, inputType := range inputTypes {
		astDef := inputType.TypeDefinition()
		inputTypeDef := &core.InputTypeDef{
			Name:   astDef.Name,
			Fields: make([]*core.FieldTypeDef, 0, len(astDef.Fields)),
		}
		for _, field := range astDef.Fields {
			fieldDef := &core.FieldTypeDef{
				Name:        field.Name,
				Description: field.Description,
				TypeDef:     getStub(field.Type, false),
			}
			inputTypeDef.Fields = append(inputTypeDef.Fields, fieldDef)
		}
		typeDefs[inputTypeDef.Name] = &core.TypeDef{
			Kind:    core.TypeDefKindInput,
			AsInput: dagql.NonNull(inputTypeDef),
		}
	}

	scalarTypes := m.Dag.ScalarTypes()
	for _, scalarType := range scalarTypes {
		switch scalarType.(type) {
		case dagql.Int:
			typeDefs[scalarType.TypeName()] = &core.TypeDef{
				Kind: core.TypeDefKindInteger,
			}
		case dagql.Boolean:
			typeDefs[scalarType.TypeName()] = &core.TypeDef{
				Kind: core.TypeDefKindBoolean,
			}
		default:
			// default everything else to string for now
			typeDefs[scalarType.TypeName()] = &core.TypeDef{
				Kind: core.TypeDefKindString,
			}
		}
	}

	// fill in stubs with kinds
	for name, stub := range typeDefStubs {
		typeDef, ok := typeDefs[strings.TrimSuffix(name, "!")]
		if !ok {
			return nil, fmt.Errorf("failed to find type %q", name)
		}
		stub.Kind = typeDef.Kind
		stub.AsObject = typeDef.AsObject
		stub.AsInput = typeDef.AsInput
	}

	sortedTypeDefs := make([]*core.TypeDef, 0, len(typeDefs))
	for _, typeDef := range typeDefs {
		sortedTypeDefs = append(sortedTypeDefs, typeDef)
	}
	slices.SortFunc(sortedTypeDefs, func(a, b *core.TypeDef) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return sortedTypeDefs, nil
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
