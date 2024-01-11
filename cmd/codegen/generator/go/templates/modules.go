package templates

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	. "github.com/dave/jennifer/jen" // nolint:revive,stylecheck
	"github.com/iancoleman/strcase"
	"golang.org/x/tools/go/packages"
)

const (
	daggerGenFilename     = "dagger.gen.go"
	contextTypename       = "context.Context"
	constructorFuncName   = "New"
	daggerObjectIfaceName = "DaggerObject"
)

func (funcs goTemplateFuncs) isModuleCode() bool {
	return funcs.module != nil
}

/*
moduleMainSrc generates the source code of the main func for Dagger Module code using the Go SDK.

The overall idea is that users just need to create a struct with the same name as their Module and then
add methods to that struct to implement their Module. Methods on that struct become Functions.

They are also free to return custom objects from Functions, which themselves may have methods that become
Functions too. However, only the "top-level" Module struct's Functions will be directly invokable.

This is essentially just the GraphQL execution model.

The implementation works by parsing the user's code and generating a main func that reads function call inputs
from the Engine, calls the relevant function and returns the result. The generated code is mostly a giant switch/case
on the object+function name, with each case doing json deserialization of the input arguments and calling the actual
Go function.
*/
func (funcs goTemplateFuncs) moduleMainSrc() (string, error) {
	// HACK: the code in this func can be pretty flaky and tricky to debug -
	// it's much easier to debug when we actually have stack traces, so we grab
	// those on a panic
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "internal error during module code generation: %v\n", r)
			debug.PrintStack()
			panic(r)
		}
	}()

	if funcs.modulePkg == nil {
		// during bootstrapping, we might not have code yet, since it takes
		// multiple passes.
		return `func main() { panic("no code yet") }`, nil
	}

	ps := &parseState{
		pkg:        funcs.modulePkg,
		fset:       funcs.moduleFset,
		methods:    make(map[string][]method),
		moduleName: funcs.module.Name,
	}

	pkgScope := funcs.modulePkg.Types.Scope()

	objFunctionCases := map[string][]Code{}

	createMod := Qual("dag", "CurrentModule").Call()

	objs := []types.Object{}
	for _, name := range pkgScope.Names() {
		obj := pkgScope.Lookup(name)
		if obj == nil {
			continue
		}

		objs = append(objs, obj)
	}

	// preserve definition order, so developer can keep more important /
	// entrypoint types higher up
	sort.Slice(objs, func(i, j int) bool {
		return objs[i].Pos() < objs[j].Pos()
	})

	tps := []types.Type{}
	for _, obj := range objs {
		// check if this is the constructor func, save it for later if so
		if ok := ps.checkConstructor(obj); ok {
			continue
		}

		// check if this is the DaggerObject interface
		if ok := ps.checkDaggerObjectIface(obj); ok {
			continue
		}

		if ps.isMainModuleObject(obj.Name()) || ps.isDaggerGenerated(obj) {
			tps = append(tps, obj.Type())
		}
	}

	if ps.daggerObjectIfaceType == nil {
		return "", fmt.Errorf("cannot find default codegen %s interface", daggerObjectIfaceName)
	}

	added := map[string]struct{}{}

	implementationCode := Empty()
	for len(tps) != 0 {
		var nextTps []types.Type
		for _, tp := range tps {
			named, isNamed := tp.(*types.Named)
			if !isNamed {
				continue
			}

			obj := named.Obj()
			if obj.Pkg() != funcs.modulePkg.Types {
				// the type must be created in the target package
				continue
			}
			if !obj.Exported() {
				// the type must be exported
				return "", fmt.Errorf("cannot code-generate unexported type %s", obj.Name())
			}

			// avoid adding a struct definition twice (if it's referenced in two function signatures)
			if _, ok := added[obj.Name()]; ok {
				continue
			}

			switch underlyingObj := named.Underlying().(type) {
			case *types.Struct:
				strct := underlyingObj
				objTypeSpec, err := ps.parseGoStruct(strct, named)
				if err != nil {
					return "", err
				}
				if objTypeSpec == nil {
					// not including in module schema, skip it
					continue
				}

				if err := ps.fillObjectFunctionCases(named, objFunctionCases); err != nil {
					// errors indicate an internal problem rather than something w/ user code, so error instead
					return "", fmt.Errorf("failed to generate function cases for %s: %w", obj.Name(), err)
				}

				// Add the object to the module
				objTypeDefCode, err := objTypeSpec.TypeDefCode()
				if err != nil {
					return "", fmt.Errorf("failed to generate type def code for %s: %w", obj.Name(), err)
				}
				createMod = dotLine(createMod, "WithObject").Call(Add(Line(), objTypeDefCode))
				added[obj.Name()] = struct{}{}

				implCode, err := objTypeSpec.ImplementationCode()
				if err != nil {
					return "", fmt.Errorf("failed to generate json method code for %s: %w", obj.Name(), err)
				}
				implementationCode.Add(implCode).Line()

				// If the object has any extra sub-types (e.g. for function return
				// values), add them to the list of types to process
				nextTps = append(nextTps, objTypeSpec.GoSubTypes()...)

			case *types.Interface:
				iface := underlyingObj
				ifaceTypeSpec, err := ps.parseGoIface(iface, named)
				if err != nil {
					return "", err
				}
				if ifaceTypeSpec == nil {
					// not including in module schema, skip it
					continue
				}

				// Add the iface to the module
				ifaceTypeDefCode, err := ifaceTypeSpec.TypeDefCode()
				if err != nil {
					return "", fmt.Errorf("failed to generate type def code for %s: %w", obj.Name(), err)
				}
				createMod = dotLine(createMod, "WithInterface").Call(Add(Line(), ifaceTypeDefCode))
				added[obj.Name()] = struct{}{}

				implCode, err := ifaceTypeSpec.ImplementationCode()
				if err != nil {
					return "", fmt.Errorf("failed to generate concrete struct code for %s: %w", obj.Name(), err)
				}
				implementationCode.Add(implCode).Line()

				// If the object has any extra sub-types (e.g. for function return
				// values), add them to the list of types to process
				nextTps = append(nextTps, ifaceTypeSpec.GoSubTypes()...)
			}
		}

		tps, nextTps = nextTps, nil
	}

	return strings.Join([]string{
		fmt.Sprintf("%#v", implementationCode),
		mainSrc,
		invokeSrc(objFunctionCases, createMod),
	}, "\n"), nil
}

