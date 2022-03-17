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

package compile

import (
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/internal"
	"cuelang.org/go/internal/astinternal"
	"cuelang.org/go/internal/core/adt"
)

// A Scope represents a nested scope of Vertices.
type Scope interface {
	Parent() Scope
	Vertex() *adt.Vertex
}

// Config configures a compilation.
type Config struct {
	// Scope specifies a node in which to look up unresolved references. This
	// is useful for evaluating expressions within an already evaluated
	// configuration.
	Scope Scope

	// Imports allows unresolved identifiers to resolve to imports.
	//
	// Under normal circumstances, identifiers bind to import specifications,
	// which get resolved to an ImportReference. Use this option to
	// automatically resolve identifiers to imports.
	Imports func(x *ast.Ident) (pkgPath string)

	// pkgPath is used to qualify the scope of hidden fields. The default
	// scope is "_".
	pkgPath string
}

// Files compiles the given files as a single instance. It disregards
// the package names and it is the responsibility of the user to verify that
// the packages names are consistent. The pkgID must be a unique identifier
// for a package in a module, for instance as obtained from build.Instance.ID.
//
// Files may return a completed parse even if it has errors.
func Files(cfg *Config, r adt.Runtime, pkgID string, files ...*ast.File) (*adt.Vertex, errors.Error) {
	c := newCompiler(cfg, pkgID, r)

	v := c.compileFiles(files)

	if c.errs != nil {
		return v, c.errs
	}
	return v, nil
}

// Expr compiles the given expression into a conjunct. The pkgID must be a
// unique identifier for a package in a module, for instance as obtained from
// build.Instance.ID.
func Expr(cfg *Config, r adt.Runtime, pkgPath string, x ast.Expr) (adt.Conjunct, errors.Error) {
	c := newCompiler(cfg, pkgPath, r)

	v := c.compileExpr(x)

	if c.errs != nil {
		return v, c.errs
	}
	return v, nil
}

func newCompiler(cfg *Config, pkgPath string, r adt.Runtime) *compiler {
	c := &compiler{
		index: r,
	}
	if cfg != nil {
		c.Config = *cfg
	}
	if pkgPath == "" {
		pkgPath = "_"
	}
	c.Config.pkgPath = pkgPath
	return c
}

type compiler struct {
	Config
	upCountOffset int32 // 1 for files; 0 for expressions

	index adt.StringIndexer

	stack      []frame
	inSelector int

	fileScope map[adt.Feature]bool

	num literal.NumInfo

	errs errors.Error
}

func (c *compiler) reset() {
	c.fileScope = nil
	c.stack = c.stack[:0]
	c.errs = nil
}

func (c *compiler) errf(n ast.Node, format string, args ...interface{}) *adt.Bottom {
	err := &compilerError{
		n:       n,
		path:    c.path(),
		Message: errors.NewMessage(format, args),
	}
	c.errs = errors.Append(c.errs, err)
	return &adt.Bottom{Err: err}
}

func (c *compiler) path() []string {
	a := []string{}
	for _, f := range c.stack {
		if f.label != nil {
			a = append(a, f.label.labelString())
		}
	}
	return a
}

type frame struct {
	label labeler  // path name leading to this frame.
	scope ast.Node // *ast.File or *ast.Struct
	field *ast.Field
	// scope   map[ast.Node]bool
	upCount int32 // 1 for field, 0 for embedding.

	aliases map[string]aliasEntry
}

type aliasEntry struct {
	label   labeler
	srcExpr ast.Expr
	expr    adt.Expr
	source  ast.Node
	used    bool
}

func (c *compiler) insertAlias(id *ast.Ident, a aliasEntry) *adt.Bottom {
	k := len(c.stack) - 1
	m := c.stack[k].aliases
	if m == nil {
		m = map[string]aliasEntry{}
		c.stack[k].aliases = m
	}

	if id == nil || !ast.IsValidIdent(id.Name) {
		return c.errf(a.source, "invalid identifier name")
	}

	if e, ok := m[id.Name]; ok {
		return c.errf(a.source,
			"alias %q already declared; previous declaration at %s",
			id.Name, e.source.Pos())
	}

	m[id.Name] = a
	return nil
}

