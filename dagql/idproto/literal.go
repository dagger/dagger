package idproto

import (
	"fmt"
	"strconv"

	"github.com/vektah/gqlparser/v2/ast"
)

type Literate interface {
	ToLiteral() *Literal
}

func (lit *Literal) ToAST() *ast.Value {
	switch x := lit.GetValue().(type) {
	case *Literal_Id:
		enc, err := x.Id.Encode()
		if err != nil {
			panic(err)
		}
		return &ast.Value{
			Raw:  enc,
			Kind: ast.StringValue,
		}
	case *Literal_Null:
		return &ast.Value{
			Raw:  "null",
			Kind: ast.NullValue,
		}
	case *Literal_Bool:
		return &ast.Value{
			Raw:  strconv.FormatBool(x.Bool),
			Kind: ast.BooleanValue,
		}
	case *Literal_Enum:
		return &ast.Value{
			Raw:  x.Enum,
			Kind: ast.EnumValue,
		}
	case *Literal_Int:
		return &ast.Value{
			Raw:  fmt.Sprintf("%d", x.Int),
			Kind: ast.IntValue,
		}
	case *Literal_Float:
		return &ast.Value{
			Raw:  strconv.FormatFloat(x.Float, 'f', -1, 64),
			Kind: ast.FloatValue,
		}
	case *Literal_String_:
		return &ast.Value{
			Raw:  x.String_,
			Kind: ast.StringValue,
		}
	case *Literal_List:
		list := &ast.Value{
			Kind: ast.ListValue,
		}
		for _, val := range x.List.Values {
			list.Children = append(list.Children, &ast.ChildValue{
				Value: val.ToAST(),
			})
		}
		return list
	case *Literal_Object:
		obj := &ast.Value{
			Kind: ast.ObjectValue,
		}
		for _, field := range x.Object.Values {
			obj.Children = append(obj.Children, &ast.ChildValue{
				Name:  field.Name,
				Value: field.Value.ToAST(),
			})
		}
		return obj
	default:
		panic(fmt.Sprintf("unsupported literal type %T", x))
	}
}
