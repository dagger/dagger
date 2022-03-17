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
	"fmt"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// TODO: unanswered questions about structural cycles:
//
// 1. When detecting a structural cycle, should we consider this as:
//    a) an unevaluated value,
//    b) an incomplete error (which does not affect parent validity), or
//    c) a special value.
//
// Making it an error is the simplest way to ensure reentrancy is disallowed:
// without an error it would require an additional mechanism to stop reentrancy
// from continuing to process. Even worse, in some cases it may only partially
// evaluate, resulting in unexpected results. For this reason, we are taking
// approach `b` for now.
//
// This has some consequences of how disjunctions are treated though. Consider
//
//     list: {
//        head: _
//        tail: list | null
//     }
//
// When making it an error, evaluating the above will result in
//
//     list: {
//        head: _
//        tail: null
//     }
//
// because list will result in a structural cycle, and thus an error, it will be
// stripped from the disjunction. This may or may not be a desirable property. A
// nice thing is that it is not required to write `list | *null`. A disadvantage
// is that this is perhaps somewhat inexplicit.
//
// When not making it an error (and simply cease evaluating child arcs upon
// cycle detection), the result would be:
//
//     list: {
//        head: _
//        tail: list | null
//     }
//
// In other words, an evaluation would result in a cycle and thus an error.
// Implementations can recognize such cases by having unevaluated arcs. An
// explicit structure cycle marker would probably be less error prone.
//
// Note that in both cases, a reference to list will still use the original
// conjuncts, so the result will be the same for either method in this case.
//
//
// 2. Structural cycle allowance.
//
// Structural cycle detection disallows reentrancy as well. This means one
// cannot use structs for recursive computation. This will probably preclude
// evaluation of some configuration. Given that there is no real alternative
// yet, we could allow structural cycle detection to be optionally disabled.

// An Environment links the parent scopes for identifier lookup to a composite
// node. Each conjunct that make up node in the tree can be associated with
// a different environment (although some conjuncts may share an Environment).
type Environment struct {
	Up     *Environment
	Vertex *Vertex

	// DynamicLabel is only set when instantiating a field from a pattern
	// constraint. It is used to resolve label references.
	DynamicLabel Feature

	// TODO(perf): make the following public fields a shareable struct as it
	// mostly is going to be the same for child nodes.

	// Cyclic indicates a structural cycle was detected for this conjunct or one
	// of its ancestors.
	Cyclic bool

	// Deref keeps track of nodes that should dereference to Vertex. It is used
	// for detecting structural cycle.
	//
	// The detection algorithm is based on Tomabechi's quasi-destructive graph
	// unification. This detection requires dependencies to be resolved into
	// fully dereferenced vertices. This is not the case in our algorithm:
	// the result of evaluating conjuncts is placed into dereferenced vertices
	// _after_ they are evaluated, but the Environment still points to the
	// non-dereferenced context.
	//
	// In order to be able to detect structural cycles, we need to ensure that
	// at least one node that is part of a cycle in the context in which
	// conjunctions are evaluated dereferences correctly.
	//
	// The only field necessary to detect a structural cycle, however, is
	// the Status field of the Vertex. So rather than dereferencing a node
	// proper, it is sufficient to copy the Status of the dereferenced nodes
	// to these nodes (will always be EvaluatingArcs).
	Deref []*Vertex

	// Cycles contains vertices for which cycles are detected. It is used
	// for tracking self-references within structural cycles.
	//
	// Unlike Deref, Cycles is not incremented with child nodes.
	// TODO: Cycles is always a tail end of Deref, so this can be optimized.
	Cycles []*Vertex

	cache map[Expr]Value
}

type ID int32

// evalCached is used to look up let expressions. Caching let expressions
// prevents a possible combinatorial explosion.
func (e *Environment) evalCached(c *OpContext, x Expr) Value {
	if v, ok := x.(Value); ok {
		return v
	}
	v, ok := e.cache[x]
	if !ok {
		if e.cache == nil {
			e.cache = map[Expr]Value{}
		}
		env, src := c.e, c.src
		c.e, c.src = e, x.Source()
		v = c.evalState(x, Partial) // TODO: should this be Finalized?
		c.e, c.src = env, src
		if b, ok := v.(*Bottom); !ok || !b.IsIncomplete() {
			e.cache[x] = v
		}
	}
	return v
}

