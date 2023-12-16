package idproto

import (
	"bytes"
	"encoding/base64"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/protobuf/proto"
)

func New(gqlType *ast.Type) *ID {
	return &ID{
		Type: NewType(gqlType),
	}
}

func (id *ID) Display() string {
	buf := new(bytes.Buffer)
	for si, sel := range id.Constructor {
		if si > 0 {
			fmt.Fprintf(buf, ".")
		}
		fmt.Fprintf(buf, "%s", sel.Field)
		for ai, arg := range sel.Args {
			if ai == 0 {
				fmt.Fprintf(buf, "(")
			} else {
				fmt.Fprintf(buf, ", ")
			}
			fmt.Fprintf(buf, "%s: %s", arg.Name, arg.Value.ToAST())
			if ai == len(sel.Args)-1 {
				fmt.Fprintf(buf, ")")
			}
		}
		if sel.Nth != 0 {
			fmt.Fprintf(buf, "#%d", sel.Nth)
		}
	}
	fmt.Fprintf(buf, ": %s", id.Type.ToAST())
	return buf.String()
}

func (id *ID) Nth(i int) *ID {
	cp := id.Clone()
	cp.Constructor[len(cp.Constructor)-1].Nth = int64(i)
	cp.Type = cp.Type.Elem
	return cp
}

func (id *ID) Append(field string, args ...*Argument) {
	var tainted bool
	for _, arg := range args {
		if arg.Tainted() {
			tainted = true
			break
		}
	}

	id.Constructor = append(id.Constructor, &Selector{
		Field:   field,
		Args:    args,
		Tainted: tainted,
	})
}

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
	// TODO sort args...? is it worth preserving them in the first place? (default answer no)
	noMeta := []*Selector{}
	for _, sel := range id.Constructor {
		if !sel.Meta {
			noMeta = append(noMeta, sel.Canonical())
		}
	}
	return &ID{
		Type:        id.Type,
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

func (id *ID) Clone() *ID {
	return proto.Clone(id).(*ID)
}

func (id *ID) Encode() (string, error) {
	proto, err := proto.Marshal(id)
	if err != nil {
		return "", err
	}
	enc := base64.URLEncoding.EncodeToString(proto)
	return enc, nil
}

func (id *ID) Decode(str string) error {
	bytes, err := base64.URLEncoding.DecodeString(str)
	if err != nil {
		return err
	}
	return proto.Unmarshal(bytes, id)
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

// Tainted returns true if the ID contains any tainted selectors.
func (arg *Argument) Tainted() bool {
	return arg.GetValue().Tainted()
}

func (lit *Literal) Tainted() bool {
	switch v := lit.Value.(type) {
	case *Literal_Id:
		return v.Id.Tainted()
	case *Literal_List:
		for _, val := range v.List.Values {
			if val.Tainted() {
				return true
			}
		}
		return false
	case *Literal_Object:
		for _, arg := range v.Object.Values {
			if arg.Tainted() {
				return true
			}
		}
		return false
	default:
		return false
	}
}
