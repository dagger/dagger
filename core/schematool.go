package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	codegenintrospection "github.com/dagger/dagger/cmd/codegen/introspection"
)

// Schema is a manipulable, in-memory GraphQL introspection schema. It wraps
// the introspection JSON shape (cmd/codegen/introspection) shared by every SDK
// so that schema inspection and merge operations can be exposed as engine
// functions, letting all SDKs reuse the exact same implementation.
type Schema struct {
	// Introspection is the parsed introspection response this schema wraps.
	Introspection *codegenintrospection.Response
}

func (*Schema) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Schema",
		NonNull:   true,
	}
}

func (*Schema) TypeDescription() string {
	return "A GraphQL introspection schema that can be inspected and merged."
}

// NewSchema parses introspection JSON into a Schema.
func NewSchema(data JSON) (*Schema, error) {
	resp, err := parseIntrospectionResponse(data)
	if err != nil {
		return nil, fmt.Errorf("parse introspection JSON: %w", err)
	}
	return &Schema{Introspection: resp}, nil
}

// parseIntrospectionResponse decodes introspection JSON and verifies it
// carries a __schema field.
func parseIntrospectionResponse(data JSON) (*codegenintrospection.Response, error) {
	var resp codegenintrospection.Response
	if err := json.Unmarshal(data.Bytes(), &resp); err != nil {
		return nil, err
	}
	if resp.Schema == nil {
		return nil, fmt.Errorf("introspection JSON has no __schema field")
	}
	return &resp, nil
}

// Contents serializes the schema back to introspection JSON.
func (s *Schema) Contents() (JSON, error) {
	data, err := json.Marshal(s.Introspection)
	if err != nil {
		return nil, fmt.Errorf("marshal introspection JSON: %w", err)
	}
	return JSON(data), nil
}

// Merge returns a new Schema with the module-defined types from moduleTypes
// (itself introspection JSON) appended. Every inserted type, and the module's
// Query constructor field, is stamped with an @sourceMap directive carrying
// moduleName — the same directive the engine stamps on module types and the
// codegen file-splitter routes on.
//
// Merge is idempotent: re-merging a module already present on the schema is a
// no-op (the multi-pass codegen loop reuses the same schema across passes).
// Presence is detected via @sourceMap.module, so types the engine itself
// contributed for the module are recognized too. A genuine name collision with
// a pre-existing, differently-owned type is an error. Neither the receiver nor
// the moduleTypes input is mutated.
func (s *Schema) Merge(moduleTypes JSON, moduleName string) (*Schema, error) {
	if moduleName == "" {
		return nil, fmt.Errorf("module name is required")
	}
	// moduleTypes is parsed into a response Merge solely owns, so stamping
	// directives onto its types never escapes back to the caller.
	module, err := parseIntrospectionResponse(moduleTypes)
	if err != nil {
		return nil, fmt.Errorf("parse module types JSON: %w", err)
	}

	merged, err := cloneResponse(s.Introspection)
	if err != nil {
		return nil, err
	}
	target := merged.Schema

	if moduleAlreadyMerged(target, moduleName) {
		return &Schema{Introspection: merged}, nil
	}

	for _, t := range module.Schema.Types {
		if !isModuleDefinedType(t) {
			continue
		}
		if target.Types.Get(t.Name) != nil {
			return nil, fmt.Errorf("type %q already exists in schema", t.Name)
		}
		t.Directives = append(t.Directives, sourceMapDirective(moduleName))
		target.Types = append(target.Types, t)
	}

	if err := mergeQueryConstructor(target, module.Schema, moduleName); err != nil {
		return nil, err
	}
	return &Schema{Introspection: merged}, nil
}

// isModuleDefinedType reports whether t is a type a module can contribute to a
// schema: a named object, interface or enum that is not a root operation type
// or a built-in introspection type.
func isModuleDefinedType(t *codegenintrospection.Type) bool {
	switch t.Kind {
	case codegenintrospection.TypeKindObject,
		codegenintrospection.TypeKindInterface,
		codegenintrospection.TypeKindEnum:
	default:
		return false
	}
	if strings.HasPrefix(t.Name, "__") {
		return false
	}
	switch t.Name {
	case "Query", "Mutation", "Subscription":
		return false
	}
	return true
}

