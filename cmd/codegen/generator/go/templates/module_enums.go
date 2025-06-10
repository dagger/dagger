package templates

import (
	"fmt"
	"go/constant"
	"go/types"
	"strings"

	. "github.com/dave/jennifer/jen" //nolint:stylecheck
)

type parsedEnumTypeReference struct {
	name       string
	moduleName string

	values []*parsedEnumMember

	goType types.Type
	isPtr  bool
}

func (spec *parsedEnumTypeReference) TypeDefCode() (*Statement, error) {
	return Qual("dag", "TypeDef").Call().Dot("WithEnum").Call(Lit(spec.name)), nil
}

func (spec *parsedEnumTypeReference) GoType() types.Type {
	return spec.goType
}

func (spec *parsedEnumTypeReference) GoSubTypes() []types.Type {
	return []types.Type{spec.goType}
}

func (ps *parseState) parseGoEnumReference(t *types.Basic, named *types.Named, isPtr bool) (*parsedEnumTypeReference, error) {
	if named == nil {
		return nil, nil
	}

	if ps.isDaggerGenerated(named.Obj()) {
		tp := ps.schema.Types.Get(named.Obj().Name())
		if tp == nil {
			return nil, fmt.Errorf("unknown type %q", named.Obj().Name())
		}
		if len(tp.EnumValues) == 0 {
			return nil, nil
		}

		ref := &parsedEnumTypeReference{
			name:   named.Obj().Name(),
			goType: named,
			isPtr:  isPtr,
		}
		for _, v := range tp.EnumValues {
			ref.values = append(ref.values, &parsedEnumMember{
				name:  v.Name,
				value: v.Directives.EnumValue(),
			})
		}
		return ref, nil
	}

	enum, err := ps.parseGoEnum(t, named)
	if err != nil {
		return nil, err
	}
	if enum == nil {
		return nil, nil
	}
	return &parsedEnumTypeReference{
		name:       enum.name,
		values:     enum.values,
		moduleName: enum.moduleName,
		goType:     named,
		isPtr:      isPtr,
	}, nil
}

func (ps *parseState) parseGoEnum(t *types.Basic, named *types.Named) (*parsedEnumType, error) {
	spec := &parsedEnumType{
		goType:     t,
		moduleName: ps.moduleName,
	}

	if named == nil {
		return nil, fmt.Errorf("enum types must be named")
	}
	spec.name = named.Obj().Name()
	if spec.name == "" {
		return nil, fmt.Errorf("enum types must be named")
	}

	if ps.isDaggerGenerated(named.Obj()) {
		return nil, nil
	}

	for _, obj := range ps.objs {
		objConst, ok := obj.(*types.Const)
		if !ok {
			continue
		}
		if objConst.Type() != named {
			continue
		}

		name := obj.Name()
		value := ""
		if objConst.Val().Kind() == constant.String {
			value = constant.StringVal(objConst.Val())
		} else {
			value = objConst.Val().ExactString()
		}

		valueSpec := &parsedEnumMember{
			originalName: name,
			name:         name,
			value:        value,
		}
		astSpec, err := ps.astSpecForObj(objConst)
		if err != nil {
			return nil, fmt.Errorf("failed to find decl for object %s: %w", spec.name, err)
		}
		if doc := docForAstSpec(astSpec); doc != nil {
			valueSpec.doc = doc.Text()
		}
		valueSpec.sourceMap = ps.sourceMap(astSpec)
		spec.values = append(spec.values, valueSpec)
	}
	if len(spec.values) == 0 {
		// no values, this isn't an enum, it's a scalar alias
		return nil, nil
	}

	// trim prefixes for all names (if we can)
	trim := true
	for _, v := range spec.values {
		if !strings.HasPrefix(v.name, spec.name) {
			trim = false
			break
		}
	}
	if trim {
		for _, v := range spec.values {
			v.name = v.name[len(spec.name):]
		}
	}

	// get the comment above the struct (if any)
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

type parsedEnumType struct {
	name       string
	moduleName string
	doc        string
	sourceMap  *sourceMap

	values []*parsedEnumMember

	goType *types.Basic
}

type parsedEnumMember struct {
	originalName string
	name         string
	value        string
	doc          string
	sourceMap    *sourceMap
}

var _ NamedParsedType = &parsedEnumType{}

func (spec *parsedEnumType) TypeDefCode() (*Statement, error) {
	withEnumArgsCode := []Code{
		Lit(spec.name),
	}
	withEnumOptsCode := []Code{}
	if spec.doc != "" {
		withEnumOptsCode = append(withEnumOptsCode, Id("Description").Op(":").Lit(strings.TrimSpace(spec.doc)))
	}
	if spec.sourceMap != nil {
		withEnumOptsCode = append(withEnumOptsCode, Id("SourceMap").Op(":").Add(spec.sourceMap.TypeDefCode()))
	}
	if len(withEnumOptsCode) > 0 {
		withEnumArgsCode = append(withEnumArgsCode, Id("dagger").Dot("TypeDefWithEnumOpts").Values(withEnumOptsCode...))
	}

	typeDefCode := Qual("dag", "TypeDef").Call().Dot("WithEnum").Call(withEnumArgsCode...)

	for _, val := range spec.values {
		valueTypeDefCode := []Code{
			Lit(val.value),
		}
		var withEnumValueOpts []Code
		if val.doc != "" {
			withEnumValueOpts = append(withEnumValueOpts, Id("Description").Op(":").Lit(strings.TrimSpace(val.doc)))
		}
		if val.sourceMap != nil {
			withEnumValueOpts = append(withEnumValueOpts, Id("SourceMap").Op(":").Add(val.sourceMap.TypeDefCode()))
		}
		if len(withEnumValueOpts) > 0 {
			valueTypeDefCode = append(valueTypeDefCode,
				Id("dagger").Dot("TypeDefWithEnumValueOpts").Values(withEnumValueOpts...),
			)
		}
		typeDefCode = dotLine(typeDefCode, "WithEnumValue").Call(valueTypeDefCode...)
	}

	return typeDefCode, nil
}

func (spec *parsedEnumType) GoType() types.Type {
	return spec.goType
}

func (spec *parsedEnumType) GoSubTypes() []types.Type {
	return nil
}

func (spec *parsedEnumType) Name() string {
	return spec.name
}

func (spec *parsedEnumType) ModuleName() string {
	return spec.moduleName
}

// Extra generated code needed for the object implementation.
func (spec *parsedEnumType) ImplementationCode() (*Statement, error) {
	code := Empty().
		Add(spec.isEnumMethodCode()).Line().Line()
	return code, nil
}

func (spec *parsedEnumType) isEnumMethodCode() *Statement {
	return Func().Params(Id("r").Id(spec.name)).
		Id("IsEnum").Params().Params().Block()
}
