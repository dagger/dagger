package core

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type JSON json.RawMessage

func init() {
	// Only affects the legacy strcase rules (modules older than
	// IdentifierNamingVersion); see the acronyms in llm.go.
	strcase.ConfigureAcronym("JSON", "JSON")
}

func (p JSON) String() string {
	return string(p)
}

func (p JSON) Bytes() []byte {
	return p
}

var _ dagql.Typed = JSON{}

var _ dagql.CachePayloadSizer = JSON{}

func (p JSON) CachePayloadBytes() int64 {
	return int64(len(p))
}

func (p JSON) TypeName() string {
	return "JSON"
}

func (p JSON) TypeDescription() string {
	return "An arbitrary JSON-encoded value."
}

func (p JSON) Type() *ast.Type {
	return &ast.Type{
		NamedType: p.TypeName(),
		NonNull:   true,
	}
}

var _ dagql.Input = JSON("")

func (p JSON) Decoder() dagql.InputDecoder {
	return p
}

func (p JSON) ToLiteral() call.Literal {
	return call.NewLiteralString(string(p))
}

func (p JSON) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	// The SDKs expect a string, not direct JSON, so marshal to that
	return json.Marshal(string(p))
}

func (p *JSON) UnmarshalJSON(bs []byte) error {
	if p == nil {
		return fmt.Errorf("cannot unmarshal into nil JSON")
	}
	if bytes.Equal(bs, []byte("null")) {
		*p = nil
		return nil
	}
	// mirroring MarshalJSON, unmarshal to a *string*
	var s string
	if err := json.Unmarshal(bs, &s); err != nil {
		return err
	}
	*p = JSON(s)
	return nil
}

var _ dagql.ScalarType = JSON{}

// DecodeInput accepts JSON text (a string, []byte or json.RawMessage) or a
// raw JSON value as GraphQL clients send it: an object or list literal, a
// number or a boolean, or the same in a variable. Raw values are stored as
// their compact JSON text, so that they produce the same literal (and so the
// same ID and cache key) as the equivalent string input.
//
// A string is always taken as JSON text, never as a JSON string value.
func (JSON) DecodeInput(val any) (res dagql.Input, err error) {
	switch x := val.(type) {
	case string:
		if x == "" {
			return nil, nil
		}
		return JSON(x), nil
	case []byte:
		return JSON(x), nil
	case json.RawMessage:
		return JSON(x), nil
	case JSON:
		return x, nil
	case map[string]any, []any, bool, float64, float32, int, int32, int64, json.Number:
		text, encErr := marshalJSONInput(x)
		if encErr != nil {
			return nil, encErr
		}
		return text, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to JSON", val)
	}
}

// marshalJSONInput encodes a raw JSON input value as compact JSON text.
// Object keys come out sorted, as Go maps have no order.
func marshalJSONInput(val any) (JSON, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// keep "<", ">" and "&" as they are, like the equivalent string input
	enc.SetEscapeHTML(false)
	if err := enc.Encode(emptyListsNonNil(val)); err != nil {
		return nil, fmt.Errorf("cannot convert %T to JSON: %w", val, err)
	}
	return JSON(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// emptyListsNonNil replaces nil lists in a raw JSON value with empty ones:
// the GraphQL parser decodes an empty list literal, at any depth, to a nil
// slice, which would otherwise encode as null rather than [].
func emptyListsNonNil(val any) any {
	switch x := val.(type) {
	case []any:
		list := make([]any, len(x))
		for i, elem := range x {
			list[i] = emptyListsNonNil(elem)
		}
		return list
	case map[string]any:
		obj := make(map[string]any, len(x))
		for k, v := range x {
			obj[k] = emptyListsNonNil(v)
		}
		return obj
	default:
		return val
	}
}

// Validate checks if the JSON is valid
func (p JSON) Validate() error {
	if !json.Valid([]byte(p)) {
		return fmt.Errorf("invalid JSON")
	}
	return nil
}
