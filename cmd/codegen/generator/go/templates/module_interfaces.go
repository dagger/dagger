package templates

import (
	"fmt"
	"go/types"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	. "github.com/dave/jennifer/jen" //nolint:stylecheck
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
	for i := range daggerObjectMethodSet.Len() {
		daggerObjectIfaceMethods[daggerObjectMethodSet.At(i).Obj()] = false
	}

	goFuncTypes := []*types.Func{}
	methodSet := types.NewMethodSet(named)
	for i := range methodSet.Len() {
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
	astSpec, err := ps.astSpecForObj(named.Obj())
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for named type %s: %w", spec.name, err)
	}
	if doc := docForAstSpec(astSpec); doc != nil {
		spec.doc = doc.Text()
	}
	spec.sourceMap = ps.sourceMap(astSpec)

	return spec, nil
}

type parsedIfaceType struct {
	name      string
	doc       string
	sourceMap *sourceMap

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
	if spec.sourceMap != nil {
		withIfaceOptsCode = append(withIfaceOptsCode, Id("SourceMap").Op(":").Add(spec.sourceMap.TypeDefCode()))
	}
	if len(withIfaceOptsCode) > 0 {
		withIfaceArgsCode = append(withIfaceArgsCode, Id("dagger").Dot("TypeDefWithInterfaceOpts").Values(withIfaceOptsCode...))
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

func (spec *parsedIfaceType) TypeDefObject() (*core.TypeDef, error) {
	typeDefObject := (&core.TypeDef{}).WithInterface(spec.name, strings.TrimSpace(spec.doc), coreSourceMap(spec.sourceMap))

	for _, m := range spec.methods {
		fnTypeDefObj, err := m.TypeDefObject()
		if err != nil {
			return nil, fmt.Errorf("failed to convert method %s to function def: %w", m.name, err)
		}
		typeDefObject, err = typeDefObject.WithFunction(fnTypeDefObj.AsObject.Value.Functions[0])
		if err != nil {
			return nil, fmt.Errorf("failed to add method %s to type def: %w", m.name, err)
		}
	}

	return typeDefObject, nil
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

func (spec *parsedIfaceType) ModuleName() string {
	return spec.moduleName
}

// The code implementing the concrete struct that implements the interface and associated methods.
func (spec *parsedIfaceType) ImplementationCode() (*Statement, error) {
	// the base boilerplate methods needed for all structs implementing an api type
	code := Empty().
		Add(spec.concreteStructDefCode()).Line().Line().
		Add(spec.idDefCode()).Line().Line().
		Add(spec.loadFromIDMethodCode()).Line().Line().
		Add(spec.withGraphQLQuery()).Line().Line().
		Add(spec.graphqlTypeMethodCode()).Line().Line().
		Add(spec.graphqlIDTypeMethodCode()).Line().Line().
		Add(spec.graphqlIDMethodCode()).Line().Line().
		Add(spec.marshalJSONMethodCode()).Line().Line().
		Add(spec.unmarshalJSONMethodCode()).Line().Line().
		Add(spec.toIfaceMethodCode()).Line().Line()

	// the ID method, which is not explicitly declared by the user but needed internally
	idMethodCode, err := spec.concreteMethodCode(&funcTypeSpec{
		name:     "ID",
		argSpecs: []paramSpec{{name: "ctx", isContext: true}},
		returnSpec: &parsedPrimitiveType{
			goType:     types.Typ[types.String],
			alias:      spec.idTypeName(),
			moduleName: spec.moduleName,
		},
		returnsError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID method code: %w", err)
	}
	code.Add(idMethodCode).Line().Line()

	// the implementations of the methods declared on the interface
	for _, method := range spec.methods {
		methodCode, err := spec.concreteMethodCode(method)
		if err != nil {
			return nil, fmt.Errorf("failed to generate method %s code: %w", method.name, err)
		}
		code.Add(methodCode).Line().Line()
	}

	return code, nil
}

func (spec *parsedIfaceType) concreteStructName() string {
	return formatIfaceImplName(spec.name)
}

func (spec *parsedIfaceType) idTypeName() string {
	return spec.name + "ID"
}

func (spec *parsedIfaceType) loadFromIDMethodName() string {
	return fmt.Sprintf("Load%sFromID", spec.name)
}

func (spec *parsedIfaceType) idDefCode() *Statement {
	return Type().Id(spec.idTypeName()).String()
}

func (spec *parsedIfaceType) concreteStructCachedFieldName(method *funcTypeSpec) string {
	return strcase.ToLowerCamel(method.name)
}

/*
The struct definition for the concrete implementation of the interface. e.g.:

	type customIfaceImpl struct {
		query  *querybuilder.Selection
		id     *CustomIfaceID
		str    *string
		int    *int
		bool   *bool
	}
*/
func (spec *parsedIfaceType) concreteStructDefCode() *Statement {
	return Type().Id(spec.concreteStructName()).StructFunc(func(g *Group) {
		g.Id("query").Op("*").Qual("querybuilder", "Selection")
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

/*
The Load*FromID method attached to the top-level Client struct for this interface. e.g.:

	func LoadCustomIfaceFromID(r *dagger.Client, id CustomIfaceID) CustomIface {
		q = querybuilder.Query().Client(r.GraphQLClient())
		q = q.Select("loadTestCustomIfaceFromID")
		q = q.Arg("id", id)
		return &customIfaceImpl{
			query:  q,
		}
	}
*/
func (spec *parsedIfaceType) loadFromIDMethodCode() *Statement {
	return Func().
		Id(spec.loadFromIDMethodName()).
		Params(Id("r").Op("*").Id("dagger").Dot("Client"), Id("id").Id(spec.idTypeName())).
		Params(Id(spec.name)).
		BlockFunc(func(g *Group) {
			g.Id("q").Op(":=").Id("querybuilder").Dot("Query").Call().Dot("Client").Call(Id("r").Dot("GraphQLClient").Call())
			g.Id("q").Op("=").Id("q").Dot("Select").Call(Lit(loadFromIDGQLFieldName(spec)))
			g.Id("q").Op("=").Id("q").Dot("Arg").Call(Lit("id"), Id("id"))
			g.Return(Op("&").Id(spec.concreteStructName()).Values(Dict{
				Id("query"): Id("q"),
			}))
		})
}

/*
The WithGraphQLQuery sets the underlying query for the impl.

	func (r *customIfaceImpl) WithGraphQLQuery(q *querybuilder.Selection) CustomIface {
		return &customIfaceImpl{query: q}
	}
*/
func (spec *parsedIfaceType) withGraphQLQuery() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("WithGraphQLQuery").
		Params(Id("q").Op("*").Id("querybuilder").Dot("Selection")).
		Params(Id(spec.name)).
		BlockFunc(func(g *Group) {
			g.Return(Op("&").Id(spec.concreteStructName()).Values(Dict{Id("query"): Id("q")}))
		})
}

/*
The XXX_GraphQLType method attached to the concrete implementation of the interface. e.g.:

	func (r *customIfaceImpl) XXX_GraphQLType() string {
		return "CustomIface"
	}
*/
func (spec *parsedIfaceType) graphqlTypeMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("XXX_GraphQLType").
		Params().
		Params(Id("string")).
		Block(Return(Lit(spec.name)))
}

/*
The XXX_GraphQLIDType method attached to the concrete implementation of the interface. e.g.:

	func (r *customIfaceImpl) XXX_GraphQLIDType() string {
		return "CustomIfaceID"
	}
*/
func (spec *parsedIfaceType) graphqlIDTypeMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("XXX_GraphQLIDType").
		Params().
		Params(Id("string")).
		Block(Return(Lit(spec.idTypeName())))
}

/*
The XXX_GraphQLID method attached to the concrete implementation of the interface. e.g.:

	func (r *customIfaceImpl) XXX_GraphQLID(ctx context.Context) (string, error) {
		id, err := r.ID(ctx)
		if err != nil {
			return "", err
		}
		return string(id), nil
	}
*/
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

/*
The MarshalJSON method attached to the concrete implementation of the interface. e.g.:

	func (r *customIfaceImpl) MarshalJSON() ([]byte, error) {
		if r == nil {
			return []byte("\"\""), nil
		}
		id, err := r.ID(context.Background())
		if err != nil {
			return nil, err
		}
		return json.Marshal(id)
	}
*/
func (spec *parsedIfaceType) marshalJSONMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("MarshalJSON").
		Params().
		Params(Id("[]byte"), Id("error")).
		BlockFunc(func(g *Group) {
			g.If(Id("r").Op("==").Nil()).Block(Return(Index().Byte().Parens(Lit(`""`)), Nil()))

			g.List(Id("id"), Id("err")).Op(":=").Id("r").Dot("ID").Call(Id("marshalCtx"))
			g.If(Id("err").Op("!=").Nil()).Block(Return(Nil(), Id("err")))
			g.Return(Id("json").Dot("Marshal").Call(Id("id")))
		})
}

/*
The UnmarshalJSON method attached to the concrete implementation of the interface. e.g.:

	func (r *customIfaceImpl) UnmarshalJSON(bs []byte) error {
		var id CustomIfaceID
		err := json.Unmarshal(bs, &id)
		if err != nil {
			return err
		}
		*r = *dag.LoadCustomIfaceFromID(id).(*customIfaceImpl)
		return nil
	}
*/
func (spec *parsedIfaceType) unmarshalJSONMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.concreteStructName())).
		Id("UnmarshalJSON").
		Params(Id("bs").Id("[]byte")).
		Params(Id("error")).
		BlockFunc(func(g *Group) {
			g.Var().Id("id").Id(spec.idTypeName())
			g.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(Id("bs"), Op("&").Id("id"))
			g.If(Id("err").Op("!=").Nil()).Block(Return(Id("err")))
			g.Op("*").Id("r").Op("=").Op("*").Id(spec.loadFromIDMethodName()).
				Call(Id("dag"), Id("id")).Assert(Id("*").Id(spec.concreteStructName()))
			g.Return(Nil())
		})
}

