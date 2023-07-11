package dagger

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"dagger.io/dagger/internal/querybuilder"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
)

var getSchema bool

func init() {
	flag.BoolVar(&getSchema, "schema", false, "print the schema rather than executing")
}

type Context struct {
	context.Context
	client *Client
}

func (c *Context) Client() *Client {
	return c.client
}

// TODO:
var defaultContext Context
var connectOnce sync.Once

func DefaultContext() Context {
	connectOnce.Do(func() {
		ctx := context.Background()
		client, err := Connect(ctx, WithLogOutput(os.Stderr))
		if err != nil {
			panic(err)
		}
		defaultContext = Context{
			Context: ctx,
			client:  client,
		}
	})
	return defaultContext
}

// TODO: this is obviously dumb, but can be cleaned up nicely w/ codegen changes
var resolvers = map[string]*goFunc{}

func (r *Environment) Serve(ctx Context) {
	if getSchema {
		id, err := r.ID(ctx)
		if err != nil {
			writeErrorf(err)
		}
		err = os.WriteFile("/outputs/envid", []byte(id), 0644)
		if err != nil {
			writeErrorf(err)
		}
		return
	}

	inputBytes, err := os.ReadFile("/inputs/dagger.json")
	if err != nil {
		writeErrorf(fmt.Errorf("unable to open request file: %w", err))
	}
	var input struct {
		Resolver string
		Parent   map[string]any
		Args     map[string]any
	}
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		writeErrorf(fmt.Errorf("unable to parse request file: %w", err))
	}

	if input.Resolver == "" {
		writeErrorf(fmt.Errorf("missing resolver"))
	}
	objName, fieldName, ok := strings.Cut(input.Resolver, ".")
	if !ok {
		writeErrorf(fmt.Errorf("invalid resolver name: %s", input.Resolver))
	}

	if objName != "Query" {
		// TODO:
		writeErrorf(fmt.Errorf("only Query is supported for now"))
	}
	fn := resolvers[fieldName]

	var result any
	if fn == nil {
		// trivial resolver
		if input.Parent != nil {
			result = input.Parent[fieldName]
		}
	} else {
		result, err = fn.call(ctx, input.Parent, input.Args)
		if err != nil {
			writeErrorf(err)
		}
	}
	if result == nil {
		result = make(map[string]any)
	}

	result, err = convertResult(ctx, result)
	if err != nil {
		writeErrorf(err)
	}
	output, err := json.Marshal(result)
	if err != nil {
		writeErrorf(fmt.Errorf("unable to marshal response: %v", err))
	}
	if err := os.WriteFile("/outputs/dagger.json", output, 0600); err != nil {
		writeErrorf(fmt.Errorf("unable to write response file: %v", err))
	}
}

type goTypes struct {
	Structs map[string]*goStruct
	Funcs   map[string]*goFunc
}

type goStruct struct {
	name    string
	typ     reflect.Type
	fields  []*goField
	methods []*goFunc
	doc     string
	// should this become a field of the Query type?
	topLevel bool
	// should this become a graphql Input type instead of an Object type?
	usedAsInput bool
}

type goField struct {
	name string
	typ  reflect.Type
}

type goFunc struct {
	name string
	// args are args to the function, except for the receiver
	// (if it's a method) and for the dagger.Context arg.
	args    []*goParam
	returns []*goParam
	typ     reflect.Type
	val     reflect.Value
	// TODO:
	// receiver *goStruct // only set for methods
	hasReceiver bool
	doc         string

	// TODO: kludge, this shouldn't be here
	isCheck bool
}

type goParam struct {
	name string
	typ  reflect.Type
}

func (fn *goFunc) srcPathAndLine() (string, int) {
	pc := fn.val.Pointer()
	fun := runtime.FuncForPC(pc)
	return fun.FileLine(pc)
}

