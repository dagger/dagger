package core

import (
	"fmt"

	"github.com/iancoleman/strcase"
	pelletier "github.com/pelletier/go-toml"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type TOML string

func init() {
	strcase.ConfigureAcronym("TOML", "TOML")
}

func (p TOML) String() string {
	return string(p)
}

func (p TOML) Bytes() []byte {
	return []byte(p)
}

var _ dagql.Typed = TOML("")

func (p TOML) TypeName() string {
	return "TOML"
}

func (p TOML) TypeDescription() string {
	return "An arbitrary TOML-encoded value."
}

func (p TOML) Type() *ast.Type {
	return &ast.Type{
		NamedType: p.TypeName(),
		NonNull:   true,
	}
}

var _ dagql.Input = TOML("")

func (p TOML) Decoder() dagql.InputDecoder {
	return p
}

func (p TOML) ToLiteral() call.Literal {
	return call.NewLiteralString(string(p))
}

var _ dagql.ScalarType = TOML("")

func (TOML) DecodeInput(val any) (res dagql.Input, err error) {
	switch x := val.(type) {
	case string:
		return TOML(x), nil
	case []byte:
		return TOML(x), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to TOML", val)
	}
}

// Validate checks if the TOML is valid
func (p TOML) Validate() error {
	if _, err := pelletier.Load(string(p)); err != nil {
		return fmt.Errorf("invalid TOML: %w", err)
	}
	return nil
}
