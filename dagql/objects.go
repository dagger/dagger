package dagql

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
	"golang.org/x/exp/slog"
)

// Class is a class of Object types.
//
// The class is defined by a set of fields, which are installed into the class
// dynamically at runtime.
type Class[T Typed] struct {
	fields  map[string]*Field[T]
	fieldsL *sync.Mutex
}

// NewClass returns a new empty class for a given type.
func NewClass[T Typed]() Class[T] {
	return Class[T]{
		fields:  map[string]*Field[T]{},
		fieldsL: new(sync.Mutex),
	}
}

func (class Class[T]) Field(name string) (Field[T], bool) {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	field, ok := class.fields[name]
	if !ok {
		return Field[T]{}, false
	}
	return *field, ok
}

func (class Class[T]) Install(fields ...Field[T]) {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	for _, field := range fields {
		field := field
		class.fields[field.Spec.Name] = &field
	}
}

var _ ObjectType = Class[Typed]{}

func (cls Class[T]) TypeName() string {
	var zero T
	return zero.Type().Name()
}

// Definition returns the schema definition of the class.
//
// The definition is derived from the type name, description, and fields. The
// type may implement Definitive or Descriptive to provide more information.
//
// Each currently defined field is installed on the returned definition.
func (cls Class[T]) Definition() *ast.Definition {
	cls.fieldsL.Lock()
	defer cls.fieldsL.Unlock()
	var zero T
	var val any = zero
	var def *ast.Definition
	if isType, ok := val.(Definitive); ok {
		def = isType.Definition()
	} else {
		def = &ast.Definition{
			Kind: ast.Object,
			Name: zero.Type().Name(),
		}
	}
	if isType, ok := val.(Descriptive); ok {
		def.Description = isType.Description()
	}
	for _, field := range cls.fields { // TODO sort, or preserve order
		def.Fields = append(def.Fields, field.Definition())
	}
	return def
}