func (fn *goFunc) call(ctx context.Context, rawParent, rawArgs map[string]any) (any, error) {
	client := DefaultContext().client
	var callArgs []reflect.Value
	if fn.hasReceiver {
		/* TODO:
		parent, err := convertInput(rawParent, fn.args[0].typ, client)
		if err != nil {
			return nil, fmt.Errorf("unable to convert parent: %w", err)
		}
		*/
		callArgs = append(callArgs, reflect.New(fn.args[0].typ).Elem())
	}

	for _, arg := range fn.args[len(callArgs):] {
		if arg.typ == reflect.TypeOf((*Context)(nil)).Elem() {
			callArgs = append(callArgs, reflect.ValueOf(Context{
				Context: ctx,
				client:  client,
			}))
			continue
		}

		rawArg, argValuePresent := rawArgs[arg.name]
		var argIsOptional bool
		switch arg.typ.Kind() {
		case reflect.Ptr, reflect.Slice:
			// TODO: sometimes we don't really want Ptr to necessarily mean "optional", i.e. *dagger.Container
			argIsOptional = true
		}

		switch {
		case argValuePresent:
			argVal, err := convertInput(rawArg, arg.typ, client)
			if err != nil {
				return nil, fmt.Errorf("unable to convert arg %s: %w", arg.name, err)
			}
			callArgs = append(callArgs, reflect.ValueOf(argVal))
		case !argIsOptional:
			return nil, fmt.Errorf("missing required argument %s", arg.name)
		default:
			callArgs = append(callArgs, reflect.New(arg.typ).Elem())
		}
	}

	reflectOutputs := fn.val.Call(callArgs)
	var returnVal any
	var returnErr error
	for _, output := range reflectOutputs {
		if output.Type() == reflect.TypeOf((*error)(nil)).Elem() {
			if !output.IsNil() {
				returnErr = output.Interface().(error)
			}
		} else {
			returnVal = output.Interface()
		}
	}
	return returnVal, returnErr
}

