package introspection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dagger.io/dagger"
)

// Codegen names SDK identifiers by asking the engine to format schema names,
// with Query.formatIdentifiers, so the parsing and formatting rules (and the
// naming dictionary of the client's engine version) live only in the engine;
// see hack/designs/identifier-casing.md, "Formatting names in codegen".
//
// The gate is the schema being generated: a schema without
// Query.formatIdentifiers (schema views before v1.0.0-0, or older engines)
// formats nothing, and codegen keeps its legacy converters, so older schemas
// generate byte-identical code.

// Casing is a convention for joining words into an identifier: a value of the
// engine's Casing enum.
type Casing string

const (
	CasingPascal         Casing = "PASCAL"          // HTTPClient
	CasingCamel          Casing = "CAMEL"           // httpClient
	CasingSnake          Casing = "SNAKE"           // http_client
	CasingScreamingSnake Casing = "SCREAMING_SNAKE" // HTTP_CLIENT
	CasingKebab          Casing = "KEBAB"           // http-client
	CasingFlat           Casing = "FLAT"            // httpclient
)

// AcronymStyle is how acronyms and terms are written where a word starts with
// a capital: a value of the engine's AcronymStyle enum.
type AcronymStyle string

const (
	AcronymsUppercase   AcronymStyle = "UPPERCASE"   // HTTPClient
	AcronymsCapitalized AcronymStyle = "CAPITALIZED" // HttpClient
)

// NameFormat is a casing with an acronym style. Its text form is
// "CASING:ACRONYMS", e.g. "SNAKE:UPPERCASE" or "PASCAL:CAPITALIZED".
type NameFormat struct {
	Casing   Casing
	Acronyms AcronymStyle
}

func (f NameFormat) String() string {
	return string(f.Casing) + ":" + string(f.Acronyms)
}

// ParseNameFormat parses the "CASING:ACRONYMS" form of a NameFormat. Both
// parts are required; their values are checked by the engine, so formats it
// gains later work without a codegen change.
func ParseNameFormat(s string) (NameFormat, error) {
	casing, acronyms, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || casing == "" || acronyms == "" || !isEnumName(casing) || !isEnumName(acronyms) {
		return NameFormat{}, fmt.Errorf("invalid name format %q: want CASING:ACRONYMS, e.g. SNAKE:UPPERCASE", s)
	}
	return NameFormat{Casing: Casing(casing), Acronyms: AcronymStyle(acronyms)}, nil
}

// ParseNameFormats parses a comma-separated list of name formats.
func ParseNameFormats(s string) ([]NameFormat, error) {
	var formats []NameFormat
	for part := range strings.SplitSeq(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		f, err := ParseNameFormat(part)
		if err != nil {
			return nil, err
		}
		formats = append(formats, f)
	}
	return formats, nil
}