func dotLine(a *Statement, id string) *Statement {
	return a.Op(".").Line().Id(id)
}

const (
	// The static part of the generated code. It calls out to the "invoke" func, which is the mostly
	// dynamically generated code that actually calls the user's functions.
	mainSrc = `func main() {
	ctx := context.Background()

	fnCall := dag.CurrentFunctionCall()
	parentName, err := fnCall.ParentName(ctx)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(2)
	}
	fnName, err := fnCall.Name(ctx)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(2)
	}
	parentJson, err := fnCall.Parent(ctx)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(2)
	}
	fnArgs, err := fnCall.InputArgs(ctx)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(2)
	}

	inputArgs := map[string][]byte{}
	for _, fnArg := range fnArgs {
		argName, err := fnArg.Name(ctx)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(2)
		}
		argValue, err := fnArg.Value(ctx)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(2)
		}
		inputArgs[argName] = []byte(argValue)
	}

	result, err := invoke(ctx, []byte(parentJson), parentName, fnName, inputArgs)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(2)
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(2)
	}
	_, err = fnCall.ReturnValue(ctx, JSON(resultBytes))
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(2)
	}
}
`
	parentJSONVar  = "parentJSON"
	parentNameVar  = "parentName"
	fnNameVar      = "fnName"
	inputArgsVar   = "inputArgs"
	invokeFuncName = "invoke"
)

// the source code of the invoke func, which is the mostly dynamically generated code that actually calls the user's functions
func invokeSrc(objFunctionCases map[string][]Code, createMod Code) string {
	// each `case` statement for every object name, which makes up the body of the invoke func
	objCases := []Code{}
	for objName, functionCases := range objFunctionCases {
		objCases = append(objCases, Case(Lit(objName)).Block(Switch(Id(fnNameVar)).Block(functionCases...)))
	}
	// when the object name is empty, return the module definition
	objCases = append(objCases, Case(Lit("")).Block(
		Return(createMod, Nil()),
	))
	// default case (return error)
	objCases = append(objCases, Default().Block(
		Return(Nil(), Qual("fmt", "Errorf").Call(Lit("unknown object %s"), Id(parentNameVar))),
	))
	objSwitch := Switch(Id(parentNameVar)).Block(objCases...)

	// func invoke(
	invokeFunc := Func().Id(invokeFuncName).Params(
		// ctx context.Context,
		Id("ctx").Qual("context", "Context"),
		// parentJSON []byte,
		Id(parentJSONVar).Index().Byte(),
		// parentName string,
		Id(parentNameVar).String(),
		// fnName string,
		Id(fnNameVar).String(),
		// inputArgs map[string][]byte,
		Id(inputArgsVar).Map(String()).Index().Byte(),
	).Params(
		// ) (_ any,
		Id("_").Id("any"),
		// err error)
		Id("err").Error(),
	).Block(objSwitch)

	return fmt.Sprintf("%#v", invokeFunc)
}

