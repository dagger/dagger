package core

import "github.com/vektah/gqlparser/v2/ast"

// Volume represents an engine-managed volume (e.g., an sshfs mount)
// exposed to the DAG as an opaque object.
type Volume struct {
	ID        string
	MountPath string
}

func (*Volume) Type() *ast.Type {
	return &ast.Type{NamedType: "Volume", NonNull: true}
}

func (*Volume) TypeDescription() string {
	return "A reference to an engine-managed volume."
}

func (v *Volume) Clone() *Volume {
	cp := *v
	return &cp
}