// A Vertex is a node in the value tree. It may be a leaf or internal node.
// It may have arcs to represent elements of a fully evaluated struct or list.
//
// For structs, it only contains definitions and concrete fields.
// optional fields are dropped.
//
// It maintains source information such as a list of conjuncts that contributed
// to the value.
type Vertex struct {
	// Parent links to a parent Vertex. This parent should only be used to
	// access the parent's Label field to find the relative location within a
	// tree.
	Parent *Vertex

	// Label is the feature leading to this vertex.
	Label Feature

	// State:
	//   eval: nil, BaseValue: nil -- unevaluated
	//   eval: *,   BaseValue: nil -- evaluating
	//   eval: *,   BaseValue: *   -- finalized
	//
	state *nodeContext
	// TODO: move the following status fields to nodeContext.

	// status indicates the evaluation progress of this vertex.
	status VertexStatus

	// isData indicates that this Vertex is to be interepreted as data: pattern
	// and additional constraints, as well as optional fields, should be
	// ignored.
	isData                bool
	Closed                bool
	nonMonotonicReject    bool
	nonMonotonicInsertGen int32
	nonMonotonicLookupGen int32

	// EvalCount keeps track of temporary dereferencing during evaluation.
	// If EvalCount > 0, status should be considered to be EvaluatingArcs.
	EvalCount int32

	// SelfCount is used for tracking self-references.
	SelfCount int32

	// BaseValue is the value associated with this vertex. For lists and structs
	// this is a sentinel value indicating its kind.
	BaseValue BaseValue

	// ChildErrors is the collection of all errors of children.
	ChildErrors *Bottom

	// The parent of nodes can be followed to determine the path within the
	// configuration of this node.
	// Value  Value
	Arcs []*Vertex // arcs are sorted in display order.

	// Conjuncts lists the structs that ultimately formed this Composite value.
	// This includes all selected disjuncts.
	//
	// This value may be nil, in which case the Arcs are considered to define
	// the final value of this Vertex.
	Conjuncts []Conjunct

	// Structs is a slice of struct literals that contributed to this value.
	// This information is used to compute the topological sort of arcs.
	Structs []*StructInfo
}

func (v *Vertex) Clone() *Vertex {
	c := *v
	c.state = nil
	return &c
}

type StructInfo struct {
	*StructLit

	Env *Environment

	CloseInfo

	// Embed indicates the struct in which this struct is embedded (originally),
	// or nil if this is a root structure.
	// Embed   *StructInfo
	// Context *RefInfo // the location from which this struct originates.
	Disable bool

	Embedding bool
}

// TODO(perf): this could be much more aggressive for eliminating structs that
// are immaterial for closing.
func (s *StructInfo) useForAccept() bool {
	if c := s.closeInfo; c != nil {
		return !c.noCheck
	}
	return true
}

// VertexStatus indicates the evaluation progress of a Vertex.
type VertexStatus int8

const (
	// Unprocessed indicates a Vertex has not been processed before.
	// Value must be nil.
	Unprocessed VertexStatus = iota

	// Evaluating means that the current Vertex is being evaluated. If this is
	// encountered it indicates a reference cycle. Value must be nil.
	Evaluating

	// Partial indicates that the result was only partially evaluated. It will
	// need to be fully evaluated to get a complete results.
	//
	// TODO: this currently requires a renewed computation. Cache the
	// nodeContext to allow reusing the computations done so far.
	Partial

	// AllArcs is request only. It must be past Partial, but
	// before recursively resolving arcs.
	AllArcs

	// EvaluatingArcs indicates that the arcs of the Vertex are currently being
	// evaluated. If this is encountered it indicates a structural cycle.
	// Value does not have to be nil
	EvaluatingArcs

	// Finalized means that this node is fully evaluated and that the results
	// are save to use without further consideration.
	Finalized
)

func (s VertexStatus) String() string {
	switch s {
	case Unprocessed:
		return "unprocessed"
	case Evaluating:
		return "evaluating"
	case Partial:
		return "partial"
	case AllArcs:
		return "allarcs"
	case EvaluatingArcs:
		return "evaluatingArcs"
	case Finalized:
		return "finalized"
	default:
		return "unknown"
	}
}

func (v *Vertex) Status() VertexStatus {
	if v.EvalCount > 0 {
		return EvaluatingArcs
	}
	return v.status
}

func (v *Vertex) UpdateStatus(s VertexStatus) {
	Assertf(v.status <= s+1, "attempt to regress status from %d to %d", v.Status(), s)

	if s == Finalized && v.BaseValue == nil {
		// panic("not finalized")
	}
	v.status = s
}

