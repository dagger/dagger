package sdk

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// goModuleTypes reports a Go module's types from the registration code in its
// committed dagger.gen.go: the arm of invoke that the compiled module would run
// to answer the same question. It makes that code's calls against the API the
// compiled module would call, in the same view, so loading builds nothing and
// fetches no dependency.
//
// A module whose bindings are generated at load has no such code to trust, and
// committed code that doesn't type-check against the module's source, or that
// holds anything this reader doesn't know, may not be what a build would run.
// Those take the runtime path, by core.ErrModuleTypesNeedRuntime, and fail or
// succeed there as a build does.
type goModuleTypes struct{}

func (goModuleTypes) ModuleTypes(
	ctx context.Context,
	_ *core.SchemaBuilder,
	src dagql.ObjectResult[*core.ModuleSource],
	_ *core.Module,
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	if useRuntimeCodegen(src) {
		return inst, core.ErrModuleTypesNeedRuntime
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("get Dagger server for go module types: %w", err)
	}
	inst, err = readGoRegistration(ctx, dag, src)
	if err != nil {
		slog.Info("go module types: taking the runtime path", "module", src.Self().ModuleOriginalName, "reason", err)
		return inst, fmt.Errorf("%w: %w", core.ErrModuleTypesNeedRuntime, err)
	}
	return inst, nil
}

func readGoRegistration(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	dir := filepath.Clean(src.Self().SourceSubpath)
	sources, err := readGoPackage(ctx, dag, src, dir)
	if err != nil {
		return inst, err
	}
	generated, ok := sources[goSDKGeneratedFile]
	if !ok {
		return inst, fmt.Errorf("no %s", goSDKGeneratedFile)
	}
	delete(sources, goSDKGeneratedFile)
	expr, err := goRegistrationExpr(generated)
	if err != nil {
		return inst, err
	}
	if err := goBindingsMatchSource(generated, sources, func(importPath string) (map[string]string, bool, error) {
		rel, ok := goGeneratedPackageDir(importPath)
		if !ok {
			return nil, false, nil
		}
		files, err := readGoPackage(ctx, dag, src, filepath.Join(dir, rel))
		return files, err == nil, err
	}); err != nil {
		return inst, fmt.Errorf("bindings don't match the source: %w", err)
	}
	reader := goRegistrationReader{dag: dag, view: call.View(engine.ModuleAPIViewVersion(src.Self().EngineVersion))}
	res, err := reader.eval(ctx, expr)
	if err != nil {
		return inst, err
	}
	inst, ok = res.(dagql.ObjectResult[*core.Module])
	if !ok {
		return inst, fmt.Errorf("registration code returned %T, not a module", res)
	}
	return inst, nil
}

// goGeneratedPackageDir returns where a module's generated package lives, from
// the module's source directory.
func goGeneratedPackageDir(importPath string) (string, bool) {
	const generated = "internal/dagger"
	if strings.HasSuffix(importPath, "/"+generated) {
		return generated, true
	}
	if _, sub, ok := strings.Cut(importPath, "/"+generated+"/"); ok {
		return filepath.Join(generated, sub), true
	}
	return "", false
}

// readGoPackage returns a directory's Go files by name, without tests.
func readGoPackage(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
	dir string,
) (map[string]string, error) {
	var entries dagql.Array[dagql.String]
	if err := dag.Select(ctx, src.Self().ContextDirectory, &entries,
		dagql.Selector{Field: "entries", Args: []dagql.NamedInput{{Name: "path", Value: dagql.Opt(dagql.String(dir))}}},
	); err != nil {
		return nil, err
	}
	sources := map[string]string{}
	for _, entry := range entries {
		name := entry.String()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		var contents dagql.String
		if err := dag.Select(ctx, src.Self().ContextDirectory, &contents,
			dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(filepath.Join(dir, name))}}},
			dagql.Selector{Field: "contents"},
		); err != nil {
			return nil, err
		}
		sources[name] = contents.String()
	}
	return sources, nil
}

