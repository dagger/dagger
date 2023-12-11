package templates

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	. "github.com/dave/jennifer/jen" // nolint:revive,stylecheck
	"github.com/iancoleman/strcase"
	"golang.org/x/tools/go/packages"
)

const (
	daggerGenFilename   = "dagger.gen.go"
	contextTypename     = "context.Context"
	constructorFuncName = "New"
)

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
		fn, isFn := obj.(*types.Func)
		if isFn && fn.Name() == constructorFuncName {
			ps.constructor = fn
			continue
		}

		tps = append(tps, obj.Type())
	}

	added := map[string]struct{}{}
	topLevel := true

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
				if !topLevel {
					return "", fmt.Errorf("cannot code-generate unexported type %s", obj.Name())
				}
				continue
			}

			strct, isStruct := named.Underlying().(*types.Struct)
			if !isStruct {
				// TODO(vito): could possibly support non-struct types, but why bother
				continue
			}

			// avoid adding a struct definition twice (if it's referenced in two function signatures)
			if _, ok := added[obj.Name()]; ok {
				continue
			}

			// TODO(vito): hacky: need to run this before fillObjectFunctionCases so it
			// collects all the methods
			objType, extraTypes, err := ps.goStructToAPIType(strct, named)
			if err != nil {
				return "", err
			}
			if objType == nil {
				// not including in module schema, skip it
				continue
			}

			if err := ps.fillObjectFunctionCases(named, objFunctionCases); err != nil {
				// errors indicate an internal problem rather than something w/ user code, so error instead
				return "", fmt.Errorf("failed to generate function cases for %s: %w", obj.Name(), err)
			}

			if len(objFunctionCases[obj.Name()]) == 0 && !ps.isMainModuleObject(obj.Name()) {
				if topLevel {
					// no functions on this top-level object, so don't add it to the module
					continue
				}
				if ps.isDaggerGenerated(named.Obj()) {
					// skip objects from outside this module
					continue
				}
			}

			// Add the object to the module
			createMod = dotLine(createMod, "WithObject").Call(Add(Line(), objType))
			added[obj.Name()] = struct{}{}

			// If the object has any extra sub-types (e.g. for function return
			// values), add them to the list of types to process
			nextTps = append(nextTps, extraTypes...)
		}

		tps, nextTps = nextTps, nil
		topLevel = false
	}

	// TODO: sort cases and functions based on their definition order
	return strings.Join([]string{mainSrc, invokeSrc(objFunctionCases, createMod)}, "\n"), nil
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
		paramSpecs, err := ps.parseParamSpecs(ps.constructor)
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
			access := Id(varName)
			tp2, access2 := findOptsAccessPattern(varType, Id(varName))
			// only apply the access pattern if this is an inline opts struct wrapper or an Optional type wrapper
			_, applyAccessPattern := tp2.(*types.Struct) // inline struct case
			if !applyAccessPattern {
				// check for Optional
				_, applyAccessPattern = ps.isOptionalWrapper(tp2)
			}
			if applyAccessPattern {
				tp = tp2
				access = access2
			}

			statements = append(statements, Var().Id(varName).Id(renderNameOrStruct(tp)))
			if spec.variadic {
				fnCallArgs = append(fnCallArgs, access.Op("..."))
			} else {
				fnCallArgs = append(fnCallArgs, access)
			}
		}

		statements = append(statements,
			If(Id(inputArgsVar).Index(Lit(spec.graphqlName())).Op("!=").Nil()).Block(
				Err().Op("=").Qual("json", "Unmarshal").Call(
					Index().Byte().Parens(Id(inputArgsVar).Index(Lit(spec.graphqlName()))),
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
}

func (ps *parseState) isMainModuleObject(name string) bool {
	return strcase.ToCamel(ps.moduleName) == strcase.ToCamel(name)
}

type method struct {
	fn *types.Func

	paramSpecs []paramSpec
}

func (ps *parseState) goTypeToAPIType(typ types.Type, named *types.Named) (*Statement, *types.Named, error) {
	switch t := typ.(type) {
	case *types.Named:
		// Named types are any types declared like `type Foo <...>`
		typeDef, _, err := ps.goTypeToAPIType(t.Underlying(), t)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert named type: %w", err)
		}
		return typeDef, t, nil
	case *types.Pointer:
		return ps.goTypeToAPIType(t.Elem(), named)
	case *types.Slice:
		elemTypeDef, underlying, err := ps.goTypeToAPIType(t.Elem(), nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert slice element type: %w", err)
		}
		return Qual("dag", "TypeDef").Call().Dot("WithListOf").Call(
			elemTypeDef,
		), underlying, nil
	case *types.Basic:
		if t.Kind() == types.Invalid {
			return nil, nil, fmt.Errorf("invalid type: %+v", t)
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
			return nil, nil, fmt.Errorf("unsupported basic type: %+v", t)
		}
		return Qual("dag", "TypeDef").Call().Dot("WithKind").Call(
			kind,
		), named, nil
	case *types.Struct:
		if named == nil {
			return nil, nil, fmt.Errorf("struct types must be named")
		}
		typeName := named.Obj().Name()
		if typeName == "" {
			return nil, nil, fmt.Errorf("struct types must be named")
		}
		return Qual("dag", "TypeDef").Call().Dot("WithObject").Call(
			Lit(typeName),
		), named, nil
	default:
		return nil, nil, fmt.Errorf("unsupported type %T", t)
	}
}

const errorTypeName = "error"

func (ps *parseState) goStructToAPIType(t *types.Struct, named *types.Named) (*Statement, []types.Type, error) {
	if named == nil {
		return nil, nil, fmt.Errorf("struct types must be named")
	}
	typeName := named.Obj().Name()
	if typeName == "" {
		return nil, nil, fmt.Errorf("struct types must be named")
	}

	// We don't support extending objects from outside this module, so we will
	// be skipping it. But first we want to verify the user isn't adding methods
	// to it (in which case we error out).
	objectIsDaggerGenerated := ps.isDaggerGenerated(named.Obj())

	methods := []*types.Func{}
	methodSet := types.NewMethodSet(types.NewPointer(named))
	// Fill out any Functions on the object, which are methods on the struct
	// TODO: support methods defined on non-pointer receivers
	for i := 0; i < methodSet.Len(); i++ {
		methodObj := methodSet.At(i).Obj()

		if ps.isDaggerGenerated(methodObj) {
			// We don't care about pre-existing methods on core types or objects from dependency modules.
			continue
		}
		if objectIsDaggerGenerated {
			return nil, nil, fmt.Errorf("cannot define methods on objects from outside this module")
		}

		method, ok := methodObj.(*types.Func)
		if !ok {
			return nil, nil, fmt.Errorf("expected method to be a func, got %T", methodObj)
		}

		if !method.Exported() {
			continue
		}

		methods = append(methods, method)
	}
	if objectIsDaggerGenerated {
		return nil, nil, nil
	}

	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Pos() < methods[j].Pos()
	})

	// args for WithObject
	withObjectArgs := []Code{
		Lit(typeName),
	}
	withObjectOpts := []Code{}

	// Fill out the Description with the comment above the struct (if any)
	typeSpec, err := ps.typeSpecForNamedType(named)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find decl for named type %s: %w", typeName, err)
	}
	if comment := typeSpec.Doc.Text(); comment != "" {
		withObjectOpts = append(withObjectOpts, Id("Description").Op(":").Lit(strings.TrimSpace(comment)))
	}
	if len(withObjectOpts) > 0 {
		withObjectArgs = append(withObjectArgs, Id("TypeDefWithObjectOpts").Values(withObjectOpts...))
	}

	typeDef := Qual("dag", "TypeDef").Call().Dot("WithObject").Call(withObjectArgs...)

	var subTypes []types.Type

	for _, method := range methods {
		fnTypeDef, functionSubTypes, err := ps.goFuncToAPIFunctionDef(typeName, method)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert method %s to function def: %w", method.Name(), err)
		}
		subTypes = append(subTypes, functionSubTypes...)

		typeDef = dotLine(typeDef, "WithFunction").Call(Add(Line(), fnTypeDef))
	}

	astStructType, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		return nil, nil, fmt.Errorf("expected type spec to be a struct, got %T", typeSpec.Type)
	}

	// Fill out the static fields of the struct (if any)
	astFields := unpackASTFields(astStructType.Fields)
	for i := 0; i < t.NumFields(); i++ {
		field := t.Field(i)
		if !field.Exported() {
			continue
		}
		name := field.Name()

		docPragmas, docComment := parsePragmaComment(astFields[i].Doc.Text())
		linePragmas, lineComment := parsePragmaComment(astFields[i].Comment.Text())
		comment := strings.TrimSpace(docComment)
		if comment == "" {
			comment = strings.TrimSpace(lineComment)
		}
		pragmas := make(map[string]string)
		maps.Copy(pragmas, docPragmas)
		maps.Copy(pragmas, linePragmas)
		if v, ok := pragmas["private"]; ok {
			private := false
			if v == "" {
				private = true
			} else {
				private, _ = strconv.ParseBool(v)
			}
			if private {
				// don't generate WithField for private fields
				continue
			}
		}

		fieldTypeDef, subType, err := ps.goTypeToAPIType(field.Type(), nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert field type: %w", err)
		}
		if subType != nil {
			subTypes = append(subTypes, subType)
		}

		// override the name with the json tag if it was set - otherwise, we
		// end up asking for a name that we won't unmarshal correctly
		tag := reflect.StructTag(t.Tag(i))
		if dt := tag.Get("json"); dt != "" {
			dt, _, _ = strings.Cut(dt, ",")
			if dt == "-" {
				continue
			}
			name = dt
		}

		withFieldArgs := []Code{
			Lit(name),
			fieldTypeDef,
		}

		if comment != "" {
			withFieldArgs = append(withFieldArgs,
				Id("TypeDefWithFieldOpts").Values(
					Id("Description").Op(":").Lit(comment),
				))
		}

		typeDef = dotLine(typeDef, "WithField").Call(withFieldArgs...)
	}

	if ps.isMainModuleObject(typeName) && ps.constructor != nil {
		fnTypeDef, _, err := ps.goFuncToAPIFunctionDef("", ps.constructor)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert constructor to function def: %w", err)
		}
		typeDef = dotLine(typeDef, "WithConstructor").Call(Add(Line(), fnTypeDef))
	}

	return typeDef, subTypes, nil
}