// Value returns the Value of v without definitions if it is a scalar
// or itself otherwise.
func (v *Vertex) Value() Value {
	switch x := v.BaseValue.(type) {
	case nil:
		return nil
	case *StructMarker, *ListMarker:
		return v
	case Value:
		return x
	default:
		panic(fmt.Sprintf("unexpected type %T", v.BaseValue))
	}
}

// isUndefined reports whether a vertex does not have a useable BaseValue yet.
func (v *Vertex) isUndefined() bool {
	switch v.BaseValue {
	case nil, cycle:
		return true
	}
	return false
}

func (x *Vertex) IsConcrete() bool {
	return x.Concreteness() <= Concrete
}

// IsData reports whether v should be interpreted in data mode. In other words,
// it tells whether optional field matching and non-regular fields, like
// definitions and hidden fields, should be ignored.
func (v *Vertex) IsData() bool {
	return v.isData || len(v.Conjuncts) == 0
}

// ToDataSingle creates a new Vertex that represents just the regular fields
// of this vertex. Arcs are left untouched.
// It is used by cue.Eval to convert nodes to data on per-node basis.
func (v *Vertex) ToDataSingle() *Vertex {
	w := *v
	w.isData = true
	w.state = nil
	w.status = Finalized
	return &w
}

// ToDataAll returns a new v where v and all its descendents contain only
// the regular fields.
func (v *Vertex) ToDataAll() *Vertex {
	arcs := make([]*Vertex, 0, len(v.Arcs))
	for _, a := range v.Arcs {
		if a.Label.IsRegular() {
			arcs = append(arcs, a.ToDataAll())
		}
	}
	w := *v
	w.state = nil
	w.status = Finalized

	w.BaseValue = toDataAll(w.BaseValue)
	w.Arcs = arcs
	w.isData = true
	w.Conjuncts = make([]Conjunct, len(v.Conjuncts))
	// TODO(perf): this is not strictly necessary for evaluation, but it can
	// hurt performance greatly. Drawback is that it may disable ordering.
	for _, s := range w.Structs {
		s.Disable = true
	}
	copy(w.Conjuncts, v.Conjuncts)
	for i, c := range w.Conjuncts {
		if v, _ := c.x.(Value); v != nil {
			w.Conjuncts[i].x = toDataAll(v).(Value)
		}
	}
	return &w
}

func toDataAll(v BaseValue) BaseValue {
	switch x := v.(type) {
	default:
		return x

	case *Vertex:
		return x.ToDataAll()

	// The following cases are always erroneous, but we handle them anyway
	// to avoid issues with the closedness algorithm down the line.
	case *Disjunction:
		d := *x
		d.Values = make([]*Vertex, len(x.Values))
		for i, v := range x.Values {
			d.Values[i] = v.ToDataAll()
		}
		return &d

	case *Conjunction:
		c := *x
		c.Values = make([]Value, len(x.Values))
		for i, v := range x.Values {
			// This case is okay because the source is of type Value.
			c.Values[i] = toDataAll(v).(Value)
		}
		return &c
	}
}

// func (v *Vertex) IsEvaluating() bool {
// 	return v.Value == cycle
// }

func (v *Vertex) IsErr() bool {
	// if v.Status() > Evaluating {
	if _, ok := v.BaseValue.(*Bottom); ok {
		return true
	}
	// }
	return false
}

func (v *Vertex) Err(c *OpContext, state VertexStatus) *Bottom {
	c.Unify(v, state)
	if b, ok := v.BaseValue.(*Bottom); ok {
		return b
	}
	return nil
}

// func (v *Vertex) Evaluate()

func (v *Vertex) Finalize(c *OpContext) {
	// Saving and restoring the error context prevents v from panicking in
	// case the caller did not handle existing errors in the context.
	err := c.errs
	c.errs = nil
	c.Unify(v, Finalized)
	c.errs = err
}

func (v *Vertex) AddErr(ctx *OpContext, b *Bottom) {
	v.BaseValue = CombineErrors(nil, v.Value(), b)
	v.UpdateStatus(Finalized)
}

func (v *Vertex) SetValue(ctx *OpContext, state VertexStatus, value BaseValue) *Bottom {
	v.BaseValue = value
	v.UpdateStatus(state)
	return nil
}

// ToVertex wraps v in a new Vertex, if necessary.
func ToVertex(v Value) *Vertex {
	switch x := v.(type) {
	case *Vertex:
		return x
	default:
		n := &Vertex{
			status:    Finalized,
			BaseValue: x,
		}
		n.AddConjunct(MakeRootConjunct(nil, v))
		return n
	}
}

