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

// Package dep analyzes dependencies between values.
package dep

import (
	"errors"

	"cuelang.org/go/internal/core/adt"
)

// A Dependency is a reference and the node that reference resolves to.
type Dependency struct {
	// Node is the referenced node.
	Node *adt.Vertex

	// Reference is the expression that referenced the node.
	Reference adt.Resolver

	top bool
}

// Import returns the import reference or nil if the reference was within
// the same package as the visited Vertex.
func (d *Dependency) Import() *adt.ImportReference {
	x, _ := d.Reference.(adt.Expr)
	return importRef(x)
}

// IsRoot reports whether the dependency is referenced by the root of the
// original Vertex passed to any of the Visit* functions, and not one of its
// descendent arcs. This always returns true for Visit().
func (d *Dependency) IsRoot() bool {
	return d.top
}

func (d *Dependency) Path() []adt.Feature {
	return nil
}

func importRef(r adt.Expr) *adt.ImportReference {
	switch x := r.(type) {
	case *adt.ImportReference:
		return x
	case *adt.SelectorExpr:
		return importRef(x.X)
	case *adt.IndexExpr:
		return importRef(x.X)
	}
	return nil
}

// VisitFunc is used for reporting dependencies.
type VisitFunc func(Dependency) error

// Visit calls f for all vertices referenced by the conjuncts of n without
// descending into the elements of list or fields of structs. Only references
// that do not refer to the conjuncts of n itself are reported.
func Visit(c *adt.OpContext, n *adt.Vertex, f VisitFunc) error {
	return visit(c, n, f, false, true)
}

// VisitAll calls f for all vertices referenced by the conjuncts of n including
// those of descendant fields and elements. Only references that do not refer to
// the conjuncts of n itself are reported.
func VisitAll(c *adt.OpContext, n *adt.Vertex, f VisitFunc) error {
	return visit(c, n, f, true, true)
}

// VisitFields calls f for n and all its descendent arcs that have a conjunct
// that originates from a conjunct in n. Only the conjuncts of n that ended up
// as a conjunct in an actual field are visited and they are visited for each
// field in which the occurs.
func VisitFields(c *adt.OpContext, n *adt.Vertex, f VisitFunc) error {
	m := marked{}

	m.markExpr(n)

	dynamic(c, n, f, m, true)
	return nil
}

var empty *adt.Vertex

func init() {
	empty = &adt.Vertex{}
	empty.UpdateStatus(adt.Finalized)
}

func visit(c *adt.OpContext, n *adt.Vertex, f VisitFunc, all, top bool) (err error) {
	if c == nil {
		panic("nil context")
	}
	v := visitor{
		ctxt:  c,
		visit: f,
		node:  n,
		all:   all,
		top:   top,
	}

	defer func() {
		switch x := recover(); x {
		case nil:
		case aborted:
			err = v.err
		default:
			panic(x)
		}
	}()

	for _, x := range n.Conjuncts {
		v.markExpr(x.Env, x.Expr())
	}

	return nil
}

var aborted = errors.New("aborted")

type visitor struct {
	ctxt  *adt.OpContext
	visit VisitFunc
	node  *adt.Vertex
	err   error
	all   bool
	top   bool
}

// TODO: factor out the below logic as either a low-level dependency analyzer or
// some walk functionality.

// markExpr visits all nodes in an expression to mark dependencies.
func (c *visitor) markExpr(env *adt.Environment, expr adt.Expr) {
	switch x := expr.(type) {
	case nil:
	case adt.Resolver:
		c.markResolver(env, x)

	case *adt.BinaryExpr:
		c.markExpr(env, x.X)
		c.markExpr(env, x.Y)

	case *adt.UnaryExpr:
		c.markExpr(env, x.X)

	case *adt.Interpolation:
		for i := 1; i < len(x.Parts); i += 2 {
			c.markExpr(env, x.Parts[i])
		}

	case *adt.BoundExpr:
		c.markExpr(env, x.Expr)

	case *adt.CallExpr:
		c.markExpr(env, x.Fun)
		saved := c.all
		c.all = true
		for _, a := range x.Args {
			c.markExpr(env, a)
		}
		c.all = saved

	case *adt.DisjunctionExpr:
		for _, d := range x.Values {
			c.markExpr(env, d.Val)
		}

	case *adt.SliceExpr:
		c.markExpr(env, x.X)
		c.markExpr(env, x.Lo)
		c.markExpr(env, x.Hi)
		c.markExpr(env, x.Stride)

	case *adt.ListLit:
		env := &adt.Environment{Up: env, Vertex: empty}
		for _, e := range x.Elems {
			switch x := e.(type) {
			case adt.Yielder:
				c.markYielder(env, x)

			case adt.Expr:
				c.markSubExpr(env, x)

			case *adt.Ellipsis:
				if x.Value != nil {
					c.markSubExpr(env, x.Value)
				}
			}
		}

	case *adt.StructLit:
		env := &adt.Environment{Up: env, Vertex: empty}
		for _, e := range x.Decls {
			c.markDecl(env, e)
		}
	}
}