/*
The toIface helper method attached to the concrete implementation of the interface
that's used to convert the concrete implementation to the interface. e.g.:

	func (r *customIfaceImpl) toIface() CustomIface {
		if r == nil {
			return nil
		}
		return r
	}
*/
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

/*
The code for the given interface method's concrete implementation attached to concrete
implementation struct. e.g.:

	func (r *customIfaceImpl) WithSomeArg(ctx context.Context, someArg string) CustomIface {
		q := r.query.Select("withSomeArg")
		q = q.Arg("someArg", someArg)

		// concreteMethodExecuteQueryCode...
	}
*/
func (spec *parsedIfaceType) concreteMethodCode(method *funcTypeSpec) (*Statement, error) {
	methodArgs := []Code{}
	for _, argSpec := range method.argSpecs {
		if argSpec.isContext {
			// ctx context.Context case
			methodArgs = append(methodArgs, Id("ctx").Qual("context", "Context"))
			continue
		}

		argTypeCode, err := spec.concreteMethodSigTypeCode(argSpec.typeSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate arg type code: %w", err)
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

			g.Id("q").Op(":=").Id("r").Dot("query").Dot("Select").Call(Lit(gqlFieldName))
			for _, argSpec := range method.argSpecs {
				if argSpec.typeSpec == nil {
					// skip context
					continue
				}
				gqlArgName := strcase.ToLowerCamel(argSpec.name)
				setCode := Id("q").Op("=").Id("q").Dot("Arg").Call(Lit(gqlArgName), Id(argSpec.name))
				g.Add(setCode).Line()
			}

			g.Add(executeQueryCode)
		}), nil
}

