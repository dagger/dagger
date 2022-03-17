// Copyright 2020 CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package adt

import (
	"bytes"
	"fmt"
	"io"
	"regexp"

	"github.com/cockroachdb/apd/v2"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// A StructLit represents an unevaluated struct literal or file body.
type StructLit struct {
	Src   ast.Node // ast.File or ast.StructLit
	Decls []Decl

	// TODO: record the merge order somewhere.

	// The below fields are redundant to Decls and are computed with Init.

	// field marks the optional conjuncts of all explicit Fields.
	// Required Fields are marked as empty
	Fields []FieldInfo

	Dynamic []*DynamicField

	// excluded are all literal fields that already exist.
	Bulk []*BulkOptionalField

	Additional  []Expr
	HasEmbed    bool
	IsOpen      bool // has a ...
	initialized bool

	types OptionalType

	// administrative fields like hasreferences.
	// hasReferences bool
}

func (o *StructLit) IsFile() bool {
	_, ok := o.Src.(*ast.File)
	return ok
}

type FieldInfo struct {
	Label    Feature
	Optional []Node
}

func (x *StructLit) HasOptional() bool {
	return x.types&(HasField|HasPattern|HasAdditional) != 0
}

func (x *StructLit) Source() ast.Node { return x.Src }

func (x *StructLit) evaluate(c *OpContext) Value {
	e := c.Env(0)
	v := &Vertex{Conjuncts: []Conjunct{{e, x, CloseInfo{}}}}
	// evaluate may not finalize a field, as the resulting value may be
	// used in a context where more conjuncts are added. It may also lead
	// to disjuncts being in a partially expanded state, leading to
	// misaligned nodeContexts.
	c.Unify(v, AllArcs)
	return v
}

// TODO: remove this method
func (o *StructLit) MarkField(f Feature) {
	o.Fields = append(o.Fields, FieldInfo{Label: f})
}

func (o *StructLit) Init() {
	if o.initialized {
		return
	}
	o.initialized = true
	for _, d := range o.Decls {
		switch x := d.(type) {
		case *Field:
			if o.fieldIndex(x.Label) < 0 {
				o.Fields = append(o.Fields, FieldInfo{Label: x.Label})
			}

		case *OptionalField:
			p := o.fieldIndex(x.Label)
			if p < 0 {
				p = len(o.Fields)
				o.Fields = append(o.Fields, FieldInfo{Label: x.Label})
			}
			o.Fields[p].Optional = append(o.Fields[p].Optional, x)
			o.types |= HasField

		case *DynamicField:
			o.Dynamic = append(o.Dynamic, x)
			o.types |= HasDynamic

		case Expr:
			o.HasEmbed = true

		case *ForClause, Yielder:
			o.HasEmbed = true

		case *BulkOptionalField:
			o.Bulk = append(o.Bulk, x)
			o.types |= HasPattern
			switch x.Filter.(type) {
			case *BasicType, *Top:
			default:
				o.types |= HasComplexPattern
			}

		case *Ellipsis:
			expr := x.Value
			if x.Value == nil {
				o.IsOpen = true
				o.types |= IsOpen
				// TODO(perf): encode more efficiently.
				expr = &Top{}
			} else {
				o.types |= HasAdditional
			}
			o.Additional = append(o.Additional, expr)

		default:
			panic("unreachable")
		}
	}
}

func (o *StructLit) fieldIndex(f Feature) int {
	for i := range o.Fields {
		if o.Fields[i].Label == f {
			return i
		}
	}
	return -1
}

func (o *StructLit) OptionalTypes() OptionalType {
	return o.types
}

func (o *StructLit) IsOptional(label Feature) bool {
	for _, f := range o.Fields {
		if f.Label == label && len(f.Optional) > 0 {
			return true
		}
	}
	return false
}

// FIELDS
//
// Fields can also be used as expressions whereby the value field is the
// expression this allows retaining more context.

// Field represents a field with a fixed label. It can be a regular field,
// definition or hidden field.
//
//   foo: bar
//   #foo: bar
//   _foo: bar
//
// Legacy:
//
//   Foo :: bar
//
type Field struct {
	Src *ast.Field

	Label Feature
	Value Expr
}

func (x *Field) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

// An OptionalField represents an optional regular field.
//
//   foo?: expr
//
type OptionalField struct {
	Src   *ast.Field
	Label Feature
	Value Expr
}

func (x *OptionalField) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

// A BulkOptionalField represents a set of optional field.
//
//   [expr]: expr
//
type BulkOptionalField struct {
	Src    *ast.Field // Elipsis or Field
	Filter Expr
	Value  Expr
	Label  Feature // for reference and formatting
}

func (x *BulkOptionalField) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

// A Ellipsis represents a set of optional fields of a given type.
//
//   ...T
//
type Ellipsis struct {
	Src   *ast.Ellipsis
	Value Expr
}

func (x *Ellipsis) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

// A DynamicField represents a regular field for which the key is computed.
//
//    "\(expr)": expr
//    (expr): expr
//
type DynamicField struct {
	Src   *ast.Field
	Key   Expr
	Value Expr
}

func (x *DynamicField) IsOptional() bool {
	return x.Src.Optional != token.NoPos
}

func (x *DynamicField) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

// A ListLit represents an unevaluated list literal.
//
//    [a, for x in src { ... }, b, ...T]
//
type ListLit struct {
	Src *ast.ListLit

	// scalars, comprehensions, ...T
	Elems []Elem
}

func (x *ListLit) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *ListLit) evaluate(c *OpContext) Value {
	e := c.Env(0)
	v := &Vertex{Conjuncts: []Conjunct{{e, x, CloseInfo{}}}}
	// TODO: should be AllArcs and then use Finalize for builtins?
	c.Unify(v, Finalized) // TODO: also partial okay?
	return v
}

