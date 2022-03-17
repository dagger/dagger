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

// walk provides functions for visiting the nodes of an ADT tree.
package walk

import (
	"fmt"

	"cuelang.org/go/internal/core/adt"
)

// Features calls f for all features used in x and indicates whether the
// feature is used as a reference or not.
func Features(x adt.Expr, f func(label adt.Feature, src adt.Node)) {
	w := Visitor{
		Feature: f,
	}
	w.Expr(x)
}

type Visitor struct {
	// TODO: lets really should be special fields
	letDone map[adt.Expr]bool

	Feature func(f adt.Feature, src adt.Node)
	Before  func(adt.Node) bool
}

func (w *Visitor) init() {
	if w.letDone == nil {
		w.letDone = map[adt.Expr]bool{}
	}
}

func (w *Visitor) Expr(x adt.Expr) {
	w.init()
	w.node(x)
}

func (w *Visitor) feature(x adt.Feature, src adt.Node) {
	if w.Feature != nil {
		w.Feature(x, src)
	}
}

func (w *Visitor) node(n adt.Node) {
	if w.Before != nil && !w.Before(n) {
		return
	}

	switch x := n.(type) {
	case nil:

	// TODO: special-case Vertex?
	case adt.Value:

	case *adt.ListLit:
		for _, x := range x.Elems {
			w.node(x)
		}

	case *adt.StructLit:
		for _, x := range x.Decls {
			w.node(x)
		}

	case *adt.FieldReference:
		w.feature(x.Label, x)

	case *adt.ValueReference:
		w.feature(x.Label, x)

	case *adt.LabelReference:

	case *adt.DynamicReference:

	case *adt.ImportReference:
		w.feature(x.ImportPath, x)
		w.feature(x.Label, x)

	case *adt.LetReference:
		w.feature(x.Label, x)
		if w.letDone == nil {
			w.letDone = map[adt.Expr]bool{}
		}
		if !w.letDone[x.X] {
			w.letDone[x.X] = true
			w.node(x.X)
		}

	case *adt.SelectorExpr:
		w.node(x.X)
		w.feature(x.Sel, x)

	case *adt.IndexExpr:
		w.node(x.X)
		w.node(x.Index)

	case *adt.SliceExpr:
		w.node(x.X)
		w.node(x.Lo)
		w.node(x.Hi)
		w.node(x.Stride)

	case *adt.Interpolation:
		for _, x := range x.Parts {
			w.node(x)
		}

	case *adt.BoundExpr:
		w.node(x.Expr)

	case *adt.UnaryExpr:
		w.node(x.X)

	case *adt.BinaryExpr:
		w.node(x.X)
		w.node(x.Y)

	case *adt.CallExpr:
		w.node(x.Fun)
		for _, arg := range x.Args {
			w.node(arg)
		}

	case *adt.DisjunctionExpr:
		for _, d := range x.Values {
			w.node(d.Val)
		}

	// Fields

	case *adt.Ellipsis:
		if x.Value != nil {
			w.node(x.Value)
		}

	case *adt.Field:
		w.feature(x.Label, x)
		w.node(x.Value)

	case *adt.OptionalField:
		w.feature(x.Label, x)
		w.node(x.Value)

	case *adt.BulkOptionalField:
		w.node(x.Filter)
		w.node(x.Value)

	case *adt.DynamicField:
		w.node(x.Key)
		w.node(x.Value)

	// Yielders

	case *adt.ForClause:
		w.feature(x.Key, x)
		w.feature(x.Value, x)
		w.node(x.Dst)

	case *adt.IfClause:
		w.node(x.Condition)
		w.node(x.Dst)

	case *adt.LetClause:
		w.feature(x.Label, x)
		w.node(x.Expr)
		w.node(x.Dst)

	case *adt.ValueClause:
		w.node(x.StructLit)

	default:
		panic(fmt.Sprintf("unknown field %T", x))
	}
}
