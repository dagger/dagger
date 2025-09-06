package core

import "github.com/vektah/gqlparser/v2/ast"

type Address struct {
	Value string
}

func (*Address) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Address",
		NonNull:   true,
	}
}

func (*Address) TypeDescription() string {
	return `A standardized address to load containers, directories, secrets, and other object types. Address format depends on the type, and is validated at type selection.`
}
