package templates

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	. "github.com/dave/jennifer/jen" //nolint:staticcheck
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

func (spec *parsedEnumTypeReference) TypeDef(dag *dagger.Client) (*dagger.TypeDef, error) {
	return dag.TypeDef().WithEnum(spec.name), nil
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
				name:       v.Name,
				value:      v.Directives.EnumValue(),
				doc:        v.Description,
				deprecated: v.DeprecationReason,
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

		pragmas := make(map[string]any)
		comment := ""
		if doc := docForAstSpec(astSpec); doc != nil {
			docPragmas, docComment := parsePragmaComment(doc.Text())
			comment = strings.TrimSpace(docComment)
			maps.Copy(pragmas, docPragmas)
		}
		if val, ok := astSpec.(*ast.ValueSpec); ok && val.Comment != nil {
			linePragmas, lineComment := parsePragmaComment(val.Comment.Text())
			if comment == "" {
				comment = strings.TrimSpace(lineComment)
			}
			maps.Copy(pragmas, linePragmas)
		}

		if raw, ok := pragmas["deprecated"]; ok {
			reason := ""
			if str, _ := raw.(string); str != "" {
				reason = str
			}
			valueSpec.deprecated = &reason
		}
		valueSpec.doc = comment

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
	deprecated   *string
	sourceMap    *sourceMap
}

var _ NamedParsedType = &parsedEnumType{}

func (spec *parsedEnumType) TypeDef(dag *dagger.Client) (*dagger.TypeDef, error) {
	withEnumOpts := dagger.TypeDefWithEnumOpts{}
	if spec.doc != "" {
		withEnumOpts.Description = strings.TrimSpace(spec.doc)
	}
	if spec.sourceMap != nil {
		withEnumOpts.SourceMap = spec.sourceMap.TypeDef(dag)
	}
	typeDefObject := dag.TypeDef().WithEnum(spec.name, withEnumOpts)

	for _, val := range spec.values {
		memberOpts := dagger.TypeDefWithEnumMemberOpts{}
		if val.value != "" {
			memberOpts.Value = val.value
		}
		if val.doc != "" {
			memberOpts.Description = strings.TrimSpace(val.doc)
		}
		if val.deprecated != nil {
			memberOpts.Deprecated = strings.TrimSpace(*val.deprecated)
		}
		if val.sourceMap != nil {
			memberOpts.SourceMap = val.sourceMap.TypeDef(dag)
		}
		typeDefObject = typeDefObject.WithEnumMember(val.name, memberOpts)
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
