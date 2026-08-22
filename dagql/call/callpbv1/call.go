package callpbv1

import "github.com/vektah/gqlparser/v2/ast"

func (t *Type) ToAST() *ast.Type {
	a := &ast.Type{
		NamedType: t.NamedType,
		NonNull:   t.NonNull,
	}
	if t.Elem != nil {
		a.Elem = t.Elem.ToAST()
	}
	return a
}

type LiteralValue[T comparable] interface {
	isLiteral_Value
	Value() T
	ASTKind() ast.ValueKind
}

func (pbLit *Literal_Bool) Value() bool {
	return pbLit.Bool
}

func (pbLit *Literal_Bool) ASTKind() ast.ValueKind {
	return ast.BooleanValue
}

func (pbLit *Literal_String_) Value() string {
	return pbLit.String_
}

func (pbLit *Literal_String_) ASTKind() ast.ValueKind {
	return ast.StringValue
}

func (pbLit *Literal_Float) Value() float64 {
	return pbLit.Float
}

func (pbLit *Literal_Float) ASTKind() ast.ValueKind {
	return ast.FloatValue
}

func (pbLit *Literal_Null) Value() any {
	return nil
}

func (pbLit *Literal_Null) ASTKind() ast.ValueKind {
	return ast.NullValue
}

func (pbLit *Literal_Int) Value() int64 {
	return pbLit.Int
}

func (pbLit *Literal_Int) ASTKind() ast.ValueKind {
	return ast.IntValue
}

func (pbLit *Literal_Enum) Value() string {
	return pbLit.Enum
}

func (pbLit *Literal_Enum) ASTKind() ast.ValueKind {
	return ast.EnumValue
}