// Unwrap returns the possibly non-concrete scalar value of v or nil if v is
// a list, struct or of undefined type.
func Unwrap(v Value) Value {
	x, ok := v.(*Vertex)
	if !ok {
		return v
	}
	x = x.Indirect()
	if n := x.state; n != nil && isCyclePlaceholder(x.BaseValue) {
		if n.errs != nil && !n.errs.IsIncomplete() {
			return n.errs
		}
		if n.scalar != nil {
			return n.scalar
		}
	}
	return x.Value()
}

// Indirect unrolls indirections of Vertex values. These may be introduced,
// for instance, by temporary bindings such as comprehension values.
// It returns v itself if v does not point to another Vertex.
func (v *Vertex) Indirect() *Vertex {
	for {
		arc, ok := v.BaseValue.(*Vertex)
		if !ok {
			return v
		}
		v = arc
	}
}

// OptionalType is a bit field of the type of optional constraints in use by an
// Acceptor.
type OptionalType int8

const (
	HasField          OptionalType = 1 << iota // X: T
	HasDynamic                                 // (X): T or "\(X)": T
	HasPattern                                 // [X]: T
	HasComplexPattern                          // anything but a basic type
	HasAdditional                              // ...T
	IsOpen                                     // Defined for all fields
)

func (v *Vertex) Kind() Kind {
	// This is possible when evaluating comprehensions. It is potentially
	// not known at this time what the type is.
	switch {
	case v.state != nil:
		return v.state.kind
	case v.BaseValue == nil:
		return TopKind
	default:
		return v.BaseValue.Kind()
	}
}

func (v *Vertex) OptionalTypes() OptionalType {
	var mask OptionalType
	for _, s := range v.Structs {
		mask |= s.OptionalTypes()
	}
	return mask
}

// IsOptional reports whether a field is explicitly defined as optional,
// as opposed to whether it is allowed by a pattern constraint.
func (v *Vertex) IsOptional(label Feature) bool {
	for _, s := range v.Structs {
		if s.IsOptional(label) {
			return true
		}
	}
	return false
}

func (v *Vertex) accepts(ok, required bool) bool {
	return ok || (!required && !v.Closed)
}

func (v *Vertex) IsClosedStruct() bool {
	switch x := v.BaseValue.(type) {
	default:
		return false

	case *StructMarker:
		if x.NeedClose {
			return true
		}

	case *Disjunction:
	}
	return v.Closed || isClosed(v)
}

func (v *Vertex) IsClosedList() bool {
	if x, ok := v.BaseValue.(*ListMarker); ok {
		return !x.IsOpen
	}
	return false
}

// TODO: return error instead of boolean? (or at least have version that does.)
func (v *Vertex) Accept(ctx *OpContext, f Feature) bool {
	if f.IsInt() {
		switch x := v.BaseValue.(type) {
		case *ListMarker:
			// TODO(perf): use precomputed length.
			if f.Index() < len(v.Elems()) {
				return true
			}
			return !v.IsClosedList()

		case *Disjunction:
			for _, v := range x.Values {
				if v.Accept(ctx, f) {
					return true
				}
			}
			return false

		default:
			return v.Kind()&ListKind != 0
		}
	}

	if k := v.Kind(); k&StructKind == 0 && f.IsString() {
		// If the value is bottom, we may not really know if this used to
		// be a struct.
		if k != BottomKind || len(v.Structs) == 0 {
			return false
		}
	}

	if f.IsHidden() || !v.IsClosedStruct() || v.Lookup(f) != nil {
		return true
	}

	// TODO(perf): collect positions in error.
	defer ctx.ReleasePositions(ctx.MarkPositions())

	return v.accepts(Accept(ctx, v, f))
}

// MatchAndInsert finds the conjuncts for optional fields, pattern
// constraints, and additional constraints that match f and inserts them in
// arc. Use f is 0 to match all additional constraints only.
func (v *Vertex) MatchAndInsert(ctx *OpContext, arc *Vertex) {
	if !v.Accept(ctx, arc.Label) {
		return
	}

	// Go backwards to simulate old implementation.
	for i := len(v.Structs) - 1; i >= 0; i-- {
		s := v.Structs[i]
		if s.Disable {
			continue
		}
		s.MatchAndInsert(ctx, arc)
	}
}

func (v *Vertex) IsList() bool {
	_, ok := v.BaseValue.(*ListMarker)
	return ok
}

// Lookup returns the Arc with label f if it exists or nil otherwise.
func (v *Vertex) Lookup(f Feature) *Vertex {
	for _, a := range v.Arcs {
		if a.Label == f {
			a = a.Indirect()
			return a
		}
	}
	return nil
}

