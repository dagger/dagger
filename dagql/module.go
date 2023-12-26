package dagql

import (
	"context"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
)

// A Module is a Server with a name and an ID, acting as an Object.
//
// All selections made against it are forwarded to the Server.
//
// All IDs that come out of it are rebased to the module's ID.
//
// A Module is created by installing a schema on a Server.
type Module struct {
	srv *Server
	id  *idproto.ID
}

var _ Object = (*Module)(nil)

func NewModule(srv *Server, id *idproto.ID) *Module {
	return &Module{
		srv: srv,
		id:  id,
	}
}

func InstallModule(dest *Server, name string, srv *Server) {
	root, ok := dest.ObjectType(dest.Root().Type().Name())
	if !ok {
		panic("root type not found")
	}
	root.Extend(
		FieldSpec{
			Name: name,
			Type: srv.root,
		},
		func(ctx context.Context, self Object, args map[string]Typed) (Typed, error) {
			return NewModule(srv, CurrentID(ctx)), nil
		},
	)
	for _, cls := range srv.objects {
		dest.InstallObject(cls)
	}
	// for name, t := range srv.scalars {
	// 	s.scalars[name] = t
	// }
	// for name, t := range srv.inputs {
	// 	s.inputs[name] = t
	// }
	// s.scalars[scalar.TypeName()] = scalar
}

func (m *Module) Type() *ast.Type {
	return m.srv.Root().Type()
}

func (m *Module) ObjectType() ObjectType {
	return m.srv.Root().ObjectType()
}

func (m *Module) ID() *idproto.ID {
	return m.id
}

func (m *Module) IDFor(ctx context.Context, sel Selector) (*idproto.ID, error) {
	subID, err := m.srv.Root().IDFor(ctx, sel)
	if err != nil {
		return nil, err
	}
	return subID.Rebase(m.id), nil
}

func (m *Module) Select(ctx context.Context, sel Selector) (val Typed, err error) {
	return m.srv.Root().Select(ctx, sel)
}
