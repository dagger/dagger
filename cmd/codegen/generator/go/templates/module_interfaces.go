package templates

import (
	"fmt"
	"go/types"
	"sort"
	"strings"

	. "github.com/dave/jennifer/jen" // nolint:revive,stylecheck
	"github.com/iancoleman/strcase"
)

func (ps *parseState) parseGoIface(t *types.Interface, named *types.Named) (*parsedIfaceType, error) {
	spec := &parsedIfaceType{
		goType:     t,
		moduleName: ps.moduleName,
	}

	if named == nil {
		return nil, fmt.Errorf("struct types must be named")
	}
	spec.name = named.Obj().Name()
	if spec.name == "" {
		return nil, fmt.Errorf("struct types must be named")
	}

	// It's safe to compare objects directly: https://github.com/golang/example/tree/1d6d2400d4027025cb8edc86a139c9c581d672f7/gotypes#objects
	// (search "objects are routinely compared by the addresses of the underlying pointers")
	daggerObjectIfaceMethods := make(map[types.Object]bool)
	daggerObjectMethodSet := types.NewMethodSet(ps.daggerObjectIfaceType)
	for i := 0; i < daggerObjectMethodSet.Len(); i++ {
		daggerObjectIfaceMethods[daggerObjectMethodSet.At(i).Obj()] = false
	}

	goFuncTypes := []*types.Func{}
	methodSet := types.NewMethodSet(named)
	for i := 0; i < methodSet.Len(); i++ {
		methodObj := methodSet.At(i).Obj()

		// check if this is a method from the embedded DaggerObject interface
		if _, ok := daggerObjectIfaceMethods[methodObj]; ok {
			daggerObjectIfaceMethods[methodObj] = true
			continue
		}

		goFuncType, ok := methodObj.(*types.Func)
		if !ok {
			return nil, fmt.Errorf("expected method to be a func, got %T", methodObj)
		}

		if !goFuncType.Exported() {
			continue
		}

		goFuncTypes = append(goFuncTypes, goFuncType)
	}
	// verify the DaggerObject interface methods are all there
	for methodObj, found := range daggerObjectIfaceMethods {
		if !found {
			return nil, fmt.Errorf("missing method %s from DaggerObject interface, which must be embedded in interfaces used in Functions and Objects", methodObj.Name())
		}
	}

	sort.Slice(goFuncTypes, func(i, j int) bool {
		return goFuncTypes[i].Pos() < goFuncTypes[j].Pos()
	})
	for _, goFuncType := range goFuncTypes {
		funcTypeSpec, err := ps.parseGoFunc(named, goFuncType)
		if err != nil {
			return nil, fmt.Errorf("failed to parse method %s: %w", goFuncType.Name(), err)
		}
		spec.methods = append(spec.methods, funcTypeSpec)
	}

	// get the comment above the interface (if any)
	astSpec, err := ps.astSpecForNamedType(named)
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for named type %s: %w", spec.name, err)
	}
	spec.doc = astSpec.Doc.Text()

	return spec, nil
}

type parsedIfaceType struct {
	name string
	doc  string

	methods []*funcTypeSpec

	goType     *types.Interface
	moduleName string
}

var _ NamedParsedType = &parsedIfaceType{}

func (spec *parsedIfaceType) TypeDefCode() (*Statement, error) {
	withIfaceArgsCode := []Code{
		Lit(spec.name),
	}
	withIfaceOptsCode := []Code{}
	if spec.doc != "" {
		withIfaceOptsCode = append(withIfaceOptsCode, Id("Description").Op(":").Lit(strings.TrimSpace(spec.doc)))
	}
	if len(withIfaceOptsCode) > 0 {
		withIfaceArgsCode = append(withIfaceArgsCode, Id("TypeDefWithInterfaceOpts").Values(withIfaceOptsCode...))
	}

	typeDefCode := Qual("dag", "TypeDef").Call().Dot("WithInterface").Call(withIfaceArgsCode...)

	for _, method := range spec.methods {
		fnTypeDefCode, err := method.TypeDefCode()
		if err != nil {
			return nil, fmt.Errorf("failed to convert method %s to function def: %w", method.name, err)
		}
		typeDefCode = dotLine(typeDefCode, "WithFunction").Call(Add(Line(), fnTypeDefCode))
	}

	return typeDefCode, nil
}

