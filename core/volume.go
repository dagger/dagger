package core

import "github.com/vektah/gqlparser/v2/ast"

// Volume is an opaque handle to an engine-managed filesystem resource
// (e.g. an sshfs mount) that can be bind-mounted into containers.
type Volume struct {
	ID        string
	MountPath string
}

func (*Volume) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Volume",
		NonNull:   true,
	}
}

func (*Volume) TypeDescription() string {
	return "A reference to an engine-managed volume."
}
