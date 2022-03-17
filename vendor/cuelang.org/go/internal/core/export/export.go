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

package export

import (
	"fmt"
	"math/rand"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/internal"
	"cuelang.org/go/internal/core/adt"
	"cuelang.org/go/internal/core/eval"
	"cuelang.org/go/internal/core/walk"
)

const debug = false

type Profile struct {
	Simplify bool

	// Final reports incomplete errors as errors.
	Final bool

	// TakeDefaults is used in Value mode to drop non-default values.
	TakeDefaults bool

	ShowOptional    bool
	ShowDefinitions bool

	// ShowHidden forces the inclusion of hidden fields when these would
	// otherwise be omitted. Only hidden fields from the current package are
	// included.
	ShowHidden     bool
	ShowDocs       bool
	ShowAttributes bool

	// ShowErrors treats errors as values and will not percolate errors up.
	//
	// TODO: convert this option to an error level instead, showing only
	// errors below a certain severity.
	ShowErrors bool

	// Use unevaluated conjuncts for these error types
	// IgnoreRecursive

	// TODO: recurse over entire tree to determine transitive closure
	// of what needs to be printed.
	// IncludeDependencies bool
}

var Simplified = &Profile{
	Simplify: true,
	ShowDocs: true,
}

var Final = &Profile{
	Simplify:     true,
	TakeDefaults: true,
	Final:        true,
}

var Raw = &Profile{
	ShowOptional:    true,
	ShowDefinitions: true,
	ShowHidden:      true,
	ShowDocs:        true,
}

var All = &Profile{
	Simplify:        true,
	ShowOptional:    true,
	ShowDefinitions: true,
	ShowHidden:      true,
	ShowDocs:        true,
	ShowAttributes:  true,
}

// Concrete

// Def exports v as a definition.
func Def(r adt.Runtime, pkgID string, v *adt.Vertex) (*ast.File, errors.Error) {
	return All.Def(r, pkgID, v)
}

// Def exports v as a definition.
func (p *Profile) Def(r adt.Runtime, pkgID string, v *adt.Vertex) (*ast.File, errors.Error) {
	e := newExporter(p, r, pkgID, v)
	e.markUsedFeatures(v)

	isDef := v.IsRecursivelyClosed()
	if isDef {
		e.inDefinition++
	}

	expr := e.expr(v)

	if isDef {
		e.inDefinition--
		if v.Kind() == adt.StructKind {
			expr = ast.NewStruct(
				ast.Embed(ast.NewIdent("_#def")),
				ast.NewIdent("_#def"), expr,
			)
		}
	}
	return e.toFile(v, expr)
}

func Expr(r adt.Runtime, pkgID string, n adt.Expr) (ast.Expr, errors.Error) {
	return Simplified.Expr(r, pkgID, n)
}

func (p *Profile) Expr(r adt.Runtime, pkgID string, n adt.Expr) (ast.Expr, errors.Error) {
	e := newExporter(p, r, pkgID, nil)
	e.markUsedFeatures(n)

	return e.expr(n), nil
}

func (e *exporter) toFile(v *adt.Vertex, x ast.Expr) (*ast.File, errors.Error) {
	f := &ast.File{}

	pkgName := ""
	pkg := &ast.Package{}
	for _, c := range v.Conjuncts {
		f, _ := c.Source().(*ast.File)
		if f == nil {
			continue
		}

		if _, name, _ := internal.PackageInfo(f); name != "" {
			pkgName = name
		}

		if e.cfg.ShowDocs {
			if doc := internal.FileComment(f); doc != nil {
				ast.AddComment(pkg, doc)
			}
		}
	}

	if pkgName != "" {
		pkg.Name = ast.NewIdent(pkgName)
		f.Decls = append(f.Decls, pkg)
	}

	switch st := x.(type) {
	case nil:
		panic("null input")

	case *ast.StructLit:
		f.Decls = append(f.Decls, st.Elts...)

	default:
		f.Decls = append(f.Decls, &ast.EmbedDecl{Expr: x})
	}
	if err := astutil.Sanitize(f); err != nil {
		err := errors.Promote(err, "export")
		return f, errors.Append(e.errs, err)
	}

	return f, nil
}

// File

func Vertex(r adt.Runtime, pkgID string, n *adt.Vertex) (*ast.File, errors.Error) {
	return Simplified.Vertex(r, pkgID, n)
}

func (p *Profile) Vertex(r adt.Runtime, pkgID string, n *adt.Vertex) (*ast.File, errors.Error) {
	e := exporter{
		ctx:   eval.NewContext(r, nil),
		cfg:   p,
		index: r,
		pkgID: pkgID,
	}
	e.markUsedFeatures(n)
	v := e.value(n, n.Conjuncts...)

	return e.toFile(n, v)
}

func Value(r adt.Runtime, pkgID string, n adt.Value) (ast.Expr, errors.Error) {
	return Simplified.Value(r, pkgID, n)
}