// ParseField parses a field selection into a Selector and return type.
func (cls Class[T]) ParseField(ctx context.Context, astField *ast.Field, vars map[string]any) (Selector, *ast.Type, error) {
	field, ok := cls.Field(astField.Name)
	if !ok {
		return Selector{}, nil, fmt.Errorf("%s has no such field: %q", cls.Definition().Name, astField.Name)
	}
	args := make([]NamedInput, len(astField.Arguments))
	for i, arg := range astField.Arguments {
		argSpec, ok := field.Spec.Args.Lookup(arg.Name)
		if !ok {
			return Selector{}, nil, fmt.Errorf("%s has no such argument: %q", field.Spec.Name, arg.Name)
		}
		val, err := arg.Value.Value(vars)
		if err != nil {
			return Selector{}, nil, err
		}
		if val == nil {
			continue
		}
		input, err := argSpec.Type.Decoder().DecodeInput(val)
		if err != nil {
			return Selector{}, nil, fmt.Errorf("init arg %q value: %w", arg.Name, err)
		}
		args[i] = NamedInput{
			Name:  arg.Name,
			Value: input,
		}
	}
	return Selector{
		Field: astField.Name,
		Args:  args,
	}, field.Spec.Type.Type(), nil
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
	field, ok := cls.Field(fieldName)
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

var _ Typed = Instance[Typed]{}

// Type returns the type of the instance.
func (o Instance[T]) Type() *ast.Type {
	return o.Self.Type()
}

var _ Object = Instance[Typed]{}

// ID returns the ID of the instance.
func (r Instance[T]) ID() *idproto.ID {
	return r.Constructor
}

// String returns the instance in Class@sha256:... format.
func (r Instance[T]) String() string {
	dig, err := r.Constructor.Digest()
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s@%s", r.Type().Name(), dig)
}

func (r Instance[T]) IDFor(ctx context.Context, sel Selector) (*idproto.ID, error) {
	field, ok := r.Class.Field(sel.Field)
	if !ok {
		var zero T
		return nil, fmt.Errorf("%s has no such field: %q", zero.Type().Name(), sel.Field)
	}
	return sel.AppendTo(r.ID(), field.Spec.Type.Type(), !field.Spec.Pure), nil
}

// Select calls a field on the instance.
func (r Instance[T]) Select(ctx context.Context, sel Selector) (val Typed, err error) {
	field, ok := r.Class.Field(sel.Field)
	if !ok {
		var zero T
		return nil, fmt.Errorf("%s has no such field: %q", zero.Type().Name(), sel.Field)
	}
	args, err := applyDefaults(field.Spec, sel.Args)
	if err != nil {
		return nil, err
	}
	val, err = r.Class.Call(ctx, r, sel.Field, args)
	if err != nil {
		return nil, err
	}
	if n, ok := val.(NullableWrapper); ok {
		val, ok = n.Unwrap()
		if !ok {
			return nil, nil
		}
	}
	if sel.Nth != 0 {
		enum, ok := val.(Enumerable)
		if !ok {
			return nil, fmt.Errorf("cannot sub-select %dth item from %T", sel.Nth, val)
		}
		val, err = enum.Nth(sel.Nth)
		if err != nil {
			return nil, err
		}
		if n, ok := val.(NullableWrapper); ok {
			val, ok = n.Unwrap()
			if !ok {
				return nil, nil
			}
		}
	}
	return val, nil
}

// Func is a helper for defining a field resolver and schema.
//
// The function must accept a context.Context, the receiver, and a struct of
// arguments. All fields of the arguments struct must be Typed so that the
// schema may be derived, and Scalar to ensure a default value may be provided.
//
// Arguments use struct tags to further configure the schema:
//
//   - `name:"bar"` sets the name of the argument. By default this is the
//     toLowerCamel'd field name.
//   - `default:"foo"` sets the default value of the argument. The Scalar type
//     determines how this value is parsed.
//   - `doc:"..."` sets the description of the argument.
//
// The function must return a Typed value, and an error.
//
// To configure a description for the field in the schema, call .Doc on the
// result.
func Func[T Typed, A any, R any](name string, fn func(ctx context.Context, self T, args A) (R, error)) Field[T] {
	return NodeFunc(name, func(ctx context.Context, self Instance[T], args A) (R, error) {
		return fn(ctx, self.Self, args)
	})
}

// NodeFunc is the same as Func, except it passes the Instance instead of the
// receiver so that you can access its ID.
func NodeFunc[T Typed, A any, R any](name string, fn func(ctx context.Context, self Instance[T], args A) (R, error)) Field[T] {
	var zeroArgs A
	inputs, argsErr := inputSpecsForType(zeroArgs)
	if argsErr != nil {
		var zeroSelf T
		slog.Error("failed to parse args", "type", zeroSelf.Type(), "field", name, "error", argsErr)
	}
	var zeroRet R
	ret, err := builtinOrTyped(zeroRet)
	if err != nil {
		var zeroSelf T
		slog.Error("failed to parse return type", "type", zeroSelf.Type(), "field", name, "error", err)
	}
	return Field[T]{
		Spec: FieldSpec{
			Name: name,
			Args: inputs,
			Type: ret,
			Pure: true, // default to pure
		},
		Func: func(ctx context.Context, self Instance[T], argVals map[string]Typed) (Typed, error) {
			if argsErr != nil {
				// this error is deferred until runtime, since it's better (at least
				// more testable) than panicking
				return nil, argsErr
			}
			var args A
			if err := setInputFields(inputs, argVals, &args); err != nil {
				return nil, err
			}
			res, err := fn(ctx, self, args)
			if err != nil {
				return nil, err
			}
			return builtinOrTyped(res)
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
	Args InputSpecs
	// Type is the type of the field's result.
	Type Typed
	// Meta indicates that the field has no impact on the field's result.
	Meta bool
	// Pure indicates that the field is a pure function of its arguments, and
	// thus can be cached indefinitely.
	Pure bool
}

// InputSpec specifies a field argument, or an input field.
type InputSpec struct {
	// Name is the name of the argument.
	Name string
	// Description is the description of the argument.
	Description string
	// Type is the type of the argument.
	Type Input
	// Default is the default value of the argument.
	Default Input
}

type InputSpecs []InputSpec

func (specs InputSpecs) Lookup(name string) (InputSpec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return InputSpec{}, false
}

func (specs InputSpecs) ArgumentDefinitions() []*ast.ArgumentDefinition {
	defs := make([]*ast.ArgumentDefinition, len(specs))
	for i, arg := range specs {
		schemaArg := &ast.ArgumentDefinition{
			Name:        arg.Name,
			Description: arg.Description,
			Type:        arg.Type.Type(),
		}
		if arg.Default != nil {
			schemaArg.DefaultValue = arg.Default.ToLiteral().ToAST()
		}
		defs[i] = schemaArg
	}
	return defs
}

func (specs InputSpecs) FieldDefinitions() []*ast.FieldDefinition {
	fields := make([]*ast.FieldDefinition, len(specs))
	for i, spec := range specs {
		field := &ast.FieldDefinition{
			Name:        spec.Name,
			Description: spec.Description,
			Type:        spec.Type.Type(),
		}
		if spec.Default != nil {
			field.DefaultValue = spec.Default.ToLiteral().ToAST()
		}
		fields[i] = field
	}
	return fields
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

// Fields defines a set of fields for an Object type.
type Fields[T Typed] []Field[T]

// Install installs the field's Object type if needed, and installs all fields
// into the type.
func (fields Fields[T]) Install(server *Server) {
	server.installLock.Lock()
	defer server.installLock.Unlock()
	var t T
	typeName := t.Type().Name()
	class := fields.findOrInitializeType(server, typeName)
	class.Install(fields...)
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
			classT.Install(Field[T]{
				Spec: FieldSpec{
					Name: "id",
					Type: idScalar,
				},
				Func: func(ctx context.Context, self Instance[T], args map[string]Typed) (Typed, error) {
					return ID[T]{ID: self.ID()}, nil
				},
			})
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

// Doc sets the description of the field. Each argument is joined by two empty
// lines.
func (field Field[T]) Doc(paras ...string) Field[T] {
	field.Spec.Description = strings.Join(paras, "\n\n")
	return field
}

// Doc sets the description of the field. Each argument is joined by two empty
// lines.
func (field Field[T]) DynamicReturnType(ret Typed) Field[T] {
	field.Spec.Type = ret
	return field
}

// Impure marks the field as "impure", meaning its result may change over time,
// or it has side effects.
func (field Field[T]) Impure() Field[T] {
	field.Spec.Pure = false
	return field
}

// Definition returns the schema definition of the field.
func (field Field[T]) Definition() *ast.FieldDefinition {
	return &ast.FieldDefinition{
		Name:        field.Spec.Name,
		Description: field.Spec.Description,
		Arguments:   field.Spec.Args.ArgumentDefinitions(),
		Type:        field.Spec.Type.Type(),
	}
}

func definition(kind ast.DefinitionKind, val Type) *ast.Definition {
	var def *ast.Definition
	if isType, ok := val.(Definitive); ok {
		def = isType.Definition()
	} else {
		def = &ast.Definition{
			Kind: kind,
			Name: val.TypeName(),
		}
	}
	if isType, ok := val.(Descriptive); ok {
		def.Description = isType.Description()
	}
	return def
}

func applyDefaults(field FieldSpec, inputs Inputs) (map[string]Typed, error) {
	args := make(map[string]Typed, len(field.Args))
	for _, arg := range field.Args {
		val, ok := inputs.Lookup(arg.Name)
		if ok {
			args[arg.Name] = val
		} else if arg.Default != nil {
			args[arg.Name] = arg.Default
		} else if arg.Type.Type().NonNull {
			return nil, fmt.Errorf("missing required argument: %q", arg.Name)
		}
	}
	return args, nil
}

func inputSpecsForType(obj any) ([]InputSpec, error) {
	var inputSpecs InputSpecs
	objT := reflect.TypeOf(obj)
	if objT == nil {
		return nil, nil
	}
	if objT.Kind() != reflect.Struct {
		return nil, fmt.Errorf("inputs must be a struct, got %T", obj)
	}
	for i := 0; i < objT.NumField(); i++ {
		field := objT.Field(i)
		name := field.Tag.Get("name")
		if name == "" {
			name = strcase.ToLowerCamel(field.Name)
		}
		fieldI := reflect.New(field.Type).Elem().Interface()
		typed, err := builtinOrInput(fieldI)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", field.Name, err)
		}
		input, ok := typed.(Input)
		if !ok {
			return nil, fmt.Errorf("arg %q: %T is not an input", field.Name, typed)
		}
		var inputDef Input
		if inputDefStr := field.Tag.Get("default"); len(inputDefStr) > 0 {
			var err error
			inputDef, err = input.Decoder().DecodeInput(inputDefStr)
			if err != nil {
				return nil, fmt.Errorf("convert default value for arg %s: %w", name, err)
			}
		}
		inputSpecs = append(inputSpecs, InputSpec{
			Name:        name,
			Description: field.Tag.Get("doc"),
			Type:        input,
			Default:     inputDef,
		})
	}
	return inputSpecs, nil
}

func setInputFields(specs InputSpecs, inputs map[string]Typed, dest any) error {
	destV := reflect.ValueOf(dest)
	for i, spec := range specs {
		val, isProvided := inputs[spec.Name]
		isNullable := !spec.Type.Type().NonNull
		if !isProvided {
			if isNullable {
				// defaults are applied before we get here, so if it's still not here,
				// it's really not set
				continue
			}
			return fmt.Errorf("missing required input: %q", spec.Name)
		}
		if err := assign(destV.Elem().Field(i), val); err != nil {
			return fmt.Errorf("assign %q: %w", spec.Name, err)
		}
	}
	return nil
}

func assign(field reflect.Value, val any) error {
	if reflect.TypeOf(val).AssignableTo(field.Type()) {
		field.Set(reflect.ValueOf(val))
		return nil
	} else if setter, ok := val.(Setter); ok {
		return setter.SetField(field)
	} else {
		return fmt.Errorf("cannot assign %T to %s", val, field.Type())
	}
}
