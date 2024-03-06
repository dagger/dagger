package idproto

import (
	"fmt"
)

type Module struct {
	raw *RawModule
	id  *ID
}

func NewModule(id *ID, name, ref string) *Module {
	return &Module{
		raw: &RawModule{
			Name:     name,
			Ref:      ref,
			IdDigest: id.raw.Digest,
		},
		id: id,
	}
}

func (m *Module) ID() *ID {
	return m.id
}

func (m *Module) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	if m == nil {
		return
	}
	m.id.gatherIDs(idsByDigest)
}

func (m *Module) decode(
	raw *RawModule,
	idsByDigest map[string]*RawID_Fields,
	memo map[string]*idState,
) error {
	if raw == nil {
		return nil
	}
	m.raw = raw

	if raw.IdDigest != "" {
		m.id = new(ID)
		if err := m.id.decode(raw.IdDigest, idsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode module ID: %w", err)
		}
	}
	return nil
}
