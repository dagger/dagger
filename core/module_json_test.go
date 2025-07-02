package core

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleJSON_BasicModule(t *testing.T) {
	// Test basic module with just name and description
	module := &Module{
		NameField:    "TestModule",
		OriginalName: "TestModule",
		Description:  "A test module for JSON serialization",
		ObjectDefs:   []*TypeDef{},
		InterfaceDefs: []*TypeDef{},
		EnumDefs:     []*TypeDef{},
	}

	// Test marshaling
	jsonStr, err := module.ToJSONString()
	require.NoError(t, err)
	t.Logf("Basic Module JSON: %s", jsonStr)

	expected := `{
		"name": "TestModule",
		"originalName": "TestModule",
		"description": "A test module for JSON serialization",
		"objects": [],
		"interfaces": [],
		"enums": []
	}`
	assert.JSONEq(t, expected, jsonStr)

	// Test unmarshaling
	reconstructed, err := ModuleFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, module.NameField, reconstructed.NameField)
	assert.Equal(t, module.OriginalName, reconstructed.OriginalName)
	assert.Equal(t, module.Description, reconstructed.Description)
	assert.Empty(t, reconstructed.ObjectDefs)
	assert.Empty(t, reconstructed.InterfaceDefs)
	assert.Empty(t, reconstructed.EnumDefs)
}

