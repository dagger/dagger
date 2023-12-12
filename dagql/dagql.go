package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
)

type FieldSpec struct {
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
	Default Scalar
}

type Selector struct {
	Field string
	Args  map[string]Typed
	Nth   int
}

type Instantiator interface {
	ID(*idproto.ID) Typed
	Instantiate(*idproto.ID, Typed) (Selectable, error)
}

type Selectable interface {
	Node
	Select(context.Context, Selector) (Typed, error)
}

// Per the GraphQL spec, a Node always has an ID.
type Node interface {
	Typed
	ID() *idproto.ID
	Value() Typed
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

			argVal := reflect.New(field.Type).Elem().Interface()
			typedArg, ok := argVal.(Scalar)
			if !ok {
				return nil, fmt.Errorf("arg %q (%T) is not a Scalar", field.Name, argVal)
			}

			var argDefault Scalar
			defaultJSON := []byte(field.Tag.Get("default"))
			if len(defaultJSON) > 0 {
				dec := json.NewDecoder(bytes.NewReader(defaultJSON))
				dec.UseNumber()
				var val any
				if err := dec.Decode(&val); err != nil {
					return nil, fmt.Errorf("decode default value for arg %s: %w", argName, err)
				}
				var err error
				argDefault, err = typedArg.New(val)
				if err != nil {
					return nil, fmt.Errorf("convert default value for arg %s: %w", argName, err)
				}
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
		Func: func(ctx context.Context, self T, argVals map[string]Typed) (Typed, error) {
			var args A
			if err := setArgFields(argSpecs, argVals, &args); err != nil {
				return nil, err
			}
			return fn(ctx, self, args)
		},
	}
}

func setArgFields(argSpecs []ArgSpec, argVals map[string]Typed, dest any) error {
	destV := reflect.ValueOf(dest)
	for i, arg := range argSpecs {
		val, ok := argVals[arg.Name]
		if !ok {
			return fmt.Errorf("missing required argument: %q", arg.Name)
		}
		destV.Elem().Field(i).Set(reflect.ValueOf(val))
	}
	return nil
}

type Class[T Typed] struct {
	Fields Fields[T]
}

var _ Instantiator = Class[Typed]{}

func (cls Class[T]) ID(id *idproto.ID) Typed {
	return ID[T]{ID: id}
}

func (cls Class[T]) Instantiate(id *idproto.ID, val Typed) (Selectable, error) {
	if ided, ok := val.(Node); ok {
		// If the value is already a Node, preserve its ID.
		id = ided.ID()
		val = ided.Value()
	}
	self, ok := val.(T)
	if !ok {
		// NB: Nullable values should already be unwrapped by now.
		return nil, fmt.Errorf("cannot instantiate %T with %T", cls, val)
	}
	return Object[T]{
		Constructor: id,
		Self:        self,
		Class:       cls,
	}, nil
}

func (cls Class[T]) Call(ctx context.Context, node Object[T], fieldName string, args map[string]Typed) (Typed, error) {
	field, ok := cls.Fields[fieldName]
	if !ok {
		var zero T
		return nil, fmt.Errorf("%s has no such field: %q", zero.Type().Name(), fieldName)
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

func (o Object[T]) Value() Typed {
	return o.Self
}

type Fields[T Typed] map[string]Field[T]

type Field[T Typed] struct {
	Spec     FieldSpec
	Func     func(ctx context.Context, self T, args map[string]Typed) (Typed, error)
	NodeFunc func(ctx context.Context, self Node, args map[string]Typed) (Typed, error)
}

var _ Node = Object[Typed]{}

func (r Object[T]) ID() *idproto.ID {
	return r.Constructor
}

var _ Selectable = Object[Typed]{}

func (r Object[T]) Select(ctx context.Context, sel Selector) (res Typed, err error) {
	field, ok := r.Class.Fields[sel.Field]
	if !ok {
		var zero T
		return nil, fmt.Errorf("%s has no such field: %q", zero.Type().Name(), sel.Field)
	}
	defer func() {
		if err := recover(); err != nil {
			panic(fmt.Errorf("panic in %s.%s: %v", r.Type().Name(), sel.Field, err))
		}
	}()
	args, err := FieldArgs(field.Spec, sel.Args)
	if err != nil {
		return nil, err
	}
	return r.Class.Call(ctx, r, sel.Field, args)
}

func FieldArgs(field FieldSpec, givenArgs map[string]Typed) (map[string]Typed, error) {
	args := make(map[string]Typed, len(field.Args))
	for _, arg := range field.Args {
		val, ok := givenArgs[arg.Name]
		if ok {
			args[arg.Name] = val
		} else if arg.Default != nil {
			args[arg.Name] = arg.Default
		} else if arg.Type.NonNull {
			return nil, fmt.Errorf("missing required argument: %q", arg.Name)
		}
	}
	return args, nil
}
