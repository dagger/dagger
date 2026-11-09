package sdk

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// goBindingsMatchSource reports an error when dagger.gen.go doesn't type-check
// against the module's source: such a module doesn't compile, so what its
// registration code says isn't an API it has.
//
// It checks the generated file's code against the declarations of the module's
// package and of its generated bindings, in the engine, with no toolchain. The
// module's dependencies aren't there, so an import of anything else is an empty
// package: whatever uses one has no type to get wrong, and only an error in the
// generated file that isn't about such a package counts. An error in a function
// body of the module's own is left to the build, at the first call.
//
// readImport returns the Go files of a package the module generates, and false
// for any other package.
func goBindingsMatchSource(
	generated string,
	sources map[string]string,
	readImport func(path string) (map[string]string, bool, error),
) error {
	fset := token.NewFileSet()
	gen, err := parser.ParseFile(fset, goSDKGeneratedFile, generated, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	files, err := parseGoDeclarations(fset, sources, gen.Name.Name)
	if err != nil {
		return err
	}
	importer := &goDeclarationsImporter{
		fset:       fset,
		readImport: readImport,
		packages:   map[string]*types.Package{},
		empty:      map[string]bool{},
	}
	importPaths := map[string]string{}
	for _, spec := range gen.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return err
		}
		name := goImportBaseName(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		importPaths[name] = importPath
	}
	var errs []error
	conf := types.Config{
		Importer:    importer,
		FakeImportC: true,
		Error: func(err error) {
			var typeErr types.Error
			if !errors.As(err, &typeErr) ||
				fset.Position(typeErr.Pos).Filename != goSDKGeneratedFile ||
				importer.aboutEmptyPackage(typeErr.Msg, importPaths) {
				return
			}
			errs = append(errs, err)
		},
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	// The generated file goes last, so a declaration it shares with the source
	// is an error in the generated file.
	pkg, _ := conf.Check(gen.Name.Name, fset, append(files, gen), info)
	if importer.err != nil {
		return importer.err
	}
	for id, obj := range info.Uses {
		fn, ok := obj.(*types.Func)
		if !ok || fn.Pkg() != pkg ||
			fset.Position(id.Pos()).Filename != goSDKGeneratedFile ||
			fset.Position(fn.Pos()).Filename == goSDKGeneratedFile {
			continue
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			continue
		}
		// A type from a package that isn't there has nothing to compare, and
		// the generator accepts none in a function it dispatches to.
		if goTupleUsesMissingType(sig.Params()) || goTupleUsesMissingType(sig.Results()) {
			errs = append(errs, fmt.Errorf("%s: can't check the signature of %s", goSDKGeneratedFile, fn.Name()))
		}
	}
	return errors.Join(errs...)
}

func goTupleUsesMissingType(tuple *types.Tuple) bool {
	for v := range tuple.Variables() {
		if goUsesMissingType(v.Type(), map[types.Type]bool{}) {
			return true
		}
	}
	return false
}

// goUsesMissingType reports whether a type is, or is built on, a type from a
// package that isn't there. A struct or interface stops it: what it holds is
// for the generated code's marshalling and wrappers, which type-check on their
// own, and may legitimately hold such types.
func goUsesMissingType(typ types.Type, seen map[types.Type]bool) bool {
	typ = types.Unalias(typ)
	if seen[typ] {
		return false
	}
	seen[typ] = true
	switch typ := typ.(type) {
	case *types.Basic:
		return typ.Kind() == types.Invalid
	case *types.Pointer:
		return goUsesMissingType(typ.Elem(), seen)
	case *types.Slice:
		return goUsesMissingType(typ.Elem(), seen)
	case *types.Array:
		return goUsesMissingType(typ.Elem(), seen)
	case *types.Map:
		return goUsesMissingType(typ.Key(), seen) || goUsesMissingType(typ.Elem(), seen)
	case *types.Chan:
		return goUsesMissingType(typ.Elem(), seen)
	case *types.Named:
		switch typ.Underlying().(type) {
		case *types.Struct, *types.Interface:
			return false
		}
		// `type Delay time.Duration`, `type Failure *os.PathError`.
		return goUsesMissingType(typ.Underlying(), seen)
	}
	return false
}

// parseGoDeclarations parses the files a build compiles for a package, without
// their function bodies. pkg, when set, leaves out files of another package.
//
// Files are chosen for one fixed architecture, not this engine's: a definition
// is cached by the source, whichever engine read it, so its check must come out
// the same on every engine. A module whose architectures declare different APIs
// gets the fixed one's declarations here and the truth from its build. Runtimes
// build without cgo, so a file that imports C isn't one of them.
func parseGoDeclarations(fset *token.FileSet, sources map[string]string, pkg string) ([]*ast.File, error) {
	ctx := build.Default
	ctx.GOOS = "linux"
	ctx.GOARCH = "amd64"
	ctx.BuildTags = nil
	ctx.ToolTags = []string{"amd64.v1"}
	ctx.CgoEnabled = false
	ctx.JoinPath = path.Join
	ctx.OpenFile = func(name string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(sources[path.Base(name)])), nil
	}
	var files []*ast.File
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		match, err := ctx.MatchFile(".", name)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		file, err := parser.ParseFile(fset, name, sources[name], parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		if pkg != "" && file.Name.Name != pkg {
			continue
		}
		if slices.ContainsFunc(file.Imports, func(spec *ast.ImportSpec) bool { return spec.Path.Value == `"C"` }) {
			continue
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				fn.Body = nil
			}
		}
		files = append(files, file)
	}
	return files, nil
}