func TestModuleJSON_ModuleWithObjectDefs(t *testing.T) {
	// Create a simple object type def
	stringType := &TypeDef{
		Kind:     TypeDefKindString,
		Optional: false,
	}

	userObjectDef := NewObjectTypeDef("User", "A user object")
	userObjectDef.Fields = []*FieldTypeDef{
		{
			Name:        "name",
			OriginalName: "name",
			Description: "User's name",
			TypeDef:     stringType,
		},
	}

	userTypeDef := &TypeDef{
		Kind:     TypeDefKindObject,
		Optional: false,
		AsObject: dagql.NonNull(userObjectDef),
	}

	module := &Module{
		NameField:     "UserModule",
		OriginalName:  "UserModule",
		Description:   "A module with user objects",
		ObjectDefs:    []*TypeDef{userTypeDef},
		InterfaceDefs: []*TypeDef{},
		EnumDefs:      []*TypeDef{},
	}

	// Test marshaling
	jsonStr, err := module.ToJSONString()
	require.NoError(t, err)
	t.Logf("Module with Objects JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := ModuleFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, module.NameField, reconstructed.NameField)
	assert.Equal(t, module.Description, reconstructed.Description)
	assert.Len(t, reconstructed.ObjectDefs, 1)

	// Verify the object type def was preserved
	reconstructedObj := reconstructed.ObjectDefs[0]
	assert.Equal(t, TypeDefKindObject, reconstructedObj.Kind)
	assert.True(t, reconstructedObj.AsObject.Valid)
	assert.Equal(t, "User", reconstructedObj.AsObject.Value.Name)
	assert.Equal(t, "A user object", reconstructedObj.AsObject.Value.Description)
	assert.Len(t, reconstructedObj.AsObject.Value.Fields, 1)
	assert.Equal(t, "name", reconstructedObj.AsObject.Value.Fields[0].Name)
}

func TestModuleJSON_ModuleWithEnumDefs(t *testing.T) {
	// Create an enum type def
	statusEnum := NewEnumTypeDef("Status", "Status enumeration", nil)
	statusEnum.Members = []*EnumMemberTypeDef{
		NewEnumMemberTypeDef("ACTIVE", "ACTIVE", "Active status", nil),
		NewEnumMemberTypeDef("INACTIVE", "INACTIVE", "Inactive status", nil),
	}

	enumTypeDef := &TypeDef{
		Kind:     TypeDefKindEnum,
		Optional: false,
		AsEnum:   dagql.NonNull(statusEnum),
	}

	module := &Module{
		NameField:     "StatusModule",
		OriginalName:  "StatusModule",
		Description:   "A module with status enums",
		ObjectDefs:    []*TypeDef{},
		InterfaceDefs: []*TypeDef{},
		EnumDefs:      []*TypeDef{enumTypeDef},
	}

	// Test marshaling
	jsonStr, err := module.ToJSONString()
	require.NoError(t, err)
	t.Logf("Module with Enums JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := ModuleFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, module.NameField, reconstructed.NameField)
	assert.Equal(t, module.Description, reconstructed.Description)
	assert.Len(t, reconstructed.EnumDefs, 1)

	// Verify the enum type def was preserved
	reconstructedEnum := reconstructed.EnumDefs[0]
	assert.Equal(t, TypeDefKindEnum, reconstructedEnum.Kind)
	assert.True(t, reconstructedEnum.AsEnum.Valid)
	assert.Equal(t, "Status", reconstructedEnum.AsEnum.Value.Name)
	assert.Equal(t, "Status enumeration", reconstructedEnum.AsEnum.Value.Description)
	assert.Len(t, reconstructedEnum.AsEnum.Value.Members, 2)
	assert.Equal(t, "ACTIVE", reconstructedEnum.AsEnum.Value.Members[0].Name)
	assert.Equal(t, "INACTIVE", reconstructedEnum.AsEnum.Value.Members[1].Name)
}

func TestModuleJSON_ModuleWithInterfaceDefs(t *testing.T) {
	// Create an interface type def
	stringType := &TypeDef{
		Kind:     TypeDefKindString,
		Optional: false,
	}

	getNameFunc := &Function{
		Name:         "getName",
		OriginalName: "getName",
		Description:  "Get the name",
		ReturnType:   stringType,
		Args:         []*FunctionArg{},
	}

	namedInterface := NewInterfaceTypeDef("Named", "Named interface")
	namedInterface.Functions = []*Function{getNameFunc}

	interfaceTypeDef := &TypeDef{
		Kind:        TypeDefKindInterface,
		Optional:    false,
		AsInterface: dagql.NonNull(namedInterface),
	}

	module := &Module{
		NameField:     "InterfaceModule",
		OriginalName:  "InterfaceModule",
		Description:   "A module with interfaces",
		ObjectDefs:    []*TypeDef{},
		InterfaceDefs: []*TypeDef{interfaceTypeDef},
		EnumDefs:      []*TypeDef{},
	}

	// Test marshaling
	jsonStr, err := module.ToJSONString()
	require.NoError(t, err)
	t.Logf("Module with Interfaces JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := ModuleFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, module.NameField, reconstructed.NameField)
	assert.Equal(t, module.Description, reconstructed.Description)
	assert.Len(t, reconstructed.InterfaceDefs, 1)

	// Verify the interface type def was preserved
	reconstructedInterface := reconstructed.InterfaceDefs[0]
	assert.Equal(t, TypeDefKindInterface, reconstructedInterface.Kind)
	assert.True(t, reconstructedInterface.AsInterface.Valid)
	assert.Equal(t, "Named", reconstructedInterface.AsInterface.Value.Name)
	assert.Equal(t, "Named interface", reconstructedInterface.AsInterface.Value.Description)
	assert.Len(t, reconstructedInterface.AsInterface.Value.Functions, 1)
	assert.Equal(t, "getName", reconstructedInterface.AsInterface.Value.Functions[0].Name)
}

func TestModuleJSON_ComplexModule(t *testing.T) {
	// Create a complex module with all type definitions
	stringType := &TypeDef{
		Kind:     TypeDefKindString,
		Optional: false,
	}

	// Create an enum
	statusEnum := NewEnumTypeDef("Status", "Status enumeration", nil)
	statusEnum.Members = []*EnumMemberTypeDef{
		NewEnumMemberTypeDef("ACTIVE", "ACTIVE", "Active status", nil),
	}
	enumTypeDef := &TypeDef{
		Kind:     TypeDefKindEnum,
		Optional: false,
		AsEnum:   dagql.NonNull(statusEnum),
	}

	// Create an interface
	getNameFunc := &Function{
		Name:         "getName",
		OriginalName: "getName",
		Description:  "Get the name",
		ReturnType:   stringType,
		Args:         []*FunctionArg{},
	}
	namedInterface := NewInterfaceTypeDef("Named", "Named interface")
	namedInterface.Functions = []*Function{getNameFunc}

	interfaceTypeDef := &TypeDef{
		Kind:        TypeDefKindInterface,
		Optional:    false,
		AsInterface: dagql.NonNull(namedInterface),
	}

	// Create an object
	userObjectDef := NewObjectTypeDef("User", "A user object")
	userObjectDef.Fields = []*FieldTypeDef{
		{
			Name:        "name",
			OriginalName: "name",
			Description: "User's name",
			TypeDef:     stringType,
		},
		{
			Name:        "status",
			OriginalName: "status",
			Description: "User's status",
			TypeDef:     enumTypeDef,
		},
	}
	userTypeDef := &TypeDef{
		Kind:     TypeDefKindObject,
		Optional: false,
		AsObject: dagql.NonNull(userObjectDef),
	}

	module := &Module{
		NameField:     "ComplexModule",
		OriginalName:  "ComplexModule",
		Description:   "A complex module with all type definitions",
		ObjectDefs:    []*TypeDef{userTypeDef},
		InterfaceDefs: []*TypeDef{interfaceTypeDef},
		EnumDefs:      []*TypeDef{enumTypeDef},
	}

	// Test marshaling
	jsonStr, err := module.ToJSONString()
	require.NoError(t, err)
	t.Logf("Complex Module JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := ModuleFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, module.NameField, reconstructed.NameField)
	assert.Equal(t, module.OriginalName, reconstructed.OriginalName)
	assert.Equal(t, module.Description, reconstructed.Description)
	assert.Len(t, reconstructed.ObjectDefs, 1)
	assert.Len(t, reconstructed.InterfaceDefs, 1)
	assert.Len(t, reconstructed.EnumDefs, 1)

	// Verify all types are preserved correctly
	assert.Equal(t, "User", reconstructed.ObjectDefs[0].AsObject.Value.Name)
	assert.Equal(t, "Named", reconstructed.InterfaceDefs[0].AsInterface.Value.Name)
	assert.Equal(t, "Status", reconstructed.EnumDefs[0].AsEnum.Value.Name)
}

func TestModuleJSON_EmptyModule(t *testing.T) {
	// Test completely empty module
	module := &Module{}

	jsonStr, err := module.ToJSONString()
	require.NoError(t, err)
	t.Logf("Empty Module JSON: %s", jsonStr)

	expected := `{
		"name": "",
		"originalName": "",
		"description": "",
		"objects": null,
		"interfaces": null,
		"enums": null
	}`
	assert.JSONEq(t, expected, jsonStr)

	// Test round-trip
	reconstructed, err := ModuleFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, "", reconstructed.NameField)
	assert.Equal(t, "", reconstructed.OriginalName)
	assert.Equal(t, "", reconstructed.Description)
}

func TestModuleJSON_InvalidJSON(t *testing.T) {
	// Test invalid JSON handling
	invalidJSONs := []string{
		`{"name":}`, // malformed JSON
		`invalid json`,
		`{"name": "test", "objects": "not an array"}`, // wrong type for objects
		`{}`, // missing required fields (should still work but with empty values)
	}

	for i, invalidJSON := range invalidJSONs {
		t.Run(fmt.Sprintf("invalid_%d", i), func(t *testing.T) {
			_, err := ModuleFromJSONString(invalidJSON)
			if i < 3 { // First 3 should definitely error
				assert.Error(t, err)
				t.Logf("Expected error for invalid JSON %q: %v", invalidJSON, err)
			} else {
				// Empty object should work
				assert.NoError(t, err)
			}
		})
	}
}

func TestModuleJSON_NilArrays(t *testing.T) {
	// Test module with nil arrays
	module := &Module{
		NameField:     "NilArrayModule",
		OriginalName:  "NilArrayModule",
		Description:   "A module with nil arrays",
		ObjectDefs:    nil,
		InterfaceDefs: nil,
		EnumDefs:      nil,
	}

	jsonStr, err := module.ToJSONString()
	require.NoError(t, err)
	t.Logf("Module with nil arrays JSON: %s", jsonStr)

	// Test round-trip
	reconstructed, err := ModuleFromJSONString(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, module.NameField, reconstructed.NameField)
	assert.Equal(t, module.OriginalName, reconstructed.OriginalName)
	assert.Equal(t, module.Description, reconstructed.Description)
	// nil arrays should be preserved or become empty
	assert.Nil(t, reconstructed.ObjectDefs)
	assert.Nil(t, reconstructed.InterfaceDefs)
	assert.Nil(t, reconstructed.EnumDefs)
}
