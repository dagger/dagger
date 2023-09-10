package idproto

import (
	"capnproto.org/go/capnp/v3"
	"github.com/opencontainers/go-digest"
)

// Tainted returns true if the ID contains any tainted selectors.
func (id ID) Tainted() (bool, error) {
	cons, err := id.Constructor()
	if err != nil {
		return false, err
	}
	for i := 0; i < cons.Len(); i++ {
		sel := cons.At(i)
		if sel.Tainted() {
			return true, nil
		}
	}
	return false, nil
}

// Canonical returns a new ID with all meta selectors removed.
func (id ID) Canonical() (canon ID, err error) {
	// TODO: is this meant to be global?
	arena := capnp.SingleSegment(nil)

	_, seg, err := capnp.NewMessage(arena)
	if err != nil {
		return canon, err
	}

	canon, err = NewRootID(seg)
	if err != nil {
		return canon, err
	}

	cons, err := id.Constructor()
	if err != nil {
		return canon, err
	}

	noMeta := []Selector{}
	for i := 0; i < cons.Len(); i++ {
		sel := cons.At(i)
		if !sel.Meta() {
			noMeta = append(noMeta, sel)
		}
	}

	noMetaCons, err := canon.NewConstructor(int32(len(noMeta)))
	if err != nil {
		return canon, err
	}

	for i, sel := range noMeta {
		if err := noMetaCons.Set(i, sel); err != nil {
			return canon, err
		}
	}

	return canon, nil
}

// Digest returns the digest of the ID.
func (id ID) Digest() (digest.Digest, error) {
	bytes, err := id.Message().Marshal()
	if err != nil {
		return "", err
	}
	return digest.FromBytes(bytes), nil
}
