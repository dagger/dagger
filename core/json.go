package core

import (
	"encoding/json"
	"fmt"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type JSON json.RawMessage

func init() {
	strcase.ConfigureAcronym("JSON", "JSON")
}

func (p JSON) String() string {
	return string(p)
}

func (p JSON) Bytes() []byte {
	return p
}

var _ dagql.Typed = JSON{}

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
	if string(bs) == "null" {
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
	default:
		return nil, fmt.Errorf("cannot convert %T to JSON", val)
	}
}
