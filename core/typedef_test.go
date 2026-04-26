package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
)

func sampleTypeDefs(t *testing.T) map[TypeDefKind]*TypeDef {
	t.Helper()

	dag := newTypeDefTestDag(t)
	stringType := &TypeDef{Kind: TypeDefKindString}
	stringTypeRes := newTypeDefDetachedResult(t, dag, "sampleStringTypeDef", stringType)
	scalarRes := newTypeDefDetachedResult(t, dag, "sampleScalarTypeDef", &ScalarTypeDef{Name: "FooScalar"})
	listRes := newTypeDefDetachedResult(t, dag, "sampleListTypeDef", &ListTypeDef{
		ElementTypeDef: stringTypeRes,
	})
	objectRes := newTypeDefDetachedResult(t, dag, "sampleObjectTypeDef", &ObjectTypeDef{Name: "FooObject"})
	interfaceRes := newTypeDefDetachedResult(t, dag, "sampleInterfaceTypeDef", &InterfaceTypeDef{Name: "FooInterface"})
	enumRes := newTypeDefDetachedResult(t, dag, "sampleEnumTypeDef", &EnumTypeDef{Name: "FooEnum"})

	return map[TypeDefKind]*TypeDef{
		TypeDefKindString: {
			Kind: TypeDefKindString,
		},
		TypeDefKindFloat: {
			Kind: TypeDefKindFloat,
		},
		TypeDefKindInteger: {
			Kind: TypeDefKindInteger,
		},
		TypeDefKindBoolean: {
			Kind: TypeDefKindBoolean,
		},
		TypeDefKindScalar: {
			Kind:     TypeDefKindScalar,
			AsScalar: dagql.NonNull(scalarRes),
		},
		TypeDefKindList: {
			Kind:   TypeDefKindList,
			AsList: dagql.NonNull(listRes),
		},
		TypeDefKindObject: {
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(objectRes),
		},
		TypeDefKindInterface: {
			Kind:        TypeDefKindInterface,
			AsInterface: dagql.NonNull(interfaceRes),
		},
		TypeDefKindEnum: {
			Kind:   TypeDefKindEnum,
			AsEnum: dagql.NonNull(enumRes),
		},
		TypeDefKindVoid: {
			Kind: TypeDefKindVoid,
		},
	}
}

func TestTypeDefConversions(t *testing.T) {
	samples := sampleTypeDefs(t)
	for _, val := range TypeDefKinds.PossibleValues("") {
		sample, ok := samples[TypeDefKind(val.Name)]
		if !ok {
			if val.Name == TypeDefKindInput.String() {
				// inputs are not needed for handling in conversion
				// and not implemented, so skip them
				continue
			}
			if !strings.HasSuffix(val.Name, "_KIND") {
				// these values are duplicates
				continue
			}
			t.Fatalf("missing TypeDefKind sample for %s", val.Name)
		}
		t.Run(fmt.Sprintf("%s.ToInput", val.Name), func(t *testing.T) {
			sample.ToInput()
		})
		t.Run(fmt.Sprintf("%s.ToTyped", val.Name), func(t *testing.T) {
			sample.ToTyped()
		})
		t.Run(fmt.Sprintf("%s.ToType", val.Name), func(t *testing.T) {
			sample.ToType()
		})
	}
}