// Should take context.
func (p *Profile) Value(r adt.Runtime, pkgID string, n adt.Value) (ast.Expr, errors.Error) {
	e := exporter{
		ctx:   eval.NewContext(r, nil),
		cfg:   p,
		index: r,
		pkgID: pkgID,
	}
	e.markUsedFeatures(n)
	v := e.value(n)
	return v, e.errs
}

type exporter struct {
	cfg  *Profile // Make value todo
	errs errors.Error

	ctx *adt.OpContext

	index adt.StringIndexer
	rand  *rand.Rand

	// For resolving references.
	stack []frame

	inDefinition int // for close() wrapping.

	// hidden label handling
	pkgID  string
	hidden map[string]adt.Feature // adt.InvalidFeatures means more than one.

	// If a used feature maps to an expression, it means it is assigned to a
	// unique let expression.
	usedFeature map[adt.Feature]adt.Expr
	labelAlias  map[adt.Expr]adt.Feature
	valueAlias  map[*ast.Alias]*ast.Alias
	letAlias    map[*ast.LetClause]*ast.LetClause

	usedHidden map[string]bool
}

func newExporter(p *Profile, r adt.Runtime, pkgID string, v *adt.Vertex) *exporter {
	return &exporter{
		cfg:   p,
		ctx:   eval.NewContext(r, v),
		index: r,
		pkgID: pkgID,
	}
}

func (e *exporter) markUsedFeatures(x adt.Expr) {
	e.usedFeature = make(map[adt.Feature]adt.Expr)

	w := &walk.Visitor{}
	w.Before = func(n adt.Node) bool {
		switch x := n.(type) {
		case *adt.Vertex:
			if !x.IsData() {
				for _, c := range x.Conjuncts {
					w.Expr(c.Expr())
				}
			}

		case *adt.DynamicReference:
			if e.labelAlias == nil {
				e.labelAlias = make(map[adt.Expr]adt.Feature)
			}
			// TODO: add preferred label.
			e.labelAlias[x.Label] = adt.InvalidLabel

		case *adt.LabelReference:
		}
		return true
	}

	w.Feature = func(f adt.Feature, src adt.Node) {
		_, ok := e.usedFeature[f]

		switch x := src.(type) {
		case *adt.LetReference:
			if !ok {
				e.usedFeature[f] = x.X
			}

		default:
			e.usedFeature[f] = nil
		}
	}

	w.Expr(x)
}

func (e *exporter) getFieldAlias(f *ast.Field, name string) string {
	a, ok := f.Label.(*ast.Alias)
	if !ok {
		a = &ast.Alias{
			Ident: ast.NewIdent(e.uniqueAlias(name)),
			Expr:  f.Label.(ast.Expr),
		}
		f.Label = a
	}
	return a.Ident.Name
}

func setFieldAlias(f *ast.Field, name string) {
	if _, ok := f.Label.(*ast.Alias); !ok {
		f.Label = &ast.Alias{
			Ident: ast.NewIdent(name),
			Expr:  f.Label.(ast.Expr),
		}
	}
}

func (e *exporter) markLets(n ast.Node) {
	if n == nil {
		return
	}
	ast.Walk(n, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.StructLit:
			e.markLetDecls(v.Elts)
		case *ast.File:
			e.markLetDecls(v.Decls)

		case *ast.Field,
			*ast.LetClause,
			*ast.IfClause,
			*ast.ForClause,
			*ast.Comprehension:
			return false
		}
		return true
	}, nil)
}

func (e *exporter) markLetDecls(decls []ast.Decl) {
	for _, d := range decls {
		if let, ok := d.(*ast.LetClause); ok {
			e.markLetAlias(let)
		}
	}
}

// markLetAlias inserts an uninitialized let clause into the current scope.
// It gets initialized upon first usage.
func (e *exporter) markLetAlias(x *ast.LetClause) {
	// The created let clause is initialized upon first usage, and removed
	// later if never referenced.
	let := &ast.LetClause{}

	if e.letAlias == nil {
		e.letAlias = make(map[*ast.LetClause]*ast.LetClause)
	}
	e.letAlias[x] = let

	scope := e.top().scope
	scope.Elts = append(scope.Elts, let)
}

// In value mode, lets are only used if there wasn't an error.
func filterUnusedLets(s *ast.StructLit) {
	k := 0
	for i, d := range s.Elts {
		if let, ok := d.(*ast.LetClause); ok && let.Expr == nil {
			continue
		}
		s.Elts[k] = s.Elts[i]
		k++
	}
	s.Elts = s.Elts[:k]
}

// resolveLet actually parses the let expression.
// If there was no recorded let expression, it expands the expression in place.
func (e *exporter) resolveLet(x *adt.LetReference) ast.Expr {
	letClause, _ := x.Src.Node.(*ast.LetClause)
	let := e.letAlias[letClause]

	switch {
	case let == nil:
		return e.expr(x.X)

	case let.Expr == nil:
		label := e.uniqueLetIdent(x.Label, x.X)

		let.Ident = e.ident(label)
		let.Expr = e.expr(x.X)
	}

	ident := ast.NewIdent(let.Ident.Name)
	ident.Node = let
	// TODO: set scope?
	return ident
}

