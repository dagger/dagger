package core

import (
	"fmt"
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
	TypeDefKindScalar: {
		Kind: TypeDefKindScalar,
		AsScalar: dagql.NonNull(&ScalarTypeDef{
			Name: "FooScalar",
		}),
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
	TypeDefKindEnum: {
		Kind: TypeDefKindEnum,
		AsEnum: dagql.NonNull(&EnumTypeDef{
			Name: "FooEnum",
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
			if val.Name == "INPUT_KIND" {
				// inputs are not needed for handling in conversion
				// and not implemented, so skip them
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