// Null represents null. It can be used as a Value and Expr.
type Null struct {
	Src ast.Node
}

func (x *Null) Source() ast.Node { return x.Src }
func (x *Null) Kind() Kind       { return NullKind }

// Bool is a boolean value. It can be used as a Value and Expr.
type Bool struct {
	Src ast.Node
	B   bool
}

func (x *Bool) Source() ast.Node { return x.Src }
func (x *Bool) Kind() Kind       { return BoolKind }

// Num is a numeric value. It can be used as a Value and Expr.
type Num struct {
	Src ast.Node
	K   Kind        // needed?
	X   apd.Decimal // Is integer if the apd.Decimal is an integer.
}

// TODO: do we need this?
// func NewNumFromString(src ast.Node, s string) Value {
// 	n := &Num{Src: src, K: IntKind}
// 	if strings.ContainsAny(s, "eE.") {
// 		n.K = FloatKind
// 	}
// 	_, _, err := n.X.SetString(s)
// 	if err != nil {
// 		pos := token.NoPos
// 		if src != nil {
// 			pos = src.Pos()
// 		}
// 		return &Bottom{Err: errors.Newf(pos, "invalid number: %v", err)}
// 	}
// 	return n
// }

func (x *Num) Source() ast.Node { return x.Src }
func (x *Num) Kind() Kind       { return x.K }

// TODO: do we still need this?
// func (x *Num) Specialize(k Kind) Value {
// 	k = k & x.K
// 	if k == x.K {
// 		return x
// 	}
// 	y := *x
// 	y.K = k
// 	return &y
// }

// String is a string value. It can be used as a Value and Expr.
type String struct {
	Src ast.Node
	Str string
	RE  *regexp.Regexp // only set if needed
}

func (x *String) Source() ast.Node { return x.Src }
func (x *String) Kind() Kind       { return StringKind }

// Bytes is a bytes value. It can be used as a Value and Expr.
type Bytes struct {
	Src ast.Node
	B   []byte
	RE  *regexp.Regexp // only set if needed
}

func (x *Bytes) Source() ast.Node { return x.Src }
func (x *Bytes) Kind() Kind       { return BytesKind }

// Composites: the evaluated fields of a composite are recorded in the arc
// vertices.

type ListMarker struct {
	Src    ast.Node
	IsOpen bool
}

func (x *ListMarker) Source() ast.Node { return x.Src }
func (x *ListMarker) Kind() Kind       { return ListKind }
func (x *ListMarker) node()            {}

type StructMarker struct {
	// NeedClose is used to signal that the evaluator should close this struct.
	// It is only set by the close builtin.
	NeedClose bool
}

func (x *StructMarker) Source() ast.Node { return nil }
func (x *StructMarker) Kind() Kind       { return StructKind }
func (x *StructMarker) node()            {}

// Top represents all possible values. It can be used as a Value and Expr.
type Top struct{ Src *ast.Ident }

func (x *Top) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}
func (x *Top) Kind() Kind { return TopKind }

// BasicType represents all values of a certain Kind. It can be used as a Value
// and Expr.
//
//   string
//   int
//   num
//   bool
//
type BasicType struct {
	Src *ast.Ident
	K   Kind
}

func (x *BasicType) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}
func (x *BasicType) Kind() Kind { return x.K }

// TODO: do we still need this?
// func (x *BasicType) Specialize(k Kind) Value {
// 	k = x.K & k
// 	if k == x.K {
// 		return x
// 	}
// 	y := *x
// 	y.K = k
// 	return &y
// }

// TODO: should we use UnaryExpr for Bound now we have BoundValue?

// BoundExpr represents an unresolved unary comparator.
//
//    <a
//    =~MyPattern
//
type BoundExpr struct {
	Src  *ast.UnaryExpr
	Op   Op
	Expr Expr
}

