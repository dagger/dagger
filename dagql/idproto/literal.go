package idproto

import (
	"fmt"
	"strconv"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type Literate interface {
	ToLiteral() *Literal
}

type Literal struct {
	value LiteralValue
	raw   *RawLiteral
}

func NewLiteral(value LiteralValue) *Literal {
	return &Literal{
		value: value,
		raw: &RawLiteral{
			Value: value.raw(),
		},
	}
}

type LiteralValue interface {
	raw() isRawLiteral_Value
	gatherIDs(map[string]*RawID_Fields)
	inputs() ([]digest.Digest, error)
	modules() []*Module
	tainted() bool
	display() string
	toInput() any
	toAST() *ast.Value
}

func (lit *Literal) Value() LiteralValue {
	if lit == nil {
		return nil
	}
	return lit.value
}

func (lit *Literal) Inputs() ([]digest.Digest, error) {
	if lit == nil || lit.value == nil {
		return nil, nil
	}
	return lit.value.inputs()
}

func (lit *Literal) Modules() []*Module {
	if lit == nil || lit.value == nil {
		return nil
	}
	return lit.value.modules()
}

func (lit *Literal) Tainted() bool {
	if lit == nil || lit.value == nil {
		return false
	}
	return lit.value.tainted()
}

// ToAST returns an AST value appropriate for passing to a GraphQL server.
func (lit *Literal) Display() string {
	if lit == nil || lit.value == nil {
		return ""
	}
	return lit.value.display()
}

// ToInput returns a value appropriate for passing to an InputDecoder with
// minimal encoding/decoding overhead.
func (lit *Literal) ToInput() any {
	if lit == nil || lit.value == nil {
		return nil
	}
	return lit.value.toInput()
}

// ToAST returns an AST value appropriate for passing to a GraphQL server.
func (lit *Literal) ToAST() *ast.Value {
	if lit == nil || lit.value == nil {
		return nil
	}
	return lit.value.toAST()
}

func (lit *Literal) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	if lit == nil || lit.value == nil {
		return
	}
	lit.value.gatherIDs(idsByDigest)
}

func (lit *Literal) decode(
	raw *RawLiteral,
	idsByDigest map[string]*RawID_Fields,
	memo map[string]*ID,
) error {
	if raw == nil {
		return nil
	}
	switch v := raw.Value.(type) {
	case *RawLiteral_IdDigest:
		if v.IdDigest == "" {
			return nil
		}
		id := new(ID)
		if err := id.decode(v.IdDigest, idsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode literal ID: %w", err)
		}
		lit.value = &Literal_Id{id: id}
	case *RawLiteral_Null:
		lit.value = &Literal_Null{v}
	case *RawLiteral_Bool:
		lit.value = &Literal_Bool{v}
	case *RawLiteral_Enum:
		lit.value = &Literal_Enum{v}
	case *RawLiteral_Int:
		lit.value = &Literal_Int{v}
	case *RawLiteral_Float:
		lit.value = &Literal_Float{v}
	case *RawLiteral_String_:
		lit.value = &Literal_String_{v}
	case *RawLiteral_List:
		list := make([]*Literal, 0, len(v.List.Values))
		for _, val := range v.List.Values {
			if val == nil || val.Value == nil {
				continue
			}
			elemLit := new(Literal)
			if err := elemLit.decode(val, idsByDigest, memo); err != nil {
				return fmt.Errorf("failed to decode list literal: %w", err)
			}
			list = append(list, elemLit)
		}
		lit.value = &Literal_List{values: list}
	case *RawLiteral_Object:
		args := make([]*Argument, 0, len(v.Object.Values))
		for _, arg := range v.Object.Values {
			if arg == nil {
				continue
			}
			fieldLit := new(Literal)
			if err := fieldLit.decode(arg.Value, idsByDigest, memo); err != nil {
				return fmt.Errorf("failed to decode object literal: %w", err)
			}
			args = append(args, &Argument{
				raw:   arg,
				value: fieldLit,
			})
		}
		lit.value = &Literal_Object{values: args}
	default:
		return fmt.Errorf("unknown literal value type %T", v)
	}
	return nil
}

