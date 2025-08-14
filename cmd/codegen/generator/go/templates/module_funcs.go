package templates

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"maps"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	. "github.com/dave/jennifer/jen" //nolint:stylecheck
	"github.com/mitchellh/mapstructure"
)

const errorTypeName = "error"

var voidDef = Qual("dag", "TypeDef").Call().
	Dot("WithKind").Call(Id("dagger").Dot("TypeDefKindVoidKind")).
	Dot("WithOptional").Call(Lit(true))
var voidDefObject = (&core.TypeDef{}).WithKind(core.TypeDefKindVoid).WithOptional(true)

func (ps *parseState) parseGoFunc(parentType *types.Named, fn *types.Func) (*funcTypeSpec, error) {
	spec := &funcTypeSpec{
		name: fn.Name(),
	}

	funcDecl, err := ps.declForFunc(parentType, fn)
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for method %s: %w", fn.Name(), err)
	}
	spec.doc = funcDecl.Doc.Text()
	spec.sourceMap = ps.sourceMap(funcDecl)

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
	name      string
	doc       string
	sourceMap *sourceMap

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
	if spec.sourceMap != nil {
		fnTypeDefCode = dotLine(fnTypeDefCode, "WithSourceMap").Call(spec.sourceMap.TypeDefCode())
	}

	for _, argSpec := range spec.argSpecs {
		if argSpec.isContext {
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
		if argSpec.sourceMap != nil {
			argOptsCode = append(argOptsCode, Id("SourceMap").Op(":").Add(argSpec.sourceMap.TypeDefCode()))
		}
		if argSpec.hasDefaultValue {
			var defaultValue string
			if enumType, ok := argSpec.typeSpec.(*parsedEnumTypeReference); ok {
				v, ok := argSpec.defaultValue.(string)
				if !ok {
					return nil, fmt.Errorf("unknown enum value %q", v)
				}
				res := enumType.lookup(v)
				if res == nil {
					return nil, fmt.Errorf("unknown enum value %q", defaultValue)
				}
				defaultValue = strconv.Quote(res.name)
			} else {
				v, err := json.Marshal(argSpec.defaultValue)
				if err != nil {
					return nil, fmt.Errorf("could not encode default value %q: %w", argSpec.defaultValue, err)
				}
				defaultValue = string(v)
			}
			argOptsCode = append(argOptsCode, Id("DefaultValue").Op(":").Id("dagger").Dot("JSON").Call(Lit(defaultValue)))
		}

		if argSpec.defaultPath != "" {
			argOptsCode = append(argOptsCode, Id("DefaultPath").Op(":").Lit(argSpec.defaultPath))
		}

		if len(argSpec.ignore) > 0 {
			ignores := make([]Code, 0, len(argSpec.ignore))
			for _, pattern := range argSpec.ignore {
				ignores = append(ignores, Lit(pattern))
			}

			argOptsCode = append(argOptsCode, Id("Ignore").Op(":").Index().String().Values(ignores...))
		}

		// arguments to WithArg (args to arg... ugh, at least the name of the variable is honest?)
		argTypeDefArgCode := []Code{Lit(argSpec.name), argTypeDefCode}
		if len(argOptsCode) > 0 {
			argTypeDefArgCode = append(argTypeDefArgCode, Id("dagger").Dot("FunctionWithArgOpts").Values(argOptsCode...))
		}
		fnTypeDefCode = dotLine(fnTypeDefCode, "WithArg").Call(argTypeDefArgCode...)
	}

	return fnTypeDefCode, nil
}