func (x *BoundExpr) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *BoundExpr) evaluate(ctx *OpContext) Value {
	v := ctx.value(x.Expr)
	if isError(v) {
		return v
	}

	switch k := v.Kind(); k {
	case IntKind, FloatKind, NumKind, StringKind, BytesKind:
	case NullKind:
		if x.Op != NotEqualOp {
			err := ctx.NewPosf(pos(x.Expr),
				"cannot use null for bound %s", x.Op)
			return &Bottom{Err: err}
		}
	default:
		mask := IntKind | FloatKind | NumKind | StringKind | BytesKind
		if x.Op == NotEqualOp {
			mask |= NullKind
		}
		if k&mask != 0 {
			ctx.addErrf(IncompleteError, token.NoPos, // TODO(errors): use ctx.pos()?
				"non-concrete value %s for bound %s", x.Expr, x.Op)
			return nil
		}
		err := ctx.NewPosf(pos(x.Expr),
			"invalid value %s (type %s) for bound %s", v, k, x.Op)
		return &Bottom{Err: err}
	}

	if v, ok := x.Expr.(Value); ok {
		if v == nil || v.Concreteness() > Concrete {
			return ctx.NewErrf("bound has fixed non-concrete value")
		}
		return &BoundValue{x.Src, x.Op, v}
	}

	// This simplifies boundary expressions. It is an alternative to an
	// evaluation strategy that makes nodes increasingly more specific.
	//
	// For instance, a completely different implementation would be to allow
	// the precense of a concrete value to ignore incomplete errors.
	//
	// TODO: consider an alternative approach.
	switch y := v.(type) {
	case *BoundValue:
		switch {
		case y.Op == NotEqualOp:
			switch x.Op {
			case LessEqualOp, LessThanOp, GreaterEqualOp, GreaterThanOp:
				// <(!=3)  =>  number
				// Smaller than an arbitrarily large number is any number.
				return &BasicType{K: y.Kind()}
			case NotEqualOp:
				// !=(!=3) ==> 3
				// Not a value that is anything but a given value is that
				// given value.
				return y.Value
			}

		case x.Op == NotEqualOp:
			// Invert if applicable.
			switch y.Op {
			case LessEqualOp:
				return &BoundValue{x.Src, GreaterThanOp, y.Value}
			case LessThanOp:
				return &BoundValue{x.Src, GreaterEqualOp, y.Value}
			case GreaterEqualOp:
				return &BoundValue{x.Src, LessThanOp, y.Value}
			case GreaterThanOp:
				return &BoundValue{x.Src, LessEqualOp, y.Value}
			}

		case (x.Op == LessThanOp || x.Op == LessEqualOp) &&
			(y.Op == GreaterThanOp || y.Op == GreaterEqualOp),
			(x.Op == GreaterThanOp || x.Op == GreaterEqualOp) &&
				(y.Op == LessThanOp || y.Op == LessEqualOp):
			// <(>=3)
			// Something smaller than an arbitrarily large number is any number.
			return &BasicType{K: y.Kind()}

		case x.Op == LessThanOp &&
			(y.Op == LessEqualOp || y.Op == LessThanOp),
			x.Op == GreaterThanOp &&
				(y.Op == GreaterEqualOp || y.Op == GreaterThanOp):
			// <(<=x)  => <x
			// <(<x)   => <x
			// Less than something that is less or equal to x is less than x.
			return &BoundValue{x.Src, x.Op, y.Value}

		case x.Op == LessEqualOp &&
			(y.Op == LessEqualOp || y.Op == LessThanOp),
			x.Op == GreaterEqualOp &&
				(y.Op == GreaterEqualOp || y.Op == GreaterThanOp):
			// <=(<x)   => <x
			// <=(<=x)  => <=x
			// Less or equal than something that is less than x is less than x.
			return y
		}

	case *BasicType:
		switch x.Op {
		case LessEqualOp, LessThanOp, GreaterEqualOp, GreaterThanOp:
			return y
		}
	}
	if v.Concreteness() > Concrete {
		// TODO(errors): analyze dependencies of x.Expr to get positions.
		ctx.addErrf(IncompleteError, token.NoPos, // TODO(errors): use ctx.pos()?
			"non-concrete value %s for bound %s", x.Expr, x.Op)
		return nil
	}
	return &BoundValue{x.Src, x.Op, v}
}

// A BoundValue is a fully evaluated unary comparator that can be used to
// validate other values.
//
//    <5
//    =~"Name$"
//
type BoundValue struct {
	Src   ast.Expr
	Op    Op
	Value Value
}

func (x *BoundValue) Source() ast.Node { return x.Src }
func (x *BoundValue) Kind() Kind {
	k := x.Value.Kind()
	switch k {
	case IntKind, FloatKind, NumKind:
		return NumKind

	case NullKind:
		if x.Op == NotEqualOp {
			return TopKind &^ NullKind
		}
	}
	return k
}

func (x *BoundValue) validate(c *OpContext, y Value) *Bottom {
	a := y // Can be list or struct.
	b := c.scalar(x.Value)
	if c.HasErr() {
		return c.Err()
	}

	switch v := BinOp(c, x.Op, a, b).(type) {
	case *Bottom:
		return v

	case *Bool:
		if v.B {
			return nil
		}
		// TODO(errors): use "invalid value %v (not an %s)" if x is a
		// predeclared identifier such as `int`.
		err := c.Newf("invalid value %v (out of bound %s)", y, x)
		err.AddPosition(y)
		return &Bottom{Src: c.src, Err: err, Code: EvalError}

	default:
		panic(fmt.Sprintf("unsupported type %T", v))
	}
}

func (x *BoundValue) validateStr(c *OpContext, a string) bool {
	if str, ok := x.Value.(*String); ok {
		b := str.Str
		switch x.Op {
		case LessEqualOp:
			return a <= b
		case LessThanOp:
			return a < b
		case GreaterEqualOp:
			return a >= b
		case GreaterThanOp:
			return a > b
		case EqualOp:
			return a == b
		case NotEqualOp:
			return a != b
		case MatchOp:
			return c.regexp(x.Value).MatchString(a)
		case NotMatchOp:
			return !c.regexp(x.Value).MatchString(a)
		}
	}
	return x.validate(c, &String{Str: a}) == nil
}