type Literal_Id struct {
	id *ID
}

func NewLiteralID(id *ID) *Literal {
	return NewLiteral(&Literal_Id{id: id})
}

func (lit *Literal_Id) Value() *ID {
	return lit.id
}

func (lit *Literal_Id) raw() isRawLiteral_Value {
	return &RawLiteral_IdDigest{IdDigest: lit.id.raw.Digest}
}

func (lit *Literal_Id) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	lit.id.gatherIDs(idsByDigest)
}

func (lit *Literal_Id) inputs() ([]digest.Digest, error) {
	return []digest.Digest{lit.id.Digest()}, nil
}

func (lit *Literal_Id) modules() []*Module {
	return lit.id.Modules()
}

func (lit *Literal_Id) tainted() bool {
	return lit.id.IsTainted()
}

func (lit *Literal_Id) display() string {
	return fmt.Sprintf("{%s}", lit.id.Display())
}

func (lit *Literal_Id) toInput() any {
	return lit.id
}

func (lit *Literal_Id) toAST() *ast.Value {
	enc, err := lit.id.Encode()
	if err != nil {
		panic(err)
	}
	return &ast.Value{
		Raw:  enc,
		Kind: ast.StringValue,
	}
}

type Literal_List struct {
	values []*Literal
}

func NewLiteralList(values ...*Literal) *Literal {
	return NewLiteral(&Literal_List{values: values})
}

func (lit *Literal_List) Range(fn func(int, Literal) error) error {
	for i, v := range lit.values {
		if v == nil {
			if err := fn(i, Literal{}); err != nil {
				return err
			}
			continue
		}
		if err := fn(i, *v); err != nil {
			return err
		}
	}
	return nil
}

func (lit *Literal_List) raw() isRawLiteral_Value {
	list := make([]*RawLiteral, len(lit.values))
	for i, val := range lit.values {
		list[i] = val.raw
	}
	return &RawLiteral_List{List: &List{Values: list}}
}

func (lit *Literal_List) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	for _, val := range lit.values {
		val.gatherIDs(idsByDigest)
	}
}

