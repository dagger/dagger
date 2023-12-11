package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/dagger/dagql/idproto"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
)

type Resolver interface {
	Node
	Resolve(context.Context, *ast.FieldDefinition, map[string]Literal) (Typed, error)
}

type FieldSpec struct {
	// Name is the name of the field.
	// Name string

	// Args is the list of arguments that the field accepts.
	Args []ArgSpec

	// Type is the type of the field's result.
	Type *ast.Type

	// Meta indicates that the field has no impact on the field's result.
	Meta bool

	// Pure indicates that the field is a pure function of its arguments, and
	// thus can be cached indefinitely.
	Pure bool
}

type ArgSpec struct {
	// Name is the name of the argument.
	Name string
	// Type is the type of the argument.
	Type *ast.Type
	// Default is the default value of the argument.
	Default *Literal
}

type Literal struct {
	*idproto.Literal
}

func (lit Literal) ToAST() *ast.Value {
	switch x := lit.Value.(type) {
	case *idproto.Literal_Int:
		return &ast.Value{
			Kind: ast.IntValue,
			Raw:  fmt.Sprintf("%d", lit.GetInt()),
		}
	case *idproto.Literal_String_:
		return &ast.Value{
			Kind: ast.IntValue,
			Raw:  fmt.Sprintf("%d", lit.GetInt()),
		}
	default:
		panic(fmt.Errorf("cannot convert %T to *ast.Value", x))
	}
}

type Selector struct {
	Field string
	Args  map[string]Literal
	Nth   int
}

type Instantiator interface {
	Instantiate(*idproto.ID, Typed) (Resolver, error)
}

// Per the GraphQL spec, a Node always has an ID.
type Node interface {
	Typed
	ID() *idproto.ID
}

type TypeResolver interface {
	isType()
}

func ArgSpecs(args any) ([]ArgSpec, error) {
	var argSpecs []ArgSpec
	argsType := reflect.TypeOf(args)
	if argsType != nil {
		if argsType.Kind() != reflect.Struct {
			return nil, fmt.Errorf("args must be a struct, got %T", args)
		}

		for i := 0; i < argsType.NumField(); i++ {
			field := argsType.Field(i)
			argName := field.Tag.Get("arg")
			if argName == "" {
				argName = strcase.ToLowerCamel(field.Name)
			}

			var argDefault *Literal
			defaultJSON := []byte(field.Tag.Get("default")) // TODO: would make more sense to GraphQL-Unmarshal this
			if len(defaultJSON) > 0 {
				dec := json.NewDecoder(bytes.NewReader(defaultJSON))
				dec.UseNumber()

				var defaultAny any
				if err := dec.Decode(&defaultAny); err != nil {
					return nil, fmt.Errorf("decode default value for arg %s: %w", argName, err)
				}

				argDefault = &Literal{idproto.LiteralValue(defaultAny)}
			}

			argVal := reflect.New(field.Type).Interface()
			typedArg, ok := argVal.(Typed)
			if !ok {
				return nil, fmt.Errorf("arg %s must be a dagql.Typed, got %T", argName, argVal)
			}

			argSpecs = append(argSpecs, ArgSpec{
				Name:    argName,
				Type:    typedArg.Type(),
				Default: argDefault,
			})
		}
	}
	return argSpecs, nil
}

func ArgsToType(argSpecs []ArgSpec, argVals map[string]Literal, dest any) error {
	argsVal := reflect.ValueOf(dest)

	for i, arg := range argSpecs {
		argVal, ok := argVals[arg.Name]
		if !ok {
			return fmt.Errorf("missing required argument: %q", arg.Name)
		}

		field := argsVal.Elem().Field(i)
		arg := reflect.New(field.Type()).Interface()
		if um, ok := arg.(Unmarshaler); ok {
			if err := um.UnmarshalLiteral(argVal.Literal); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("cannot unmarshal %T", arg)
		}

		field.Set(reflect.ValueOf(arg).Elem())
	}

	return nil
}

// Func is a helper for statically defining fields. It panics if anything goes
// wrong, as dev-time errors are preferable to runtime errors, so it shouldn't
// be used for anything dynamic.
func Func[T Typed, A any, R Typed](fn func(ctx context.Context, self T, args A) (R, error)) Field[T] {
	var zeroArgs A
	argSpecs, err := ArgSpecs(zeroArgs)
	if err != nil {
		panic(err)
	}

	var zeroRet R
	return Field[T]{
		Spec: FieldSpec{
			Args: argSpecs,
			Type: zeroRet.Type(),
		},
		Func: func(ctx context.Context, self T, argVals map[string]Literal) (Typed, error) {
			var args A
			if err := ArgsToType(argSpecs, argVals, &args); err != nil {
				return nil, err
			}
			return fn(ctx, self, args)
		},
	}
}

type Marshaler interface {
	MarshalLiteral() (*idproto.Literal, error)
}

type Unmarshaler interface {
	UnmarshalLiteral(*idproto.Literal) error
}

type ScalarResolver[T Marshaler] struct{}

func (ScalarResolver[T]) isType() {}

type Class[T Typed] struct {
	Fields Fields[T]
}

func (r Class[T]) isType() {}

var _ Instantiator = Class[Typed]{}

func (cls Class[T]) Instantiate(id *idproto.ID, val Typed) (Resolver, error) {
	self, ok := val.(T)
	if !ok {
		return nil, fmt.Errorf("cannot instantiate %T with %T", cls, val)
	}
	return Object[T]{
		Constructor: id,
		Self:        self,
		Class:       cls,
	}, nil
}

func (cls Class[T]) Call(ctx context.Context, node Object[T], fieldName string, args map[string]Literal) (Typed, error) {
	field, ok := cls.Fields[fieldName]
	if !ok {
		return nil, fmt.Errorf("no such field: %q", fieldName)
	}
	if field.NodeFunc != nil {
		return field.NodeFunc(ctx, node, args)
	}
	return field.Func(ctx, node.Self, args)
}

type Object[T Typed] struct {
	Constructor *idproto.ID
	Self        T
	Class       Class[T]
}

var _ Node = Object[Typed]{}

func (o Object[T]) Type() *ast.Type {
	return o.Self.Type()
}

type Fields[T Typed] map[string]Field[T]

type Field[T any] struct {
	Spec     FieldSpec
	Func     func(ctx context.Context, self T, args map[string]Literal) (Typed, error)
	NodeFunc func(ctx context.Context, self Node, args map[string]Literal) (Typed, error)
}

var _ Node = Object[Typed]{}

func (r Object[T]) ID() *idproto.ID {
	return r.Constructor
}

var _ Resolver = Object[Typed]{}

func (r Object[T]) Resolve(ctx context.Context, field *ast.FieldDefinition, givenArgs map[string]Literal) (Typed, error) {
	args := make(map[string]Literal, len(field.Arguments))
	for _, arg := range field.Arguments {
		val, ok := givenArgs[arg.Name]
		if ok {
			args[arg.Name] = val
		} else {
			if arg.DefaultValue != nil {
				val, err := arg.DefaultValue.Value(nil)
				if err != nil {
					return nil, err
				}
				args[arg.Name] = Literal{idproto.LiteralValue(val)}
			} else if arg.Type.NonNull {
				return nil, fmt.Errorf("missing required argument: %q", arg.Name)
			}
		}
	}
	return r.Class.Call(ctx, r, field.Name, args)
}
