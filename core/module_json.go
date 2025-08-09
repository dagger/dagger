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
func (mod Module) MarshalJSON() ([]byte, error) {
	moduleJSON := ModuleJSON{
		Name:          mod.NameField,
		OriginalName:  mod.OriginalName,
		Description:   mod.Description,
		ObjectDefs:    mod.ObjectDefs,
		InterfaceDefs: mod.InterfaceDefs,
		EnumDefs:      mod.EnumDefs,
	}

	return json.Marshal(moduleJSON)
}

// UnmarshalJSON implements custom JSON unmarshaling for Module
func (mod *Module) UnmarshalJSON(data []byte) error {
	moduleJSON := ModuleJSON{
		ObjectDefs:    make([]*TypeDef, 0),
		InterfaceDefs: make([]*TypeDef, 0),
		EnumDefs:      make([]*TypeDef, 0),
	}
	if err := json.Unmarshal(data, &moduleJSON); err != nil {
		return err
	}

	mod.NameField = moduleJSON.Name
	mod.OriginalName = moduleJSON.OriginalName
	mod.Description = moduleJSON.Description
	mod.ObjectDefs = moduleJSON.ObjectDefs
	mod.InterfaceDefs = moduleJSON.InterfaceDefs
	mod.EnumDefs = moduleJSON.EnumDefs

	return nil
}

// ToJSONString generates a JSON string representation of the Module
func (mod Module) ToJSONString() (string, error) {
	jsonBytes, err := json.Marshal(mod)
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