func (spec *parsedIfaceType) GoType() types.Type {
	return spec.goType
}

func (spec *parsedIfaceType) GoSubTypes() []types.Type {
	var subTypes []types.Type
	for _, method := range spec.methods {
		subTypes = append(subTypes, method.GoSubTypes()...)
	}
	return subTypes
}

func (spec *parsedIfaceType) Name() string {
	return spec.name
}

func (spec *parsedIfaceType) ConcreteStructCode() ([]Code, error) {
	allCode := []Code{
		spec.concreteStructDefCode(),
		spec.idDefCode(),
		spec.loadFromIDMethodCode(),
		spec.graphqlTypeMethodCode(),
		spec.graphqlIDTypeMethodCode(),
		spec.graphqlIDMethodCode(),
		spec.marshalJSONMethodCode(),
		spec.unmarshalJSONMethodCode(),
		spec.toIfaceMethodCode(),
	}

	idMethodCode, err := spec.concreteMethodCode(&funcTypeSpec{
		name:         "ID",
		argSpecs:     []paramSpec{{name: "ctx", isContext: true}},
		returnSpec:   &parsedPrimitiveType{goType: types.Typ[types.String], alias: spec.idTypeName()},
		returnsError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID method code: %w", err)
	}
	allCode = append(allCode, idMethodCode)

	for _, method := range spec.methods {
		methodCode, err := spec.concreteMethodCode(method)
		if err != nil {
			return nil, fmt.Errorf("failed to generate method %s code: %w", method.name, err)
		}
		allCode = append(allCode, methodCode)
	}

	return allCode, nil
}

func (spec *parsedIfaceType) concreteStructName() string {
	return formatIfaceImplName(spec.name)
}

func (spec *parsedIfaceType) idTypeName() string {
	return spec.name + "ID"
}

func (spec *parsedIfaceType) loadFromIDGQLFieldName() string {
	// NOTE: unfortunately we currently need to account for namespacing here
	return fmt.Sprintf("load%s%sFromID", strcase.ToCamel(spec.moduleName), spec.name)
}

func (spec *parsedIfaceType) loadFromIDMethodName() string {
	return fmt.Sprintf("Load%sFromID", spec.name)
}

func (spec *parsedIfaceType) idDefCode() *Statement {
	return Type().Id(spec.idTypeName()).String()
}

func (spec *parsedIfaceType) concreteStructDefCode() *Statement {
	return Type().Id(spec.concreteStructName()).StructFunc(func(g *Group) {
		g.Id("q").Op("*").Qual("querybuilder", "Selection")
		g.Id("c").Qual("graphql", "Client")
		g.Id("id").Op("*").Id(spec.idTypeName())

		for _, method := range spec.methods {
			if method.returnSpec == nil {
				continue
			}
			primitiveType, ok := method.returnSpec.(*parsedPrimitiveType)
			if !ok {
				continue
			}
			g.Id(spec.concreteStructCachedFieldName(method)).Op("*").Id(primitiveType.GoType().String())
		}
	})
}

func (spec *parsedIfaceType) loadFromIDMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id("Client")).
		Id(spec.loadFromIDMethodName()).
		Params(Id("id").Id(spec.idTypeName())).
		Params(Id(spec.name)).
		BlockFunc(func(g *Group) {
			g.Id("q").Op(":=").Id("r").Dot("q").Dot("Select").Call(Lit(spec.loadFromIDGQLFieldName()))
			g.Id("q").Op("=").Id("q").Dot("Arg").Call(Lit("id"), Id("id"))
			g.Return(Op("&").Id(spec.concreteStructName()).Values(Dict{
				Id("q"): Id("q"),
				Id("c"): Id("r").Dot("c"),
			}))
		})
}