func (c *compiler) updateAlias(id *ast.Ident, expr adt.Expr) {
	k := len(c.stack) - 1
	m := c.stack[k].aliases

	x := m[id.Name]
	x.expr = expr
	x.label = nil
	x.srcExpr = nil
	m[id.Name] = x
}

// lookupAlias looks up an alias with the given name at the k'th stack position.
func (c *compiler) lookupAlias(k int, id *ast.Ident) aliasEntry {
	m := c.stack[k].aliases
	name := id.Name
	entry, ok := m[name]

	if !ok {
		err := c.errf(id, "could not find LetClause associated with identifier %q", name)
		return aliasEntry{expr: err}
	}

	switch {
	case entry.label != nil:
		if entry.srcExpr == nil {
			entry.expr = c.errf(id, "cyclic references in let clause or alias")
			break
		}

		src := entry.srcExpr
		entry.srcExpr = nil // mark to allow detecting cycles
		m[name] = entry

		entry.expr = c.labeledExprAt(k, nil, entry.label, src)
		entry.label = nil
	}

	entry.used = true
	m[name] = entry
	return entry
}

func (c *compiler) pushScope(n labeler, upCount int32, id ast.Node) *frame {
	c.stack = append(c.stack, frame{
		label:   n,
		scope:   id,
		upCount: upCount,
	})
	return &c.stack[len(c.stack)-1]
}

func (c *compiler) popScope() {
	k := len(c.stack) - 1
	f := c.stack[k]
	for k, v := range f.aliases {
		if !v.used {
			c.errf(v.source, "unreferenced alias or let clause %s", k)
		}
	}
	c.stack = c.stack[:k]
}

func (c *compiler) compileFiles(a []*ast.File) *adt.Vertex { // Or value?
	c.fileScope = map[adt.Feature]bool{}
	c.upCountOffset = 1

	// TODO(resolve): this is also done in the runtime package, do we need both?

	// Populate file scope to handle unresolved references.
	// Excluded from cross-file resolution are:
	// - import specs
	// - aliases
	// - anything in an anonymous file
	//
	for _, f := range a {
		if p := internal.GetPackageInfo(f); p.IsAnonymous() {
			continue
		}
		for _, d := range f.Decls {
			if f, ok := d.(*ast.Field); ok {
				if id, ok := f.Label.(*ast.Ident); ok {
					c.fileScope[c.label(id)] = true
				}
			}
		}
	}

	// TODO: set doc.
	res := &adt.Vertex{}

	// env := &adt.Environment{Vertex: nil} // runtime: c.runtime

	env := &adt.Environment{}
	top := env

	for p := c.Config.Scope; p != nil; p = p.Parent() {
		top.Vertex = p.Vertex()
		top.Up = &adt.Environment{}
		top = top.Up
	}

	for _, file := range a {
		c.pushScope(nil, 0, file) // File scope
		v := &adt.StructLit{Src: file}
		c.addDecls(v, file.Decls)
		res.Conjuncts = append(res.Conjuncts, adt.MakeRootConjunct(env, v))
		c.popScope()
	}

	return res
}

func (c *compiler) compileExpr(x ast.Expr) adt.Conjunct {
	expr := c.expr(x)

	env := &adt.Environment{}
	top := env

	for p := c.Config.Scope; p != nil; p = p.Parent() {
		top.Vertex = p.Vertex()
		top.Up = &adt.Environment{}
		top = top.Up
	}

	return adt.MakeRootConjunct(env, expr)
}

