package templates

import (
	"fmt"
	"go/constant"
	"go/types"
	"strings"

	. "github.com/dave/jennifer/jen" //nolint:stylecheck
)

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

		value := ""
		if objConst.Val().Kind() == constant.String {
			value = constant.StringVal(objConst.Val())
		} else {
			value = objConst.Val().ExactString()
		}

		astSpec, err := ps.astSpecForObj(objConst)
		if err != nil {
			return nil, fmt.Errorf("failed to find decl for object %s: %w", spec.name, err)
		}

		valueSpec := &parsedEnumValue{
			value: value,
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

	values []*parsedEnumValue

	goType *types.Basic
}

type parsedEnumValue struct {
	value     string
	doc       string
	sourceMap *sourceMap
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
	return Empty(), nil
}
