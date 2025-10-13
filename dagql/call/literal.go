package call

import (
	"fmt"
	"iter"
	"strconv"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

type Literate interface {
	ToLiteral() Literal
}

type Literal interface {
	Inputs() ([]digest.Digest, error)
	Modules() []*Module
	Display() string
	ToInput() any
	ToAST() *ast.Value

	pb() *callpbv1.Literal
	gatherCalls(map[string]*callpbv1.Call)
}

// ToLiteral converts any JSON-serializable value to a Literal.
func ToLiteral(val any) (Literal, error) {
	switch v := val.(type) {
	case string:
		return NewLiteralString(v), nil
	case bool:
		return NewLiteralBool(v), nil
	case int64:
		return NewLiteralInt(v), nil
	case float64:
		return NewLiteralFloat(v), nil
	case []any:
		list := make([]Literal, len(v))
		for i, val := range v {
			lit, err := ToLiteral(val)
			if err != nil {
				return nil, err
			}
			list[i] = lit
		}
		return NewLiteralList(list...), nil
	case map[string]any:
		args := make([]*Argument, 0, len(v))
		for k, val := range v {
			lit, err := ToLiteral(val)
			if err != nil {
				return nil, err
			}
			args = append(args, NewArgument(k, lit, false))
		}
		return NewLiteralObject(args...), nil
	case *ID:
		return NewLiteralID(v), nil
	default:
		return nil, fmt.Errorf("unknown literal value type %T", v)
	}
}

type LiteralID struct {
	id *ID
}

func NewLiteralID(id *ID) *LiteralID {
	return &LiteralID{id: id}
}

func (lit *LiteralID) Value() *ID {
	return lit.id
}

func (lit *LiteralID) Inputs() ([]digest.Digest, error) {
	return []digest.Digest{lit.id.Digest()}, nil
}

func (lit *LiteralID) Modules() []*Module {
	return lit.id.Modules()
}

func (lit *LiteralID) Display() string {
	return fmt.Sprintf("{%s}", lit.id.Display())
}

func (lit *LiteralID) ToInput() any {
	return lit.id
}

func (lit *LiteralID) ToAST() *ast.Value {
	enc, err := lit.id.Encode()
	if err != nil {
		panic(err)
	}
	return &ast.Value{
		Raw:  enc,
		Kind: ast.StringValue,
	}
}

func (lit *LiteralID) pb() *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: lit.id.pb.Digest}}
}

func (lit *LiteralID) gatherCalls(callsByDigest map[string]*callpbv1.Call) {
	lit.id.gatherCalls(callsByDigest)
}

type LiteralList struct {
	values []Literal
}

func NewLiteralList(values ...Literal) *LiteralList {
	return &LiteralList{values: values}
}

func (lit *LiteralList) Values() iter.Seq2[int, Literal] {
	return func(yield func(int, Literal) bool) {
		for i, v := range lit.values {
			if !yield(i, v) {
				return
			}
		}
	}
}

func (lit *LiteralList) Len() int {
	return len(lit.values)
}

func (lit *LiteralList) Inputs() ([]digest.Digest, error) {
	var inputs []digest.Digest
	for _, v := range lit.values {
		ins, err := v.Inputs()
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, ins...)
	}
	return inputs, nil
}

func (lit *LiteralList) Modules() []*Module {
	mods := []*Module{}
	for _, val := range lit.values {
		mods = append(mods, val.Modules()...)
	}
	return mods
}

func (lit *LiteralList) Display() string {
	list := "["
	for i, val := range lit.values {
		if i > 0 {
			list += ","
		}
		list += val.Display()
	}
	list += "]"
	return list
}

func (lit *LiteralList) ToInput() any {
	list := make([]any, len(lit.values))
	for i, val := range lit.values {
		list[i] = val.ToInput()
	}
	return list
}

func (lit *LiteralList) ToAST() *ast.Value {
	list := &ast.Value{
		Kind: ast.ListValue,
	}
	for _, val := range lit.values {
		list.Children = append(list.Children, &ast.ChildValue{
			Value: val.ToAST(),
		})
	}
	return list
}

