package templates

import (
	"fmt"
	"go/ast"
	"go/types"
	"maps"
	"reflect"
	"sort"
	"strings"

	"dagger.io/dagger"
	. "github.com/dave/jennifer/jen" //nolint:staticcheck
)

func (ps *parseState) parseGoStruct(t *types.Struct, named *types.Named) (*parsedObjectType, error) { //nolint:gocyclo // parsing struct metadata is inherently branchy
	spec := &parsedObjectType{
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

	// We don't support extending objects from outside this module, so we will
	// be skipping it. But first we want to verify the user isn't adding methods
	// to it (in which case we error out).
	objectIsDaggerGenerated := ps.isDaggerGenerated(named.Obj())

	goFuncTypes := []*types.Func{}
	methodSet := types.NewMethodSet(types.NewPointer(named))
	for i := range methodSet.Len() {
		methodObj := methodSet.At(i).Obj()

		if ps.isDaggerGenerated(methodObj) {
			// We don't care about pre-existing methods on core types or objects from dependency modules.
			continue
		}
		if objectIsDaggerGenerated {
			return nil, fmt.Errorf("cannot define methods on objects from outside this module")
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
	if objectIsDaggerGenerated {
		return nil, nil
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

	// get the comment above the struct (if any)
	astSpec, err := ps.astSpecForObj(named.Obj())
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for named type %s: %w", spec.name, err)
	}
	if doc := docForAstSpec(astSpec); doc != nil {
		docPragmas, docComment := parsePragmaComment(doc.Text())
		comment := strings.TrimSpace(docComment)
		if raw, ok := docPragmas["deprecated"]; ok {
			reason := ""
			if str, _ := raw.(string); str != "" {
				reason = str
			}
			spec.deprecated = &reason
		}
		spec.doc = comment
	}

	spec.sourceMap = ps.sourceMap(astSpec)

	astTypeSpec, ok := astSpec.(*ast.TypeSpec)
	if !ok {
		return nil, fmt.Errorf("expected type spec, got %T", astSpec)
	}
	astStructType, ok := astTypeSpec.Type.(*ast.StructType)
	if !ok {
		return nil, fmt.Errorf("expected type spec to be a struct, got %T", astTypeSpec.Type)
	}

	// Fill out the static fields of the struct (if any)
	astFields := unpackASTFields(astStructType.Fields)
	for i := range t.NumFields() {
		field := t.Field(i)
		if !field.Exported() {
			continue
		}

		fieldSpec := &fieldSpec{goType: field.Type()}
		fieldSpec.typeSpec, err = ps.parseGoTypeReference(fieldSpec.goType, nil, false)
		if err != nil {
			return nil, fmt.Errorf("failed to parse field type: %w", err)
		}

		fieldSpec.goName = field.Name()
		fieldSpec.name = fieldSpec.goName

		// override the name with the json tag if it was set - otherwise, we
		// end up asking for a name that we won't unmarshal correctly
		tag := reflect.StructTag(t.Tag(i))
		if dt := tag.Get("json"); dt != "" {
			namePart, _, _ := strings.Cut(dt, ",")
			if namePart == "-" {
				continue
			}
			if namePart != "" {
				fieldSpec.name = namePart
			}
		}

		docPragmas, docComment := parsePragmaComment(astFields[i].Doc.Text())
		linePragmas, lineComment := parsePragmaComment(astFields[i].Comment.Text())
		comment := strings.TrimSpace(docComment)
		if comment == "" {
			comment = strings.TrimSpace(lineComment)
		}
		pragmas := make(map[string]any)
		maps.Copy(pragmas, docPragmas)
		maps.Copy(pragmas, linePragmas)
		if v, ok := pragmas["private"]; ok {
			if v == nil {
				fieldSpec.isPrivate = true
			} else {
				fieldSpec.isPrivate, _ = v.(bool)
			}
		}
		if raw, ok := pragmas["deprecated"]; ok {
			reason := ""
			if str, _ := raw.(string); str != "" {
				reason = str
			}
			fieldSpec.deprecated = &reason
		}

		fieldSpec.doc = comment

		fieldSpec.sourceMap = ps.sourceMap(astFields[i])

		spec.fields = append(spec.fields, fieldSpec)
	}

	if ps.isMainModuleObject(spec.name) && ps.constructor != nil {
		spec.constructor, err = ps.parseGoFunc(nil, ps.constructor)
		if err != nil {
			return nil, fmt.Errorf("failed to parse constructor: %w", err)
		}
	}

	return spec, nil
}

type parsedObjectType struct {
	name       string
	moduleName string
	doc        string
	sourceMap  *sourceMap
	deprecated *string

	fields      []*fieldSpec
	methods     []*funcTypeSpec
	constructor *funcTypeSpec

	goType *types.Struct
}

var _ NamedParsedType = &parsedObjectType{}

func (spec *parsedObjectType) TypeDef(dag *dagger.Client) (*dagger.TypeDef, error) {
	withObjectOpts := dagger.TypeDefWithObjectOpts{}
	if spec.doc != "" {
		withObjectOpts.Description = strings.TrimSpace(spec.doc)
	}
	if spec.deprecated != nil {
		withObjectOpts.Deprecated = strings.TrimSpace(*spec.deprecated)
	}
	if spec.sourceMap != nil {
		withObjectOpts.SourceMap = spec.sourceMap.TypeDef(dag)
	}
	if spec.name == "" {
		return nil, fmt.Errorf("object name is empty")
	}
	typeDefObject := dag.TypeDef().WithObject(spec.name, withObjectOpts)

	for _, m := range spec.methods {
		fnTypeDef, err := m.TypeDefFunc(dag)
		if err != nil {
			return nil, fmt.Errorf("failed to convert method %s to function def: %w", m.name, err)
		}
		typeDefObject = typeDefObject.WithFunction(fnTypeDef)
	}

	for _, field := range spec.fields {
		if field.isPrivate {
			continue
		}

		fieldTypeDef, err := field.typeSpec.TypeDef(dag)
		if err != nil {
			return nil, fmt.Errorf("failed to convert field type: %w", err)
		}
		withFieldOpts := dagger.TypeDefWithFieldOpts{}
		if field.doc != "" {
			withFieldOpts.Description = field.doc
		}
		if field.sourceMap != nil {
			withFieldOpts.SourceMap = field.sourceMap.TypeDef(dag)
		}
		if field.deprecated != nil {
			withFieldOpts.Deprecated = strings.TrimSpace(*field.deprecated)
		}
		typeDefObject = typeDefObject.WithField(field.name, fieldTypeDef, withFieldOpts)
	}

	if spec.constructor != nil {
		fnTypeDef, err := spec.constructor.TypeDefFunc(dag)
		if err != nil {
			return nil, fmt.Errorf("failed to convert constructor to function def: %w", err)
		}
		typeDefObject = typeDefObject.WithConstructor(fnTypeDef)
	}

	return typeDefObject, nil
}

func (spec *parsedObjectType) GoType() types.Type {
	return spec.goType
}

func (spec *parsedObjectType) GoSubTypes() []types.Type {
	var subTypes []types.Type
	for _, method := range spec.methods {
		subTypes = append(subTypes, method.GoSubTypes()...)
	}
	for _, field := range spec.fields {
		if field.isPrivate {
			continue
		}
		subTypes = append(subTypes, field.typeSpec.GoSubTypes()...)
	}
	if spec.constructor != nil {
		subTypes = append(subTypes, spec.constructor.GoSubTypes()...)
	}
	return subTypes
}

func (spec *parsedObjectType) Name() string {
	return spec.name
}

func (spec *parsedObjectType) ModuleName() string {
	return spec.moduleName
}

// Extra generated code needed for the object implementation.
func (spec *parsedObjectType) ImplementationCode() (*Statement, error) {
	code := Empty()

	method, err := spec.marshalJSONMethodCode()
	if err != nil {
		return nil, err
	}
	code.Add(method.Line().Line())

	method, err = spec.unmarshalJSONMethodCode()
	if err != nil {
		return nil, err
	}
	code.Add(method.Line().Line())
	return code, nil
}

/*
UnmarshalJSON is needed because objects may have fields of an interface type,
which the JSON unmarshaller can't handle on its own. Instead, this custom
unmarshaller will unmarshal the JSON into a struct where all the fields are
concrete types, including the underlying concrete struct implementation of any
interface fields.

After it unmarshals into that, it copies the fields to the real object fields, handling any
special cases around interface conversions (e.g. converting a slice of structs to a slice of
interfaces).

e.g.:

	func (r *Test) UnmarshalJSON(bs []byte) error {
		var concrete struct {
			Iface          *customIfaceImpl
			IfaceList      []*customIfaceImpl
			OtherIfaceList []*otherIfaceImpl
		}
		err := json.Unmarshal(bs, &concrete)
		if err != nil {
			return err
		}
		r.Iface = concrete.Iface.toIface()
		r.IfaceList = convertSlice(concrete.IfaceList, (*customIfaceImpl).toIface)
		r.OtherIfaceList = convertSlice(concrete.OtherIfaceList, (*otherIfaceImpl).toIface)
		return nil
	}
*/
func (spec *parsedObjectType) unmarshalJSONMethodCode() (*Statement, error) {
	concreteFields := make([]Code, 0, len(spec.fields))
	setFieldCodes := make([]*Statement, 0, len(spec.fields))
	for _, field := range spec.fields {
		fieldTypeCode, err := spec.concreteFieldTypeCode(field.typeSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate field type code: %w", err)
		}
		fieldCode := Id(field.goName).Add(fieldTypeCode)
		if field.goName != field.name {
			fieldCode.Tag(map[string]string{"json": field.name})
		}
		concreteFields = append(concreteFields, fieldCode)

		setFieldCode, err := spec.setFieldsFromUnmarshalStructCode(field)
		if err != nil {
			return nil, fmt.Errorf("failed to generate set field code: %w", err)
		}
		setFieldCodes = append(setFieldCodes, setFieldCode)
	}

	return Func().Params(Id("r").Op("*").Id(spec.name)).
		Id("UnmarshalJSON").
		Params(Id("bs").Id("[]byte")).
		Params(Id("error")).
		BlockFunc(func(g *Group) {
			g.Var().Id("concrete").Struct(concreteFields...)
			g.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(Id("bs"), Op("&").Id("concrete"))
			g.If(Id("err").Op("!=").Nil()).Block(Return(Id("err")))
			for _, setFieldCode := range setFieldCodes {
				g.Add(setFieldCode)
			}
			g.Return(Nil())
		}), nil
}

/*
MarshalJSON is needed because if using embedded fields, a struct will inherit
the embedded MarshalJSON function (which could be an imported type, so would
return an ID instead of a JSON serialization) - so we should always provide our
own implementation.

The custom marshaller transforms all the fields from the target into a concrete
struct (exactly the same as in UnmarshalJSON), but taking interfaces and
turning them into "any" types (since we don't know the underlying type, so we
should defer to their serialization).

e.g.:

	func (r *Test) MarshalJSON() ([]byte, error) {
		var concrete struct {
			IfaceField          any
			IfaceFieldNeverSet  any
			IfacePrivateField   any
			IfaceListField      any
			OtherIfaceListField any
		}
		concrete.IfaceField = r.IfaceField
		concrete.IfaceFieldNeverSet = r.IfaceFieldNeverSet
		concrete.IfacePrivateField = r.IfacePrivateField
		concrete.IfaceListField = r.IfaceListField
		concrete.OtherIfaceListField = r.OtherIfaceListField
		return json.Marshal(&concrete)
	}
*/
func (spec *parsedObjectType) marshalJSONMethodCode() (*Statement, error) {
	concreteFields := make([]Code, 0, len(spec.fields))
	getFieldCodes := make([]*Statement, 0, len(spec.fields))
	for _, field := range spec.fields {
		fieldTypeCode, err := spec.marshalFieldTypeCode(field.typeSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to generate field type code: %w", err)
		}
		fieldCode := Id(field.goName).Add(fieldTypeCode)
		if field.goName != field.name {
			fieldCode.Tag(map[string]string{"json": field.name})
		}
		concreteFields = append(concreteFields, fieldCode)

		getFieldCode := spec.setFieldsToMarshalStructCode(field)
		getFieldCodes = append(getFieldCodes, getFieldCode)
	}

	return Func().Params(Id("r").Id(spec.name)).
		Id("MarshalJSON").
		Params().
		Params(Id("[]byte"), Id("error")).
		BlockFunc(func(g *Group) {
			g.Var().Id("concrete").Struct(concreteFields...)
			for _, getFieldCode := range getFieldCodes {
				g.Add(getFieldCode)
			}
			g.Return(Id("json").Dot("Marshal").Call(Op("&").Id("concrete")))
		}), nil
}

/*
The code for the type of a field in the concrete struct we use for marshalling into.
*/
func (spec *parsedObjectType) marshalFieldTypeCode(typeSpec ParsedType) (*Statement, error) {
	switch typeSpec := typeSpec.(type) {
	case *parsedIfaceTypeReference:
		return Id("any"), nil
	case *parsedSliceType:
		if _, ok := typeSpec.underlying.(*parsedIfaceTypeReference); ok {
			return Id("any"), nil
		}
	}
	return spec.concreteFieldTypeCode(typeSpec)
}

/*
The code for the type of a field in the concrete struct unmarshalled into. Mainly needs to handle
interface types, which need to be converted to their concrete struct implementations.
*/
func (spec *parsedObjectType) concreteFieldTypeCode(typeSpec ParsedType) (*Statement, error) {
	s := Empty()
	switch typeSpec := typeSpec.(type) {
	case *parsedPrimitiveType:
		if typeSpec.isPtr {
			s.Op("*")
		}
		if typeSpec.alias != "" {
			if typeSpec.moduleName == "" {
				s.Id("dagger." + typeSpec.alias)
			} else {
				s.Id(typeSpec.alias)
			}
		} else {
			tp := typeSpec.GoType()
			if basic, ok := tp.(*types.Basic); ok {
				if basic.Kind() == types.Invalid {
					s.Id("any")
					break
				}
			}

			s.Id(typeSpec.GoType().String())
		}

	case *parsedEnumTypeReference:
		if typeSpec.isPtr {
			s.Op("*")
		}
		if typeSpec.moduleName == "" {
			s.Id("dagger." + typeSpec.name)
		} else {
			s.Id(typeSpec.name)
		}

	case *parsedSliceType:
		fieldTypeCode, err := spec.concreteFieldTypeCode(typeSpec.underlying)
		if err != nil {
			return nil, fmt.Errorf("failed to generate slice field type code: %w", err)
		}
		s.Index().Add(fieldTypeCode)

	case *parsedObjectTypeReference:
		if typeSpec.isPtr {
			s.Op("*")
		}
		s.Id(typeName(typeSpec))

	case *parsedIfaceTypeReference:
		s.Op("*").Id(formatIfaceImplName(typeName(typeSpec)))

	default:
		return nil, fmt.Errorf("unsupported concrete field type %T", typeSpec)
	}

	return s, nil
}

/*
The code for setting the fields of the real object from the concrete struct unmarshalled into. e.g.:

	r.Iface = concrete.Iface.toIface()
	r.IfaceList = convertSlice(concrete.IfaceList, (*customIfaceImpl).toIface)
*/
func (spec *parsedObjectType) setFieldsFromUnmarshalStructCode(field *fieldSpec) (*Statement, error) {
	s := Empty()
	switch typeSpec := field.typeSpec.(type) {
	case *parsedPrimitiveType, *parsedEnumTypeReference, *parsedObjectTypeReference:
		s.Id("r").Dot(field.goName).Op("=").Id("concrete").Dot(field.goName)

	case *parsedSliceType:
		switch underlyingTypeSpec := typeSpec.underlying.(type) {
		case *parsedIfaceTypeReference:
			s.Id("r").Dot(field.goName).Op("=").Id("convertSlice").Call(
				Id("concrete").Dot(field.goName),
				Parens(Op("*").Id(formatIfaceImplName(underlyingTypeSpec.name))).Dot("toIface"),
			)
		default:
			s.Id("r").Dot(field.goName).Op("=").Id("concrete").Dot(field.goName)
		}

	case *parsedIfaceTypeReference:
		s.Id("r").Dot(field.goName).Op("=").Id("concrete").Dot(field.goName).Dot("toIface").Call()

	default:
		return nil, fmt.Errorf("unsupported field type %T", typeSpec)
	}

	return s, nil
}

func (spec *parsedObjectType) setFieldsToMarshalStructCode(field *fieldSpec) *Statement {
	return Empty().Id("concrete").Dot(field.goName).Op("=").Id("r").Dot(field.goName)
}

type fieldSpec struct {
	name       string
	doc        string
	deprecated *string
	sourceMap  *sourceMap
	typeSpec   ParsedType

	// isPrivate is true if the field is marked with the +private pragma
	isPrivate bool
	// goName is the name of the field in the Go struct. It may be different than name if the user changed the name of the field via a json tag
	goName string

	goType types.Type
}
