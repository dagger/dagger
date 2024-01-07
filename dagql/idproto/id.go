package idproto

import (
	"bytes"
	"encoding/base64"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/protobuf/proto"
)

func New() *ID {
	// we start with nil so there's always a nil parent at the bottom
	return nil
}

func (id *ID) Path() string {
	buf := new(bytes.Buffer)
	if id.Parent != nil {
		fmt.Fprintf(buf, "%s.", id.Parent.Path())
	}
	fmt.Fprint(buf, id.DisplaySelf())
	return buf.String()
}

func (id *ID) DisplaySelf() string {
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s", id.Field)
	for ai, arg := range id.Args {
		if ai == 0 {
			fmt.Fprintf(buf, "(")
		} else {
			fmt.Fprintf(buf, ", ")
		}
		if id, ok := arg.Value.Value.(*Literal_Id); ok {
			fmt.Fprintf(buf, "%s: {%s}", arg.Name, id.Id.Display())
		} else if str, ok := arg.Value.Value.(*Literal_String_); ok {
			fmt.Fprintf(buf, "%s: %q", arg.Name, truncate(str.String_, 100))
		} else {
			fmt.Fprintf(buf, "%s: %s", arg.Name, arg.Value.ToAST().String())
		}
		if ai == len(id.Args)-1 {
			fmt.Fprintf(buf, ")")
		}
	}
	if id.Nth != 0 {
		fmt.Fprintf(buf, "#%d", id.Nth)
	}
	return buf.String()
}

func (id *ID) Display() string {
	return fmt.Sprintf("%s: %s", id.Path(), id.Type.ToAST())
}

func (id *ID) WithNth(i int) *ID {
	cp := id.Clone()
	cp.Nth = int64(i)
	return cp
}

func (id *ID) SelectNth(i int) {
	id.Nth = int64(i)
	id.Type = id.Type.Elem
}

func (id *ID) Append(ret *ast.Type, field string, args ...*Argument) *ID {
	var tainted bool
	for _, arg := range args {
		if arg.Tainted() {
			tainted = true
			break
		}
	}

	return &ID{
		Parent:  id,
		Type:    NewType(ret),
		Field:   field,
		Args:    args,
		Tainted: tainted,
	}
}

func (id *ID) Rebase(root *ID) *ID {
	cp := id.Clone()
	rebase(cp, root)
	return cp
}

func rebase(id *ID, root *ID) {
	if id.Parent == nil {
		id.Parent = root
	} else {
		rebase(id.Parent, root)
	}
}

func (id *ID) SetTainted(tainted bool) {
	id.Tainted = tainted
}

// Tainted returns true if the ID contains any tainted selectors.
func (id *ID) IsTainted() bool {
	if id.Tainted {
		return true
	}
	if id.Parent != nil {
		return id.Parent.IsTainted()
	}
	return false
}

// Canonical returns the ID with any contained IDs canonicalized.
func (id *ID) Canonical() *ID {
	if id.Meta {
		return id.Parent.Canonical()
	}
	canon := id.Clone()
	if id.Parent != nil {
		canon.Parent = id.Parent.Canonical()
	}
	// TODO sort args...? is it worth preserving them in the first place? (default answer no)
	for i, arg := range canon.Args {
		canon.Args[i] = arg.Canonical()
	}
	return canon
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
		return fmt.Errorf("cannot decode ID from %q: %w", str, err)
	}
	return proto.Unmarshal(bytes, id)
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
		return v.Id.IsTainted()
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

func truncate(s string, length int) string {
	if len(s) <= length {
		return s
	}

	if length < 5 {
		return s[:length]
	}

	dig := digest.FromString(s)
	prefixLength := (length - 3) / 2
	suffixLength := length - 3 - prefixLength
	abbrev := s[:prefixLength] + "..." + s[len(s)-suffixLength:]
	return fmt.Sprintf("%s:%d:%s", dig, len(s), abbrev)
}
