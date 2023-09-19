package templates

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	. "github.com/dave/jennifer/jen" // nolint:revive,stylecheck
	"github.com/iancoleman/strcase"
	"golang.org/x/tools/go/packages"
)

/* TODO:
* Handle IDable core types as inputs args+parents
* Handle chainable methods
   * Need to avoid circular typedef object references, json marshal fails right now
* Handle types from 3rd party imports in the type signature
   * Add packages.NeedImports and packages.NeedDependencies to packages.Load opts, ensure performance is okay (or deal with that by lazy loading)
* Fix problem where changing a function signature requires running `dagger mod sync` twice (first one will result in package errors being seen, second one fixes)
   * Use Overlays field in packages.Config to provide partial generation of dagger.gen.go, without the unupdated code we generate here
* Handle no go.mod being present more gracefully and w/ better error messages
* Handle automatically re-running `dagger mod sync` when invoking functions from CLI, to save users from having to always remember while developing locally
* More flexibility around only returning error, not returning error, etc.
* Support methods defined on non-pointer receivers
*/

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
func (funcs goTemplateFuncs) moduleMainSrc() string {
	fset := token.NewFileSet()
	pkgs, err := packages.Load(&packages.Config{
		Context: funcs.ctx,
		Dir:     funcs.sourceDirectoryPath,
		Tests:   false,
		Fset:    fset,
		Mode: packages.NeedName |
			packages.NeedTypes |
			packages.NeedSyntax |
			packages.NeedTypesInfo,
	}, "./...")
	if err != nil {
		panic(fmt.Sprintf("failed to load packages: %v", err))
	}
	var mainPkg *packages.Package
	for _, pkg := range pkgs {
		if pkg.Name == "main" {
			mainPkg = pkg
			break
		}
	}
	if mainPkg == nil {
		return defaultErrorMainSrc("no main package yet")
	}

	moduleStructName := strcase.ToCamel(funcs.moduleName)
	moduleObj := mainPkg.Types.Scope().Lookup(moduleStructName)
	if moduleObj == nil {
		return defaultErrorMainSrc("no module struct yet")
	}

	ps := &parseState{
		visitedStructs: make(map[string]struct{}),
		pkgs:           pkgs,
		fset:           fset,
		methods:        make(map[methodKey]*types.Signature),
	}

	objFunctionCases := map[string][]Code{}

	if err := ps.fillObjectFunctionCases(moduleObj.Type(), objFunctionCases, moduleStructName); err != nil {
		// errors indicate an internal problem rather than something w/ user code, so panic instead
		panic(err)
	}
	return strings.Join([]string{mainSrc, invokeSrc(objFunctionCases)}, "\n")
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
func invokeSrc(objFunctionCases map[string][]Code) string {
	// each `case` statement for every object name, which makes up the body of the invoke func
	objCases := []Code{}
	for objName, functionCases := range objFunctionCases {
		objCases = append(objCases, Case(Lit(objName)).Block(Switch(Id(fnNameVar)).Block(functionCases...)))
	}
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
		// ) (any,
		Id("any"),
		// error)
		Error(),
	).Block(objSwitch)

	return fmt.Sprintf("%#v", invokeFunc)
}

