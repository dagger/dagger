package idproto

import (
	"fmt"

	"github.com/opencontainers/go-digest"
)

type Module struct {
	raw *RawModule
	id  *ID
}

func NewModule(id *ID, name, ref string) *Module {
	return &Module{
		raw: &RawModule{
			Name: name,
			Ref:  ref,
		},
		id: id,
	}
}

func (m *Module) ID() *ID {
	return m.id
}

func (m *Module) clone(memo map[digest.Digest]*ID) (*Module, error) {
	if m == nil {
		return nil, nil
	}

	newMod := &Module{
		raw: &RawModule{
			Name: m.raw.Name,
			Ref:  m.raw.Ref,
		},
	}
	var err error
	newMod.id, err = m.id.clone(memo)
	if err != nil {
		return nil, fmt.Errorf("failed to clone module ID: %w", err)
	}
	return newMod, nil
}

func (m *Module) encode(idsByDigest map[string]*RawID_Fields) (*RawModule, error) {
	if m == nil {
		return nil, nil
	}

	var err error
	m.raw.IdDigest, err = m.id.encode(idsByDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to encode module ID: %w", err)
	}
	return m.raw, nil
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
