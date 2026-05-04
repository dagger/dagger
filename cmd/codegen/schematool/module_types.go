// Package schematool manipulates Dagger introspection JSON.
// It is consumed both as a Go library (by the Go SDK's codegen,
// which runs in-process) and as a CLI by other SDKs.
//
// See hack/designs/no-codegen-at-runtime-moduletypes.md for the
// design rationale.
package schematool

import (
	"encoding/json"
	"fmt"
	"io"
)

// ModuleTypes is the language-agnostic input shape that a SDK's
// Phase-1 source analyzer produces for Merge. It is a minimal
// subset of core.Module: the module's name plus the types it
// declares.
type ModuleTypes struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Objects     []ObjectDef    `json:"objects,omitempty"`
	Interfaces  []InterfaceDef `json:"interfaces,omitempty"`
	Enums       []EnumDef      `json:"enums,omitempty"`
}

// ObjectDef mirrors core.ObjectTypeDef to the extent the SDK needs
// to expose via introspection.
type ObjectDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Constructor *Function  `json:"constructor,omitempty"`
	Functions   []Function `json:"functions,omitempty"`
	Fields      []FieldDef `json:"fields,omitempty"`
}

type InterfaceDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Functions   []Function `json:"functions,omitempty"`
}

type EnumDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Values      []EnumValue `json:"values"`
}

type EnumValue struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Value       string `json:"value,omitempty"`
}

type FieldDef struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	TypeRef     *TypeRef `json:"type"`
}

type Function struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Args        []FuncArg `json:"args,omitempty"`
	ReturnType  *TypeRef  `json:"returnType"`
}

type FuncArg struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	TypeRef      *TypeRef `json:"type"`
	DefaultValue *string  `json:"defaultValue,omitempty"`
}

// TypeRef is a reference to a type by name, optionally nested
// (list / non-null). Mirrors introspection.TypeRef but carries
// only the fields SDK-produced JSON needs to set.
type TypeRef struct {
	Kind   string   `json:"kind"` // OBJECT, INTERFACE, ENUM, SCALAR, LIST, NON_NULL
	Name   string   `json:"name,omitempty"`
	OfType *TypeRef `json:"ofType,omitempty"`
}

// DecodeModuleTypes reads a ModuleTypes JSON value from r.
func DecodeModuleTypes(r io.Reader) (*ModuleTypes, error) {
	var mt ModuleTypes
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&mt); err != nil {
		return nil, fmt.Errorf("decode module types: %w", err)
	}
	return &mt, nil
}
