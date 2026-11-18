package dangv2

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"

	"github.com/dagger/dagger/core"
)

// This file is the Dang side of the DangCheck builtin (see
// core/llm_dang_tools.go): it analyzes source read from an fs.FS against a
// schema, and renders what it finds for a model. tools.go resolves a call's
// scope in the engine and hands it here.

const (
	// dangSnippetName names a snippet in positions and messages.
	dangSnippetName = "<check>"
	// dangSnippetFile is the file a snippet is checked as, beside the
	// scope's own files. Its name can't clash with a workspace file's in
	// practice, and is displayed as dangSnippetName.
	dangSnippetFile = "<check>.dang"
	// dangWorkspaceFile declares the workspace scope's values.
	dangWorkspaceFile = "<workspace>.dang"

	// dangMembersWithDocs is how many members a listing shows with their
	// signatures and docs; the rest are only named.
	dangMembersWithDocs = 40
	// dangSearchLimit is how many search results are shown.
	dangSearchLimit = 20
	// dangDocLineMax caps a doc line shown beside a signature.
	dangDocLineMax = 120
	// dangDocMax caps the docs shown for one member.
	dangDocMax = 1500
)

// dangScope is where a Dang tool call's source is checked and run: the
// workspace scope, or a module's directory of .dang files.
type dangScope struct {
	// fsys holds the source, at its workspace-relative path, so positions
	// and errors name files as the workspace does.
	fsys fs.FS

	// dir is the directory of .dang files in fsys: the module's, or "." for
	// the workspace scope (which has only dangWorkspaceFile, if anything).
	dir string

	// cwd is the workspace's cwd, relative to its root: paths are shown
	// relative to it, as `at` resolves them.
	cwd string

	// module says dir is a module of the workspace, which DangCheck checks
	// without a source and DangEval runs the top level of.
	module bool

	// file is a .dang file of dir the call is about (`at` a file or a
	// position in one), or "".
	file string

	// pos is a position in file, or nil.
	pos *analysis.Pos

	// label names the scope in footers.
	label string

	// sdk names the SDK a module scope's module is written with, if known:
	// a module that isn't written in Dang has no .dang files to diagnose.
	sdk string

	// unloaded, for the workspace scope, notes the installed modules left
	// out of it because they failed to load (see unloadedNote).
	unloaded string

	// values declares the scope's bound values: `ws`, `chat`, `me` and the
	// bound tool objects (see valueDeclarations).
	values string

	// bound are the values values declares, which a DangEval snippet
	// writes by reassigning them (but for read-only ones).
	bound []dangValue

	// schema is the API the Dagger import (auto-imported) exposes.
	schema *introspection.Schema
}

// source returns the scope's source: its directory's files, any declaration
// of its values, and extra files (a snippet) laid over them.
func (sc *dangScope) source(extra map[string]string) analysis.Source {
	files := map[string]string{}
	if sc.values != "" {
		files[path.Join(sc.dir, dangWorkspaceFile)] = sc.values
	}
	for p, text := range extra {
		files[p] = text
	}
	if len(files) == 0 {
		return sc.fsys
	}
	return analysis.Overlay(sc.fsys, files)
}

func (sc *dangScope) imports() []dang.ImportConfig {
	if sc.schema == nil {
		return nil
	}
	return []dang.ImportConfig{{Name: "Dagger", Schema: sc.schema, AutoImport: true}}
}

// snippetPath is where a snippet is checked as one more file of the scope.
func (sc *dangScope) snippetPath() string {
	return path.Join(sc.dir, dangSnippetFile)
}

// display names a Source path for a reader, as `at` takes it back.
func (sc *dangScope) display(p string) string {
	switch path.Base(p) {
	case dangSnippetFile:
		return dangSnippetName
	case dangWorkspaceFile:
		return "<workspace>"
	}
	if strings.HasPrefix(p, "<") || sc.cwd == "" || sc.cwd == "." {
		return p
	}
	return displayWorkspacePath(sc.cwd, p)
}