// mergeQueryConstructor adds the module's constructor field to the schema's
// Query type. If the module's own introspection already declares the field on
// its Query type, that field (carrying its arguments) is reused; otherwise a
// no-arg constructor pointing at the module's main object is synthesized. The
// main object is the one whose name matches moduleName in PascalCase.
func mergeQueryConstructor(target, module *codegenintrospection.Schema, moduleName string) error {
	queryType := target.Query()
	if queryType == nil {
		return fmt.Errorf("schema has no Query type")
	}

	fieldName := strcase.ToLowerCamel(moduleName)
	if findField(queryType, fieldName) != nil {
		// Idempotent: the constructor is already registered.
		return nil
	}

	if modQuery := module.Query(); modQuery != nil {
		if field := findField(modQuery, fieldName); field != nil {
			field.Directives = append(field.Directives, sourceMapDirective(moduleName))
			queryType.Fields = append(queryType.Fields, field)
			return nil
		}
	}

	mainObject := target.Types.Get(strcase.ToCamel(moduleName))
	if mainObject == nil {
		// No main object: the module's other types are still merged, but
		// there is nothing to construct.
		return nil
	}
	queryType.Fields = append(queryType.Fields, &codegenintrospection.Field{
		Name:        fieldName,
		Description: mainObject.Description,
		TypeRef: &codegenintrospection.TypeRef{
			Kind: codegenintrospection.TypeKindNonNull,
			OfType: &codegenintrospection.TypeRef{
				Kind: codegenintrospection.TypeKindObject,
				Name: mainObject.Name,
			},
		},
		Args:       codegenintrospection.InputValues{},
		Directives: codegenintrospection.Directives{sourceMapDirective(moduleName)},
	})
	return nil
}

// moduleAlreadyMerged reports whether the schema already carries a type or a
// Query constructor field stamped with @sourceMap for the given module. This
// keys on the same directive the engine emits on module types, so a module
// whose types are already present — whether from a prior Merge or contributed
// by the engine — is recognized.
func moduleAlreadyMerged(schema *codegenintrospection.Schema, moduleName string) bool {
	for _, t := range schema.Types {
		if sm := t.Directives.SourceMap(); sm != nil && sm.Module == moduleName {
			return true
		}
	}
	if query := schema.Query(); query != nil {
		for _, f := range query.Fields {
			if sm := f.Directives.SourceMap(); sm != nil && sm.Module == moduleName {
				return true
			}
		}
	}
	return false
}

// sourceMapDirective builds the @sourceMap directive stamped on merged
// types and the constructor field. The codegen file-splitter
// (cmd/codegen/introspection: DependencyNames/Include/Exclude) reads the
// "module" arg to place a module's types in internal/dagger/<module>.gen.go.
func sourceMapDirective(moduleName string) *codegenintrospection.Directive {
	value := encodeDirectiveValue(moduleName)
	return &codegenintrospection.Directive{
		Name: "sourceMap",
		Args: []*codegenintrospection.DirectiveArg{
			{Name: "module", Value: &value},
		},
	}
}

// encodeDirectiveValue JSON-encodes a directive argument value, mirroring how
// introspection responses carry directive argument values (a string is
// quoted). sourceMapDirective uses it on the write side; the read side decodes
// via Directives.SourceMap().
func encodeDirectiveValue(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

func findField(t *codegenintrospection.Type, name string) *codegenintrospection.Field {
	for _, f := range t.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// cloneResponse deep-copies an introspection response through a JSON round
// trip, so Merge can mutate the copy without affecting the receiver.
func cloneResponse(r *codegenintrospection.Response) (*codegenintrospection.Response, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("clone schema: %w", err)
	}
	var out codegenintrospection.Response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("clone schema: %w", err)
	}
	return &out, nil
}
