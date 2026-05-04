package schematool

import "github.com/dagger/dagger/cmd/codegen/introspection"

func listTypes(schema *introspection.Schema, kind string) []string {
	var out []string
	for _, t := range schema.Types {
		if kind != "" && string(t.Kind) != kind {
			continue
		}
		out = append(out, t.Name)
	}
	return out
}
