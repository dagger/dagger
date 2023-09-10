package idproto

import (
	"github.com/opencontainers/go-digest"
	"google.golang.org/protobuf/proto"
)

func (id *ID) Tainted() bool {
	for _, sel := range id.Constructor {
		if sel.Tainted {
			return true
		}
	}
	return false
}

func (id *ID) Canonical() *ID {
	noMeta := []*Selector{}
	for _, sel := range id.Constructor {
		if !sel.Meta {
			noMeta = append(noMeta, sel)
		}
	}
	return &ID{
		TypeName:    id.TypeName,
		Constructor: noMeta,
	}
}

func (id *ID) Digest() (digest.Digest, error) {
	bytes, err := proto.Marshal(id)
	if err != nil {
		return "", err
	}
	return digest.FromBytes(bytes), nil
}