func (x *BoundValue) validateInt(c *OpContext, a int64) bool {
	switch n := x.Value.(type) {
	case *Num:
		b, err := n.X.Int64()
		if err != nil {
			break
		}
		switch x.Op {
		case LessEqualOp:
			return a <= b
		case LessThanOp:
			return a < b
		case GreaterEqualOp:
			return a >= b
		case GreaterThanOp:
			return a > b
		case EqualOp:
			return a == b
		case NotEqualOp:
			return a != b
		}
	}
	return x.validate(c, c.NewInt64(a)) == nil
}

// A NodeLink is used during computation to refer to an existing Vertex.
// It is used to signal a potential cycle or reference.
// Note that a NodeLink may be used as a value. This should be taken into
// account.
type NodeLink struct {
	Node *Vertex
}

func (x *NodeLink) Kind() Kind {
	return x.Node.Kind()
}
func (x *NodeLink) Source() ast.Node { return x.Node.Source() }

func (x *NodeLink) resolve(c *OpContext, state VertexStatus) *Vertex {
	return x.Node
}

// A FieldReference represents a lexical reference to a field.
//
//    a
//
type FieldReference struct {
	Src     *ast.Ident
	UpCount int32
	Label   Feature
}

func (x *FieldReference) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *FieldReference) resolve(c *OpContext, state VertexStatus) *Vertex {
	n := c.relNode(x.UpCount)
	pos := pos(x)
	return c.lookup(n, pos, x.Label, state)
}

// A ValueReference represents a lexical reference to a value.
//
//    a: X=b
//
type ValueReference struct {
	Src     *ast.Ident
	UpCount int32
	Label   Feature // for informative purposes
}

func (x *ValueReference) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *ValueReference) resolve(c *OpContext, state VertexStatus) *Vertex {
	if x.UpCount == 0 {
		return c.vertex
	}
	n := c.relNode(x.UpCount - 1)
	return n
}

// A LabelReference refers to the string or integer value of a label.
//
//    [X=Pattern]: b: X
//
type LabelReference struct {
	Src     *ast.Ident
	UpCount int32
}

// TODO: should this implement resolver at all?

func (x *LabelReference) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *LabelReference) evaluate(ctx *OpContext) Value {
	label := ctx.relLabel(x.UpCount)
	if label == 0 {
		// There is no label. This may happen if a LabelReference is evaluated
		// outside of the context of a parent node, for instance if an
		// "additional" items or properties is evaluated in isolation.
		//
		// TODO: this should return the pattern of the label.
		return &BasicType{K: StringKind}
	}
	return label.ToValue(ctx)
}

// A DynamicReference is like a LabelReference, but with a computed label.
//
//    X=(x): X
//    X="\(x)": X
//
type DynamicReference struct {
	Src     *ast.Ident
	UpCount int32
	Label   Expr

	// TODO: only use aliases and store the actual expression only in the scope.
	// The feature is unique for every instance. This will also allow dynamic
	// fields to be ordered among normal fields.
	//
	// This could also be used to assign labels to embedded values, if they
	// don't match a label.
	Alias Feature
}

func (x *DynamicReference) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *DynamicReference) resolve(ctx *OpContext, state VertexStatus) *Vertex {
	e := ctx.Env(x.UpCount)
	frame := ctx.PushState(e, x.Src)
	v := ctx.value(x.Label)
	ctx.PopState(frame)
	f := ctx.Label(x.Label, v)
	return ctx.lookup(e.Vertex, pos(x), f, state)
}

// An ImportReference refers to an imported package.
//
//    import "strings"
//
//    strings.ToLower("Upper")
//
type ImportReference struct {
	Src        *ast.Ident
	ImportPath Feature
	Label      Feature // for informative purposes
}

func (x *ImportReference) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *ImportReference) resolve(ctx *OpContext, state VertexStatus) *Vertex {
	path := x.ImportPath.StringValue(ctx)
	v := ctx.Runtime.LoadImport(path)
	if v == nil {
		ctx.addErrf(EvalError, x.Src.Pos(), "cannot find package %q", path)
	}
	return v
}

// A LetReference evaluates a let expression in its original environment.
//
//   let X = x
//
type LetReference struct {
	Src     *ast.Ident
	UpCount int32
	Label   Feature // for informative purposes
	X       Expr
}

func (x *LetReference) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *LetReference) resolve(c *OpContext, state VertexStatus) *Vertex {
	e := c.Env(x.UpCount)
	label := e.Vertex.Label
	if x.X == nil {
		panic("nil expression")
	}
	// Anonymous arc.
	return &Vertex{Parent: nil, Label: label, Conjuncts: []Conjunct{{e, x.X, CloseInfo{}}}}
}

func (x *LetReference) evaluate(c *OpContext) Value {
	e := c.Env(x.UpCount)

	// Not caching let expressions may lead to exponential behavior.
	return e.evalCached(c, x.X)
}

// A SelectorExpr looks up a fixed field in an expression.
//
//     X.Sel
//
type SelectorExpr struct {
	Src *ast.SelectorExpr
	X   Expr
	Sel Feature
}