func (lit *Literal_List) inputs() ([]digest.Digest, error) {
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

func (lit *Literal_List) modules() []*Module {
	mods := []*Module{}
	for _, val := range lit.values {
		mods = append(mods, val.Modules()...)
	}
	return mods
}

func (lit *Literal_List) tainted() bool {
	for _, val := range lit.values {
		if val.Tainted() {
			return true
		}
	}
	return false
}

func (lit *Literal_List) display() string {
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

func (lit *Literal_List) toInput() any {
	list := make([]any, len(lit.values))
	for i, val := range lit.values {
		list[i] = val.ToInput()
	}
	return list
}

func (lit *Literal_List) toAST() *ast.Value {
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

type Literal_Object struct {
	values []*Argument
}

func NewLiteralObject(values ...*Argument) *Literal {
	return NewLiteral(&Literal_Object{values: values})
}

func (lit *Literal_Object) Range(fn func(int, string, Literal) error) error {
	for i, v := range lit.values {
		if v == nil {
			if err := fn(i, "", Literal{}); err != nil {
				return err
			}
			continue
		}
		if v.value == nil {
			if err := fn(i, v.raw.Name, Literal{}); err != nil {
				return err
			}
			continue
		}
		if err := fn(i, v.raw.Name, *v.value); err != nil {
			return err
		}
	}
	return nil
}

func (lit *Literal_Object) raw() isRawLiteral_Value {
	args := make([]*RawArgument, len(lit.values))
	for i, val := range lit.values {
		args[i] = val.raw
	}
	return &RawLiteral_Object{Object: &Object{Values: args}}
}

func (lit *Literal_Object) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	for _, val := range lit.values {
		val.gatherIDs(idsByDigest)
	}
}

func (lit *Literal_Object) inputs() ([]digest.Digest, error) {
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

func (lit *Literal_Object) modules() []*Module {
	mods := []*Module{}
	for _, arg := range lit.values {
		mods = append(mods, arg.value.Modules()...)
	}
	return mods
}

func (lit *Literal_Object) tainted() bool {
	for _, arg := range lit.values {
		if arg.Tainted() {
			return true
		}
	}
	return false
}

func (lit *Literal_Object) display() string {
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

func (lit *Literal_Object) toInput() any {
	obj := make(map[string]any, len(lit.values))
	for _, field := range lit.values {
		obj[field.raw.Name] = field.value.ToInput()
	}
	return obj
}

func (lit *Literal_Object) toAST() *ast.Value {
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

type Literal_Bool = literalPrimitiveType[bool, *RawLiteral_Bool]

func NewLiteralBool(val bool) *Literal {
	return NewLiteral(&Literal_Bool{&RawLiteral_Bool{Bool: val}})
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Bool) value() bool {
	return rawLit.Bool
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Bool) astKind() ast.ValueKind {
	return ast.BooleanValue
}

type Literal_Enum = literalPrimitiveType[string, *RawLiteral_Enum]

func NewLiteralEnum(val string) *Literal {
	return NewLiteral(&Literal_Enum{&RawLiteral_Enum{Enum: val}})
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Enum) value() string {
	return rawLit.Enum
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Enum) astKind() ast.ValueKind {
	return ast.EnumValue
}

type Literal_Int = literalPrimitiveType[int64, *RawLiteral_Int]

func NewLiteralInt(val int64) *Literal {
	return NewLiteral(&Literal_Int{&RawLiteral_Int{Int: val}})
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Int) value() int64 {
	return rawLit.Int
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Int) astKind() ast.ValueKind {
	return ast.IntValue
}

type Literal_Float = literalPrimitiveType[float64, *RawLiteral_Float]

func NewLiteralFloat(val float64) *Literal {
	return NewLiteral(&Literal_Float{&RawLiteral_Float{Float: val}})
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Float) value() float64 {
	return rawLit.Float
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Float) astKind() ast.ValueKind {
	return ast.FloatValue
}

type Literal_String_ = literalPrimitiveType[string, *RawLiteral_String_]

func NewLiteralString(val string) *Literal {
	return NewLiteral(&Literal_String_{&RawLiteral_String_{String_: val}})
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_String_) value() string {
	return rawLit.String_
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_String_) astKind() ast.ValueKind {
	return ast.StringValue
}

type Literal_Null = literalPrimitiveType[any, *RawLiteral_Null]

func NewLiteralNull() *Literal {
	return NewLiteral(&Literal_Null{&RawLiteral_Null{Null: true}})
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Null) value() any {
	return nil
}

//nolint:unused // it is used in literalPrimitiveValue...?
func (rawLit *RawLiteral_Null) astKind() ast.ValueKind {
	return ast.NullValue
}

type literalPrimitiveValue[T comparable] interface {
	isRawLiteral_Value
	value() T
	astKind() ast.ValueKind
}

type literalPrimitiveType[T comparable, V literalPrimitiveValue[T]] struct {
	rawVal V
}

func (lit *literalPrimitiveType[T, V]) Value() T {
	return lit.rawVal.value()
}

func (lit *literalPrimitiveType[T, V]) raw() isRawLiteral_Value {
	return lit.rawVal
}

func (lit *literalPrimitiveType[T, V]) gatherIDs(_ map[string]*RawID_Fields) {}

func (lit *literalPrimitiveType[T, V]) inputs() ([]digest.Digest, error) {
	return nil, nil
}

func (lit *literalPrimitiveType[T, V]) modules() []*Module {
	return nil
}

func (lit *literalPrimitiveType[T, V]) tainted() bool {
	return false
}

func (lit *literalPrimitiveType[T, V]) display() string {
	// kludge to special case truncation of strings
	if lit.rawVal.astKind() == ast.StringValue {
		var val any = lit.rawVal.value()
		return truncate(strconv.Quote(val.(string)), 100)
	}
	return fmt.Sprintf("%v", lit.rawVal.value())
}

func (lit *literalPrimitiveType[T, V]) toInput() any {
	return lit.rawVal.value()
}

func (lit *literalPrimitiveType[T, V]) toAST() *ast.Value {
	kind := lit.rawVal.astKind()
	var raw string
	if kind == ast.NullValue {
		// this teeny kludge allows us to use literalPrimitiveType with Literal_Null
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
