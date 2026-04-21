package core

import "github.com/vektah/gqlparser/v2/ast"

// Volume is an opaque handle to an engine-managed filesystem resource (e.g.
// an sshfs mount) that can be bind-mounted into containers via
// Container.WithVolumeMount. The engine owns the lifecycle of the backing
// resource; the Volume value only carries the identifiers needed to locate it.
type Volume struct {
	// ID identifies the mount to the engine. The server uses this to look up
	// the backing resource (e.g. to refcount / release it).
	ID string

	// MountPath is the absolute path on the engine host where the backing
	// resource is mounted. At exec time this is the Source of the
	// engine-side HostMount that realizes the VolumeMount.
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