func (x *SelectorExpr) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *SelectorExpr) resolve(c *OpContext, state VertexStatus) *Vertex {
	n := c.node(x, x.X, x.Sel.IsRegular(), state)
	if n == emptyNode {
		return n
	}
	if n.status == Partial {
		if b := n.state.incompleteErrors(); b != nil && b.Code < CycleError {
			n.BaseValue = b
			return n
		}
	}
	return c.lookup(n, x.Src.Sel.Pos(), x.Sel, state)
}

// IndexExpr is like a selector, but selects an index.
//
//    X[Index]
//
type IndexExpr struct {
	Src   *ast.IndexExpr
	X     Expr
	Index Expr
}

func (x *IndexExpr) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *IndexExpr) resolve(ctx *OpContext, state VertexStatus) *Vertex {
	// TODO: support byte index.
	n := ctx.node(x, x.X, true, state)
	i := ctx.value(x.Index)
	if n == emptyNode {
		return n
	}
	if n.status == Partial {
		if b := n.state.incompleteErrors(); b != nil && b.Code < CycleError {
			n.BaseValue = b
			return n
		}
	}
	f := ctx.Label(x.Index, i)
	return ctx.lookup(n, x.Src.Index.Pos(), f, state)
}

// A SliceExpr represents a slice operation. (Not currently in spec.)
//
//    X[Lo:Hi:Stride]
//
type SliceExpr struct {
	Src    *ast.SliceExpr
	X      Expr
	Lo     Expr
	Hi     Expr
	Stride Expr
}

func (x *SliceExpr) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *SliceExpr) evaluate(c *OpContext) Value {
	// TODO: strides

	v := c.value(x.X)
	const as = "slice index"

	switch v := v.(type) {
	case nil:
		c.addErrf(IncompleteError, c.pos(), "non-concrete slice subject %s", x.X)
		return nil
	case *Vertex:
		if !v.IsList() {
			break
		}

		var (
			lo = uint64(0)
			hi = uint64(len(v.Arcs))
		)
		if x.Lo != nil {
			lo = c.uint64(c.value(x.Lo), as)
		}
		if x.Hi != nil {
			hi = c.uint64(c.value(x.Hi), as)
			if hi > uint64(len(v.Arcs)) {
				return c.NewErrf("index %d out of range", hi)
			}
		}
		if lo > hi {
			return c.NewErrf("invalid slice index: %d > %d", lo, hi)
		}

		n := c.newList(c.src, v.Parent)
		for i, a := range v.Arcs[lo:hi] {
			label, err := MakeLabel(a.Source(), int64(i), IntLabel)
			if err != nil {
				c.AddBottom(&Bottom{Src: a.Source(), Err: err})
				return nil
			}
			arc := *a
			arc.Parent = n
			arc.Label = label
			n.Arcs = append(n.Arcs, &arc)
		}
		n.status = Finalized
		return n

	case *Bytes:
		var (
			lo = uint64(0)
			hi = uint64(len(v.B))
		)
		if x.Lo != nil {
			lo = c.uint64(c.value(x.Lo), as)
		}
		if x.Hi != nil {
			hi = c.uint64(c.value(x.Hi), as)
			if hi > uint64(len(v.B)) {
				return c.NewErrf("index %d out of range", hi)
			}
		}
		if lo > hi {
			return c.NewErrf("invalid slice index: %d > %d", lo, hi)
		}
		return c.newBytes(v.B[lo:hi])
	}

	if isError(v) {
		return v
	}
	return c.NewErrf("cannot slice %v (type %s)", v, v.Kind())
}

// An Interpolation is a string interpolation.
//
//    "a \(b) c"
//
type Interpolation struct {
	Src   *ast.Interpolation
	K     Kind   // string or bytes
	Parts []Expr // odd: strings, even sources
}

func (x *Interpolation) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *Interpolation) evaluate(c *OpContext) Value {
	buf := bytes.Buffer{}
	for _, e := range x.Parts {
		v := c.value(e)
		if x.K == BytesKind {
			buf.Write(c.ToBytes(v))
		} else {
			buf.WriteString(c.ToString(v))
		}
	}
	if err := c.Err(); err != nil {
		err = &Bottom{
			Code: err.Code,
			Err:  errors.Wrapf(err.Err, pos(x), "invalid interpolation"),
		}
		// c.AddBottom(err)
		// return nil
		return err
	}
	if x.K == BytesKind {
		return &Bytes{x.Src, buf.Bytes(), nil}
	}
	return &String{x.Src, buf.String(), nil}
}

// UnaryExpr is a unary expression.
//
//    Op X
//    -X !X +X
//
type UnaryExpr struct {
	Src *ast.UnaryExpr
	Op  Op
	X   Expr
}

func (x *UnaryExpr) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *UnaryExpr) evaluate(c *OpContext) Value {
	if !c.concreteIsPossible(x.Op, x.X) {
		return nil
	}
	v := c.value(x.X)
	if isError(v) {
		return v
	}

	op := x.Op
	k := kind(v)
	expectedKind := k
	switch op {
	case SubtractOp:
		if v, ok := v.(*Num); ok {
			f := *v
			f.X.Neg(&v.X)
			f.Src = x.Src
			return &f
		}
		expectedKind = NumKind

	case AddOp:
		if v, ok := v.(*Num); ok {
			// TODO: wrap in thunk to save position of '+'?
			return v
		}
		expectedKind = NumKind

	case NotOp:
		if v, ok := v.(*Bool); ok {
			return &Bool{x.Src, !v.B}
		}
		expectedKind = BoolKind
	}
	if k&expectedKind != BottomKind {
		c.addErrf(IncompleteError, pos(x.X),
			"operand %s of '%s' not concrete (was %s)", x.X, op, k)
		return nil
	}
	return c.NewErrf("invalid operation %s (%s %s)", x, op, k)
}

