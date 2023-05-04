package dagger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func Serve(entrypoints ...any) {
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
		if err := types.fillParamNames(parsed, fileSet); err != nil {
			writeErrorf(err)
		}
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
		Parent   json.RawMessage
		Args     json.RawMessage
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
	if ok {
		for _, m := range strukt.methods {
			if lowerCamelCase(m.name) == fieldName {
				fn = m
				break
			}
		}
	} else if objName == "Query" {
		for _, f := range types.Funcs {
			if lowerCamelCase(f.name) == fieldName {
				fn = f
				break
			}
		}
	} else {
		writeErrorf(fmt.Errorf("unknown struct: %s", objName))
	}

	if fn == nil {
		// trivial resolver
		var res any
		if input.Parent != nil {
			var parent map[string]any
			if err := json.Unmarshal(input.Parent, &parent); err != nil {
				writeErrorf(fmt.Errorf("unable to unmarshal parent: %w", err))
			}
			res = parent[fieldName]
		}
		if res == nil {
			res = make(map[string]any)
		}
		if err := writeResult(res); err != nil {
			writeErrorf(fmt.Errorf("unable to write result: %w", err))
		}
		return
	}

	res, err := fn.call(ctx, input.Parent, input.Args)
	if err != nil {
		writeErrorf(err)
	}
	if serializer, ok := res.(querybuilder.GraphQLMarshaller); ok {
		id, err := serializer.XXX_GraphQLID(ctx)
		if err != nil {
			writeErrorf(err)
			return
		}
		res = map[string]any{"id": id}
	}

	if err := writeResult(res); err != nil {
		writeErrorf(err)
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
		/* TODO:(sipsma)
		Huge hack: don't include any struct from the dagger go package, assume it is
		from the core API and thus doesn't need to be included in the schema here.
		The much cleaner approach will come when we integrate this code w/ the
		in-progress codegen work.
		*/
		if strings.HasPrefix(s.typ.PkgPath(), "dagger.io/dagger") {
			continue
		}

		if s.usedAsObject {
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
				// skip receiver+context
				for _, arg := range m.args[2:] {
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
			for _, f := range s.fields {
				t, err := goReflectTypeToGraphqlType(f.typ, false)
				if err != nil {
					return nil, err
				}
				fieldDef := &ast.FieldDefinition{
					Name: lowerCamelCase(f.name),
					Type: t,
				}
				objDef.Fields = append(objDef.Fields, fieldDef)
			}
			doc.Definitions = append(doc.Definitions, objDef)
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
		// skip context
		for _, arg := range fn.args[1:] {
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
	name         string
	typ          reflect.Type
	fields       []*goField
	methods      []*goFunc
	doc          string
	usedAsInput  bool
	usedAsObject bool
	topLevel     bool
}

type goField struct {
	name string
	typ  reflect.Type
}

type goFunc struct {
	name     string
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

func (f *goFunc) srcPathAndLine() (string, int) {
	pc := f.val.Pointer()
	fun := runtime.FuncForPC(pc)
	return fun.FileLine(pc)
}

func (fn *goFunc) call(ctx context.Context, rawParent, rawArgs json.RawMessage) (any, error) {
	name := fn.name
	var callArgs []reflect.Value
	if fn.receiver != nil {
		name = fmt.Sprintf("%s.%s", fn.receiver.typ.Name(), fn.name)
		parent := reflect.New(fn.receiver.typ).Interface()
		if err := json.Unmarshal(rawParent, parent); err != nil {
			return nil, fmt.Errorf("unable to unmarshal parent: %w", err)
		}
		callArgs = append(callArgs, reflect.ValueOf(parent).Elem())
	}
	client, err := Connect(ctx, WithLogOutput(os.Stderr))
	if err != nil {
		return nil, err
	}
	defer client.Close()
	callArgs = append(callArgs, reflect.ValueOf(Context{
		Context: ctx,
		client:  client,
	}))

	rawArgMap := map[string]json.RawMessage{}
	if err := json.Unmarshal(rawArgs, &rawArgMap); err != nil {
		return nil, fmt.Errorf("unable to unmarshal args: %w", err)
	}
	// skip base call args (receiver if method, plus context for both method and plain func)
	for _, arg := range fn.args[len(callArgs):] {
		argVal := reflect.New(arg.typ).Interface()
		rawArg, argValuePresent := rawArgMap[arg.name]
		var argIsOptional bool
		switch arg.typ.Kind() {
		case reflect.Ptr, reflect.Slice:
			argIsOptional = true
		}

		if argValuePresent {
			if err := json.Unmarshal(rawArg, argVal); err != nil {
				return nil, err
			}
		} else if !argIsOptional {
			return nil, fmt.Errorf("missing required argument %s", arg.name)
		}
		callArgs = append(callArgs, reflect.ValueOf(argVal).Elem())
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
	} else {
		strukt.usedAsObject = true
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			return fmt.Errorf("embedded fields not supported yet: %v", f)
		}
		if !f.IsExported() {
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
	if fn.receiver != nil {
		fn.args = append(fn.args, &goParam{
			typ: fn.receiver.typ,
		})
	}

	if len(fn.args) == fn.typ.NumIn() {
		return fmt.Errorf("func must take a least one arg of dagger.Context")
	}

	// verify next arg is a dagger.Context
	contextParam := fn.typ.In(len(fn.args))
	if contextParam != reflect.TypeOf((*Context)(nil)).Elem() {
		return fmt.Errorf("first arg of func must be dagger.Context: %s", contextParam.Kind())
	}

	// fill in rest of user defined args (if any)
	for i := len(fn.args); i < fn.typ.NumIn(); i++ {
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
func (ts *goTypes) fillParamNames(parsed *goast.File, fileSet *token.FileSet) (rerr error) {
	goast.Inspect(parsed, func(n goast.Node) bool {
		if n == nil {
			return false
		}
		switch f := n.(type) {
		case *goast.FuncDecl:
			if f.Recv == nil {
				rerr = errors.Join(rerr, ts.fillFuncParamNames(f, fileSet))
			} else {
				rerr = errors.Join(rerr, ts.fillMethodParamNames(f))
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
		// continue walking if no error
		return rerr == nil
	})
	return rerr
}

func (ts *goTypes) fillMethodParamNames(decl *goast.FuncDecl) error {
	if len(decl.Recv.List) != 1 {
		return nil
	}
	recvIdent, ok := decl.Recv.List[0].Type.(*goast.Ident)
	if !ok {
		return nil
	}
	recv, ok := ts.Structs[recvIdent.Name]
	if !ok {
		return nil
	}
	var fn *goFunc
	for _, m := range recv.methods {
		if m.name == decl.Name.Name {
			fn = m
			break
		}
	}
	if fn == nil {
		// TODO: this probably breaks when there's a private method on a struct, which we don't put in schema
		// just loosen error condition, check in caller that all methods are found
		return fmt.Errorf("method %v not found on %v", decl.Name.Name, recv.name)
	}
	fn.doc = decl.Doc.Text()

	argIndex := 1 // +1 because the ast doesn't include receiver here, but args does
	for _, param := range decl.Type.Params.List {
		// if the signature is like func(a, b string), then a and b are in the same Names slice
		for _, name := range param.Names {
			fn.args[argIndex].name = name.Name
			argIndex++
		}
	}

	return nil
}

func (ts *goTypes) fillFuncParamNames(decl *goast.FuncDecl, fileSet *token.FileSet) error {
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
		return nil
	}

	argIndex := 0
	for _, param := range decl.Type.Params.List {
		// if the signature is like func(a, b string), then a and b are in the same Names slice
		for _, name := range param.Names {
			fn.args[argIndex].name = name.Name
			argIndex++
		}
	}
	return nil
}

func writeResult(result interface{}) error {
	output, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("unable to marshal response: %v", err)
	}
	var mapRes any
	if err := json.Unmarshal(output, &mapRes); err != nil {
		return fmt.Errorf("unable to unmarshal response: %v", err)
	}
	lowerCaseResult(mapRes)
	output, err = json.Marshal(mapRes)
	if err != nil {
		return fmt.Errorf("unable to marshal response: %v", err)
	}
	if err := os.WriteFile("/outputs/dagger.json", output, 0600); err != nil {
		return fmt.Errorf("unable to write response file: %v", err)
	}
	return nil
}

func writeErrorf(err error) {
	fmt.Println(err.Error())
	os.Exit(1)
}

func init() {
	// TODO:(sipsma) silly hack, is there a pre-made list of these somewhere?
	// Or can we have a rule that "ALLCAPS" becomes "allcaps" instead of aLLCAPS?
	strcase.ConfigureAcronym("URL", "url")
	strcase.ConfigureAcronym("CI", "ci")
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

func lowerCaseResult(x any) {
	switch x := x.(type) {
	case map[string]any:
		for k, v := range x {
			lowerCaseResult(v)
			newK := lowerCamelCase(k)
			x[newK] = v
			if newK != k {
				delete(x, k)
			}
		}
	default:
	}
}
