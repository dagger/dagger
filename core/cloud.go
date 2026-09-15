package core

import "github.com/vektah/gqlparser/v2/ast"

type Cloud struct {
}

func (*Cloud) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Cloud",
		NonNull:   true,
	}
}

func (*Cloud) TypeDescription() string {
	return "Dagger Cloud configuration and state"
}