// goRegistrationExpr returns the expression dagger.gen.go's invoke returns
// when asked for the module's definition (an empty parent name).
func goRegistrationExpr(src string) (ast.Expr, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "dagger.gen.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "invoke" || fn.Recv != nil || fn.Body == nil {
			continue
		}
		// Generated invoke is exactly: `_ = inputArgs`, then the switch on the
		// parent's name. Anything else could change what the binary returns.
		if len(fn.Body.List) != 2 {
			return nil, fmt.Errorf("invoke has %d statements", len(fn.Body.List))
		}
		if assign, ok := fn.Body.List[0].(*ast.AssignStmt); !ok || len(assign.Lhs) != 1 || !isGoIdent(assign.Lhs[0], "_") {
			return nil, fmt.Errorf("invoke doesn't start as generated")
		}
		sw, ok := fn.Body.List[1].(*ast.SwitchStmt)
		if !ok || !isGoIdent(sw.Tag, "parentName") || sw.Init != nil {
			return nil, fmt.Errorf("invoke doesn't switch on the parent's name")
		}
		for _, clause := range sw.Body.List {
			cc, ok := clause.(*ast.CaseClause)
			if !ok || len(cc.List) != 1 {
				continue
			}
			if lit, ok := cc.List[0].(*ast.BasicLit); !ok || lit.Value != `""` {
				continue
			}
			if len(cc.Body) != 1 {
				return nil, fmt.Errorf("registration arm has %d statements", len(cc.Body))
			}
			ret, ok := cc.Body[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 2 || !isGoIdent(ret.Results[1], "nil") {
				return nil, fmt.Errorf("registration arm doesn't return a module and nil")
			}
			return ret.Results[0], nil
		}
	}
	return nil, fmt.Errorf("no registration arm in dagger.gen.go")
}

func isGoIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

const goSDKGeneratedFile = "dagger.gen.go"

type goRegistrationCall struct {
	// args names the call's required arguments, in the order the generated
	// client's method takes them. An options struct may follow.
	args []string
}

// goRegistrationCalls is what generated registration code calls, by receiver.
// Anything else sends the module down the runtime path.
var goRegistrationCalls = map[string]map[string]goRegistrationCall{
	"Client": {
		"typeDef":   {},
		"function":  {args: []string{"name", "returnType"}},
		"sourceMap": {args: []string{"filename", "line", "column"}},
		"module":    {},
	},
	"Module": {
		"withDescription": {args: []string{"description"}},
		"withObject":      {args: []string{"object"}},
		"withInterface":   {args: []string{"iface"}},
		"withEnum":        {args: []string{"enum"}},
	},
	"Function": {
		"withDescription": {args: []string{"description"}},
		"withArg":         {args: []string{"name", "typeDef"}},
		"withSourceMap":   {args: []string{"sourceMap"}},
		"withCachePolicy": {args: []string{"policy"}},
		"withDeprecated":  {},
		"withCheck":       {},
		"withGenerator":   {},
		"withUp":          {},
		"withAgent":       {},
	},
	"TypeDef": {
		"withKind":        {args: []string{"kind"}},
		"withOptional":    {args: []string{"optional"}},
		"withListOf":      {args: []string{"elementType"}},
		"withObject":      {args: []string{"name"}},
		"withInterface":   {args: []string{"name"}},
		"withEnum":        {args: []string{"name"}},
		"withScalar":      {args: []string{"name"}},
		"withField":       {args: []string{"name", "typeDef"}},
		"withFunction":    {args: []string{"function"}},
		"withConstructor": {args: []string{"function"}},
		"withEnumMember":  {args: []string{"name"}},
		"withEnumValue":   {args: []string{"value"}},

		"withCollection":      {},
		"withCollectionGet":   {args: []string{"name"}},
		"withCollectionKeys":  {args: []string{"name"}},
		"withCollectionDelta": {args: []string{"name"}},
	},
}

var goRegistrationEnums = map[string]dagql.Input{
	"TypeDefKindStringKind":         core.TypeDefKindString,
	"TypeDefKindIntegerKind":        core.TypeDefKindInteger,
	"TypeDefKindFloatKind":          core.TypeDefKindFloat,
	"TypeDefKindBooleanKind":        core.TypeDefKindBoolean,
	"TypeDefKindVoidKind":           core.TypeDefKindVoid,
	"FunctionCachePolicyDefault":    core.FunctionCachePolicyDefault,
	"FunctionCachePolicyPerSession": core.FunctionCachePolicyPerSession,
	"FunctionCachePolicyNever":      core.FunctionCachePolicyNever,
}

