package templates

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"dagger.io/dagger"
	. "github.com/dave/jennifer/jen"
	"github.com/iancoleman/strcase"
	"golang.org/x/tools/go/packages"
)

func (funcs goTemplateFuncs) moduleMainSrc() string {
	// TODO: figure out which error case gets hit when run on empty dir, ensure there's very helpful messages

	// TODO: add support for selectively recursing into external packages

	fset := token.NewFileSet()
	pkgs, err := packages.Load(&packages.Config{
		Dir:   funcs.sourceDirectoryPath,
		Tests: false,
		Fset:  fset,
		Mode: packages.NeedName |
			packages.NeedTypes |
			packages.NeedSyntax |
			packages.NeedTypesInfo,
	}, "./...")
	if err != nil {
		return defaultErrorMainSrc("unloadable package")
	}

	// TODO: handle case where no go.mod found (curently results in only package w/ empty name)

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
	if len(mainPkg.Errors) > 0 {
		return defaultErrorMainSrc("main package has errors")
	}

	moduleStructName := strcase.ToCamel(funcs.moduleName)
	moduleObj := mainPkg.Types.Scope().Lookup(moduleStructName)
	if moduleObj == nil {
		return defaultErrorMainSrc("no module struct yet")
	}

	ps := &parseState{
		namedTypeDefs: make(map[string]*dagger.TypeDefInput),
		pkgs:          pkgs,
		fset:          fset,
	}
	moduleAPITypeDef, err := ps.goTypeToAPIType(moduleObj.Type(), nil)
	if err != nil {
		return defaultErrorMainSrc(fmt.Sprintf("failed to convert module type: %v", err))
	}

	objFunctionCases := map[string][]Code{}
	if err := fillObjectFunctionCases(moduleAPITypeDef, objFunctionCases, moduleStructName); err != nil {
		panic(err)
	}
	return strings.Join([]string{mainSrc, invokeSrc(objFunctionCases)}, "\n")
}