/*
type walkState struct {
	inputPath   bool // are we following a type path from an argument?
	isSubObject bool // false if this is an object directly provided to Serve, true otherwise
}

func (ts *goTypes) walk(t reflect.Type, state walkState) {
	switch t.Kind() {
	case reflect.Ptr:
		ts.walk(t.Elem(), state)
	case reflect.Struct:
		ts.walkStruct(t, state)
	case reflect.Slice:
		ts.walk(t.Elem(), state)
	default:
	}
}

func (ts *goTypes) walkStruct(t reflect.Type, state walkState) error {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("unexpected non-struct type: %s", t.Name())
	}

	_, ok := ts.Structs[t.Name()]
	if ok {
		return nil
	}
	strukt := &goStruct{
		name: t.Name(),
		typ:  t,
	}
	ts.Structs[t.Name()] = strukt
	if !state.isSubObject {
		strukt.topLevel = true
	}

	if state.inputPath {
		strukt.usedAsInput = true
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			return fmt.Errorf("embedded fields not supported yet: %v", f)
		}
		if !f.IsExported() {
			// TODO: probably log this somehow for a debug mode, possible gotcha
			continue
		}

		strukt.fields = append(strukt.fields, &goField{
			name: f.Name,
			typ:  f.Type,
		})
		ts.walk(f.Type, walkState{
			inputPath:   state.inputPath,
			isSubObject: true,
		})
	}

	// TODO:(sipsma) error for methods on input types
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		if m.PkgPath != "" {
			// skip unexported methods
			// TODO: double check this...
			continue
		}
		fn := &goFunc{
			name:     m.Name,
			typ:      m.Type,
			val:      m.Func,
			receiver: strukt,
		}
		if err := ts.walkArgsAndReturns(fn, state); err != nil {
			return err
		}
		strukt.methods = append(strukt.methods, fn)
	}

	return nil
}

func (ts *goTypes) walkFunc(t reflect.Type, val reflect.Value, state walkState) error {
	if t.PkgPath() != "" {
		// skip unexported funcs
		// TODO: double check this...
		return nil
	}
	name := runtime.FuncForPC(val.Pointer()).Name()
	if name == "" {
		return fmt.Errorf("entrypoint func must not be anonymous")
	}

	fn := &goFunc{
		name: name,
		typ:  t,
		val:  val,
	}
	if err := ts.walkArgsAndReturns(fn, state); err != nil {
		return err
	}
	ts.Funcs[fn.name] = fn
	return nil
}

func (ts *goTypes) walkArgsAndReturns(fn *goFunc, state walkState) error {
	skipArgs := 0
	if fn.receiver != nil {
		skipArgs++
	}

	if fn.typ.NumIn() == skipArgs {
		return fmt.Errorf("func must take a least one arg of dagger.Context")
	}

	// verify next arg is a dagger.Context
	contextParam := fn.typ.In(skipArgs)
	if contextParam != reflect.TypeOf((*Context)(nil)).Elem() {
		return fmt.Errorf("first arg of func must be dagger.Context: %s", contextParam.Kind())
	}
	skipArgs++

	// fill in rest of user defined args (if any)
	for i := skipArgs; i < fn.typ.NumIn(); i++ {
		inputParam := fn.typ.In(i)
		fn.args = append(fn.args, &goParam{
			typ: inputParam,
		})
		ts.walk(inputParam, walkState{
			inputPath:   true,
			isSubObject: true,
		})
	}

	// fill in returns
	if fn.typ.NumOut() != 2 {
		return fmt.Errorf("func %s must return exactly two values with last value being error", fn.name)
	}
	firstReturn := fn.typ.Out(0)
	fn.returns = append(fn.returns, &goParam{
		typ: firstReturn,
	})
	ts.walk(firstReturn, walkState{
		inputPath:   state.inputPath,
		isSubObject: true,
	})
	errorReturn := fn.typ.Out(1)
	if errorReturn != reflect.TypeOf((*error)(nil)).Elem() {
		return fmt.Errorf("last return of func %s must be of type error", fn.name)
	}
	fn.returns = append(fn.returns, &goParam{
		typ: errorReturn,
	})

	return nil
}

// annotate all the types we found from reflection with data only available via the AST, such as
// * Names of params (i.e. in func(foo string)), we can only get the name "foo" from the AST
// * Doc strings
func (ts *goTypes) fillASTData(parsed *goast.File, fileSet *token.FileSet) {
	goast.Inspect(parsed, func(n goast.Node) bool {
		if n == nil {
			return false
		}
		switch f := n.(type) {
		case *goast.FuncDecl:
			if f.Recv == nil {
				ts.fillFuncFromAST(f, fileSet)
			} else {
				ts.fillMethodFromAST(f)
			}
		case *goast.GenDecl:
			// check if it's a struct we know about, if so, fill in the doc string
			if f.Tok != token.TYPE {
				return true
			}
			for _, spec := range f.Specs {
				typeSpec, ok := spec.(*goast.TypeSpec)
				if !ok {
					continue
				}
				strukt, ok := ts.Structs[typeSpec.Name.Name]
				if !ok {
					continue
				}
				strukt.doc = f.Doc.Text()
			}
		default:
		}
		return true
	})
}

func (ts *goTypes) fillMethodFromAST(decl *goast.FuncDecl) {
	if len(decl.Recv.List) != 1 {
		return
	}
	recvIdent, ok := decl.Recv.List[0].Type.(*goast.Ident)
	if !ok {
		return
	}
	recv, ok := ts.Structs[recvIdent.Name]
	if !ok {
		return
	}
	var fn *goFunc
	for _, m := range recv.methods {
		if m.name == decl.Name.Name {
			fn = m
			break
		}
	}
	if fn == nil {
		// not found
		// TODO: have caller verify every func was found eventually
		return
	}
	fn.doc = decl.Doc.Text()

	astParamList := decl.Type.Params.List[1:] // skip the dagger.Context param
	argIndex := 0
	for _, param := range astParamList {
		// if the signature is like func(a, b string), then a and b are in the same Names slice
		for _, name := range param.Names {
			fn.args[argIndex].name = name.Name
			argIndex++
		}
	}
}

func (ts *goTypes) fillFuncFromAST(decl *goast.FuncDecl, fileSet *token.FileSet) {
	// TODO: this probably doesn't work when funcs have duplicated names across packages

	// TODO: maybe change map of fnName->fn to be fnSourceLine->fn?
	// Can't just check for func name because reflect/runtime give a weird name for it
	var fn *goFunc
	for _, possibleFn := range ts.Funcs {
		astStart := fileSet.PositionFor(decl.Pos(), false)
		astEnd := fileSet.PositionFor(decl.End(), false)
		srcPath, srcLine := possibleFn.srcPathAndLine()

		if astStart.Filename != srcPath {
			continue
		}
		// srcLine can be inside the function body due to optimizations that set it to
		// the location of the return statement
		if srcLine < astStart.Line || srcLine > astEnd.Line {
			continue
		}

		fn = possibleFn
		fn.name = decl.Name.Name
		fn.doc = decl.Doc.Text()
		break
	}
	if fn == nil {
		// not found
		// TODO: have caller verify every func was found eventually
		return
	}

	astParamList := decl.Type.Params.List[1:] // skip the dagger.Context param
	argIndex := 0
	for _, param := range astParamList {
		// if the signature is like func(a, b string), then a and b are in the same Names slice
		for _, name := range param.Names {
			fn.args[argIndex].name = name.Name
			argIndex++
		}
	}
}
*/

