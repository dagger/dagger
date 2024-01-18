package dagql

import (
	"context"
	"fmt"
	"reflect"
	"sort"
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
	inner   T
	module  *idproto.ID
	idable  bool
	fields  map[string]*Field[T]
	fieldsL *sync.Mutex
}

type ClassOpts[T Typed] struct {
	// NoIDs disables the default "id" field and disables the IDType method.
	NoIDs bool

	// Typed contains the Typed value whose Type() determines the class's type.
	//
	// In the simple case, we can just use a zero-value, but it is also allowed
	// to use a dynamic Typed value.
	Typed T

	// ModuleID is the ID of the module that the class belongs to.
	ModuleID *idproto.ID
}

// NewClass returns a new empty class for a given type.
func NewClass[T Typed](opts_ ...ClassOpts[T]) Class[T] {
	var opts ClassOpts[T]
	if len(opts_) > 0 {
		opts = opts_[0]
	}
	class := Class[T]{
		inner:   opts.Typed,
		module:  opts.ModuleID,
		fields:  map[string]*Field[T]{},
		fieldsL: new(sync.Mutex),
	}
	if !opts.NoIDs {
		class.Install(
			Field[T]{
				Spec: FieldSpec{
					Name:        "id",
					Description: fmt.Sprintf("A unique identifier for this %s.", class.TypeName()),
					Type:        ID[T]{inner: opts.Typed},
					Pure:        true,
				},
				Func: func(ctx context.Context, self Instance[T], args map[string]Input) (Typed, error) {
					return NewDynamicID[T](self.ID(), opts.Typed), nil
				},
			},
		)
		class.idable = true
	}
	return class
}

func (class Class[T]) Typed() Typed {
	return class.inner
}