func (lit *LiteralList) pb() *callpbv1.Literal {
	list := make([]*callpbv1.Literal, len(lit.values))
	for i, val := range lit.values {
		list[i] = val.pb()
	}
	return &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{Values: list}}}
}

func (lit *LiteralList) gatherCalls(callsByDigest map[string]*callpbv1.Call) {
	for _, val := range lit.values {
		val.gatherCalls(callsByDigest)
	}
}

type LiteralObject struct {
	values []*Argument
}

func NewLiteralObject(values ...*Argument) *LiteralObject {
	return &LiteralObject{values: values}
}

func (lit *LiteralObject) Args() iter.Seq2[int, *Argument] {
	return func(yield func(int, *Argument) bool) {
		for i, v := range lit.values {
			if !yield(i, v) {
				return
			}
		}
	}
}

func (lit *LiteralObject) Len() int {
	return len(lit.values)
}

func (lit *LiteralObject) Inputs() ([]digest.Digest, error) {
	var inputs []digest.Digest
	for _, v := range lit.values {
		ins, err := v.value.Inputs()
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, ins...)
	}
	return inputs, nil
}

func (lit *LiteralObject) Modules() []*Module {
	mods := []*Module{}
	for _, arg := range lit.values {
		mods = append(mods, arg.value.Modules()...)
	}
	return mods
}

func (lit *LiteralObject) Display() string {
	obj := "{"
	for i, field := range lit.values {
		if i > 0 {
			obj += ","
		}
		obj += field.pb.Name + ": " + field.value.Display()
	}
	obj += "}"
	return obj
}

func (lit *LiteralObject) ToInput() any {
	obj := make(map[string]any, len(lit.values))
	for _, field := range lit.values {
		obj[field.pb.Name] = field.value.ToInput()
	}
	return obj
}

func (lit *LiteralObject) ToAST() *ast.Value {
	obj := &ast.Value{
		Kind: ast.ObjectValue,
	}
	for _, field := range lit.values {
		obj.Children = append(obj.Children, &ast.ChildValue{
			Name:  field.pb.Name,
			Value: field.value.ToAST(),
		})
	}
	return obj
}

func (lit *LiteralObject) pb() *callpbv1.Literal {
	args := make([]*callpbv1.Argument, len(lit.values))
	for i, val := range lit.values {
		args[i] = val.pb
	}
	return &callpbv1.Literal{Value: &callpbv1.Literal_Object{Object: &callpbv1.Object{Values: args}}}
}

func (lit *LiteralObject) gatherCalls(callsByDigest map[string]*callpbv1.Call) {
	for _, val := range lit.values {
		val.gatherCalls(callsByDigest)
	}
}

type LiteralBool = LiteralPrimitiveType[bool, *callpbv1.Literal_Bool]

func NewLiteralBool(val bool) *LiteralBool {
	return &LiteralBool{&callpbv1.Literal_Bool{Bool: val}}
}

type LiteralEnum = LiteralPrimitiveType[string, *callpbv1.Literal_Enum]

func NewLiteralEnum(val string) *LiteralEnum {
	return &LiteralEnum{&callpbv1.Literal_Enum{Enum: val}}
}

type LiteralInt = LiteralPrimitiveType[int64, *callpbv1.Literal_Int]

func NewLiteralInt(val int64) *LiteralInt {
	return &LiteralInt{&callpbv1.Literal_Int{Int: val}}
}

type LiteralFloat = LiteralPrimitiveType[float64, *callpbv1.Literal_Float]

func NewLiteralFloat(val float64) *LiteralFloat {
	return &LiteralFloat{&callpbv1.Literal_Float{Float: val}}
}

type LiteralString = LiteralPrimitiveType[string, *callpbv1.Literal_String_]

func NewLiteralString(val string) *LiteralString {
	return &LiteralString{&callpbv1.Literal_String_{String_: val}}
}

