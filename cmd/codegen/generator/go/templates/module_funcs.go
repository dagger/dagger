package templates

import (
	"encoding/json"
	"fmt"
	"go/types"
	"maps"
	"strconv"
	"strings"

	. "github.com/dave/jennifer/jen" //nolint:stylecheck
)

const errorTypeName = "error"

var voidDef = Qual("dag", "TypeDef").Call().
	Dot("WithKind").Call(Id("VoidKind")).
	Dot("WithOptional").Call(Lit(true))

func (ps *parseState) parseGoFunc(parentType *types.Named, fn *types.Func) (*funcTypeSpec, error) {
	spec := &funcTypeSpec{
		name: fn.Name(),
	}

	funcDecl, err := ps.declForFunc(parentType, fn)
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for method %s: %w", fn.Name(), err)
	}
	spec.doc = funcDecl.Doc.Text()

	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("expected method to be a func, got %T", fn.Type())
	}
	spec.goType = sig

	spec.argSpecs, err = ps.parseParamSpecs(parentType, fn)
	if err != nil {
		return nil, err
	}

	if parentType != nil {
		if _, ok := parentType.Underlying().(*types.Struct); ok {
			// stash away the method signature so we can remember details on how it's
			// invoked (e.g. no error return, no ctx arg, error-only return, etc)
			// TODO: clean up w/ new approach of everything being a TypeSpec?
			receiverTypeName := parentType.Obj().Name()
			ps.methods[receiverTypeName] = append(ps.methods[receiverTypeName], method{fn: fn, paramSpecs: spec.argSpecs})
		}
	}

	results := sig.Results()
	switch results.Len() {
	case 0:
		// returnSpec stays nil, indicating void return
	case 1:
		result := results.At(0).Type()
		if result.String() == errorTypeName {
			spec.returnsError = true
			break
		}
		spec.returnSpec, err = ps.parseGoTypeReference(result, nil, false)
		if err != nil {
			return nil, fmt.Errorf("failed to parse return type: %w", err)
		}
	case 2:
		spec.returnsError = true
		result := results.At(0).Type()
		spec.returnSpec, err = ps.parseGoTypeReference(result, nil, false)
		if err != nil {
			return nil, fmt.Errorf("failed to parse return type: %w", err)
		}
	default:
		return nil, fmt.Errorf("method %s has too many return values", fn.Name())
	}

	return spec, nil
}

type funcTypeSpec struct {
	name string
	doc  string

	argSpecs []paramSpec

	returnSpec   ParsedType // nil if void return
	returnsError bool

	goType *types.Signature
}

var _ ParsedType = &funcTypeSpec{}

func (spec *funcTypeSpec) TypeDefCode() (*Statement, error) {
	var fnReturnTypeDefCode *Statement
	if spec.returnSpec == nil {
		fnReturnTypeDefCode = voidDef
	} else {
		var err error
		fnReturnTypeDefCode, err = spec.returnSpec.TypeDefCode()
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type code: %w", err)
		}
	}

	fnTypeDefCode := Qual("dag", "Function").Call(Lit(spec.name), Add(Line(), fnReturnTypeDefCode))

	if spec.doc != "" {
		fnTypeDefCode = dotLine(fnTypeDefCode, "WithDescription").Call(Lit(strings.TrimSpace(spec.doc)))
	}

	for i, argSpec := range spec.argSpecs {
		if i == 0 && argSpec.paramType.String() == contextTypename {
			// ignore ctx arg
			continue
		}

		argTypeDefCode, err := argSpec.typeSpec.TypeDefCode()
		if err != nil {
			return nil, fmt.Errorf("failed to generate arg type code: %w", err)
		}
		if argSpec.optional {
			argTypeDefCode = argTypeDefCode.Dot("WithOptional").Call(Lit(true))
		}

		argOptsCode := []Code{}
		if argSpec.description != "" {
			argOptsCode = append(argOptsCode, Id("Description").Op(":").Lit(argSpec.description))
		}
		if argSpec.defaultValue != "" {
			var jsonEnc string
			if argSpec.typeSpec.GoType().String() == "string" {
				enc, err := json.Marshal(argSpec.defaultValue)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal default value: %w", err)
				}
				jsonEnc = string(enc)
			} else {
				jsonEnc = argSpec.defaultValue
			}
			argOptsCode = append(argOptsCode, Id("DefaultValue").Op(":").Id("JSON").Call(Lit(jsonEnc)))
		}

		// arguments to WithArg (args to arg... ugh, at least the name of the variable is honest?)
		argTypeDefArgCode := []Code{Lit(argSpec.name), argTypeDefCode}
		if len(argOptsCode) > 0 {
			argTypeDefArgCode = append(argTypeDefArgCode, Id("FunctionWithArgOpts").Values(argOptsCode...))
		}
		fnTypeDefCode = dotLine(fnTypeDefCode, "WithArg").Call(argTypeDefArgCode...)
	}

	return fnTypeDefCode, nil
}

func (spec *funcTypeSpec) GoType() types.Type {
	return spec.goType
}

func (spec *funcTypeSpec) GoSubTypes() []types.Type {
	var types []types.Type
	if spec.returnSpec != nil {
		types = append(types, spec.returnSpec.GoSubTypes()...)
	}
	for _, argSpec := range spec.argSpecs {
		if argSpec.typeSpec == nil {
			// ignore context
			continue
		}
		types = append(types, argSpec.typeSpec.GoSubTypes()...)
	}
	return types
}