// dangValue is a value the workspace scope binds by name, loaded from its ID:
// the workspace as `ws`, the conversation as `chat`, its agent as `me`, and
// each bound tool object.
type dangValue struct {
	// id is empty for a value bound as null: `me` without an agent.
	name, typeName, id string
	// readOnly values can't be reassigned (see bindingWriteDiagnostics).
	readOnly bool
}

// what says what v is, for a message about it.
func (v dangValue) what() string {
	switch {
	case v.name == core.DangWorkspaceBinding:
		return "the conversation's workspace"
	case v.isChat():
		return "the conversation"
	case v.name == core.DangAgentBinding:
		return "the conversation's agent"
	}
	return "bound tool object"
}

// isChat reports whether v is the conversation, rather than a tool object
// named `chat` where the conversation isn't bound (dagger mcp).
func (v dangValue) isChat() bool {
	return v.name == core.DangChatBinding && v.typeName == "LLM"
}

// valueDeclarations is the source binding each value by name, as a module
// function's `ws: Workspace!` argument is bound, one line each. Each is lazy:
// a value a snippet never reads is never loaded. Types are qualified, since a
// module's own type of the same name (the bound object of its own main type,
// in its scope) is not the API's. A value without an ID is null, of the
// nullable type.
func valueDeclarations(values []dangValue) string {
	var b strings.Builder
	for _, v := range values {
		if v.id == "" {
			fmt.Fprintf(&b, "let %s: Dagger.%s = null\n", v.name, v.typeName)
			continue
		}
		fmt.Fprintf(&b, "let %s: Dagger.%s! = Dagger.node(id: %q).{{... on Dagger.%s!}}\n", v.name, v.typeName, v.id, v.typeName)
	}
	return b.String()
}

// attributeBindingFailures reports a failure located in the declarations of
// the scope's values (see valueDeclarations), a file no reader has, against
// the value that failed to load instead, without a position: line N
// declares bound[N-1].
func (sc *dangScope) attributeBindingFailures(diags []analysis.Diagnostic) {
	for i, d := range diags {
		if path.Base(d.Path) != dangWorkspaceFile {
			continue
		}
		line := d.Range.Start.Line
		if line < 1 || line > len(sc.bound) {
			continue
		}
		v := sc.bound[line-1]
		diags[i].Path = ""
		diags[i].Range = analysis.Range{}
		diags[i].Message = fmt.Sprintf("`%s` (%s) failed to load: %s", v.name, v.what(), d.Message)
	}
}

// boundAssignment is a reassignment of one of the scope's bound values.
type boundAssignment struct {
	value dangValue
	path  string
	pos   analysis.Pos
}

// boundAssignments finds the reassignments of sc's bound values in the files
// a analyzed (only the one at onlyPath, if set): `name = …` (or a path
// through it, `name.x = …`) where name resolves to the value's declaration,
// not to a local shadowing it.
func boundAssignments(a *analysis.Analysis, sc *dangScope, onlyPath string) []boundAssignment {
	bound := map[string]dangValue{}
	for _, v := range sc.bound {
		bound[v.name] = v
	}
	if a == nil || len(bound) == 0 {
		return nil
	}
	var found []boundAssignment
	for _, f := range a.Files {
		if f.Block == nil || path.Base(f.Path) == dangWorkspaceFile || (onlyPath != "" && f.Path != onlyPath) {
			continue
		}
		f.Block.Walk(func(n dang.Node) bool {
			if n == nil {
				return false
			}
			re, ok := n.(*dang.Reassignment)
			if !ok {
				return true
			}
			sym := assignedSymbol(re.Target)
			if sym == nil || sym.Loc == nil {
				return true
			}
			v, ok := bound[sym.Name]
			if !ok {
				return true
			}
			pos := analysis.Pos{Line: sym.Loc.Line, Column: sym.Loc.Column}
			resolved, err := a.Inspect(f.Path, pos)
			if err != nil || resolved.Definition == nil || path.Base(resolved.Definition.Path) != dangWorkspaceFile {
				return true
			}
			found = append(found, boundAssignment{value: v, path: f.Path, pos: pos})
			return true
		})
	}
	return found
}