/*
The code for binding args and executing the query for the given interface method's concrete implementation.
*/
func (spec *parsedIfaceType) concreteMethodExecuteQueryCode(method *funcTypeSpec) (*Statement, error) {
	s := Empty()
	switch returnType := method.returnSpec.(type) {
	case nil:
		/*
			Void return, just need to return error. e.g.:

				q := r.query.Select("void")
				var response Void
				q = q.Bind(&response)
				return q.Execute(ctx)
		*/

		implTypeCode, err := spec.concreteMethodImplTypeCode(method.returnSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type code: %w", err)
		}
		s.Var().Id("response").Add(implTypeCode).Line()
		s.Id("q").Op("=").Id("q").Dot("Bind").Call(Op("&").Id("response")).Line()
		s.Return(
			Id("q").Dot("Execute").Call(Id("ctx")),
		)

	case *parsedPrimitiveType:
		/*
			Just return the primitive type response + error. e.g.:

				q := r.query.Select("str")
				var response string
				q = q.Bind(&response)
				return response, q.Execute(ctx)
		*/

		implTypeCode, err := spec.concreteMethodImplTypeCode(method.returnSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type code: %w", err)
		}
		s.Var().Id("response").Add(implTypeCode).Line()
		s.Id("q").Op("=").Id("q").Dot("Bind").Call(Op("&").Id("response")).Line()
		s.Return(
			Id("response"),
			Id("q").Dot("Execute").Call(Id("ctx")),
		)

	case *parsedIfaceTypeReference, *parsedObjectTypeReference:
		/*
			Just object type with chained query (no error). e.g.:

				return (&customIfaceImpl{}).WithGraphQLQuery(q)
		*/

		implTypeCode, err := spec.concreteMethodImplTypeCode(method.returnSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type code: %w", err)
		}
		s.Return(Params(Op("&").Add(implTypeCode).Values()).Dot("WithGraphQLQuery").Call(Id("q")))

	case *parsedSliceType:
		switch underlyingReturnType := returnType.underlying.(type) {
		case NamedParsedType:
			/*
				Need to return a slice of an object/interface. This is done by querying for the IDs and then
				converting those ids into a slice of the object/interface. e.g.:

					q = q.Select("id")
					var idResults []struct {
						Id dagger.DirectoryID
					}
					q = q.Bind(&idResults)
					err := q.Execute(ctx)
					if err != nil {
						return nil, err
					}
					var results []*Directory
					for _, idResult := range idResults {
						id := idResult.Id

						results = append(results, &dagger.Directory{
							query:  q.query.Root().Select("loadDirectoryFromID").Arg("id", id),
						})
					}
					return results, nil
			*/

			// TODO: if iface is from this module then it needs namespacing...
			idScalarName := typeName(underlyingReturnType) + "ID"
			loadFromIDQueryName := loadFromIDGQLFieldName(underlyingReturnType)

			s.Id("q").Op("=").Id("q").Dot("Select").Call(Lit("id")).Line()
			s.Var().Id("idResults").Index().Struct(Id("Id").Id(idScalarName)).Line()
			s.Id("q").Op("=").Id("q").Dot("Bind").Call(Op("&").Id("idResults")).Line()

			s.Id("err").Op(":=").Id("q").Dot("Execute").Call(Id("ctx")).Line()
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
				g.Id("id").Op(":=").Id("idResult").Dot("Id")
				query := Id("r").Dot("query").Dot("Root").Call().Dot("Select").Call(Lit(loadFromIDQueryName)).Dot("Arg").Call(Lit("id"), Id("id"))
				g.Id("results").Op("=").Append(Id("results"), Params(Op("&").Add(underlyingImplTypeCode).Values()).Dot("WithGraphQLQuery").Call(query))
			}).Line()

			s.Return(Id("results"), Nil())

		case *parsedPrimitiveType, nil:
			/*
				Need to return the slice of the primitive, e.g.:

					var response []string
					q = q.Bind(&response)
					return response, q.Execute(ctx)
			*/

			implTypeCode, err := spec.concreteMethodImplTypeCode(method.returnSpec)
			if err != nil {
				return nil, fmt.Errorf("failed to generate return type code: %w", err)
			}
			s.Var().Id("response").Add(implTypeCode).Line()
			s.Id("q").Op("=").Id("q").Dot("Bind").Call(Op("&").Id("response")).Line()
			s.Return(
				Id("response"),
				Id("q").Dot("Execute").Call(Id("ctx")),
			)

		default:
			return nil, fmt.Errorf("unsupported method return slice element type %T", underlyingReturnType)
		}

	default:
		return nil, fmt.Errorf("unsupported method return type %T", method.returnSpec)
	}

	return s, nil
}