func (ps *parseState) parseParamSpecs(parentType *types.Named, fn *types.Func) ([]paramSpec, error) {
	sig := fn.Type().(*types.Signature)
	params := sig.Params()
	if params.Len() == 0 {
		return nil, nil
	}

	specs := make([]paramSpec, 0, params.Len())

	i := 0
	if params.At(i).Type().String() == contextTypename {
		spec, err := ps.parseParamSpecVar(params.At(i), "", "")
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)

		i++
	}

	fnDecl, err := ps.declForFunc(parentType, fn)
	if err != nil {
		return nil, err
	}

	// is the first data param an inline struct? if so, process each field of
	// the struct as a top-level param
	if i+1 == params.Len() {
		param := params.At(i)
		paramType, ok := asInlineStruct(param.Type())
		if ok {
			stype, ok := asInlineStructAst(fnDecl.Type.Params.List[i].Type)
			if !ok {
				return nil, fmt.Errorf("expected struct type for %s", param.Name())
			}

			parent := &paramSpec{
				name:      params.At(i).Name(),
				paramType: param.Type(),
			}

			paramFields := unpackASTFields(stype.Fields)
			for f := 0; f < paramType.NumFields(); f++ {
				spec, err := ps.parseParamSpecVar(paramType.Field(f), paramFields[f].Doc.Text(), paramFields[f].Comment.Text())
				if err != nil {
					return nil, err
				}
				spec.parent = parent
				specs = append(specs, spec)
			}
			return specs, nil
		}
	}

	// if other parameter passing schemes fail, just treat each remaining arg
	// as a top-level param
	paramFields := unpackASTFields(fnDecl.Type.Params)
	for ; i < params.Len(); i++ {
		docComment, lineComment := ps.commentForFuncField(fnDecl, paramFields, i)
		spec, err := ps.parseParamSpecVar(params.At(i), docComment.Text(), lineComment.Text())
		if err != nil {
			return nil, err
		}
		if sig.Variadic() && i == params.Len()-1 {
			spec.variadic = true
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func (ps *parseState) parseParamSpecVar(field *types.Var, docComment string, lineComment string) (paramSpec, error) {
	if _, ok := field.Type().(*types.Struct); ok {
		return paramSpec{}, fmt.Errorf("nested structs are not supported")
	}

	paramType := field.Type()
	baseType := paramType
	isPtr := false
	for {
		ptr, ok := baseType.(*types.Pointer)
		if !ok {
			break
		}
		isPtr = true
		baseType = ptr.Elem()
	}

	optional := false
	defaultValue := ""

	wrappedType, isOptionalType, err := ps.isOptionalWrapper(baseType)
	if err != nil {
		return paramSpec{}, fmt.Errorf("failed to check if type is optional: %w", err)
	}
	if isOptionalType {
		optional = true
		baseType = wrappedType
		isPtr = false
		for {
			ptr, ok := baseType.(*types.Pointer)
			if !ok {
				break
			}
			isPtr = true
			baseType = ptr.Elem()
		}
	}

	docPragmas, docComment := parsePragmaComment(docComment)
	linePragmas, lineComment := parsePragmaComment(lineComment)
	comment := strings.TrimSpace(docComment)
	if comment == "" {
		comment = strings.TrimSpace(lineComment)
	}

	pragmas := make(map[string]string)
	maps.Copy(pragmas, docPragmas)
	maps.Copy(pragmas, linePragmas)
	if v, ok := pragmas["default"]; ok {
		defaultValue = v
	}
	if v, ok := pragmas["optional"]; ok {
		if v == "" {
			optional = true
		} else {
			optional, _ = strconv.ParseBool(v)
		}
	}

	// ignore ctx arg for parsing type reference
	isContext := paramType.String() == contextTypename
	var typeSpec ParsedType
	if !isContext {
		var err error
		typeSpec, err = ps.parseGoTypeReference(baseType, nil, isPtr)
		if err != nil {
			return paramSpec{}, fmt.Errorf("failed to parse type reference: %w", err)
		}
	}

	name := field.Name()
	if name == "" && typeSpec != nil {
		// emulate struct behaviour, where a field with no name gets the type name
		name = typeSpec.GoType().String()
	}

	return paramSpec{
		name:               name,
		paramType:          paramType,
		typeSpec:           typeSpec,
		optional:           optional,
		hasOptionalWrapper: isOptionalType,
		isContext:          isContext,
		defaultValue:       defaultValue,
		description:        comment,
	}, nil
}

type paramSpec struct {
	name        string
	description string

	optional bool
	variadic bool
	// hasOptionalWrapper is true if the type is wrapped in the Optional generic type
	hasOptionalWrapper bool
	// isContext is true if the type is context.Context
	isContext bool

	defaultValue string

	// paramType is the full type declared in the function signature, which may
	// include pointer types, Optional, etc
	paramType types.Type
	// typeSpec is the parsed TypeSpec of the argument's "base type", which doesn't
	// include pointers, Optional, etc
	typeSpec ParsedType

	// parent is set if this paramSpec is nested inside a parent inline struct,
	// and is used to create a declaration of the entire inline struct
	parent *paramSpec
}