// snippetBoundAssignments finds the reassignments of sc's bound values in a
// snippet checked at a position, which no analysis resolves: by name, unless
// the snippet itself declares the name. A local of the position's scope
// shadowing a bound value isn't told apart.
func snippetBoundAssignments(forms []dang.Node, sc *dangScope) []boundAssignment {
	bound := map[string]dangValue{}
	for _, v := range sc.bound {
		bound[v.name] = v
	}
	declared := map[string]bool{}
	for _, form := range forms {
		for _, name := range form.DeclaredSymbols() {
			declared[name] = true
		}
	}
	var found []boundAssignment
	for _, form := range forms {
		form.Walk(func(n dang.Node) bool {
			if n == nil {
				return false
			}
			re, ok := n.(*dang.Reassignment)
			if !ok {
				return true
			}
			sym := assignedSymbol(re.Target)
			if sym == nil || sym.Loc == nil || declared[sym.Name] {
				return true
			}
			if v, ok := bound[sym.Name]; ok {
				found = append(found, boundAssignment{value: v, path: sym.Loc.Filename, pos: analysis.Pos{Line: sym.Loc.Line, Column: sym.Loc.Column}})
			}
			return true
		})
	}
	return found
}

// assignedSymbol is the name an assignment's target writes: the target, or
// the root of a path through it.
func assignedSymbol(target dang.Node) *dang.Symbol {
	for {
		switch t := target.(type) {
		case *dang.Symbol:
			return t
		case *dang.Select:
			if t.Receiver == nil {
				return nil
			}
			target = t.Receiver
		default:
			return nil
		}
	}
}

// assignedNames are the names of the values assigned.
func assignedNames(assigned []boundAssignment) map[string]bool {
	names := map[string]bool{}
	for _, as := range assigned {
		names[as.value.name] = true
	}
	return names
}

// bindingWriteDiagnostics reports the reassignments of bound values a
// snippet can't make, where it makes them: a read-only value's (`me`), and
// `chat`'s beside any other value's, since the LLM it assigns carries its own
// workspace and tool objects.
func bindingWriteDiagnostics(assigned []boundAssignment) []analysis.Diagnostic {
	var diags []analysis.Diagnostic
	diag := func(as boundAssignment, msg string) {
		diags = append(diags, analysis.Diagnostic{
			Severity: analysis.SeverityError,
			Code:     analysis.CodeType,
			Path:     as.path,
			Range:    analysis.Range{Start: as.pos, End: analysis.Pos{Line: as.pos.Line, Column: as.pos.Column + len(as.value.name)}},
			Message:  msg,
		})
	}
	var others []string
	seen := map[string]bool{}
	for _, as := range assigned {
		if as.value.readOnly {
			diag(as, fmt.Sprintf("cannot assign to `%s`: it is %s, bound read-only", as.value.name, as.value.what()))
			continue
		}
		if !as.value.isChat() && !seen[as.value.name] {
			seen[as.value.name] = true
			others = append(others, "`"+as.value.name+"`")
		}
	}
	if len(others) == 0 {
		return diags
	}
	for _, as := range assigned {
		if as.value.isChat() {
			diag(as, fmt.Sprintf("cannot assign to `chat` and %s in one snippet: %s", strings.Join(others, " and "), core.DangChatAloneMessage))
			break
		}
	}
	return diags
}

// dangReport accumulates a tool's text output.
type dangReport struct {
	strings.Builder
	errors, warnings int
}

func (r *dangReport) linef(format string, args ...any) {
	fmt.Fprintf(r, format, args...)
	r.WriteByte('\n')
}