func (spec *parsedIfaceType) graphqlTypeMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("XXX_GraphQLType").
		Params().
		Params(Id("string")).
		Block(Return(Lit(spec.name)))
}

func (spec *parsedIfaceType) graphqlIDTypeMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("XXX_GraphQLIDType").
		Params().
		Params(Id("string")).
		Block(Return(Lit(spec.idTypeName())))
}

func (spec *parsedIfaceType) graphqlIDMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("XXX_GraphQLID").
		Params(Id("ctx").Qual("context", "Context")).
		Params(Id("string"), Id("error")).
		BlockFunc(func(g *Group) {
			g.List(Id("id"), Id("err")).Op(":=").Id("r").Dot("ID").Call(Id("ctx"))
			g.If(Id("err").Op("!=").Nil()).Block(Return(Lit(""), Id("err")))
			g.Return(Id("string").Parens(Id("id")), Nil())
		})
}

func (spec *parsedIfaceType) marshalJSONMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("MarshalJSON").
		Params().
		Params(Id("[]byte"), Id("error")).
		BlockFunc(func(g *Group) {
			g.If(Id("r").Op("==").Nil()).Block(Return(Index().Byte().Parens(Lit(`""`)), Nil()))

			g.List(Id("id"), Id("err")).Op(":=").Id("r").Dot("ID").Call(Qual("context", "Background").Call())
			g.If(Id("err").Op("!=").Nil()).Block(Return(Nil(), Id("err")))
			g.Return(Id("json").Dot("Marshal").Call(Id("id")))
		})
}

func (spec *parsedIfaceType) unmarshalJSONMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("UnmarshalJSON").
		Params(Id("bs").Id("[]byte")).
		Params(Id("error")).
		BlockFunc(func(g *Group) {
			g.Var().Id("id").Id(spec.idTypeName())
			g.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(Id("bs"), Op("&").Id("id"))
			g.If(Id("err").Op("!=").Nil()).Block(Return(Id("err")))
			g.Op("*").Id("r").Op("=").Op("*").Id("dag").Dot(spec.loadFromIDMethodName()).
				Call(Id("id")).Assert(Id("*").Id(spec.concreteStructName()))
			g.Return(Nil())
		})
}

func (spec *parsedIfaceType) toIfaceMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("toIface").
		Params().
		Params(Id(spec.name)).
		BlockFunc(func(g *Group) {
			g.If(Id("r").Op("==").Nil()).Block(Return(Nil()))
			g.Return(Id("r"))
		})
}

func (spec *parsedIfaceType) concreteStructCachedFieldName(method *funcTypeSpec) string {
	return strcase.ToLowerCamel(method.name)
}

func (spec *parsedIfaceType) concreteMethodCode(method *funcTypeSpec) (*Statement, error) {
	methodArgs := []Code{}
	for _, argSpec := range method.argSpecs {
		if argSpec.isContext {
			// ctx context.Context case
			methodArgs = append(methodArgs, Id(argSpec.name).Qual("context", "Context"))
			continue
		}

		argTypeCode, err := spec.concreteMethodSigTypeCode(argSpec.typeSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate arg type code: %w", err)
		}
		if argSpec.hasOptionalWrapper {
			argTypeCode = Id("Optional").Types(argTypeCode.Clone())
		}
		methodArgs = append(methodArgs, Id(argSpec.name).Add(argTypeCode))
	}

	methodReturns := []Code{}
	if method.returnSpec != nil {
		methodReturnCode, err := spec.concreteMethodSigTypeCode(method.returnSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type code: %w", err)
		}
		methodReturns = append(methodReturns, methodReturnCode)
	}
	if method.returnsError {
		methodReturns = append(methodReturns, Id("error"))
	}

	gqlFieldName := strcase.ToLowerCamel(method.name)
	executeQueryCode, err := spec.concreteMethodExecuteQueryCode(method)
	if err != nil {
		return nil, fmt.Errorf("failed to generate execute query code: %w", err)
	}
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id(method.name).
		Params(methodArgs...).
		Params(methodReturns...).
		BlockFunc(func(g *Group) {
			g.Add(spec.concreteMethodCheckCachedFieldCode(method))

			g.Id("q").Op(":=").Id("r").Dot("q").Dot("Select").Call(Lit(gqlFieldName))
			for _, argSpec := range method.argSpecs {
				if argSpec.typeSpec == nil {
					// skip context
					continue
				}
				gqlArgName := strcase.ToLowerCamel(argSpec.name)
				setCode := Id("q").Op("=").Id("q").Dot("Arg").Call(Lit(gqlArgName), Id(argSpec.name))
				if argSpec.hasOptionalWrapper {
					g.If(
						List(Id(argSpec.name), Id("ok")).Op(":=").Id(argSpec.name).Dot("Get").Call(),
						Id("ok"),
					).Block(setCode)
				} else {
					g.Add(setCode).Line()
				}
			}

			g.Add(executeQueryCode)
		}), nil
}