func (class Class[T]) IDType() (IDType, bool) {
	if class.idable {
		return ID[T]{inner: class.inner}, true
	} else {
		return nil, false
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
	return cls.inner.Type().Name()
}

func (cls Class[T]) Extend(spec FieldSpec, fun FieldFunc) {
	cls.fieldsL.Lock()
	defer cls.fieldsL.Unlock()
	cls.fields[spec.Name] = &Field[T]{
		Spec: spec,
		Func: func(ctx context.Context, self Instance[T], args map[string]Input) (Typed, error) {
			return fun(ctx, self, args)
		},
	}
}

// Definition returns the schema definition of the class.
//
// The definition is derived from the type name, description, and fields. The
// type may implement Definitive or Descriptive to provide more information.
//
// Each currently defined field is installed on the returned definition.
func (cls Class[T]) TypeDefinition() *ast.Definition {
	cls.fieldsL.Lock()
	defer cls.fieldsL.Unlock()
	var val any = cls.inner
	var def *ast.Definition
	if isType, ok := val.(Definitive); ok {
		def = isType.TypeDefinition()
	} else {
		def = &ast.Definition{
			Kind: ast.Object,
			Name: cls.inner.Type().Name(),
		}
	}
	if isType, ok := val.(Descriptive); ok {
		def.Description = isType.TypeDescription()
	}
	for _, field := range cls.fields {
		def.Fields = append(def.Fields, field.FieldDefinition())
	}
	// TODO preserve order
	sort.Slice(def.Fields, func(i, j int) bool {
		return def.Fields[i].Name < def.Fields[j].Name
	})
	return def
}

// ParseField parses a field selection into a Selector and return type.
func (cls Class[T]) ParseField(ctx context.Context, astField *ast.Field, vars map[string]any) (Selector, *ast.Type, error) {
	field, ok := cls.Field(astField.Name)
	if !ok {
		return Selector{}, nil, fmt.Errorf("%s has no such field: %q", cls.TypeName(), astField.Name)
	}
	args := make([]NamedInput, len(astField.Arguments))
	for i, arg := range astField.Arguments {
		argSpec, ok := field.Spec.Args.Lookup(arg.Name)
		if !ok {
			return Selector{}, nil, fmt.Errorf("%s.%s has no such argument: %q", cls.TypeName(), field.Spec.Name, arg.Name)
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
			return Selector{}, nil, fmt.Errorf("init arg %q value as %T (%s) using %T: %w", arg.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
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
func (cls Class[T]) Call(ctx context.Context, node Instance[T], fieldName string, args map[string]Input) (Typed, error) {
	field, ok := cls.Field(fieldName)
	if !ok {
		return nil, fmt.Errorf("Call: %s has no such field: %q", cls.inner.Type().Name(), fieldName)
	}
	return field.Func(ctx, node, args)
}

// Instance is an instance of an Object type.
type Instance[T Typed] struct {
	Constructor *idproto.ID
	Self        T
	Class       Class[T]
	Module      *idproto.ID
}

var _ Typed = Instance[Typed]{}

// Type returns the type of the instance.
func (o Instance[T]) Type() *ast.Type {
	return o.Self.Type()
}

var _ Object = Instance[Typed]{}

// ID returns the ID of the instance.
func (r Instance[T]) ObjectType() ObjectType {
	return r.Class
}

// ID returns the ID of the instance.
func (r Instance[T]) ID() *idproto.ID {
	return r.Constructor
}

// Wrapper is an interface for types that wrap another type.
type Wrapper interface {
	Unwrap() Typed
}

var _ Wrapper = Instance[Typed]{}

// Inner returns the inner value of the instance.
func (r Instance[T]) Unwrap() Typed {
	return r.Self
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
		return nil, fmt.Errorf("IDFor: %s has no such field: %q", r.Class.inner.Type().Name(), sel.Field)
	}
	id := sel.AppendTo(r.ID(), field.Spec.Type.Type(), !field.Spec.Pure)
	id.Module = r.Class.module
	return id, nil
}

// Select calls a field on the instance.
func (r Instance[T]) Select(ctx context.Context, sel Selector) (val Typed, err error) {
	var zero T
	field, ok := r.Class.Field(sel.Field)
	if !ok {
		return nil, fmt.Errorf("Select: %s has no such field: %q", zero.Type().Name(), sel.Field)
	}
	args, err := applyDefaults(field.Spec, sel.Args)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", zero.Type().Name(), sel.Field, err)
	}
	val, err = r.Class.Call(ctx, r, sel.Field, args)
	if err != nil {
		return nil, err
	}
	if n, ok := val.(Derefable); ok {
		val, ok = n.Deref()
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
		if n, ok := val.(Derefable); ok {
			val, ok = n.Deref()
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
	inputs, argsErr := inputSpecsForType(zeroArgs, true)
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
		Func: func(ctx context.Context, self Instance[T], argVals map[string]Input) (Typed, error) {
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
	// DeprecatedReason deprecates the input and provides a reason.
	DeprecatedReason string
}

func (spec FieldSpec) FieldDefinition() *ast.FieldDefinition {
	def := &ast.FieldDefinition{
		Name:        spec.Name,
		Description: spec.Description,
		Arguments:   spec.Args.ArgumentDefinitions(),
		Type:        spec.Type.Type(),
	}
	if spec.DeprecatedReason != "" {
		def.Directives = append(def.Directives, deprecated(spec.DeprecatedReason))
	}
	if !spec.Pure {
		def.Directives = append(def.Directives, impure())
	}
	if spec.Meta {
		def.Directives = append(def.Directives, meta())
	}
	return def
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
	// DeprecatedReason deprecates the input and provides a reason.
	DeprecatedReason string
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
	for i, spec := range specs {
		schemaArg := &ast.ArgumentDefinition{
			Name:        spec.Name,
			Description: spec.Description,
			Type:        spec.Type.Type(),
		}
		if spec.Default != nil {
			schemaArg.DefaultValue = spec.Default.ToLiteral().ToAST()
		}
		if spec.DeprecatedReason != "" {
			schemaArg.Directives = append(schemaArg.Directives, deprecated(spec.DeprecatedReason))
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
		if spec.DeprecatedReason != "" {
			field.Directives = append(field.Directives, deprecated(spec.DeprecatedReason))
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
	TypeDescription() string
}

// Definitive is a type that knows how to define itself in the schema.
type Definitive interface {
	TypeDefinition() *ast.Definition
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
	objectFields, err := reflectFieldsForType(t, false, builtinOrTyped)
	if err != nil {
		panic(fmt.Errorf("fields for %T: %w", t, err))
	}
	for _, field := range objectFields {
		name := field.Name
		fields = append(fields, Field[T]{
			Spec: FieldSpec{
				Name: name,
				Type: field.Value,
				Pure: true,
			},
			Func: func(ctx context.Context, self Instance[T], args map[string]Input) (Typed, error) {
				t, found, err := getField(self.Self, false, name)
				if err != nil {
					return nil, err
				}
				if !found {
					return nil, fmt.Errorf("no such field: %q", name)
				}
				return t, nil
			},
		})
	}
	class.Install(fields...)
}

func (fields Fields[T]) findOrInitializeType(server *Server, typeName string) Class[T] {
	var classT Class[T]
	class, ok := server.objects[typeName]
	if !ok {
		classT = NewClass[T]()
		server.installObjectLocked(classT)
	} else {
		classT = class.(Class[T])
	}
	return classT
}

// Field defines a field of an Object type.
type Field[T Typed] struct {
	Spec FieldSpec
	Func func(context.Context, Instance[T], map[string]Input) (Typed, error)
}

// Doc sets the description of the field. Each argument is joined by two empty
// lines.
func (field Field[T]) Doc(paras ...string) Field[T] {
	field.Spec.Description = FormatDescription(paras...)
	return field
}

func (field Field[T]) ArgDoc(name string, paras ...string) Field[T] {
	for i, arg := range field.Spec.Args {
		if arg.Name == name {
			field.Spec.Args[i].Description = FormatDescription(paras...)
			return field
		}
	}
	panic(fmt.Sprintf("field %s has no such argument: %q", field.Spec.Name, name))
}

func FormatDescription(paras ...string) string {
	for i, p := range paras {
		paras[i] = strings.Join(strings.Fields(strings.TrimSpace(p)), " ")
	}
	return strings.Join(paras, "\n\n")
}

// Doc sets the description of the field. Each argument is joined by two empty
// lines.
func (field Field[T]) DynamicReturnType(ret Typed) Field[T] {
	field.Spec.Type = ret
	return field
}

// Impure marks the field as "impure", meaning its result may change over time,
// or it has side effects.
func (field Field[T]) Deprecated(paras ...string) Field[T] {
	field.Spec.DeprecatedReason = FormatDescription(paras...)
	return field
}

// Impure marks the field as "impure", meaning its result may change over time,
// or it has side effects.
func (field Field[T]) Impure() Field[T] {
	field.Spec.Pure = false
	return field
}

// Impure marks the field as "impure", meaning its result may change over time,
// or it has side effects.
func (field Field[T]) Meta() Field[T] {
	field.Spec.Meta = true
	return field
}

// WithPurity sets the purity of the field.
func (field Field[T]) WithPurity(purity bool) Field[T] {
	field.Spec.Pure = purity
	return field
}

// Definition returns the schema definition of the field.
func (field Field[T]) FieldDefinition() *ast.FieldDefinition {
	spec := field.Spec
	if spec.Type == nil {
		panic(fmt.Errorf("field %q has no type", spec.Name))
	}
	return field.Spec.FieldDefinition()
}

func definition(kind ast.DefinitionKind, val Type) *ast.Definition {
	var def *ast.Definition
	if isType, ok := val.(Definitive); ok {
		def = isType.TypeDefinition()
	} else {
		def = &ast.Definition{
			Kind: kind,
			Name: val.TypeName(),
		}
	}
	if isType, ok := val.(Descriptive); ok {
		def.Description = isType.TypeDescription()
	}
	return def
}

func applyDefaults(field FieldSpec, inputs Inputs) (map[string]Input, error) {
	args := make(map[string]Input, len(field.Args))
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

type reflectField[T any] struct {
	Name  string
	Value T
	Field reflect.StructField
}

func inputSpecsForType(obj any, optIn bool) (InputSpecs, error) {
	fields, err := reflectFieldsForType(obj, optIn, builtinOrInput)
	if err != nil {
		return nil, err
	}
	specs := make([]InputSpec, len(fields))
	for i, field := range fields {
		name := field.Name
		fieldT := field.Field
		input := field.Value
		var inputDef Input
		if inputDefStr, hasDefault := fieldT.Tag.Lookup("default"); hasDefault {
			var err error
			inputDef, err = input.Decoder().DecodeInput(inputDefStr)
			if err != nil {
				return nil, fmt.Errorf("convert default value %q for arg %q: %w", inputDefStr, name, err)
			}
			if input.Type().NonNull {
				input = DynamicOptional{
					Elem: input,
				}
			}
		}
		specs[i] = InputSpec{
			Name:             field.Name,
			Description:      field.Field.Tag.Get("doc"),
			Type:             input,
			Default:          inputDef,
			DeprecatedReason: field.Field.Tag.Get("deprecated"),
		}
	}
	return specs, nil
}

func reflectFieldsForType[T any](obj any, optIn bool, init func(any) (T, error)) ([]reflectField[T], error) {
	var fields []reflectField[T]
	objT := reflect.TypeOf(obj)
	if objT == nil {
		return nil, nil
	}
	if objT.Kind() == reflect.Ptr {
		objT = objT.Elem()
	}
	if objT.Kind() != reflect.Struct {
		return nil, fmt.Errorf("inputs must be a struct, got %T (%s)", obj, objT.Kind())
	}
	for i := 0; i < objT.NumField(); i++ {
		fieldT := objT.Field(i)
		if fieldT.Anonymous {
			fieldI := reflect.New(fieldT.Type).Elem().Interface()
			embeddedFields, err := reflectFieldsForType(fieldI, optIn, init)
			if err != nil {
				return nil, fmt.Errorf("embedded struct %q: %w", fieldT.Name, err)
			}
			fields = append(fields, embeddedFields...)
			continue
		}
		isField := optIn || fieldT.Tag.Get("field") == "true"
		if !isField {
			continue
		}
		name := fieldT.Tag.Get("name")
		if name == "" && isField {
			name = strcase.ToLowerCamel(fieldT.Name)
		}
		if name == "" || name == "-" {
			continue
		}
		fieldI := reflect.New(fieldT.Type).Elem().Interface()
		val, err := init(fieldI)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", name, err)
		}
		fields = append(fields, reflectField[T]{
			Name:  name,
			Value: val,
			Field: fieldT,
		})
	}
	return fields, nil
}

func getField(obj any, optIn bool, fieldName string) (res Typed, found bool, rerr error) {
	defer func() {
		if err := recover(); err != nil {
			rerr = fmt.Errorf("get field %q: %s", fieldName, err)
		}
	}()
	objT := reflect.TypeOf(obj)
	if objT == nil {
		return nil, false, fmt.Errorf("get field %q: object is nil", fieldName)
	}
	objV := reflect.ValueOf(obj)
	if objT.Kind() == reflect.Ptr {
		// if objV.IsZero() {
		// 	return nil, false, nil
		// }
		objT = objT.Elem()
		objV = objV.Elem()
	}
	if objT.Kind() != reflect.Struct {
		return nil, false, fmt.Errorf("get field %q: object must be a struct, got %T (%s)", fieldName, obj, objT.Kind())
	}
	for i := 0; i < objT.NumField(); i++ {
		fieldT := objT.Field(i)
		if fieldT.Anonymous {
			fieldI := objV.Field(i).Interface()
			t, found, err := getField(fieldI, optIn, fieldName)
			if err != nil {
				return nil, false, fmt.Errorf("embedded struct %q: %w", fieldT.Name, err)
			}
			if found {
				return t, true, nil
			}
			continue
		}
		isField := optIn || fieldT.Tag.Get("field") == "true"
		if !isField {
			continue
		}
		name := fieldT.Tag.Get("name")
		if name == "" && isField {
			name = strcase.ToLowerCamel(fieldT.Name)
		}
		if name == "" || name == "-" {
			continue
		}
		if name == fieldName {
			val := objV.Field(i).Interface()
			t, err := builtinOrTyped(val)
			if err != nil {
				return nil, false, fmt.Errorf("get field %q: %w", name, err)
			}
			// if !t.Type().NonNull && objV.Field(i).IsZero() {
			// 	return nil, true, nil
			// }
			return t, true, nil
		}
	}
	return nil, false, nil
}

func setInputFields(specs InputSpecs, inputs map[string]Input, dest any) error {
	destT := reflect.TypeOf(dest).Elem()
	destV := reflect.ValueOf(dest).Elem()
	if destT == nil {
		return nil
	}
	if destT.Kind() != reflect.Struct {
		return fmt.Errorf("inputs must be a struct, got %T (%s)", dest, destT.Kind())
	}
	for i := 0; i < destT.NumField(); i++ {
		fieldT := destT.Field(i)
		fieldV := destV.Field(i)
		if fieldT.Anonymous {
			// embedded struct
			val := reflect.New(fieldT.Type)
			if err := setInputFields(specs, inputs, val.Interface()); err != nil {
				return err
			}
			fieldV.Set(val.Elem())
			continue
		}
		name := fieldT.Tag.Get("name")
		if name == "" {
			name = strcase.ToLowerCamel(fieldT.Name)
		}
		if name == "-" {
			continue
		}
		spec, found := specs.Lookup(name)
		if !found {
			return fmt.Errorf("missing input spec for %q", name)
		}
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
		if err := assign(fieldV, val); err != nil {
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
