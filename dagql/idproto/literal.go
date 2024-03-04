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
}

func NewLiteral(value LiteralValue) *Literal {
	return &Literal{
		value: value,
	}
}

type LiteralValue interface {
	clone(map[digest.Digest]*ID) (LiteralValue, error)
	encode(map[string]*RawID_Fields) (*RawLiteral, error)
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

func (lit *Literal) clone(memo map[digest.Digest]*ID) (*Literal, error) {
	if lit == nil || lit.value == nil {
		return lit, nil
	}

	newLit := new(Literal)
	var err error
	newLit.value, err = lit.value.clone(memo)
	if err != nil {
		return nil, fmt.Errorf("failed to clone literal value: %w", err)
	}
	return newLit, nil
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

func (lit *Literal) encode(idsByDigest map[string]*RawID_Fields) (*RawLiteral, error) {
	if lit == nil || lit.value == nil {
		return nil, nil
	}
	return lit.value.encode(idsByDigest)
}

func (lit *Literal) decode(
	raw *RawLiteral,
	idsByDigest map[string]*RawID_Fields,
	memo map[string]*idState,
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
		if len(list) == 0 {
			return nil
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
		if len(args) == 0 {
			return nil
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
	return &Literal{
		value: &Literal_Id{id: id},
	}
}

func (lit *Literal_Id) Value() *ID {
	return lit.id
}

func (lit *Literal_Id) clone(memo map[digest.Digest]*ID) (LiteralValue, error) {
	newID, err := lit.id.clone(memo)
	if err != nil {
		return nil, fmt.Errorf("failed to clone literal ID: %w", err)
	}
	return &Literal_Id{id: newID}, nil
}

func (lit *Literal_Id) encode(idsByDigest map[string]*RawID_Fields) (*RawLiteral, error) {
	id, err := lit.id.encode(idsByDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to encode literal ID: %w", err)
	}
	return &RawLiteral{Value: &RawLiteral_IdDigest{IdDigest: id}}, nil
}

func (lit *Literal_Id) inputs() ([]digest.Digest, error) {
	d, err := lit.id.Digest()
	if err != nil {
		return nil, err
	}
	return []digest.Digest{d}, nil
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
	return &Literal{
		value: &Literal_List{values: values},
	}
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

func (lit *Literal_List) clone(memo map[digest.Digest]*ID) (LiteralValue, error) {
	list := make([]*Literal, len(lit.values))
	for i, val := range lit.values {
		var err error
		list[i], err = val.clone(memo)
		if err != nil {
			return nil, fmt.Errorf("failed to clone list literal: %w", err)
		}
	}
	return &Literal_List{values: list}, nil
}

func (lit *Literal_List) encode(idsByDigest map[string]*RawID_Fields) (*RawLiteral, error) {
	list := make([]*RawLiteral, len(lit.values))
	for i, val := range lit.values {
		var err error
		list[i], err = val.encode(idsByDigest)
		if err != nil {
			return nil, fmt.Errorf("failed to encode list literal: %w", err)
		}
	}
	return &RawLiteral{Value: &RawLiteral_List{List: &List{Values: list}}}, nil
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
	return &Literal{
		value: &Literal_Object{values: values},
	}
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

func (lit *Literal_Object) clone(memo map[digest.Digest]*ID) (LiteralValue, error) {
	args := make([]*Argument, len(lit.values))
	for i, arg := range lit.values {
		var err error
		args[i], err = arg.clone(memo)
		if err != nil {
			return nil, fmt.Errorf("failed to clone object literal: %w", err)
		}
	}
	return &Literal_Object{values: args}, nil
}

func (lit *Literal_Object) encode(idsByDigest map[string]*RawID_Fields) (*RawLiteral, error) {
	args := make([]*RawArgument, len(lit.values))
	for i, arg := range lit.values {
		var err error
		args[i], err = arg.encode(idsByDigest)
		if err != nil {
			return nil, fmt.Errorf("failed to encode object literal: %w", err)
		}
	}
	return &RawLiteral{Value: &RawLiteral_Object{Object: &Object{Values: args}}}, nil
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

type Literal_Bool = literalPrimitiveType[*RawLiteral_Bool, bool]

func NewLiteralBool(val bool) *Literal {
	return &Literal{
		value: &Literal_Bool{&RawLiteral_Bool{Bool: val}},
	}
}

func (rawLit *RawLiteral_Bool) Value() bool {
	return rawLit.Bool
}

func (rawLit *RawLiteral_Bool) astKind() ast.ValueKind {
	return ast.BooleanValue
}

type Literal_Enum = literalPrimitiveType[*RawLiteral_Enum, string]

func NewLiteralEnum(val string) *Literal {
	return &Literal{
		value: &Literal_Enum{&RawLiteral_Enum{Enum: val}},
	}
}

func (rawLit *RawLiteral_Enum) Value() string {
	return rawLit.Enum
}

func (rawLit *RawLiteral_Enum) astKind() ast.ValueKind {
	return ast.EnumValue
}

type Literal_Int = literalPrimitiveType[*RawLiteral_Int, int64]

func NewLiteralInt(val int64) *Literal {
	return &Literal{
		value: &Literal_Int{&RawLiteral_Int{Int: val}},
	}
}

func (rawLit *RawLiteral_Int) Value() int64 {
	return rawLit.Int
}

func (rawLit *RawLiteral_Int) astKind() ast.ValueKind {
	return ast.IntValue
}

type Literal_Float = literalPrimitiveType[*RawLiteral_Float, float64]

func NewLiteralFloat(val float64) *Literal {
	return &Literal{
		value: &Literal_Float{&RawLiteral_Float{Float: val}},
	}
}

func (rawLit *RawLiteral_Float) Value() float64 {
	return rawLit.Float
}

func (rawLit *RawLiteral_Float) astKind() ast.ValueKind {
	return ast.FloatValue
}

type Literal_String_ = literalPrimitiveType[*RawLiteral_String_, string]

func NewLiteralString(val string) *Literal {
	return &Literal{
		value: &Literal_String_{&RawLiteral_String_{String_: val}},
	}
}

func (rawLit *RawLiteral_String_) Value() string {
	return rawLit.String_
}

func (rawLit *RawLiteral_String_) astKind() ast.ValueKind {
	return ast.StringValue
}

type Literal_Null = literalPrimitiveType[*RawLiteral_Null, any]

func NewLiteralNull() *Literal {
	return &Literal{
		value: &Literal_Null{&RawLiteral_Null{Null: true}},
	}
}

func (rawLit *RawLiteral_Null) Value() any {
	return nil
}

func (rawLit *RawLiteral_Null) astKind() ast.ValueKind {
	return ast.NullValue
}

type literalPrimitiveType[P interface {
	isRawLiteral_Value
	Value() T
	astKind() ast.ValueKind
}, T comparable] struct {
	raw P
}

func (lit *literalPrimitiveType[P, T]) Value() T {
	return lit.raw.Value()
}

func (lit literalPrimitiveType[P, T]) clone(_ map[digest.Digest]*ID) (LiteralValue, error) {
	return &lit, nil
}

func (lit *literalPrimitiveType[P, T]) encode(_ map[string]*RawID_Fields) (*RawLiteral, error) {
	return &RawLiteral{Value: lit.raw}, nil
}

func (lit *literalPrimitiveType[P, T]) inputs() ([]digest.Digest, error) {
	return nil, nil
}

func (lit *literalPrimitiveType[P, T]) modules() []*Module {
	return nil
}

func (lit *literalPrimitiveType[P, T]) tainted() bool {
	return false
}

func (lit *literalPrimitiveType[P, T]) display() string {
	// kludge to special case truncation of strings
	if lit.raw.astKind() == ast.StringValue {
		var val any = lit.raw.Value()
		return truncate(strconv.Quote(val.(string)), 100)
	}
	return fmt.Sprintf("%v", lit.raw.Value())
}

func (lit *literalPrimitiveType[P, T]) toInput() any {
	return lit.raw.Value()
}

func (lit *literalPrimitiveType[P, T]) toAST() *ast.Value {
	kind := lit.raw.astKind()
	var raw string
	if kind == ast.NullValue {
		// this teeny kludge allows us to use literalPrimitiveType with Literal_Null
		// otherwise the raw value would show up as "<nil>"
		raw = "null"
	} else {
		raw = fmt.Sprintf("%v", lit.raw.Value())
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