// resolve assumes that all existing resolutions are legal. Validation should
// be done in a separate step if required.
//
// TODO: collect validation pass to verify that all resolutions are
// legal?
func (c *compiler) resolve(n *ast.Ident) adt.Expr {
	// X in import "path/X"
	// X in import X "path"
	if imp, ok := n.Node.(*ast.ImportSpec); ok {
		return &adt.ImportReference{
			Src:        n,
			ImportPath: c.label(imp.Path),
			Label:      c.label(n),
		}
	}

	label := c.label(n)

	if label == adt.InvalidLabel { // `_`
		return &adt.Top{Src: n}
	}

	// Unresolved field.
	if n.Node == nil {
		upCount := int32(0)
		for _, c := range c.stack {
			upCount += c.upCount
		}
		if c.fileScope[label] {
			return &adt.FieldReference{
				Src:     n,
				UpCount: upCount,
				Label:   label,
			}
		}
		upCount += c.upCountOffset
		for p := c.Scope; p != nil; p = p.Parent() {
			for _, a := range p.Vertex().Arcs {
				if a.Label == label {
					return &adt.FieldReference{
						Src:     n,
						UpCount: upCount,
						Label:   label,
					}
				}
			}
			upCount++
		}

		if c.Config.Imports != nil {
			if pkgPath := c.Config.Imports(n); pkgPath != "" {
				return &adt.ImportReference{
					Src:        n,
					ImportPath: adt.MakeStringLabel(c.index, pkgPath),
					Label:      c.label(n),
				}
			}
		}

		if p := predeclared(n); p != nil {
			return p
		}

		return c.errf(n, "reference %q not found", n.Name)
	}

	//   X in [X=x]: y  Scope: Field  Node: Expr (x)
	//   X in X=[x]: y  Scope: Field  Node: Field
	//   X in x: X=y    Scope: Field  Node: Alias
	if f, ok := n.Scope.(*ast.Field); ok {
		upCount := int32(0)

		k := len(c.stack) - 1
		for ; k >= 0; k-- {
			if c.stack[k].field == f {
				break
			}
			upCount += c.stack[k].upCount
		}

		label := &adt.LabelReference{
			Src:     n,
			UpCount: upCount,
		}

		switch f := n.Node.(type) {
		case *ast.Field:
			_ = c.lookupAlias(k, f.Label.(*ast.Alias).Ident) // mark as used
			return &adt.DynamicReference{
				Src:     n,
				UpCount: upCount,
				Label:   label,
			}

		case *ast.Alias:
			_ = c.lookupAlias(k, f.Ident) // mark as used
			return &adt.ValueReference{
				Src:     n,
				UpCount: upCount,
				Label:   c.label(f.Ident),
			}
		}
		return label
	}

	upCount := int32(0)

	k := len(c.stack) - 1
	for ; k >= 0; k-- {
		if c.stack[k].scope == n.Scope {
			break
		}
		upCount += c.stack[k].upCount
	}
	if k < 0 {
		// This is a programmatic error and should never happen if the users
		// just builds with the cue command or if astutil.Resolve is used
		// correctly.
		c.errf(n, "reference %q set to unknown node in AST; "+
			"this can result from incorrect API usage or a compiler bug",
			n.Name)
	}

	if n.Scope == nil {
		// Package.
		// Should have been handled above.
		return c.errf(n, "unresolved identifier %v", n.Name)
	}

	switch f := n.Node.(type) {
	// Local expressions
	case *ast.LetClause:
		entry := c.lookupAlias(k, n)

		// let x = y
		return &adt.LetReference{
			Src:     n,
			UpCount: upCount,
			Label:   label,
			X:       entry.expr,
		}

	// TODO: handle new-style aliases

	case *ast.Field:
		// X=x: y
		// X=(x): y
		// X="\(x)": y
		a, ok := f.Label.(*ast.Alias)
		if !ok {
			return c.errf(n, "illegal reference %s", n.Name)
		}
		aliasInfo := c.lookupAlias(k, a.Ident) // marks alias as used.
		lab, ok := a.Expr.(ast.Label)
		if !ok {
			return c.errf(a.Expr, "invalid label expression")
		}
		name, _, err := ast.LabelName(lab)
		switch {
		case errors.Is(err, ast.ErrIsExpression):
			if aliasInfo.expr == nil {
				panic("unreachable")
			}
			return &adt.DynamicReference{
				Src:     n,
				UpCount: upCount,
				Label:   aliasInfo.expr,
			}

		case err != nil:
			return c.errf(n, "invalid label: %v", err)

		case name != "":
			label = c.label(lab)

		default:
			return c.errf(n, "unsupported field alias %q", name)
		}
	}

	return &adt.FieldReference{
		Src:     n,
		UpCount: upCount,
		Label:   label,
	}
}