// goDeclarationsImporter imports a module's generated packages from their
// source, and everything else as an empty package. The exception is context,
// the one package from outside that generated code passes to the module's own
// functions.
type goDeclarationsImporter struct {
	fset       *token.FileSet
	readImport func(path string) (map[string]string, bool, error)
	packages   map[string]*types.Package
	empty      map[string]bool
	err        error
}

var goUndefinedInPackage = regexp.MustCompile(`^undefined: (\w+)\.\w+$`)

// aboutEmptyPackage reports whether an error in a file with these imports is
// that a package that isn't there lacks a name.
func (i *goDeclarationsImporter) aboutEmptyPackage(msg string, importPaths map[string]string) bool {
	match := goUndefinedInPackage.FindStringSubmatch(msg)
	return match != nil && i.empty[importPaths[match[1]]]
}

func (i *goDeclarationsImporter) Import(importPath string) (*types.Package, error) {
	if importPath == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg, ok := i.packages[importPath]; ok {
		return pkg, nil
	}
	pkg, err := i.importPackage(importPath)
	if err != nil {
		// A problem with the check isn't a problem with the module's types.
		if i.err == nil {
			i.err = err
		}
		return nil, err
	}
	i.packages[importPath] = pkg
	return pkg, nil
}

func (i *goDeclarationsImporter) importPackage(importPath string) (*types.Package, error) {
	sources, generated, err := i.readImport(importPath)
	if err != nil {
		return nil, fmt.Errorf("read package %s: %w", importPath, err)
	}
	name := goImportBaseName(importPath)
	if importPath == "context" {
		sources = map[string]string{"context.go": "package context\n\ntype Context interface{}\n"}
		i.empty[importPath] = true
	} else if !generated {
		pkg := types.NewPackage(importPath, name)
		pkg.MarkComplete()
		i.empty[importPath] = true
		return pkg, nil
	}
	files, err := parseGoDeclarations(i.fset, sources, "")
	if err != nil {
		return nil, fmt.Errorf("parse package %s: %w", importPath, err)
	}
	conf := types.Config{
		Importer:         i,
		IgnoreFuncBodies: true,
		FakeImportC:      true,
		// Its imports are empty here too, so its own errors say nothing.
		Error: func(error) {},
	}
	pkg, _ := conf.Check(importPath, i.fset, files, nil)
	if pkg == nil {
		return nil, fmt.Errorf("check package %s", importPath)
	}
	return pkg, nil
}

// goImportBaseName is the name code uses for an import that doesn't name it.
func goImportBaseName(importPath string) string {
	base := path.Base(importPath)
	if len(base) > 1 && base[0] == 'v' {
		if _, err := strconv.Atoi(base[1:]); err == nil {
			base = path.Base(path.Dir(importPath))
		}
	}
	if i := strings.Index(base, ".v"); i > 0 {
		base = base[:i]
	}
	return base
}
