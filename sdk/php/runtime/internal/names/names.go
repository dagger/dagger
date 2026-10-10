// Package names formats a module schema's names through the engine, for SDK
// codegen that runs without an engine session.
//
// SDK codegen doesn't format names itself: it looks them up in the schema's
// names as the engine formatted them (see hack/designs/identifier-casing.md,
// "Formatting names in codegen", in the Dagger repository). A runtime's
// codegen exec has no engine session, so the runtime formats the module
// schema's names and hands them to codegen as a sidecar file:
//
//	{"CAMEL:CAPITALIZED": {"withGPU": "withGpu", ...}, ...}
//
// The runtime's own session is served at its engineVersion, where
// Query.formatIdentifiers may not exist, so it calls the internal
// Query.__formatIdentifiers, which every view has, with the dictionary of the
// module schema's version.
package names

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
)

// Format is a casing with an acronym style, as values of the engine's Casing
// and AcronymStyle enums. Its text form, "CASING:ACRONYMS", keys the sidecar
// file.
type Format struct {
	Casing   string
	Acronyms string
}

func (f Format) String() string {
	return f.Casing + ":" + f.Acronyms
}

// FormatFunc formats a batch of names in a format with the naming dictionary
// of an engine version, returning them in input order.
type FormatFunc func(ctx context.Context, names []string, format Format, version string) ([]string, error)

const formatIdentifiersQuery = `query FormatIdentifiers($names: [String!]!, $casing: String!, $acronyms: String, $version: String!) {
  __formatIdentifiers(names: $names, casing: $casing, acronyms: $acronyms, version: $version)
}`

// Engine formats names with the engine's Query.__formatIdentifiers.
func Engine(client graphql.Client) FormatFunc {
	return func(ctx context.Context, names []string, format Format, version string) ([]string, error) {
		var data struct {
			FormatIdentifiers []string `json:"__formatIdentifiers"`
		}
		err := client.MakeRequest(ctx, &graphql.Request{
			Query:  formatIdentifiersQuery,
			OpName: "FormatIdentifiers",
			Variables: map[string]any{
				"names":    names,
				"casing":   format.Casing,
				"acronyms": format.Acronyms,
				"version":  version,
			},
		}, &graphql.Response{Data: &data})
		if err != nil {
			return nil, err
		}
		return data.FormatIdentifiers, nil
	}
}

// batchBytes bounds the size of the names sent per request. The core
// schema's names fit in one.
const batchBytes = 256 << 10

// File formats the names of the schema in schemaJSON (introspection JSON) in
// each format, as the sidecar file maps them. It returns nil for a schema
// without Query.formatIdentifiers, so codegen keeps its legacy conversion.
func File(ctx context.Context, schemaJSON []byte, formats []Format, format FormatFunc) (map[string]map[string]string, error) {
	var schema introspectionSchema
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return nil, fmt.Errorf("decode introspection JSON: %w", err)
	}
	if !schema.hasFormatIdentifiers() {
		return nil, nil
	}
	names := schema.names()
	file := make(map[string]map[string]string, len(formats))
	for _, f := range formats {
		formatted := make(map[string]string, len(names))
		for todo := names; len(todo) > 0; {
			n, size := 0, 0
			for n < len(todo) && (n == 0 || size+len(todo[n]) <= batchBytes) {
				size += len(todo[n])
				n++
			}
			batch := todo[:n]
			todo = todo[n:]
			out, err := format(ctx, batch, f, schema.SchemaVersion)
			if err != nil {
				return nil, fmt.Errorf("format names as %s: %w", f, err)
			}
			if len(out) != len(batch) {
				return nil, fmt.Errorf("format names as %s: sent %d names, got %d back", f, len(batch), len(out))
			}
			for i, name := range batch {
				formatted[name] = out[i]
			}
		}
		file[f.String()] = formatted
	}
	return file, nil
}

// introspectionSchema is the part of the introspection JSON naming needs.
type introspectionSchema struct {
	Schema struct {
		Types []struct {
			Name   string `json:"name"`
			Fields []struct {
				Name string `json:"name"`
				Args []struct {
					Name string `json:"name"`
				} `json:"args"`
			} `json:"fields"`
			InputFields []struct {
				Name string `json:"name"`
			} `json:"inputFields"`
			EnumValues []struct {
				Name string `json:"name"`
			} `json:"enumValues"`
		} `json:"types"`
	} `json:"__schema"`
	SchemaVersion string `json:"__schemaVersion"`
}

// hasFormatIdentifiers reports whether the schema has
// Query.formatIdentifiers: the gate for formatting its names through the
// engine.
func (s *introspectionSchema) hasFormatIdentifiers() bool {
	for _, t := range s.Schema.Types {
		if t.Name != "Query" {
			continue
		}
		for _, f := range t.Fields {
			if f.Name == "formatIdentifiers" {
				return true
			}
		}
	}
	return false
}

// names returns the distinct type, field, argument, input field and enum
// value names in the schema, sorted, leaving out introspection names ("__"
// prefix) and names the engine can't format (non-ASCII, or no letters or
// digits).
func (s *introspectionSchema) names() []string {
	seen := map[string]bool{}
	add := func(name string) {
		if !strings.HasPrefix(name, "__") && formattable(name) {
			seen[name] = true
		}
	}
	for _, t := range s.Schema.Types {
		if strings.HasPrefix(t.Name, "__") {
			continue
		}
		add(t.Name)
		for _, f := range t.Fields {
			add(f.Name)
			for _, arg := range f.Args {
				add(arg.Name)
			}
		}
		for _, f := range t.InputFields {
			add(f.Name)
		}
		for _, v := range t.EnumValues {
			add(v.Name)
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func formattable(name string) bool {
	alnum := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 0x80 {
			return false
		}
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
			alnum = true
		}
	}
	return alnum
}
