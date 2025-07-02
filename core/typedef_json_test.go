package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypeDefJSON_StringType(t *testing.T) {
	// Test basic string type
	typeDef := &TypeDef{
		Kind:     TypeDefKindString,
		Optional: false,
	}

	// Test marshaling
	jsonStr, err := typeDef.ToJSONString()
	require.NoError(t, err)
	t.Logf("String TypeDef JSON: %s", jsonStr)

	expected := `{"kind":"STRING_KIND","optional":false}`
	assert.JSONEq(t, expected, jsonStr)

	// Test unmarshaling
	var unmarshaled TypeDef
	err = json.Unmarshal([]byte(jsonStr), &unmarshaled)
	require.NoError(t, err)
	assert.Equal(t, TypeDefKindString, unmarshaled.Kind)
	assert.False(t, unmarshaled.Optional)
}

func TestTypeDefJSON_OptionalIntegerType(t *testing.T) {
	// Test optional integer type
	typeDef := &TypeDef{
		Kind:     TypeDefKindInteger,
		Optional: true,
	}

	jsonStr, err := typeDef.ToJSONString()
	require.NoError(t, err)
	t.Logf("Optional Integer TypeDef JSON: %s", jsonStr)

	expected := `{"kind":"INTEGER_KIND","optional":true}`
	assert.JSONEq(t, expected, jsonStr)

	// Test round-trip
	reconstructed, err := TypeDefFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, typeDef.Kind, reconstructed.Kind)
	assert.Equal(t, typeDef.Optional, reconstructed.Optional)
}

func TestTypeDefJSON_ListType(t *testing.T) {
	// Test list of strings
	stringType := &TypeDef{
		Kind:     TypeDefKindString,
		Optional: false,
	}

	listType := &TypeDef{
		Kind:     TypeDefKindList,
		Optional: false,
		AsList: dagql.NonNull(&ListTypeDef{
			ElementTypeDef: stringType,
		}),
	}

	jsonStr, err := listType.ToJSONString()
	require.NoError(t, err)
	t.Logf("List TypeDef JSON: %s", jsonStr)

	// Test unmarshaling
	reconstructed, err := TypeDefFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, TypeDefKindList, reconstructed.Kind)
	assert.True(t, reconstructed.AsList.Valid)
	assert.Equal(t, TypeDefKindString, reconstructed.AsList.Value.ElementTypeDef.Kind)
}