var voidDef = Qual("dag", "TypeDef").Call().
	Dot("WithKind").Call(Id("Voidkind")).
	Dot("WithOptional").Call(Lit(true))

func (ps *parseState) goFuncToAPIFunctionDef(receiverTypeName string, fn *types.Func) (*Statement, []types.Type, error) {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil, nil, fmt.Errorf("expected method to be a func, got %T", fn.Type())
	}

	// stash away the method signature so we can remember details on how it's
	// invoked (e.g. no error return, no ctx arg, error-only return, etc)
	specs, err := ps.parseParamSpecs(fn)
	if err != nil {
		return nil, nil, err
	}
	if receiverTypeName != "" {
		ps.methods[receiverTypeName] = append(ps.methods[receiverTypeName], method{fn: fn, paramSpecs: specs})
	}

	var fnReturnType *Statement

	var subTypes []types.Type

	results := sig.Results()
	var returnSubType *types.Named
	switch results.Len() {
	case 0:
		fnReturnType = voidDef
	case 1:
		result := results.At(0).Type()
		if result.String() == errorTypeName {
			fnReturnType = voidDef
		} else {
			fnReturnType, returnSubType, err = ps.goTypeToAPIType(result, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert result type: %w", err)
			}
		}
	case 2:
		result := results.At(0).Type()
		subTypes = append(subTypes, result)
		fnReturnType, returnSubType, err = ps.goTypeToAPIType(result, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert result type: %w", err)
		}
	default:
		return nil, nil, fmt.Errorf("method %s has too many return values", fn.Name())
	}
	if returnSubType != nil {
		subTypes = append(subTypes, returnSubType)
	}

	fnDef := Qual("dag", "Function").Call(Lit(fn.Name()), Add(Line(), fnReturnType))

	funcDecl, err := ps.declForFunc(fn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find decl for method %s: %w", fn.Name(), err)
	}
	if comment := funcDecl.Doc.Text(); comment != "" {
		fnDef = dotLine(fnDef, "WithDescription").Call(Lit(strings.TrimSpace(comment)))
	}

	for i, spec := range specs {
		if i == 0 && spec.paramType.String() == contextTypename {
			// ignore ctx arg
			continue
		}

		typeDef, subType, err := ps.goTypeToAPIType(spec.baseType, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert param type: %w", err)
		}
		if subType != nil {
			subTypes = append(subTypes, subType)
		}

		if spec.optional {
			typeDef = typeDef.Dot("WithOptional").Call(Lit(true))
		}

		// arguments to WithArg
		args := []Code{Lit(spec.graphqlName()), typeDef}

		argOpts := []Code{}
		if spec.description != "" {
			argOpts = append(argOpts, Id("Description").Op(":").Lit(spec.description))
		}
		if spec.defaultValue != "" {
			var jsonEnc string
			if spec.baseType.String() == "string" {
				enc, err := json.Marshal(spec.defaultValue)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to marshal default value: %w", err)
				}
				jsonEnc = string(enc)
			} else {
				jsonEnc = spec.defaultValue
			}
			argOpts = append(argOpts, Id("DefaultValue").Op(":").Id("JSON").Call(Lit(jsonEnc)))
		}
		if len(argOpts) > 0 {
			args = append(args, Id("FunctionWithArgOpts").Values(argOpts...))
		}

		fnDef = dotLine(fnDef, "WithArg").Call(args...)
	}

	return fnDef, subTypes, nil
}