type goRegistrationReader struct {
	dag *dagql.Server
	// view is the core API view the compiled module's client would have: its
	// engineVersion, capped at this engine's. Some calls mean different things
	// in different views, such as withEnumMember before v0.18.11.
	view call.View
}

// eval evaluates one chain of calls rooted at dag: dag.TypeDef().WithKind(...).
func (r goRegistrationReader) eval(ctx context.Context, expr ast.Expr) (dagql.AnyResult, error) {
	var calls []*ast.CallExpr
	var names []string
	for {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return nil, fmt.Errorf("expected a call, got %T", expr)
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil, fmt.Errorf("expected a method call, got %T", call.Fun)
		}
		calls = append(calls, call)
		names = append(names, lowerFirst(sel.Sel.Name))
		if id, ok := sel.X.(*ast.Ident); ok {
			if id.Name != "dag" {
				return nil, fmt.Errorf("call on %s", id.Name)
			}
			break
		}
		expr = sel.X
	}

	var result string
	selectors := make([]dagql.Selector, 0, len(calls))
	receiver := "Client"
	for i := len(calls) - 1; i >= 0; i-- {
		spec, ok := goRegistrationCalls[receiver][names[i]]
		if !ok {
			return nil, fmt.Errorf("unknown call %s.%s", receiver, names[i])
		}
		args, err := r.args(ctx, spec, calls[i].Args)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", receiver, names[i], err)
		}
		selectors = append(selectors, dagql.Selector{Field: names[i], Args: args, View: r.view})
		if receiver == "Client" {
			switch names[i] {
			case "typeDef":
				receiver = "TypeDef"
			case "function":
				receiver = "Function"
			case "module":
				receiver = "Module"
			case "sourceMap":
				receiver = "SourceMap"
			}
			result = receiver
		}
	}
	switch result {
	case "TypeDef":
		var dest dagql.ObjectResult[*core.TypeDef]
		err := r.dag.Select(ctx, r.dag.Root(), &dest, selectors...)
		return dest, err
	case "Function":
		var dest dagql.ObjectResult[*core.Function]
		err := r.dag.Select(ctx, r.dag.Root(), &dest, selectors...)
		return dest, err
	case "SourceMap":
		var dest dagql.ObjectResult[*core.SourceMap]
		err := r.dag.Select(ctx, r.dag.Root(), &dest, selectors...)
		return dest, err
	case "Module":
		var dest dagql.ObjectResult[*core.Module]
		err := r.dag.Select(ctx, r.dag.Root(), &dest, selectors...)
		return dest, err
	}
	return nil, fmt.Errorf("unknown result %q", result)
}

func (r goRegistrationReader) args(ctx context.Context, spec goRegistrationCall, exprs []ast.Expr) ([]dagql.NamedInput, error) {
	if len(exprs) < len(spec.args) || len(exprs) > len(spec.args)+1 {
		return nil, fmt.Errorf("%d arguments for %v", len(exprs), spec.args)
	}
	var named []dagql.NamedInput
	for i, name := range spec.args {
		v, err := r.input(ctx, exprs[i])
		if err != nil {
			return nil, fmt.Errorf("argument %s: %w", name, err)
		}
		named = append(named, dagql.NamedInput{Name: name, Value: v})
	}
	if len(exprs) == len(spec.args) {
		return named, nil
	}
	opts, ok := exprs[len(spec.args)].(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("options are %T", exprs[len(spec.args)])
	}
	for _, elt := range opts.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, fmt.Errorf("option is %T", elt)
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("option key is %T", kv.Key)
		}
		// The generated client sends no option holding its zero value.
		if isZeroGoLiteral(kv.Value) {
			continue
		}
		v, err := r.input(ctx, kv.Value)
		if err != nil {
			return nil, fmt.Errorf("option %s: %w", key.Name, err)
		}
		name := lowerFirst(key.Name)
		switch val := v.(type) {
		case dagql.ID[*core.SourceMap]:
			v = dagql.Opt(val)
		case dagql.String:
			// These options are nullable; the other strings default to empty.
			if name == "deprecated" || name == "timeToLive" || name == "reason" {
				v = dagql.Opt(val)
			}
		}
		named = append(named, dagql.NamedInput{Name: name, Value: v})
	}
	return named, nil
}

func isZeroGoLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && (lit.Value == `""` || lit.Value == "``")
}

func (r goRegistrationReader) input(ctx context.Context, expr ast.Expr) (dagql.Input, error) {
	switch x := expr.(type) {
	case *ast.BasicLit:
		switch x.Kind {
		case token.STRING:
			s, err := strconv.Unquote(x.Value)
			if err != nil {
				return nil, err
			}
			return dagql.String(s), nil
		case token.INT:
			n, err := strconv.Atoi(x.Value)
			if err != nil {
				return nil, err
			}
			return dagql.Int(n), nil
		}
		return nil, fmt.Errorf("literal %s", x.Value)
	case *ast.Ident:
		switch x.Name {
		case "true":
			return dagql.Boolean(true), nil
		case "false":
			return dagql.Boolean(false), nil
		}
		return nil, fmt.Errorf("identifier %s", x.Name)
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && id.Name == "dagger" {
			if v, ok := goRegistrationEnums[x.Sel.Name]; ok {
				return v, nil
			}
		}
		return nil, fmt.Errorf("unknown constant %s", x.Sel.Name)
	case *ast.CompositeLit:
		return r.stringListInput(ctx, x)
	case *ast.CallExpr:
		return r.callInput(ctx, x)
	}
	return nil, fmt.Errorf("expression %T", expr)
}

func (r goRegistrationReader) stringListInput(ctx context.Context, lit *ast.CompositeLit) (dagql.Input, error) {
	if _, ok := lit.Type.(*ast.ArrayType); !ok {
		return nil, fmt.Errorf("composite literal %T", lit.Type)
	}
	list := dagql.ArrayInput[dagql.String]{}
	for _, elt := range lit.Elts {
		v, err := r.input(ctx, elt)
		if err != nil {
			return nil, err
		}
		s, ok := v.(dagql.String)
		if !ok {
			return nil, fmt.Errorf("list element %T", v)
		}
		list = append(list, s)
	}
	return list, nil
}

// callInput is an argument that is a call: dagger.JSON of a literal, or another
// chain of calls on dag, passed by its ID.
func (r goRegistrationReader) callInput(ctx context.Context, call *ast.CallExpr) (dagql.Input, error) {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && isGoIdent(sel.X, "dagger") && sel.Sel.Name == "JSON" && len(call.Args) == 1 {
		v, err := r.input(ctx, call.Args[0])
		if err != nil {
			return nil, err
		}
		s, ok := v.(dagql.String)
		if !ok {
			return nil, fmt.Errorf("JSON of %T", v)
		}
		return core.JSON(s), nil
	}
	res, err := r.eval(ctx, call)
	if err != nil {
		return nil, err
	}
	switch obj := res.(type) {
	case dagql.ObjectResult[*core.TypeDef]:
		return goResultIDInput(obj)
	case dagql.ObjectResult[*core.Function]:
		return goResultIDInput(obj)
	case dagql.ObjectResult[*core.SourceMap]:
		return goResultIDInput(obj)
	}
	return nil, fmt.Errorf("argument of type %T", res)
}

func goResultIDInput[T dagql.Typed](res dagql.ObjectResult[T]) (dagql.Input, error) {
	id, err := res.ID()
	if err != nil {
		return nil, err
	}
	return dagql.NewID[T](id), nil
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// GeneratorMergesOwnTypes reports whether the SDK's generator writes a module's
// own types into its bindings by itself, so the module must not also be one of
// its own dependencies: the generator refuses a dependency named after the
// module.
func GeneratorMergesOwnTypes(sdkSource string) bool {
	name, _, err := parseSDKName(sdkSource)
	return err == nil && name == sdkGo
}
