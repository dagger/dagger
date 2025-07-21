package core

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// TypeDefJSON represents the JSON structure for TypeDef serialization
type TypeDefJSON struct {
	Kind     string      `json:"kind"`
	Optional bool        `json:"optional"`
	Values   interface{} `json:"values,omitempty"`
}

// MarshalJSON implements custom JSON marshaling for TypeDef
func (t TypeDef) MarshalJSON() ([]byte, error) {
	typeDefJSON := TypeDefJSON{
		Kind:     string(t.Kind),
		Optional: t.Optional,
	}

	// Set the values field based on the Kind
	switch t.Kind {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindFloat, TypeDefKindBoolean, TypeDefKindVoid:
		// Primitive types don't need values
		typeDefJSON.Values = nil
	case TypeDefKindList:
		if t.AsList.Valid {
			typeDefJSON.Values = t.AsList.Value
		}
	case TypeDefKindObject:
		if t.AsObject.Valid {
			typeDefJSON.Values = t.AsObject.Value
		}
	case TypeDefKindInterface:
		if t.AsInterface.Valid {
			typeDefJSON.Values = t.AsInterface.Value
		}
	case TypeDefKindInput:
		if t.AsInput.Valid {
			typeDefJSON.Values = t.AsInput.Value
		}
	case TypeDefKindScalar:
		if t.AsScalar.Valid {
			typeDefJSON.Values = t.AsScalar.Value
		}
	case TypeDefKindEnum:
		if t.AsEnum.Valid {
			typeDefJSON.Values = t.AsEnum.Value
		}
	}

	return json.Marshal(typeDefJSON)
}

// UnmarshalJSON implements custom JSON unmarshaling for TypeDef
func (t *TypeDef) UnmarshalJSON(data []byte) error {
	var typeDefJSON TypeDefJSON
	if err := json.Unmarshal(data, &typeDefJSON); err != nil {
		return err
	}

	t.Kind = TypeDefKind(typeDefJSON.Kind)
	t.Optional = typeDefJSON.Optional

	// Initialize all fields as null/invalid
	t.AsList = dagql.Null[*ListTypeDef]()
	t.AsObject = dagql.Null[*ObjectTypeDef]()
	t.AsInterface = dagql.Null[*InterfaceTypeDef]()
	t.AsInput = dagql.Null[*InputTypeDef]()
	t.AsScalar = dagql.Null[*ScalarTypeDef]()
	t.AsEnum = dagql.Null[*EnumTypeDef]()

	// If there are no values, we're done (for primitive types)
	if typeDefJSON.Values == nil {
		return nil
	}

	// Marshal values back to JSON to unmarshal into the specific type
	valuesJSON, err := json.Marshal(typeDefJSON.Values)
	if err != nil {
		return fmt.Errorf("failed to marshal values: %w", err)
	}

	// Set the appropriate field based on the Kind
	switch t.Kind {
	case TypeDefKindList:
		var listTypeDef ListTypeDef
		if err := json.Unmarshal(valuesJSON, &listTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal list values: %w", err)
		}
		t.AsList = dagql.NonNull(&listTypeDef)
	case TypeDefKindObject:
		objectTypeDef := ObjectTypeDef{
			Fields:    make([]*FieldTypeDef, 0),
			Functions: make([]*Function, 0),
		}
		if err := json.Unmarshal(valuesJSON, &objectTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal object values: %w", err)
		}
		t.AsObject = dagql.NonNull(&objectTypeDef)
	case TypeDefKindInterface:
		interfaceTypeDef := InterfaceTypeDef{
			Functions: make([]*Function, 0),
		}
		if err := json.Unmarshal(valuesJSON, &interfaceTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal interface values: %w", err)
		}
		t.AsInterface = dagql.NonNull(&interfaceTypeDef)
	case TypeDefKindInput:
		inputTypeDef := InputTypeDef{
			Fields: make([]*FieldTypeDef, 0),
		}
		if err := json.Unmarshal(valuesJSON, &inputTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal input values: %w", err)
		}
		t.AsInput = dagql.NonNull(&inputTypeDef)
	case TypeDefKindScalar:
		var scalarTypeDef ScalarTypeDef
		if err := json.Unmarshal(valuesJSON, &scalarTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal scalar values: %w", err)
		}
		t.AsScalar = dagql.NonNull(&scalarTypeDef)
	case TypeDefKindEnum:
		enumTypeDef := EnumTypeDef{
			Members: make([]*EnumMemberTypeDef, 0),
		}
		if err := json.Unmarshal(valuesJSON, &enumTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal enum values: %w", err)
		}
		t.AsEnum = dagql.NonNull(&enumTypeDef)
	default:
		return fmt.Errorf("unknown TypeDefKind: %s", t.Kind)
	}

	return nil
}

// ToJSONString generates a JSON string representation of the TypeDef
func (t TypeDef) ToJSONString() (string, error) {
	jsonBytes, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("failed to marshal TypeDef to JSON: %w", err)
	}
	return string(jsonBytes), nil
}

// FromJSONString creates a TypeDef from a JSON string
func TypeDefFromJSONString(jsonStr string) (*TypeDef, error) {
	var typeDef TypeDef
	if err := json.Unmarshal([]byte(jsonStr), &typeDef); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to TypeDef: %w", err)
	}
	return &typeDef, nil
}

// Helper function to check if a TypeDefKind is valid
func IsValidTypeDefKind(kind string) bool {
	switch TypeDefKind(kind) {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindFloat, TypeDefKindBoolean,
		TypeDefKindScalar, TypeDefKindList, TypeDefKindObject, TypeDefKindInterface,
		TypeDefKindInput, TypeDefKindVoid, TypeDefKindEnum:
		return true
	default:
		return false
	}
}
