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

func (id *ID) Inputs() ([]digest.Digest, error) {
	seen := map[digest.Digest]struct{}{}
	var inputs []digest.Digest
	for _, arg := range id.Args {
		ins, err := arg.Value.Inputs()
		if err != nil {
			return nil, err
		}
		for _, in := range ins {
			if _, ok := seen[in]; ok {
				continue
			}
			seen[in] = struct{}{}
			inputs = append(inputs, in)
		}
	}
	return inputs, nil
}

func (lit *Literal) Inputs() ([]digest.Digest, error) {
	switch x := lit.Value.(type) {
	case *Literal_Id:
		dig, err := x.Id.Digest()
		if err != nil {
			return nil, err
		}
		return []digest.Digest{dig}, nil
	case *Literal_List:
		var inputs []digest.Digest
		for _, v := range x.List.Values {
			ins, err := v.Inputs()
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, ins...)
		}
		return inputs, nil
	case *Literal_Object:
		var inputs []digest.Digest
		for _, v := range x.Object.Values {
			ins, err := v.Value.Inputs()
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, ins...)
		}
		return inputs, nil
	default:
		return nil, nil
	}
}

func (id *ID) Modules() []*ID {
	allMods := []*ID{}
	for id != nil {
		if id.Module != nil {
			allMods = append(allMods, id.Module)
		}
		for _, arg := range id.Args {
			allMods = append(allMods, arg.Value.Modules()...)
		}
		id = id.Parent
	}
	seen := map[digest.Digest]struct{}{}
	deduped := []*ID{}
	for _, mod := range allMods {
		dig, err := mod.Digest()
		if err != nil {
			panic(err)
		}
		if _, ok := seen[dig]; ok {
			continue
		}
		seen[dig] = struct{}{}
		deduped = append(deduped, mod)
	}
	return deduped
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
		fmt.Fprintf(buf, "%s: %s", arg.Name, arg.Value.Display())
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
	return base64.URLEncoding.EncodeToString(proto), nil
}

func (id *ID) Decode(str string) error {
	bytes, err := base64.URLEncoding.DecodeString(str)
	if err != nil {
		return fmt.Errorf("cannot decode ID from %q: %w", str, err)
	}
	return proto.Unmarshal(bytes, id)
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
