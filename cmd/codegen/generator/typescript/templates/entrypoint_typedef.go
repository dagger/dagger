package templates

import (
	"encoding/json"
)

// TypedefModule is the JSON shape produced by the TypeScript SDK
// introspector (`sdk/typescript/src/module/introspector/typedef_json.ts`).
// It is consumed by the entrypoint emitter (entrypoint.go) to render the
// static dispatch `__dagger.entrypoint.ts` file.
type TypedefModule struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description,omitempty"`
	Objects     map[string]*TypedefObject    `json:"objects"`
	Enums       map[string]*TypedefEnum      `json:"enums"`
	Interfaces  map[string]*TypedefInterface `json:"interfaces"`
}

type TypedefObject struct {
	Name            string                      `json:"name"`
	Kind            string                      `json:"kind"` // "class" | "object"
	IsExported      bool                        `json:"isExported"`
	IsDefaultExport bool                        `json:"isDefaultExport"`
	Description     string                      `json:"description"`
	Deprecated      string                      `json:"deprecated,omitempty"`
	Location        *TypedefLocation            `json:"location,omitempty"`
	Constructor     *TypedefConstructor         `json:"constructor,omitempty"`
	Methods         map[string]*TypedefFunction `json:"methods"`
	Properties      map[string]*TypedefProperty `json:"properties"`
}

type TypedefConstructor struct {
	Name      string             `json:"name"`
	Arguments []*TypedefArgument `json:"arguments"`
}

type TypedefFunction struct {
	Name        string             `json:"name"`
	Alias       string             `json:"alias,omitempty"`
	Cache       string             `json:"cache,omitempty"`
	Description string             `json:"description"`
	Deprecated  string             `json:"deprecated,omitempty"`
	IsCheck     bool               `json:"isCheck"`
	IsGenerator bool               `json:"isGenerator"`
	IsUp        bool               `json:"isUp"`
	ReturnType  *TypedefType       `json:"returnType,omitempty"`
	Arguments   []*TypedefArgument `json:"arguments"`
}

type TypedefArgument struct {
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	Deprecated     string          `json:"deprecated,omitempty"`
	Type           *TypedefType    `json:"type,omitempty"`
	IsVariadic     bool            `json:"isVariadic"`
	IsNullable     bool            `json:"isNullable"`
	IsOptional     bool            `json:"isOptional"`
	DefaultValue   json.RawMessage `json:"defaultValue,omitempty"`
	DefaultPath    string          `json:"defaultPath,omitempty"`
	DefaultAddress string          `json:"defaultAddress,omitempty"`
	Ignore         []string        `json:"ignore,omitempty"`
}

type TypedefProperty struct {
	Name        string       `json:"name"`
	Alias       string       `json:"alias,omitempty"`
	Description string       `json:"description,omitempty"`
	Deprecated  string       `json:"deprecated,omitempty"`
	IsExposed   bool         `json:"isExposed"`
	Type        *TypedefType `json:"type,omitempty"`
}

type TypedefEnum struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	Values      map[string]*TypedefEnumValue `json:"values"`
}

type TypedefEnumValue struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description"`
	Deprecated  string `json:"deprecated,omitempty"`
}

type TypedefInterface struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Functions   map[string]*TypedefFunction `json:"functions"`
}

// TypedefType is the discriminated typedef payload — `kind` is one of the
// "*_KIND" string constants from the Dagger GraphQL schema. `Name` is set
// for OBJECT/ENUM/INTERFACE/SCALAR; `TypeDef` is set for LIST.
type TypedefType struct {
	Kind    string       `json:"kind"`
	Name    string       `json:"name,omitempty"`
	TypeDef *TypedefType `json:"typeDef,omitempty"`
}

type TypedefLocation struct {
	Filepath string `json:"filepath"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// TypeDef kind constants — match the Dagger GraphQL schema's TypeDefKind enum.
const (
	KindString    = "STRING_KIND"
	KindInteger   = "INTEGER_KIND"
	KindFloat     = "FLOAT_KIND"
	KindBoolean   = "BOOLEAN_KIND"
	KindVoid      = "VOID_KIND"
	KindList      = "LIST_KIND"
	KindObject    = "OBJECT_KIND"
	KindEnum      = "ENUM_KIND"
	KindInterface = "INTERFACE_KIND"
	KindScalar    = "SCALAR_KIND"
	KindInput     = "INPUT_KIND"
)