func (spec *parsedIfaceType) concreteMethodExecuteQueryCode(method *funcTypeSpec) (*Statement, error) {
	s := Empty()
	switch returnType := method.returnSpec.(type) {
	case nil:
		implTypeCode, err := spec.concreteMethodImplTypeCode(method.returnSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type code: %w", err)
		}
		s.Var().Id("response").Add(implTypeCode).Line()
		s.Id("q").Op("=").Id("q").Dot("Bind").Call(Op("&").Id("response")).Line()
		s.Return(
			Id("q").Dot("Execute").Call(Id("ctx"), Id("r").Dot("c")),
		)

	case *parsedPrimitiveType:
		implTypeCode, err := spec.concreteMethodImplTypeCode(method.returnSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type code: %w", err)
		}
		s.Var().Id("response").Add(implTypeCode).Line()
		s.Id("q").Op("=").Id("q").Dot("Bind").Call(Op("&").Id("response")).Line()
		s.Return(
			Id("response"),
			Id("q").Dot("Execute").Call(Id("ctx"), Id("r").Dot("c")),
		)

	case *parsedIfaceTypeReference, *parsedObjectTypeReference:
		implTypeCode, err := spec.concreteMethodImplTypeCode(method.returnSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type code: %w", err)
		}
		s.Return(Op("&").Add(implTypeCode).Values(Dict{
			Id("q"): Id("q"),
			Id("c"): Id("r").Dot("c"),
		}))

	case *parsedSliceType:
		switch underlyingReturnType := returnType.underlying.(type) {
		case NamedParsedType:
			// TODO: if iface is from this module then it needs namespacing...
			idScalarName := fmt.Sprintf("%sID", strcase.ToCamel(underlyingReturnType.Name()))
			loadFromIDQueryName := fmt.Sprintf("load%sFromID", strcase.ToCamel(underlyingReturnType.Name()))

			s.Id("q").Op("=").Id("q").Dot("Select").Call(Lit("id")).Line()
			s.Var().Id("idResults").Index().Struct(Id("Id").Id(idScalarName)).Line()
			s.Id("q").Op("=").Id("q").Dot("Bind").Call(Op("&").Id("idResults")).Line()

			s.Id("err").Op(":=").Id("q").Dot("Execute").Call(Id("ctx"), Id("r").Dot("c")).Line()
			s.If(Id("err").Op("!=").Nil()).Block(Return(Nil(), Id("err"))).Line()

			underlyingReturnTypeCode, err := spec.concreteMethodSigTypeCode(returnType.underlying)
			if err != nil {
				return nil, fmt.Errorf("failed to generate underlying return type code: %w", err)
			}
			underlyingImplTypeCode, err := spec.concreteMethodImplTypeCode(returnType.underlying)
			if err != nil {
				return nil, fmt.Errorf("failed to generate underlying impl type code: %w", err)
			}
			s.Var().Id("results").Index().Add(underlyingReturnTypeCode).Line()
			s.For(List(Id("_"), Id("idResult")).Op(":=").Range().Id("idResults")).BlockFunc(func(g *Group) {
				g.Id("id").Op(":=").Id("idResult").Dot("Id").Line()
				g.Id("results").Op("=").Append(Id("results"), Op("&").Add(underlyingImplTypeCode).Values(Dict{
					Id("id"): Op("&").Id("id"),
					Id("q"):  Id("querybuilder").Dot("Query").Call().Dot("Select").Call(Lit(loadFromIDQueryName)).Dot("Arg").Call(Lit("id"), Id("id")),
					Id("c"):  Id("r").Dot("c"),
				}))
			}).Line()

			s.Return(Id("results"), Nil())

		case *parsedPrimitiveType, nil:
			implTypeCode, err := spec.concreteMethodImplTypeCode(method.returnSpec)
			if err != nil {
				return nil, fmt.Errorf("failed to generate return type code: %w", err)
			}
			s.Var().Id("response").Add(implTypeCode).Line()
			s.Id("q").Op("=").Id("q").Dot("Bind").Call(Op("&").Id("response")).Line()
			s.Return(
				Id("response"),
				Id("q").Dot("Execute").Call(Id("ctx"), Id("r").Dot("c")),
			)

		default:
			return nil, fmt.Errorf("unsupported method return slice element type %T", underlyingReturnType)
		}

	default:
		return nil, fmt.Errorf("unsupported method return type %T", method.returnSpec)
	}

	return s, nil
}