// diagnostics counts diags and renders them, their paths named for a reader.
// Every Dang diagnostic the tools show goes through here.
func (r *dangReport) diagnostics(sc *dangScope, diags []analysis.Diagnostic) {
	shown := make([]analysis.Diagnostic, len(diags))
	for i, d := range diags {
		if d.Severity == analysis.SeverityError {
			r.errors++
		} else {
			r.warnings++
		}
		if d.Path != "" {
			d.Path = sc.display(d.Path)
		}
		shown[i] = d
	}
	r.WriteString(renderDiagnostics(shown))
}

// renderDiagnostics renders diagnostics with analysis.Render, as
// `path:line:col: severity: message` with each one's context (the member a
// selection names, or a receiver's members and the closest names to an
// unknown one) on indented lines. Messages are cleaned up first: Dang's
// source reports carry ANSI color, and a multi-line message is indented
// under its first line. A diagnostic without a file (a failure Dang
// reported without a position) has no position either.
func renderDiagnostics(diags []analysis.Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		d.Message = strings.ReplaceAll(strings.TrimSpace(ansi.Strip(d.Message)), "\n", "\n  ")
		if d.Path == "" {
			msg := d.Message
			if d.Replacement != "" {
				msg += fmt.Sprintf(" (quick fix: %s)", d.Replacement)
			}
			fmt.Fprintf(&b, "%s: %s\n", d.Severity, msg)
			continue
		}
		b.WriteString(analysis.Render([]analysis.Diagnostic{d}, analysis.RenderOptions{}))
	}
	return b.String()
}