// Elems returns the regular elements of a list.
func (v *Vertex) Elems() []*Vertex {
	// TODO: add bookkeeping for where list arcs start and end.
	a := make([]*Vertex, 0, len(v.Arcs))
	for _, x := range v.Arcs {
		if x.Label.IsInt() {
			a = append(a, x)
		}
	}
	return a
}

// GetArc returns a Vertex for the outgoing arc with label f. It creates and
// ads one if it doesn't yet exist.
func (v *Vertex) GetArc(c *OpContext, f Feature) (arc *Vertex, isNew bool) {
	arc = v.Lookup(f)
	if arc == nil {
		for _, a := range v.state.usedArcs {
			if a.Label == f {
				arc = a
				v.Arcs = append(v.Arcs, arc)
				isNew = true
				if c.nonMonotonicInsertNest > 0 {
					a.nonMonotonicInsertGen = c.nonMonotonicGeneration
				}
				break
			}
		}
	}
	if arc == nil {
		arc = &Vertex{Parent: v, Label: f}
		v.Arcs = append(v.Arcs, arc)
		isNew = true
		if c.nonMonotonicInsertNest > 0 {
			arc.nonMonotonicInsertGen = c.nonMonotonicGeneration
		}
	}
	if c.nonMonotonicInsertNest == 0 {
		arc.nonMonotonicInsertGen = 0
	}
	return arc, isNew
}

func (v *Vertex) Source() ast.Node {
	if v != nil {
		if b, ok := v.BaseValue.(Value); ok {
			return b.Source()
		}
	}
	return nil
}

// AddConjunct adds the given Conjuncts to v if it doesn't already exist.
func (v *Vertex) AddConjunct(c Conjunct) *Bottom {
	if v.BaseValue != nil {
		// TODO: investigate why this happens at all. Removing it seems to
		// change the order of fields in some cases.
		//
		// This is likely a bug in the evaluator and should not happen.
		return &Bottom{Err: errors.Newf(token.NoPos, "cannot add conjunct")}
	}
	v.addConjunct(c)
	return nil
}

func (v *Vertex) addConjunct(c Conjunct) {
	for _, x := range v.Conjuncts {
		if x == c {
			return
		}
	}
	v.Conjuncts = append(v.Conjuncts, c)
}

func (v *Vertex) AddStruct(s *StructLit, env *Environment, ci CloseInfo) *StructInfo {
	info := StructInfo{
		StructLit: s,
		Env:       env,
		CloseInfo: ci,
	}
	for _, t := range v.Structs {
		if *t == info {
			return t
		}
	}
	t := &info
	v.Structs = append(v.Structs, t)
	return t
}

// Path computes the sequence of Features leading from the root to of the
// instance to this Vertex.
//
// NOTE: this is for debugging purposes only.
func (v *Vertex) Path() []Feature {
	return appendPath(nil, v)
}

func appendPath(a []Feature, v *Vertex) []Feature {
	if v.Parent == nil {
		return a
	}
	a = appendPath(a, v.Parent)
	if v.Label != 0 {
		// A Label may be 0 for programmatically inserted nodes.
		a = append(a, v.Label)
	}
	return a
}

// An Conjunct is an Environment-Expr pair. The Environment is the starting
// point for reference lookup for any reference contained in X.
type Conjunct struct {
	Env *Environment
	x   Node

	// CloseInfo is a unique number that tracks a group of conjuncts that need
	// belong to a single originating definition.
	CloseInfo CloseInfo
}

// TODO(perf): replace with composite literal if this helps performance.

// MakeRootConjunct creates a conjunct from the given environment and node.
// It panics if x cannot be used as an expression.
func MakeRootConjunct(env *Environment, x Node) Conjunct {
	return MakeConjunct(env, x, CloseInfo{})
}

func MakeConjunct(env *Environment, x Node, id CloseInfo) Conjunct {
	if env == nil {
		// TODO: better is to pass one.
		env = &Environment{}
	}
	switch x.(type) {
	case Expr, interface{ expr() Expr }:
	default:
		panic(fmt.Sprintf("invalid Node type %T", x))
	}
	return Conjunct{env, x, id}
}

func (c *Conjunct) Source() ast.Node {
	return c.x.Source()
}

func (c *Conjunct) Field() Node {
	return c.x
}

func (c *Conjunct) Expr() Expr {
	switch x := c.x.(type) {
	case Expr:
		return x
	case interface{ expr() Expr }:
		return x.expr()
	default:
		panic("unreachable")
	}
}