/*
Code for checking whether we have already cached the result of a primitive type in the concrete struct
e.g.:

	if r.str != nil {
		return *r.str, nil
	}
*/
func (spec *parsedIfaceType) concreteMethodCheckCachedFieldCode(method *funcTypeSpec) *Statement {
	structFieldName := spec.concreteStructCachedFieldName(method)

	s := Null()
	if _, ok := method.returnSpec.(*parsedPrimitiveType); ok {
		s.If(Id("r").Dot(structFieldName).Op("!=").Nil()).Block(
			Return(Op("*").Id("r").Dot(structFieldName), Nil()),
		)
	}
	return s
}

/*
The code to use for the given type when used in a method signature as an arg or a return type. It's
important that this always be the expected pointer type and, if it's an interface, the actual go
interface type rather than the underlying concrete struct implementing it.
*/
func (spec *parsedIfaceType) concreteMethodSigTypeCode(argTypeSpec ParsedType) (*Statement, error) {
	s := Empty()
	switch argTypeSpec := argTypeSpec.(type) {
	case nil:
		// theoretically there should never be a void arg, but it's trivial enough to handle gracefully here...
		s.Id("Void")

	case *parsedPrimitiveType:
		// just make sure to use the alias of the primitive type if set, e.g. if it's a type declared like
		// `type MyString string` then we want to use `MyString` rather than `string`
		if argTypeSpec.alias != "" {
			s.Id(argTypeSpec.alias)
		} else {
			s.Id(argTypeSpec.GoType().String())
		}

	case *parsedSliceType:
		// just return []T for the underlying element type
		underlyingCode, err := spec.concreteMethodSigTypeCode(argTypeSpec.underlying)
		if err != nil {
			return nil, fmt.Errorf("failed to generate underlying type code: %w", err)
		}
		s.Index().Add(underlyingCode)

	case *parsedObjectTypeReference:
		if argTypeSpec.isPtr {
			s.Op("*")
		}
		s.Id(typeName(argTypeSpec))

	case *parsedIfaceTypeReference:
		s.Id(typeName(argTypeSpec))

	default:
		return nil, fmt.Errorf("unsupported method signature type %T", argTypeSpec)
	}

	return s, nil
}

/*
The code to use for the given type when used in the actual implementation of a method. This differs from
concreteMethodSigTypeCode when the type is an interface, in which case we want to use the internal concrete
struct rather than the interface type.
*/
func (spec *parsedIfaceType) concreteMethodImplTypeCode(returnTypeSpec ParsedType) (*Statement, error) {
	s := Empty()
	switch returnTypeSpec := returnTypeSpec.(type) {
	case nil:
		s.Id("dagger").Dot("Void")

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
		s.Id(typeName(returnTypeSpec))

	case *parsedIfaceTypeReference:
		s.Id(formatIfaceImplName(typeName(returnTypeSpec)))

	default:
		return nil, fmt.Errorf("unsupported method concrete return type %T", returnTypeSpec)
	}

	return s, nil
}

// The name of the concrete struct implementing the interface with the given name.
// If the interface is "Foo", this is "fooImpl".
func formatIfaceImplName(s string) string {
	return strcase.ToLowerCamel(s) + "Impl"
}
