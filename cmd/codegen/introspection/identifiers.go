package introspection

import (
	"encoding/json"
	"strings"

	"github.com/dagger/dagger/engine/naming"
)

// IdentifierWord is one word of a schema name, as the engine parsed it. The
// engine writes these to the schema JSON's top-level "__identifiers" key, so
// codegen that runs offline from that JSON can format names in any casing
// without the naming dictionary (see hack/designs/identifier-casing.md,
// "Schema JSON words").
type IdentifierWord struct {
	// Kind is WORD, ACRONYM or TERM.
	Kind string `json:"kind"`
	// Text is the word's standard spelling: client, HTTP, GitHub.
	Text string `json:"text"`
	// Suffix is a plural "s" and/or trailing digits: SHA+"s", OAuth+"2".
	Suffix string `json:"suffix"`
	// Capitalized is the word's form in the CAPITALIZED acronym style,
	// without the suffix: Client, Http, GitHub, Ipv6, 3D.
	Capitalized string `json:"capitalized"`
}

// Identifiers maps schema names to their words.
type Identifiers map[string][]IdentifierWord

// NewIdentifierWords converts a parsed name to its schema JSON words.
func NewIdentifierWords(id naming.Identifier) []IdentifierWord {
	words := make([]IdentifierWord, len(id.Words))
	for i, w := range id.Words {
		// Format a lone word without its suffix to get exactly the
		// capitalized form naming.Format uses.
		bare := w
		bare.Suffix = ""
		words[i] = IdentifierWord{
			Kind:        w.Kind.String(),
			Text:        w.Text,
			Suffix:      w.Suffix,
			Capitalized: naming.Identifier{Words: []naming.Word{bare}}.Format(naming.Pascal, naming.Capitalized),
		}
	}
	return words
}

// Add parses name with dict and records its words, unless it is already
// present, is an introspection name ("__" prefix), or doesn't parse (a name
// with no letters or digits, like "_"); codegen falls back to its own
// conversion for names that are missing.
func (ids Identifiers) Add(dict *naming.Dictionary, name string) {
	if strings.HasPrefix(name, "__") {
		return
	}
	if _, ok := ids[name]; ok {
		return
	}
	id, err := dict.Parse(name)
	if err != nil {
		return
	}
	ids[name] = NewIdentifierWords(id)
}

// AddSchema records the words of every type, field, argument, input field
// and enum value name in the schema.
func (ids Identifiers) AddSchema(dict *naming.Dictionary, s *Schema) {
	for _, t := range s.Types {
		if strings.HasPrefix(t.Name, "__") {
			// Introspection types, and every name inside them.
			continue
		}
		ids.Add(dict, t.Name)
		for _, f := range t.Fields {
			ids.Add(dict, f.Name)
			for _, arg := range f.Args {
				ids.Add(dict, arg.Name)
			}
		}
		for _, f := range t.InputFields {
			ids.Add(dict, f.Name)
		}
		for _, v := range t.EnumValues {
			ids.Add(dict, v.Name)
		}
	}
}

// Identifier returns the words the engine parsed name into, as a
// naming.Identifier that naming.Format formats exactly like the engine
// would. It reports false when the schema JSON has no words for name: the
// schema predates identifier words (older engine versions), or the name is
// an introspection name.
//
// Every ACRONYM and TERM word gets a Term carrying its capitalized form, since
// the JSON doesn't say whether a word came from the dictionary; formatting
// doesn't depend on it.
func (s *Schema) Identifier(name string) (naming.Identifier, bool) {
	jsonWords, ok := s.Identifiers[name]
	if !ok || len(jsonWords) == 0 {
		return naming.Identifier{}, false
	}
	words := make([]naming.Word, len(jsonWords))
	for i, w := range jsonWords {
		word := naming.Word{Text: w.Text, Suffix: w.Suffix}
		switch w.Kind {
		case naming.KindWord.String():
			word.Kind = naming.KindWord
		case naming.KindAcronym.String():
			word.Kind = naming.KindAcronym
		case naming.KindTerm.String():
			word.Kind = naming.KindTerm
		default:
			// A kind from a newer engine: let the caller fall back.
			return naming.Identifier{}, false
		}
		if word.Kind != naming.KindWord {
			word.Term = &naming.Term{Spelling: w.Text, Capitalized: w.Capitalized}
		}
		words[i] = word
	}
	return naming.Identifier{Name: name, Words: words}, true
}

// responseJSON is the JSON form of Response. The identifiers live on the
// Schema so they travel with it through codegen, but are serialized next to
// __schema and __schemaVersion.
type responseJSON struct {
	Schema        *Schema     `json:"__schema"`
	SchemaVersion string      `json:"__schemaVersion"`
	Identifiers   Identifiers `json:"__identifiers,omitempty"`
}

func (r Response) MarshalJSON() ([]byte, error) {
	out := responseJSON{Schema: r.Schema, SchemaVersion: r.SchemaVersion}
	if r.Schema != nil {
		out.Identifiers = r.Schema.Identifiers
	}
	return json.Marshal(out)
}

func (r *Response) UnmarshalJSON(data []byte) error {
	var in responseJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	r.Schema = in.Schema
	r.SchemaVersion = in.SchemaVersion
	if r.Schema != nil {
		r.Schema.Identifiers = in.Identifiers
	}
	return nil
}
