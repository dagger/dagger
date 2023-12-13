package dagql

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
)

// Func is a helper for defining a field resolver and schema.
//
// The function must accept a context.Context, the receiver, and a struct of
// arguments. All fields of the arguments struct must be Typed so that the
// schema may be derived, and Scalar to ensure a default value may be provided.
//
// Arguments use struct tags to further configure the schema:
//
//   - `arg:"bar"` sets the name of the argument. By default this is the
//     toLowerCamel'd field name.
//   - `default:"foo"` sets the default value of the argument. The Scalar type
//     determines how this value is parsed.
//   - `doc:"..."` sets the description of the argument.
//
// The function must return a Typed value, and an error.
//
// To configure a description for the field in the schema, call .Doc on the
// result.
func Func[T Typed, A any, R Typed](name string, fn func(ctx context.Context, self T, args A) (R, error)) Field[T] {
	var zeroArgs A
	argSpecs, argsErr := argSpecs(zeroArgs)

	var zeroRet R
	return Field[T]{
		Spec: FieldSpec{
			Name: name,
			Args: argSpecs,
			Type: zeroRet.Type(),
		},
		Func: func(ctx context.Context, self Instance[T], argVals map[string]Typed) (Typed, error) {
			if argsErr != nil {
				// this error is deferred until runtime, since it's better (at least
				// more testable) than panicking
				return nil, argsErr
			}
			var args A
			if err := setArgFields(argSpecs, argVals, &args); err != nil {
				return nil, err
			}
			return fn(ctx, self.Self, args)
		},
	}
}

// FieldSpec is a specification for a field.
type FieldSpec struct {
	// Name is the name of the field.
	Name string
	// Description is the description of the field.
	Description string
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

// ArgSpec is a specification for an argument to a field.
type ArgSpec struct {
	// Name is the name of the argument.
	Name string
	// Description is the description of the argument.
	Description string
	// Type is the type of the argument.
	Type *ast.Type
	// Default is the default value of the argument.
	Default Scalar
}

func argSpecs(args any) ([]ArgSpec, error) {
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
				Name:        argName,
				Description: field.Tag.Get("doc"),
				Type:        typedArg.Type(),
				Default:     argDefault,
			})
		}
	}
	return argSpecs, nil
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

// Descriptive is an interface for types that have a description.
//
// The description is used in the schema. To provide a full definition,
// implement Definitive instead.
type Descriptive interface {
	Description() string
}

// Definitive is a type that knows how to define itself in the schema.
type Definitive interface {
	Definition() *ast.Definition
}

// Class is a class of Object types.
//
// The class is defined by a set of fields, which are installed into the class
// dynamically at runtime.
type Class[T Typed] struct {
	Fields map[string]*Field[T]
}

// NewClass returns a new empty class for a given type.
func NewClass[T Typed]() Class[T] {
	return Class[T]{
		Fields: map[string]*Field[T]{},
	}
}

var _ ObjectType = Class[Typed]{}

// Definition returns the schema definition of the class.
//
// The definition is derived from the type name, description, and fields. The
// type may implement Definitive or Descriptive to provide more information.
//
// Each currently defined field is installed on the returned definition.
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

// FieldDefinition returns the schema definition of a field, if present.
func (cls Class[T]) FieldDefinition(name string) (*ast.FieldDefinition, bool) {
	field, found := cls.Fields[name]
	if !found {
		return nil, false
	}
	return field.Definition(), true
}

// NewID returns a typed ID for the class.
func (cls Class[T]) NewID(id *idproto.ID) Typed {
	return ID[T]{ID: id}
}

// New returns a new instance of the class.
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

// Call calls a field on the class against an instance.
func (cls Class[T]) Call(ctx context.Context, node Instance[T], fieldName string, args map[string]Typed) (Typed, error) {
	field, ok := cls.Fields[fieldName]
	if !ok {
		var zero T
		return nil, fmt.Errorf("%s has no such field: %q", zero.Type().Name(), fieldName)
	}
	return field.Func(ctx, node, args)
}

// Instance is an instance of an Object type.
type Instance[T Typed] struct {
	Constructor *idproto.ID
	Self        T
	Class       Class[T]
}

var _ Object = Instance[Typed]{}

// Type returns the type of the instance.
func (o Instance[T]) Type() *ast.Type {
	return o.Self.Type()
}

var _ Object = Instance[Typed]{}

// ID returns the ID of the instance.
func (r Instance[T]) ID() *idproto.ID {
	return r.Constructor
}

// Select calls a field on the instance.
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

// Fields defines a set of fields for an Object type.
type Fields[T Typed] []Field[T]

// Install installs the field's Object type if needed, and installs all fields
// into the type.
func (fields Fields[T]) Install(server *Server) {
	var t T
	typeName := t.Type().Name()
	class := fields.findOrInitializeType(server, typeName)
	for _, field := range fields {
		field := field
		class.Fields[field.Spec.Name] = &field
	}
}

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
				Func: func(ctx context.Context, self Instance[T], args map[string]Typed) (Typed, error) {
					return ID[T]{ID: self.ID()}, nil
				},
			}.Install(classT)
		}
	} else {
		classT = class.(Class[T])
	}
	return classT
}

// Field defines a field of an Object type.
type Field[T Typed] struct {
	Spec FieldSpec
	Func func(context.Context, Instance[T], map[string]Typed) (Typed, error)
}

// Install installs the field into a class.
func (field Field[T]) Install(class Class[T]) {
	// TODO data race
	class.Fields[field.Spec.Name] = &field
}

// Doc sets the description of the field. Each argument is joined by two empty
// lines.
func (field Field[T]) Doc(paras ...string) Field[T] {
	field.Spec.Description = strings.Join(paras, "\n\n")
	return field
}

// Definition returns the schema definition of the field.
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
		Name:        fieldName,
		Description: field.Spec.Description,
		Arguments:   schemaArgs,
		Type:        field.Spec.Type,
	}
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