// counts renders the diagnostic counts for a footer.
func (r *dangReport) counts() string {
	if r.errors == 0 && r.warnings == 0 {
		return "ok"
	}
	var parts []string
	if r.errors > 0 {
		parts = append(parts, plural(r.errors, "error"))
	}
	if r.warnings > 0 {
		parts = append(parts, plural(r.warnings, "warning"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// checkDang is DangCheck: diagnostics for the scope's module or file, or a
// snippet's diagnostics or description, then search results.
func checkDang(ctx context.Context, sc *dangScope, source, search string) (core.DangReport, error) {
	source = strings.TrimSpace(source)
	search = strings.TrimSpace(search)
	if source == "" && search == "" && !sc.module {
		return core.DangReport{}, errors.New("nothing to check: pass source, search, or at (a module, file or position)")
	}

	var r dangReport
	var scope dang.TypeScope
	files := 0
	switch {
	case source == "":
		// The module's or file's own diagnostics.
		a, err := analysis.Analyze(ctx, sc.source(nil), sc.dir, analysis.Options{
			Focus:         sc.file,
			ImportConfigs: sc.imports(),
			// A model reads the diagnostics: give an unknown name what
			// exists instead.
			DiagnosticContext: true,
		})
		if err != nil {
			return core.DangReport{}, err
		}
		files = countFiles(a)
		if sc.module {
			diags := slices.Concat(a.Diagnostics(), bindingWriteDiagnostics(boundAssignments(a, sc, "")))
			if files == 0 {
				// Nothing to diagnose: say why, as a diagnostic.
				diags = []analysis.Diagnostic{{Severity: analysis.SeverityError, Message: noDangFilesMessage(sc)}}
			}
			r.diagnostics(sc, diags)
		}
		scope = searchScope(a, sc)
	case sc.pos != nil:
		// A snippet as if written at a position of a file.
		a, err := analysis.Analyze(ctx, sc.source(nil), sc.dir, analysis.Options{
			Focus:         sc.file,
			ImportConfigs: sc.imports(),
			// A model reads the diagnostics: give an unknown name what
			// exists instead.
			DiagnosticContext: true,
		})
		if err != nil {
			return core.DangReport{}, err
		}
		files = countFiles(a)
		scope = a.ScopeAt(sc.file, *sc.pos)
		if scope == nil {
			// The file doesn't parse: say why.
			r.diagnostics(sc, a.Diagnostics())
			break
		}
		forms, diags := checkInScope(ctx, scope, source)
		diags = append(diags, bindingWriteDiagnostics(snippetBoundAssignments(forms, sc))...)
		describeSnippet(ctx, &r, sc, nil, scope, source, forms, diags)
	default:
		// A snippet as one more file of the scope.
		snippet := sc.snippetPath()
		a, err := analysis.Analyze(ctx, sc.source(map[string]string{snippet: source}), sc.dir, analysis.Options{
			Focus:         snippet,
			ImportConfigs: sc.imports(),
			// A model reads the diagnostics: give an unknown name what
			// exists instead.
			DiagnosticContext: true,
		})
		if err != nil {
			return core.DangReport{}, err
		}
		files = countFiles(a)
		scope = a.FileScope(snippet)
		var forms []dang.Node
		if f := a.File(snippet); f != nil && f.Block != nil {
			forms = f.Block.Forms
		}
		describeSnippet(ctx, &r, sc, a, scope, source, forms, slices.Concat(a.Diagnostics(), bindingWriteDiagnostics(boundAssignments(a, sc, snippet))))
	}

	if search != "" {
		if r.Len() > 0 {
			r.WriteByte('\n')
		}
		renderSearch(&r, scope, search)
	}

	footer := []string{r.counts(), sc.label}
	if sc.module {
		footer = append(footer, plural(files, "file"))
	}
	footer = append(footer, "nothing ran")
	r.linef("%s", strings.Join(footer, " · "))
	// A snippet that doesn't parse or check is rejected; the diagnostics of
	// a module or file checked without one are only reported.
	return core.DangReport{Text: r.String(), Rejected: source != "" && r.errors > 0}, nil
}

// noDangFilesMessage says why a module scope has no .dang files to
// diagnose: most likely, its module isn't written in Dang.
func noDangFilesMessage(sc *dangScope) string {
	if !isDangSDK(sc.sdk) {
		return fmt.Sprintf("%s is written with the %s SDK, not Dang: it has no .dang files to diagnose; pass source to check Dang against its API", sc.label, sc.sdk)
	}
	return fmt.Sprintf("no .dang files in %s", sc.display(sc.dir))
}

// countFiles counts the scope's own analyzed files.
func countFiles(a *analysis.Analysis) int {
	n := 0
	for _, f := range a.Files {
		switch path.Base(f.Path) {
		case dangSnippetFile, dangWorkspaceFile:
		default:
			n++
		}
	}
	return n
}

// searchScope is the scope search ranks names in, without a snippet.
func searchScope(a *analysis.Analysis, sc *dangScope) dang.TypeScope {
	if sc.file != "" {
		if sc.pos != nil {
			return a.ScopeAt(sc.file, *sc.pos)
		}
		return a.FileScope(sc.file)
	}
	if len(a.Files) > 0 {
		return a.FileScope(a.Files[0].Path)
	}
	return a.Scope
}

// checkInScope type-checks a snippet in a scope, e.g. one inside a function
// body, without evaluating it.
func checkInScope(ctx context.Context, scope dang.TypeScope, source string) ([]dang.Node, []analysis.Diagnostic) {
	parsed, err := dang.Parse(dangSnippetName, []byte(source),
		dang.GlobalStore("filePath", dangSnippetName),
		dang.WithVersion(dang.VersionFromContext(ctx)))
	if err != nil {
		return nil, errorDiagnostics(err)
	}
	block, ok := parsed.(*dang.FileBlock)
	if !ok {
		return nil, nil
	}
	ctx, warnings := dang.WithInferWarningSink(ctx)
	// Infer in a clone, so the snippet's declarations stay out of scope.
	_, err = dang.InferFormsWithPhases(ctx, block.Forms, scope.Clone(), hm.NewSimpleFresher())
	diags := errorDiagnostics(err)
	for _, w := range warnings.Take() {
		d := analysis.Diagnostic{Severity: analysis.SeverityWarning, Code: analysis.CodeWarning, Message: w.Message}
		if w.Location != nil {
			d.Path = w.Location.Filename
			d.Range.Start = analysis.Pos{Line: w.Location.Line, Column: w.Location.Column}
		}
		diags = append(diags, d)
	}
	return block.Forms, diags
}

// errorDiagnostics converts a Dang error into diagnostics, one per error an
// *dang.InferenceErrors collects, located where Dang located them.
func errorDiagnostics(err error) []analysis.Diagnostic {
	if err == nil {
		return nil
	}
	var errs *dang.InferenceErrors
	if errors.As(err, &errs) {
		var diags []analysis.Diagnostic
		for _, e := range errs.Errors {
			diags = append(diags, errorDiagnostics(e)...)
		}
		return diags
	}
	d := analysis.Diagnostic{Severity: analysis.SeverityError, Code: analysis.CodeType}
	var (
		loc       *dang.SourceLocation
		sourceErr *dang.SourceError
		inferErr  *dang.InferError
		parseErr  interface {
			ParseErrorLocation() *dang.SourceLocation
		}
	)
	switch {
	case errors.As(err, &sourceErr):
		loc, d.Message = sourceErr.Location, sourceErr.Inner.Error()
	case errors.As(err, &parseErr):
		loc, d.Message, d.Code = parseErr.ParseErrorLocation(), err.Error(), analysis.CodeSyntax
	case errors.As(err, &inferErr):
		loc, d.Message = inferErr.Location, inferErr.Error()
	default:
		d.Message = err.Error()
	}
	if loc != nil {
		d.Path = loc.Filename
		d.Range.Start = analysis.Pos{Line: loc.Line, Column: loc.Column}
	}
	return []analysis.Diagnostic{d}
}

// describeSnippet reports a checked snippet: its diagnostics, or, when it
// checks, what its last form is. a is the analysis the snippet is a file of,
// if it is one, for declaration sites.
func describeSnippet(ctx context.Context, r *dangReport, sc *dangScope, a *analysis.Analysis, scope dang.TypeScope, source string, forms []dang.Node, diags []analysis.Diagnostic) {
	var last dang.Node
	if len(forms) > 0 {
		last = forms[len(forms)-1]
	}

	// A type name, or a member of one (`Container`, `JSON.encode`,
	// `Container.withExec`): a lookup, whatever checking it as an
	// expression says.
	if scope != nil && isQualifiedName(source) {
		if named, ok := lookupTypeName(scope, source); ok {
			describeType(ctx, r, scope, source, named)
			return
		}
		if recv, _, ok := cutLast(source, "."); ok {
			if _, isType := lookupTypeName(scope, recv); isType {
				if m, err := analysis.Doc(ctx, scope, source); err == nil {
					describeMember(r, *m, "")
					return
				}
			}
		}
	}

	// A selection naming a member that exists (`xs.reduce`) is a lookup too:
	// the errors of using it bare don't matter.
	var member *analysis.Member
	if sel, ok := last.(*dang.Select); ok {
		if m, ok := analysis.SelectedMember(sel); ok {
			member = &m
			diags = dropWithin(diags, sel)
		}
	}

	errs := 0
	for _, d := range diags {
		if d.Severity == analysis.SeverityError {
			errs++
		}
	}
	r.diagnostics(sc, diags)
	if errs > 0 || last == nil {
		return
	}
	if len(last.DeclaredSymbols()) > 0 {
		// A declaration describes nothing; the diagnostics say it checks.
		return
	}

	if sym, ok := last.(*dang.Symbol); ok && scope != nil {
		if m, ok := analysis.Lookup(scope, sym.Name); ok {
			member = &m
		}
	}
	if member != nil {
		describeMember(r, *member, definitionOf(a, sc, last))
		if member.Kind == analysis.KindFunction {
			return
		}
	}
	t := last.GetInferredType()
	if t == nil {
		return
	}
	if !hasMemberListing(t) {
		// A scalar, list or record: its type says what it is; its members
		// are a lookup of its type away (`String`, `List`).
		if member == nil {
			r.linef("%s", dang.FormatType(t))
		}
		return
	}
	if member != nil {
		r.WriteByte('\n')
	}
	describeMembers(r, dang.FormatType(t), analysis.TypeMembers(t))
}

// hasMemberListing reports whether a value of type t is described with its
// type's members: an object or interface, whose members are what to do with
// it next. A scalar's, enum's, list's or record's members are noise beside
// its value, and a lookup of its type away.
func hasMemberListing(t hm.Type) bool {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	named, ok := t.(*dang.Type)
	return ok && named.Named != "" && (named.Kind == dang.ObjectKind || named.Kind == dang.InterfaceKind)
}

// dropWithin leaves out the diagnostics located inside node.
func dropWithin(diags []analysis.Diagnostic, node dang.Node) []analysis.Diagnostic {
	loc := node.GetSourceLocation()
	if loc == nil {
		return diags
	}
	end := analysis.Pos{Line: loc.Line, Column: loc.Column + loc.Length}
	if loc.End != nil {
		end = analysis.Pos{Line: loc.End.Line, Column: loc.End.Column}
	}
	var kept []analysis.Diagnostic
	for _, d := range diags {
		start := d.Range.Start
		inside := !posBefore(start, analysis.Pos{Line: loc.Line, Column: loc.Column}) && !posBefore(end, start)
		if !inside {
			kept = append(kept, d)
		}
	}
	return kept
}

func posBefore(p, q analysis.Pos) bool {
	if p.Line != q.Line {
		return p.Line < q.Line
	}
	return p.Column < q.Column
}

// definitionOf returns where the name a snippet's last form refers to is
// declared in the scope's own files, if it is.
func definitionOf(a *analysis.Analysis, sc *dangScope, node dang.Node) string {
	if a == nil {
		return ""
	}
	var loc *dang.SourceLocation
	switch n := node.(type) {
	case *dang.Symbol:
		loc = n.Loc
	case *dang.Select:
		if n.Field != nil {
			loc = n.Field.Loc
		}
	}
	if loc == nil {
		return ""
	}
	sym, err := a.Inspect(sc.snippetPath(), analysis.Pos{Line: loc.Line, Column: loc.Column})
	if err != nil || sym.Definition == nil {
		return ""
	}
	switch path.Base(sym.Definition.Path) {
	case dangSnippetFile, dangWorkspaceFile:
		return ""
	}
	return fmt.Sprintf("%s:%s", sc.display(sym.Definition.Path), sym.Definition.Range.Start)
}

// describeMember renders one member: its signature, docs, deprecation,
// example and declaration site.
func describeMember(r *dangReport, m analysis.Member, definedAt string) {
	r.linef("%s", m.QualifiedSignature())
	if doc := strings.TrimSpace(m.Doc); doc != "" {
		r.linef("  %s", strings.ReplaceAll(capRunes(doc, dangDocMax), "\n", "\n  "))
	}
	if m.Deprecated != "" {
		dep := "deprecated: " + m.Deprecated
		if m.Replacement != "" {
			dep += " (use " + m.Replacement + ")"
		}
		r.linef("  %s", dep)
	}
	if m.Example != "" {
		r.linef("  example: %s", strings.ReplaceAll(m.Example, "\n", "\n    "))
	}
	if definedAt != "" {
		r.linef("  declared at %s", definedAt)
	}
}

// describeType renders a type a name names: its docs and members.
func describeType(ctx context.Context, r *dangReport, scope dang.TypeScope, name string, named dang.TypeScope) {
	list, err := analysis.Members(ctx, scope, name)
	if err != nil {
		r.linef("%s: %s", name, err)
		return
	}
	r.linef("%s %s", kindOf(named), name)
	if doc := strings.TrimSpace(named.GetTypeDocString()); doc != "" {
		r.linef("  %s", strings.ReplaceAll(capRunes(doc, dangDocMax), "\n", "\n  "))
	}
	r.WriteByte('\n')
	describeMembers(r, name, list.Members)
}

// kindOf names the kind of a named type.
func kindOf(named dang.TypeScope) string {
	if mod, ok := named.(*dang.Type); ok {
		if mod.IsMeta() {
			mod = mod.InstanceType()
		}
		switch mod.Kind {
		case dang.EnumKind:
			return "enum"
		case dang.ScalarKind:
			return "scalar"
		case dang.InterfaceKind:
			return "interface"
		case dang.UnionKind:
			return "union"
		case dang.InputKind:
			return "input"
		}
	}
	return "type"
}

// describeMembers lists members of what has type typ: the first
// dangMembersWithDocs with their signatures and docs, the rest by name.
func describeMembers(r *dangReport, typ string, members []analysis.Member) {
	if len(members) == 0 {
		r.linef("%s has no members", typ)
		return
	}
	r.linef("%s has %s:", typ, plural(len(members), "member"))
	for i, m := range members {
		if i == dangMembersWithDocs {
			names := make([]string, 0, len(members)-i)
			for _, rest := range members[i:] {
				names = append(names, rest.Name)
			}
			r.linef("  … %d more (check one as a member reference for its docs): %s", len(names), strings.Join(names, " "))
			break
		}
		r.linef("  %s", memberLine(m, false))
	}
}

// memberLine renders a member on one line: its signature and the first line
// of its docs.
func memberLine(m analysis.Member, qualified bool) string {
	sig := m.Signature()
	switch {
	case isTypeMember(m):
		// "type Dagger.Workspace", not "Dagger.type Workspace".
		if qualified && m.Receiver != "" {
			sig = fmt.Sprintf("%s %s.%s", m.Kind, m.Receiver, m.Name)
		}
	case qualified:
		sig = m.QualifiedSignature()
	}
	if m.Static && !qualified {
		sig = "(static) " + sig
	}
	if m.Deprecated != "" {
		sig += " (deprecated)"
	}
	if doc := firstLine(m.Doc); doc != "" {
		sig += " — " + capRunes(doc, dangDocLineMax)
	}
	return sig
}

// isTypeMember reports whether m names a type rather than a value.
func isTypeMember(m analysis.Member) bool {
	return m.Kind != analysis.KindFunction && m.Kind != analysis.KindValue
}

// renderSearch ranks what the scope can reach by name and docs. A type the
// scope names directly and through its import (Workspace, Dagger.Workspace)
// is listed once, by its shorter name, which Search ranks first.
func renderSearch(r *dangReport, scope dang.TypeScope, query string) {
	var results []analysis.Member
	types := map[string]bool{}
	for _, m := range analysis.Search(scope, query, 2*dangSearchLimit) {
		if isTypeMember(m) {
			key := string(m.Kind) + " " + m.Name
			if types[key] {
				continue
			}
			types[key] = true
		}
		if results = append(results, m); len(results) == dangSearchLimit {
			break
		}
	}
	if len(results) == 0 {
		r.linef("search %q: no matches", query)
		return
	}
	r.linef("search %q:", query)
	for _, m := range results {
		r.linef("  %s", memberLine(m, true))
	}
}

// isQualifiedName reports whether s is a name, or names joined by dots.
func isQualifiedName(s string) bool {
	for _, part := range strings.Split(s, ".") {
		if !isIdent(part) {
			return false
		}
	}
	return true
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// lookupTypeName resolves a possibly qualified type name (e.g. "List",
// "Dagger.Container") in scope, as analysis.Members does.
func lookupTypeName(scope dang.TypeScope, name string) (dang.TypeScope, bool) {
	if !isQualifiedName(name) {
		return nil, false
	}
	parts := strings.Split(name, ".")
	current := scope
	for _, part := range parts {
		next, ok := current.NamedType(part)
		if !ok {
			if len(parts) == 1 && part == "Map" {
				return dang.MapTypeModule, true
			}
			return nil, false
		}
		current = next
	}
	return current, true
}

// cutLast is strings.Cut on the last occurrence of sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// capRunes keeps max runes of s, marking a cut with an ellipsis.
func capRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}
