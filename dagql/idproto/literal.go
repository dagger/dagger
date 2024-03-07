package idproto

import (
	"fmt"
	"strconv"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type Literate interface {
	ToLiteral() Literal
}

type Literal interface {
	Inputs() ([]digest.Digest, error)
	Modules() []*Module
	Tainted() bool
	Display() string
	ToInput() any
	ToAST() *ast.Value

	raw() *RawLiteral
	gatherIDs(map[string]*RawID_Fields)
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

func (lit *LiteralID) Tainted() bool {
	return lit.id.IsTainted()
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

func (lit *LiteralID) raw() *RawLiteral {
	return &RawLiteral{Value: &RawLiteral_IdDigest{IdDigest: lit.id.raw.Digest}}
}

func (lit *LiteralID) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	lit.id.gatherIDs(idsByDigest)
}

type LiteralList struct {
	values []Literal
}

func NewLiteralList(values ...Literal) *LiteralList {
	return &LiteralList{values: values}
}

func (lit *LiteralList) Range(fn func(int, Literal) error) error {
	for i, v := range lit.values {
		if v == nil {
			if err := fn(i, nil); err != nil {
				return err
			}
			continue
		}
		if err := fn(i, v); err != nil {
			return err
		}
	}
	return nil
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

func (lit *LiteralList) Tainted() bool {
	for _, val := range lit.values {
		if val.Tainted() {
			return true
		}
	}
	return false
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

func (lit *LiteralList) raw() *RawLiteral {
	list := make([]*RawLiteral, len(lit.values))
	for i, val := range lit.values {
		list[i] = val.raw()
	}
	return &RawLiteral{Value: &RawLiteral_List{List: &List{Values: list}}}
}

func (lit *LiteralList) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	for _, val := range lit.values {
		val.gatherIDs(idsByDigest)
	}
}

type LiteralObject struct {
	values []*Argument
}

func NewLiteralObject(values ...*Argument) *LiteralObject {
	return &LiteralObject{values: values}
}

func (lit *LiteralObject) Range(fn func(int, string, Literal) error) error {
	for i, v := range lit.values {
		if v == nil {
			if err := fn(i, "", nil); err != nil {
				return err
			}
			continue
		}
		if v.value == nil {
			if err := fn(i, v.raw.Name, nil); err != nil {
				return err
			}
			continue
		}
		if err := fn(i, v.raw.Name, v.value); err != nil {
			return err
		}
	}
	return nil
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

func (lit *LiteralObject) Tainted() bool {
	for _, arg := range lit.values {
		if arg.Tainted() {
			return true
		}
	}
	return false
}

func (lit *LiteralObject) Display() string {
	obj := "{"
	for i, field := range lit.values {
		if i > 0 {
			obj += ","
		}
		obj += field.raw.Name + ": " + field.value.Display()
	}
	obj += "}"
	return obj
}

func (lit *LiteralObject) ToInput() any {
	obj := make(map[string]any, len(lit.values))
	for _, field := range lit.values {
		obj[field.raw.Name] = field.value.ToInput()
	}
	return obj
}

func (lit *LiteralObject) ToAST() *ast.Value {
	obj := &ast.Value{
		Kind: ast.ObjectValue,
	}
	for _, field := range lit.values {
		obj.Children = append(obj.Children, &ast.ChildValue{
			Name:  field.raw.Name,
			Value: field.value.ToAST(),
		})
	}
	return obj
}

func (lit *LiteralObject) raw() *RawLiteral {
	args := make([]*RawArgument, len(lit.values))
	for i, val := range lit.values {
		args[i] = val.raw
	}
	return &RawLiteral{Value: &RawLiteral_Object{Object: &Object{Values: args}}}
}

func (lit *LiteralObject) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	for _, val := range lit.values {
		val.gatherIDs(idsByDigest)
	}
}

type LiteralBool = LiteralPrimitiveType[bool, *RawLiteral_Bool]

func NewLiteralBool(val bool) *LiteralBool {
	return &LiteralBool{&RawLiteral_Bool{Bool: val}}
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Bool) value() bool {
	return rawLit.Bool
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Bool) astKind() ast.ValueKind {
	return ast.BooleanValue
}

type LiteralEnum = LiteralPrimitiveType[string, *RawLiteral_Enum]

func NewLiteralEnum(val string) *LiteralEnum {
	return &LiteralEnum{&RawLiteral_Enum{Enum: val}}
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Enum) value() string {
	return rawLit.Enum
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Enum) astKind() ast.ValueKind {
	return ast.EnumValue
}

type LiteralInt = LiteralPrimitiveType[int64, *RawLiteral_Int]

func NewLiteralInt(val int64) *LiteralInt {
	return &LiteralInt{&RawLiteral_Int{Int: val}}
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Int) value() int64 {
	return rawLit.Int
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Int) astKind() ast.ValueKind {
	return ast.IntValue
}

type LiteralFloat = LiteralPrimitiveType[float64, *RawLiteral_Float]

func NewLiteralFloat(val float64) *LiteralFloat {
	return &LiteralFloat{&RawLiteral_Float{Float: val}}
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Float) value() float64 {
	return rawLit.Float
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Float) astKind() ast.ValueKind {
	return ast.FloatValue
}

type LiteralString = LiteralPrimitiveType[string, *RawLiteral_String_]

func NewLiteralString(val string) *LiteralString {
	return &LiteralString{&RawLiteral_String_{String_: val}}
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_String_) value() string {
	return rawLit.String_
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_String_) astKind() ast.ValueKind {
	return ast.StringValue
}

type LiteralNull = LiteralPrimitiveType[any, *RawLiteral_Null]

func NewLiteralNull() *LiteralNull {
	return &LiteralNull{&RawLiteral_Null{Null: true}}
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Null) value() any {
	return nil
}

//nolint:unused // it is used in LiteralPrimitiveType...?
func (rawLit *RawLiteral_Null) astKind() ast.ValueKind {
	return ast.NullValue
}

type LiteralPrimitiveType[T comparable, V interface {
	isRawLiteral_Value
	value() T
	astKind() ast.ValueKind
}] struct {
	rawVal V
}

func (lit *LiteralPrimitiveType[T, V]) Value() T {
	return lit.rawVal.value()
}

func (lit *LiteralPrimitiveType[T, V]) Inputs() ([]digest.Digest, error) {
	return nil, nil
}

func (lit *LiteralPrimitiveType[T, V]) Modules() []*Module {
	return nil
}

func (lit *LiteralPrimitiveType[T, V]) Tainted() bool {
	return false
}

func (lit *LiteralPrimitiveType[T, V]) Display() string {
	// kludge to special case truncation of strings
	if lit.rawVal.astKind() == ast.StringValue {
		var val any = lit.rawVal.value()
		return truncate(strconv.Quote(val.(string)), 100)
	}
	return fmt.Sprintf("%v", lit.rawVal.value())
}

func (lit *LiteralPrimitiveType[T, V]) ToInput() any {
	return lit.rawVal.value()
}

func (lit *LiteralPrimitiveType[T, V]) ToAST() *ast.Value {
	kind := lit.rawVal.astKind()
	var raw string
	if kind == ast.NullValue {
		// this teeny kludge allows us to use LiteralPrimitiveType with Literal_Null
		// otherwise the raw value would show up as "<nil>"
		raw = "null"
	} else {
		raw = fmt.Sprintf("%v", lit.rawVal.value())
	}
	return &ast.Value{
		Raw:  raw,
		Kind: kind,
	}
}

func (lit *LiteralPrimitiveType[T, V]) raw() *RawLiteral {
	return &RawLiteral{Value: lit.rawVal}
}

func (lit *LiteralPrimitiveType[T, V]) gatherIDs(_ map[string]*RawID_Fields) {}

func decodeLiteral(
	raw *RawLiteral,
	idsByDigest map[string]*RawID_Fields,
	memo map[string]*ID,
) (Literal, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.Value.(type) {
	case *RawLiteral_IdDigest:
		if v.IdDigest == "" {
			return nil, nil
		}
		id := new(ID)
		if err := id.decode(v.IdDigest, idsByDigest, memo); err != nil {
			return nil, fmt.Errorf("failed to decode literal ID: %w", err)
		}
		return NewLiteralID(id), nil
	case *RawLiteral_Null:
		return NewLiteralNull(), nil
	case *RawLiteral_Bool:
		return NewLiteralBool(v.Bool), nil
	case *RawLiteral_Enum:
		return NewLiteralEnum(v.Enum), nil
	case *RawLiteral_Int:
		return NewLiteralInt(v.Int), nil
	case *RawLiteral_Float:
		return NewLiteralFloat(v.Float), nil
	case *RawLiteral_String_:
		return NewLiteralString(v.String_), nil
	case *RawLiteral_List:
		list := make([]Literal, 0, len(v.List.Values))
		for _, val := range v.List.Values {
			if val == nil || val.Value == nil {
				continue
			}
			elemLit, err := decodeLiteral(val, idsByDigest, memo)
			if err != nil {
				return nil, fmt.Errorf("failed to decode list literal: %w", err)
			}
			list = append(list, elemLit)
		}
		return NewLiteralList(list...), nil
	case *RawLiteral_Object:
		args := make([]*Argument, 0, len(v.Object.Values))
		for _, arg := range v.Object.Values {
			if arg == nil {
				continue
			}
			fieldLit, err := decodeLiteral(arg.Value, idsByDigest, memo)
			if err != nil {
				return nil, fmt.Errorf("failed to decode object literal: %w", err)
			}
			args = append(args, &Argument{
				raw:   arg,
				value: fieldLit,
			})
		}
		return NewLiteralObject(args...), nil
	default:
		return nil, fmt.Errorf("unknown literal value type %T", v)
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
