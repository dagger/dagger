package dagql

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
)

type FieldSpec struct {
	// Name is the name of the field.
	Name string
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

func (sel Selector) AppendToID(id *idproto.ID, field *ast.FieldDefinition) *idproto.ID {
	cp := id.Clone()
	idArgs := make([]*idproto.Argument, 0, len(sel.Args))
	for name, val := range sel.Args {
		idArgs = append(idArgs, &idproto.Argument{
			Name:  name,
			Value: ToLiteral(val),
		})
	}
	sort.Slice(idArgs, func(i, j int) bool {
		return idArgs[i].Name < idArgs[j].Name
	})
	cp.Constructor = append(cp.Constructor, &idproto.Selector{
		Field:   sel.Field,
		Args:    idArgs,
		Tainted: field.Directives.ForName("tainted") != nil, // TODO
		Meta:    field.Directives.ForName("meta") != nil,    // TODO
	})
	cp.TypeName = field.Type.Name()
	return cp
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
			if defaultVal := field.Tag.Get("default"); len(defaultVal) > 0 {
				var err error
				argDefault, err = typedArg.ScalarType().New(defaultVal)
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
func Func[T Typed, A any, R Typed](name string, fn func(ctx context.Context, self T, args A) (R, error)) Field[T] {
	var zeroArgs A
	argSpecs, argsErr := ArgSpecs(zeroArgs)

	var zeroRet R
	return Field[T]{
		Spec: FieldSpec{
			Name: name,
			Args: argSpecs,
			Type: zeroRet.Type(),
		},
		Func: func(ctx context.Context, self T, argVals map[string]Typed) (Typed, error) {
			if argsErr != nil {
				// this error is deferred until runtime, since it's better (at least
				// more testable) than panicking
				return nil, argsErr
			}
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
	Fields map[string]*Field[T]
}

func NewClass[T Typed]() Class[T] {
	return Class[T]{
		Fields: map[string]*Field[T]{},
	}
}

var _ ObjectType = Class[Typed]{}

type Descriptive interface {
	Description() string
}

type Definitive interface {
	Definition() *ast.Definition
}

func (cls Class[T]) Definition() *ast.Definition {
	var zero T
	name := zero.Type().Name()
	var def *ast.Definition
	if isType, ok := any(zero).(Definitive); ok {
		def = isType.Definition()
	} else {
		def = &ast.Definition{
			Kind: ast.Object,
			Name: name,
		}
	}
	if isType, ok := any(zero).(Descriptive); ok {
		def.Description = isType.Description()
	}
	for _, field := range cls.Fields { // TODO sort, or preserve order
		def.Fields = append(def.Fields, field.Definition())
	}
	return def
}

func (cls Class[T]) FieldDefinition(name string) (*ast.FieldDefinition, bool) {
	field, found := cls.Fields[name]
	if !found {
		return nil, false
	}
	return field.Definition(), true
}

func (cls Class[T]) NewID(id *idproto.ID) Typed {
	return ID[T]{ID: id}
}

func (cls Class[T]) New(id *idproto.ID, val Typed) (Object, error) {
	self, ok := val.(T)
	if !ok {
		// NB: Nullable values should already be unwrapped by now.
		return nil, fmt.Errorf("cannot instantiate %T with %T", cls, val)
	}
	return Instance[T]{
		Constructor: id,
		Self:        self,
		Class:       cls,
	}, nil
}

func (cls Class[T]) Call(ctx context.Context, node Instance[T], fieldName string, args map[string]Typed) (Typed, error) {
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

type Instance[T Typed] struct {
	Constructor *idproto.ID
	Self        T
	Class       Class[T]
}

var _ Object = Instance[Typed]{}

func (o Instance[T]) Type() *ast.Type {
	return o.Self.Type()
}

type Fields[T Typed] []Field[T]

func (fields Fields[T]) findOrInitializeType(server *Server, typeName string) Class[T] {
	var classT Class[T]
	class, ok := server.classes[typeName]
	if !ok {
		classT = NewClass[T]()
		server.classes[typeName] = classT

		// TODO: better way to avoid registering IDs for schema introspection
		// builtins
		if !strings.HasPrefix(typeName, "__") {
			idScalar := ID[T]{}
			server.scalars[idScalar.TypeName()] = idScalar
			Field[T]{
				Spec: FieldSpec{
					Name: "id",
					Type: &ast.Type{
						NamedType: idScalar.TypeName(),
						NonNull:   true,
					},
				},
				// TODO there might be a better way to do this. maybe just pass Object[T] to the function?
				NodeFunc: func(ctx context.Context, self Instance[T], args map[string]Typed) (Typed, error) {
					return ID[T]{ID: self.ID()}, nil
				},
			}.Install(classT)
		}
	} else {
		classT = class.(Class[T])
	}
	return classT
}

func (fields Fields[T]) Install(server *Server) {
	var t T
	typeName := t.Type().Name()
	class := fields.findOrInitializeType(server, typeName)
	for _, field := range fields {
		field := field
		class.Fields[field.Spec.Name] = &field
	}
}

type Field[T Typed] struct {
	Spec     FieldSpec
	Func     func(ctx context.Context, self T, args map[string]Typed) (Typed, error)
	NodeFunc func(ctx context.Context, self Instance[T], args map[string]Typed) (Typed, error)
}

func (field Field[T]) Install(class Class[T]) {
	class.Fields[field.Spec.Name] = &field
}

func (field Field[T]) Definition() *ast.FieldDefinition {
	fieldName := field.Spec.Name

	schemaArgs := ast.ArgumentDefinitionList{}
	for _, arg := range field.Spec.Args {
		schemaArg := &ast.ArgumentDefinition{
			Name: arg.Name,
			Type: arg.Type,
		}
		if arg.Default != nil {
			schemaArg.DefaultValue = LiteralToAST(arg.Default.Literal())
		}
		schemaArgs = append(schemaArgs, schemaArg)
	}

	return &ast.FieldDefinition{
		Name: fieldName,
		// Description  string
		Arguments: schemaArgs,
		// DefaultValue *Value                 // only for input objects
		Type: field.Spec.Type,
		// Directives   DirectiveList
	}
}

var _ Object = Instance[Typed]{}

func (r Instance[T]) ID() *idproto.ID {
	return r.Constructor
}

func (r Instance[T]) Select(ctx context.Context, sel Selector) (res Typed, err error) {
	field, ok := r.Class.Fields[sel.Field]
	if !ok {
		var zero T
		return nil, fmt.Errorf("%s has no such field: %q", zero.Type().Name(), sel.Field)
	}
	args, err := applyDefaults(field.Spec, sel.Args)
	if err != nil {
		return nil, err
	}
	return r.Class.Call(ctx, r, sel.Field, args)
}

func applyDefaults(field FieldSpec, givenArgs map[string]Typed) (map[string]Typed, error) {
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
