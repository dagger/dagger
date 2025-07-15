package templates

import (
	"cmp"
	"fmt"
	"go/constant"
	"go/types"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	. "github.com/dave/jennifer/jen" //nolint:stylecheck
)

type parsedEnumTypeReference struct {
	name       string
	moduleName string

	values []*parsedEnumMember

	goType types.Type
	isPtr  bool
}

func (spec *parsedEnumTypeReference) lookup(key string) *parsedEnumMember {
	for _, value := range spec.values {
		if value.name == key {
			return value
		}
	}
	for _, value := range spec.values {
		if value.value == key {
			return value
		}
	}
	return nil
}

func (spec *parsedEnumTypeReference) TypeDefCode() (*Statement, error) {
	return Qual("dag", "TypeDef").Call().Dot("WithEnum").Call(Lit(spec.name)), nil
}

func (spec *parsedEnumTypeReference) TypeDefObject() (*core.TypeDef, error) {
	return (&core.TypeDef{}).WithEnum(spec.name, "", nil), nil
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
		memberTypeDefCode := []Code{
			Lit(val.name),
		}
		var withEnumMemberOpts []Code
		if val.value != "" {
			withEnumMemberOpts = append(withEnumMemberOpts, Id("Value").Op(":").Lit(val.value))
		}
		if val.doc != "" {
			withEnumMemberOpts = append(withEnumMemberOpts, Id("Description").Op(":").Lit(strings.TrimSpace(val.doc)))
		}
		if val.sourceMap != nil {
			withEnumMemberOpts = append(withEnumMemberOpts, Id("SourceMap").Op(":").Add(val.sourceMap.TypeDefCode()))
		}
		if len(withEnumMemberOpts) > 0 {
			memberTypeDefCode = append(memberTypeDefCode,
				Id("dagger").Dot("TypeDefWithEnumMemberOpts").Values(withEnumMemberOpts...),
			)
		}
		typeDefCode = dotLine(typeDefCode, "WithEnumMember").Call(memberTypeDefCode...)
	}

	return typeDefCode, nil
}

func (spec *parsedEnumType) TypeDefObject() (*core.TypeDef, error) {
	typeDefObject := (&core.TypeDef{}).WithEnum(spec.name, strings.TrimSpace(spec.doc), coreSourceMap(spec.sourceMap))

	var err error
	for _, val := range spec.values {
		typeDefObject, err = typeDefObject.WithEnumMember(val.name, val.value, strings.TrimSpace(val.doc), coreSourceMap(val.sourceMap))
		if err != nil {
			return nil, err
		}
	}
	return typeDefObject, nil
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
		Add(spec.isEnumMethodCode()).Line().Line().
		Add(spec.nameMethodCode()).Line().Line().
		Add(spec.valueMethodCode()).Line().Line().
		Add(spec.marshalJSONMethodCode()).Line().Line().
		Add(spec.unmarshalJSONMethodCode()).Line().Line()
	return code, nil
}

func (spec *parsedEnumType) isEnumMethodCode() *Statement {
	return Func().Params(Id("r").Id(spec.name)).
		Id("IsEnum").Params().Params().Block()
}

func (spec *parsedEnumType) nameMethodCode() *Statement {
	values := slices.Clone(spec.values)
	slices.SortFunc(values, func(a, b *parsedEnumMember) int {
		return cmp.Compare(a.value, b.value)
	})
	values = slices.CompactFunc(values, func(e1, e2 *parsedEnumMember) bool {
		return e1.value == e2.value
	})
	slices.SortFunc(values, func(a, b *parsedEnumMember) int {
		return cmp.Compare(a.name, b.name)
	})

	return Func().Params(Id("r").Id(spec.name)).
		Id("Name").
		Params().
		Params(String()).
		BlockFunc(func(g *Group) {
			var cases []Code
			for _, v := range values {
				cases = append(cases, Case(Id(v.originalName)).Block(Return(Lit(v.name))))
			}
			g.Switch(Id("r")).Block(cases...)
			g.Return(Lit(""))
		})
}

func (spec *parsedEnumType) valueMethodCode() *Statement {
	return Func().Params(Id("r").Id(spec.name)).
		Id("Value").
		Params().
		Params(String()).
		BlockFunc(func(g *Group) {
			g.Return(String().Call(Id("r")))
		})
}

func (spec *parsedEnumType) marshalJSONMethodCode() *Statement {
	return Func().Params(Id("r").Id(spec.name)).
		Id("MarshalJSON").
		Params().
		Params(Index().Byte(), Id("error")).
		BlockFunc(func(g *Group) {
			g.If(Id("r").Op("==").Lit("")).Block(
				Return(Index().Byte().Call(Lit(`""`)), Nil()),
			)
			g.Id("name").Op(":=").Id("r").Dot("Name").Call()
			g.If(Id("name").Op("==").Lit("")).Block(
				Return(
					Nil(),
					Qual("fmt", "Errorf").Call(Lit("invalid enum value %q"), Id("r")),
				),
			)
			g.Return(Qual("json", "Marshal").Call(Id("name")))
		})
}

func (spec *parsedEnumType) unmarshalJSONMethodCode() *Statement {
	return Func().Params(Id("r").Op("*").Id(spec.name)).
		Id("UnmarshalJSON").
		Params(Id("bs").Id("[]byte")).
		Params(Id("error")).
		BlockFunc(func(g *Group) {
			g.Var().Id("s").String()
			g.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(Id("bs"), Op("&").Id("s"))
			g.If(Id("err").Op("!=").Nil()).Block(Return(Id("err")))

			var cases []Code
			cases = append(cases, Case(Lit("")).Block(Op("*").Id("r").Op("=").Lit("")))
			for _, v := range spec.values {
				cases = append(cases, Case(Lit(v.name)).Block(Op("*").Id("r").Op("=").Id(v.originalName)))
			}
			cases = append(cases, Default().Block(Return(
				Qual("fmt", "Errorf").Call(Lit("invalid enum value %q"), Id("s")),
			)))
			g.Switch(Id("s")).Block(cases...)
			g.Return(Nil())
		})
}
