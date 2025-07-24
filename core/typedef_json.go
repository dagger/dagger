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
func (typeDef TypeDef) MarshalJSON() ([]byte, error) {
	typeDefJSON := TypeDefJSON{
		Kind:     string(typeDef.Kind),
		Optional: typeDef.Optional,
	}

	// Set the values field based on the Kind
	switch typeDef.Kind {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindFloat, TypeDefKindBoolean, TypeDefKindVoid:
		// Primitive types don't need values
		typeDefJSON.Values = nil
	case TypeDefKindList:
		if typeDef.AsList.Valid {
			typeDefJSON.Values = typeDef.AsList.Value
		}
	case TypeDefKindObject:
		if typeDef.AsObject.Valid {
			typeDefJSON.Values = typeDef.AsObject.Value
		}
	case TypeDefKindInterface:
		if typeDef.AsInterface.Valid {
			typeDefJSON.Values = typeDef.AsInterface.Value
		}
	case TypeDefKindInput:
		if typeDef.AsInput.Valid {
			typeDefJSON.Values = typeDef.AsInput.Value
		}
	case TypeDefKindScalar:
		if typeDef.AsScalar.Valid {
			typeDefJSON.Values = typeDef.AsScalar.Value
		}
	case TypeDefKindEnum:
		if typeDef.AsEnum.Valid {
			typeDefJSON.Values = typeDef.AsEnum.Value
		}
	}

	return json.Marshal(typeDefJSON)
}

// UnmarshalJSON implements custom JSON unmarshaling for TypeDef
func (typeDef *TypeDef) UnmarshalJSON(data []byte) error {
	var typeDefJSON TypeDefJSON
	if err := json.Unmarshal(data, &typeDefJSON); err != nil {
		return err
	}

	typeDef.Kind = TypeDefKind(typeDefJSON.Kind)
	typeDef.Optional = typeDefJSON.Optional

	// Initialize all fields as null/invalid
	typeDef.AsList = dagql.Null[*ListTypeDef]()
	typeDef.AsObject = dagql.Null[*ObjectTypeDef]()
	typeDef.AsInterface = dagql.Null[*InterfaceTypeDef]()
	typeDef.AsInput = dagql.Null[*InputTypeDef]()
	typeDef.AsScalar = dagql.Null[*ScalarTypeDef]()
	typeDef.AsEnum = dagql.Null[*EnumTypeDef]()

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
	switch typeDef.Kind {
	case TypeDefKindList:
		var listTypeDef ListTypeDef
		if err := json.Unmarshal(valuesJSON, &listTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal list values: %w", err)
		}
		typeDef.AsList = dagql.NonNull(&listTypeDef)
	case TypeDefKindObject:
		objectTypeDef := ObjectTypeDef{
			Fields:    make([]*FieldTypeDef, 0),
			Functions: make([]*Function, 0),
		}
		if err := json.Unmarshal(valuesJSON, &objectTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal object values: %w", err)
		}
		// trick to have constructor working
		var withConstructor struct {
			Constructor *Function
		}
		if err := json.Unmarshal(valuesJSON, &withConstructor); err != nil {
			return fmt.Errorf("failed to unmarshal constructor values: %w", err)
		}
		if withConstructor.Constructor != nil {
			objectTypeDef.Constructor = dagql.NonNull(withConstructor.Constructor)
		}
		typeDef.AsObject = dagql.NonNull(&objectTypeDef)
	case TypeDefKindInterface:
		interfaceTypeDef := InterfaceTypeDef{
			Functions: make([]*Function, 0),
		}
		if err := json.Unmarshal(valuesJSON, &interfaceTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal interface values: %w", err)
		}
		typeDef.AsInterface = dagql.NonNull(&interfaceTypeDef)
	case TypeDefKindInput:
		inputTypeDef := InputTypeDef{
			Fields: make([]*FieldTypeDef, 0),
		}
		if err := json.Unmarshal(valuesJSON, &inputTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal input values: %w", err)
		}
		typeDef.AsInput = dagql.NonNull(&inputTypeDef)
	case TypeDefKindScalar:
		var scalarTypeDef ScalarTypeDef
		if err := json.Unmarshal(valuesJSON, &scalarTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal scalar values: %w", err)
		}
		typeDef.AsScalar = dagql.NonNull(&scalarTypeDef)
	case TypeDefKindEnum:
		enumTypeDef := EnumTypeDef{
			Members: make([]*EnumMemberTypeDef, 0),
		}
		if err := json.Unmarshal(valuesJSON, &enumTypeDef); err != nil {
			return fmt.Errorf("failed to unmarshal enum values: %w", err)
		}
		typeDef.AsEnum = dagql.NonNull(&enumTypeDef)
	default:
		return fmt.Errorf("unknown TypeDefKind: %s", typeDef.Kind)
	}

	return nil
}

// ToJSONString generates a JSON string representation of the TypeDef
func (typeDef TypeDef) ToJSONString() (string, error) {
	jsonBytes, err := json.Marshal(typeDef)
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