// TODO: use jennifer for generating this magical typedef
func renderNameOrStruct(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		return "*" + renderNameOrStruct(ptr.Elem())
	}
	if sl, ok := t.(*types.Slice); ok {
		return "[]" + renderNameOrStruct(sl.Elem())
	}
	if st, ok := t.(*types.Struct); ok {
		result := "struct {\n"
		for i := 0; i < st.NumFields(); i++ {
			if !st.Field(i).Embedded() {
				result += st.Field(i).Name() + " "
			}
			result += renderNameOrStruct(st.Field(i).Type())
			if tag := st.Tag(i); tag != "" {
				result += " `" + tag + "`"
			}
			result += "\n"
		}
		result += "}"
		return result
	}
	if named, ok := t.(*types.Named); ok {
		if _, ok := named.Underlying().(*types.Interface); ok {
			return "*" + formatIfaceImplName(named.Obj().Name())
		}

		// Assume local
		//
		// TODO: this isn't always true - we likely want to support returning
		// types from other packages as well. However, this is tricky - how
		// should we handle returning *time.Time? We should probably convert
		// this to a graphql type that all langs can convert to their native
		// representation.
		base := named.Obj().Name()
		if typeArgs := named.TypeArgs(); typeArgs.Len() > 0 {
			base += "["
			for i := 0; i < typeArgs.Len(); i++ {
				if i > 0 {
					base += ", "
				}
				base += renderNameOrStruct(typeArgs.At(i))
			}
			base += "]"
		}
		return base
	}
	// HACK(vito): this is passed to Id(), which is a bit weird, but works
	return t.String()
}

var checkErrStatement = If(Err().Op("!=").Nil()).Block(
	// fmt.Println(err.Error())
	Qual("fmt", "Println").Call(Err().Dot("Error").Call()),
	// os.Exit(2)
	Qual("os", "Exit").Call(Lit(2)),
)

func (ps *parseState) checkConstructor(obj types.Object) bool {
	fn, isFn := obj.(*types.Func)
	if !isFn {
		return false
	}
	if fn.Name() != constructorFuncName {
		return false
	}

	ps.constructor = fn
	return true
}

func (ps *parseState) checkDaggerObjectIface(obj types.Object) bool {
	named, isNamed := obj.Type().(*types.Named)
	if !isNamed {
		return false
	}
	iface, isIface := named.Underlying().(*types.Interface)
	if !isIface {
		return false
	}
	if named.Obj().Name() != daggerObjectIfaceName {
		return false
	}

	ps.daggerObjectIfaceType = iface
	return true
}