func (ps *parseState) parseParamSpecs(fn *types.Func) ([]paramSpec, error) {
	sig := fn.Type().(*types.Signature)
	params := sig.Params()
	if params.Len() == 0 {
		return nil, nil
	}

	specs := make([]paramSpec, 0, params.Len())

	i := 0
	if params.At(i).Type().String() == contextTypename {
		spec, err := ps.parseParamSpecVar(params.At(i), "", "")
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)

		i++
	}

	fnDecl, err := ps.declForFunc(fn)
	if err != nil {
		return nil, err
	}

	// is the first data param an inline struct? if so, process each field of
	// the struct as a top-level param
	if i+1 == params.Len() {
		param := params.At(i)
		paramType, ok := asInlineStruct(param.Type())
		if ok {
			stype, ok := asInlineStructAst(fnDecl.Type.Params.List[i].Type)
			if !ok {
				return nil, fmt.Errorf("expected struct type for %s", param.Name())
			}

			parent := &paramSpec{
				name:      params.At(i).Name(),
				paramType: param.Type(),
				baseType:  param.Type(),
			}

			paramFields := unpackASTFields(stype.Fields)
			for f := 0; f < paramType.NumFields(); f++ {
				spec, err := ps.parseParamSpecVar(paramType.Field(f), paramFields[f].Doc.Text(), paramFields[f].Comment.Text())
				if err != nil {
					return nil, err
				}
				spec.parent = parent
				specs = append(specs, spec)
			}
			return specs, nil
		}
	}

	// if other parameter passing schemes fail, just treat each remaining arg
	// as a top-level param
	paramFields := unpackASTFields(fnDecl.Type.Params)
	for ; i < params.Len(); i++ {
		docComment, lineComment := ps.commentForFuncField(fnDecl, paramFields, i)
		spec, err := ps.parseParamSpecVar(params.At(i), docComment.Text(), lineComment.Text())
		if err != nil {
			return nil, err
		}
		if sig.Variadic() && i == params.Len()-1 {
			spec.variadic = true
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func (ps *parseState) parseParamSpecVar(field *types.Var, docComment string, lineComment string) (paramSpec, error) {
	if _, ok := field.Type().(*types.Struct); ok {
		return paramSpec{}, fmt.Errorf("nested structs are not supported")
	}

	paramType := field.Type()
	baseType := paramType
	for {
		ptr, ok := baseType.(*types.Pointer)
		if !ok {
			break
		}
		baseType = ptr.Elem()
	}

	optional := false
	defaultValue := ""

	if named, ok := ps.isOptionalWrapper(baseType); ok {
		typeArgs := named.TypeArgs()
		if typeArgs.Len() != 1 {
			return paramSpec{}, fmt.Errorf("optional type must have exactly one type argument")
		}
		optional = true

		baseType = typeArgs.At(0)
		for {
			ptr, ok := baseType.(*types.Pointer)
			if !ok {
				break
			}
			baseType = ptr.Elem()
		}
	}

	docPragmas, docComment := parsePragmaComment(docComment)
	linePragmas, lineComment := parsePragmaComment(lineComment)
	comment := strings.TrimSpace(docComment)
	if comment == "" {
		comment = strings.TrimSpace(lineComment)
	}

	pragmas := make(map[string]string)
	maps.Copy(pragmas, docPragmas)
	maps.Copy(pragmas, linePragmas)
	if v, ok := pragmas["default"]; ok {
		defaultValue = v
	}
	if v, ok := pragmas["optional"]; ok {
		if v == "" {
			optional = true
		} else {
			optional, _ = strconv.ParseBool(v)
		}
	}

	return paramSpec{
		name:         field.Name(),
		paramType:    paramType,
		baseType:     baseType,
		optional:     optional,
		defaultValue: defaultValue,
		description:  comment,
	}, nil
}

type paramSpec struct {
	name        string
	description string

	optional bool
	variadic bool

	defaultValue string

	// paramType is the full type declared in the function signature, which may
	// include pointer types, Optional, etc
	paramType types.Type
	// baseType is the simplified base type derived from the function signature
	baseType types.Type

	// parent is set if this paramSpec is nested inside a parent inline struct,
	// and is used to create a declaration of the entire inline struct
	parent *paramSpec
}

func (spec *paramSpec) graphqlName() string {
	return strcase.ToLowerCamel(spec.name)
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

func (ps *parseState) isOptionalWrapper(typ types.Type) (*types.Named, bool) {
	named, ok := typ.(*types.Named)
	if !ok {
		return nil, false
	}
	if named.Obj().Name() == "Optional" && ps.isDaggerGenerated(named.Obj()) {
		return named, true
	}
	return nil, false
}

// findOptsAccessPattern takes a type and a base statement (the name of a
// variable that has the target type) and produces a type that can be used in a
// variable declaration, as well as a statement that has the same type as the
// target statement.
//
// This is essentially for helping resolve the pointeriness of types: a type of
// **T and a variable p becomes T and &&p. This means we can *always* construct
// an Opts object and unmarshal into it without having nil dereferences.
func findOptsAccessPattern(t types.Type, access *Statement) (types.Type, *Statement) {
	switch t := t.(type) {
	case *types.Pointer:
		// taking the address of an address isn't allowed - so we use a ptr
		// helper function
		t2, val := findOptsAccessPattern(t.Elem(), access)
		return t2, Id("ptr").Call(val)
	// case *types.Slice:
	// 	t2, val := findOptsAccessPattern(t.Elem(), access)
	// 	return t2, Index().Id(renderNameOrStruct(t.Elem())).Values(val)
	default:
		return t, access
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
