package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"java-sdk/internal/dagger"
)

// Codegen names Java identifiers with names the engine formatted (see
// hack/designs/identifier-casing.md, "Formatting names in codegen"). The
// codegen plugin runs in an exec without an engine session, so the runtime
// formats the names of the module's schema here and hands them to the plugin
// as a names file (daggerengine.names), the same sidecar `codegen introspect
// --names-out` writes for the client.
//
// The runtime's own session is served at its engineVersion, which has
// Query.formatIdentifiers; it passes the version of the schema being
// generated, so names are parsed with that version's dictionary.

// nameFormat is a casing and acronym style, as the engine's Casing and
// AcronymStyle values.
type nameFormat struct {
	casing   string
	acronyms string
}

// key is the format's key in a names file: CASING:ACRONYMS.
func (f nameFormat) key() string {
	return f.casing + ":" + f.acronyms
}

// namesFormats are the name formats the Java codegen plugin reads: members in
// camelCase and classes in PascalCase, both with acronyms written like words
// (asJson, JsonValue), and enum constants in SCREAMING_SNAKE_CASE
// (PER_SESSION).
var namesFormats = []nameFormat{
	{"CAMEL", "CAPITALIZED"},
	{"PASCAL", "CAPITALIZED"},
	{"SCREAMING_SNAKE", "UPPERCASE"},
}

// NamesPath is where the codegen plugin reads the names file from.
const NamesPath = "/names.json"

// formatNamesBatchBytes bounds the size of the names sent per request.
const formatNamesBatchBytes = 256 << 10

// formatNamesQuery formats a batch of names in every format of namesFormats,
// one alias (f0, f1, ...) per format.
var formatNamesQuery = func() string {
	var query strings.Builder
	query.WriteString("query FormatIdentifiers($names: [String!]!, $version: String) {\n")
	for i, f := range namesFormats {
		fmt.Fprintf(&query, "  f%d: formatIdentifiers(names: $names, casing: %s, acronyms: %s, version: $version)\n", i, f.casing, f.acronyms)
	}
	query.WriteString("}")
	return query.String()
}()

// introspectionSchema is the part of an introspection result codegen names
// come from.
type introspectionSchema struct {
	Schema struct {
		QueryType *struct {
			Name string `json:"name"`
		} `json:"queryType"`
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
// Query.formatIdentifiers: the gate for naming with engine-formatted names.
// Schemas without it (before v1.0.0) keep codegen's legacy conversion.
func (s *introspectionSchema) hasFormatIdentifiers() bool {
	queryName := "Query"
	if s.Schema.QueryType != nil && s.Schema.QueryType.Name != "" {
		queryName = s.Schema.QueryType.Name
	}
	for _, t := range s.Schema.Types {
		if t.Name != queryName {
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
// value names in the schema, sorted. It leaves out introspection names ("__"
// prefix, including every name inside an introspection type) and names the
// engine can't format (no letters or digits, like "_").
func (s *introspectionSchema) names() []string {
	seen := map[string]bool{}
	add := func(name string) {
		if strings.HasPrefix(name, "__") || !formattable(name) {
			return
		}
		seen[name] = true
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

// formattable reports whether the engine can parse name: ASCII, with at least
// one letter or digit.
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

// batchNames splits names into batches of at most maxBytes (but at least one
// name each).
func batchNames(names []string, maxBytes int) [][]string {
	var batches [][]string
	for len(names) > 0 {
		n, size := 0, 0
		for n < len(names) && (n == 0 || size+len(names[n]) <= maxBytes) {
			size += len(names[n])
			n++
		}
		batches = append(batches, names[:n])
		names = names[n:]
	}
	return batches
}

// formatNamesFunc formats a batch of names in every format of namesFormats,
// returning them in input order, keyed by format key.
type formatNamesFunc func(ctx context.Context, names []string, version string) (map[string][]string, error)

// schemaNamesFile returns the names file for an introspection result: its
// names formatted in every format of namesFormats, as {"CAMEL:CAPITALIZED":
// {name: formatted}, ...}. It returns nil for a schema without
// Query.formatIdentifiers.
func schemaNamesFile(ctx context.Context, introspectionJSON []byte, format formatNamesFunc) ([]byte, error) {
	var schema introspectionSchema
	if err := json.Unmarshal(introspectionJSON, &schema); err != nil {
		return nil, fmt.Errorf("decode introspection json: %w", err)
	}
	if !schema.hasFormatIdentifiers() {
		return nil, nil
	}
	file := map[string]map[string]string{}
	for _, f := range namesFormats {
		file[f.key()] = map[string]string{}
	}
	for _, batch := range batchNames(schema.names(), formatNamesBatchBytes) {
		out, err := format(ctx, batch, schema.SchemaVersion)
		if err != nil {
			return nil, err
		}
		for _, f := range namesFormats {
			formatted := out[f.key()]
			if len(formatted) != len(batch) {
				return nil, fmt.Errorf("format names as %s: sent %d names, got %d back", f.key(), len(batch), len(formatted))
			}
			for i, name := range batch {
				file[f.key()][name] = formatted[i]
			}
		}
	}
	return json.Marshal(file)
}

// formatNamesWithEngine formats names with Query.formatIdentifiers, in one
// request for every format.
func formatNamesWithEngine(ctx context.Context, names []string, version string) (map[string][]string, error) {
	var data map[string][]string
	vars := map[string]any{
		"names": names,
	}
	if version != "" {
		vars["version"] = version
	}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query:     formatNamesQuery,
		OpName:    "FormatIdentifiers",
		Variables: vars,
	}, &graphql.Response{Data: &data})
	if err != nil {
		return nil, fmt.Errorf("format names: %w", err)
	}
	out := map[string][]string{}
	for i, f := range namesFormats {
		out[f.key()] = data[fmt.Sprintf("f%d", i)]
	}
	return out, nil
}

// codegenNames returns the names file the codegen plugin reads for a module's
// schema, or nil if the schema has no Query.formatIdentifiers.
func codegenNames(ctx context.Context, introspectionJSON *dagger.File) (*dagger.File, error) {
	contents, err := introspectionJSON.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read introspection json: %w", err)
	}
	data, err := schemaNamesFile(ctx, []byte(contents), formatNamesWithEngine)
	if err != nil || data == nil {
		return nil, err
	}
	return dag.File("names.json", string(data)), nil
}