// BinaryExpr is a binary expression.
//
//    X + Y
//    X & Y
//
type BinaryExpr struct {
	Src *ast.BinaryExpr
	Op  Op
	X   Expr
	Y   Expr
}

func (x *BinaryExpr) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *BinaryExpr) evaluate(c *OpContext) Value {
	env := c.Env(0)
	if x.Op == AndOp {
		// Anonymous Arc
		v := &Vertex{Conjuncts: []Conjunct{{env, x, CloseInfo{}}}}
		c.Unify(v, Finalized)
		return v
	}

	if !c.concreteIsPossible(x.Op, x.X) || !c.concreteIsPossible(x.Op, x.Y) {
		return nil
	}

	// TODO: allow comparing to a literal Bottom only. Find something more
	// principled perhaps. One should especially take care that two values
	// evaluating to Bottom don't evaluate to true. For now we check for
	// Bottom here and require that one of the values be a Bottom literal.
	if x.Op == EqualOp || x.Op == NotEqualOp {
		if isLiteralBottom(x.X) {
			return c.validate(env, x.Src, x.Y, x.Op)
		}
		if isLiteralBottom(x.Y) {
			return c.validate(env, x.Src, x.X, x.Op)
		}
	}

	left, _ := c.Concrete(env, x.X, x.Op)
	right, _ := c.Concrete(env, x.Y, x.Op)

	if err := CombineErrors(x.Src, left, right); err != nil {
		return err
	}

	if err := c.Err(); err != nil {
		return err
	}

	return BinOp(c, x.Op, left, right)
}

func (c *OpContext) validate(env *Environment, src ast.Node, x Expr, op Op) (r Value) {
	s := c.PushState(env, src)
	if c.nonMonotonicLookupNest == 0 {
		c.nonMonotonicGeneration++
	}

	var match bool
	// NOTE: using Unwrap is maybe note entirely accurate, as it may discard
	// a future error. However, if it does so, the error will at least be
	// reported elsewhere.
	switch b := c.value(x).(type) {
	case nil:
	case *Bottom:
		if b.Code == CycleError {
			c.PopState(s)
			c.AddBottom(b)
			return nil
		}
		match = op == EqualOp
		// We have a nonmonotonic use of a failure. Referenced fields should
		// not be added anymore.
		c.nonMonotonicRejectNest++
		c.evalState(x, Partial)
		c.nonMonotonicRejectNest--

	default:
		// TODO(cycle): if EqualOp:
		// - ensure to pass special status to if clause or keep a track of "hot"
		//   paths.
		// - evaluate hypothetical struct
		// - walk over all fields and verify that fields are not contradicting
		//   previously marked fields.
		//
		switch {
		case b.Concreteness() > Concrete:
			// TODO: mimic comparison to bottom semantics. If it is a valid
			// value, check for concreteness that this level only. This
			// should ultimately be replaced with an exists and valid
			// builtin.
			match = op == EqualOp
		default:
			match = op != EqualOp
		}
		c.nonMonotonicLookupNest++
		c.evalState(x, Partial)
		c.nonMonotonicLookupNest--
	}

	c.PopState(s)
	return &Bool{src, match}
}

// A CallExpr represents a call to a builtin.
//
//    len(x)
//    strings.ToLower(x)
//
type CallExpr struct {
	Src  *ast.CallExpr
	Fun  Expr
	Args []Expr
}

func (x *CallExpr) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *CallExpr) evaluate(c *OpContext) Value {
	fun := c.value(x.Fun)
	var b *Builtin
	switch f := fun.(type) {
	case *Builtin:
		b = f

	case *BuiltinValidator:
		// We allow a validator that takes no arguments accept the validated
		// value to be called with zero arguments.
		switch {
		case f.Src != nil:
			c.AddErrf("cannot call previously called validator %s", x.Fun)

		case f.Builtin.IsValidator(len(x.Args)):
			v := *f
			v.Src = x
			return &v

		default:
			b = f.Builtin
		}

	default:
		c.AddErrf("cannot call non-function %s (type %s)", x.Fun, kind(fun))
		return nil
	}
	args := []Value{}
	for i, a := range x.Args {
		expr := c.value(a)
		switch v := expr.(type) {
		case nil:
			// There SHOULD be an error in the context. If not, we generate
			// one.
			c.Assertf(pos(x.Fun), c.HasErr(),
				"argument %d to function %s is incomplete", i, x.Fun)

		case *Bottom:
			// TODO(errors): consider adding an argument index for this errors.
			// On the other hand, this error is really not related to the
			// argument itself, so maybe it is good as it is.
			c.AddBottom(v)

		default:
			args = append(args, expr)
		}
	}
	if c.HasErr() {
		return nil
	}
	if b.IsValidator(len(args)) {
		return &BuiltinValidator{x, b, args}
	}
	result := b.call(c, pos(x), args)
	if result == nil {
		return nil
	}
	return c.evalState(result, Partial)
}