func (c *compiler) addDecls(st *adt.StructLit, a []ast.Decl) {
	for _, d := range a {
		c.markAlias(d)
	}
	for _, d := range a {
		c.addLetDecl(d)
	}
	for _, d := range a {
		if x := c.decl(d); x != nil {
			st.Decls = append(st.Decls, x)
		}
	}
}

func (c *compiler) markAlias(d ast.Decl) {
	switch x := d.(type) {
	case *ast.Field:
		lab := x.Label
		if a, ok := lab.(*ast.Alias); ok {
			if _, ok = a.Expr.(ast.Label); !ok {
				c.errf(a, "alias expression is not a valid label")
			}

			e := aliasEntry{source: a}

			c.insertAlias(a.Ident, e)
		}

	case *ast.LetClause:
		a := aliasEntry{
			label:   (*letScope)(x),
			srcExpr: x.Expr,
			source:  x,
		}
		c.insertAlias(x.Ident, a)

	case *ast.Alias:
		c.errf(x, "old-style alias no longer supported: use let clause; use cue fix to update.")
	}
}

func (c *compiler) decl(d ast.Decl) adt.Decl {
	switch x := d.(type) {
	case *ast.BadDecl:
		return c.errf(d, "")

	case *ast.Field:
		lab := x.Label
		if a, ok := lab.(*ast.Alias); ok {
			if lab, ok = a.Expr.(ast.Label); !ok {
				return c.errf(a, "alias expression is not a valid label")
			}

			switch lab.(type) {
			case *ast.Ident, *ast.BasicLit, *ast.ListLit:
				// Even though we won't need the alias, we still register it
				// for duplicate and failed reference detection.
			default:
				c.updateAlias(a.Ident, c.expr(a.Expr))
			}
		}

		v := x.Value
		var value adt.Expr
		if a, ok := v.(*ast.Alias); ok {
			c.pushScope(nil, 0, a)
			c.insertAlias(a.Ident, aliasEntry{source: a})
			value = c.labeledExpr(x, (*fieldLabel)(x), a.Expr)
			c.popScope()
		} else {
			value = c.labeledExpr(x, (*fieldLabel)(x), v)
		}

		switch l := lab.(type) {
		case *ast.Ident, *ast.BasicLit:
			label := c.label(lab)

			if label == adt.InvalidLabel {
				return c.errf(x, "cannot use _ as label")
			}

			// TODO(legacy): remove: old-school definitions
			if x.Token == token.ISA && !label.IsDef() {
				name, isIdent, err := ast.LabelName(lab)
				if err == nil && isIdent {
					idx := c.index.StringToIndex(name)
					label, _ = adt.MakeLabel(x, idx, adt.DefinitionLabel)
				}
			}

			if x.Optional == token.NoPos {
				return &adt.Field{
					Src:   x,
					Label: label,
					Value: value,
				}
			} else {
				return &adt.OptionalField{
					Src:   x,
					Label: label,
					Value: value,
				}
			}

		case *ast.ListLit:
			if len(l.Elts) != 1 {
				// error
				return c.errf(x, "list label must have one element")
			}
			var label adt.Feature
			elem := l.Elts[0]
			// TODO: record alias for error handling? In principle it is okay
			// to have duplicates, but we do want it to be used.
			if a, ok := elem.(*ast.Alias); ok {
				label = c.label(a.Ident)
				elem = a.Expr
			}

			return &adt.BulkOptionalField{
				Src:    x,
				Filter: c.expr(elem),
				Value:  value,
				Label:  label,
			}

		case *ast.ParenExpr:
			if x.Token == token.ISA {
				c.errf(x, "definitions not supported for dynamic fields")
			}
			return &adt.DynamicField{
				Src:   x,
				Key:   c.expr(l),
				Value: value,
			}

		case *ast.Interpolation:
			if x.Token == token.ISA {
				c.errf(x, "definitions not supported for interpolations")
			}
			return &adt.DynamicField{
				Src:   x,
				Key:   c.expr(l),
				Value: value,
			}
		}

	// Handled in addLetDecl.
	case *ast.LetClause:
	// case: *ast.Alias: // TODO(value alias)

	case *ast.CommentGroup:
		// Nothing to do for a free-floating comment group.

	case *ast.Attribute:
		// Nothing to do for now for an attribute declaration.

	case *ast.Ellipsis:
		return &adt.Ellipsis{
			Src:   x,
			Value: c.expr(x.Type),
		}

	case *ast.Comprehension:
		return c.comprehension(x)

	case *ast.EmbedDecl: // Deprecated
		return c.expr(x.Expr)

	case ast.Expr:
		return c.expr(x)
	}
	return nil
}

