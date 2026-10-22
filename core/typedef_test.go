package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
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

// A module function's Platform argument decodes normalized, as the core
// API's does: spellings of one platform give one literal, and so one key,
// and the value stays the String scalar module arguments travel as.
func TestPlatformArgInputNormalizes(t *testing.T) {
	dag := newTypeDefTestDag(t)
	platformRes := newTypeDefDetachedResult(t, dag, "platformScalarTypeDef", &ScalarTypeDef{Name: "Platform"})
	platformType := &TypeDef{Kind: TypeDefKindScalar, AsScalar: dagql.NonNull(platformRes)}
	optionalType := &TypeDef{Kind: TypeDefKindScalar, AsScalar: dagql.NonNull(platformRes), Optional: true}
	listType := &TypeDef{Kind: TypeDefKindList, AsList: dagql.NonNull(newTypeDefDetachedResult(t, dag, "platformListTypeDef", &ListTypeDef{
		ElementTypeDef: newTypeDefDetachedResult(t, dag, "platformElemTypeDef", platformType),
	}))}

	literal := func(def *TypeDef, val any) string {
		t.Helper()
		input, err := def.ToInput().Decoder().DecodeInput(val)
		require.NoError(t, err)
		return input.ToLiteral().Display()
	}

	input, err := platformType.ToInput().Decoder().DecodeInput("linux/arm64/v8")
	require.NoError(t, err)
	require.Equal(t, dagql.NewScalar[dagql.String]("Platform", "linux/arm64"), input)

	for _, def := range []*TypeDef{platformType, optionalType} {
		require.Equal(t, literal(def, "linux/arm64"), literal(def, "linux/arm64/v8"))
		require.Equal(t, literal(def, "linux/arm/v7"), literal(def, "linux/arm"))
		require.NotEqual(t, literal(def, "linux/arm/v6"), literal(def, "linux/arm/v7"))
	}
	require.Equal(t,
		literal(listType, []any{"linux/arm64", "linux/amd64"}),
		literal(listType, []any{"linux/aarch64/v8", "linux/x86_64"}))

	_, err = platformType.ToInput().Decoder().DecodeInput("windows98")
	require.ErrorContains(t, err, "unknown operating system or architecture")
}

func TestFunctionCallReturnValueStoresDecodedJSON(t *testing.T) {
	fnCall := newFunctionCall(FunctionCall{Name: "fn"})

	err := fnCall.ReturnValue(context.Background(), JSON(`{"n":123,"s":"ok"}`))
	require.NoError(t, err)

	res, ok, err := fnCall.returnResult()
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, res.HasError)

	obj, ok := res.Value.(map[string]any)
	require.True(t, ok)
	require.Equal(t, json.Number("123"), obj["n"])
	require.Equal(t, "ok", obj["s"])
}

func TestFunctionCallReturnValueAllowsNull(t *testing.T) {
	fnCall := newFunctionCall(FunctionCall{Name: "fn"})

	err := fnCall.ReturnValue(context.Background(), JSON(`null`))
	require.NoError(t, err)

	res, ok, err := fnCall.returnResult()
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, res.HasError)
	require.Nil(t, res.Value)
}

func TestFunctionCallReturnIsExactlyOnce(t *testing.T) {
	fnCall := newFunctionCall(FunctionCall{Name: "fn"})

	require.NoError(t, fnCall.ReturnValue(context.Background(), JSON(`"first"`)))
	require.ErrorContains(t, fnCall.ReturnValue(context.Background(), JSON(`"second"`)), "already set")
}

func TestFunctionCallReturnRequiresActiveCall(t *testing.T) {
	fnCall := &FunctionCall{Name: "decoded"}

	require.ErrorContains(t, fnCall.ReturnValue(context.Background(), JSON(`"x"`)), "not active")
}