// A Builtin is a value representing a native function call.
type Builtin struct {
	// TODO:  make these values for better type checking.
	Params []Param
	Result Kind
	Func   func(c *OpContext, args []Value) Expr

	Package Feature
	Name    string
}

type Param struct {
	Name  Feature // name of the argument; mostly for documentation
	Value Value   // Could become Value later, using disjunctions for defaults.
}

// Kind returns the kind mask of this parameter.
func (p Param) Kind() Kind {
	return p.Value.Kind()
}

// Default reports the default value for this Param or nil if there is none.
func (p Param) Default() Value {
	d, ok := p.Value.(*Disjunction)
	if !ok || d.NumDefaults != 1 {
		return nil
	}
	return d.Values[0]
}

func (x *Builtin) WriteName(w io.Writer, c *OpContext) {
	_, _ = fmt.Fprintf(w, "%s.%s", x.Package.StringValue(c), x.Name)
}

// Kind here represents the case where Builtin is used as a Validator.
func (x *Builtin) Kind() Kind {
	return FuncKind
}

func (x *Builtin) BareValidator() *BuiltinValidator {
	if len(x.Params) != 1 ||
		(x.Result != BoolKind && x.Result != BottomKind) {
		return nil
	}
	return &BuiltinValidator{Builtin: x}
}

// IsValidator reports whether b should be interpreted as a Validator for the
// given number of arguments.
func (b *Builtin) IsValidator(numArgs int) bool {
	return numArgs == len(b.Params)-1 &&
		b.Result&^BoolKind == 0 &&
		b.Params[numArgs].Default() == nil
}

func bottom(v Value) *Bottom {
	if x, ok := v.(*Vertex); ok {
		v = x.Value()
	}
	b, _ := v.(*Bottom)
	return b
}

func (x *Builtin) call(c *OpContext, p token.Pos, args []Value) Expr {
	fun := x // right now always x.
	if len(args) > len(x.Params) {
		c.addErrf(0, p,
			"too many arguments in call to %s (have %d, want %d)",
			fun, len(args), len(x.Params))
		return nil
	}
	for i := len(args); i < len(x.Params); i++ {
		v := x.Params[i].Default()
		if v == nil {
			c.addErrf(0, p,
				"not enough arguments in call to %s (have %d, want %d)",
				fun, len(args), len(x.Params))
			return nil
		}
		args = append(args, v)
	}
	for i, a := range args {
		if x.Params[i].Kind() == BottomKind {
			continue
		}
		if b := bottom(a); b != nil {
			return b
		}
		if k := kind(a); x.Params[i].Kind()&k == BottomKind {
			code := EvalError
			b, _ := args[i].(*Bottom)
			if b != nil {
				code = b.Code
			}
			c.addErrf(code, pos(a),
				"cannot use %s (type %s) as %s in argument %d to %s",
				a, k, x.Params[i].Kind(), i+1, fun)
			return nil
		}
		v := x.Params[i].Value
		if _, ok := v.(*BasicType); !ok {
			env := c.Env(0)
			x := &BinaryExpr{Op: AndOp, X: v, Y: a}
			n := &Vertex{Conjuncts: []Conjunct{{env, x, CloseInfo{}}}}
			c.Unify(n, Finalized)
			if _, ok := n.BaseValue.(*Bottom); ok {
				c.addErrf(0, pos(a),
					"cannot use %s as %s in argument %d to %s",
					a, v, i+1, fun)
				return nil
			}
			args[i] = n
		}
	}
	return x.Func(c, args)
}

func (x *Builtin) Source() ast.Node { return nil }

// A BuiltinValidator is a Value that results from evaluation a partial call
// to a builtin (using CallExpr).
//
//    strings.MinRunes(4)
//
type BuiltinValidator struct {
	Src     *CallExpr
	Builtin *Builtin
	Args    []Value // any but the first value
}

func (x *BuiltinValidator) Source() ast.Node {
	if x.Src == nil {
		return x.Builtin.Source()
	}
	return x.Src.Source()
}

func (x *BuiltinValidator) Pos() token.Pos {
	if src := x.Source(); src != nil {
		return src.Pos()
	}
	return token.NoPos
}

func (x *BuiltinValidator) Kind() Kind {
	return x.Builtin.Params[0].Kind()
}

func (x *BuiltinValidator) validate(c *OpContext, v Value) *Bottom {
	args := make([]Value, len(x.Args)+1)
	args[0] = v
	copy(args[1:], x.Args)

	return validateWithBuiltin(c, x.Pos(), x.Builtin, args)
}

func validateWithBuiltin(c *OpContext, src token.Pos, b *Builtin, args []Value) *Bottom {
	var severeness ErrorCode
	var err errors.Error

	res := b.call(c, src, args)
	switch v := res.(type) {
	case nil:
		return nil

	case *Bottom:
		if v == nil {
			return nil // caught elsewhere, but be defensive.
		}
		severeness = v.Code
		err = v.Err

	case *Bool:
		if v.B {
			return nil
		}

	default:
		return c.NewErrf("invalid validator %s.%s", b.Package.StringValue(c), b.Name)
	}

	// failed:
	var buf bytes.Buffer
	b.WriteName(&buf, c)
	if len(args) > 1 {
		buf.WriteString("(")
		for i, a := range args[1:] {
			if i > 0 {
				_, _ = buf.WriteString(", ")
			}
			buf.WriteString(c.Str(a))
		}
		buf.WriteString(")")
	}

	vErr := c.NewPosf(src, "invalid value %s (does not satisfy %s)", args[0], buf.String())

	for _, v := range args {
		vErr.AddPosition(v)
	}

	return &Bottom{Code: severeness, Err: errors.Wrap(vErr, err)}
}

