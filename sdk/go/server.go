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

func Serve(server any) {
	ctx := context.Background()

	types := &goTypes{
		Structs: make(map[string]*goStruct),
	}
	types.walk(reflect.TypeOf(server), walkState{})
	srcFiles := make(map[string]struct{})
	for _, s := range types.Structs {
		for _, m := range s.methods {
			srcFiles[m.srcPath()] = struct{}{}
		}
	}
	for srcFile := range srcFiles {
		parsed, err := parser.ParseFile(token.NewFileSet(), srcFile, nil, 0)
		if err != nil {
			panic(err)
		}
		types.fillParamNames(parsed)
	}

	// if the schema is being requested, just return that
	var getSchema bool
	flag.BoolVar(&getSchema, "schema", false, "print the schema rather than executing")
	flag.Parse()

	if getSchema {
		schema := types.schema()
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

	var method *goMethod
	strukt, ok := types.Structs[objName]
	if !ok && objName != "Query" {
		writeErrorf(fmt.Errorf("unknown struct: %s", objName))
	}
	if ok {
		for _, m := range strukt.methods {
			if lowerCamelCase(m.name) == fieldName {
				method = m
				break
			}
		}
	}
	if method == nil {
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

	res, err := method.call(ctx, input.Parent, input.Args)
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
}

func (ts *goTypes) schema() []byte {
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
					Name: lowerCamelCase(s.typ.Name()),
					Type: ast.NonNullNamedType(s.typ.Name(), nil),
				})
			}
			objDef := &ast.Definition{
				Kind: ast.Object,
				Name: s.typ.Name(),
			}
			for _, m := range s.methods {
				ret := goReflectTypeToGraphqlType(m.returns[0].typ, false)
				fieldDef := &ast.FieldDefinition{
					Name: lowerCamelCase(m.name),
					Type: ret,
				}
				// skip receiver+context
				for _, arg := range m.args[2:] {
					argDef := &ast.ArgumentDefinition{
						Name: lowerCamelCase(arg.name),
						Type: goReflectTypeToGraphqlType(arg.typ, true),
					}
					fieldDef.Arguments = append(fieldDef.Arguments, argDef)
				}
				objDef.Fields = append(objDef.Fields, fieldDef)
			}
			for _, f := range s.fields {
				fieldDef := &ast.FieldDefinition{
					Name: lowerCamelCase(f.name),
					Type: goReflectTypeToGraphqlType(f.typ, false),
				}
				objDef.Fields = append(objDef.Fields, fieldDef)
			}
			doc.Definitions = append(doc.Definitions, objDef)
		}
		if s.usedAsInput {
			inputDef := &ast.Definition{
				Kind: ast.InputObject,
				Name: inputName(s.typ.Name()),
			}
			// TODO:(sipsma) just ignoring methods for now, maybe should be error or something else
			for _, f := range s.fields {
				fieldDef := &ast.FieldDefinition{
					Name: lowerCamelCase(f.name),
					Type: goReflectTypeToGraphqlType(f.typ, true),
				}
				inputDef.Fields = append(inputDef.Fields, fieldDef)
			}
			doc.Definitions = append(doc.Definitions, inputDef)
		}
	}

	var b bytes.Buffer
	formatter.NewFormatter(&b).FormatSchemaDocument(doc)
	return b.Bytes()
}

type goStruct struct {
	name         string
	typ          reflect.Type
	fields       []*goField
	methods      []*goMethod
	usedAsInput  bool
	usedAsObject bool
	topLevel     bool
}

type goField struct {
	name string
	typ  reflect.Type
}

type goMethod struct {
	name    string
	args    []*goParam
	returns []*goParam
	typ     reflect.Type
	val     reflect.Value
	parent  *goStruct
}

type goParam struct {
	name string
	typ  reflect.Type
}

func (m *goMethod) srcPath() string {
	pc := m.val.Pointer()
	fun := runtime.FuncForPC(pc)
	srcPath, _ := fun.FileLine(pc)
	return srcPath
}

