// Package testnames formats schema names the way the engine's
// Query.formatIdentifiers does, for codegen tests that run without an engine.
package testnames

import (
	"fmt"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/engine/naming"
)

// Format formats names in f with the latest naming dictionary, leaving out
// names the engine can't format, like introspection.FormatNames.
func Format(names []string, f introspection.NameFormat) map[string]string {
	casing, style := convert(f)
	formatted := map[string]string{}
	for _, name := range names {
		id, err := naming.Parse(name)
		if err != nil {
			continue
		}
		formatted[name] = id.Format(casing, style)
	}
	return formatted
}

// Load formats every name of the schema (introspection.Schema.Names) in each
// of formats and keeps them on the schema, like
// introspection.Schema.LoadFormattedNames against an engine, whether or not
// the schema has Query.formatIdentifiers.
func Load(schema *introspection.Schema, formats ...introspection.NameFormat) {
	names := schema.Names()
	if schema.FormattedNames == nil {
		schema.FormattedNames = map[introspection.NameFormat]map[string]string{}
	}
	for _, f := range formats {
		schema.FormattedNames[f] = Format(names, f)
	}
}

func convert(f introspection.NameFormat) (naming.Casing, naming.AcronymStyle) {
	for _, casing := range naming.Casings {
		if casing.String() != string(f.Casing) {
			continue
		}
		for _, style := range naming.AcronymStyles {
			if style.String() == string(f.Acronyms) {
				return casing, style
			}
		}
	}
	panic(fmt.Sprintf("unknown name format %s", f))
}
