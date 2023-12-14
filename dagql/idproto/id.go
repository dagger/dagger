package idproto

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/protobuf/proto"
)

func New(gqlType *ast.Type) *ID {
	return &ID{
		Type: NewType(gqlType),
	}
}

func NewType(gqlType *ast.Type) *Type {
	t := &Type{
		NamedType: gqlType.NamedType,
		NonNull:   gqlType.NonNull,
	}
	if gqlType.Elem != nil {
		t.Elem = NewType(gqlType.Elem)
	}
	return t
}

func Arg(name string, value any) *Argument {
	return &Argument{
		Name:  name,
		Value: LiteralValue(value),
	}
}

type Literate interface {
	Literal() *Literal
}

func LiteralValue(value any) *Literal {
	switch v := value.(type) {
	case *ID:
		return &Literal{Value: &Literal_Id{Id: v}}
	case int:
		return &Literal{Value: &Literal_Int{Int: int64(v)}}
	case int32:
		return &Literal{Value: &Literal_Int{Int: int64(v)}}
	case int64:
		return &Literal{Value: &Literal_Int{Int: v}}
	case float32:
		return &Literal{Value: &Literal_Float{Float: float64(v)}}
	case float64:
		return &Literal{Value: &Literal_Float{Float: v}}
	case string:
		return &Literal{Value: &Literal_String_{String_: v}}
	case bool:
		return &Literal{Value: &Literal_Bool{Bool: v}}
	case []any:
		list := make([]*Literal, len(v))
		for i, val := range v {
			list[i] = LiteralValue(val)
		}
		return &Literal{Value: &Literal_List{List: &List{Values: list}}}
	case map[string]any:
		args := make([]*Argument, len(v))
		i := 0
		for name, val := range v {
			args[i] = &Argument{
				Name:  name,
				Value: LiteralValue(val),
			}
			i++
		}
		sort.SliceStable(args, func(i, j int) bool {
			return args[i].Name < args[j].Name
		})
		return &Literal{Value: &Literal_Object{Object: &Object{Values: args}}}
	case Literate:
		return v.Literal()
	case json.Number:
		if strings.Contains(v.String(), ".") {
			f, err := v.Float64()
			if err != nil {
				panic(err)
			}
			return LiteralValue(f)
		}
		i, err := v.Int64()
		if err != nil {
			panic(err)
		}
		return LiteralValue(i)
	default:
		panic(fmt.Sprintf("unsupported literal type %T", v))
	}
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