func (spec *funcTypeSpec) TypeDefObject() (*core.TypeDef, error) {
	var fnReturnTypeDef *core.TypeDef
	if spec.returnSpec == nil {
		fnReturnTypeDef = voidDefObject
	} else {
		var err error
		fnReturnTypeDef, err = spec.returnSpec.TypeDefObject()
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type object: %w", err)
		}
	}

	fnTypeDefObject := core.NewFunction(spec.name, fnReturnTypeDef)

	if spec.doc != "" {
		fnTypeDefObject = fnTypeDefObject.WithDescription(strings.TrimSpace(spec.doc))
	}
	if spec.sourceMap != nil {
		fnTypeDefObject.WithSourceMap(coreSourceMap(spec.sourceMap))
	}

	for _, argSpec := range spec.argSpecs {
		if argSpec.isContext {
			// ignore ctx arg
			continue
		}

		argTypeDefObject, err := argSpec.typeSpec.TypeDefObject()
		if err != nil {
			return nil, fmt.Errorf("failed to generate arg type object: %w", err)
		}
		if argSpec.optional {
			argTypeDefObject = argTypeDefObject.WithOptional(true)
		}

		var defaultValueJSON core.JSON
		if argSpec.hasDefaultValue {
			var defaultValue string
			if enumType, ok := argSpec.typeSpec.(*parsedEnumTypeReference); ok {
				v, ok := argSpec.defaultValue.(string)
				if !ok {
					return nil, fmt.Errorf("unknown enum value %q", v)
				}
				res := enumType.lookup(v)
				if res == nil {
					return nil, fmt.Errorf("unknown enum value %q", defaultValue)
				}
				defaultValue = strconv.Quote(res.name)
			} else {
				v, err := json.Marshal(argSpec.defaultValue)
				if err != nil {
					return nil, fmt.Errorf("could not encode default value %q: %w", argSpec.defaultValue, err)
				}
				defaultValue = string(v)
			}
			defaultValueJSON = core.JSON(defaultValue)
		}

		fnTypeDefObject = fnTypeDefObject.WithArg(argSpec.name, argTypeDefObject, strings.TrimSpace(argSpec.description), defaultValueJSON, argSpec.defaultPath, argSpec.ignore, coreSourceMap(argSpec.sourceMap))
	}

	return (&core.TypeDef{
		Kind:     core.TypeDefKindObject,
		AsObject: dagql.NonNull(core.NewObjectTypeDef("", "")),
	}).WithFunction(fnTypeDefObject)
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
	if spec, err := ps.parseParamSpecVar(params.At(i), nil, "", ""); err == nil && spec.isContext {
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
			for f := range paramType.NumFields() {
				spec, err := ps.parseParamSpecVar(paramType.Field(f), paramFields[f], paramFields[f].Doc.Text(), paramFields[f].Comment.Text())
				if err != nil {
					return nil, err
				}
				if spec.isContext {
					return nil, fmt.Errorf("unexpected context type in inline field %s", spec.name)
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
		spec, err := ps.parseParamSpecVar(params.At(i), paramFields[i], docComment.Text(), lineComment.Text())
		if err != nil {
			return nil, err
		}
		if spec.isContext {
			return nil, fmt.Errorf("unexpected context type for arg %s", spec.name)
		}
		if sig.Variadic() && i == params.Len()-1 {
			spec.variadic = true
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func (ps *parseState) parseParamSpecVar(field *types.Var, astField *ast.Field, docComment string, lineComment string) (paramSpec, error) {
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

	docPragmas, docComment := parsePragmaComment(docComment)
	linePragmas, lineComment := parsePragmaComment(lineComment)
	comment := strings.TrimSpace(docComment)
	if comment == "" {
		comment = strings.TrimSpace(lineComment)
	}

	pragmas := make(map[string]any)
	maps.Copy(pragmas, docPragmas)
	maps.Copy(pragmas, linePragmas)

	defaultValue, hasDefaultValue := pragmas["default"]

	optional := false
	if v, ok := pragmas["optional"]; ok {
		if v == nil {
			optional = true
		} else {
			optional, _ = v.(bool)
		}
	}
	defaultPath := ""
	if v, ok := pragmas["defaultPath"]; ok {
		defaultPath, ok = v.(string)
		if !ok {
			return paramSpec{}, fmt.Errorf("defaultPath pragma %q, must be a valid string", v)
		}
		if strings.HasPrefix(defaultPath, `"`) && strings.HasSuffix(defaultPath, `"`) {
			defaultPath = defaultPath[1 : len(defaultPath)-1]
		}

		optional = true // If defaultPath is set, the argument becomes optional
	}

	ignore := []string{}
	if v, ok := pragmas["ignore"]; ok {
		err := mapstructure.Decode(v, &ignore)
		if err != nil {
			return paramSpec{}, fmt.Errorf("ignore pragma %q, must be a valid JSON array: %w", v, err)
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

	var sourceMap *sourceMap
	if astField != nil {
		sourceMap = ps.sourceMap(astField)
	}

	return paramSpec{
		name:            name,
		paramType:       paramType,
		sourceMap:       sourceMap,
		typeSpec:        typeSpec,
		optional:        optional,
		isContext:       isContext,
		defaultValue:    defaultValue,
		hasDefaultValue: hasDefaultValue,
		description:     comment,
		defaultPath:     defaultPath,
		ignore:          ignore,
	}, nil
}

type paramSpec struct {
	name        string
	description string
	sourceMap   *sourceMap

	optional bool
	variadic bool
	// isContext is true if the type is context.Context
	isContext bool

	// Set a default value for the argument. Value must be a json-encoded literal value
	defaultValue    any
	hasDefaultValue bool

	// paramType is the full type declared in the function signature, which may
	// include pointer types, etc
	paramType types.Type
	// typeSpec is the parsed TypeSpec of the argument's "base type", which doesn't
	// include pointers, etc
	typeSpec ParsedType

	// parent is set if this paramSpec is nested inside a parent inline struct,
	// and is used to create a declaration of the entire inline struct
	parent *paramSpec

	// Only applies to arguments of type File or Directory.
	// If the argument is not set, load it from the given path in the context directory
	defaultPath string

	// Only applies to arguments of type Directory.
	// The ignore patterns are applied to the input directory, and
	// matching entries are filtered out, in a cache-efficient manner.
	ignore []string
}