// fillObjectFunctionCases recursively fills out the `cases` map with entries for object name -> `case` statement blocks
// for each function in that object
func (ps *parseState) fillObjectFunctionCases(type_ types.Type, cases map[string][]Code, moduleName string) error {
	typeDef, err := ps.goTypeToAPIType(type_, nil)
	if err != nil {
		return fmt.Errorf("failed to convert module type: %w", err)
	}
	if typeDef.Kind != dagger.Objectkind {
		return nil
	}
	checkErrStatement := If(Err().Op("!=").Nil()).Block(
		// fmt.Println(err.Error())
		Qual("fmt", "Println").Call(Err().Dot("Error").Call()),
		// os.Exit(2)
		Qual("os", "Exit").Call(Lit(2)),
	)

	objName := typeDef.AsObject.Name
	if existingCases := cases[objName]; len(existingCases) > 0 {
		// handles recursive types, e.g. objects with chainable methods that return themselves
		return nil
	}

	for _, fnDef := range typeDef.AsObject.Functions {
		sig := ps.methods[methodKey{objName, fnDef.Name}]

		statements := []Code{
			Var().Id("err").Error(),
		}

		parentVarName := "parent"
		statements = append(statements,
			Var().Id(parentVarName).Id(objName),
			Err().Op("=").Qual("json", "Unmarshal").Call(Id(parentJSONVar), Op("&").Id(parentVarName)),
			checkErrStatement,
		)

		fnCallArgs := []Code{Op("&").Id(parentVarName)}

		// TODO(vito): should this be required?
		if sig.Params().Len() > 0 && sig.Params().At(0).Type().String() == "context.Context" {
			fnCallArgs = append(fnCallArgs, Id("ctx"))
		}

		for i, argDef := range fnDef.Args {
			statements = append(statements,
				Var().Id(argDef.Name).Id(apiTypeKindToGoType(argDef.TypeDef)),
				Err().Op("=").Qual("json", "Unmarshal").Call(
					Index().Byte().Parens(Id(inputArgsVar).Index(Lit(argDef.Name))),
					Op("&").Id(argDef.Name),
				),
				checkErrStatement,
			)
			switch argDef.TypeDef.Kind {
			case dagger.Stringkind, dagger.Integerkind, dagger.Booleankind, dagger.Listkind:
				fnCallArgs = append(fnCallArgs, Id(argDef.Name))
			case dagger.Objectkind:
				fnCallArgs = append(fnCallArgs, Op("&").Id(argDef.Name))
				if err := ps.fillObjectFunctionCases(sig.Params().At(i).Type(), cases, moduleName); err != nil {
					return err
				}
			}
		}

		results := sig.Results()

		switch results.Len() {
		case 2:
			// assume second value is error

			if results.At(1).Type().String() != errorTypeName {
				// sanity check
				return fmt.Errorf("second return value must be error, have %s", results.At(0).Type().String())
			}

			statements = append(statements, Return(
				Parens(Op("*").Id(objName)).Dot(fnDef.Name).Call(fnCallArgs...),
			))

			cases[objName] = append(cases[objName], Case(Lit(fnDef.Name)).Block(statements...))

			if err := ps.fillObjectFunctionCases(results.At(0).Type(), cases, moduleName); err != nil {
				return err
			}
		case 1:
			if results.At(0).Type().String() == errorTypeName {
				// void error return

				statements = append(statements, Return(
					Nil(),
					Parens(Op("*").Id(objName)).Dot(fnDef.Name).Call(fnCallArgs...),
				))

				cases[objName] = append(cases[objName], Case(Lit(fnDef.Name)).Block(statements...))
			} else {
				// non-error return

				statements = append(statements, Return(
					Parens(Op("*").Id(objName)).Dot(fnDef.Name).Call(fnCallArgs...),
					Nil(),
				))

				cases[objName] = append(cases[objName], Case(Lit(fnDef.Name)).Block(statements...))

				if err := ps.fillObjectFunctionCases(results.At(0).Type(), cases, moduleName); err != nil {
					return err
				}
			}

		case 0:
			// void return
			//
			// NB(vito): it's really weird to have a fully void return (not even
			// error), but we should still support it for completeness.

			statements = append(statements,
				Parens(Op("*").Id(objName)).Dot(fnDef.Name).Call(fnCallArgs...),
				Return(Nil(), Nil()))

			cases[objName] = append(cases[objName], Case(Lit(fnDef.Name)).Block(statements...))

		default:
			return fmt.Errorf("unexpected number of results from method %s: %d", fnDef.Name, results.Len())
		}
	}

	// Special case for object name is module and function name is empty. This is called by the engine when
	// it wants the module to return the definition of the functions it serves rather than actually invoke
	// any of them.
	if objName == moduleName {
		typeDefBytes, err := json.Marshal(typeDef)
		if err != nil {
			return err
		}
		cases[objName] = append(cases[objName], Case(Lit("")).Block(
			/*
				var err error
				typeDefBytes := []byte(`huge json blob`)
				var typeDef TypeDefInput
				err := json.Unmarshal(typeDefBytes, &typeDef)
				if err != nil {
					fmt.Println(err.Error())
					os.Exit(2)
				}
				mod := dag.CurrentModule()
				for _, fnDef := range typeDef.AsObject.Functions {
					mod = mod.WithFunction(dag.NewFunction(fnDef))
				}
				return mod, nil
			*/
			Var().Id("err").Error(),
			Var().Id("typeDefBytes").Index().Byte().Op("=").Index().Byte().Parens(Lit(string(typeDefBytes))),
			Var().Id("typeDef").Id("TypeDefInput"),
			Err().Op("=").Qual("json", "Unmarshal").Call(Id("typeDefBytes"), Op("&").Id("typeDef")),
			checkErrStatement,
			Id("mod").Op(":=").Qual("dag", "CurrentModule").Call(),
			For(List(Id("_"), Id("fnDef")).Op(":=").Range().Id("typeDef").Dot("AsObject").Dot("Functions")).Block(
				Id("mod").Op("=").Id("mod").Dot("WithFunction").Call(Qual("dag", "NewFunction").Call(Id("fnDef"))),
			),
			Return(Id("mod"), Nil()),
		))
	}

	// default case (return error)
	cases[objName] = append(cases[objName], Default().Block(
		Return(Nil(), Qual("fmt", "Errorf").Call(Lit("unknown function %s"), Id(fnNameVar))),
	))

	return nil
}