// fillObjectFunctionCases recursively fills out the `cases` map with entries for object name -> `case` statement blocks
// for each function in that object
func (ps *parseState) fillObjectFunctionCases(type_ types.Type, cases map[string][]Code) error {
	var objName string
	switch x := type_.(type) {
	case *types.Pointer:
		return ps.fillObjectFunctionCases(x.Elem(), cases)
	case *types.Named:
		objName = x.Obj().Name()
	default:
		return nil
	}

	if existingCases := cases[objName]; len(existingCases) > 0 {
		// handles recursive types, e.g. objects with chainable methods that return themselves
		return nil
	}

	hasConstructor := ps.isMainModuleObject(objName) && ps.constructor != nil

	methods := ps.methods[objName]
	if len(methods) == 0 && !hasConstructor {
		return nil
	}

	for _, method := range methods {
		fnName, sig := method.fn.Name(), method.fn.Type().(*types.Signature)
		if err := ps.fillObjectFunctionCase(objName, fnName, fnName, sig, method.paramSpecs, cases); err != nil {
			return err
		}
	}

	// main module object constructor case
	if hasConstructor {
		sig, ok := ps.constructor.Type().(*types.Signature)
		if !ok {
			return fmt.Errorf("expected %s to be a function, got %T", constructorFuncName, ps.constructor.Type())
		}
		paramSpecs, err := ps.parseParamSpecs(nil, ps.constructor)
		if err != nil {
			return fmt.Errorf("failed to parse %s function: %w", constructorFuncName, err)
		}

		// Validate the constructor returns the main module object (further general validation happens in fillObjectFunctionCase)
		results := sig.Results()
		if results.Len() == 0 {
			return fmt.Errorf("%s must return a value", constructorFuncName)
		}
		resultType := results.At(0).Type()
		if ptrType, ok := resultType.(*types.Pointer); ok {
			resultType = ptrType.Elem()
		}
		namedType, ok := resultType.(*types.Named)
		if !ok {
			return fmt.Errorf("%s must return the main module object %q", constructorFuncName, objName)
		}
		if namedType.Obj().Name() != objName {
			return fmt.Errorf("%s must return the main module object %q", constructorFuncName, objName)
		}

		if err := ps.fillObjectFunctionCase(objName, ps.constructor.Name(), "", sig, paramSpecs, cases); err != nil {
			return err
		}
	}

	// default case (return error)
	cases[objName] = append(cases[objName], Default().Block(
		Return(Nil(), Qual("fmt", "Errorf").Call(Lit("unknown function %s"), Id(fnNameVar))),
	))

	return nil
}

func (ps *parseState) fillObjectFunctionCase(
	objName string,
	fnName string,
	caseName string, // separate from fnName to handle constructor where the caseName is empty string
	sig *types.Signature,
	paramSpecs []paramSpec,
	cases map[string][]Code,
) error {
	statements := []Code{}

	parentVarName := "parent"
	statements = append(statements,
		Var().Id(parentVarName).Id(objName),
		Err().Op("=").Qual("json", "Unmarshal").Call(Id(parentJSONVar), Op("&").Id(parentVarName)),
		checkErrStatement,
	)

	var fnCallArgs []Code
	if sig.Recv() != nil {
		fnCallArgs = []Code{Op("&").Id(parentVarName)}
	}

	vars := map[string]struct{}{}
	for i, spec := range paramSpecs {
		if i == 0 && spec.paramType.String() == contextTypename {
			fnCallArgs = append(fnCallArgs, Id("ctx"))
			continue
		}

		var varName string
		var varType types.Type
		var target *Statement
		if spec.parent == nil {
			varName = strcase.ToLowerCamel(spec.name)
			varType = spec.paramType
			target = Id(varName)
		} else {
			// create only one declaration for option structs
			varName = spec.parent.name
			varType = spec.parent.paramType
			target = Id(spec.parent.name).Dot(spec.name)
		}

		if _, ok := vars[varName]; !ok {
			vars[varName] = struct{}{}

			tp := varType
			fnCallArgCode := Id(varName)
			tp2, fnCallArgCode2, ok, err := ps.functionCallArgCode(varType, Id(varName))
			if err != nil {
				return fmt.Errorf("failed to get function call arg code for %s: %w", varName, err)
			}
			if ok {
				tp = tp2
				fnCallArgCode = fnCallArgCode2
			}

			statements = append(statements, Var().Id(varName).Id(renderNameOrStruct(tp)))
			if spec.variadic {
				fnCallArgs = append(fnCallArgs, fnCallArgCode.Op("..."))
			} else {
				fnCallArgs = append(fnCallArgs, fnCallArgCode)
			}
		}

		statements = append(statements,
			If(Id(inputArgsVar).Index(Lit(spec.name)).Op("!=").Nil()).Block(
				Err().Op("=").Qual("json", "Unmarshal").Call(
					Index().Byte().Parens(Id(inputArgsVar).Index(Lit(spec.name))),
					Op("&").Add(target),
				),
				checkErrStatement,
			))
	}

	results := sig.Results()

	var callStatement *Statement
	if sig.Recv() != nil {
		callStatement = Parens(Op("*").Id(objName)).Dot(fnName).Call(fnCallArgs...)
	} else {
		callStatement = Id(fnName).Call(fnCallArgs...)
	}

	switch results.Len() {
	case 2:
		// assume second value is error

		if results.At(1).Type().String() != errorTypeName {
			// sanity check
			return fmt.Errorf("second return value must be error, have %s", results.At(1).Type().String())
		}

		statements = append(statements, Return(callStatement))
		cases[objName] = append(cases[objName], Case(Lit(caseName)).Block(statements...))

		if err := ps.fillObjectFunctionCases(results.At(0).Type(), cases); err != nil {
			return err
		}

		return nil

	case 1:
		if results.At(0).Type().String() == errorTypeName {
			// void error return

			statements = append(statements, Return(Nil(), callStatement))
			cases[objName] = append(cases[objName], Case(Lit(caseName)).Block(statements...))
		} else {
			// non-error return

			statements = append(statements, Return(callStatement, Nil()))
			cases[objName] = append(cases[objName], Case(Lit(caseName)).Block(statements...))

			if err := ps.fillObjectFunctionCases(results.At(0).Type(), cases); err != nil {
				return err
			}
		}

		return nil

	case 0:
		// void return
		//
		// NB(vito): it's really weird to have a fully void return (not even
		// error), but we should still support it for completeness.

		statements = append(statements,
			callStatement,
			Return(Nil(), Nil()))
		cases[objName] = append(cases[objName], Case(Lit(caseName)).Block(statements...))

		return nil

	default:
		return fmt.Errorf("unexpected number of results from function %s: %d", fnName, results.Len())
	}
}