func isEnumName(s string) bool {
	for _, r := range s {
		if (r < 'A' || r > 'Z') && r != '_' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// HasFormatIdentifiers reports whether the schema has Query.formatIdentifiers,
// the gate for formatting its names through the engine.
func (s *Schema) HasFormatIdentifiers() bool {
	if s == nil {
		return false
	}
	query := s.Query()
	if query == nil {
		return false
	}
	for _, f := range query.Fields {
		if f.Name == "formatIdentifiers" {
			return true
		}
	}
	return false
}

// Names returns the distinct type, field, argument, input field and enum
// value names in the schema, sorted. It leaves out introspection names ("__"
// prefix, including every name inside an introspection type) and names the
// engine can't format (no letters or digits, like "_").
func (s *Schema) Names() []string {
	seen := map[string]bool{}
	add := func(name string) {
		if strings.HasPrefix(name, "__") || !formattable(name) {
			return
		}
		seen[name] = true
	}
	for _, t := range s.Types {
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

// formatBatchBytes bounds the size of the names sent per formatIdentifiers
// request. The core schema's names fit in one.
const formatBatchBytes = 256 << 10

const formatIdentifiersQuery = `query FormatIdentifiers($names: [String!]!, $casing: Casing!, $acronyms: AcronymStyle) {
  formatIdentifiers(names: $names, casing: $casing, acronyms: $acronyms)
}`

// FormatNames asks the engine to format names in the given casing and acronym
// style, and returns a map from each name to its formatted form. The engine
// parses the names with the naming dictionary of dag's engine version.
//
// It reports false, formatting nothing, when schema (the schema codegen
// generates from) has no Query.formatIdentifiers: codegen then keeps its
// legacy converter. Names the engine can't format (see Names) are left out of
// the map, as are duplicates. Large inputs are sent in several requests.
func FormatNames(ctx context.Context, dag *dagger.Client, schema *Schema, names []string, casing Casing, acronyms AcronymStyle) (map[string]string, bool, error) {
	if !schema.HasFormatIdentifiers() {
		return nil, false, nil
	}
	if dag == nil {
		return nil, false, fmt.Errorf("format names: the schema has Query.formatIdentifiers, but codegen has no engine connection")
	}

	seen := make(map[string]bool, len(names))
	var todo []string
	for _, name := range names {
		if seen[name] || !formattable(name) {
			continue
		}
		seen[name] = true
		todo = append(todo, name)
	}

	formatted := make(map[string]string, len(todo))
	for len(todo) > 0 {
		n, size := 0, 0
		for n < len(todo) && (n == 0 || size+len(todo[n]) <= formatBatchBytes) {
			size += len(todo[n])
			n++
		}
		batch := todo[:n]
		todo = todo[n:]

		var data struct {
			FormatIdentifiers []string `json:"formatIdentifiers"`
		}
		if err := dag.Do(ctx, &dagger.Request{
			Query:  formatIdentifiersQuery,
			OpName: "FormatIdentifiers",
			Variables: map[string]any{
				"names":    batch,
				"casing":   string(casing),
				"acronyms": string(acronyms),
			},
		}, &dagger.Response{
			Data: &data,
		}); err != nil {
			return nil, false, fmt.Errorf("format names as %s: %w", NameFormat{casing, acronyms}, err)
		}
		if len(data.FormatIdentifiers) != len(batch) {
			return nil, false, fmt.Errorf("format names as %s: sent %d names, got %d back", NameFormat{casing, acronyms}, len(batch), len(data.FormatIdentifiers))
		}
		for i, name := range batch {
			formatted[name] = data.FormatIdentifiers[i]
		}
	}
	return formatted, true, nil
}

// LoadFormattedNames formats every name of the schema (see Names) in each of
// formats through the engine (see FormatNames), and keeps the results on the
// schema for FormattedName. Formats already loaded are skipped. A schema
// without Query.formatIdentifiers is left as is, so FormattedName reports
// false and codegen falls back to its legacy converters.
func (s *Schema) LoadFormattedNames(ctx context.Context, dag *dagger.Client, formats ...NameFormat) error {
	if !s.HasFormatIdentifiers() {
		return nil
	}
	var names []string
	for _, f := range formats {
		if _, ok := s.FormattedNames[f]; ok {
			continue
		}
		if names == nil {
			names = s.Names()
		}
		formatted, ok, err := FormatNames(ctx, dag, s, names, f.Casing, f.Acronyms)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if s.FormattedNames == nil {
			s.FormattedNames = map[NameFormat]map[string]string{}
		}
		s.FormattedNames[f] = formatted
	}
	return nil
}

// FormattedName returns a schema name as the engine formatted it in f. It
// reports false when the name wasn't formatted in f: the schema predates
// Query.formatIdentifiers, f wasn't loaded, or the name isn't one of the
// schema's names (see Names). Codegen then uses its legacy converter.
func (s *Schema) FormattedName(name string, f NameFormat) (string, bool) {
	if s == nil {
		return "", false
	}
	formatted, ok := s.FormattedNames[f][name]
	return formatted, ok
}

// NamesFile is the JSON sidecar `codegen introspect --names-out` writes for
// SDK codegen that runs without an engine connection: the text form of each
// name format ("SNAKE:UPPERCASE") maps to the schema's names formatted in it.
// A format without a key wasn't formatted (the schema has no
// Query.formatIdentifiers), and a name missing from a format's map isn't
// formattable: codegen uses its legacy converter for both.
type NamesFile map[string]map[string]string

// NamesFile returns the formatted names loaded on the schema (see
// LoadFormattedNames) as a NamesFile. It is empty, not nil, when none are.
func (s *Schema) NamesFile() NamesFile {
	file := NamesFile{}
	for f, names := range s.FormattedNames {
		file[f.String()] = names
	}
	return file
}
