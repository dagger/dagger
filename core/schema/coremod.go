package schema

import (
	"context"
	"fmt"

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
