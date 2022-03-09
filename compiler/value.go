package compiler

import (
	"errors"
	"path"
	"path/filepath"
	"sort"
	"strconv"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cueformat "cuelang.org/go/cue/format"
)

// Value is a wrapper around cue.Value.
// Use instead of cue.Value and cue.Instance
type Value struct {
	val cue.Value
	cc  *Compiler
}

// FillPath fills the value in-place
func (v *Value) FillPath(p cue.Path, x interface{}) error {
	v.cc.lock()
	defer v.cc.unlock()

	// If calling Fill() with a Value, we want to use the underlying
	// cue.Value to fill.
	if val, ok := x.(*Value); ok {
		v.val = v.val.FillPath(p, val.val)
	} else {
		v.val = v.val.FillPath(p, x)
	}
	return v.val.Err()
}

// FillFields fills multiple fields, in place
func (v *Value) FillFields(values map[string]interface{}) (*Value, error) {
	for p, x := range values {
		if err := v.FillPath(cue.ParsePath(p), x); err != nil {
			return nil, err
		}
	}

	return v, nil
}

// Fill updates a value, in place
func (v *Value) Fill(value interface{}) (*Value, error) {
	if err := v.FillPath(cue.MakePath(), value); err != nil {
		return nil, err
	}
	return v, nil
}

// LookupPath is a concurrency safe wrapper around cue.Value.LookupPath
func (v *Value) LookupPath(p cue.Path) *Value {
	v.cc.rlock()
	defer v.cc.runlock()

	return v.cc.Wrap(v.val.LookupPath(p))
}

// Lookup is a helper function to lookup by path parts.
func (v *Value) Lookup(path string) *Value {
	return v.LookupPath(cue.ParsePath(path))
}

func (v *Value) ReferencePath() (*Value, cue.Path) {
	vv, p := v.val.ReferencePath()
	return v.cc.Wrap(vv), p
}

// Proxy function to the underlying cue.Value
func (v *Value) Len() cue.Value {
	return v.val.Len()
}

// Proxy function to the underlying cue.Value
func (v *Value) Kind() cue.Kind {
	return v.val.Kind()
}

// Proxy function to the underlying cue.Value
func (v *Value) IncompleteKind() cue.Kind {
	return v.Cue().IncompleteKind()
}

// Field represents a struct field
type Field struct {
	Selector cue.Selector
	Value    *Value
}

// Label returns the unquoted selector
func (f Field) Label() string {
	l := f.Selector.String()
	if unquoted, err := strconv.Unquote(l); err == nil {
		return unquoted
	}
	return l
}

// Proxy function to the underlying cue.Value
// Field ordering is guaranteed to be stable.
func (v *Value) Fields(opts ...cue.Option) ([]Field, error) {
	it, err := v.val.Fields(opts...)
	if err != nil {
		return nil, err
	}

	fields := []Field{}
	for it.Next() {
		fields = append(fields, Field{
			Selector: it.Selector(),
			Value:    v.cc.Wrap(it.Value()),
		})
	}

	sort.SliceStable(fields, func(i, j int) bool {
		return fields[i].Selector.String() < fields[j].Selector.String()
	})

	return fields, nil
}

// Proxy function to the underlying cue.Value
func (v *Value) Struct() (*cue.Struct, error) {
	return v.val.Struct()
}

// Proxy function to the underlying cue.Value
func (v *Value) Exists() bool {
	return v.val.Exists()
}

// Proxy function to the underlying cue.Value
func (v *Value) Bytes() ([]byte, error) {
	return v.val.Bytes()
}

// Proxy function to the underlying cue.Value
func (v *Value) String() (string, error) {
	return v.val.String()
}

// Proxy function to the underlying cue.Value
func (v *Value) Int64() (int64, error) {
	return v.val.Int64()
}

// Proxy function to the underlying cue.Value
func (v *Value) Bool() (bool, error) {
	return v.val.Bool()
}

// Proxy function to the underlying cue.Value
func (v *Value) Path() cue.Path {
	return v.val.Path()
}

// Proxy function to the underlying cue.Value
func (v *Value) Decode(x interface{}) error {
	return v.val.Decode(x)
}

func (v *Value) List() ([]*Value, error) {
	l := []*Value{}
	it, err := v.val.List()
	if err != nil {
		return nil, err
	}
	for it.Next() {
		l = append(l, v.cc.Wrap(it.Value()))
	}

	return l, nil
}