// markResolve resolves dependencies.
func (c *visitor) markResolver(env *adt.Environment, r adt.Resolver) {
	switch x := r.(type) {
	case nil:
	case *adt.LetReference:
		saved := c.ctxt.PushState(env, nil)
		env := c.ctxt.Env(x.UpCount)
		c.markExpr(env, x.X)
		c.ctxt.PopState(saved)
		return
	}

	if ref, _ := c.ctxt.Resolve(env, r); ref != nil {
		if ref != c.node {
			d := Dependency{
				Node:      ref,
				Reference: r,
				top:       c.top,
			}
			if err := c.visit(d); err != nil {
				c.err = err
				panic(aborted)
			}
		}

		return
	}

	// It is possible that a reference cannot be resolved because it is
	// incomplete. In this case, we should check whether subexpressions of the
	// reference can be resolved to mark those dependencies. For instance,
	// prefix paths of selectors and the value or index of an index experssion
	// may independently resolve to a valid dependency.

	switch x := r.(type) {
	case *adt.NodeLink:
		panic("unreachable")

	case *adt.IndexExpr:
		c.markExpr(env, x.X)
		c.markExpr(env, x.Index)

	case *adt.SelectorExpr:
		c.markExpr(env, x.X)
	}
}

func (c *visitor) markSubExpr(env *adt.Environment, x adt.Expr) {
	if c.all {
		saved := c.top
		c.top = false
		c.markExpr(env, x)
		c.top = saved
	}
}

func (c *visitor) markDecl(env *adt.Environment, d adt.Decl) {
	switch x := d.(type) {
	case *adt.Field:
		c.markSubExpr(env, x.Value)

	case *adt.OptionalField:
		// when dynamic, only continue if there is evidence of
		// the field in the parallel actual evaluation.
		c.markSubExpr(env, x.Value)

	case *adt.BulkOptionalField:
		c.markExpr(env, x.Filter)
		// when dynamic, only continue if there is evidence of
		// the field in the parallel actual evaluation.
		c.markSubExpr(env, x.Value)

	case *adt.DynamicField:
		c.markExpr(env, x.Key)
		// when dynamic, only continue if there is evidence of
		// a matching field in the parallel actual evaluation.
		c.markSubExpr(env, x.Value)

	case adt.Yielder:
		c.markYielder(env, x)

	case adt.Expr:
		c.markExpr(env, x)

	case *adt.Ellipsis:
		if x.Value != nil {
			c.markSubExpr(env, x.Value)
		}
	}
}

func (c *visitor) markYielder(env *adt.Environment, y adt.Yielder) {
	switch x := y.(type) {
	case *adt.ForClause:
		c.markExpr(env, x.Src)
		env := &adt.Environment{Up: env, Vertex: empty}
		c.markYielder(env, x.Dst)
		// In dynamic mode, iterate over all actual value and
		// evaluate.

	case *adt.LetClause:
		c.markExpr(env, x.Expr)
		env := &adt.Environment{Up: env, Vertex: empty}
		c.markYielder(env, x.Dst)

	case *adt.IfClause:
		c.markExpr(env, x.Condition)
		// In dynamic mode, only continue if condition is true.
		c.markYielder(env, x.Dst)

	case *adt.ValueClause:
		c.markExpr(env, x.StructLit)
	}
}