type LiteralNull = LiteralPrimitiveType[any, *callpbv1.Literal_Null]

func NewLiteralNull() *LiteralNull {
	return &LiteralNull{&callpbv1.Literal_Null{Null: true}}
}

type LiteralPrimitiveType[T comparable, V callpbv1.LiteralValue[T]] struct {
	pbVal V
}

func (lit *LiteralPrimitiveType[T, V]) Value() T {
	return lit.pbVal.Value()
}

func (lit *LiteralPrimitiveType[T, V]) Inputs() ([]digest.Digest, error) {
	return nil, nil
}

func (lit *LiteralPrimitiveType[T, V]) Modules() []*Module {
	return nil
}

func (lit *LiteralPrimitiveType[T, V]) Display() string {
	if lit.pbVal.ASTKind() == ast.StringValue {
		var val any = lit.pbVal.Value()
		return strconv.Quote(val.(string))
	}
	return fmt.Sprintf("%v", lit.pbVal.Value())
}

func (lit *LiteralPrimitiveType[T, V]) ToInput() any {
	return lit.pbVal.Value()
}

func (lit *LiteralPrimitiveType[T, V]) ToAST() *ast.Value {
	kind := lit.pbVal.ASTKind()
	var raw string
	if kind == ast.NullValue {
		// this teeny kludge allows us to use LiteralPrimitiveType with Literal_Null
		// otherwise the raw Value would show up as "<nil>"
		raw = "null"
	} else {
		raw = fmt.Sprintf("%v", lit.pbVal.Value())
	}
	return &ast.Value{
		Raw:  raw,
		Kind: kind,
	}
}

func (lit *LiteralPrimitiveType[T, V]) pb() *callpbv1.Literal {
	return &callpbv1.Literal{Value: lit.pbVal}
}

func (lit *LiteralPrimitiveType[T, V]) gatherCalls(_ map[string]*callpbv1.Call) {}

func decodeLiteral(
	pb *callpbv1.Literal,
	callsByDigest map[string]*callpbv1.Call,
	memo map[string]*ID,
) (Literal, error) {
	if pb == nil {
		return nil, nil
	}
	switch v := pb.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		if v.CallDigest == "" {
			return nil, nil
		}
		call := new(ID)
		if err := call.decode(v.CallDigest, callsByDigest, memo); err != nil {
			return nil, fmt.Errorf("failed to decode literal Call: %w", err)
		}
		return NewLiteralID(call), nil
	case *callpbv1.Literal_Null:
		return NewLiteralNull(), nil
	case *callpbv1.Literal_Bool:
		return NewLiteralBool(v.Bool), nil
	case *callpbv1.Literal_Enum:
		return NewLiteralEnum(v.Enum), nil
	case *callpbv1.Literal_Int:
		return NewLiteralInt(v.Int), nil
	case *callpbv1.Literal_Float:
		return NewLiteralFloat(v.Float), nil
	case *callpbv1.Literal_String_:
		return NewLiteralString(v.String_), nil
	case *callpbv1.Literal_List:
		list := make([]Literal, 0, len(v.List.Values))
		for _, val := range v.List.Values {
			if val == nil || val.Value == nil {
				continue
			}
			elemLit, err := decodeLiteral(val, callsByDigest, memo)
			if err != nil {
				return nil, fmt.Errorf("failed to decode list literal: %w", err)
			}
			list = append(list, elemLit)
		}
		return NewLiteralList(list...), nil
	case *callpbv1.Literal_Object:
		args := make([]*Argument, 0, len(v.Object.Values))
		for _, arg := range v.Object.Values {
			if arg == nil {
				continue
			}
			fieldLit, err := decodeLiteral(arg.Value, callsByDigest, memo)
			if err != nil {
				return nil, fmt.Errorf("failed to decode object literal: %w", err)
			}
			args = append(args, &Argument{
				pb:    arg,
				value: fieldLit,
			})
		}
		return NewLiteralObject(args...), nil
	default:
		return nil, fmt.Errorf("unknown literal value type %T", v)
	}
}
