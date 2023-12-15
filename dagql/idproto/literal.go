package idproto

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

type Literate interface {
	ToLiteral() *Literal
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
		return v.ToLiteral()
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
	case nil:
		return &Literal{Value: &Literal_Null{Null: true}}
	default:
		panic(fmt.Sprintf("unsupported literal type %T", v))
	}
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