func (e *exporter) uniqueLetIdent(f adt.Feature, x adt.Expr) adt.Feature {
	if e.usedFeature[f] == x {
		return f
	}

	f, _ = e.uniqueFeature(f.IdentString(e.ctx))
	e.usedFeature[f] = x
	return f
}

func (e *exporter) uniqueAlias(name string) string {
	f := adt.MakeIdentLabel(e.ctx, name, "")

	if _, ok := e.usedFeature[f]; !ok {
		e.usedFeature[f] = nil
		return name
	}

	_, name = e.uniqueFeature(f.IdentString(e.ctx))
	return name
}

// uniqueFeature returns a name for an identifier that uniquely identifies
// the given expression. If the preferred name is already taken, a new globally
// unique name of the form base_X ... base_XXXXXXXXXXXXXX is generated.
//
// It prefers short extensions over large ones, while ensuring the likelihood of
// fast termination is high. There are at least two digits to make it visually
// clearer this concerns a generated number.
//
func (e *exporter) uniqueFeature(base string) (f adt.Feature, name string) {
	if e.rand == nil {
		e.rand = rand.New(rand.NewSource(808))
	}

	// Try the first few numbers in sequence.
	for i := 1; i < 5; i++ {
		name := fmt.Sprintf("%s_%01X", base, i)
		f := adt.MakeIdentLabel(e.ctx, name, "")
		if _, ok := e.usedFeature[f]; !ok {
			e.usedFeature[f] = nil
			return f, name
		}
	}

	const mask = 0xff_ffff_ffff_ffff // max bits; stay clear of int64 overflow
	const shift = 4                  // rate of growth
	digits := 1
	for n := int64(0x10); ; n = int64(mask&((n<<shift)-1)) + 1 {
		num := e.rand.Intn(int(n)-1) + 1
		name := fmt.Sprintf("%[1]s_%0[2]*[3]X", base, digits, num)
		f := adt.MakeIdentLabel(e.ctx, name, "")
		if _, ok := e.usedFeature[f]; !ok {
			e.usedFeature[f] = nil
			return f, name
		}
		digits++
	}
}

type frame struct {
	scope *ast.StructLit

	docSources []adt.Conjunct

	// For resolving dynamic fields.
	field     *ast.Field
	labelExpr ast.Expr
	upCount   int32 // for off-by-one handling

	// labeled fields
	fields map[adt.Feature]entry

	// field to new field
	mapped map[adt.Node]ast.Node
}

type entry struct {
	alias      string
	field      *ast.Field
	node       ast.Node // How to reference. See astutil.Resolve
	references []*ast.Ident
}

func (e *exporter) addField(label adt.Feature, f *ast.Field, n ast.Node) {
	frame := e.top()
	entry := frame.fields[label]
	entry.field = f
	entry.node = n
	frame.fields[label] = entry
}

func (e *exporter) addEmbed(x ast.Expr) {
	frame := e.top()
	frame.scope.Elts = append(frame.scope.Elts, x)
}

func (e *exporter) pushFrame(conjuncts []adt.Conjunct) (s *ast.StructLit, saved []frame) {
	saved = e.stack
	s = &ast.StructLit{}
	e.stack = append(e.stack, frame{
		scope:      s,
		mapped:     map[adt.Node]ast.Node{},
		fields:     map[adt.Feature]entry{},
		docSources: conjuncts,
	})
	return s, saved
}

func (e *exporter) popFrame(saved []frame) {
	top := e.stack[len(e.stack)-1]

	for _, f := range top.fields {
		node := f.node
		if f.alias != "" && f.field != nil {
			setFieldAlias(f.field, f.alias)
			node = f.field
		}
		for _, r := range f.references {
			r.Node = node
		}
	}

	e.stack = saved
}

func (e *exporter) top() *frame {
	return &(e.stack[len(e.stack)-1])
}

func (e *exporter) frame(upCount int32) *frame {
	for i := len(e.stack) - 1; i >= 0; i-- {
		f := &(e.stack[i])
		if upCount <= (f.upCount - 1) {
			return f
		}
		upCount -= f.upCount
	}
	if debug {
		// This may be valid when exporting incomplete references. These are
		// not yet handled though, so find a way to catch them when debugging
		// printing of values that are supposed to be complete.
		panic("unreachable reference")
	}

	return &frame{}
}

func (e *exporter) setDocs(x adt.Node) {
	f := e.stack[len(e.stack)-1]
	f.docSources = []adt.Conjunct{adt.MakeRootConjunct(nil, x)}
	e.stack[len(e.stack)-1] = f
}

// func (e *Exporter) promise(upCount int32, f completeFunc) {
// 	e.todo = append(e.todo, f)
// }

func (e *exporter) errf(format string, args ...interface{}) *ast.BottomLit {
	err := &exporterError{}
	e.errs = errors.Append(e.errs, err)
	return &ast.BottomLit{}
}

type errTODO errors.Error

type exporterError struct {
	errTODO
}
