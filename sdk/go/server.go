package dagger

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"runtime"
	"strings"

	"dagger.io/dagger/internal/querybuilder"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

type Context struct {
	context.Context
	client *Client
}

func (c *Context) Client() *Client {
	return c.client
}

//nolint:gocyclo
func ServeCommands(entrypoints ...any) {
	ctx := context.Background()

	types := &goTypes{
		Structs: make(map[string]*goStruct),
		Funcs:   make(map[string]*goFunc),
	}
	for _, entrypoint := range entrypoints {
		t := reflect.TypeOf(entrypoint)
		var err error
		switch t.Kind() {
		case reflect.Struct:
			err = types.walkStruct(t, walkState{})
		case reflect.Func:
			err = types.walkFunc(t, reflect.ValueOf(entrypoint), walkState{})
		default:
			err = fmt.Errorf("unexpected entrypoint type: %v", t)
		}
		if err != nil {
			writeErrorf(err)
		}
	}
	srcFiles := make(map[string]struct{})
	for _, s := range types.Structs {
		for _, m := range s.methods {
			filePath, _ := m.srcPathAndLine()
			srcFiles[filePath] = struct{}{}
		}
	}
	for _, f := range types.Funcs {
		filePath, _ := f.srcPathAndLine()
		srcFiles[filePath] = struct{}{}
	}
	for srcFile := range srcFiles {
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, srcFile, nil, parser.ParseComments)
		if err != nil {
			writeErrorf(err)
		}
		types.fillParamNames(parsed, fileSet)
	}

	// if the schema is being requested, just return that
	var getSchema bool
	flag.BoolVar(&getSchema, "schema", false, "print the schema rather than executing")
	flag.Parse()

	if getSchema {
		schema, err := types.schema()
		if err != nil {
			writeErrorf(err)
		}
		if err := os.WriteFile("/outputs/schema.graphql", schema, 0600); err != nil {
			writeErrorf(fmt.Errorf("unable to write schema response file: %v", err))
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

	objName, fieldName, ok := strings.Cut(input.Resolver, ".")
	if !ok {
		writeErrorf(fmt.Errorf("invalid resolver name: %s", input.Resolver))
	}

	if input.Resolver == "" {
		writeErrorf(fmt.Errorf("missing resolver"))
	}

	var fn *goFunc
	strukt, ok := types.Structs[objName]
	switch {
	case ok:
		for _, m := range strukt.methods {
			if lowerCamelCase(m.name) == fieldName {
				fn = m
				break
			}
		}
	case objName == "Query":
		for _, f := range types.Funcs {
			if lowerCamelCase(f.name) == fieldName {
				fn = f
				break
			}
		}
	default:
		writeErrorf(fmt.Errorf("unknown struct: %s", objName))
	}

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

func (ts *goTypes) schema() ([]byte, error) {
	doc := &ast.SchemaDocument{}
	queryExtension := &ast.Definition{
		Kind: ast.Object,
		Name: "Query",
	}
	doc.Extensions = append(doc.Extensions, queryExtension)

	for _, s := range ts.Structs {
		marshaller := reflect.TypeOf((*querybuilder.GraphQLMarshaller)(nil)).Elem()
		if reflect.PtrTo(s.typ).Implements(marshaller) {
			// this is from our core api, don't need it in the schema here
			continue
		}

		if s.usedAsInput {
			inputDef := &ast.Definition{
				Kind:        ast.InputObject,
				Name:        inputName(s.typ.Name()),
				Description: s.doc,
			}
			// TODO:(sipsma) just ignoring methods for now, maybe should be error or something else
			for _, f := range s.fields {
				t, err := goReflectTypeToGraphqlType(f.typ, true)
				if err != nil {
					return nil, err
				}
				fieldDef := &ast.FieldDefinition{
					Name: lowerCamelCase(f.name),
					Type: t,
				}
				inputDef.Fields = append(inputDef.Fields, fieldDef)
			}
			doc.Definitions = append(doc.Definitions, inputDef)
		} else {
			if s.topLevel {
				queryExtension.Fields = append(queryExtension.Fields, &ast.FieldDefinition{
					Name:        lowerCamelCase(s.typ.Name()),
					Type:        ast.NonNullNamedType(s.typ.Name(), nil),
					Description: s.doc,
				})
			}
			objDef := &ast.Definition{
				Kind:        ast.Object,
				Name:        s.typ.Name(),
				Description: s.doc,
			}
			for _, m := range s.methods {
				ret, err := goReflectTypeToGraphqlType(m.returns[0].typ, false)
				if err != nil {
					return nil, err
				}
				fieldDef := &ast.FieldDefinition{
					Name:        lowerCamelCase(m.name),
					Type:        ret,
					Description: m.doc,
				}
				for _, arg := range m.args {
					t, err := goReflectTypeToGraphqlType(arg.typ, true)
					if err != nil {
						return nil, err
					}
					argDef := &ast.ArgumentDefinition{
						Name: lowerCamelCase(arg.name),
						Type: t,
					}
					fieldDef.Arguments = append(fieldDef.Arguments, argDef)
				}
				objDef.Fields = append(objDef.Fields, fieldDef)
			}
			// NOTE: we are purposely skipping including fields in the schema for now.
			// This is because for entrypoints we really only care about exposing the
			// methods to the CLI user, fields are only needed in the entrypoint
			// implementation code for passing context from parent commands to child
			// commands.
			// In the future we will most likely want to support returning objects
			// as outputs of methods/funcs. One option will be to only include
			// fields when they are "leaf" outputs of methods/funcs as opposed to
			// objects used as parents between resolvers.
			doc.Definitions = append(doc.Definitions, objDef)
		}
	}

	for _, fn := range ts.Funcs {
		ret, err := goReflectTypeToGraphqlType(fn.returns[0].typ, false)
		if err != nil {
			return nil, err
		}
		fieldDef := &ast.FieldDefinition{
			Name:        lowerCamelCase(fn.name),
			Type:        ret,
			Description: fn.doc,
		}
		for _, arg := range fn.args {
			t, err := goReflectTypeToGraphqlType(arg.typ, true)
			if err != nil {
				return nil, err
			}
			argDef := &ast.ArgumentDefinition{
				Name: lowerCamelCase(arg.name),
				Type: t,
			}
			fieldDef.Arguments = append(fieldDef.Arguments, argDef)
		}
		queryExtension.Fields = append(queryExtension.Fields, fieldDef)
	}

	var b bytes.Buffer
	formatter.NewFormatter(&b).FormatSchemaDocument(doc)
	return b.Bytes(), nil
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
	args     []*goParam
	returns  []*goParam
	typ      reflect.Type
	val      reflect.Value
	receiver *goStruct // only set for methods
	doc      string
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
	client, err := Connect(ctx, WithLogOutput(os.Stderr))
	if err != nil {
		return nil, err
	}
	defer client.Close()

	name := fn.name
	var callArgs []reflect.Value
	if fn.receiver != nil {
		name = fmt.Sprintf("%s.%s", fn.receiver.typ.Name(), fn.name)
		parent, err := convertInput(rawParent, fn.receiver.typ, client)
		if err != nil {
			return nil, fmt.Errorf("unable to convert parent: %w", err)
		}
		callArgs = append(callArgs, reflect.ValueOf(parent))
	}
	callArgs = append(callArgs, reflect.ValueOf(Context{
		Context: ctx,
		client:  client,
	}))

	for _, arg := range fn.args {
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

	results := fn.val.Call(callArgs)
	if len(results) != 2 {
		return nil, fmt.Errorf("resolver %s.%s returned %d results, expected 2", name, name, len(results))
	}
	returnErr := results[1].Interface()
	if returnErr != nil {
		return nil, returnErr.(error)
	}
	return results[0].Interface(), nil
}

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

// TODO: update name of method, does much more now
func (ts *goTypes) fillParamNames(parsed *goast.File, fileSet *token.FileSet) {
	goast.Inspect(parsed, func(n goast.Node) bool {
		if n == nil {
			return false
		}
		switch f := n.(type) {
		case *goast.FuncDecl:
			if f.Recv == nil {
				ts.fillFuncParamNames(f, fileSet)
			} else {
				ts.fillMethodParamNames(f)
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

func (ts *goTypes) fillMethodParamNames(decl *goast.FuncDecl) {
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

func (ts *goTypes) fillFuncParamNames(decl *goast.FuncDecl, fileSet *token.FileSet) {
	// TODO: this probably doesn't work when funcs have duplicated names across packages

	// TODO: rename this to indicate it also fills in the real func name and docs in addition to param names

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
			result := typ.MethodByName(querybuilder.GraphQLMarshallerType).Call([]reflect.Value{})
			return ast.NonNullNamedType(result[0].String(), nil), nil
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
		// NOTE: I don't think this works if you try to do a selection on the object, but
		// that's fine for now since we don't support that yet with entrypoints
		return map[string]any{"id": id}, nil
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
		// For now, we can safely assume that this object was created by convertResult, so we
		// can just grab the "id" field. In the future when we expand beyond entrypoints, this
		// may need more generalization.
		inputMap, ok := input.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected input to be a map[string]any, got %+v", input)
		}
		idVal, ok := inputMap["id"]
		if !ok {
			return nil, fmt.Errorf("expected input to have an id field, got %+v", input)
		}
		id, ok := idVal.(string)
		if !ok {
			return nil, fmt.Errorf("expected id to be a string, got %+v", idVal)
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
