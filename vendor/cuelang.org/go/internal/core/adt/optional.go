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

// MatchAndInsert finds matching optional parts for a given Arc and adds its
// conjuncts. Bulk fields are only applied if no fields match, and additional
// constraints are only added if neither regular nor bulk fields match.
func (o *StructInfo) MatchAndInsert(c *OpContext, arc *Vertex) {
	env := o.Env

	closeInfo := o.CloseInfo
	closeInfo.IsClosed = false

	// Match normal fields
	matched := false
outer:
	for _, f := range o.Fields {
		if f.Label == arc.Label {
			for _, e := range f.Optional {
				arc.AddConjunct(MakeConjunct(env, e, closeInfo))
			}
			matched = true
			break outer
		}
	}

	f := arc.Label
	if !f.IsRegular() {
		return
	}
	var label Value

	if int64(f.Index()) == MaxIndex {
		f = 0
	} else if o.types&HasComplexPattern != 0 && f.IsString() {
		label = f.ToValue(c)
	}

	if len(o.Bulk) > 0 {
		bulkEnv := *env
		bulkEnv.DynamicLabel = f
		bulkEnv.Deref = nil
		bulkEnv.Cycles = nil

		// match bulk optional fields / pattern properties
		for _, b := range o.Bulk {
			// if matched && f.additional {
			// 	continue
			// }
			if matchBulk(c, env, b, f, label) {
				matched = true
				info := closeInfo.SpawnSpan(b.Value, ConstraintSpan)
				arc.AddConjunct(MakeConjunct(&bulkEnv, b, info))
			}
		}
	}

	if matched || len(o.Additional) == 0 {
		return
	}

	addEnv := *env
	addEnv.Deref = nil
	addEnv.Cycles = nil

	// match others
	for _, x := range o.Additional {
		info := closeInfo
		if _, ok := x.(*Top); !ok {
			info = info.SpawnSpan(x, ConstraintSpan)
		}
		arc.AddConjunct(MakeConjunct(&addEnv, x, info))
	}
}

// matchBulk reports whether feature f matches the filter of x. It evaluation of
// the filter is erroneous, it returns false and the error will  be set in c.
func matchBulk(c *OpContext, env *Environment, x *BulkOptionalField, f Feature, label Value) bool {
	v := env.evalCached(c, x.Filter)
	v = Unwrap(v)

	// Fast-track certain cases.
	switch x := v.(type) {
	case *Bottom:
		if c.errs == nil {
			c.AddBottom(x)
		}
		return false
	case *Top:
		return true

	case *BasicType:
		return x.K&StringKind != 0

	case *BoundValue:
		switch x.Kind() {
		case StringKind:
			if label == nil {
				return false
			}
			str := label.(*String).Str
			return x.validateStr(c, str)

		case IntKind:
			return x.validateInt(c, int64(f.Index()))
		}
	}

	if label == nil {
		return false
	}

	n := Vertex{}
	m := MakeRootConjunct(env, v)
	n.AddConjunct(m)
	n.AddConjunct(MakeRootConjunct(m.Env, label))

	c.inConstraint++
	n.Finalize(c)
	c.inConstraint--

	b, _ := n.BaseValue.(*Bottom)
	return b == nil
}