// A Disjunction represents a disjunction, where each disjunct may or may not
// be marked as a default.
type DisjunctionExpr struct {
	Src    *ast.BinaryExpr
	Values []Disjunct

	HasDefaults bool
}

// A Disjunct is used in Disjunction.
type Disjunct struct {
	Val     Expr
	Default bool
}

func (x *DisjunctionExpr) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *DisjunctionExpr) evaluate(c *OpContext) Value {
	e := c.Env(0)
	v := &Vertex{Conjuncts: []Conjunct{{e, x, CloseInfo{}}}}
	c.Unify(v, Finalized) // TODO: also partial okay?
	// TODO: if the disjunction result originated from a literal value, we may
	// consider the result closed to create more permanent errors.
	return v
}

// A Conjunction is a conjunction of values that cannot be represented as a
// single value. It is the result of unification.
type Conjunction struct {
	Src    ast.Expr
	Values []Value
}

func (x *Conjunction) Source() ast.Node { return x.Src }
func (x *Conjunction) Kind() Kind {
	k := TopKind
	for _, v := range x.Values {
		k &= v.Kind()
	}
	return k
}

// A disjunction is a disjunction of values. It is the result of expanding
// a DisjunctionExpr if the expression cannot be represented as a single value.
type Disjunction struct {
	Src ast.Expr

	// Values are the non-error disjuncts of this expression. The first
	// NumDefault values are default values.
	Values []*Vertex

	Errors *Bottom // []bottom

	// NumDefaults indicates the number of default values.
	NumDefaults int
	HasDefaults bool
}

func (x *Disjunction) Source() ast.Node { return x.Src }
func (x *Disjunction) Kind() Kind {
	k := BottomKind
	for _, v := range x.Values {
		k |= v.Kind()
	}
	return k
}

// A ForClause represents a for clause of a comprehension. It can be used
// as a struct or list element.
//
//    for k, v in src {}
//
type ForClause struct {
	Syntax *ast.ForClause
	Key    Feature
	Value  Feature
	Src    Expr
	Dst    Yielder
}

func (x *ForClause) Source() ast.Node {
	if x.Syntax == nil {
		return nil
	}
	return x.Syntax
}

func (x *ForClause) yield(c *OpContext, f YieldFunc) {
	n := c.node(x, x.Src, true, AllArcs)
	for _, a := range n.Arcs {
		if !a.Label.IsRegular() {
			continue
		}

		c.Unify(a, Partial)

		n := &Vertex{status: Finalized}

		if x.Value != InvalidLabel {
			b := &Vertex{
				Label:     x.Value,
				BaseValue: a,
			}
			n.Arcs = append(n.Arcs, b)
		}

		if x.Key != InvalidLabel {
			v := &Vertex{Label: x.Key}
			key := a.Label.ToValue(c)
			v.AddConjunct(MakeRootConjunct(c.Env(0), key))
			v.SetValue(c, Finalized, key)
			n.Arcs = append(n.Arcs, v)
		}

		sub := c.spawn(n)
		saved := c.PushState(sub, x.Dst.Source())
		x.Dst.yield(c, f)
		if b := c.PopState(saved); b != nil {
			c.AddBottom(b)
			break
		}
		if c.HasErr() {
			break
		}
	}
}

// An IfClause represents an if clause of a comprehension. It can be used
// as a struct or list element.
//
//    if cond {}
//
type IfClause struct {
	Src       *ast.IfClause
	Condition Expr
	Dst       Yielder
}

func (x *IfClause) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *IfClause) yield(ctx *OpContext, f YieldFunc) {
	if ctx.BoolValue(ctx.value(x.Condition)) {
		x.Dst.yield(ctx, f)
	}
}

// An LetClause represents a let clause in a comprehension.
//
//    for k, v in src {}
//
type LetClause struct {
	Src   *ast.LetClause
	Label Feature
	Expr  Expr
	Dst   Yielder
}

func (x *LetClause) Source() ast.Node {
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *LetClause) yield(c *OpContext, f YieldFunc) {
	n := &Vertex{Arcs: []*Vertex{
		{Label: x.Label, Conjuncts: []Conjunct{{c.Env(0), x.Expr, CloseInfo{}}}},
	}}

	sub := c.spawn(n)
	saved := c.PushState(sub, x.Dst.Source())
	x.Dst.yield(c, f)
	if b := c.PopState(saved); b != nil {
		c.AddBottom(b)
	}
}

// A ValueClause represents the value part of a comprehension.
type ValueClause struct {
	*StructLit
}

func (x *ValueClause) Source() ast.Node {
	if x.StructLit == nil {
		return nil
	}
	if x.Src == nil {
		return nil
	}
	return x.Src
}

func (x *ValueClause) yield(op *OpContext, f YieldFunc) {
	f(op.Env(0), x.StructLit)
}
