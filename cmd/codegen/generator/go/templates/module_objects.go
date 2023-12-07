package templates

import (
	"fmt"
	"go/ast"
	"go/types"
	"maps"
	"reflect"
	"sort"
	"strconv"
	"strings"

	. "github.com/dave/jennifer/jen" // nolint:revive,stylecheck
)

func (ps *parseState) parseGoStruct(t *types.Struct, named *types.Named) (*parsedObjectType, error) {
	spec := &parsedObjectType{
		goType: t,
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
	for i := 0; i < methodSet.Len(); i++ {
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
	astSpec, err := ps.astSpecForNamedType(named)
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for named type %s: %w", spec.name, err)
	}
	spec.doc = astSpec.Doc.Text()

	astStructType, ok := astSpec.Type.(*ast.StructType)
	if !ok {
		return nil, fmt.Errorf("expected type spec to be a struct, got %T", astSpec.Type)
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

		fieldSpec := &fieldSpec{}
		fieldSpec.typeSpec, err = ps.parseGoTypeReference(field.Type(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to parse field type: %w", err)
		}

		fieldSpec.doc = comment

		fieldSpec.name = name

		// override the name with the json tag if it was set - otherwise, we
		// end up asking for a name that we won't unmarshal correctly
		tag := reflect.StructTag(t.Tag(i))
		if dt := tag.Get("json"); dt != "" {
			dt, _, _ = strings.Cut(dt, ",")
			if dt == "-" {
				continue
			}
			fieldSpec.name = dt
		}

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
	name string
	doc  string

	fields      []*fieldSpec
	methods     []*funcTypeSpec
	constructor *funcTypeSpec

	goType *types.Struct
}

var _ ParsedType = &parsedObjectType{}

func (spec *parsedObjectType) TypeDefCode() (*Statement, error) {
	withObjectArgsCode := []Code{
		Lit(spec.name),
	}
	withObjectOptsCode := []Code{}
	if spec.doc != "" {
		withObjectOptsCode = append(withObjectOptsCode, Id("Description").Op(":").Lit(strings.TrimSpace(spec.doc)))
	}
	if len(withObjectOptsCode) > 0 {
		withObjectArgsCode = append(withObjectArgsCode, Id("TypeDefWithObjectOpts").Values(withObjectOptsCode...))
	}

	typeDefCode := Qual("dag", "TypeDef").Call().Dot("WithObject").Call(withObjectArgsCode...)

	for _, method := range spec.methods {
		fnTypeDefCode, err := method.TypeDefCode()
		if err != nil {
			return nil, fmt.Errorf("failed to convert method %s to function def: %w", method.name, err)
		}
		typeDefCode = dotLine(typeDefCode, "WithFunction").Call(Add(Line(), fnTypeDefCode))
	}

	for _, field := range spec.fields {
		fieldTypeDefCode, err := field.typeSpec.TypeDefCode()
		if err != nil {
			return nil, fmt.Errorf("failed to convert field type: %w", err)
		}
		withFieldArgsCode := []Code{
			Lit(field.name),
			fieldTypeDefCode,
		}
		if field.doc != "" {
			withFieldArgsCode = append(withFieldArgsCode,
				Id("TypeDefWithFieldOpts").Values(
					Id("Description").Op(":").Lit(field.doc),
				))
		}
		typeDefCode = dotLine(typeDefCode, "WithField").Call(withFieldArgsCode...)
	}

	if spec.constructor != nil {
		fnTypeDefCode, err := spec.constructor.TypeDefCode()
		if err != nil {
			return nil, fmt.Errorf("failed to convert constructor to function def: %w", err)
		}
		typeDefCode = dotLine(typeDefCode, "WithConstructor").Call(Add(Line(), fnTypeDefCode))
	}

	return typeDefCode, nil
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
		subTypes = append(subTypes, field.typeSpec.GoSubTypes()...)
	}
	if spec.constructor != nil {
		subTypes = append(subTypes, spec.constructor.GoSubTypes()...)
	}
	return subTypes
}

type fieldSpec struct {
	name     string
	doc      string
	typeSpec ParsedType
}
