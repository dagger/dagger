package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/naming"
)

// Identifier is a name parsed into words by engine/naming.
type Identifier struct {
	Name  string            `field:"true" doc:"The name as given."`
	Words []*IdentifierWord `field:"true" doc:"The words that make up the name, in order."`
}

func (*Identifier) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Identifier",
		NonNull:   true,
	}
}

func (*Identifier) TypeDescription() string {
	return "A name parsed into words, which can be formatted in any casing."
}

// NewIdentifier converts a parsed name to its API representation.
func NewIdentifier(id naming.Identifier) *Identifier {
	words := make([]*IdentifierWord, len(id.Words))
	for i, w := range id.Words {
		word := &IdentifierWord{
			Text:   w.Text,
			Suffix: w.Suffix,
			Kind:   identifierWordKinds[w.Kind],
		}
		if w.Term != nil {
			word.Term = NewNamingTerm(*w.Term)
		}
		words[i] = word
	}
	return &Identifier{Name: id.Name, Words: words}
}

// Naming converts the identifier back to the engine/naming representation.
func (id *Identifier) Naming() naming.Identifier {
	words := make([]naming.Word, len(id.Words))
	for i, w := range id.Words {
		word := naming.Word{
			Kind:   w.Kind.naming(),
			Text:   w.Text,
			Suffix: w.Suffix,
		}
		if w.Term != nil {
			word.Term = &naming.Term{Spelling: w.Term.Spelling, Capitalized: w.Term.Capitalized}
		}
		words[i] = word
	}
	return naming.Identifier{Name: id.Name, Words: words}
}

// IdentifierWord is one word of an Identifier.
type IdentifierWord struct {
	Text   string             `field:"true" doc:"Standard spelling: \"client\" (WORD), \"HTTP\" (ACRONYM), \"GitHub\" (TERM)."`
	Suffix string             `field:"true" doc:"A plural \"s\" and/or trailing digits: SHA+\"s\", OAuth+\"2\"."`
	Kind   IdentifierWordKind `field:"true" doc:"The kind of word."`
	// Term is exposed through an explicit nullable accessor: a
	// struct-derived pointer field renders non-null.
	Term *NamingTerm
}

func (*IdentifierWord) Type() *ast.Type {
	return &ast.Type{
		NamedType: "IdentifierWord",
		NonNull:   true,
	}
}

func (*IdentifierWord) TypeDescription() string {
	return "One word of an identifier."
}

// NamingTerm is an entry in the naming dictionary.
type NamingTerm struct {
	Spelling    string `field:"true" doc:"Standard spelling: \"HTTP\", \"IPv6\", \"GitHub\", \"iOS\"."`
	Capitalized string `field:"true" doc:"Spelling in the CAPITALIZED style: \"Http\", \"Ipv6\", \"GitHub\", \"Ios\"."`
}

func NewNamingTerm(t naming.Term) *NamingTerm {
	return &NamingTerm{Spelling: t.Spelling, Capitalized: t.Capitalized}
}

func (*NamingTerm) Type() *ast.Type {
	return &ast.Type{
		NamedType: "NamingTerm",
		NonNull:   true,
	}
}

func (*NamingTerm) TypeDescription() string {
	return "An entry in the naming dictionary."
}

// IdentifierWordKind is a GraphQL enum type.
type IdentifierWordKind string

var IdentifierWordKinds = dagql.NewEnum[IdentifierWordKind]()

var (
	IdentifierWordKindWord    = IdentifierWordKinds.Register("WORD", "An ordinary word.")
	IdentifierWordKindAcronym = IdentifierWordKinds.Register("ACRONYM", "An acronym, from the dictionary or a run of capitals.")
	IdentifierWordKindTerm    = IdentifierWordKinds.Register("TERM", "A dictionary term with a fixed mixed-case spelling (GitHub, IPv6).")
)

var identifierWordKinds = map[naming.WordKind]IdentifierWordKind{
	naming.KindWord:    IdentifierWordKindWord,
	naming.KindAcronym: IdentifierWordKindAcronym,
	naming.KindTerm:    IdentifierWordKindTerm,
}

func (k IdentifierWordKind) naming() naming.WordKind {
	switch k {
	case IdentifierWordKindAcronym:
		return naming.KindAcronym
	case IdentifierWordKindTerm:
		return naming.KindTerm
	default:
		return naming.KindWord
	}
}

func (IdentifierWordKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "IdentifierWordKind",
		NonNull:   true,
	}
}

func (IdentifierWordKind) TypeDescription() string {
	return "The kind of a word in an identifier."
}

func (IdentifierWordKind) Decoder() dagql.InputDecoder {
	return IdentifierWordKinds
}

func (k IdentifierWordKind) ToLiteral() call.Literal {
	return IdentifierWordKinds.Literal(k)
}

// Casing is a GraphQL enum type.
type Casing string

var Casings = dagql.NewEnum[Casing]()

var (
	CasingPascal         = Casings.Register("PASCAL", "HTTPClient")
	CasingCamel          = Casings.Register("CAMEL", "httpClient")
	CasingSnake          = Casings.Register("SNAKE", "http_client")
	CasingScreamingSnake = Casings.Register("SCREAMING_SNAKE", "HTTP_CLIENT")
	CasingKebab          = Casings.Register("KEBAB", "http-client")
	CasingFlat           = Casings.Register("FLAT", "httpclient (output only: drops word boundaries)")
)

