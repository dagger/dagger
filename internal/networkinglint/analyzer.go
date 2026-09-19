// Package networkinglint rejects network connections which do not select a
// realm before their first packet.
package networkinglint

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "networkrealm",
	Doc:  "require engine network connections to use a realm",
	Run:  run,
}

var forbiddenPackageFuncs = map[string]map[string]bool{
	"net": {
		"Dial":         true,
		"DialTimeout":  true,
		"Listen":       true,
		"ListenPacket": true,
	},
	"net/http": {
		"Get":      true,
		"Head":     true,
		"Post":     true,
		"PostForm": true,
	},
	"crypto/tls": {
		"Dial":              true,
		"DialWithDialer":    true,
		"DialWithDialerTLS": true,
	},
}

var forbiddenHTTPVars = map[string]bool{
	"DefaultClient": true,
}

func run(pass *analysis.Pass) (any, error) {
	if exemptPackage(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		filename := pass.Fset.Position(file.Pos()).Filename
		if strings.HasSuffix(filename, "_test.go") || generated(file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CallExpr:
				checkCall(pass, node)
			case *ast.SelectorExpr:
				checkHTTPDefault(pass, node)
			case *ast.CompositeLit:
				checkHTTPClient(pass, node)
			}
			return true
		})
	}
	return nil, nil
}

func exemptPackage(path string) bool {
	return path == "github.com/dagger/dagger/engine/telemetry" ||
		path == "github.com/dagger/dagger/engine/realm" ||
		path == "github.com/dagger/dagger/engine/netaccounting" ||
		path == "github.com/dagger/dagger/internal/networkinglint" ||
		strings.HasPrefix(path, "github.com/dagger/dagger/engine/client")
}

func generated(file *ast.File) bool {
	for _, comment := range file.Comments {
		if comment.Pos() > file.Package {
			break
		}
		if strings.Contains(comment.Text(), "Code generated") {
			return true
		}
	}
	return false
}

func checkCall(pass *analysis.Pass, call *ast.CallExpr) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	packageIdent, isPackageCall := selector.X.(*ast.Ident)
	_, isPackageCall = pass.TypesInfo.Uses[packageIdent].(*types.PkgName)
	if fn, ok := pass.TypesInfo.Uses[selector.Sel].(*types.Func); ok && isPackageCall {
		if pkg := fn.Pkg(); pkg != nil && forbiddenPackageFuncs[pkg.Path()][fn.Name()] {
			if networkArgumentIsUnix(pass, call) {
				return
			}
			pass.Reportf(call.Pos(), "%s.%s must use a realm", pkg.Name(), fn.Name())
			return
		}
	}

	selection := pass.TypesInfo.Selections[selector]
	if selection == nil {
		return
	}
	named := receiverNamed(selection.Recv())
	if named == nil || named.Obj().Pkg() == nil {
		return
	}
	path := named.Obj().Pkg().Path()
	name := named.Obj().Name()
	method := selection.Obj().Name()
	if path == "net" && name == "Dialer" && (method == "Dial" || method == "DialContext") {
		if networkArgumentIsUnix(pass, call) {
			return
		}
		pass.Reportf(call.Pos(), "net.Dialer.%s must use realm.Dialer", method)
	}
	if path == "net" && name == "ListenConfig" &&
		(method == "Listen" || method == "ListenPacket") {
		pass.Reportf(call.Pos(), "net.ListenConfig.%s must use a realm", method)
	}
}

func networkArgumentIsUnix(pass *analysis.Pass, call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	arg := call.Args[0]
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok && pass.TypesInfo.Selections[selector] != nil {
		if len(call.Args) < 2 {
			return false
		}
		arg = call.Args[len(call.Args)-2]
	}
	value := pass.TypesInfo.Types[arg].Value
	return value != nil && strings.HasPrefix(value.ExactString(), `"unix`)
}

func checkHTTPDefault(pass *analysis.Pass, selector *ast.SelectorExpr) {
	obj, ok := pass.TypesInfo.Uses[selector.Sel].(*types.Var)
	if !ok || obj.Pkg() == nil || obj.Pkg().Path() != "net/http" || !forbiddenHTTPVars[obj.Name()] {
		return
	}
	pass.Reportf(selector.Pos(), "http.%s must use a realm HTTP client", obj.Name())
}

func checkHTTPClient(pass *analysis.Pass, literal *ast.CompositeLit) {
	typ := pass.TypesInfo.TypeOf(literal.Type)
	named := receiverNamed(typ)
	if named == nil || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "net/http" || named.Obj().Name() != "Client" {
		return
	}
	for _, elt := range literal.Elts {
		field, ok := elt.(*ast.KeyValueExpr)
		if ok {
			if ident, ok := field.Key.(*ast.Ident); ok && ident.Name == "Transport" {
				if isRawHTTPTransport(pass, field.Value) {
					pass.Reportf(field.Value.Pos(), "http.Client Transport must use a realm transport")
				}
				return
			}
		}
	}
	pass.Reportf(literal.Pos(), "http.Client must set a realm Transport")
}

func isRawHTTPTransport(pass *analysis.Pass, expr ast.Expr) bool {
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		expr = unary.X
	}
	if literal, ok := expr.(*ast.CompositeLit); ok {
		named := receiverNamed(pass.TypesInfo.TypeOf(literal.Type))
		return named != nil && named.Obj().Pkg() != nil &&
			named.Obj().Pkg().Path() == "net/http" && named.Obj().Name() == "Transport"
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	obj, ok := pass.TypesInfo.Uses[selector.Sel].(*types.Var)
	return ok && obj.Pkg() != nil && obj.Pkg().Path() == "net/http" &&
		obj.Name() == "DefaultTransport"
}

func receiverNamed(typ types.Type) *types.Named {
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	named, _ := typ.(*types.Named)
	return named
}
