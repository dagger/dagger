package resourceid

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/idproto"
	"github.com/dagger/graphql/language/ast"
	"github.com/opencontainers/go-digest"
	"google.golang.org/protobuf/proto"
)

func New[T any](typeName string) *ID[T] {
	return &ID[T]{idproto.New(typeName)}
}

func FromProto[T any](proto *idproto.ID) *ID[T] {
	return &ID[T]{proto}
}

// ID is a thin wrapper around *idproto.ID that is primed to expect a
// particular return type.
type ID[T any] struct {
	*idproto.ID
}

func (id *ID[T]) Literal() *idproto.Literal {
	return idproto.LiteralValue(id.ID)
}

func (id *ID[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.String())
}

func (id *ID[T]) UnmarshalJSON(p []byte) error {
	var str string
	if err := json.Unmarshal(p, &str); err != nil {
		return err
	}

	idp, err := Decode(str)
	if err != nil {
		return err
	}

	id.ID = idp

	return nil
}

func (id *ID[T]) ResourceTypeName() string {
	var t T
	name := fmt.Sprintf("%T", t)
	return strings.TrimPrefix(name, "*")
}

func DecodeID[T any](id string) (*ID[T], error) {
	if id == "" {
		// TODO(vito): this is a little awkward, can we avoid
		// it? adding initially for backwards compat, since some
		// places compare with empty string
		return nil, nil
	}
	idp, err := Decode(id)
	if err != nil {
		return nil, err
	}
	return FromProto[T](idp), nil
}

func Decode(id string) (*idproto.ID, error) {
	if id == "" {
		// TODO(vito): this is a little awkward, can we avoid
		// it? adding initially for backwards compat, since some
		// places compare with empty string
		return nil, nil
	}
	bytes, err := base64.URLEncoding.DecodeString(id)
	if err != nil {
		return nil, err
	}
	var idproto idproto.ID
	if err := proto.Unmarshal(bytes, &idproto); err != nil {
		return nil, err
	}
	return &idproto, nil
}

func (id *ID[T]) String() string {
	enc, err := encode(id.ID)
	if err != nil {
		panic(err)
	}
	return enc
}

func encode(idp *idproto.ID) (string, error) {
	proto, err := proto.Marshal(idp)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(proto), nil
}

type IDCache interface {
	Get(digest.Digest) (any, error)
	GetOrInitialize(digest.Digest, func() (any, error)) (any, error)
}

func GraphQLNode(id *idproto.ID) *ast.Field {
	if len(id.Constructor) == 0 {
		panic("TODO")
	}

	first, rest := id.Constructor[0], id.Constructor[1:]

	field := ast.NewField(nil)
	field.Name = ast.NewName(nil)
	field.Name.Value = first.Field
	field.Arguments = make([]*ast.Argument, len(first.Args))
	for i, arg := range first.Args {
		astArg := ast.NewArgument(nil)
		astArg.Name = ast.NewName(nil)
		astArg.Name.Value = arg.Name
		astArg.Value = GraphQLValue(arg.Value)
		field.Arguments[i] = astArg
	}

	if len(rest) > 0 {
		restIDP := id.Clone()
		restIDP.Constructor = rest

		field.SelectionSet = &ast.SelectionSet{
			Selections: []ast.Selection{
				GraphQLNode(restIDP),
			},
		}
	}

	return field
}

func GraphQLValue(lit *idproto.Literal) ast.Value {
	switch v := lit.Value.(type) {
	case *idproto.Literal_Id:
		val := ast.NewStringValue(nil)
		enc, err := encode(v.Id)
		if err != nil {
			panic(err)
		}
		val.Value = enc
		return val
	case *idproto.Literal_String_:
		val := ast.NewStringValue(nil)
		val.Value = v.String_
		return val
	case *idproto.Literal_Bool:
		val := ast.NewBooleanValue(nil)
		val.Value = v.Bool
		return val
	case *idproto.Literal_Int:
		val := ast.NewIntValue(nil)
		val.Value = fmt.Sprintf("%d", v.Int)
		return val
	case *idproto.Literal_Float:
		val := ast.NewFloatValue(nil)
		val.Value = fmt.Sprintf("%f", v.Float)
		return val
	case *idproto.Literal_Null:
		panic("TODO: null as value is not allowed; maybe remove this?")
	case *idproto.Literal_Enum:
		val := ast.NewEnumValue(nil)
		val.Value = v.Enum
		return val
	case *idproto.Literal_List:
		val := ast.NewListValue(nil)
		val.Values = make([]ast.Value, len(v.List.Values))
		for i, lit := range v.List.Values {
			val.Values[i] = GraphQLValue(lit)
		}
		return val
	case *idproto.Literal_Object:
		val := ast.NewObjectValue(nil)
		val.Fields = make([]*ast.ObjectField, len(v.Object.Values))
		for i, field := range v.Object.Values {
			val.Fields[i] = &ast.ObjectField{
				Name:  ast.NewName(nil),
				Value: GraphQLValue(field.Value),
			}
		}
		return val
	default:
		panic(fmt.Sprintf("unknown literal type %T", v))
	}
}
