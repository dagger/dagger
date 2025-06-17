package core

import (
	"github.com/vektah/gqlparser/v2/ast"
)

type Host struct {
}

func (*Host) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Host",
		NonNull:   true,
	}
}

func (*Host) TypeDescription() string {
	return "Information about the host environment."
}