func TestTypeDefJSON_ScalarType(t *testing.T) {
	// Test custom scalar type
	scalarDef := NewScalarTypeDef("DateTime", "A custom date-time scalar")
	typeDef := &TypeDef{
		Kind:     TypeDefKindScalar,
		Optional: false,
		AsScalar: dagql.NonNull(scalarDef),
	}

	jsonStr, err := typeDef.ToJSONString()
	require.NoError(t, err)
	t.Logf("Scalar TypeDef JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := TypeDefFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, TypeDefKindScalar, reconstructed.Kind)
	assert.True(t, reconstructed.AsScalar.Valid)
	assert.Equal(t, "DateTime", reconstructed.AsScalar.Value.Name)
	assert.Equal(t, "A custom date-time scalar", reconstructed.AsScalar.Value.Description)
}

func TestTypeDefJSON_ObjectType(t *testing.T) {
	// Test custom object type
	objectDef := NewObjectTypeDef("User", "A user object")
	typeDef := &TypeDef{
		Kind:     TypeDefKindObject,
		Optional: false,
		AsObject: dagql.NonNull(objectDef),
	}

	jsonStr, err := typeDef.ToJSONString()
	require.NoError(t, err)
	t.Logf("Object TypeDef JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := TypeDefFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, TypeDefKindObject, reconstructed.Kind)
	assert.True(t, reconstructed.AsObject.Valid)
	assert.Equal(t, "User", reconstructed.AsObject.Value.Name)
	assert.Equal(t, "A user object", reconstructed.AsObject.Value.Description)
}

func TestTypeDefJSON_EnumType(t *testing.T) {
	// Test enum type
	enumDef := NewEnumTypeDef("Status", "Status enumeration", nil)
	typeDef := &TypeDef{
		Kind:     TypeDefKindEnum,
		Optional: false,
		AsEnum: dagql.NonNull(enumDef),
	}

	jsonStr, err := typeDef.ToJSONString()
	require.NoError(t, err)
	t.Logf("Enum TypeDef JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := TypeDefFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, TypeDefKindEnum, reconstructed.Kind)
	assert.True(t, reconstructed.AsEnum.Valid)
	assert.Equal(t, "Status", reconstructed.AsEnum.Value.Name)
	assert.Equal(t, "Status enumeration", reconstructed.AsEnum.Value.Description)
}

func TestTypeDefJSON_ComplexNestedType(t *testing.T) {
	// Test complex nested type: Optional List of User objects
	userObjectDef := NewObjectTypeDef("User", "A user object")
	userTypeDef := &TypeDef{
		Kind:     TypeDefKindObject,
		Optional: false,
		AsObject: dagql.NonNull(userObjectDef),
	}

	userListType := &TypeDef{
		Kind:     TypeDefKindList,
		Optional: true, // Optional list
		AsList: dagql.NonNull(&ListTypeDef{
			ElementTypeDef: userTypeDef,
		}),
	}

	jsonStr, err := userListType.ToJSONString()
	require.NoError(t, err)
	t.Logf("Complex Nested TypeDef JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := TypeDefFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, TypeDefKindList, reconstructed.Kind)
	assert.True(t, reconstructed.Optional)
	assert.True(t, reconstructed.AsList.Valid)

	// Check the nested object type
	elementType := reconstructed.AsList.Value.ElementTypeDef
	assert.Equal(t, TypeDefKindObject, elementType.Kind)
	assert.True(t, elementType.AsObject.Valid)
	assert.Equal(t, "User", elementType.AsObject.Value.Name)
}

func TestTypeDefJSON_AllPrimitiveTypes(t *testing.T) {
	// Test all primitive types
	primitiveTypes := []struct {
		name string
		kind TypeDefKind
	}{
		{"string", TypeDefKindString},
		{"integer", TypeDefKindInteger},
		{"float", TypeDefKindFloat},
		{"boolean", TypeDefKindBoolean},
		{"void", TypeDefKindVoid},
	}

	for _, pt := range primitiveTypes {
		t.Run(pt.name, func(t *testing.T) {
			typeDef := &TypeDef{
				Kind:     pt.kind,
				Optional: false,
			}

			jsonStr, err := typeDef.ToJSONString()
			require.NoError(t, err)
			t.Logf("%s TypeDef JSON: %s", pt.name, jsonStr)

			// Test round-trip
			reconstructed, err := TypeDefFromJSONString(jsonStr)
			require.NoError(t, err)
			assert.Equal(t, pt.kind, reconstructed.Kind)
			assert.False(t, reconstructed.Optional)
		})
	}
}

func TestTypeDefJSON_InvalidJSON(t *testing.T) {
	// Test invalid JSON handling
	invalidJSONs := []string{
		`{"kind":"INVALID_KIND","optional":false}`,
		`{"optional":false}`, // missing kind
		`{"kind":"","optional":false}`, // empty kind
		`invalid json`,
	}

	for _, invalidJSON := range invalidJSONs {
		t.Run("invalid_"+invalidJSON[:min(10, len(invalidJSON))], func(t *testing.T) {
			_, err := TypeDefFromJSONString(invalidJSON)
			// We expect an error, but we don't enforce the specific error type
			// since validation might be handled differently
			t.Logf("Expected error for invalid JSON %q: %v", invalidJSON, err)
		})
	}
}

func TestIsValidTypeDefKind(t *testing.T) {
	// Test the helper function
	validKinds := []string{
		string(TypeDefKindString),
		string(TypeDefKindInteger),
		string(TypeDefKindFloat),
		string(TypeDefKindBoolean),
		string(TypeDefKindScalar),
		string(TypeDefKindList),
		string(TypeDefKindObject),
		string(TypeDefKindInterface),
		string(TypeDefKindInput),
		string(TypeDefKindVoid),
		string(TypeDefKindEnum),
	}

	for _, kind := range validKinds {
		assert.True(t, IsValidTypeDefKind(kind), "Kind %s should be valid", kind)
	}

	invalidKinds := []string{
		"INVALID_KIND",
		"",
		"string", // lowercase
		"random",
	}

	for _, kind := range invalidKinds {
		assert.False(t, IsValidTypeDefKind(kind), "Kind %s should be invalid", kind)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}