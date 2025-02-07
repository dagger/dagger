package templates

import (
	"fmt"
	"go/constant"
	"go/types"
	"strings"

	. "github.com/dave/jennifer/jen" //nolint:stylecheck
	"github.com/iancoleman/strcase"
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

		name := obj.Name()
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
			originalName: name,
			name:         name,
			value:        value,
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

	values []*parsedEnumValue

	goType *types.Basic
}

type parsedEnumValue struct {
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
		// XXX: fallback to ye-olde behavior where name = value on ye-olde dagger versions
		valueTypeDefCode := []Code{
			Lit(strcase.ToScreamingSnake(val.name)),
			Lit(val.value),
		}
		var withEnumMemberOpts []Code
		if val.doc != "" {
			withEnumMemberOpts = append(withEnumMemberOpts, Id("Description").Op(":").Lit(strings.TrimSpace(val.doc)))
		}
		if val.sourceMap != nil {
			withEnumMemberOpts = append(withEnumMemberOpts, Id("SourceMap").Op(":").Add(val.sourceMap.TypeDefCode()))
		}
		if len(withEnumMemberOpts) > 0 {
			valueTypeDefCode = append(valueTypeDefCode,
				Id("dagger").Dot("TypeDefWithEnumMemberOpts").Values(withEnumMemberOpts...),
			)
		}
		typeDefCode = dotLine(typeDefCode, "WithEnumMember").Call(valueTypeDefCode...)
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
	return Func().Params(Id("r").Id(spec.name)).
		Id("Name").
		Params().
		Params(String()).
		BlockFunc(func(g *Group) {
			var cases []Code
			for _, v := range spec.values {
				raw := strcase.ToScreamingSnake(v.name)
				cases = append(cases, Case(Id(v.originalName)).Block(Return(Lit(raw))))
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
		Params(Id("[]byte"), Id("error")).
		BlockFunc(func(g *Group) {
			g.Return(Id("json").Dot("Marshal").Call(Id("r").Dot("Name").Call()))
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
			for _, v := range spec.values {
				raw := strcase.ToScreamingSnake(v.name)
				cases = append(cases, Case(Lit(raw)).Block(Op("*").Id("r").Op("=").Id(v.originalName)))
			}
			cases = append(cases, Default().Block(Return(
				Qual("fmt", "Errorf").Call(Lit("unknown enum value %q"), Id("s")),
			)))
			g.Switch(Id("s")).Block(cases...)
			g.Return(Nil())
		})
}