func (v *Value) IsConcrete() bool {
	return v.val.IsConcrete()
}

// Recursive concreteness check.
func (v *Value) IsConcreteR(opts ...cue.Option) error {
	o := []cue.Option{
		cue.All(),
		cue.Concrete(true),
		cue.Hidden(true),
	}
	o = append(o, opts...)
	return v.val.Validate(o...)
}

func (v *Value) Walk(before func(*Value) bool, after func(*Value)) {
	// FIXME: this is a long lock
	v.cc.rlock()
	defer v.cc.runlock()

	var (
		llBefore func(cue.Value) bool
		llAfter  func(cue.Value)
	)
	if before != nil {
		llBefore = func(child cue.Value) bool {
			return before(v.cc.Wrap(child))
		}
	}
	if after != nil {
		llAfter = func(child cue.Value) {
			after(v.cc.Wrap(child))
		}
	}
	v.val.Walk(llBefore, llAfter)
}

// Export concrete values to JSON. ignoring non-concrete values.
// Contrast with cue.Value.MarshalJSON which requires all values
// to be concrete.
func (v *Value) JSON() JSON {
	cuePathToStrings := func(p cue.Path) []string {
		selectors := p.Selectors()
		out := make([]string, len(selectors))
		for i, sel := range selectors {
			out[i] = sel.String()
		}
		return out
	}

	var out JSON
	v.val.Walk(
		func(v cue.Value) bool {
			b, err := v.MarshalJSON()
			if err == nil {
				newOut, err := out.Set(b, cuePathToStrings(v.Path())...)
				if err == nil {
					out = newOut
				}
				return false
			}
			return true
		},
		nil,
	)
	out, _ = out.Get(cuePathToStrings(v.Path())...)
	return out
}

// Check that a value is valid. Optionally check that it matches
// all the specified spec definitions..
func (v *Value) Validate() error {
	return v.val.Validate()
}

// Return cue source for this value
func (v *Value) Source(opts ...cue.Option) ([]byte, error) {
	v.cc.rlock()
	defer v.cc.runlock()

	return cueformat.Node(v.val.Eval().Syntax(opts...),
		cueformat.UseSpaces(4),
		cueformat.TabIndent(false),
	)
}

func (v *Value) Cue() cue.Value {
	return v.val
}

// Returns true if value has a dagger attribute (eg. artifact, secret, input)
func (v *Value) HasAttr(filter ...string) bool {
	attrs := v.val.Attributes(cue.ValueAttr)

	for _, attr := range attrs {
		name := attr.Name()
		// match `@dagger(...)`
		if name == "dagger" {
			// did not provide filter, match any @dagger attr
			if len(filter) == 0 {
				return true
			}

			// loop over args (CSV content in attribute)
			for i := 0; i < attr.NumArgs(); i++ {
				key, _ := attr.Arg(i)
				// one or several values where provided, filter
				for _, val := range filter {
					if key == val {
						return true
					}
				}
			}
		}
	}

	return false
}

// Filename returns the CUE filename where the value was defined
func (v *Value) Filename() (string, error) {
	pos := v.Cue().Pos()
	if !pos.IsValid() {
		return "", errors.New("invalid token position")
	}
	return pos.Filename(), nil
}

// Dirname returns the CUE dirname where the value was defined
func (v *Value) Dirname() (string, error) {
	f, err := v.Filename()
	if err != nil {
		return "", err
	}
	return filepath.Dir(f), nil
}

// AbsPath returns an absolute path contained in Value
// Paths are relative to the CUE file they were declared in.
func (v *Value) AbsPath() (string, error) {
	p, err := v.String()
	if err != nil {
		return "", nil
	}

	if filepath.IsAbs(p) {
		return p, nil
	}

	d, err := v.Dirname()
	if err != nil {
		return "", err
	}
	return path.Join(d, p), nil
}

func (v *Value) Dereference() *Value {
	dVal := cue.Dereference(v.val)
	return v.cc.Wrap(dVal)
}

func (v *Value) Default() (*Value, bool) {
	val, hasDef := v.val.Default()
	return v.cc.Wrap(val), hasDef
}

func (v *Value) Doc() []*ast.CommentGroup {
	return v.Cue().Doc()
}