func writeErrorf(err error) {
	fmt.Println(err.Error())
	os.Exit(1)
}

func init() {
	// TODO:(sipsma) silly+unmaintainable, is there a pre-made list of these somewhere?
	// Or can we have a rule that "ALLCAPS" becomes "allcaps" instead of aLLCAPS?
	strcase.ConfigureAcronym("URL", "url")
	strcase.ConfigureAcronym("CI", "ci")
	strcase.ConfigureAcronym("SDK", "sdk")
}

func lowerCamelCase(s string) string {
	return strcase.ToLowerCamel(s)
}

func inputName(name string) string {
	return name + "Input"
}

func goReflectTypeToGraphqlType(t reflect.Type, isInput bool) (*ast.Type, error) {
	switch t.Kind() {
	case reflect.String:
		/* TODO:(sipsma)
		Huge hack: handle any scalar type from the go SDK (i.e. DirectoryID/ContainerID)
		The much cleaner approach will come when we integrate this code w/ the
		in-progress codegen work.
		*/
		if strings.HasPrefix(t.PkgPath(), "dagger.io/dagger") {
			return ast.NonNullNamedType(t.Name(), nil), nil
		}
		return ast.NonNullNamedType("String", nil), nil
	case reflect.Int:
		return ast.NonNullNamedType("Int", nil), nil
	case reflect.Float32, reflect.Float64:
		// TODO:(sipsma) does this actually handle both float32 and float64?
		return ast.NonNullNamedType("Float", nil), nil
	case reflect.Bool:
		return ast.NonNullNamedType("Boolean", nil), nil
	case reflect.Slice:
		elementType, err := goReflectTypeToGraphqlType(t.Elem(), isInput)
		if err != nil {
			return nil, err
		}
		return ast.ListType(elementType, nil), nil
	case reflect.Struct:
		// Handle types that implement the GraphQL serializer
		// TODO: move this at the top so it works on scalars as well
		marshaller := reflect.TypeOf((*querybuilder.GraphQLMarshaller)(nil)).Elem()
		if t.Implements(marshaller) {
			typ := reflect.New(t)
			var typeName string
			if isInput {
				result := typ.MethodByName(querybuilder.GraphQLMarshallerIDType).Call([]reflect.Value{})
				typeName = result[0].String()
			} else {
				result := typ.MethodByName(querybuilder.GraphQLMarshallerType).Call([]reflect.Value{})
				typeName = result[0].String()
			}
			return ast.NonNullNamedType(typeName, nil), nil
		}

		if isInput {
			return ast.NonNullNamedType(inputName(t.Name()), nil), nil
		}
		return ast.NonNullNamedType(t.Name(), nil), nil // TODO:(sipsma) doesn't handle anything from another package (besides the sdk)
	case reflect.Pointer:
		nonNullType, err := goReflectTypeToGraphqlType(t.Elem(), isInput)
		if err != nil {
			return nil, err
		}
		nonNullType.NonNull = false
		return nonNullType, nil
	default:
		return nil, fmt.Errorf("unsupported type %s", t.Kind())
	}
}