func (m *goMethod) call(ctx context.Context, rawParent, rawArgs json.RawMessage) (any, error) {
	parent := reflect.New(m.parent.typ).Interface()
	if err := json.Unmarshal(rawParent, parent); err != nil {
		panic(err)
	}

	callArgs := []reflect.Value{
		reflect.ValueOf(parent).Elem(),
		reflect.ValueOf(ctx),
	}

	rawArgMap := map[string]json.RawMessage{}
	if err := json.Unmarshal(rawArgs, &rawArgMap); err != nil {
		panic(err)
	}
	// skip receiver+context
	for _, arg := range m.args[2:] {
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

	results := m.val.Call(callArgs)
	if len(results) != 2 {
		return nil, fmt.Errorf("resolver %s.%s returned %d results, expected 2", m.parent.typ.Name(), m.name, len(results))
	}
	err := results[1].Interface()
	if err != nil {
		return nil, err.(error)
	}
	return results[0].Interface(), nil
}

type walkState struct {
	inputPath   bool // are we following a type path from an argument?
	isSubObject bool // false if this is an object directly provided to Serve, true otherwise
}

// TODO:(sipsma) support more "root types" like map[string]any, plain func, etc. Should support "dynamic" schemas
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

func (ts *goTypes) walkStruct(t reflect.Type, state walkState) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Errorf("unexpected type: %v", t))
	}

	strukt, ok := ts.Structs[t.Name()]
	if !ok {
		strukt = &goStruct{
			name: t.Name(),
			typ:  t,
		}
		ts.Structs[t.Name()] = strukt
	}
	if !state.isSubObject {
		strukt.topLevel = true
	}

	// TODO:(sipsma) dedupe if already walked (currently walk every possible path in type DAG)
	if state.inputPath {
		strukt.usedAsInput = true
	} else {
		strukt.usedAsObject = true
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			panic(fmt.Errorf("embedded fields not supported yet: %v", f))
		}
		if !f.IsExported() {
			continue
		}
		if len(strukt.fields) <= i {
			expanded := make([]*goField, i+1)
			copy(expanded, strukt.fields)
			strukt.fields = expanded
			strukt.fields[i] = &goField{
				name: f.Name,
			}
		}
		strukt.fields[i].typ = f.Type
		ts.walk(f.Type, walkState{
			inputPath:   state.inputPath,
			isSubObject: true,
		})
	}
	// TODO:(sipsma) should we skip methods for input types? error out? something else?
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		if m.PkgPath != "" {
			// skip unexported methods
			continue
		}
		if len(strukt.methods) <= i {
			expanded := make([]*goMethod, i+1)
			copy(expanded, strukt.methods)
			strukt.methods = expanded
			strukt.methods[i] = &goMethod{
				name:   m.Name,
				parent: strukt,
			}
		}
		strukt.methods[i].typ = m.Type
		strukt.methods[i].val = m.Func

		// (skip first two args for receiver and context)
		// TODO:(sipsma) validate the above (right context type, etc)
		for j := 2; j < m.Type.NumIn(); j++ {
			if len(strukt.methods[i].args) <= j {
				expanded := make([]*goParam, j+1)
				copy(expanded, strukt.methods[i].args)
				strukt.methods[i].args = expanded
				strukt.methods[i].args[j] = &goParam{}
			}
			strukt.methods[i].args[j].typ = m.Type.In(j)
			ts.walk(m.Type.In(j), walkState{
				inputPath:   true,
				isSubObject: true,
			})
		}
		for j := 0; j < m.Type.NumOut(); j++ {
			// TODO:(sipsma) only support (type, error) probably
			if len(strukt.methods[i].returns) <= j {
				expanded := make([]*goParam, j+1)
				copy(expanded, strukt.methods[i].returns)
				strukt.methods[i].returns = expanded
				strukt.methods[i].returns[j] = &goParam{}
			}
			strukt.methods[i].returns[j].typ = m.Type.Out(j)
			ts.walk(m.Type.Out(j), walkState{
				inputPath:   state.inputPath,
				isSubObject: true,
			})
		}
	}
}

func (ts *goTypes) fillParamNames(parsed *goast.File) {
	goast.Inspect(parsed, func(n goast.Node) bool {
		if n == nil {
			return false
		}
		switch f := n.(type) {
		case *goast.FuncDecl:
			if f.Recv == nil {
				return true
			}
			if len(f.Recv.List) != 1 {
				return true
			}
			recvIdent, ok := f.Recv.List[0].Type.(*goast.Ident)
			if !ok {
				return true
			}
			recv, ok := ts.Structs[recvIdent.Name]
			if !ok {
				return true
			}
			var method *goMethod
			for _, m := range recv.methods {
				if m.name == f.Name.Name {
					method = m
					break
				}
			}
			if method == nil {
				panic(fmt.Errorf("method %v not found on %v", f.Name.Name, recv.name))
			}
			for i, param := range f.Type.Params.List {
				// (skip first arg for context)
				if i == 0 {
					continue
				}
				if len(param.Names) == 0 {
					continue
				}
				method.args[i+1].name = param.Names[0].Name
			}
		default:
		}
		return true
	})
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
	// TODO:(sipsma) silly hack to get netlify models to have better names
	// need more general solution
	strcase.ConfigureAcronym("URL", "url")
}

func lowerCamelCase(s string) string {
	return strcase.ToLowerCamel(s)
}

func inputName(name string) string {
	return name + "Input"
}

func goReflectTypeToGraphqlType(t reflect.Type, isInput bool) *ast.Type {
	switch t.Kind() {
	case reflect.String:
		/* TODO:(sipsma)
		Huge hack: handle any scalar type from the go SDK (i.e. DirectoryID/ContainerID)
		The much cleaner approach will come when we integrate this code w/ the
		in-progress codegen work.
		*/
		if strings.HasPrefix(t.PkgPath(), "dagger.io/dagger") {
			return ast.NonNullNamedType(t.Name(), nil)
		}
		return ast.NonNullNamedType("String", nil)
	case reflect.Int:
		return ast.NonNullNamedType("Int", nil)
	case reflect.Float32, reflect.Float64:
		// TODO:(sipsma) does this actually handle both float32 and float64?
		return ast.NonNullNamedType("Float", nil)
	case reflect.Bool:
		return ast.NonNullNamedType("Boolean", nil)
	case reflect.Slice:
		return ast.ListType(goReflectTypeToGraphqlType(t.Elem(), isInput), nil)
	case reflect.Struct:
		// Handle types that implement the GraphQL serializer
		// TODO: move this at the top so it works on scalars as well
		marshaller := reflect.TypeOf((*querybuilder.GraphQLMarshaller)(nil)).Elem()
		if t.Implements(marshaller) {
			typ := reflect.New(t)
			result := typ.MethodByName(querybuilder.GraphQLMarshallerType).Call([]reflect.Value{})
			return ast.NonNullNamedType(result[0].String(), nil)
		}

		if isInput {
			return ast.NonNullNamedType(inputName(t.Name()), nil)
		}
		return ast.NonNullNamedType(t.Name(), nil) // TODO:(sipsma) doesn't handle anything from another package (besides the sdk)
	case reflect.Pointer:
		nonNullType := goReflectTypeToGraphqlType(t.Elem(), isInput)
		nonNullType.NonNull = false
		return nonNullType
	default:
		panic(fmt.Errorf("unsupported type %s", t.Kind()))
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
