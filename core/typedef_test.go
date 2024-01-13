package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
)

// Samples contains a valid type definition for each kind. If you add a new
// TypeDefKind, add a sample here as a first step to getting the tests to pass.
var Samples = map[TypeDefKind]*TypeDef{
	TypeDefKindString: {
		Kind: TypeDefKindString,
	},
	TypeDefKindInteger: {
		Kind: TypeDefKindInteger,
	},
	TypeDefKindBoolean: {
		Kind: TypeDefKindBoolean,
	},
	TypeDefKindList: {
		Kind: TypeDefKindList,
		AsList: dagql.NonNull(&ListTypeDef{
			ElementTypeDef: &TypeDef{
				Kind: TypeDefKindString,
			},
		}),
	},
	TypeDefKindObject: {
		Kind: TypeDefKindObject,
		AsObject: dagql.NonNull(&ObjectTypeDef{
			Name: "FooObject",
		}),
	},
	TypeDefKindInterface: {
		Kind: TypeDefKindInterface,
		AsInterface: dagql.NonNull(&InterfaceTypeDef{
			Name: "FooInterface",
		}),
	},
	TypeDefKindVoid: {
		Kind: TypeDefKindVoid,
	},
}

func TestTypeDefConversions(t *testing.T) {
	for _, val := range TypeDefKinds.PossibleValues() {
		val := val
		sample, ok := Samples[TypeDefKind(val.Name)]
		if !ok {
			t.Fatalf("missing TypeDefKind sample for %s", val.Name)
		}
		t.Run("%s.ToInput", func(t *testing.T) {
			sample.ToInput()
		})
		t.Run("%s.ToTyped", func(t *testing.T) {
			sample.ToTyped()
		})
		t.Run("%s.ToType", func(t *testing.T) {
			sample.ToType()
		})
	}
}