const (
	mainSrc = `func main() {
	ctx := context.Background()

	fnCall := dag.CurrentFunctionCall()
	parentName, err := fnCall.ParentName(ctx)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(2)
	}
	fnName, err := fnCall.FunctionName(ctx)
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
		inputArgs[fnArg.Name] = []byte(fnArg.Value)
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
	_, err = fnCall.ReturnValue(ctx, resultBytes)
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

func invokeSrc(objFunctionCases map[string][]Code) string {
	objCases := []Code{}
	for objName, functionCases := range objFunctionCases {
		objCases = append(objCases, Case(Lit(objName)).Block(Switch(Id(fnNameVar)).Block(functionCases...)))
	}

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

func fillObjectFunctionCases(typeDef *dagger.TypeDefInput, cases map[string][]Code, moduleName string) error {
	if typeDef.Kind != dagger.Object {
		return nil
	}
	checkErrStatement := If(Err().Op("!=").Nil()).Block(
		// fmt.Println(err.Error())
		Qual("fmt", "Println").Call(Err().Dot("Error").Call()),
		// os.Exit(2)
		Qual("os", "Exit").Call(Lit(2)),
	)

	functionCases := []Code{}
	for _, fnDef := range typeDef.AsObject.Functions {
		statements := []Code{
			Var().Id("err").Error(),
		}

		parentVarName := "parent"
		statements = append(statements,
			Var().Id(parentVarName).Id(typeDef.AsObject.Name),
			Err().Op("=").Qual("json", "Unmarshal").Call(Id(parentJSONVar), Op("&").Id(parentVarName)),
			checkErrStatement,
		)

		fnCallArgs := []Code{Op("&").Id(parentVarName), Id("ctx")}
		for _, argDef := range fnDef.Args {
			statements = append(statements,
				Var().Id(argDef.Name).Id(apiTypeKindToGoType(argDef.TypeDef)),
				Err().Op("=").Qual("json", "Unmarshal").Call(
					Index().Byte().Parens(Id(inputArgsVar).Index(Lit(argDef.Name))),
					Op("&").Id(argDef.Name),
				),
				checkErrStatement,
			)
			switch argDef.TypeDef.Kind {
			case dagger.String, dagger.Integer, dagger.Boolean, dagger.List:
				fnCallArgs = append(fnCallArgs, Id(argDef.Name))
			case dagger.Object:
				fnCallArgs = append(fnCallArgs, Op("&").Id(argDef.Name))
			}
		}

		statements = append(statements, Return(Parens(Op("*").Id(typeDef.AsObject.Name)).Dot(fnDef.Name).Call(fnCallArgs...)))

		functionCases = append(functionCases, Case(Lit(fnDef.Name)).Block(statements...))

		if err := fillObjectFunctionCases(fnDef.ReturnType, cases, moduleName); err != nil {
			return err
		}
	}

	// special case for object name is module and function name is empty (return module functions)
	if typeDef.AsObject.Name == moduleName {
		typeDefBytes, err := json.Marshal(typeDef)
		if err != nil {
			return err
		}
		functionCases = append(functionCases, Case(Lit("")).Block(
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
					mod = mod.WithFunction(fnDef)
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
				Id("mod").Op("=").Id("mod").Dot("WithFunction").Call(Op("&").Id("fnDef")),
			),
			Return(Id("mod"), Nil()),
		))
	}

	cases[typeDef.AsObject.Name] = functionCases
	return nil
}

func apiTypeKindToGoType(typeDef *dagger.TypeDefInput) string {
	switch typeDef.Kind {
	case dagger.String:
		return "string"
	case dagger.Integer:
		return "int"
	case dagger.Boolean:
		return "bool"
	case dagger.List:
		return "[]" + apiTypeKindToGoType(typeDef.AsList.ElementTypeDef)
	case dagger.Object:
		return typeDef.AsObject.Name
	}
	return ""
}

type parseState struct {
	namedTypeDefs map[string]*dagger.TypeDefInput
	pkgs          []*packages.Package
	fset          *token.FileSet
}

func (ps *parseState) goTypeToAPIType(typ types.Type, named *types.Named) (*dagger.TypeDefInput, error) {
	switch t := typ.(type) {
	case *types.Named:
		name := t.Obj().Name()
		if name != "" {
			if namedTypeDef, ok := ps.namedTypeDefs[name]; ok {
				return namedTypeDef, nil
			}
		}

		typeDef, err := ps.goTypeToAPIType(t.Underlying(), t)
		if err != nil {
			return nil, fmt.Errorf("failed to convert named type %s: %w", t.Obj().Name(), err)
		}
		if name != "" {
			ps.namedTypeDefs[name] = typeDef
		}
		return typeDef, nil
	case *types.Pointer:
		return ps.goTypeToAPIType(t.Elem(), named)
	case *types.Basic:
		typeDef := &dagger.TypeDefInput{}
		switch t.Info() {
		case types.IsString:
			typeDef.Kind = dagger.String
		case types.IsInteger:
			typeDef.Kind = dagger.Integer
		case types.IsBoolean:
			typeDef.Kind = dagger.Boolean
		}
		return typeDef, nil
	case *types.Slice:
		elemTypeDef, err := ps.goTypeToAPIType(t.Elem(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to convert slice element type: %w", err)
		}
		return &dagger.TypeDefInput{
			Kind: dagger.List,
			AsList: &dagger.ListTypeDefInput{
				ElementTypeDef: elemTypeDef,
			},
		}, nil
	case *types.Struct:
		if named == nil {
			return nil, fmt.Errorf("struct types must be named")
		}
		typeDef := &dagger.TypeDefInput{
			Kind: dagger.Object,
			AsObject: &dagger.ObjectTypeDefInput{
				Name: named.Obj().Name(),
			},
		}

		tokenFile := ps.fset.File(named.Obj().Pos())
		isDaggerGenerated := tokenFile.Name() == "dagger.gen.go" // TODO: don't hardcode
		if isDaggerGenerated {
			// acknowledge its existence but don't need to include the fields+functions
			return typeDef, nil
		}

		typeSpec, err := ps.typeSpecForNamedType(named)
		if err != nil {
			return nil, fmt.Errorf("failed to find decl for named type %s: %w", named.Obj().Name(), err)
		}
		if doc := typeSpec.Doc; doc != nil {
			typeDef.AsObject.Description = doc.Text()
		}
		astStructType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return nil, fmt.Errorf("expected type spec to be a struct, got %T", typeSpec.Type)
		}

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

		methodSet := types.NewMethodSet(types.NewPointer(named))
		for i := 0; i < methodSet.Len(); i++ {
			method, ok := methodSet.At(i).Obj().(*types.Func)
			if !ok {
				return nil, fmt.Errorf("expected method to be a func, got %T", methodSet.At(i).Obj())
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
			for i := 0; i < methodSig.Params().Len(); i++ {
				param := methodSig.Params().At(i)
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
			if methodResults.Len() == 0 {
				return nil, fmt.Errorf("method %s has no return value", method.Name())
			}
			if methodResults.Len() > 2 {
				return nil, fmt.Errorf("method %s has too many return values", method.Name())
			}
			// ignore error, which should be second or not present (for now, we can be more flexible later)
			result := methodResults.At(0)
			resultTypeDef, err := ps.goTypeToAPIType(result.Type(), nil)
			if err != nil {
				return nil, fmt.Errorf("failed to convert result type: %w", err)
			}
			fnTypeDef.ReturnType = resultTypeDef
			typeDef.AsObject.Functions = append(typeDef.AsObject.Functions, fnTypeDef)
		}

		return typeDef, nil
	default:
		return nil, fmt.Errorf("unsupported type %T", t)
	}
}

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
				if ok {
					return fnDecl, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no decl for %s", fnType.Name())
}

func defaultErrorMainSrc(msg string) string {
	return fmt.Sprintf("%#v", Id("panic").Call(Lit(msg)))
}