func (c *compiler) addLetDecl(d ast.Decl) {
	switch x := d.(type) {
	// An alias reference will have an expression that is looked up in the
	// environment cash.
	case *ast.LetClause:
		// Cache the parsed expression. Creating a unique expression for each
		// reference allows the computation to be shared given that we don't
		// have fields for expressions. This, in turn, prevents exponential
		// blowup in x2: x1+x1, x3: x2+x2,  ... patterns.
		expr := c.labeledExpr(nil, (*letScope)(x), x.Expr)
		c.updateAlias(x.Ident, expr)

	case *ast.Alias:
		c.errf(x, "old-style alias no longer supported: use let clause; use cue fix to update.")
	}
}

func (c *compiler) elem(n ast.Expr) adt.Elem {
	switch x := n.(type) {
	case *ast.Ellipsis:
		return &adt.Ellipsis{
			Src:   x,
			Value: c.expr(x.Type),
		}

	case *ast.Comprehension:
		return c.comprehension(x)

	case ast.Expr:
		return c.expr(x)
	}
	return nil
}

func (c *compiler) comprehension(x *ast.Comprehension) adt.Elem {
	var cur adt.Yielder
	var first adt.Elem
	var prev, next *adt.Yielder
	for _, v := range x.Clauses {
		switch x := v.(type) {
		case *ast.ForClause:
			var key adt.Feature
			if x.Key != nil {
				key = c.label(x.Key)
			}
			y := &adt.ForClause{
				Syntax: x,
				Key:    key,
				Value:  c.label(x.Value),
				Src:    c.expr(x.Source),
			}
			cur = y
			c.pushScope((*forScope)(x), 1, v)
			defer c.popScope()
			next = &y.Dst

		case *ast.IfClause:
			y := &adt.IfClause{
				Src:       x,
				Condition: c.expr(x.Condition),
			}
			cur = y
			next = &y.Dst

		case *ast.LetClause:
			y := &adt.LetClause{
				Src:   x,
				Label: c.label(x.Ident),
				Expr:  c.expr(x.Expr),
			}
			cur = y
			c.pushScope((*letScope)(x), 1, v)
			defer c.popScope()
			next = &y.Dst
		}

		if prev != nil {
			*prev = cur
		} else {
			var ok bool
			if first, ok = cur.(adt.Elem); !ok {
				return c.errf(x,
					"first comprehension clause must be 'if' or 'for'")
			}
		}
		prev = next
	}

	// TODO: make x.Value an *ast.StructLit and this is redundant.
	if y, ok := x.Value.(*ast.StructLit); !ok {
		return c.errf(x.Value,
			"comprehension value must be struct, found %T", y)
	}

	y := c.expr(x.Value)

	st, ok := y.(*adt.StructLit)
	if !ok {
		// Error must have been generated.
		return y
	}

	if prev != nil {
		*prev = &adt.ValueClause{StructLit: st}
	} else {
		return c.errf(x, "comprehension value without clauses")
	}

	return first
}

func (c *compiler) labeledExpr(f *ast.Field, lab labeler, expr ast.Expr) adt.Expr {
	k := len(c.stack) - 1
	return c.labeledExprAt(k, f, lab, expr)
}

func (c *compiler) labeledExprAt(k int, f *ast.Field, lab labeler, expr ast.Expr) adt.Expr {
	if c.stack[k].field != nil {
		panic("expected nil field")
	}
	saved := c.stack[k]

	c.stack[k].label = lab
	c.stack[k].field = f

	value := c.expr(expr)

	c.stack[k] = saved
	return value
}

