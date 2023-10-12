package templates

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/dave/jennifer/jen" // nolint:revive,stylecheck
	"github.com/fatih/structtag"
	"github.com/iancoleman/strcase"
	"golang.org/x/tools/go/packages"
)

/* TODO:
* Handle types from 3rd party imports in the type signature
   * Add packages.NeedImports and packages.NeedDependencies to packages.Load opts, ensure performance is okay (or deal with that by lazy loading)
* Fix problem where changing a function signature requires running `dagger mod sync` twice (first one will result in package errors being seen, second one fixes)
   * Use Overlays field in packages.Config to provide partial generation of dagger.gen.go, without the unupdated code we generate here
* Handle automatically re-running `dagger mod sync` when invoking functions from CLI, to save users from having to always remember while developing locally
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
	if funcs.modulePkg == nil {
		// during bootstrapping, we might not have code yet, since it takes
		// multiple passes.
		return `func main() { panic("no code yet") }`
	}

	ps := &parseState{
		pkg:     funcs.modulePkg,
		fset:    funcs.moduleFset,
		methods: make(map[string][]method),
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

	for _, obj := range objs {
		named, isNamed := obj.Type().(*types.Named)
		if !isNamed {
			continue
		}

		strct, isStruct := named.Underlying().(*types.Struct)
		if !isStruct {
			// TODO(vito): could possibly support non-struct types, but why bother
			continue
		}

		// TODO(vito): hacky: need to run this before fillObjectFunctionCases so it
		// collects all the methods
		objType, err := ps.goStructToAPIType(strct, named)
		if err != nil {
			panic(err)
		}

		if err := ps.fillObjectFunctionCases(named, objFunctionCases); err != nil {
			// errors indicate an internal problem rather than something w/ user code, so panic instead
			panic(err)
		}

		if len(objFunctionCases[obj.Name()]) == 0 {
			// no functions on this object, so don't add it to the module
			continue
		}

		// Add the object to the module
		createMod = dotLine(createMod, "WithObject").Call(Add(Line(), objType))
	}

	// TODO: sort cases and functions based on their definition order
	return strings.Join([]string{mainSrc, invokeSrc(objFunctionCases, createMod)}, "\n")
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
		// ) (any,
		Id("any"),
		// error)
		Error(),
	).Block(objSwitch)

	return fmt.Sprintf("%#v", invokeFunc)
}

func renderNameOrStruct(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		return "*" + renderNameOrStruct(ptr.Elem())
	}
	if named, ok := t.(*types.Named); ok {
		// assume local
		return named.Obj().Name()
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

	methods := ps.methods[objName]
	if len(methods) == 0 {
		return nil
	}

	for _, method := range methods {
		fnName, sig := method.name, method.sig

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

		for i := 0; i < sig.Params().Len(); i++ {
			arg := sig.Params().At(i)

			if i == 0 && arg.Type().String() == "context.Context" {
				fnCallArgs = append(fnCallArgs, Id("ctx"))
				continue
			}

			if opts, ok := namedOrDirectStruct(arg.Type()); ok {
				optsName := arg.Name()

				statements = append(statements,
					Var().Id(optsName).Id(renderNameOrStruct(arg.Type())))

				for f := 0; f < opts.NumFields(); f++ {
					param := opts.Field(f)

					argName := strcase.ToLowerCamel(param.Name())
					statements = append(statements,
						If(Id(inputArgsVar).Index(Lit(argName)).Op("!=").Nil()).Block(
							Err().Op("=").Qual("json", "Unmarshal").Call(
								Index().Byte().Parens(Id(inputArgsVar).Index(Lit(argName))),
								Op("&").Id(optsName).Dot(param.Name()),
							),
							checkErrStatement,
						))
				}

				fnCallArgs = append(fnCallArgs, Id(optsName))
			} else {
				argName := strcase.ToLowerCamel(arg.Name())

				statements = append(statements,
					Var().Id(argName).Id(renderNameOrStruct(arg.Type())),
					Err().Op("=").Qual("json", "Unmarshal").Call(
						Index().Byte().Parens(Id(inputArgsVar).Index(Lit(argName))),
						Op("&").Id(argName),
					),
					checkErrStatement,
				)

				fnCallArgs = append(fnCallArgs, Id(argName))
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
				Parens(Op("*").Id(objName)).Dot(fnName).Call(fnCallArgs...),
			))

			cases[objName] = append(cases[objName], Case(Lit(fnName)).Block(statements...))

			if err := ps.fillObjectFunctionCases(results.At(0).Type(), cases); err != nil {
				return err
			}
		case 1:
			if results.At(0).Type().String() == errorTypeName {
				// void error return

				statements = append(statements, Return(
					Nil(),
					Parens(Op("*").Id(objName)).Dot(fnName).Call(fnCallArgs...),
				))

				cases[objName] = append(cases[objName], Case(Lit(fnName)).Block(statements...))
			} else {
				// non-error return

				statements = append(statements, Return(
					Parens(Op("*").Id(objName)).Dot(fnName).Call(fnCallArgs...),
					Nil(),
				))

				cases[objName] = append(cases[objName], Case(Lit(fnName)).Block(statements...))

				if err := ps.fillObjectFunctionCases(results.At(0).Type(), cases); err != nil {
					return err
				}
			}

		case 0:
			// void return
			//
			// NB(vito): it's really weird to have a fully void return (not even
			// error), but we should still support it for completeness.

			statements = append(statements,
				Parens(Op("*").Id(objName)).Dot(fnName).Call(fnCallArgs...),
				Return(Nil(), Nil()))

			cases[objName] = append(cases[objName], Case(Lit(fnName)).Block(statements...))

		default:
			return fmt.Errorf("unexpected number of results from method %s: %d", fnName, results.Len())
		}
	}

	// default case (return error)
	cases[objName] = append(cases[objName], Default().Block(
		Return(Nil(), Qual("fmt", "Errorf").Call(Lit("unknown function %s"), Id(fnNameVar))),
	))

	return nil
}

type parseState struct {
	pkg     *packages.Package
	fset    *token.FileSet
	methods map[string][]method
}

type method struct {
	name string
	sig  *types.Signature
}

func (ps *parseState) goTypeToAPIType(typ types.Type, named *types.Named) (*Statement, error) {
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
		if t.Kind() == types.Invalid {
			return nil, fmt.Errorf("invalid type: %+v", t)
		}
		var kind Code
		switch t.Info() {
		case types.IsString:
			kind = Id("Stringkind")
		case types.IsInteger:
			kind = Id("Integerkind")
		case types.IsBoolean:
			kind = Id("Booleankind")
		default:
			return nil, fmt.Errorf("unsupported basic type: %+v", t)
		}
		return Qual("dag", "TypeDef").Call().Dot("WithKind").Call(
			kind,
		), nil
	case *types.Slice:
		elemTypeDef, err := ps.goTypeToAPIType(t.Elem(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to convert slice element type: %w", err)
		}
		return Qual("dag", "TypeDef").Call().Dot("WithListOf").Call(
			elemTypeDef,
		), nil
	case *types.Struct:
		if named == nil {
			return nil, fmt.Errorf("struct types must be named")
		}
		typeName := named.Obj().Name()
		if typeName == "" {
			return nil, fmt.Errorf("struct types must be named")
		}
		return Qual("dag", "TypeDef").Call().Dot("WithObject").Call(
			Lit(typeName),
		), nil
	default:
		return nil, fmt.Errorf("unsupported type %T", t)
	}
}

const errorTypeName = "error"

func (ps *parseState) goStructToAPIType(t *types.Struct, named *types.Named) (*Statement, error) {
	if named == nil {
		return nil, fmt.Errorf("struct types must be named")
	}

	typeName := named.Obj().Name()
	if typeName == "" {
		return nil, fmt.Errorf("struct types must be named")
	}

	// args for WithObject
	withObjectArgs := []Code{
		Lit(typeName),
	}
	withObjectOpts := []Code{}

	// Fill out the Description with the comment above the struct (if any)
	typeSpec, err := ps.typeSpecForNamedType(named)
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for named type %s: %w", typeName, err)
	}
	if doc := typeSpec.Doc; doc != nil { // TODO(vito): for some reason this is always nil
		withObjectOpts = append(withObjectOpts, Id("Description").Op(":").Lit(doc.Text()))
	}
	if len(withObjectOpts) > 0 {
		withObjectArgs = append(withObjectArgs, Id("TypeDefWithObjectOpts").Values(withObjectOpts...))
	}

	typeDef := Qual("dag", "TypeDef").Call().Dot("WithObject").Call(withObjectArgs...)

	tokenFile := ps.fset.File(named.Obj().Pos())
	isDaggerGenerated := filepath.Base(tokenFile.Name()) == "dagger.gen.go" // TODO: don't hardcode

	methods := []*types.Func{}

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

		methods = append(methods, method)
	}

	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Pos() < methods[j].Pos()
	})

	for _, method := range methods {
		fnTypeDef, err := ps.goMethodToAPIFunctionDef(typeName, method, named)
		if err != nil {
			return nil, fmt.Errorf("failed to convert method %s to function def: %w", method.Name(), err)
		}

		typeDef = dotLine(typeDef, "WithFunction").Call(Add(Line(), fnTypeDef))
	}

	if isDaggerGenerated {
		// If this object is from the core API or another dependency, we only care
		// about any new methods being attached to it, so we're all done in this
		// case
		return typeDef, nil
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

		withFieldArgs := []Code{
			Lit(field.Name()),
			fieldTypeDef,
		}

		if description != "" {
			withFieldArgs = append(withFieldArgs,
				Id("TypeDefWithFieldOpts").Values(
					Id("Description").Op(":").Lit(description),
				))
		}

		typeDef = dotLine(typeDef, "WithField").Call(withFieldArgs...)
	}

	return typeDef, nil
}

var voidDef = Qual("dag", "TypeDef").Call().
	Dot("WithKind").Call(Id("Voidkind")).
	Dot("WithOptional").Call(Lit(true))

func (ps *parseState) goMethodToAPIFunctionDef(typeName string, fn *types.Func, named *types.Named) (*Statement, error) {
	methodSig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("expected method to be a func, got %T", fn.Type())
	}

	// stash away the method signature so we can remember details on how it's
	// invoked (e.g. no error return, no ctx arg, error-only return, etc)
	ps.methods[typeName] = append(ps.methods[typeName], method{
		name: fn.Name(),
		sig:  methodSig,
	})

	var err error

	var fnReturnType *Statement

	methodResults := methodSig.Results()
	switch methodResults.Len() {
	case 0:
		fnReturnType = voidDef
	case 1:
		result := methodResults.At(0).Type()
		if result.String() == errorTypeName {
			fnReturnType = voidDef
		} else {
			fnReturnType, err = ps.goTypeToAPIType(result, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to convert result type: %w", err)
			}
		}
	case 2:
		result := methodResults.At(0).Type()
		fnReturnType, err = ps.goTypeToAPIType(result, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to convert result type: %w", err)
		}
	default:
		return nil, fmt.Errorf("method %s has too many return values", fn.Name())
	}

	fnDef := Qual("dag", "Function").Call(Lit(fn.Name()), Add(Line(), fnReturnType))

	funcDecl, err := ps.declForFunc(fn)
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for method %s: %w", fn.Name(), err)
	}
	if doc := funcDecl.Doc; doc != nil {
		fnDef = dotLine(fnDef, "WithDescription").Call(Lit(doc.Text()))
	}

	for i := 0; i < methodSig.Params().Len(); i++ {
		param := methodSig.Params().At(i)

		if i == 0 && param.Type().String() == "context.Context" {
			// ignore ctx arg
			continue
		}

		if opts, ok := namedOrDirectStruct(param.Type()); ok {
			for f := 0; f < opts.NumFields(); f++ {
				param := opts.Field(f)

				tags, err := structtag.Parse(opts.Tag(f))
				if err != nil {
					return nil, fmt.Errorf("failed to parse struct tag: %w", err)
				}

				argTypeDef, err := ps.goTypeToAPIType(param.Type(), nil)
				if err != nil {
					return nil, fmt.Errorf("failed to convert param type: %w", err)
				}

				argOptional := true
				if tags != nil {
					if tag, err := tags.Get("required"); err == nil {
						argOptional = tag.Value() == "true"
					}
				}

				// all values in a struct are optional
				argTypeDef = argTypeDef.Dot("WithOptional").Call(Lit(argOptional))

				// arguments to WithArg
				argArgs := []Code{
					Lit(param.Name()),
					argTypeDef,
				}

				argOpts := []Code{}

				if tags != nil {
					// TODO: support this?
					// if tag, err := tags.Get("name"); err == nil {
					// 	def.Name = tag.Value()
					// }

					if tag, err := tags.Get("doc"); err == nil {
						argOpts = append(argOpts, Id("Description").Op(":").Lit(tag.Value()))
					}

					if tag, err := tags.Get("default"); err == nil {
						var jsonEnc string
						if param.Type().String() == "string" {
							enc, err := json.Marshal(tag.Value())
							if err != nil {
								return nil, fmt.Errorf("failed to marshal default value: %w", err)
							}
							jsonEnc = string(enc)
						} else {
							jsonEnc = tag.Value() // assume JSON encoded
						}
						argOpts = append(argOpts, Id("DefaultValue").Op(":").Id("JSON").Call(Lit(jsonEnc)))
					}
				}

				if len(argOpts) > 0 {
					argArgs = append(argArgs, Id("FunctionWithArgOpts").Values(argOpts...))
				}

				fnDef = dotLine(fnDef, "WithArg").Call(argArgs...)
			}
		} else {
			argTypeDef, err := ps.goTypeToAPIType(param.Type(), nil)
			if err != nil {
				return nil, fmt.Errorf("failed to convert param type: %w", err)
			}

			fnDef = dotLine(fnDef, "WithArg").Call(Lit(param.Name()), argTypeDef)
		}
	}

	return fnDef, nil
}

func namedOrDirectStruct(t types.Type) (*types.Struct, bool) {
	switch x := t.(type) {
	case *types.Named:
		return namedOrDirectStruct(x.Underlying())
	case *types.Struct:
		return x, true
	default:
		return nil, false
	}
}

// typeSpecForNamedType returns the *ast* type spec for the given Named type. This is needed
// because the types.Named object does not have the comments associated with the type, which
// we want to parse.
func (ps *parseState) typeSpecForNamedType(namedType *types.Named) (*ast.TypeSpec, error) {
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
func (ps *parseState) declForFunc(fnType *types.Func) (*ast.FuncDecl, error) {
	tokenFile := ps.fset.File(fnType.Pos())
	if tokenFile == nil {
		return nil, fmt.Errorf("no file for %s", fnType.Name())
	}
	for _, f := range ps.pkg.Syntax {
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
	return nil, fmt.Errorf("no decl for %s", fnType.Name())
}
