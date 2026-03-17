package core

import "github.com/vektah/gqlparser/v2/ast"

// JSONValue is a simple state carrier for JSON-encoded bytes
type JSONValue struct {
	Data []byte
}

func (*JSONValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "JSONValue",
		NonNull:   true,
	}
}
