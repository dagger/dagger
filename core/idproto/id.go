package idproto

import (
	"github.com/opencontainers/go-digest"
	"google.golang.org/protobuf/proto"
)

// Tainted returns true if the ID contains any tainted selectors.
func (id *ID) Tainted() bool {
	for _, sel := range id.Constructor {
		if sel.Tainted {
			return true
		}
	}
	return false
}

// Canonical returns the ID with any contained IDs canonicalized.
func (id *ID) Canonical() *ID {
	noMeta := []*Selector{}
	for _, sel := range id.Constructor {
		if !sel.Meta {
			noMeta = append(noMeta, sel.Canonical())
		}
	}
	return &ID{
		TypeName:    id.TypeName,
		Constructor: noMeta,
	}
}

// Digest returns the digest of the encoded ID. It does NOT canonicalize the ID
// first.
func (id *ID) Digest() (digest.Digest, error) {
	bytes, err := proto.Marshal(id)
	if err != nil {
		return "", err
	}
	return digest.FromBytes(bytes), nil
}

// Canonical returns the selector with any contained IDs canonicalized.
func (sel *Selector) Canonical() *Selector {
	cp := proto.Clone(sel).(*Selector)
	for i := range cp.Args {
		cp.Args[i] = cp.Args[i].Canonical()
	}
	return sel
}

// Canonical returns the literal with any contained IDs canonicalized.
func (lit *Literal) Canonical() *Literal {
	switch v := lit.Value.(type) {
	case *Literal_Id:
		return &Literal{Value: &Literal_Id{Id: v.Id.Canonical()}}
	case *Literal_List:
		list := make([]*Literal, len(v.List.Values))
		for i, val := range v.List.Values {
			list[i] = val.Canonical()
		}
		return &Literal{Value: &Literal_List{List: &List{Values: list}}}
	case *Literal_Object:
		args := make([]*Argument, len(v.Object.Values))
		for i, arg := range v.Object.Values {
			args[i] = arg.Canonical()
		}
		return &Literal{Value: &Literal_Object{Object: &Object{Values: args}}}
	default:
		return lit
	}
}

// Canonical returns the argument with any contained IDs canonicalized.
func (arg *Argument) Canonical() *Argument {
	return &Argument{
		Name:  arg.Name,
		Value: arg.GetValue().Canonical(),
	}
}
