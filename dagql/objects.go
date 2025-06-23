package dagql

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/cache"
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

	invalidateSchemaCache func()
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
func NewClass[T Typed](srv *Server, opts_ ...ClassOpts[T]) Class[T] {
	var opts ClassOpts[T]
	if len(opts_) > 0 {
		opts = opts_[0]
	}
	class := Class[T]{
		inner:   opts.Typed,
		fields:  map[string][]*Field[T]{},
		fieldsL: new(sync.Mutex),

		invalidateSchemaCache: srv.invalidateSchemaCache,
	}
	if !opts.NoIDs {
		class.Install(
			Field[T]{
				Spec: &FieldSpec{
					Name:        "id",
					Description: fmt.Sprintf("A unique identifier for this %s.", class.TypeName()),
					Type:        ID[T]{inner: opts.Typed},
				},
				Func: func(ctx context.Context, self ObjectInstance[T], args map[string]Input, view View) (Value, error) {
					// TODO: instance of an ID is weird, but okay I guess?
					id := NewDynamicID[T](self.ID(), opts.Typed)
					return NewInstanceForCurrentID(ctx, id)
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

func (class Class[T]) Field(name string, view View) (Field[T], bool) {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	return class.fieldLocked(name, view)
}

func (class Class[T]) FieldSpec(name string, view View) (FieldSpec, bool) {
	field, ok := class.Field(name, view)
	if !ok {
		return FieldSpec{}, false
	}
	return *field.Spec, true
}

func (class Class[T]) fieldLocked(name string, view View) (Field[T], bool) {
	fields, ok := class.fields[name]
	if !ok {
		return Field[T]{}, false
	}
	for i := len(fields) - 1; i >= 0; i-- {
		// iterate backwards to allow last-defined field to have precedence
		field := fields[i]

		if field.Spec.ViewFilter == nil {
			return *field, true
		}
		if field.Spec.ViewFilter.Contains(view) {
			return *field, true
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
			oldSpec := *field.Spec
			newSpec := *fields[len(fields)-1].Spec

			field.Spec = &newSpec
			// a little hacky, but preserve a couple values
			field.Spec.Type = oldSpec.Type
			field.Spec.ViewFilter = oldSpec.ViewFilter
		}

		for _, other := range class.fields[field.Spec.Name] {
			if field.Spec.ViewFilter == nil && other.Spec.ViewFilter != nil {
				panic(fmt.Sprintf("field %q cannot be added to the global view, it already has a view", field.Spec.Name))
			}
			if field.Spec.ViewFilter != nil && other.Spec.ViewFilter == nil {
				panic(fmt.Sprintf("field %q cannot be added with a view, it's already in the global view", field.Spec.Name))
			}
		}

		class.fields[field.Spec.Name] = append(class.fields[field.Spec.Name], &field)
	}
	if class.invalidateSchemaCache != nil {
		class.invalidateSchemaCache()
	}
}

var _ ObjectType = Class[Typed]{}

func (class Class[T]) TypeName() string {
	return class.inner.Type().Name()
}

func (class Class[T]) Extend(spec FieldSpec, fun FieldFunc, cacheSpec CacheSpec) {
	class.fieldsL.Lock()
	f := &Field[T]{
		Spec: &spec,
		Func: func(ctx context.Context, self ObjectInstance[T], args map[string]Input, view View) (Value, error) {
			return fun(ctx, self, args)
		},
	}
	f.CacheSpec = cacheSpec
	class.fields[spec.Name] = append(class.fields[spec.Name], f)
	class.fieldsL.Unlock()

	// Invalidate cache after releasing the lock to avoid Class[...].fieldsL and
	// *Server.schemaLock deadlock if the schema is concurrently introspected and
	// updated (via Extend)
	if class.invalidateSchemaCache != nil {
		class.invalidateSchemaCache()
	}
}

// TypeDefinition returns the schema definition of the class.
//
// The definition is derived from the type name, description, and fields. The
// type may implement Definitive or Descriptive to provide more information.
//
// Each currently defined field is installed on the returned definition.
func (class Class[T]) TypeDefinition(view View) *ast.Definition {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	var val any = class.inner
	var def *ast.Definition
	if isType, ok := val.(Definitive); ok {
		def = isType.TypeDefinition(view)
	} else {
		def = &ast.Definition{
			Kind: ast.Object,
			Name: class.inner.Type().Name(),
		}
	}
	if isType, ok := val.(Descriptive); ok {
		def.Description = isType.TypeDescription()
	}
	for name := range class.fields {
		if field, ok := class.fieldLocked(name, view); ok {
			def.Fields = append(def.Fields, field.FieldDefinition(view))
		}
	}
	// TODO preserve order
	sort.Slice(def.Fields, func(i, j int) bool {
		return def.Fields[i].Name < def.Fields[j].Name
	})
	return def
}

// ParseField parses a field selection into a Selector and return type.
func (class Class[T]) ParseField(ctx context.Context, view View, astField *ast.Field, vars map[string]any) (Selector, *ast.Type, error) {
	field, ok := class.Field(astField.Name, view)
	if !ok {
		return Selector{}, nil, fmt.Errorf("%s has no such field: %q", class.TypeName(), astField.Name)
	}
	args := make([]NamedInput, len(astField.Arguments))
	for i, arg := range astField.Arguments {
		argSpec, ok := field.Spec.Args.Input(arg.Name, view)
		if !ok {
			return Selector{}, nil, fmt.Errorf("%s.%s has no such argument: %q", class.TypeName(), field.Spec.Name, arg.Name)
		}

		if argSpec.Internal {
			return Selector{}, nil, fmt.Errorf("cannot use internal argument %q in selector for %s.%s", arg.Name, class.TypeName(), field.Spec.Name)
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
	if field.Spec.ViewFilter == nil {
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
func (class Class[T]) New(val Value) (ObjectValue, error) {
	if objInstance, ok := val.(ObjectInstance[T]); ok {
		return objInstance, nil
	}
	if inst, ok := val.(instance[T]); ok {
		return objectInstance[T]{
			instance: inst,
			Class:    class,
		}, nil
	}

	self, ok := UnwrapAs[T](val)
	if !ok {
		return nil, fmt.Errorf("cannot instantiate %T with %T", class, val)
	}

	return objectInstance[T]{
		instance: instance[T]{
			Constructor: val.ID(),
			self:        self,
		},
		Class: class,
	}, nil
}

// Call calls a field on the class against an instance.
func (class Class[T]) Call(
	ctx context.Context,
	srv *Server,
	node ObjectInstance[T],
	fieldName string,
	view View,
	args map[string]Input,
) (*CacheValWithCallbacks, error) {
	field, ok := class.Field(fieldName, view)
	if !ok {
		return nil, fmt.Errorf("Call: %s has no such field: %q", class.inner.Type().Name(), fieldName)
	}

	val, err := field.Func(ctx, node, args, view)
	if err != nil {
		return nil, err
	}

	// field implementations can optionally return a wrapped Typed val that has
	// a callback that should always run after the field is called
	var postCall cache.PostCallFunc
	if val != nil {
		postCall = val.GetPostCall()
	}

	// they can also return types that need to run a callback when they are
	// removed from the cache (to clean up or release any state)
	// TODO: should OnRelease just be moved to Value/Instance now?
	var onRelease cache.OnReleaseFunc
	if onReleaser, ok := UnwrapAs[OnReleaser](val); ok {
		onRelease = onReleaser.OnRelease
	}

	return &CacheValWithCallbacks{
		Value:     val,
		PostCall:  postCall,
		OnRelease: onRelease,
	}, nil
}

// TODO: doc, for Value
type Instance[T Typed] interface {
	Value
	Self() T
	WithDigest(digest.Digest) Instance[T]
	InstanceWithPostCall(fn cache.PostCallFunc) Instance[T]
}

type instance[T Typed] struct {
	Constructor *call.ID
	self        T
	postCall    cache.PostCallFunc
}

var _ Instance[Typed] = instance[Typed]{}

func (o instance[T]) AstType() *ast.Type {
	return o.self.Type()
}

/*
func (o instance[T]) Type() *ast.Type {
	return o.self.Type()
}
*/

// ID returns the ID of the instance.
func (r instance[T]) ID() *call.ID {
	return r.Constructor
}

func (r instance[T]) Self() T {
	return r.self
}

func (r instance[T]) SetField(field reflect.Value) error {
	/* TODO: this fails when you have e.g. an int instead of a dagql.Int....
	typedIface := reflect.TypeOf((*Typed)(nil)).Elem()
	if !field.Type().Implements(typedIface) {
		return fmt.Errorf("field %q does not implement Typed", field.Type())
	}
	*/
	return assign(field, r.self)
}

// Unwrap returns the inner value of the instance.
func (r instance[T]) Unwrap() Typed {
	return r.self
}

func (r instance[T]) DerefValue() (Value, bool) {
	derefableSelf, ok := any(r.self).(DerefableValue)
	if !ok {
		return r, true
	}
	return derefableSelf.DerefToValue(r.Constructor, r.postCall)
}

func (r instance[T]) NthValue(nth int) (Value, error) {
	// TODO:
	// TODO:
	// TODO:
	/*
		fmt.Println("Instance.NthValue",
			"id", r.Constructor.Display(),
			fmt.Sprintf("%T", r.self),
		)
	*/

	enumerableSelf, ok := any(r.self).(Enumerable)
	if !ok {
		return nil, fmt.Errorf("cannot get %dth value from %T", nth, r.self)
	}
	// TODO: postCall dropped here, that's correct, right?
	return enumerableSelf.NthValue(nth, r.Constructor)
}

func (r instance[T]) WithPostCall(fn cache.PostCallFunc) Value {
	r.postCall = fn
	return r
}

func (r instance[T]) InstanceWithPostCall(fn cache.PostCallFunc) Instance[T] {
	r.postCall = fn
	return r
}

// WithDigest returns an updated instance with the given metadata set.
// customDigest overrides the default digest of the instance to the provided value.
// NOTE: customDigest must be used with care as any instances with the same digest
// will be considered equivalent and can thus replace each other in the cache.
// Generally, customDigest should be used when there's a content-based digest available
// that won't be caputured by the default, call-chain derived digest.
func (r instance[T]) WithDigest(customDigest digest.Digest) Instance[T] {
	return instance[T]{
		Constructor: r.Constructor.WithDigest(customDigest),
		self:        r.self,
	}
}

// String returns the instance in Class@sha256:... format.
func (r instance[T]) String() string {
	return fmt.Sprintf("%s@%s", r.self.Type().Name(), r.Constructor.Digest())
}

func (r instance[T]) GetPostCall() cache.PostCallFunc {
	return r.postCall
}

type ObjectInstance[T Typed] interface {
	Instance[T]
	ObjectValue
	WithObjectDigest(digest.Digest) ObjectInstance[T]
}

type objectInstance[T Typed] struct {
	instance[T]
	Class Class[T]
}

// ObjectType returns the ObjectType of the instance.
func (r objectInstance[T]) ObjectType() ObjectType {
	return r.Class
}

func (r objectInstance[T]) WithObjectDigest(customDigest digest.Digest) ObjectInstance[T] {
	return objectInstance[T]{
		instance: instance[T]{
			Constructor: r.Constructor.WithDigest(customDigest),
			self:        r.self,
		},
		Class: r.Class,
	}
}

func NoopDone(res Value, cached bool, rerr error) {}

// Select calls the field on the instance specified by the selector
func (r objectInstance[T]) Select(ctx context.Context, s *Server, sel Selector) (Value, error) {
	preselectResult, err := r.preselect(ctx, s, sel)
	if err != nil {
		return nil, err
	}
	return r.call(ctx, s, preselectResult.newID, preselectResult.inputArgs, preselectResult.doNotCache)
}

type preselectResult struct {
	inputArgs  map[string]Input
	newID      *call.ID
	doNotCache bool
}

// sortArgsToSchema sorts the arguments to match the schema definition order.
func (r objectInstance[T]) sortArgsToSchema(fieldSpec *FieldSpec, view View, idArgs []*call.Argument) {
	inputs := fieldSpec.Args.Inputs(view)
	sort.Slice(idArgs, func(i, j int) bool {
		iIdx := slices.IndexFunc(inputs, func(input InputSpec) bool {
			return input.Name == idArgs[i].Name()
		})
		jIdx := slices.IndexFunc(inputs, func(input InputSpec) bool {
			return input.Name == idArgs[j].Name()
		})
		return iIdx < jIdx
	})
}

func (r objectInstance[T]) preselect(ctx context.Context, s *Server, sel Selector) (*preselectResult, error) {
	view := sel.View
	field, ok := r.Class.Field(sel.Field, view)
	if !ok {
		return nil, fmt.Errorf("Select: %s has no such field: %q", r.Class.TypeName(), sel.Field)
	}
	if field.Spec.ViewFilter == nil {
		// fields in the global view shouldn't attach the current view to the
		// selector (since they're global from all perspectives)
		view = ""
	}

	idArgs := make([]*call.Argument, 0, len(sel.Args))
	inputArgs := make(map[string]Input, len(sel.Args))
	for _, argSpec := range field.Spec.Args.Inputs(view) {
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
			return nil, fmt.Errorf("missing required argument: %q", argSpec.Name)
		}
	}

	r.sortArgsToSchema(field.Spec, view, idArgs)

	astType := field.Spec.Type.Type()
	if sel.Nth != 0 {
		astType = astType.Elem
	}

	newID := r.Constructor.Append(
		astType,
		sel.Field,
		string(view),
		field.Spec.Module,
		sel.Nth,
		"",
		idArgs...,
	)

	doNotCache := field.CacheSpec.DoNotCache != ""
	if field.CacheSpec.GetCacheConfig != nil {
		origDgst := newID.Digest()

		cacheCfgCtx := idToContext(ctx, newID)
		cacheCfgCtx = srvToContext(cacheCfgCtx, s)
		cacheCfg, err := field.CacheSpec.GetCacheConfig(cacheCfgCtx, r, inputArgs, view, CacheConfig{
			Digest: origDgst,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to compute cache key for %s.%s: %w", r.self.Type().Name(), sel.Field, err)
		}

		if len(cacheCfg.UpdatedArgs) > 0 {
			maps.Copy(inputArgs, cacheCfg.UpdatedArgs)
			for argName, argInput := range cacheCfg.UpdatedArgs {
				var found bool
				for i, idArg := range idArgs {
					if idArg.Name() == argName {
						idArgs[i] = idArg.WithValue(argInput.ToLiteral())
						found = true
						break
					}
				}
				if !found {
					idArgs = append(idArgs, call.NewArgument(
						argName,
						argInput.ToLiteral(),
						false,
					))
				}
			}
			r.sortArgsToSchema(field.Spec, view, idArgs)
			newID = r.Constructor.Append(
				astType,
				sel.Field,
				string(view),
				field.Spec.Module,
				sel.Nth,
				"",
				idArgs...,
			)
		}

		if cacheCfg.Digest != origDgst {
			newID = newID.WithDigest(cacheCfg.Digest)
		}
	}

	return &preselectResult{
		inputArgs:  inputArgs,
		newID:      newID,
		doNotCache: doNotCache,
	}, nil
}

// Call calls the field on the instance specified by the ID.
func (r objectInstance[T]) Call(ctx context.Context, s *Server, newID *call.ID) (Value, error) {
	fieldName := newID.Field()
	view := View(newID.View())
	field, ok := r.Class.Field(fieldName, view)
	if !ok {
		return nil, fmt.Errorf("Call: %s has no such field: %q", r.Class.TypeName(), fieldName)
	}

	idArgs := newID.Args()
	inputArgs := make(map[string]Input, len(idArgs))
	for _, argSpec := range field.Spec.Args.Inputs(view) {
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
				return nil, fmt.Errorf("Call: init arg %q value as %T (%s) using %T: %w", argSpec.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
			}
			inputArgs[argSpec.Name] = input

		case argSpec.Default != nil:
			inputArgs[argSpec.Name] = argSpec.Default

		case argSpec.Type.Type().NonNull:
			// error out if the arg is missing but required
			return nil, fmt.Errorf("missing required argument: %q", argSpec.Name)
		}
	}

	doNotCache := field.CacheSpec.DoNotCache != ""
	return r.call(ctx, s, newID, inputArgs, doNotCache)
}

func (r objectInstance[T]) call(
	ctx context.Context,
	s *Server,
	newID *call.ID,
	inputArgs map[string]Input,
	doNotCache bool,
) (Value, error) {
	ctx = idToContext(ctx, newID)
	ctx = srvToContext(ctx, s)
	callCacheKey := newID.Digest()
	if doNotCache {
		callCacheKey = ""
	}

	var opts []CacheCallOpt
	if s.telemetry != nil {
		opts = append(opts, WithTelemetry(func(ctx context.Context) (context.Context, func(Value, bool, error)) {
			return s.telemetry(ctx, r, newID)
		}))
	}
	res, err := s.Cache.GetOrInitializeWithCallbacks(ctx, callCacheKey, true, func(ctx context.Context) (*CacheValWithCallbacks, error) {
		valWithCallbacks, err := r.Class.Call(ctx, s, r, newID.Field(), View(newID.View()), inputArgs)
		if err != nil {
			return nil, err
		}
		val := valWithCallbacks.Value

		if val == nil {
			return nil, nil
		}

		val, ok := val.DerefValue()
		if !ok {
			return nil, nil
		}
		nth := int(newID.Nth())
		if nth != 0 {
			val, err = val.NthValue(nth)
			if err != nil {
				return nil, fmt.Errorf("cannot get %dth value from %T: %w", nth, val, err)
			}
			val, ok = val.DerefValue()
			if !ok {
				return nil, nil
			}
		}

		return &CacheValWithCallbacks{
			Value:     val,
			PostCall:  valWithCallbacks.PostCall,
			OnRelease: valWithCallbacks.OnRelease,
		}, nil
	}, opts...)

	if err != nil {
		return nil, err
	}
	if err := res.PostCall(ctx); err != nil {
		return nil, fmt.Errorf("post-call error: %w", err)
	}
	val := res.Result()

	// If the returned val is IDable and has a different digest than the original, then
	// add that different digest as a cache key for this val.
	// This enables APIs to return new object instances with overridden purity and/or digests, e.g. returning
	// values that have a pure content-based cache key different from the call-chain ID digest.
	if idable, ok := val.(IDable); ok && idable != nil && !doNotCache {
		valID := idable.ID()

		// only need to add a new cache key if the returned val has a different custom digest than the original
		digestChanged := valID.Digest() != newID.Digest()

		// Corner case: the `id` field on an object returns an IDable value (IDs are themselves both values and IDable).
		// However, if we cached `val` in this case, we would be caching <id digest> -> <id value>, which isn't what we
		// want. Instead, we only want to cache <id digest> -> <actual object value>.
		// To avoid this, we check that the returned IDable type is the actual object type.
		matchesType := valID.Type().ToAST().Name() == val.AstType().Name()

		if digestChanged && matchesType {
			newID = valID
			_, err := s.Cache.GetOrInitializeValue(ctx, valID.Digest(), val)
			if err != nil {
				return nil, err
			}
		}
	}

	return val, nil
}

type View string

type ViewFilter interface {
	Contains(View) bool
}

// GlobalView is the default global view. Everyone can see it, and it behaves
// identically everywhere.
var GlobalView ViewFilter = nil

// AllView is similar to the global view, however, instead of being an empty
// view, it's still counted as a view.
//
// This means that each call for a field is associated with the server view,
// which results in slightly different caching behavior. Additionally, it can
// be overridden in different views.
type AllView struct{}

func (AllView) Contains(view View) bool {
	return true
}

// ExactView contains exactly one view.
type ExactView string

func (exact ExactView) Contains(view View) bool {
	return string(exact) == string(view)
}

type (
	FuncHandler[T Typed, A any, R any]       func(ctx context.Context, self T, args A) (R, error)
	NodeFuncHandler[T Typed, A any, R Typed] func(ctx context.Context, self ObjectInstance[T], args A) (Instance[R], error)
)

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
func Func[T Typed, A any, R any](name string, fn FuncHandler[T, A, R]) Field[T] {
	return FuncWithCacheKey(name, fn, nil)
}

// FuncWithCacheKey is like Func but allows specifying a custom digest that will be used to cache the operation in dagql.
func FuncWithCacheKey[T Typed, A any, R any](
	name string,
	fn FuncHandler[T, A, R],
	cacheFn GetCacheConfigFunc[T, A],
) Field[T] {
	var zeroR R
	switch zeroR := any(zeroR).(type) {
	case Value:
		// TODO: dedupe this code
		var zeroArgs A
		inputs, argsErr := InputSpecsForType(zeroArgs, true)
		if argsErr != nil {
			var zeroSelf T
			slog.Error("failed to parse args", "type", zeroSelf.Type(), "field", name, "error", argsErr)
		}

		spec := &FieldSpec{
			Name: name,
			Args: inputs,
			Type: zeroR.Unwrap(),
		}
		field := Field[T]{
			Spec: spec,
			Func: func(ctx context.Context, self ObjectInstance[T], argVals map[string]Input, view View) (Value, error) {
				if argsErr != nil {
					// this error is deferred until runtime, since it's better (at least
					// more testable) than panicking
					return nil, argsErr
				}
				var args A
				if err := spec.Args.Decode(argVals, &args, view); err != nil {
					return nil, err
				}
				res, err := fn(ctx, self.Self(), args)
				if err != nil {
					return nil, fmt.Errorf("Func: %s.%s: %w", self.Self().Type().Name(), name, err)
				}

				retVal, ok := any(res).(Value)
				if !ok {
					// shouldn't be possible
					return nil, fmt.Errorf("expected %T to be a Value, got %T", res, res)
				}

				return retVal, nil
			},
		}

		if cacheFn != nil {
			field.CacheSpec.GetCacheConfig = func(ctx context.Context, self Value, argVals map[string]Input, view View, baseCfg CacheConfig) (*CacheConfig, error) {
				if argsErr != nil {
					// this error is deferred until runtime, since it's better (at least
					// more testable) than panicking
					return nil, argsErr
				}
				var args A
				if err := spec.Args.Decode(argVals, &args, view); err != nil {
					return nil, err
				}
				inst, ok := self.(ObjectInstance[T])
				if !ok {
					return nil, fmt.Errorf("expected object instance of %T, got %T", field, self)
				}
				return cacheFn(ctx, inst, args, baseCfg)
			}
		}

		return field

	case Typed:
		// TODO: dedupe this code with NodeFunc to extent possible
		var zeroArgs A
		inputs, argsErr := InputSpecsForType(zeroArgs, true)
		if argsErr != nil {
			var zeroSelf T
			slog.Error("failed to parse args", "type", zeroSelf.Type(), "field", name, "error", argsErr)
		}

		spec := &FieldSpec{
			Name: name,
			Args: inputs,
			Type: zeroR,
		}
		field := Field[T]{
			Spec: spec,
			Func: func(ctx context.Context, self ObjectInstance[T], argVals map[string]Input, view View) (Value, error) {
				if argsErr != nil {
					// this error is deferred until runtime, since it's better (at least
					// more testable) than panicking
					return nil, argsErr
				}
				var args A
				if err := spec.Args.Decode(argVals, &args, view); err != nil {
					return nil, err
				}
				res, err := fn(ctx, self.Self(), args)
				if err != nil {
					return nil, fmt.Errorf("Func: %s.%s: %w", self.Self().Type().Name(), name, err)
				}
				typedRes, err := builtinOrTyped(res)
				if err != nil {
					return nil, fmt.Errorf("expected %T to be a Typed value, got %T: %w", res, res, err)
				}

				ret, err := NewInstanceForCurrentID(ctx, typedRes)
				if err != nil {
					return nil, fmt.Errorf("Func: %s.%s: %w", self.Self().Type().Name(), name, err)
				}
				return ret, nil
			},
		}

		if cacheFn != nil {
			field.CacheSpec.GetCacheConfig = func(ctx context.Context, self Value, argVals map[string]Input, view View, baseCfg CacheConfig) (*CacheConfig, error) {
				if argsErr != nil {
					// this error is deferred until runtime, since it's better (at least
					// more testable) than panicking
					return nil, argsErr
				}
				var args A
				if err := spec.Args.Decode(argVals, &args, view); err != nil {
					return nil, err
				}
				inst, ok := self.(ObjectInstance[T])
				if !ok {
					return nil, fmt.Errorf("expected object instance of %T, got %T", field, self)
				}
				return cacheFn(ctx, inst, args, baseCfg)
			}
		}

		return field

	case string:
		return nodeFuncForBuiltin[T, A, R, String](name, fn, cacheFn)
	case *string:
		return nodeFuncForBuiltin[T, A, R, Nullable[String]](name, fn, cacheFn)
	case []string:
		return nodeFuncForBuiltin[T, A, R, Array[String]](name, fn, cacheFn)

	case int, int32, int64:
		return nodeFuncForBuiltin[T, A, R, Int](name, fn, cacheFn)
	case *int, *int32, *int64:
		return nodeFuncForBuiltin[T, A, R, Nullable[Int]](name, fn, cacheFn)
	case []int, []int32, []int64:
		return nodeFuncForBuiltin[T, A, R, Array[Int]](name, fn, cacheFn)

	case float32, float64:
		return nodeFuncForBuiltin[T, A, R, Float](name, fn, cacheFn)
	case *float32, *float64:
		return nodeFuncForBuiltin[T, A, R, Nullable[Float]](name, fn, cacheFn)
	case []float32, []float64:
		return nodeFuncForBuiltin[T, A, R, Array[Float]](name, fn, cacheFn)

	case bool:
		return nodeFuncForBuiltin[T, A, R, Boolean](name, fn, cacheFn)
	case *bool:
		return nodeFuncForBuiltin[T, A, R, Nullable[Boolean]](name, fn, cacheFn)
	case []bool:
		return nodeFuncForBuiltin[T, A, R, Array[Boolean]](name, fn, cacheFn)

	default:
		// TODO: avoid panic?
		panic(fmt.Sprintf("%T is not a Typed or supported builtin value", zeroR))
	}
}

func nodeFuncForBuiltin[T Typed, A any, R1 any, R2 Typed](
	name string,
	fn FuncHandler[T, A, R1],
	cacheFn GetCacheConfigFunc[T, A],
) Field[T] {
	return NodeFuncWithCacheKey(name, func(ctx context.Context, self ObjectInstance[T], args A) (Instance[R2], error) {
		var zeroR2 R2
		res, err := fn(ctx, self.Self(), args)
		if err != nil {
			return nil, err
		}
		typedRes, err := builtinOrTyped(res)
		if err != nil {
			return nil, fmt.Errorf("expected %T to be a Typed value, got %T: %w", res, res, err)
		}
		ret, ok := typedRes.(R2)
		if !ok {
			return nil, fmt.Errorf("expected %T to be a %T, got %T", res, zeroR2, res)
		}

		retInst, err := NewInstanceForCurrentID(ctx, ret)
		if err != nil {
			return nil, fmt.Errorf("Func: %s.%s: %w", self.Self().Type().Name(), name, err)
		}
		return retInst, nil
	}, cacheFn)
}

// NodeFunc is the same as Func, except it passes the Instance instead of the
// receiver so that you can access its ID.
func NodeFunc[T Typed, A any, R Typed](name string, fn NodeFuncHandler[T, A, R]) Field[T] {
	return NodeFuncWithCacheKey(name, fn, nil)
}

// NodeFuncWithCacheKey is like NodeFunc but allows specifying a custom digest that will be used to cache the operation in dagql.
func NodeFuncWithCacheKey[T Typed, A any, R Typed](
	name string,
	fn NodeFuncHandler[T, A, R],
	cacheFn GetCacheConfigFunc[T, A],
) Field[T] {
	var zeroArgs A
	inputs, argsErr := InputSpecsForType(zeroArgs, true)
	if argsErr != nil {
		var zeroSelf T
		slog.Error("failed to parse args", "type", zeroSelf.Type(), "field", name, "error", argsErr)
	}

	var zeroRet R
	spec := &FieldSpec{
		Name: name,
		Args: inputs,
		Type: zeroRet,
	}
	field := Field[T]{
		Spec: spec,
		Func: func(ctx context.Context, self ObjectInstance[T], argVals map[string]Input, view View) (Value, error) {
			if argsErr != nil {
				// this error is deferred until runtime, since it's better (at least
				// more testable) than panicking
				return nil, argsErr
			}
			var args A
			if err := spec.Args.Decode(argVals, &args, view); err != nil {
				return nil, err
			}
			res, err := fn(ctx, self, args)
			if err != nil {
				return nil, err
			}
			return res, nil
		},
	}

	if cacheFn != nil {
		field.CacheSpec.GetCacheConfig = func(ctx context.Context, self Value, argVals map[string]Input, view View, baseCfg CacheConfig) (*CacheConfig, error) {
			if argsErr != nil {
				// this error is deferred until runtime, since it's better (at least
				// more testable) than panicking
				return nil, argsErr
			}
			var args A
			if err := spec.Args.Decode(argVals, &args, view); err != nil {
				return nil, err
			}
			inst, ok := self.(ObjectInstance[T])
			if !ok {
				return nil, fmt.Errorf("expected instance of %T, got %T", field, self)
			}
			return cacheFn(ctx, inst, args, baseCfg)
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
	// Sensitive indicates that the value returned by this field is sensitive and
	// should not be displayed in telemetry.
	Sensitive bool
	// DeprecatedReason deprecates the field and provides a reason.
	DeprecatedReason string
	// ExperimentalReason marks the field as experimental and provides a reason.
	ExperimentalReason string
	// Module is the module that provides the field's implementation.
	Module *call.Module
	// Directives is the list of GraphQL directives attached to this field.
	Directives []*ast.Directive

	// ViewFilter is filter that specifies under which views this field is
	// accessible. If not view is present, the default is the "global" view.
	ViewFilter ViewFilter

	// extend is used during installation to copy the spec of a previous field
	// with the same name
	extend bool
}

func (spec FieldSpec) FieldDefinition(view View) *ast.FieldDefinition {
	def := &ast.FieldDefinition{
		Name:        spec.Name,
		Description: spec.Description,
		Arguments:   spec.Args.ArgumentDefinitions(view),
		Type:        spec.Type.Type(),
	}
	if len(spec.Directives) > 0 {
		def.Directives = slices.Clone(spec.Directives)
	}
	if spec.DeprecatedReason != "" {
		def.Directives = append(def.Directives, deprecated(spec.DeprecatedReason))
	}
	if spec.ExperimentalReason != "" {
		def.Directives = append(def.Directives, experimental(spec.ExperimentalReason))
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
	// ExperimentalReason marks the field as experimental and provides a reason.
	ExperimentalReason string
	// Sensitive indicates that the value of this arg is sensitive and should be
	// omitted from telemetry.
	Sensitive bool
	// Directives is the list of GraphQL directives attached to this input.
	Directives []*ast.Directive

	// ViewFilter is filter that specifies under which views this field is
	// accessible. If not view is present, the default is the "global" view.
	ViewFilter ViewFilter

	// Internal indicates that this input can only be set by internal server
	// calls, never by external clients. It may appear in IDs sent to/from
	// clients, but can't be set in new graphql queries.
	// This argument will not be exposed in the introspection schema.
	Internal bool
}

func (spec *InputSpec) merge(other *InputSpec) {
	if other.Name != "" {
		spec.Name = other.Name
	}
	if other.Description != "" {
		spec.Description = other.Description
	}
	if other.Type != nil {
		spec.Type = other.Type
	}
	if other.Default != nil {
		spec.Default = other.Default
	}
	if other.DeprecatedReason != "" {
		spec.DeprecatedReason = other.DeprecatedReason
	}
	if other.Sensitive {
		spec.Sensitive = other.Sensitive
	}
	if len(other.Directives) > 0 {
		spec.Directives = slices.Clone(spec.Directives)
		spec.Directives = append(spec.Directives, other.Directives...)
	}
	if other.ViewFilter != nil {
		spec.ViewFilter = other.ViewFilter
	}
	if other.Internal {
		spec.Internal = other.Internal
	}
}

type Argument struct {
	Spec InputSpec
}

func Arg(name string) Argument {
	return Argument{
		Spec: InputSpec{
			Name: name,
		},
	}
}

func (arg Argument) Doc(paras ...string) Argument {
	arg.Spec.Description = FormatDescription(paras...)
	return arg
}

func (arg Argument) Sensitive() Argument {
	arg.Spec.Sensitive = true
	return arg
}

func (arg Argument) Internal() Argument {
	arg.Spec.Internal = true
	return arg
}

func (arg Argument) Default(input Input) Argument {
	arg.Spec.Default = input
	return arg
}

func (arg Argument) View(view ViewFilter) Argument {
	arg.Spec.ViewFilter = view
	return arg
}

func (arg Argument) Deprecated(paras ...string) Argument {
	if len(paras) == 0 && arg.Spec.Description != "" {
		arg.Spec.DeprecatedReason = arg.Spec.Description
		arg.Spec.Description = deprecationDescription(arg.Spec.Description)
		return arg
	}
	arg.Spec.DeprecatedReason = FormatDescription(paras...)
	return arg
}

func (arg Argument) Experimental(paras ...string) Argument {
	if len(paras) == 0 && arg.Spec.Description != "" {
		arg.Spec.ExperimentalReason = arg.Spec.Description
		arg.Spec.Description = experimentalDescription(arg.Spec.Description)
		return arg
	}
	arg.Spec.ExperimentalReason = FormatDescription(paras...)
	return arg
}

type InputSpecs struct {
	// raw is the list of input specs.
	// It should not be accessed directly (hence private), but should instead
	// be accessed through a specific dagql view.
	raw []InputSpec
}

func NewInputSpecs(specs ...InputSpec) InputSpecs {
	return InputSpecs{
		raw: specs,
	}
}

func (specs *InputSpecs) Add(target ...InputSpec) {
	specs.raw = append(specs.raw, target...)
}

func (specs InputSpecs) Input(name string, view View) (InputSpec, bool) {
	for i := len(specs.raw) - 1; i >= 0; i-- {
		// iterate backwards to allow last-defined spec to have precedence
		spec := specs.raw[i]

		if spec.Name != name {
			continue
		}
		if spec.ViewFilter == nil || spec.ViewFilter.Contains(view) {
			return spec, true
		}
	}
	return InputSpec{}, false
}

func (specs InputSpecs) Inputs(view View) (args []InputSpec) {
	seen := make(map[string]bool, len(specs.raw))
	for i := len(specs.raw) - 1; i >= 0; i-- {
		// iterate backwards to allow last-defined spec to have precedence
		spec := specs.raw[i]

		if seen[spec.Name] {
			continue
		}
		if spec.ViewFilter != nil && !spec.ViewFilter.Contains(view) {
			continue
		}
		seen[spec.Name] = true
		args = append(args, spec)
	}
	slices.Reverse(args)
	return args
}

func (specs InputSpecs) ArgumentDefinitions(view View) []*ast.ArgumentDefinition {
	args := specs.Inputs(view)
	defs := make([]*ast.ArgumentDefinition, 0, len(args))

	for _, arg := range args {
		schemaArg := &ast.ArgumentDefinition{
			Name:        arg.Name,
			Description: arg.Description,
			Type:        arg.Type.Type(),
		}
		if arg.Default != nil {
			schemaArg.DefaultValue = arg.Default.ToLiteral().ToAST()
		}
		if len(arg.Directives) > 0 {
			schemaArg.Directives = slices.Clone(arg.Directives)
		}
		if arg.DeprecatedReason != "" {
			schemaArg.Directives = append(schemaArg.Directives, deprecated(arg.DeprecatedReason))
		}
		if arg.ExperimentalReason != "" {
			schemaArg.Directives = append(schemaArg.Directives, experimental(arg.ExperimentalReason))
		}
		if arg.Internal {
			schemaArg.Directives = append(schemaArg.Directives, internal())
		}
		defs = append(defs, schemaArg)
	}
	return defs
}

func (specs InputSpecs) FieldDefinitions(view View) (defs []*ast.FieldDefinition) {
	for _, argDef := range specs.ArgumentDefinitions(view) {
		fieldDef := argDefToFieldDef(argDef)
		defs = append(defs, fieldDef)
	}
	return defs
}

func argDefToFieldDef(arg *ast.ArgumentDefinition) *ast.FieldDefinition {
	return &ast.FieldDefinition{
		Name:         arg.Name,
		Description:  arg.Description,
		Type:         arg.Type,
		DefaultValue: arg.DefaultValue,
		Directives:   arg.Directives,
	}
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
	TypeDefinition(view View) *ast.Definition
}

// Fields defines a set of fields for an Object type.
type Fields[T Typed] []Field[T]

// Install installs the field's Object type if needed, and installs all fields
// into the type.
func (fields Fields[T]) Install(server *Server) {
	class := server.InstallObject(NewClass[T](server)).(Class[T])

	var t T
	objectFields, err := reflectFieldsForType(t, false, builtinOrTyped)
	if err != nil {
		panic(fmt.Errorf("fields for %T: %w", t, err))
	}
	for _, field := range objectFields {
		name := field.Name
		fields = append(fields, Field[T]{
			Spec: &FieldSpec{
				Name:               name,
				Type:               field.Value,
				Description:        field.Field.Tag.Get("doc"),
				DeprecatedReason:   field.Field.Tag.Get("deprecated"),
				ExperimentalReason: field.Field.Tag.Get("experimental"),
			},
			Func: func(ctx context.Context, self ObjectInstance[T], args map[string]Input, view View) (Value, error) {
				t, found, err := getField(ctx, server, self.Self(), false, name)
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

type CacheSpec struct {
	// If set, this GetCacheConfig will be called before ID evaluation to determine the
	// ID's digest. Otherwise the ID defaults to the digest of the call chain.
	GetCacheConfig GenericGetCacheConfigFunc

	// If set, the result of this field will never be cached and not have concurrent equal
	// calls deduped. The string value is a reason why the field should not be cached.
	DoNotCache string
}

type GenericGetCacheConfigFunc func(context.Context, Value, map[string]Input, View, CacheConfig) (*CacheConfig, error)

type GetCacheConfigFunc[T Typed, A any] func(context.Context, ObjectInstance[T], A, CacheConfig) (*CacheConfig, error)

// CacheConfig is the configuration for caching a field. Currently just custom digest
// but intended to support more in time (TTL, etc).
type CacheConfig struct {
	Digest      digest.Digest
	UpdatedArgs map[string]Input
}

// Field defines a field of an Object type.
type Field[T Typed] struct {
	Spec      *FieldSpec
	CacheSpec CacheSpec
	Func      func(context.Context, ObjectInstance[T], map[string]Input, View) (Value, error)
}

func (field Field[T]) Extend() Field[T] {
	field.Spec.extend = true
	return field
}

func (field Field[T]) Sensitive() Field[T] {
	field.Spec.Sensitive = true
	return field
}

// View sets a view for this field.
func (field Field[T]) View(view ViewFilter) Field[T] {
	field.Spec.ViewFilter = view
	return field
}

// DoNotCache marks the field as not to be stored in the cache for the given reason why
func (field Field[T]) DoNotCache(reason string, paras ...string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	field.CacheSpec.DoNotCache = FormatDescription(append([]string{reason}, paras...)...)
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

func (field Field[T]) Args(args ...Argument) Field[T] {
	original := make(map[string]InputSpec, len(field.Spec.Args.raw))
	for _, arg := range field.Spec.Args.raw {
		if arg.Name == "" {
			panic("argument name cannot be empty")
		}
		original[arg.Name] = arg
	}

	newArgs := make([]InputSpec, 0, len(args))
	patched := make(map[string]struct{}, len(args))
	for _, patch := range args {
		arg, ok := original[patch.Spec.Name]
		if !ok {
			panic(fmt.Sprintf("argument %q not found", patch.Spec.Name))
		}
		arg.merge(&patch.Spec)
		newArgs = append(newArgs, arg)
		patched[patch.Spec.Name] = struct{}{}
	}

	// check if there were any original args not patched, if so include them
	// at the end in a stable order
	for _, origArg := range field.Spec.Args.raw {
		if _, ok := patched[origArg.Name]; ok {
			continue
		}
		newArgs = append(newArgs, origArg)
	}

	field.Spec.Args = InputSpecs{newArgs}
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

// Deprecated marks the field as experimental
func (field Field[T]) Experimental(paras ...string) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	field.Spec.ExperimentalReason = FormatDescription(paras...)
	return field
}

// FieldDefinition returns the schema definition of the field.
func (field Field[T]) FieldDefinition(view View) *ast.FieldDefinition {
	if field.Spec.Type == nil {
		panic(fmt.Errorf("field %q has no type", field.Spec.Name))
	}
	return field.Spec.FieldDefinition(view)
}

func definition(kind ast.DefinitionKind, val Type, view View) *ast.Definition {
	var def *ast.Definition
	if isType, ok := val.(Definitive); ok {
		def = isType.TypeDefinition(view)
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

func InputSpecsForType(obj any, optIn bool) (InputSpecs, error) {
	fields, err := reflectFieldsForType(obj, optIn, builtinOrInput)
	if err != nil {
		return InputSpecs{}, err
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
				return InputSpecs{}, fmt.Errorf("convert default value %q for arg %q: %w", inputDefStr, name, err)
			}
			if input.Type().NonNull {
				input = DynamicOptional{
					Elem: input,
				}
			}
		}
		spec := InputSpec{
			Name:               field.Name,
			Description:        field.Field.Tag.Get("doc"),
			Type:               input,
			Default:            inputDef,
			DeprecatedReason:   field.Field.Tag.Get("deprecated"),
			ExperimentalReason: field.Field.Tag.Get("experimental"),
			Sensitive:          field.Field.Tag.Get("sensitive") == "true",
			Internal:           field.Field.Tag.Get("internal") == "true",
		}
		if spec.Description == "" && spec.DeprecatedReason != "" {
			spec.Description = deprecationDescription(spec.DeprecatedReason)
		}
		if spec.Description == "" && spec.ExperimentalReason != "" {
			spec.Description = experimentalDescription(spec.ExperimentalReason)
		}
		specs[i] = spec
	}
	return InputSpecs{specs}, nil
}

func deprecationDescription(reason string) string {
	return fmt.Sprintf("DEPRECATED: %s", reason)
}

func experimentalDescription(reason string) string {
	return fmt.Sprintf("EXPERIMENTAL: %s", reason)
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
		if name == "" {
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

func getField(
	ctx context.Context,
	srv *Server,
	obj any,
	optIn bool,
	fieldName string,
) (res Value, found bool, rerr error) {
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
			t, found, err := getField(ctx, srv, fieldI, optIn, fieldName)
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

			if val, ok := val.(Value); ok {
				return val, true, nil
			}

			t, err := builtinOrTyped(val)
			if err != nil {
				return nil, false, fmt.Errorf("get field %q: %w", name, err)
			}
			if !t.Type().NonNull && objV.Field(i).IsZero() {
				return nil, true, nil
			}

			retVal, err := NewInstanceForCurrentID(ctx, t)
			if err != nil {
				return nil, false, fmt.Errorf("get field %q: %w", name, err)
			}

			return retVal, true, nil
		}
	}
	return nil, false, nil
}

func (specs InputSpecs) Decode(inputs map[string]Input, dest any, view View) error {
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
			if err := specs.Decode(inputs, val.Interface(), view); err != nil {
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
		spec, found := specs.Input(name, view)
		if !found {
			continue
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
		return fmt.Errorf("assign: cannot assign %T to %s", val, field.Type())
	}
}

func appendAssign(slice reflect.Value, val any) error {
	if slice.Kind() != reflect.Slice {
		return fmt.Errorf("appendAssign: expected slice, got %v", slice.Kind())
	}
	if reflect.TypeOf(val).AssignableTo(slice.Type().Elem()) {
		slice.Set(reflect.Append(slice, reflect.ValueOf(val)))
		return nil
	} else if setter, ok := val.(Setter); ok {
		dst := reflect.New(slice.Type().Elem()).Elem()
		if err := setter.SetField(dst); err != nil {
			return fmt.Errorf("appendAssign: Setter.SetField: %w", err)
		}
		slice.Set(reflect.Append(slice, dst))
		return nil
	} else {
		return fmt.Errorf("appendAssign: cannot assign %T to %s", val, slice.Type())
	}
}
