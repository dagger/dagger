package templates

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"maps"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/mitchellh/mapstructure"
)

const errorTypeName = "error"

func (ps *parseState) parseGoFunc(parentType *types.Named, fn *types.Func) (*funcTypeSpec, error) {
	spec := &funcTypeSpec{
		name: fn.Name(),
	}

	funcDecl, err := ps.declForFunc(parentType, fn)
	if err != nil {
		return nil, fmt.Errorf("failed to find decl for method %s: %w", fn.Name(), err)
	}

	docPragmas, docComment := parsePragmaComment(funcDecl.Doc.Text())
	spec.doc = docComment

	if v, ok := docPragmas["cache"]; ok {
		spec.cachePolicy, ok = v.(string)
		if !ok {
			return nil, fmt.Errorf("cache pragma %q, must be a valid string", v)
		}
	}

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
	name        string
	doc         string
	sourceMap   *sourceMap
	cachePolicy string

	argSpecs []paramSpec

	returnSpec   ParsedType // nil if void return
	returnsError bool

	goType *types.Signature
}

var _ ParsedType = &funcTypeSpec{}
var _ FuncParsedType = &funcTypeSpec{}

func (spec *funcTypeSpec) TypeDef(dag *dagger.Client) (*dagger.TypeDef, error) {
	return nil, nil
}

func (spec *funcTypeSpec) TypeDefFunc(dag *dagger.Client) (*dagger.Function, error) {
	var fnReturnTypeDef *dagger.TypeDef
	if spec.returnSpec == nil {
		fnReturnTypeDef = dag.TypeDef().WithKind(dagger.TypeDefKindVoidKind).WithOptional(true)
	} else {
		var err error
		fnReturnTypeDef, err = spec.returnSpec.TypeDef(dag)
		if err != nil {
			return nil, fmt.Errorf("failed to generate return type object: %w", err)
		}
	}

	fnTypeDef := dag.Function(spec.name, fnReturnTypeDef)

	if spec.doc != "" {
		fnTypeDef = fnTypeDef.WithDescription(strings.TrimSpace(spec.doc))
	}

	switch spec.cachePolicy {
	case "never":
		fnTypeDef = fnTypeDef.WithCachePolicy(dagger.FunctionCachePolicyNever)

	case "session":
		fnTypeDef = fnTypeDef.WithCachePolicy(dagger.FunctionCachePolicyPerSession)

	case "":

	default:
		fnTypeDef = fnTypeDef.WithCachePolicy(
			dagger.FunctionCachePolicyDefault,
			dagger.FunctionWithCachePolicyOpts{
				TimeToLive: strings.TrimSpace(spec.cachePolicy),
			},
		)
	}

	if spec.sourceMap != nil {
		fnTypeDef = fnTypeDef.WithSourceMap(spec.sourceMap.TypeDef(dag))
	}

	for _, argSpec := range spec.argSpecs {
		if argSpec.isContext {
			// ignore ctx arg
			continue
		}

		argTypeDef, err := argSpec.typeSpec.TypeDef(dag)
		if err != nil {
			return nil, fmt.Errorf("failed to generate arg type object: %w", err)
		}
		if argSpec.optional {
			argTypeDef = argTypeDef.WithOptional(true)
		}

		argOpts := dagger.FunctionWithArgOpts{}
		if argSpec.description != "" {
			argOpts.Description = strings.TrimSpace(argSpec.description)
		}
		if argSpec.sourceMap != nil {
			argOpts.SourceMap = argSpec.sourceMap.TypeDef(dag)
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
			argOpts.DefaultValue = dagger.JSON(defaultValue)
		}

		if argSpec.defaultPath != "" {
			argOpts.DefaultPath = argSpec.defaultPath
		}

		if len(argSpec.ignore) > 0 {
			argOpts.Ignore = argSpec.ignore
		}

		fnTypeDef = fnTypeDef.WithArg(argSpec.name, argTypeDef, argOpts)
	}

	return fnTypeDef, nil
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