type parseState struct {
	pkg        *packages.Package
	fset       *token.FileSet
	methods    map[string][]method
	moduleName string

	// If it exists, constructor is the New func that returns the main module object
	constructor *types.Func

	// the DaggerObject interface type, used to check that user defined interfaces embed it
	daggerObjectIfaceType *types.Interface
}

func (ps *parseState) isMainModuleObject(name string) bool {
	return strcase.ToCamel(ps.moduleName) == strcase.ToCamel(name)
}

type method struct {
	fn *types.Func

	paramSpecs []paramSpec
}

// astSpecForNamedType returns the *ast* type spec for the given Named type. This is needed
// because the types.Named object does not have the comments associated with the type, which
// we want to parse.
func (ps *parseState) astSpecForNamedType(namedType *types.Named) (*ast.TypeSpec, error) {
	tokenFile := ps.fset.File(namedType.Obj().Pos())
	if tokenFile == nil {
		return nil, fmt.Errorf("no file for %s", namedType.Obj().Name())
	}
	for _, f := range ps.pkg.Syntax {
		if ps.fset.File(f.Pos()) != tokenFile {
			continue
		}
		for _, decl := range f.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if typeSpec.Name.Name == namedType.Obj().Name() {
					if typeSpec.Doc == nil {
						typeSpec.Doc = genDecl.Doc
					}
					return typeSpec, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no decl for %s", namedType.Obj().Name())
}

// declForFunc returns the *ast* func decl for the given Func type. This is needed
// because the types.Func object does not have the comments associated with the type, which
// we want to parse.
func (ps *parseState) declForFunc(parentType *types.Named, fnType *types.Func) (*ast.FuncDecl, error) {
	var recv *types.Named
	if signature, ok := fnType.Type().(*types.Signature); ok && signature.Recv() != nil {
		tp := signature.Recv().Type()
		for {
			if p, ok := tp.(*types.Pointer); ok {
				tp = p.Elem()
				continue
			}
			break
		}
		recv, _ = tp.(*types.Named)
	}

	var underlying types.Type
	if parentType != nil {
		underlying = parentType.Underlying()
	}

	tokenFile := ps.fset.File(fnType.Pos())
	if tokenFile == nil {
		return nil, fmt.Errorf("no file for %s", fnType.Name())
	}

	for _, f := range ps.pkg.Syntax {
		if ps.fset.File(f.Pos()) != tokenFile {
			continue
		}
		for _, decl := range f.Decls {
			switch underlying.(type) {
			case *types.Struct, nil:
				// top-level func or method on object case
				for _, decl := range f.Decls {
					fnDecl, ok := decl.(*ast.FuncDecl)
					if !ok {
						continue
					}
					if fnDecl.Name.Name != fnType.Name() {
						continue
					}
					if recv != nil {
						if len(fnDecl.Recv.List) != 1 {
							continue
						}

						tp := fnDecl.Recv.List[0].Type
						for {
							if star, ok := tp.(*ast.StarExpr); ok {
								tp = star.X
								continue
							}
							break
						}
						ident, ok := tp.(*ast.Ident)
						if !ok {
							continue
						}
						if ident.Name != recv.Obj().Name() {
							continue
						}
					}
					return fnDecl, nil
				}

			case *types.Interface:
				// interface method case
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if typeSpec.Name.Name != parentType.Obj().Name() {
						continue
					}
					iface, ok := typeSpec.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					for _, ifaceField := range iface.Methods.List {
						ifaceFieldFunc, ok := ifaceField.Type.(*ast.FuncType)
						if !ok {
							continue
						}
						ifaceFieldFuncName := ifaceField.Names[0].Name
						if ifaceFieldFuncName != fnType.Name() {
							continue
						}
						return &ast.FuncDecl{
							Doc:  ifaceField.Doc,
							Type: ifaceFieldFunc,
						}, nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("no decl for %s", fnType.Name())
}

// commentForFuncField returns the *ast* comment group for the given position. This
// is needed because function args (despite being fields) don't have comments
// associated with them, so this is a neat little hack to get them out.
func (ps *parseState) commentForFuncField(fnDecl *ast.FuncDecl, unpackedParams []*ast.Field, i int) (docComment *ast.CommentGroup, lineComment *ast.CommentGroup) {
	pos := unpackedParams[i].Pos()
	tokenFile := ps.fset.File(pos)
	if tokenFile == nil {
		return nil, nil
	}
	line := tokenFile.Line(pos)

	allowDocComment := true
	allowLineComment := true
	if i == 0 {
		fieldStartLine := tokenFile.Line(fnDecl.Type.Params.Pos())
		if fieldStartLine == line || fieldStartLine == line-1 {
			// the argument is on the same (or next) line as the function
			// declaration, so there is no doc comment to find
			allowDocComment = false
		}
	} else {
		prevArgLine := tokenFile.Line(unpackedParams[i-1].Pos())
		if prevArgLine == line || prevArgLine == line-1 {
			// the argument is on the same (or next) line as the previous
			// argument, so again there is no doc comment to find
			allowDocComment = false
		}
	}
	if i+1 < len(fnDecl.Type.Params.List) {
		nextArgLine := tokenFile.Line(unpackedParams[i+1].Pos())
		if nextArgLine == line {
			// the argument is on the same line as the next argument, so there is
			// no line comment to find
			allowLineComment = false
		}
	} else {
		fieldEndLine := tokenFile.Line(fnDecl.Type.Params.End())
		if fieldEndLine == line {
			// the argument is on the same line as the end of the field list, so there
			// is no line comment to find
			allowLineComment = false
		}
	}

	for _, f := range ps.pkg.Syntax {
		if ps.fset.File(f.Pos()) != tokenFile {
			continue
		}

		if allowDocComment {
			// take the last position in the last line, and try and find a
			// comment that contains it
			npos := tokenFile.LineStart(tokenFile.Line(pos)) - 1
			for _, comment := range f.Comments {
				if comment.Pos() <= npos && npos <= comment.End() {
					docComment = comment
					break
				}
			}
		}

		if allowLineComment {
			// take the last position in the current line, and try and find a
			// comment that contains it
			npos := tokenFile.LineStart(tokenFile.Line(pos)+1) - 1
			for _, comment := range f.Comments {
				if comment.Pos() <= npos && npos <= comment.End() {
					lineComment = comment
					break
				}
			}
		}
	}
	return docComment, lineComment
}

func (ps *parseState) isDaggerGenerated(obj types.Object) bool {
	tokenFile := ps.fset.File(obj.Pos())
	return filepath.Base(tokenFile.Name()) == daggerGenFilename
}

// returns whether the given type is an Optional and, if so, the wrapped type
func (ps *parseState) isOptionalWrapper(typ types.Type) (types.Type, bool, error) {
	named, ok := typ.(*types.Named)
	if !ok {
		return nil, false, nil
	}
	if named.Obj().Name() != "Optional" || !ps.isDaggerGenerated(named.Obj()) {
		return nil, false, nil
	}
	typeArgs := named.TypeArgs()
	if typeArgs.Len() != 1 {
		return nil, false, fmt.Errorf("optional type must have exactly one type argument")
	}
	return typeArgs.At(0), true, nil
}

/*
functionCallArgCode takes a type and code for providing an arg of that type (just
the name of the arg variable as a base) and returns the type that should be used
to declare the arg as well as the code that should be used to provide the arg
variable to a function call

This is needed to handle various special cases:
* Function args that are various degrees of pointeriness (i.e. *string, **int, etc.)
* Concrete structs implementing an interface that will be provided as an arg
* Slices and Optional wrappers of the above
*/
func (ps *parseState) functionCallArgCode(t types.Type, access *Statement) (types.Type, *Statement, bool, error) {
	switch t := t.(type) {
	case *types.Pointer:
		// taking the address of an address isn't allowed - so we use a ptr
		// helper function
		t2, access2, ok, err := ps.functionCallArgCode(t.Elem(), access)
		if err != nil {
			return nil, nil, false, err
		}
		if ok {
			/*
				Taking the address of an address isn't allowed - so we use a ptr helper
				function. e.g.:
					ptr(access)
			*/
			return t2, Id("ptr").Call(access2), true, nil
		}
		return nil, nil, false, nil
	case *types.Named:
		wrappedType, isOptionalType, err := ps.isOptionalWrapper(t)
		if err != nil {
			return nil, nil, false, err
		}
		if isOptionalType {
			// Check if this is an Optional of an interface
			wrappedNamed, ok := wrappedType.(*types.Named)
			if !ok {
				return t, access, true, nil
			}
			_, ok = wrappedNamed.Underlying().(*types.Interface)
			if !ok {
				return t, access, true, nil
			}

			/*
				Need to convert concrete impl struct wrapped by Optional to interface wrapped
				by Optional. e.g.:
					convertOptionalVal(access, (*ifaceImpl).toIface)
			*/
			return t, Id("convertOptionalVal").Call(
				access,
				Parens(Op("*").Id(formatIfaceImplName(wrappedNamed.Obj().Name()))).Dot("toIface"),
			), true, nil
		}

		if _, ok := t.Underlying().(*types.Interface); ok {
			/*
				Need to convert concrete impl struct interface. e.g.:
					access.toIface
			*/
			return t, access.Dot("toIface").Call(), true, nil
		}
		return nil, nil, false, nil
	case *types.Slice:
		elemNamed, ok := t.Elem().(*types.Named)
		if !ok {
			return nil, nil, false, nil
		}
		_, ok = elemNamed.Underlying().(*types.Interface)
		if !ok {
			return nil, nil, false, nil
		}

		/*
			Need to convert slice of concrete impl structs to slice of interface e.g.:
				convertSlice(access, (*ifaceImpl).toIface)
		*/
		return t, Id("convertSlice").Call(
			access,
			Parens(Op("*").Id(formatIfaceImplName(elemNamed.Obj().Name()))).Dot("toIface"),
		), true, nil
	case *types.Struct:
		// inline struct case
		return t, access, true, nil
	default:
		return nil, nil, false, nil
	}
}

var pragmaCommentRegexp = regexp.MustCompile(`\+\s*(\S+?)(?:=(.+))?(?:\n|$)`)

// parsePragmaComment parses a dagger "pragma", that is used to define additional metadata about a parameter.
func parsePragmaComment(comment string) (data map[string]string, rest string) {
	data = map[string]string{}
	lastEnd := 0
	for _, v := range pragmaCommentRegexp.FindAllStringSubmatchIndex(comment, -1) {
		var key, value string
		if v[2] != -1 {
			key = comment[v[2]:v[3]]
		}
		if v[4] != -1 {
			value = comment[v[4]:v[5]]
		}
		data[key] = value

		rest += comment[lastEnd:v[0]]
		lastEnd = v[1]
	}
	rest += comment[lastEnd:]

	return data, rest
}

func asInlineStruct(t types.Type) (*types.Struct, bool) {
	switch t := t.(type) {
	case *types.Pointer:
		return asInlineStruct(t.Elem())
	case *types.Struct:
		return t, true
	default:
		return nil, false
	}
}

func asInlineStructAst(t ast.Node) (*ast.StructType, bool) {
	switch t := t.(type) {
	case *ast.StarExpr:
		return asInlineStructAst(t.X)
	case *ast.StructType:
		return t, true
	default:
		return nil, false
	}
}

func unpackASTFields(fields *ast.FieldList) []*ast.Field {
	var unpacked []*ast.Field
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			unpacked = append(unpacked, field)
			continue
		}
		for i, name := range field.Names {
			field := *field
			field.Names = []*ast.Ident{name}
			if i != 0 {
				field.Doc = nil
				field.Comment = nil
			}
			unpacked = append(unpacked, &field)
		}
	}
	return unpacked
}
