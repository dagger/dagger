package dagql

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

// Class is a class of Object types.
//
// The class is defined by a set of fields, which are installed into the class
// dynamically at runtime.
type Class[T Typed] struct {
	inner   T
	idable  bool
	fields  map[string][]*Field[T]
	fieldsL *sync.Mutex
}

var _ ObjectType = Class[Typed]{}

type ClassOpts[T Typed] struct {
	// NoIDs disables the default "id" field and disables the IDType method.
	NoIDs bool

	// Typed contains the Typed value whose Type() determines the class's type.
	//
	// In the simple case, we can just use a zero-value, but it is also allowed
	// to use a dynamic Typed value.
	Typed T
}

// NewClass returns a new empty class for a given type.
func NewClass[T Typed](opts_ ...ClassOpts[T]) Class[T] {
	var opts ClassOpts[T]
	if len(opts_) > 0 {
		opts = opts_[0]
	}
	class := Class[T]{
		inner:   opts.Typed,
		fields:  map[string][]*Field[T]{},
		fieldsL: new(sync.Mutex),
	}
	if !opts.NoIDs {
		class.Install(
			Field[T]{
				Spec: FieldSpec{
					Name:        "id",
					Description: fmt.Sprintf("A unique identifier for this %s.", class.TypeName()),
					Type:        ID[T]{inner: opts.Typed},
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

func (class Class[T]) Field(name string, views ...string) (Field[T], bool) {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	return class.fieldLocked(name, views...)
}

func (class Class[T]) FieldSpec(name string, views ...string) (FieldSpec, bool) {
	field, ok := class.Field(name, views...)
	if !ok {
		return FieldSpec{}, false
	}
	return field.Spec, true
}

func (class Class[T]) fieldLocked(name string, views ...string) (Field[T], bool) {
	fields, ok := class.fields[name]
	if !ok {
		return Field[T]{}, false
	}
	for i := len(fields) - 1; i >= 0; i-- {
		// iterate backwards to allow last-defined field to have precedence
		field := fields[i]

		if field.ViewFilter == nil {
			return *field, true
		}
		for _, view := range views {
			if field.ViewFilter.Contains(view) {
				return *field, true
			}
		}
	}
	return Field[T]{}, false
}

func (class Class[T]) Install(fields ...Field[T]) {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	for _, field := range fields {
		if field.Spec.extend {
			fields := class.fields[field.Spec.Name]
			if len(fields) == 0 {
				panic(fmt.Sprintf("field %q cannot be extended, as it has not been defined", field.Spec.Name))
			}
			oldSpec := field.Spec

			field.Spec = fields[len(fields)-1].Spec
			field.Spec.Type = oldSpec.Type // a little hacky, but preserve the return type
		}

		for _, other := range class.fields[field.Spec.Name] {
			if field.ViewFilter == nil && other.ViewFilter != nil {
				panic(fmt.Sprintf("field %q cannot be added to the global view, it already has a view", field.Spec.Name))
			}
			if field.ViewFilter != nil && other.ViewFilter == nil {
				panic(fmt.Sprintf("field %q cannot be added with a view, it's already in the global view", field.Spec.Name))
			}
		}

		class.fields[field.Spec.Name] = append(class.fields[field.Spec.Name], &field)
	}
}

var _ ObjectType = Class[Typed]{}

func (cls Class[T]) TypeName() string {
	return cls.inner.Type().Name()
}

func (cls Class[T]) Extend(spec FieldSpec, fun FieldFunc, cacheKeyFun FieldCacheKeyFunc) {
	cls.fieldsL.Lock()
	defer cls.fieldsL.Unlock()
	f := &Field[T]{
		Spec: spec,
		Func: func(ctx context.Context, self Instance[T], args map[string]Input) (Typed, error) {
			return fun(ctx, self, args)
		},
	}
	if cacheKeyFun != nil {
		f.CacheKeyFunc = func(ctx context.Context, self Instance[T], args map[string]Input, origDgst digest.Digest) (digest.Digest, error) {
			return cacheKeyFun(ctx, self, args, origDgst)
		}
	}
	cls.fields[spec.Name] = append(cls.fields[spec.Name], f)
}

// TypeDefinition returns the schema definition of the class.
//
// The definition is derived from the type name, description, and fields. The
// type may implement Definitive or Descriptive to provide more information.
//
// Each currently defined field is installed on the returned definition.
func (cls Class[T]) TypeDefinition(views ...string) *ast.Definition {
	cls.fieldsL.Lock()
	defer cls.fieldsL.Unlock()
	var val any = cls.inner
	var def *ast.Definition
	if isType, ok := val.(Definitive); ok {
		def = isType.TypeDefinition(views...)
	} else {
		def = &ast.Definition{
			Kind: ast.Object,
			Name: cls.inner.Type().Name(),
		}
	}
	if isType, ok := val.(Descriptive); ok {
		def.Description = isType.TypeDescription()
	}
	for name := range cls.fields {
		if field, ok := cls.fieldLocked(name, views...); ok {
			def.Fields = append(def.Fields, field.FieldDefinition())
		}
	}
	// TODO preserve order
	sort.Slice(def.Fields, func(i, j int) bool {
		return def.Fields[i].Name < def.Fields[j].Name
	})
	return def
}

// ParseField parses a field selection into a Selector and return type.
func (cls Class[T]) ParseField(ctx context.Context, view string, astField *ast.Field, vars map[string]any) (Selector, *ast.Type, error) {
	field, ok := cls.Field(astField.Name, view)
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
		input, err := argSpec.Type.Decoder().DecodeInput(val)
		if err != nil {
			return Selector{}, nil, fmt.Errorf("init arg %q value as %T (%s) using %T: %w", arg.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
		}
		args[i] = NamedInput{
			Name:  arg.Name,
			Value: input,
		}
	}
	if field.ViewFilter == nil {
		// fields in the global view shouldn't attach the current view to the
		// selector (since they're global from all perspectives)
		view = ""
	}
	return Selector{
		Field: astField.Name,
		Args:  args,
		View:  view,
	}, field.Spec.Type.Type(), nil
}

// New returns a new instance of the class.
func (cls Class[T]) New(id *call.ID, val Typed) (Object, error) {
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
func (cls Class[T]) Call(ctx context.Context, node Instance[T], fieldName string, view string, args map[string]Input) (Typed, error) {
	field, ok := cls.Field(fieldName, view)
	if !ok {
		return nil, fmt.Errorf("Call: %s has no such field: %q", cls.inner.Type().Name(), fieldName)
	}
	return field.Func(ctx, node, args)
}

// Instance is an instance of an Object type.
type Instance[T Typed] struct {
	Constructor *call.ID
	Self        T
	Class       Class[T]
	Module      *call.ID
}

var _ Typed = Instance[Typed]{}

// Type returns the type of the instance.
func (o Instance[T]) Type() *ast.Type {
	return o.Self.Type()
}

var _ Object = Instance[Typed]{}

// ObjectType returns the ObjectType of the instance.
func (r Instance[T]) ObjectType() ObjectType {
	return r.Class
}

// ID returns the ID of the instance.
func (r Instance[T]) ID() *call.ID {
	return r.Constructor
}

// Wrapper is an interface for types that wrap another type.
type Wrapper interface {
	Unwrap() Typed
}

var _ Wrapper = Instance[Typed]{}

// Unwrap returns the inner value of the instance.
func (r Instance[T]) Unwrap() Typed {
	return r.Self
}

// String returns the instance in Class@sha256:... format.
func (r Instance[T]) String() string {
	return fmt.Sprintf("%s@%s", r.Type().Name(), r.Constructor.Digest())
}

// WithMetadata returns an updated instance with the given metadata set.
// isPure changes the purity of the instance.
// customDigest overrides the default digest of the instance to the provided value.
// NOTE: customDigest must be used with care as any instances with the same digest
// will be considered equivalent and can thus replace each other in the cache.
// Generally, customDigest should be used when there's a content-based digest available
// that won't be caputured by the default, call-chain derived digest.
func (r Instance[T]) WithMetadata(customDigest digest.Digest, isPure bool) Instance[T] {
	return Instance[T]{
		Constructor: r.Constructor.WithMetadata(customDigest, !isPure),
		Self:        r.Self,
		Class:       r.Class,
		Module:      r.Module,
	}
}

func NoopDone(res Typed, cached bool, rerr error) {}

// Selects calls the field on the instance specified by the selector
func (r Instance[T]) Select(ctx context.Context, s *Server, sel Selector) (Typed, *call.ID, error) {
	view := sel.View
	field, ok := r.Class.Field(sel.Field, view)
	if !ok {
		return nil, nil, fmt.Errorf("Select: %s has no such field: %q", r.Class.TypeName(), sel.Field)
	}
	if field.ViewFilter == nil {
		// fields in the global view shouldn't attach the current view to the
		// selector (since they're global from all perspectives)
		view = ""
	}

	idArgs := make([]*call.Argument, 0, len(sel.Args))
	inputArgs := make(map[string]Input, len(sel.Args))
	for _, argSpec := range field.Spec.Args {
		// just be n^2 since the overhead of a map is likely more expensive
		// for the expected low value of n
		var namedInput NamedInput
		for _, selArg := range sel.Args {
			if selArg.Name == argSpec.Name {
				namedInput = selArg
				break
			}
		}

		switch {
		case namedInput.Value != nil:
			idArgs = append(idArgs, call.NewArgument(
				namedInput.Name,
				namedInput.Value.ToLiteral(),
				argSpec.Sensitive,
			))
			inputArgs[argSpec.Name] = namedInput.Value

		case argSpec.Default != nil:
			inputArgs[argSpec.Name] = argSpec.Default

		case argSpec.Type.Type().NonNull:
			// error out if the arg is missing but required
			return nil, nil, fmt.Errorf("missing required argument: %q", argSpec.Name)
		}
	}
	// TODO: it's better DX if it matches schema order
	sort.Slice(idArgs, func(i, j int) bool {
		return idArgs[i].Name() < idArgs[j].Name()
	})

	astType := field.Spec.Type.Type()
	if sel.Nth != 0 {
		astType = astType.Elem
	}

	tainted := !sel.Pure && field.Spec.ImpurityReason != ""

	newID := r.Constructor.Append(
		astType,
		sel.Field,
		view,
		field.Spec.Module,
		tainted,
		sel.Nth,
		"",
		idArgs...,
	)

	if field.CacheKeyFunc != nil {
		customDgst, err := field.CacheKeyFunc(ctx, r, inputArgs, newID.Digest())
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compute cache key for %s.%s: %w", r.Type().Name(), sel.Field, err)
		}
		newID = newID.WithMetadata(customDgst, tainted)
	}

	return r.call(ctx, s, newID, inputArgs)
}

// Call calls the field on the instance specified by the ID.
func (r Instance[T]) Call(ctx context.Context, s *Server, newID *call.ID) (Typed, *call.ID, error) {
	fieldName := newID.Field()
	view := newID.View()
	field, ok := r.Class.Field(fieldName, view)
	if !ok {
		return nil, nil, fmt.Errorf("Call: %s has no such field: %q", r.Class.TypeName(), fieldName)
	}

	idArgs := newID.Args()
	inputArgs := make(map[string]Input, len(idArgs))
	for _, argSpec := range field.Spec.Args {
		// just be n^2 since the overhead of a map is likely more expensive
		// for the expected low value of n
		var inputLit call.Literal
		for _, idArg := range idArgs {
			if idArg.Name() == argSpec.Name {
				inputLit = idArg.Value()
				break
			}
		}

		switch {
		case inputLit != nil:
			input, err := argSpec.Type.Decoder().DecodeInput(inputLit.ToInput())
			if err != nil {
				return nil, nil, fmt.Errorf("Call: init arg %q value as %T (%s) using %T: %w", argSpec.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
			}
			inputArgs[argSpec.Name] = input

		case argSpec.Default != nil:
			inputArgs[argSpec.Name] = argSpec.Default

		case argSpec.Type.Type().NonNull:
			// error out if the arg is missing but required
			return nil, nil, fmt.Errorf("missing required argument: %q", argSpec.Name)
		}
	}

	return r.call(ctx, s, newID, inputArgs)
}

func (r Instance[T]) call(
	ctx context.Context,
	s *Server,
	newID *call.ID,
	inputArgs map[string]Input,
) (Typed, *call.ID, error) {
	doCall := func(ctx context.Context) (innerVal Typed, innerErr error) {
		if s.telemetry != nil {
			wrappedCtx, done := s.telemetry(ctx, r, newID)
			defer func() { done(innerVal, false, innerErr) }()
			ctx = wrappedCtx
		}

		innerVal, innerErr = r.Class.Call(ctx, r, newID.Field(), newID.View(), inputArgs)
		if innerErr != nil {
			return nil, innerErr
		}

		if n, ok := innerVal.(Derefable); ok {
			innerVal, ok = n.Deref()
			if !ok {
				return nil, nil
			}
		}
		nth := int(newID.Nth())
		if nth != 0 {
			enum, ok := innerVal.(Enumerable)
			if !ok {
				return nil, fmt.Errorf("cannot sub-select %dth item from %T", nth, innerVal)
			}
			innerVal, innerErr = enum.Nth(nth)
			if innerErr != nil {
				return nil, innerErr
			}
			if n, ok := innerVal.(Derefable); ok {
				innerVal, ok = n.Deref()
				if !ok {
					return nil, nil
				}
			}
		}

		return innerVal, nil
	}

	ctx = idToContext(ctx, newID)
	dig := newID.Digest()
	var val Typed
	var err error
	if newID.IsTainted() {
		val, err = doCall(ctx)
	} else {
		val, _, err = s.Cache.GetOrInitialize(ctx, dig, doCall)
	}
	if err != nil {
		return nil, nil, err
	}

	// If the returned val is IDable, is pure, and has a different digest than the original, then
	// add that different digest as a cache key for this val.
	// This enables APIs to return new object instances with overridden purity and/or digests, e.g. returning
	// values that have a pure content-based cache key different from the call-chain ID digest.
	if idable, ok := val.(IDable); ok && idable != nil {
		valID := idable.ID()

		// only cache pure results
		isPure := !valID.IsTainted()

		// only need to add a new cache key if the returned val has a different custom digest than the original
		digestChanged := valID.Digest() != dig

		// Corner case: the `id` field on an object returns an IDable value (IDs are themselves both values and IDable).
		// However, if we cached `val` in this case, we would be caching <id digest> -> <id value>, which isn't what we
		// want. Instead, we only want to cache <id digest> -> <actual object value>.
		// To avoid this, we check that the returned IDable type is the actual object type.
		matchesType := valID.Type().ToAST().Name() == val.Type().Name()

		if isPure && digestChanged && matchesType {
			newID = valID
			_, _, err := s.Cache.GetOrInitialize(ctx, valID.Digest(), func(context.Context) (Typed, error) {
				return val, nil
			})
			if err != nil {
				return nil, nil, err
			}
		}
	}

	return val, newID, nil
}

type View interface {
	Contains(string) bool
}

// GlobalView is the default global view. Everyone can see it, and it behaves
// identically everywhere.
var GlobalView View = nil

// AllView is similar to the global view, however, instead of being an empty
// view, it's still counted as a view.
//
// This means that each call for a field is associated with the server view,
// which results in slightly different caching behavior. Additionally, it can
// be overridden in different views.
type AllView struct{}

func (v AllView) Contains(s string) bool {
	return true
}

// ExactView contains exactly one view.
type ExactView string

func (v ExactView) Contains(s string) bool {
	return s == string(v)
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

// FuncWithCacheKey is like Func but allows specifying a custom digest that will be used to cache the operation in dagql.
func FuncWithCacheKey[T Typed, A any, R any](
	name string,
	fn func(ctx context.Context, self T, args A) (R, error),
	cacheKeyFn func(ctx context.Context, self Instance[T], args A, origDgst digest.Digest) (digest.Digest, error),
) Field[T] {
	return NodeFuncWithCacheKey(name, func(ctx context.Context, self Instance[T], args A) (R, error) {
		return fn(ctx, self.Self, args)
	}, cacheKeyFn)
}

// NodeFunc is the same as Func, except it passes the Instance instead of the
// receiver so that you can access its ID.
func NodeFunc[T Typed, A any, R any](name string, fn func(ctx context.Context, self Instance[T], args A) (R, error)) Field[T] {
	return NodeFuncWithCacheKey(name, fn, nil)
}

// NodeFuncWithCacheKey is like NodeFunc but allows specifying a custom digest that will be used to cache the operation in dagql.
func NodeFuncWithCacheKey[T Typed, A any, R any](
	name string,
	fn func(ctx context.Context, self Instance[T], args A) (R, error),
	cacheKeyFn func(ctx context.Context, self Instance[T], args A, origDgst digest.Digest) (digest.Digest, error),
) Field[T] {
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

	field := Field[T]{
		Spec: FieldSpec{
			Name: name,
			Args: inputs,
			Type: ret,
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

	if cacheKeyFn != nil {
		field.CacheKeyFunc = func(ctx context.Context, self Instance[T], argVals map[string]Input, origDgst digest.Digest) (digest.Digest, error) {
			if argsErr != nil {
				// this error is deferred until runtime, since it's better (at least
				// more testable) than panicking
				return "", argsErr
			}
			var args A
			if err := setInputFields(inputs, argVals, &args); err != nil {
				return "", err
			}
			return cacheKeyFn(ctx, self, args, origDgst)
		}
	}

	return field
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
	// ImpurityReason indicates that the field's result may change over time.
	ImpurityReason string
	// DeprecatedReason deprecates the field and provides a reason.
	DeprecatedReason string
	// Module is the module that provides the field's implementation.
	Module *call.Module
	// Directives is the list of GraphQL directives attached to this field.
	Directives []*ast.Directive

	// extend is used during installation to copy the spec of a previous field
	// with the same name
	extend bool
}

func (spec FieldSpec) FieldDefinition() *ast.FieldDefinition {
	def := &ast.FieldDefinition{
		Name:        spec.Name,
		Description: spec.Description,
		Arguments:   spec.Args.ArgumentDefinitions(),
		Type:        spec.Type.Type(),
	}
	if len(spec.Directives) > 0 {
		def.Directives = append([]*ast.Directive{}, spec.Directives...)
	}
	if spec.DeprecatedReason != "" {
		def.Directives = append(def.Directives, deprecated(spec.DeprecatedReason))
	}
	if spec.ImpurityReason != "" {
		def.Directives = append(def.Directives, impure(spec.ImpurityReason))
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
	// Sensitive indicates that the value of this arg is sensitive and should be
	// omitted from telemetry.
	Sensitive bool
	// Directives is the list of GraphQL directives attached to this input.
	Directives []*ast.Directive
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
		if len(spec.Directives) > 0 {
			schemaArg.Directives = append([]*ast.Directive{}, spec.Directives...)
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
		if len(spec.Directives) > 0 {
			field.Directives = append([]*ast.Directive{}, spec.Directives...)
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
	TypeDefinition(views ...string) *ast.Definition
}

// Fields defines a set of fields for an Object type.
type Fields[T Typed] []Field[T]

// Install installs the field's Object type if needed, and installs all fields
// into the type.
func (fields Fields[T]) Install(server *Server) {
	// FIXME: shortcut to get our "agent" middleware to work
	// server.installLock.Lock()
	// defer server.installLock.Unlock()
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
				Name:             name,
				Type:             field.Value,
				Description:      field.Field.Tag.Get("doc"),
				DeprecatedReason: field.Field.Tag.Get("deprecated"),
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
		server.installObject(classT)
	} else {
		classT = class.(Class[T])
	}
	return classT
}

// Field defines a field of an Object type.
type Field[T Typed] struct {
	Spec         FieldSpec
	Func         func(context.Context, Instance[T], map[string]Input) (Typed, error)
	CacheKeyFunc func(context.Context, Instance[T], map[string]Input, digest.Digest) (digest.Digest, error)

	// ViewFilter is filter that specifies under which views this field is
	// accessible. If not view is present, the default is the "global" view.
	ViewFilter View
}

func (field Field[T]) Extend() Field[T] {
	field.Spec.extend = true
	return field
}

// View sets a view for this field.
func (field Field[T]) View(view View) Field[T] {
	field.ViewFilter = view
	return field
}

// Doc sets the description of the field. Each argument is joined by two empty
// lines.
func (field Field[T]) Doc(paras ...string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	field.Spec.Description = FormatDescription(paras...)
	return field
}

func (field Field[T]) ArgDoc(name string, paras ...string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	for i, arg := range field.Spec.Args {
		if arg.Name == name {
			field.Spec.Args[i].Description = FormatDescription(paras...)
			return field
		}
	}
	panic(fmt.Sprintf("field %s has no such argument: %q", field.Spec.Name, name))
}

func (field Field[T]) ArgSensitive(name string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	for i, arg := range field.Spec.Args {
		if arg.Name == name {
			field.Spec.Args[i].Sensitive = true
			return field
		}
	}
	panic(fmt.Sprintf("field %s has no such argument: %q", field.Spec.Name, name))
}

func (field Field[T]) ArgDeprecated(name string, paras ...string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	for i, arg := range field.Spec.Args {
		if arg.Name == name {
			reason := FormatDescription(paras...)
			field.Spec.Args[i].DeprecatedReason = reason
			if field.Spec.Args[i].Description == "" {
				field.Spec.Args[i].Description = deprecationDescription(reason)
			}
			return field
		}
	}
	panic(fmt.Sprintf("field %s has no such argument: %q", field.Spec.Name, name))
}

func (field Field[T]) ArgRemove(name string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}

	args := make(InputSpecs, 0, len(field.Spec.Args)-1)
	for _, arg := range field.Spec.Args {
		if arg.Name == name {
			continue
		}
		args = append(args, arg)
	}
	if len(args) == len(field.Spec.Args) {
		panic(fmt.Sprintf("field %s has no such argument: %q", field.Spec.Name, name))
	}

	field.Spec.Args = args
	return field
}

func FormatDescription(paras ...string) string {
	for i, p := range paras {
		paras[i] = strings.Join(strings.Fields(strings.TrimSpace(p)), " ")
	}
	return strings.Join(paras, "\n\n")
}

// Deprecated marks the field as deprecated, meaning it should not be used by
// new code.
func (field Field[T]) Deprecated(paras ...string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	field.Spec.DeprecatedReason = FormatDescription(paras...)
	return field
}

// Impure marks the field as "impure", meaning its result may change over time,
// or it has side effects.
func (field Field[T]) Impure(reason string, paras ...string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	field.Spec.ImpurityReason = FormatDescription(append([]string{reason}, paras...)...)
	return field
}

// Meta indicates that the field has no impact on the field's result.
func (field Field[T]) Meta() Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	field.Spec.Meta = true
	return field
}

// FieldDefinition returns the schema definition of the field.
func (field Field[T]) FieldDefinition() *ast.FieldDefinition {
	spec := field.Spec
	if spec.Type == nil {
		panic(fmt.Errorf("field %q has no type", spec.Name))
	}
	return field.Spec.FieldDefinition()
}

func definition(kind ast.DefinitionKind, val Type, views ...string) *ast.Definition {
	var def *ast.Definition
	if isType, ok := val.(Definitive); ok {
		def = isType.TypeDefinition(views...)
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
		spec := InputSpec{
			Name:             field.Name,
			Description:      field.Field.Tag.Get("doc"),
			Type:             input,
			Default:          inputDef,
			DeprecatedReason: field.Field.Tag.Get("deprecated"),
			Sensitive:        field.Field.Tag.Get("sensitive") == "true",
		}
		if spec.Description == "" && spec.DeprecatedReason != "" {
			spec.Description = deprecationDescription(spec.DeprecatedReason)
		}
		specs[i] = spec
	}
	return specs, nil
}

func deprecationDescription(reason string) string {
	return fmt.Sprintf("DEPRECATED: %s", reason)
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
	for i := range objT.NumField() {
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
	for i := range objT.NumField() {
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
	for i := range destT.NumField() {
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
			return fmt.Errorf("assign input %q (%T) as %+v (%T): %w",
				spec.Name,
				fieldV.Interface(),
				val,
				val,
				err,
			)
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

func appendAssign(slice reflect.Value, val any) error {
	if reflect.TypeOf(val).AssignableTo(slice.Type().Elem()) {
		slice.Set(reflect.Append(slice, reflect.ValueOf(val)))
		return nil
	} else if setter, ok := val.(Setter); ok {
		return setter.SetField(slice)
	} else {
		return fmt.Errorf("cannot assign %T to %s", val, slice.Type())
	}
}