func apiTypeKindToGoType(typeDef *dagger.TypeDefInput) string {
	switch typeDef.Kind {
	case dagger.Stringkind:
		return "string"
	case dagger.Integerkind:
		return "int"
	case dagger.Booleankind:
		return "bool"
	case dagger.Listkind:
		return "[]" + apiTypeKindToGoType(typeDef.AsList.ElementTypeDef)
	case dagger.Objectkind:
		return typeDef.AsObject.Name
	}
	return ""
}

type parseState struct {
	visitedStructs map[string]struct{}
	pkgs           []*packages.Package
	fset           *token.FileSet
	methods        map[methodKey]*types.Signature
}

type methodKey struct {
	recvName string
	name     string
}

func (ps *parseState) goTypeToAPIType(typ types.Type, named *types.Named) (*dagger.TypeDefInput, error) {
	switch t := typ.(type) {
	case *types.Named:
		// Named types are any types declared like `type Foo <...>`
		typeDef, err := ps.goTypeToAPIType(t.Underlying(), t)
		if err != nil {
			return nil, fmt.Errorf("failed to convert named type: %w", err)
		}
		return typeDef, nil
	case *types.Pointer:
		return ps.goTypeToAPIType(t.Elem(), named)
	case *types.Basic:
		typeDef := &dagger.TypeDefInput{}
		switch t.Info() {
		case types.IsString:
			typeDef.Kind = dagger.Stringkind
		case types.IsInteger:
			typeDef.Kind = dagger.Integerkind
		case types.IsBoolean:
			typeDef.Kind = dagger.Booleankind
		}
		return typeDef, nil
	case *types.Slice:
		elemTypeDef, err := ps.goTypeToAPIType(t.Elem(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to convert slice element type: %w", err)
		}
		return &dagger.TypeDefInput{
			Kind: dagger.Listkind,
			AsList: &dagger.ListTypeDefInput{
				ElementTypeDef: elemTypeDef,
			},
		}, nil
	case *types.Struct:
		return ps.goStructToAPIType(t, named)
	default:
		return nil, fmt.Errorf("unsupported type %T", t)
	}
}

const errorTypeName = "error"

func (ps *parseState) goStructToAPIType(t *types.Struct, named *types.Named) (*dagger.TypeDefInput, error) {
	if named == nil {
		return nil, fmt.Errorf("struct types must be named")
	}
	name := named.Obj().Name()
	if name == "" {
		return nil, fmt.Errorf("struct types must be named")
	}
	// Return "stub" type if we've already handled this type; the full definition that includes all fields+functions
	// only needs to appear once. Every other occurrence should just have a ref to the object's name.
	if _, ok := ps.visitedStructs[name]; ok {
		return &dagger.TypeDefInput{
			Kind:     dagger.Objectkind,
			AsObject: &dagger.ObjectTypeDefInput{Name: name},
		}, nil
	}
	// we only cache the type def w/ the name so that an future encounters with it just result in that stub
	// being used rather than the whole def being included many times
	ps.visitedStructs[name] = struct{}{}

	typeDef := &dagger.TypeDefInput{
		Kind:     dagger.Objectkind,
		AsObject: &dagger.ObjectTypeDefInput{Name: name},
	}
	tokenFile := ps.fset.File(named.Obj().Pos())
	isDaggerGenerated := filepath.Base(tokenFile.Name()) == "dagger.gen.go" // TODO: don't hardcode

	// Fill out any Functions on the object, which are methods on the struct
	// TODO: support methods defined on non-pointer receivers
	methodSet := types.NewMethodSet(types.NewPointer(named))
	for i := 0; i < methodSet.Len(); i++ {
		methodObj := methodSet.At(i).Obj()
		methodTokenFile := ps.fset.File(methodObj.Pos())
		methodIsDaggerGenerated := filepath.Base(methodTokenFile.Name()) == "dagger.gen.go" // TODO: don't hardcode
		if methodIsDaggerGenerated {
			// We don't care about pre-existing methods on core types or objects from dependency modules.
			continue
		}

		method, ok := methodObj.(*types.Func)
		if !ok {
			return nil, fmt.Errorf("expected method to be a func, got %T", methodObj)
		}
		if !method.Exported() {
			continue
		}
		fnTypeDef := &dagger.FunctionDef{
			Name: method.Name(),
		}

		funcDecl, err := ps.declForFunc(method)
		if err != nil {
			return nil, fmt.Errorf("failed to find decl for method %s: %w", method.Name(), err)
		}
		if doc := funcDecl.Doc; doc != nil {
			fnTypeDef.Description = doc.Text()
		}

		methodSig, ok := method.Type().(*types.Signature)
		if !ok {
			return nil, fmt.Errorf("expected method to be a func, got %T", method.Type())
		}

		// stash away the method signature so we can remember details on how it's
		// invoked (e.g. no error return, no ctx arg, error-only return, etc)
		ps.methods[methodKey{name, method.Name()}] = methodSig

		for i := 0; i < methodSig.Params().Len(); i++ {
			param := methodSig.Params().At(i)

			// first arg must be Context
			if i == 0 {
				if param.Type().String() != "context.Context" {
					return nil, fmt.Errorf("method %s has first arg %s, expected context.Context", method.Name(), param.Type().String())
				}
				continue
			}

			argTypeDef, err := ps.goTypeToAPIType(param.Type(), nil)
			if err != nil {
				return nil, fmt.Errorf("failed to convert param type: %w", err)
			}
			fnTypeDef.Args = append(fnTypeDef.Args, &dagger.FunctionArgDef{
				Name:    param.Name(),
				TypeDef: argTypeDef,
			})
		}

		methodResults := methodSig.Results()
		switch methodResults.Len() {
		case 0:
			fnTypeDef.ReturnType = &dagger.TypeDefInput{
				Kind:     dagger.Voidkind,
				Optional: true,
			}
		case 1:
			result := methodResults.At(0).Type()
			if result.String() == errorTypeName {
				fnTypeDef.ReturnType = &dagger.TypeDefInput{
					Kind:     dagger.Voidkind,
					Optional: true,
				}
			} else {
				resultTypeDef, err := ps.goTypeToAPIType(result, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to convert result type: %w", err)
				}
				fnTypeDef.ReturnType = resultTypeDef
			}
		case 2:
			result := methodResults.At(0).Type()
			resultTypeDef, err := ps.goTypeToAPIType(result, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to convert result type: %w", err)
			}
			fnTypeDef.ReturnType = resultTypeDef
		default:
			return nil, fmt.Errorf("method %s has too many return values", method.Name())
		}

		typeDef.AsObject.Functions = append(typeDef.AsObject.Functions, fnTypeDef)
	}

	if isDaggerGenerated {
		// If this object is from the core API or another dependency, we only care about any new methods
		// being attached to it, so we're all done in this case
		return typeDef, nil
	}

	// Fill out the Description with the comment above the struct (if any)
	typeSpec, err := ps.typeSpecForNamedType(named)
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for named type %s: %w", name, err)
	}
	if doc := typeSpec.Doc; doc != nil {
		typeDef.AsObject.Description = doc.Text()
	}
	astStructType, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		return nil, fmt.Errorf("expected type spec to be a struct, got %T", typeSpec.Type)
	}

	// Fill out the static fields of the struct (if any)
	for i := 0; i < t.NumFields(); i++ {
		field := t.Field(i)
		if !field.Exported() {
			continue
		}
		fieldTypeDef, err := ps.goTypeToAPIType(field.Type(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to convert field type: %w", err)
		}
		var description string
		if doc := astStructType.Fields.List[i].Doc; doc != nil {
			description = doc.Text()
		}
		typeDef.AsObject.Fields = append(typeDef.AsObject.Fields, &dagger.FieldTypeDefInput{
			Name:        field.Name(),
			Description: description,
			TypeDef:     fieldTypeDef,
		})
	}

	return typeDef, nil
}