func (c *compiler) expr(expr ast.Expr) adt.Expr {
	switch n := expr.(type) {
	case nil:
		return nil
	case *ast.Ident:
		return c.resolve(n)

	case *ast.StructLit:
		c.pushScope(nil, 1, n)
		v := &adt.StructLit{Src: n}
		c.addDecls(v, n.Elts)
		c.popScope()
		return v

	case *ast.ListLit:
		c.pushScope(nil, 1, n)
		v := &adt.ListLit{Src: n}
		elts, ellipsis := internal.ListEllipsis(n)
		for _, d := range elts {
			elem := c.elem(d)

			switch x := elem.(type) {
			case nil:
			case adt.Elem:
				v.Elems = append(v.Elems, x)
			default:
				c.errf(d, "type %T not allowed in ListLit", d)
			}
		}
		if ellipsis != nil {
			d := &adt.Ellipsis{
				Src:   ellipsis,
				Value: c.expr(ellipsis.Type),
			}
			v.Elems = append(v.Elems, d)
		}
		c.popScope()
		return v

	case *ast.SelectorExpr:
		c.inSelector++
		ret := &adt.SelectorExpr{
			Src: n,
			X:   c.expr(n.X),
			Sel: c.label(n.Sel)}
		c.inSelector--
		return ret

	case *ast.IndexExpr:
		return &adt.IndexExpr{
			Src:   n,
			X:     c.expr(n.X),
			Index: c.expr(n.Index),
		}

	case *ast.SliceExpr:
		slice := &adt.SliceExpr{Src: n, X: c.expr(n.X)}
		if n.Low != nil {
			slice.Lo = c.expr(n.Low)
		}
		if n.High != nil {
			slice.Hi = c.expr(n.High)
		}
		return slice

	case *ast.BottomLit:
		return &adt.Bottom{
			Src:  n,
			Code: adt.UserError,
			Err:  errors.Newf(n.Pos(), "explicit error (_|_ literal) in source"),
		}

	case *ast.BadExpr:
		return c.errf(n, "invalid expression")

	case *ast.BasicLit:
		return c.parse(n)

	case *ast.Interpolation:
		if len(n.Elts) == 0 {
			return c.errf(n, "invalid interpolation")
		}
		first, ok1 := n.Elts[0].(*ast.BasicLit)
		last, ok2 := n.Elts[len(n.Elts)-1].(*ast.BasicLit)
		if !ok1 || !ok2 {
			return c.errf(n, "invalid interpolation")
		}
		if len(n.Elts) == 1 {
			return c.expr(n.Elts[0])
		}
		lit := &adt.Interpolation{Src: n}
		info, prefixLen, _, err := literal.ParseQuotes(first.Value, last.Value)
		if err != nil {
			return c.errf(n, "invalid interpolation: %v", err)
		}
		if info.IsDouble() {
			lit.K = adt.StringKind
		} else {
			lit.K = adt.BytesKind
		}
		prefix := ""
		for i := 0; i < len(n.Elts); i += 2 {
			l, ok := n.Elts[i].(*ast.BasicLit)
			if !ok {
				return c.errf(n, "invalid interpolation")
			}
			s := l.Value
			if !strings.HasPrefix(s, prefix) {
				return c.errf(l, "invalid interpolation: unmatched ')'")
			}
			s = l.Value[prefixLen:]
			x := parseString(c, l, info, s)
			lit.Parts = append(lit.Parts, x)
			if i+1 < len(n.Elts) {
				lit.Parts = append(lit.Parts, c.expr(n.Elts[i+1]))
			}
			prefix = ")"
			prefixLen = 1
		}
		return lit

	case *ast.ParenExpr:
		return c.expr(n.X)

	case *ast.CallExpr:
		call := &adt.CallExpr{Src: n, Fun: c.expr(n.Fun)}
		for _, a := range n.Args {
			call.Args = append(call.Args, c.expr(a))
		}
		return call

	case *ast.UnaryExpr:
		switch n.Op {
		case token.NOT, token.ADD, token.SUB:
			return &adt.UnaryExpr{
				Src: n,
				Op:  adt.OpFromToken(n.Op),
				X:   c.expr(n.X),
			}
		case token.GEQ, token.GTR, token.LSS, token.LEQ,
			token.NEQ, token.MAT, token.NMAT:
			return &adt.BoundExpr{
				Src:  n,
				Op:   adt.OpFromToken(n.Op),
				Expr: c.expr(n.X),
			}

		case token.MUL:
			return c.errf(n, "preference mark not allowed at this position")
		default:
			return c.errf(n, "unsupported unary operator %q", n.Op)
		}

	case *ast.BinaryExpr:
		switch n.Op {
		case token.OR:
			d := &adt.DisjunctionExpr{Src: n}
			c.addDisjunctionElem(d, n.X, false)
			c.addDisjunctionElem(d, n.Y, false)
			return d

		default:
			op := adt.OpFromToken(n.Op)
			x := c.expr(n.X)
			y := c.expr(n.Y)
			if op != adt.AndOp {
				c.assertConcreteIsPossible(n.X, op, x)
				c.assertConcreteIsPossible(n.Y, op, y)
			}
			// return updateBin(c,
			return &adt.BinaryExpr{Src: n, Op: op, X: x, Y: y} // )
		}

	default:
		return c.errf(n, "%s values not allowed in this position", ast.Name(n))
	}
}