var namingCasings = map[Casing]naming.Casing{
	CasingPascal:         naming.Pascal,
	CasingCamel:          naming.Camel,
	CasingSnake:          naming.Snake,
	CasingScreamingSnake: naming.ScreamingSnake,
	CasingKebab:          naming.Kebab,
	CasingFlat:           naming.Flat,
}

// Naming returns the engine/naming casing.
func (c Casing) Naming() (naming.Casing, error) {
	casing, ok := namingCasings[c]
	if !ok {
		return 0, fmt.Errorf("unknown casing %q", string(c))
	}
	return casing, nil
}

func (Casing) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Casing",
		NonNull:   true,
	}
}

func (Casing) TypeDescription() string {
	return "A convention for joining words into an identifier."
}

func (Casing) Decoder() dagql.InputDecoder {
	return Casings
}

func (c Casing) ToLiteral() call.Literal {
	return Casings.Literal(c)
}

// AcronymStyle is a GraphQL enum type.
type AcronymStyle string

var AcronymStyles = dagql.NewEnum[AcronymStyle]()

var (
	AcronymStyleUppercase   = AcronymStyles.Register("UPPERCASE", "HTTPClient, IPv6Address, GitHubRepo")
	AcronymStyleCapitalized = AcronymStyles.Register("CAPITALIZED", "HttpClient, Ipv6Address, GitHubRepo")
)

var namingAcronymStyles = map[AcronymStyle]naming.AcronymStyle{
	AcronymStyleUppercase:   naming.Uppercase,
	AcronymStyleCapitalized: naming.Capitalized,
}

// Naming returns the engine/naming acronym style.
func (s AcronymStyle) Naming() (naming.AcronymStyle, error) {
	style, ok := namingAcronymStyles[s]
	if !ok {
		return 0, fmt.Errorf("unknown acronym style %q", string(s))
	}
	return style, nil
}

func (AcronymStyle) Type() *ast.Type {
	return &ast.Type{
		NamedType: "AcronymStyle",
		NonNull:   true,
	}
}

func (AcronymStyle) TypeDescription() string {
	return "How acronyms and terms are written where a word starts with a capital."
}

func (AcronymStyle) Decoder() dagql.InputDecoder {
	return AcronymStyles
}

func (s AcronymStyle) ToLiteral() call.Literal {
	return AcronymStyles.Literal(s)
}

// Identifiers are plain data: the words, not the dictionary that produced
// them, so a decoded identifier formats the same as the one that was saved.

type persistedNamingTermPayload struct {
	Spelling    string `json:"spelling"`
	Capitalized string `json:"capitalized"`
}

type persistedIdentifierWordPayload struct {
	Text   string                      `json:"text"`
	Suffix string                      `json:"suffix,omitempty"`
	Kind   IdentifierWordKind          `json:"kind"`
	Term   *persistedNamingTermPayload `json:"term,omitempty"`
}

type persistedIdentifierPayload struct {
	Name  string                           `json:"name"`
	Words []persistedIdentifierWordPayload `json:"words"`
}

func (w *IdentifierWord) persisted() persistedIdentifierWordPayload {
	payload := persistedIdentifierWordPayload{Text: w.Text, Suffix: w.Suffix, Kind: w.Kind}
	if w.Term != nil {
		payload.Term = &persistedNamingTermPayload{Spelling: w.Term.Spelling, Capitalized: w.Term.Capitalized}
	}
	return payload
}

func (p persistedIdentifierWordPayload) decode() *IdentifierWord {
	word := &IdentifierWord{Text: p.Text, Suffix: p.Suffix, Kind: p.Kind}
	if p.Term != nil {
		word.Term = &NamingTerm{Spelling: p.Term.Spelling, Capitalized: p.Term.Capitalized}
	}
	return word
}

func (id *Identifier) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if id == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted identifier: nil identifier")
	}
	payload := persistedIdentifierPayload{
		Name:  id.Name,
		Words: make([]persistedIdentifierWordPayload, 0, len(id.Words)),
	}
	for _, w := range id.Words {
		if w == nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted identifier %q: nil word", id.Name)
		}
		payload.Words = append(payload.Words, w.persisted())
	}
	return encodePersistedObjectPayload(payload)
}

func (*Identifier) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedIdentifierPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted identifier payload: %w", err)
	}
	id := &Identifier{Name: persisted.Name, Words: make([]*IdentifierWord, 0, len(persisted.Words))}
	for _, w := range persisted.Words {
		id.Words = append(id.Words, w.decode())
	}
	return id, nil
}

func (w *IdentifierWord) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if w == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted identifier word: nil word")
	}
	return encodePersistedObjectPayload(w.persisted())
}

func (*IdentifierWord) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedIdentifierWordPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted identifier word payload: %w", err)
	}
	return persisted.decode(), nil
}

func (t *NamingTerm) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if t == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted naming term: nil term")
	}
	return encodePersistedObjectPayload(persistedNamingTermPayload(*t))
}

func (*NamingTerm) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedNamingTermPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted naming term payload: %w", err)
	}
	return &NamingTerm{Spelling: persisted.Spelling, Capitalized: persisted.Capitalized}, nil
}