// convertResult will recursively walk the result and update any values that can
// be converted into a graphql ID to that. It also fixes the casing of any fields
// to match the casing of the graphql schema (lower camel case).
// TODO: the MarshalGQL func in querybuilder is very similar to this one, dedupe somehow?
func convertResult(ctx context.Context, result any) (any, error) {
	if result == nil {
		return result, nil
	}

	if result, ok := result.(querybuilder.GraphQLMarshaller); ok {
		id, err := result.XXX_GraphQLID(ctx)
		if err != nil {
			return nil, err
		}
		// ID-able dagger objects are serialized as their ID string across the wire
		// between the session and project container.
		return id, nil
	}

	switch typ := reflect.TypeOf(result).Kind(); typ {
	case reflect.Pointer:
		return convertResult(ctx, reflect.ValueOf(result).Elem().Interface())
	case reflect.Interface:
		return convertResult(ctx, reflect.ValueOf(result).Elem().Interface())
	case reflect.Slice:
		slice := reflect.ValueOf(result)
		for i := 0; i < slice.Len(); i++ {
			converted, err := convertResult(ctx, slice.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			slice.Index(i).Set(reflect.ValueOf(converted))
		}
		return slice.Interface(), nil
	case reflect.Struct:
		converted := map[string]any{}
		for i := 0; i < reflect.TypeOf(result).NumField(); i++ {
			field := reflect.TypeOf(result).Field(i)
			value := reflect.ValueOf(result).Field(i).Interface()
			convertedField, err := convertResult(ctx, value)
			if err != nil {
				return nil, err
			}
			converted[lowerCamelCase(field.Name)] = convertedField
		}
		return converted, nil
	case reflect.Map:
		converted := map[string]any{}
		for _, key := range reflect.ValueOf(result).MapKeys() {
			value := reflect.ValueOf(result).MapIndex(key).Interface()
			convertedValue, err := convertResult(ctx, value)
			if err != nil {
				return nil, err
			}
			converted[lowerCamelCase(key.String())] = convertedValue
		}
		return converted, nil
	default:
		return result, nil
	}
}

// TODO: doc, basically inverse of convertResult
func convertInput(input any, desiredType reflect.Type, client *Client) (any, error) {
	// check if desiredType implements querybuilder.GraphQLMarshaller, in which case it's a core type e.g. Container
	marshaller := reflect.TypeOf((*querybuilder.GraphQLMarshaller)(nil)).Elem()
	if desiredType.Implements(marshaller) {
		// ID-able dagger objects are serialized as their ID string across the wire
		// between the session and project container.
		id, ok := input.(string)
		if !ok {
			return nil, fmt.Errorf("expected id to be a string, got %T(%+v)", input, input)
		}
		if desiredType.Kind() != reflect.Ptr {
			// just assuming it's always a pointer for now, not actually important
			return nil, fmt.Errorf("expected desiredType to be a pointer, got %s", desiredType.Kind())
		}
		desiredType = desiredType.Elem()

		// TODO: Add a .XXX_GraphQLObject(id) method to the generated SDK code to make this simpler + more maintainable
		graphqlType := reflect.New(desiredType).Interface().(querybuilder.GraphQLMarshaller).XXX_GraphQLType()
		switch graphqlType {
		case "Container":
			return client.Container(ContainerOpts{
				ID: ContainerID(id),
			}), nil
		case "Directory":
			return client.Directory(DirectoryOpts{
				ID: DirectoryID(id),
			}), nil
		case "Socket":
			return client.Socket(SocketOpts{
				ID: SocketID(id),
			}), nil
		case "File":
			return client.File(FileID(id)), nil
		case "Secret":
			return client.Secret(SecretID(id)), nil
		case "Cache":
			cacheID := CacheID(id)
			return CacheVolume{
				q:  client.q,
				c:  client.c,
				id: &cacheID,
			}, nil
		default:
			return nil, fmt.Errorf("unhandled GraphQL marshaller type %s", graphqlType)
		}
	}

	// recurse
	newInput := reflect.New(desiredType).Interface()
	inputObj := reflect.ValueOf(newInput).Elem()
	switch desiredType.Kind() {
	case reflect.Pointer:
		x, err := convertInput(inputObj.Interface(), desiredType.Elem(), client)
		if err != nil {
			return nil, err
		}
		return &x, nil
	case reflect.Slice:
		for i := 0; i < inputObj.Len(); i++ {
			value := inputObj.Index(i).Interface()
			convertedValue, err := convertInput(value, desiredType.Elem(), client)
			if err != nil {
				return nil, err
			}
			inputObj.Index(i).Set(reflect.ValueOf(convertedValue))
		}
		return inputObj.Interface(), nil
	case reflect.Struct:
		for i := 0; i < desiredType.NumField(); i++ {
			inputMap, ok := input.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected input to be a map[string]any, got %T", input)
			}
			value := inputMap[lowerCamelCase(desiredType.Field(i).Name)]
			desiredValueType := desiredType.Field(i).Type
			convertedField, err := convertInput(value, desiredValueType, client)
			if err != nil {
				return nil, err
			}
			inputObj.Field(i).Set(reflect.ValueOf(convertedField))
		}
		return inputObj.Interface(), nil
	case reflect.Map:
		for _, key := range inputObj.MapKeys() {
			value := inputObj.MapIndex(key).Interface()
			convertedValue, err := convertInput(value, desiredType.Elem(), client)
			if err != nil {
				return nil, err
			}
			inputObj.SetMapIndex(key, reflect.ValueOf(convertedValue))
		}
		return inputObj.Interface(), nil
	default:
		return input, nil
	}
}
