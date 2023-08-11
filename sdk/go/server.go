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

	"dagger.io/dagger/querybuilder"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
)

var getSchema bool

func init() {
	flag.BoolVar(&getSchema, "schema", false, "print the schema rather than executing")
}

var (
	daggerContextT = reflect.TypeOf((*Context)(nil)).Elem()
	errorT         = reflect.TypeOf((*error)(nil)).Elem()
	marshallerT    = reflect.TypeOf((*querybuilder.GraphQLMarshaller)(nil)).Elem()
)

type Context struct {
	context.Context
	client *Client
}

func (c Context) Client() *Client {
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

func DefaultClient() *Client {
	return DefaultContext().Client()
}

// TODO: this is obviously dumb, but can be cleaned up nicely w/ codegen changes
var resolvers = map[string]*goFunc{}

func (r *Environment) Serve() {
	ctx := DefaultContext()
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
	_, fieldName, ok := strings.Cut(input.Resolver, ".")
	if !ok {
		writeErrorf(fmt.Errorf("invalid resolver name: %s", input.Resolver))
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

	if fn.resultConverter == nil {
		fn.resultConverter = convertResult
	}
	result, err = fn.resultConverter(ctx, result)
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
	hasReceiver     bool
	doc             string
	resultConverter func(context.Context, any) (any, error)
}

type goParam struct {
	name     string
	doc      string
	typ      reflect.Type
	optional bool
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
		if arg.typ == daggerContextT {
			callArgs = append(callArgs, reflect.ValueOf(Context{
				Context: ctx,
				client:  client,
			}))
			continue
		}

		rawArg, argValuePresent := rawArgs[arg.name]

		// support FooOpts structs
		if arg.typ.Kind() == reflect.Struct {
			opts := reflect.New(arg.typ)
			for i := 0; i < arg.typ.NumField(); i++ {
				field := arg.typ.Field(i)
				rawArg, optPresent := rawArgs[strcase.ToLowerCamel(field.Name)]
				if optPresent {
					optVal, err := convertInput(rawArg, field.Type, client)
					if err != nil {
						return nil, fmt.Errorf("unable to convert arg %s: %w", arg.name, err)
					}
					opts.Elem().Field(i).Set(reflect.ValueOf(optVal))
				}
			}
			callArgs = append(callArgs, opts.Elem())
			continue
		}

		switch {
		case argValuePresent:
			argVal, err := convertInput(rawArg, arg.typ, client)
			if err != nil {
				return nil, fmt.Errorf("unable to convert arg %s: %w", arg.name, err)
			}
			callArgs = append(callArgs, reflect.ValueOf(argVal))
		case !arg.optional:
			return nil, fmt.Errorf("missing required argument %s", arg.name)
		default:
			callArgs = append(callArgs, reflect.New(arg.typ).Elem())
		}
	}

	reflectOutputs := fn.val.Call(callArgs)
	var returnVal any
	var returnErr error
	for _, output := range reflectOutputs {
		if output.Type() == errorT {
			if !output.IsNil() {
				returnErr = output.Interface().(error)
			}
		} else {
			returnVal = output.Interface()
		}
	}
	return returnVal, returnErr
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
	// Handle types that implement the GraphQL serializer
	if t.Implements(marshallerT) {
		typ := reflect.New(t.Elem())
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

	switch t.Kind() {
	case reflect.String:
		// /* TODO:(sipsma)
		// Huge hack: handle any scalar type from the go SDK (i.e. DirectoryID/ContainerID)
		// The much cleaner approach will come when we integrate this code w/ the
		// in-progress codegen work.
		// */
		// if strings.HasPrefix(t.PkgPath(), "dagger.io/dagger") {
		// 	return ast.NonNullNamedType(t.Name(), nil), nil
		// }
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
		return ast.NonNullListType(elementType, nil), nil
	case reflect.Struct:
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

	// TODO: hack, could give CheckResult an ID? Or maybe just need to support "data-only" object fields or something
	if checkRes, ok := result.(*EnvironmentCheckResult); ok {
		success, err := checkRes.Success(ctx)
		if err != nil {
			return nil, err
		}
		output, err := checkRes.Output(ctx)
		if err != nil {
			return nil, err
		}
		subresults, err := checkRes.Subresults(ctx)
		if err != nil {
			return nil, err
		}
		// TODO: getting bitten again by bug in go sdk around returned list of objects being empty...
		// TODO: As a result, also not handling arbitrary nesting of subresults yet, just one level
		var actualSubresults []map[string]any
		for _, subresult := range subresults {
			success, err := subresult.Success(ctx)
			if err != nil {
				return nil, err
			}
			output, err := subresult.Output(ctx)
			if err != nil {
				return nil, err
			}
			name, err := subresult.Name(ctx)
			if err != nil {
				return nil, err
			}
			actualSubresults = append(actualSubresults, map[string]any{
				"success": success,
				"output":  output,
				"name":    name,
			})
		}
		return map[string]any{
			"success":    success,
			"output":     output,
			"subresults": actualSubresults,
		}, nil
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
	if desiredType.Implements(marshallerT) {
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
				Q:  client.Q,
				C:  client.C,
				id: &cacheID,
			}, nil
		default:
			return nil, fmt.Errorf("unhandled GraphQL marshaller type %s", graphqlType)
		}
	}

	// recurse
	inputObj := reflect.ValueOf(input)
	switch desiredType.Kind() {
	case reflect.Pointer:
		x, err := convertInput(inputObj.Interface(), desiredType.Elem(), client)
		if err != nil {
			return nil, err
		}
		ptr := reflect.New(desiredType.Elem())
		ptr.Elem().Set(reflect.ValueOf(x))
		return ptr.Interface(), nil
	case reflect.Slice:
		returnObj := reflect.MakeSlice(desiredType, inputObj.Len(), inputObj.Len())
		for i := 0; i < inputObj.Len(); i++ {
			value := inputObj.Index(i).Interface()
			convertedValue, err := convertInput(value, desiredType.Elem(), client)
			if err != nil {
				return nil, err
			}
			returnObj.Index(i).Set(reflect.ValueOf(convertedValue))
		}
		return returnObj.Interface(), nil
	case reflect.Struct:
		returnObj := reflect.New(desiredType).Elem()
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
			returnObj.Field(i).Set(reflect.ValueOf(convertedField))
		}
		return returnObj.Interface(), nil
	case reflect.Map:
		returnObj := reflect.MakeMap(desiredType)
		for _, key := range inputObj.MapKeys() {
			value := inputObj.MapIndex(key).Interface()
			convertedValue, err := convertInput(value, desiredType.Elem(), client)
			if err != nil {
				return nil, err
			}
			inputObj.SetMapIndex(key, reflect.ValueOf(convertedValue))
		}
		return returnObj.Interface(), nil
	default:
		return input, nil
	}
}
