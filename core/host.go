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

type HostResource struct {
	Address string
}

func (*HostResource) Type() *ast.Type {
	return &ast.Type{
		NamedType: "HostResource",
		NonNull:   true,
	}
}

func (*HostResource) TypeDescription() string {
	return `A resource to be loaded from the host using a standardized address.
May be converted to a directory, container, secret, file...`
}
