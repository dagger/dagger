package call

import (
	"fmt"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

type Module struct {
	pb *callpbv1.Module
	id *ID
}

func NewModule(id *ID, name, ref string) *Module {
	return &Module{
		pb: &callpbv1.Module{
			Name:       name,
			Ref:        ref,
			CallDigest: id.pb.Digest,
		},
		id: id,
	}
}

func (m *Module) ID() *ID {
	return m.id
}

func (m *Module) gatherCalls(callsByDigest map[string]*callpbv1.Call) {
	if m == nil {
		return
	}
	m.id.gatherCalls(callsByDigest)
}

func (m *Module) decode(
	pb *callpbv1.Module,
	callsByDigest map[string]*callpbv1.Call,
	memo map[string]*ID,
) error {
	if pb == nil {
		return nil
	}
	m.pb = pb

	if pb.CallDigest != "" {
		m.id = new(ID)
		if err := m.id.decode(pb.CallDigest, callsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode module Call: %w", err)
		}
	}
	return nil
}
