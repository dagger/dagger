package core

import (
	"encoding/json"
	"fmt"
)

// ModuleJSON represents the JSON structure for Module serialization
type ModuleJSON struct {
	Name          string     `json:"name"`
	OriginalName  string     `json:"originalName"`
	Description   string     `json:"description"`
	ObjectDefs    []*TypeDef `json:"objects"`
	InterfaceDefs []*TypeDef `json:"interfaces"`
	EnumDefs      []*TypeDef `json:"enums"`
}

// MarshalJSON implements custom JSON marshaling for Module
func (m Module) MarshalJSON() ([]byte, error) {
	moduleJSON := ModuleJSON{
		Name:          m.NameField,
		OriginalName:  m.OriginalName,
		Description:   m.Description,
		ObjectDefs:    m.ObjectDefs,
		InterfaceDefs: m.InterfaceDefs,
		EnumDefs:      m.EnumDefs,
	}

	return json.Marshal(moduleJSON)
}

// UnmarshalJSON implements custom JSON unmarshaling for Module
func (m *Module) UnmarshalJSON(data []byte) error {
	moduleJSON := ModuleJSON{
		ObjectDefs:    make([]*TypeDef, 0),
		InterfaceDefs: make([]*TypeDef, 0),
		EnumDefs:      make([]*TypeDef, 0),
	}
	if err := json.Unmarshal(data, &moduleJSON); err != nil {
		return err
	}

	m.NameField = moduleJSON.Name
	m.OriginalName = moduleJSON.OriginalName
	m.Description = moduleJSON.Description
	m.ObjectDefs = moduleJSON.ObjectDefs
	m.InterfaceDefs = moduleJSON.InterfaceDefs
	m.EnumDefs = moduleJSON.EnumDefs

	return nil
}

// ToJSONString generates a JSON string representation of the Module
func (m Module) ToJSONString() (string, error) {
	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Module to JSON: %w", err)
	}
	return string(jsonBytes), nil
}

// FromJSONString creates a Module from a JSON string
func ModuleFromJSONString(jsonStr string) (*Module, error) {
	var module Module
	if err := json.Unmarshal([]byte(jsonStr), &module); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to Module: %w", err)
	}
	return &module, nil
}