func (spec *parsedIfaceType) concreteMethodCheckCachedFieldCode(method *funcTypeSpec) *Statement {
	structFieldName := spec.concreteStructCachedFieldName(method)

	s := Empty()
	if _, ok := method.returnSpec.(*parsedPrimitiveType); ok {
		s.If(Id("r").Dot(structFieldName).Op("!=").Nil()).Block(
			Return(Op("*").Id("r").Dot(structFieldName), Nil()),
		)
	}
	return s
}

func (spec *parsedIfaceType) concreteMethodSigTypeCode(argTypeSpec ParsedType) (*Statement, error) {
	s := Empty()
	switch argTypeSpec := argTypeSpec.(type) {
	case nil:
		s.Id("Void")

	case *parsedPrimitiveType:
		if argTypeSpec.alias != "" {
			s.Id(argTypeSpec.alias)
		} else {
			s.Id(argTypeSpec.GoType().String())
		}

	case *parsedSliceType:
		underlyingCode, err := spec.concreteMethodSigTypeCode(argTypeSpec.underlying)
		if err != nil {
			return nil, fmt.Errorf("failed to generate underlying type code: %w", err)
		}
		s.Index().Add(underlyingCode)

	case *parsedObjectTypeReference:
		if argTypeSpec.isPtr {
			s.Op("*")
		}
		s.Id(argTypeSpec.name)

	case *parsedIfaceTypeReference:
		s.Id(argTypeSpec.name)

	default:
		return nil, fmt.Errorf("unsupported method signature type %T", argTypeSpec)
	}

	return s, nil
}

func (spec *parsedIfaceType) concreteMethodImplTypeCode(returnTypeSpec ParsedType) (*Statement, error) {
	s := Empty()
	switch returnTypeSpec := returnTypeSpec.(type) {
	case nil:
		s.Id("Void")

	case *parsedPrimitiveType:
		if returnTypeSpec.alias != "" {
			s.Id(returnTypeSpec.alias)
		} else {
			s.Id(returnTypeSpec.GoType().String())
		}

	case *parsedSliceType:
		underlyingTypeCode, err := spec.concreteMethodImplTypeCode(returnTypeSpec.underlying)
		if err != nil {
			return nil, fmt.Errorf("failed to generate underlying type code: %w", err)
		}
		s.Index().Add(underlyingTypeCode)

	case *parsedObjectTypeReference:
		s.Id(returnTypeSpec.name)

	case *parsedIfaceTypeReference:
		s.Id(formatIfaceImplName(returnTypeSpec.name))

	default:
		return nil, fmt.Errorf("unsupported method concrete return type %T", returnTypeSpec)
	}

	return s, nil
}

func formatIfaceImplName(s string) string {
	return strcase.ToLowerCamel(s) + "Impl"
}