func (c *compiler) assertConcreteIsPossible(src ast.Node, op adt.Op, x adt.Expr) bool {
	if !adt.AssertConcreteIsPossible(op, x) {
		str := astinternal.DebugStr(src)
		c.errf(src, "invalid operand %s ('%s' requires concrete value)", str, op)
	}
	return false
}

func (c *compiler) addDisjunctionElem(d *adt.DisjunctionExpr, n ast.Expr, mark bool) {
	switch x := n.(type) {
	case *ast.BinaryExpr:
		if x.Op == token.OR {
			c.addDisjunctionElem(d, x.X, mark)
			c.addDisjunctionElem(d, x.Y, mark)
			return
		}
	case *ast.UnaryExpr:
		if x.Op == token.MUL {
			d.HasDefaults = true
			c.addDisjunctionElem(d, x.X, true)
			return
		}
	}
	d.Values = append(d.Values, adt.Disjunct{Val: c.expr(n), Default: mark})
}

// TODO(perf): validate that regexps are cached at the right time.

func (c *compiler) parse(l *ast.BasicLit) (n adt.Expr) {
	s := l.Value
	if s == "" {
		return c.errf(l, "invalid literal %q", s)
	}
	switch l.Kind {
	case token.STRING:
		info, nStart, _, err := literal.ParseQuotes(s, s)
		if err != nil {
			return c.errf(l, err.Error())
		}
		s := s[nStart:]
		return parseString(c, l, info, s)

	case token.FLOAT, token.INT:
		err := literal.ParseNum(s, &c.num)
		if err != nil {
			return c.errf(l, "parse error: %v", err)
		}
		kind := adt.FloatKind
		if c.num.IsInt() {
			kind = adt.IntKind
		}
		n := &adt.Num{Src: l, K: kind}
		if err = c.num.Decimal(&n.X); err != nil {
			return c.errf(l, "error converting number to decimal: %v", err)
		}
		return n

	case token.TRUE:
		return &adt.Bool{Src: l, B: true}

	case token.FALSE:
		return &adt.Bool{Src: l, B: false}

	case token.NULL:
		return &adt.Null{Src: l}

	default:
		return c.errf(l, "unknown literal type")
	}
}

// parseString decodes a string without the starting and ending quotes.
func parseString(c *compiler, node ast.Expr, q literal.QuoteInfo, s string) (n adt.Expr) {
	str, err := q.Unquote(s)
	if err != nil {
		return c.errf(node, "invalid string: %v", err)
	}
	if q.IsDouble() {
		return &adt.String{Src: node, Str: str, RE: nil}
	}
	return &adt.Bytes{Src: node, B: []byte(str), RE: nil}
}
