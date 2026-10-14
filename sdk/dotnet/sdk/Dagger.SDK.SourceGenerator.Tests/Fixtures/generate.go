//go:build ignore

// Regenerates the core schema fixtures of the source generator tests from
// docs/docs-graphql/schema.graphqls, without an engine. Run it from the
// repository root after the schema changes:
//
//	go run ./sdk/dotnet/sdk/Dagger.SDK.SourceGenerator.Tests/Fixtures/generate.go
//
// core-schema.json is the schema as introspection JSON, without descriptions
// (they only add size). core-names.json is the names sidecar `codegen
// introspect --names-out` writes for it, in the formats the source generator
// reads, formatted with the engine's own naming package, so it is what the
// engine would return for this schema version.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/engine/naming"
)

// schemaVersion is the __schemaVersion written to the fixture, and the engine
// version whose naming dictionary formats the names.
const schemaVersion = "v1.0.0"

// nameFormats are the name formats the source generator reads: PascalCase
// members and camelCase parameters, with acronyms written like words.
var nameFormats = []struct {
	casing naming.Casing
	key    string
}{
	{naming.Pascal, "PASCAL:CAPITALIZED"},
	{naming.Camel, "CAMEL:CAPITALIZED"},
}

var builtinScalars = []string{"Boolean", "Float", "ID", "Int", "String"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	_, self, _, _ := runtime.Caller(0)
	fixtures := filepath.Dir(self)
	root := filepath.Join(fixtures, "..", "..", "..", "..", "..")

	sdlPath := filepath.Join(root, "docs", "docs-graphql", "schema.graphqls")
	sdl, err := os.ReadFile(sdlPath)
	if err != nil {
		return err
	}
	doc, err := parser.ParseSchema(&ast.Source{Name: sdlPath, Input: string(sdl)})
	if err != nil {
		return err
	}

	schemaJSON, err := json.Marshal(map[string]any{
		"__schema":        schemaObject(doc),
		"__schemaVersion": schemaVersion,
	})
	if err != nil {
		return err
	}

	var resp introspection.Response
	if err := json.Unmarshal(schemaJSON, &resp); err != nil {
		return err
	}
	if !resp.Schema.HasFormatIdentifiers() {
		return fmt.Errorf("the schema has no Query.formatIdentifiers")
	}
	dict := naming.DictionaryFor(schemaVersion)
	names := map[string]map[string]string{}
	for _, f := range nameFormats {
		formatted := map[string]string{}
		for _, name := range resp.Schema.Names() {
			id, err := dict.Parse(name)
			if err != nil {
				return fmt.Errorf("parse %q: %w", name, err)
			}
			formatted[name] = id.Format(f.casing, naming.Capitalized)
		}
		names[f.key] = formatted
	}
	namesJSON, err := json.Marshal(names)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(fixtures, "core-schema.json"), append(schemaJSON, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(fixtures, "core-names.json"), append(namesJSON, '\n'), 0o644)
}

// schemaObject converts the schema document to the "__schema" object of an
// introspection response, leaving out descriptions. Types are sorted by name
// and include the built-in scalars; fields, arguments and values keep the
// document's order. Keys are only written when they have a value.
func schemaObject(doc *ast.SchemaDocument) map[string]any {
	kinds := map[string]string{}
	for _, name := range builtinScalars {
		kinds[name] = "SCALAR"
	}
	for _, def := range doc.Definitions {
		kinds[def.Name] = string(def.Kind)
	}

	types := []map[string]any{}
	for _, name := range builtinScalars {
		types = append(types, map[string]any{"kind": "SCALAR", "name": name})
	}
	for _, def := range doc.Definitions {
		t := map[string]any{"kind": string(def.Kind), "name": def.Name}
		if len(def.Interfaces) > 0 {
			var ifaces []map[string]any
			for _, iface := range def.Interfaces {
				ifaces = append(ifaces, map[string]any{"kind": "INTERFACE", "name": iface})
			}
			t["interfaces"] = ifaces
		}
		switch def.Kind {
		case ast.Object, ast.Interface:
			var fields []map[string]any
			for _, f := range def.Fields {
				field := map[string]any{"name": f.Name, "type": typeRef(f.Type, kinds)}
				if len(f.Arguments) > 0 {
					var args []map[string]any
					for _, arg := range f.Arguments {
						args = append(args, inputValue(arg.Name, arg.Type, arg.DefaultValue, arg.Directives, kinds))
					}
					field["args"] = args
				}
				addDirectives(field, f.Directives)
				fields = append(fields, field)
			}
			if len(fields) > 0 {
				t["fields"] = fields
			}
		case ast.InputObject:
			var fields []map[string]any
			for _, f := range def.Fields {
				fields = append(fields, inputValue(f.Name, f.Type, f.DefaultValue, f.Directives, kinds))
			}
			if len(fields) > 0 {
				t["inputFields"] = fields
			}
		case ast.Enum:
			var values []map[string]any
			for _, v := range def.EnumValues {
				value := map[string]any{"name": v.Name}
				addDirectives(value, v.Directives)
				values = append(values, value)
			}
			if len(values) > 0 {
				t["enumValues"] = values
			}
		}
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool {
		return types[i]["name"].(string) < types[j]["name"].(string)
	})

	return map[string]any{
		"queryType": map[string]any{"name": "Query"},
		"types":     types,
	}
}

func inputValue(name string, t *ast.Type, def *ast.Value, directives ast.DirectiveList, kinds map[string]string) map[string]any {
	value := map[string]any{"name": name, "type": typeRef(t, kinds)}
	if def != nil {
		value["defaultValue"] = def.String()
	}
	addDirectives(value, directives)
	return value
}

func typeRef(t *ast.Type, kinds map[string]string) map[string]any {
	if t.NonNull {
		inner := *t
		inner.NonNull = false
		return map[string]any{"kind": "NON_NULL", "ofType": typeRef(&inner, kinds)}
	}
	if t.Elem != nil {
		return map[string]any{"kind": "LIST", "ofType": typeRef(t.Elem, kinds)}
	}
	return map[string]any{"kind": kinds[t.NamedType], "name": t.NamedType}
}

// addDirectives writes @deprecated as isDeprecated and deprecationReason, and
// the other directives as a "directives" list, like the engine does.
func addDirectives(obj map[string]any, directives ast.DirectiveList) {
	var list []map[string]any
	for _, d := range directives {
		if d.Name == "deprecated" {
			obj["isDeprecated"] = true
			reason := "No longer supported"
			if arg := d.Arguments.ForName("reason"); arg != nil {
				reason = arg.Value.Raw
			}
			obj["deprecationReason"] = reason
			continue
		}
		directive := map[string]any{"name": d.Name}
		if len(d.Arguments) > 0 {
			var args []map[string]any
			for _, arg := range d.Arguments {
				args = append(args, map[string]any{"name": arg.Name, "value": arg.Value.String()})
			}
			directive["args"] = args
		}
		list = append(list, directive)
	}
	if len(list) > 0 {
		obj["directives"] = list
	}
}