// typeSpecForNamedType returns the *ast* type spec for the given Named type. This is needed
// because the types.Named object does not have the comments associated with the type, which
// we want to parse.
func (ps *parseState) typeSpecForNamedType(namedType *types.Named) (*ast.TypeSpec, error) {
	tokenFile := ps.fset.File(namedType.Obj().Pos())
	if tokenFile == nil {
		return nil, fmt.Errorf("no file for %s", namedType.Obj().Name())
	}
	for _, pkg := range ps.pkgs {
		for _, f := range pkg.Syntax {
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
						return typeSpec, nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("no decl for %s", namedType.Obj().Name())
}

// declForFunc returns the *ast* func decl for the given Func type. This is needed
// because the types.Func object does not have the comments associated with the type, which
// we want to parse.
func (ps *parseState) declForFunc(fnType *types.Func) (*ast.FuncDecl, error) {
	tokenFile := ps.fset.File(fnType.Pos())
	if tokenFile == nil {
		return nil, fmt.Errorf("no file for %s", fnType.Name())
	}
	for _, pkg := range ps.pkgs {
		for _, f := range pkg.Syntax {
			if ps.fset.File(f.Pos()) != tokenFile {
				continue
			}
			for _, decl := range f.Decls {
				fnDecl, ok := decl.(*ast.FuncDecl)
				if ok && fnDecl.Name.Name == fnType.Name() {
					return fnDecl, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no decl for %s", fnType.Name())
}

func defaultErrorMainSrc(msg string) string {
	return fmt.Sprintf("%#v", Func().Id("main").Parens(nil).Block(Id("panic").Call(Lit(msg))))
}
