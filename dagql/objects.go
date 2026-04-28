package dagql

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
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
	fieldsL *sync.RWMutex

	// interfaces records the interfaces this class implements.
	// Uses a map (reference type) so it's shared across value copies of Class.
	interfaces map[string]*Interface

	invalidateSchemaCache func()

	// The inner type sourceMap directive so additional type
	// registered by the engine can store also store its origin.
	sourceMap *ast.Directive
}

var _ ObjectType = Class[Typed]{}

// InterfaceImplementor is implemented by object types that can declare
// interface conformance. Class[T] satisfies this interface.
type InterfaceImplementor interface {
	ImplementInterface(iface *Interface)
	// ImplementInterfaceUnchecked declares interface conformance without
	// performing dagql's structural Satisfies check. This is used when a
	// higher-level layer (e.g. core's IsSubtypeOf) has already validated
	// conformance with richer semantics (covariance, contravariance, etc.).
	ImplementInterfaceUnchecked(iface *Interface)
}

var _ InterfaceImplementor = Class[Typed]{}

// ImplementInterface is the same as Implements but satisfies the
// InterfaceImplementor interface for use via the ObjectType interface.
func (class Class[T]) ImplementInterface(iface *Interface) {
	class.Implements(iface)
}

// ImplementInterfaceUnchecked declares interface conformance without performing
// the dagql structural Satisfies check. Use this when a higher-level type system
// (e.g. core's IsSubtypeOf) has already validated conformance.
func (class Class[T]) ImplementInterfaceUnchecked(iface *Interface) {
	class.fieldsL.Lock()
	class.interfaces[iface.TypeName()] = iface
	class.fieldsL.Unlock()
	iface.addImplementor(class.TypeName())
	if class.invalidateSchemaCache != nil {
		class.invalidateSchemaCache()
	}
}

type ClassOpts[T Typed] struct {
	// NoIDs disables the default "id" field and disables the IDType method.
	NoIDs bool

	// Typed contains the Typed value whose Type() determines the class's type.
	//
	// In the simple case, we can just use a zero-value, but it is also allowed
	// to use a dynamic Typed value.
	Typed T

	// The inner type sourceMap directive so additional type
	// registered by the engine can store also store its origin.
	SourceMap *ast.Directive
}

// NewClass returns a new empty class for a given type.
func NewClass[T Typed](srv *Server, opts_ ...ClassOpts[T]) Class[T] {
	var opts ClassOpts[T]

	for _, o := range opts_ {
		if o.NoIDs {
			opts.NoIDs = true
		}

		if any(o.Typed) != nil {
			opts.Typed = o.Typed
		}

		if o.SourceMap != nil {
			opts.SourceMap = o.SourceMap
		}
	}

	class := Class[T]{
		inner:      opts.Typed,
		fields:     map[string][]*Field[T]{},
		fieldsL:    new(sync.RWMutex),
		interfaces: map[string]*Interface{},
		sourceMap:  opts.SourceMap,

		invalidateSchemaCache: srv.invalidateSchemaCache,
	}
	if !opts.NoIDs {
		class.Install(
			Field[T]{
				Spec: &FieldSpec{
					Name:        "id",
					Description: fmt.Sprintf("A unique identifier for this %s.", class.TypeName()),
					Type:        AnyID{},
					Args: NewInputSpecs(
						InputSpec{
							Name:        "recipe",
							Description: "Return the canonical recipe-form ID instead of the default runtime handle ID.",
							Type:        Boolean(false),
							Default:     Boolean(false),
							Internal:    true,
						},
					),
					DoNotCache: "ID fields are loaded from the receiver result.",
				},
				Func: func(ctx context.Context, self ObjectResult[T], args map[string]Input, _ call.View) (AnyResult, error) {
					recipe, _ := args["recipe"].(Boolean)
					var (
						selfID *call.ID
						err    error
					)
					if bool(recipe) {
						selfID, err = self.RecipeID(ctx)
					} else {
						selfID, err = self.ID()
					}
					if err != nil {
						return nil, err
					}
					return NewResultForCurrentCall(ctx, NewAnyID(selfID))
				},
			},
		)
		class.idable = true
	}
	return class
}

func (class Class[T]) ForkObjectType(srv *Server) (ObjectType, error) {
	class.fieldsL.RLock()
	defer class.fieldsL.RUnlock()

	forked := class
	forked.fields = make(map[string][]*Field[T], len(class.fields))
	for name, fields := range class.fields {
		forked.fields[name] = slices.Clone(fields)
	}
	forked.interfaces = make(map[string]*Interface, len(class.interfaces))
	for name, iface := range class.interfaces {
		forked.interfaces[name] = iface
	}
	forked.fieldsL = new(sync.RWMutex)
	forked.invalidateSchemaCache = srv.invalidateSchemaCache
	return forked, nil
}

func (class Class[T]) Typed() Typed {
	return class.inner
}

func (class Class[T]) IDType() (IDType, bool) {
	if class.idable {
		return ID[T]{inner: class.inner, sourceMap: class.sourceMap}, true
	} else {
		return nil, false
	}
}

func (class Class[T]) Field(name string, view call.View) (Field[T], bool) {
	class.fieldsL.RLock()
	defer class.fieldsL.RUnlock()
	return class.fieldLocked(name, view)
}

func (class Class[T]) FieldSpec(name string, view call.View) (FieldSpec, bool) {
	field, ok := class.Field(name, view)
	if !ok {
		return FieldSpec{}, false
	}
	return *field.Spec, true
}

func (class Class[T]) FieldSpecs(view call.View) []FieldSpec {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	var specs []FieldSpec
	for name := range class.fields {
		if field, ok := class.fieldLocked(name, view); ok {
			specs = append(specs, *field.Spec)
		}
	}
	return specs
}

func (class Class[T]) fieldLocked(name string, view call.View) (Field[T], bool) {
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

// Implements declares that this class implements the given interface.
//
// It verifies that the class structurally satisfies the interface (has all
// required fields with compatible types). If not, it panics — this is a
// programming error, like a bad field type.
//
// The check uses the empty view ("") which sees all global fields.
func (class Class[T]) Implements(iface *Interface) {
	if !iface.Satisfies(class, "") {
		panic(fmt.Sprintf("type %s does not satisfy interface %s", class.TypeName(), iface.TypeName()))
	}
	class.fieldsL.Lock()
	class.interfaces[iface.TypeName()] = iface
	class.fieldsL.Unlock()
	iface.addImplementor(class.TypeName())
	if class.invalidateSchemaCache != nil {
		class.invalidateSchemaCache()
	}
}

// Interfaces returns the interfaces this class implements.
func (class Class[T]) Interfaces() []*Interface {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	result := make([]*Interface, 0, len(class.interfaces))
	for _, iface := range class.interfaces {
		result = append(result, iface)
	}
	return result
}

func (class Class[T]) Extend(spec FieldSpec, fun FieldFunc) {
	class.fieldsL.Lock()
	f := &Field[T]{
		Spec: &spec,
		Func: func(ctx context.Context, self ObjectResult[T], args map[string]Input, view call.View) (AnyResult, error) {
			return fun(ctx, self, args)
		},
	}
	class.fields[spec.Name] = append(class.fields[spec.Name], f)
	class.fieldsL.Unlock()

	// Invalidate cache after releasing the lock to avoid Class[...].fieldsL and
	// *Server.schemaLock deadlock if the schema is concurrently introspected and
	// updated (via Extend)
	if class.invalidateSchemaCache != nil {
		class.invalidateSchemaCache()
	}
}

func (class Class[T]) ExtendLoadByID(spec FieldSpec, fun LoadByIDFunc) {
	class.fieldsL.Lock()
	spec.BuiltinLoadByIDFunc = fun
	f := &Field[T]{
		Spec: &spec,
	}
	class.fields[spec.Name] = append(class.fields[spec.Name], f)
	class.fieldsL.Unlock()

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
func (class Class[T]) TypeDefinition(view call.View) *ast.Definition {
	class.fieldsL.RLock()
	defer class.fieldsL.RUnlock()
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
	// Populate interface names on the definition.
	for name := range class.interfaces {
		def.Interfaces = append(def.Interfaces, name)
	}
	sort.Strings(def.Interfaces)
	return def
}

// ParseField parses a field selection into a Selector and return type.
func (class Class[T]) ParseField(ctx context.Context, view call.View, astField *ast.Field, vars map[string]any) (Selector, *ast.Type, error) {
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
func (class Class[T]) New(val AnyResult) (AnyObjectResult, error) {
	if objResult, ok := val.(ObjectResult[T]); ok {
		return objResult, nil
	}
	if inst, ok := val.(Result[T]); ok {
		return ObjectResult[T]{
			Result: inst,
			class:  class,
		}, nil
	}
	if inst, ok := val.(Result[Typed]); ok {
		if _, ok := UnwrapAs[T](inst.Self()); !ok {
			if derefCapable, ok := any(inst).(interface{ withDerefViewAny() AnyResult }); ok {
				if shared := inst.cacheSharedResult(); shared != nil && shared.self != nil {
					if inner, valid := derefTyped(shared.self); valid {
						if _, ok := UnwrapAs[T](inner); ok {
							derefVal := derefCapable.withDerefViewAny()
							if derefInst, ok := derefVal.(Result[Typed]); ok {
								return ObjectResult[T]{
									Result: Result[T](derefInst),
									class:  class,
								}, nil
							}
						}
					}
				}
			}
			return nil, fmt.Errorf("cannot instantiate %T with %T", class, val)
		}
		return ObjectResult[T]{
			Result: Result[T](inst),
			class:  class,
		}, nil
	}

	if _, ok := UnwrapAs[T](val); !ok {
		return nil, fmt.Errorf("cannot instantiate %T with %T", class, val)
	}
	shared := val.cacheSharedResult()
	if shared == nil {
		return nil, fmt.Errorf("cannot instantiate %T with %T: missing shared result", class, val)
	}

	return ObjectResult[T]{
		Result: Result[T]{
			shared:   shared,
			hitCache: val.HitCache(),
		},
		class: class,
	}, nil
}

func NoopDone(res AnyResult, cached bool, rerr *error) {}

// Select calls the field on the instance specified by the selector
func (r ObjectResult[T]) Select(ctx context.Context, s *Server, sel Selector) (AnyResult, error) {
	r, preselectResult, err := r.preselect(ctx, sel)
	if err != nil {
		return nil, err
	}
	return r.call(ctx, s, preselectResult.request, preselectResult.inputArgs)
}

type preselectResult struct {
	inputArgs map[string]Input
	request   *CallRequest
}

// sortCallArgsToSchema sorts the arguments to match the schema definition order.
func (r ObjectResult[T]) sortCallArgsToSchema(fieldSpec *FieldSpec, view call.View, args []*ResultCallArg) {
	inputs := fieldSpec.Args.Inputs(view)
	sort.Slice(args, func(i, j int) bool {
		iIdx := slices.IndexFunc(inputs, func(input InputSpec) bool {
			return input.Name == args[i].Name
		})
		jIdx := slices.IndexFunc(inputs, func(input InputSpec) bool {
			return input.Name == args[j].Name
		})
		return iIdx < jIdx
	})
}

func (r ObjectResult[T]) preselect(ctx context.Context, sel Selector) (ObjectResult[T], *preselectResult, error) {
	view := sel.View
	field, ok := r.class.Field(sel.Field, view)
	if !ok {
		return r, nil, fmt.Errorf("Select: %s has no such field: %q", r.class.TypeName(), sel.Field)
	}
	if field.Spec.ViewFilter == nil {
		// fields in the global view shouldn't attach the current view to the
		// selector (since they're global from all perspectives)
		view = ""
	}
	inputArgs := make(map[string]Input, len(sel.Args))
	frameArgs := make([]*ResultCallArg, 0, len(sel.Args))
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
			inputArgs[argSpec.Name] = namedInput.Value
			frameArg, err := resultCallArgFromInput(ctx, argSpec.Name, namedInput.Value, argSpec.Sensitive)
			if err != nil {
				return r, nil, err
			}
			frameArgs = append(frameArgs, frameArg)

		case argSpec.Default != nil:
			inputArgs[argSpec.Name] = argSpec.Default

		case argSpec.Type.Type().NonNull:
			// error out if the arg is missing but required
			return r, nil, fmt.Errorf("missing required argument: %q", argSpec.Name)
		}
	}

	astType := field.Spec.Type.Type()
	if sel.Nth != 0 {
		astType = astType.Elem
	}
	r.sortCallArgsToSchema(field.Spec, view, frameArgs)

	implicitInputs, err := field.Spec.resolveImplicitInputCallArgs(ctx, inputArgs)
	if err != nil {
		typ := r.Type()
		if typ == nil {
			return r, nil, fmt.Errorf("failed to resolve identity inputs for <nil>.%s: %w", sel.Field, err)
		}
		return r, nil, fmt.Errorf("failed to resolve identity inputs for %s.%s: %w", typ.Name(), sel.Field, err)
	}

	receiverRef, err := resultCallRefFromResult(ctx, r)
	if err != nil {
		typ := r.Type()
		if typ == nil {
			return r, nil, fmt.Errorf("failed to resolve receiver for <nil>.%s: %w", sel.Field, err)
		}
		return r, nil, fmt.Errorf("failed to resolve receiver for %s.%s: %w", typ.Name(), sel.Field, err)
	}
	req := &CallRequest{
		ResultCall: &ResultCall{
			Kind:           ResultCallKindField,
			Type:           NewResultCallType(astType),
			Field:          sel.Field,
			View:           view,
			Nth:            int64(sel.Nth),
			Receiver:       receiverRef,
			Module:         field.Spec.Module.clone(),
			Args:           frameArgs,
			ImplicitInputs: implicitInputs,
		},
		TTL:           field.Spec.TTL,
		DoNotCache:    field.Spec.DoNotCache != "",
		IsPersistable: field.Spec.IsPersistable,
	}
	if clientMD, err := engine.ClientMetadataFromContext(ctx); err != nil {
		slog.Warn("failed to get client metadata from context for call", "err", err)
	} else {
		req.ConcurrencyKey = clientMD.ClientID
	}
	if field.Spec.GetDynamicInput != nil {
		if err := field.Spec.GetDynamicInput(ctx, r, inputArgs, view, req); err != nil {
			typ := r.Type()
			if typ == nil {
				return r, nil, fmt.Errorf("failed to compute cache key for <nil>.%s: %w", sel.Field, err)
			}
			return r, nil, fmt.Errorf("failed to compute cache key for %s.%s: %w", typ.Name(), sel.Field, err)
		}
		r.sortCallArgsToSchema(field.Spec, view, req.Args)
		inputArgs, err = field.Spec.Args.InputsFromResultCallArgs(ctx, req.Args, view)
		if err != nil {
			return r, nil, err
		}
		implicitInputs, err = field.Spec.resolveImplicitInputCallArgs(ctx, inputArgs)
		if err != nil {
			typ := r.Type()
			if typ == nil {
				return r, nil, fmt.Errorf("failed to resolve identity inputs for <nil>.%s: %w", sel.Field, err)
			}
			return r, nil, fmt.Errorf("failed to resolve identity inputs for %s.%s: %w", typ.Name(), sel.Field, err)
		}
		req.ImplicitInputs = implicitInputs
	}

	return r, &preselectResult{
		inputArgs: inputArgs,
		request:   req,
	}, nil
}

func (r ObjectResult[T]) call(
	ctx context.Context,
	s *Server,
	req *CallRequest,
	inputArgs map[string]Input,
) (AnyResult, error) {
	ctx = ContextWithCall(ctx, req.ResultCall)
	fieldName := req.Field
	view := req.View
	field, ok := r.class.Field(fieldName, view)
	if !ok {
		return nil, fmt.Errorf("call: %s has no such field: %q", r.class.inner.Type().Name(), fieldName)
	}
	if field.Spec.BuiltinLoadByIDFunc != nil {
		return field.Spec.BuiltinLoadByIDFunc(ctx, r, inputArgs)
	}
	var (
		res AnyResult
		err error
	)
	if s.telemetry != nil && !field.Spec.NoTelemetry {
		telemetryCtx, done := s.telemetry(ctx, req)
		defer func() {
			var cached bool
			if res != nil {
				cached = res.HitCache()
			}
			done(res, cached, &err)
		}()
		ctx = telemetryCtx
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("call %s.%s: current client metadata: %w", r.class.inner.Type().Name(), fieldName, err)
	}
	if clientMetadata.SessionID == "" {
		return nil, fmt.Errorf("call %s.%s: empty session ID", r.class.inner.Type().Name(), fieldName)
	}
	cache, err := EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("call %s.%s: current dagql cache: %w", r.class.inner.Type().Name(), fieldName, err)
	}

	res, err = cache.GetOrInitCall(ctx, clientMetadata.SessionID, s, req, func(ctx context.Context) (AnyResult, error) {
		val, err := field.Func(ctx, r, inputArgs, view)
		if err != nil {
			return nil, err
		}
		if val == nil {
			return nil, nil
		}

		val, ok = val.DerefValue()
		if !ok {
			return nil, nil
		}
		nth := int(req.Nth)
		if nth != 0 {
			val, err = val.NthValue(ctx, nth)
			if err != nil {
				return nil, fmt.Errorf("cannot get %dth value from %T: %w", nth, val, err)
			}
			val, ok = val.DerefValue()
			if !ok {
				return nil, nil
			}
		}

		return val, nil
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}

	return res, nil
}

type ViewFilter interface {
	Contains(call.View) bool
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

func (AllView) Contains(view call.View) bool {
	return true
}

// ExactView contains exactly one view.
type ExactView string

func (exact ExactView) Contains(view call.View) bool {
	return string(exact) == string(view)
}

type (
	FuncHandler[T Typed, A any, R any]     func(ctx context.Context, self T, args A) (R, error)
	NodeFuncHandler[T Typed, A any, R any] func(ctx context.Context, self ObjectResult[T], args A) (R, error)
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
	return FuncWithDynamicInputs(name, fn, nil)
}

// FuncWithDynamicInputs is like Func but lets a resolver customize request/cache
// behavior for each call (for example argument rewrites, TTL, do-not-cache, or
// concurrency key).
func FuncWithDynamicInputs[T Typed, A any, R any](
	name string,
	fn FuncHandler[T, A, R],
	cacheFn DynamicInputFunc[T, A],
) Field[T] {
	return NodeFuncWithDynamicInputs(name, func(ctx context.Context, self ObjectResult[T], args A) (R, error) {
		return fn(ctx, self.Self(), args)
	}, cacheFn)
}

// NodeFunc is the same as Func, except it passes the ObjectResult instead of the
// receiver so that you can access its ID.
func NodeFunc[T Typed, A any, R any](name string, fn NodeFuncHandler[T, A, R]) Field[T] {
	return NodeFuncWithDynamicInputs(name, fn, nil)
}

// NodeFuncWithDynamicInputs is like NodeFunc but lets a resolver customize
// request/cache behavior for each call (for example argument rewrites, TTL,
// do-not-cache, or concurrency key).
func NodeFuncWithDynamicInputs[T Typed, A any, R any](
	name string,
	fn NodeFuncHandler[T, A, R],
	cacheFn DynamicInputFunc[T, A],
) Field[T] {
	var zeroArgs A
	inputs, argsErr := InputSpecsForType(zeroArgs, true)
	if argsErr != nil {
		var zeroSelf T
		slog.Error("failed to parse args", "type", zeroSelf.Type(), "field", name, "error", argsErr)
	}

	spec := &FieldSpec{
		Name: name,
		Args: inputs,
	}

	var zeroRet R
	var returnTypeError error
	if res, ok := any(zeroRet).(AnyResult); ok {
		spec.Type = res.Unwrap()
	} else {
		spec.Type, returnTypeError = builtinOrTyped(zeroRet)
	}

	field := Field[T]{
		Spec: spec,
		Func: func(ctx context.Context, self ObjectResult[T], argVals map[string]Input, view call.View) (AnyResult, error) {
			// these errors are deferred until runtime, since it's better (at least
			// more testable) than panicking
			if argsErr != nil {
				return nil, argsErr
			}
			if returnTypeError != nil {
				return nil, returnTypeError
			}

			var args A
			if err := spec.Args.Decode(argVals, &args, view); err != nil {
				return nil, err
			}
			ret, err := fn(ctx, self, args)
			if err != nil {
				return nil, err
			}

			if res, ok := any(ret).(AnyResult); ok {
				return res, nil
			}
			res, err := builtinOrTyped(ret)
			if err != nil {
				return nil, fmt.Errorf("expected %T to be a Typed value, got %T: %w", ret, ret, err)
			}

			return NewResultForCurrentCall(ctx, res)
		},
	}

	if cacheFn != nil {
		field.Spec.GetDynamicInput = func(ctx context.Context, self AnyResult, argVals map[string]Input, view call.View, req *CallRequest) error {
			if argsErr != nil {
				// this error is deferred until runtime, since it's better (at least
				// more testable) than panicking
				return argsErr
			}
			var args A
			if err := spec.Args.Decode(argVals, &args, view); err != nil {
				return err
			}
			inst, ok := self.(ObjectResult[T])
			if !ok {
				return fmt.Errorf("expected instance of %T, got %T", field, self)
			}
			return cacheFn(ctx, inst, args, req)
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
	DeprecatedReason *string
	// ExperimentalReason marks the field as experimental and provides a reason.
	ExperimentalReason string
	// Module is frame-native provenance for the module that provides the field's
	// implementation.
	Module *ResultCallModule
	// Directives is the list of GraphQL directives attached to this field.
	Directives []*ast.Directive
	// BuiltinLoadByIDFunc is the execution path for schema-generated
	// load<Type>FromID fields, which re-enter the graph from an object ID
	// rather than behaving like normal field resolvers.
	BuiltinLoadByIDFunc LoadByIDFunc

	// ViewFilter is filter that specifies under which views this field is
	// accessible. If not view is present, the default is the "global" view.
	ViewFilter ViewFilter

	// If set, the result of this field will never be cached and not have concurrent equal
	// calls deduped. The string value is a reason why the field should not be cached.
	DoNotCache string

	// If set, the result of this field will be cached for the given TTL (in seconds).
	TTL int64

	// If set, the result of this field is eligible for persistent cache storage.
	IsPersistable bool

	// If set, this GetDynamicInput will be called before cache evaluation to
	// make any dynamic adjustments to the call request or its policy.
	GetDynamicInput GenericDynamicInputFunc

	// ImplicitInputs are engine-computed inputs that are attached to the call
	// identity but are not explicit GraphQL field args.
	ImplicitInputs []ImplicitInput

	// NoTelemetry suppresses telemetry (AroundFunc) for this field.
	// Used for entrypoint proxies that delegate to real fields which
	// emit their own telemetry.
	NoTelemetry bool

	// extend is used during installation to copy the spec of a previous field
	// with the same name
	extend bool
}

func (spec FieldSpec) FieldDefinition(view call.View) *ast.FieldDefinition {
	def := &ast.FieldDefinition{
		Name:        spec.Name,
		Description: spec.Description,
		Arguments:   spec.Args.ArgumentDefinitions(view),
		Type:        spec.Type.Type(),
	}
	if len(spec.Directives) > 0 {
		def.Directives = slices.Clone(spec.Directives)
	}
	// When the return type is ID, add @expectedType to convey type info.
	if spec.Type.Type().Name() == "ID" {
		if idTyped, ok := spec.Type.(interface{ ExpectedTypeName() string }); ok {
			if expectedName := idTyped.ExpectedTypeName(); expectedName != "" {
				def.Directives = append(def.Directives, ExpectedTypeDirective(expectedName))
			}
		}
	}
	if spec.DeprecatedReason != nil {
		def.Directives = append(def.Directives, deprecated(spec.DeprecatedReason))
	}
	if spec.ExperimentalReason != "" {
		def.Directives = append(def.Directives, experimental(spec.ExperimentalReason))
	}
	return def
}

func (spec *FieldSpec) resolveImplicitInputCallArgs(ctx context.Context, inputArgs map[string]Input) ([]*ResultCallArg, error) {
	if spec == nil || len(spec.ImplicitInputs) == 0 {
		return nil, nil
	}

	inputIdxByName := make(map[string]int, len(spec.ImplicitInputs))
	implicitArgs := make([]*ResultCallArg, 0, len(spec.ImplicitInputs))
	for _, implicitInput := range spec.ImplicitInputs {
		inputVal, err := implicitInput.Resolver(ctx, inputArgs)
		if err != nil {
			return nil, fmt.Errorf("resolve implicit input %q: %w", implicitInput.Name, err)
		}
		if inputVal == nil {
			return nil, fmt.Errorf("implicit input %q resolved to nil", implicitInput.Name)
		}

		newInput, err := resultCallArgFromInput(ctx, implicitInput.Name, inputVal, false)
		if err != nil {
			return nil, fmt.Errorf("resolve implicit input %q: %w", implicitInput.Name, err)
		}
		if idx, ok := inputIdxByName[implicitInput.Name]; ok {
			implicitArgs[idx] = newInput
			continue
		}
		inputIdxByName[implicitInput.Name] = len(implicitArgs)
		implicitArgs = append(implicitArgs, newInput)
	}
	sort.Slice(implicitArgs, func(i, j int) bool {
		return implicitArgs[i].Name < implicitArgs[j].Name
	})
	return implicitArgs, nil
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
	DeprecatedReason *string
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
	if other.DeprecatedReason != nil {
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
		reason := arg.Spec.Description
		arg.Spec.DeprecatedReason = &reason
		arg.Spec.Description = deprecationDescription(arg.Spec.Description)
		return arg
	}
	reason := FormatDescription(paras...)
	arg.Spec.DeprecatedReason = &reason
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

func (specs InputSpecs) HasRequired(view call.View) bool {
	for _, input := range specs.Inputs(view) {
		if input.Default == nil && input.Type.Type().NonNull {
			return true
		}
	}
	return false
}

func (specs InputSpecs) Input(name string, view call.View) (InputSpec, bool) {
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

func (specs InputSpecs) Inputs(view call.View) (args []InputSpec) {
	// This function is currently in the hot path, so we optimize duplicate checks by only using
	// a map when the number of args is above a certain threshold, using slice iteration otherwise.
	// The previous implementation that only used a map was a genuine bottleneck since most of the
	// time the number of args is small.

	const useMapThreshold = 15 // based on some benchmarks on an m4 laptop, fairly approximate though

	var seen map[string]struct{}
	if len(specs.raw) > useMapThreshold {
		seen = make(map[string]struct{}, len(specs.raw))
	}

	args = make([]InputSpec, 0, len(specs.raw))
	for i := len(specs.raw) - 1; i >= 0; i-- {
		// iterate backwards to allow last-defined spec to have precedence
		spec := specs.raw[i]

		var alreadySeen bool
		if seen != nil {
			_, alreadySeen = seen[spec.Name]
		} else {
			// check for duplicates w/ O(n^2) iteration since n is small
			for _, a := range args {
				if a.Name == spec.Name {
					alreadySeen = true
					break
				}
			}
		}
		if alreadySeen {
			continue
		}

		if spec.ViewFilter != nil && !spec.ViewFilter.Contains(view) {
			continue
		}

		args = append(args, spec)
		if seen != nil {
			seen[spec.Name] = struct{}{}
		}
	}
	slices.Reverse(args)
	return args
}

func (specs InputSpecs) InputsFromResultCallArgs(ctx context.Context, args []*ResultCallArg, view call.View) (map[string]Input, error) {
	inputs := make(map[string]Input, len(args))
	for _, argSpec := range specs.Inputs(view) {
		var requestArg *ResultCallArg
		for _, arg := range args {
			if arg != nil && arg.Name == argSpec.Name {
				requestArg = arg
				break
			}
		}
		switch {
		case requestArg != nil:
			inputVal, err := inputValueFromResultCallLiteral(ctx, requestArg.Value)
			if err != nil {
				return nil, fmt.Errorf("request arg %q: %w", argSpec.Name, err)
			}
			input, err := argSpec.Type.Decoder().DecodeInput(inputVal)
			if err != nil {
				return nil, fmt.Errorf("request arg %q value as %T (%s) using %T: %w", argSpec.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
			}
			inputs[argSpec.Name] = input
		case argSpec.Default != nil:
			inputs[argSpec.Name] = argSpec.Default
		case argSpec.Type.Type().NonNull:
			return nil, fmt.Errorf("missing required argument: %q", argSpec.Name)
		}
	}
	return inputs, nil
}

func (specs InputSpecs) ArgumentDefinitions(view call.View) []*ast.ArgumentDefinition {
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
		// Add @expectedType for ID-typed arguments that don't already
		// have one. The reflection-based InputSpecsForType path adds
		// this to spec.Directives, but directly-constructed InputSpecs
		// (e.g. from ExtendEnvType) may not.
		if arg.Type.Type().Name() == "ID" {
			hasExpectedType := slices.ContainsFunc(schemaArg.Directives,
				func(d *ast.Directive) bool { return d.Name == "expectedType" })
			if !hasExpectedType {
				if name := findExpectedTypeName(arg.Type); name != "" {
					schemaArg.Directives = append(schemaArg.Directives, ExpectedTypeDirective(name))
				}
			}
		}
		if arg.DeprecatedReason != nil {
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

func (specs InputSpecs) FieldDefinitions(view call.View) (defs []*ast.FieldDefinition) {
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
	TypeDefinition(view call.View) *ast.Definition
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
		spec := &FieldSpec{
			Name:               name,
			Type:               field.Value,
			Description:        field.Field.Tag.Get("doc"),
			ExperimentalReason: field.Field.Tag.Get("experimental"),
			DoNotCache:         field.Field.Tag.Get("doNotCache"),
		}
		if dep, ok := field.Field.Tag.Lookup("deprecated"); ok {
			reason := dep // keep "" if that’s what the module author wrote: @deprecated("") != @deprecated()
			spec.DeprecatedReason = &reason
		}

		fields = append(fields, Field[T]{
			Spec: spec,
			Func: func(ctx context.Context, self ObjectResult[T], args map[string]Input, view call.View) (AnyResult, error) {
				t, found, err := getField(ctx, self.Self(), false, name)
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

	// Re-check auto-interfaces now that all fields (including sync, etc.)
	// have been installed. InstallObject only sees the id field.
	server.AutoImplementInterfaces(class)
}

type GenericDynamicInputFunc func(
	context.Context,
	AnyResult,
	map[string]Input,
	call.View,
	*CallRequest,
) error

type DynamicInputFunc[T Typed, A any] func(
	context.Context,
	ObjectResult[T],
	A,
	*CallRequest,
) error

type ImplicitInputResolver func(context.Context, map[string]Input) (Input, error)

type ImplicitInput struct {
	Name     string
	Resolver ImplicitInputResolver
}

// Field defines a field of an Object type.
type Field[T Typed] struct {
	Spec *FieldSpec
	Func func(context.Context, ObjectResult[T], map[string]Input, call.View) (AnyResult, error)
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
	field.Spec.DoNotCache = FormatDescription(append([]string{reason}, paras...)...)
	return field
}

func (field Field[T]) IsPersistable() Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	field.Spec.IsPersistable = true
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

func (field Field[T]) WithInput(inputs ...ImplicitInput) Field[T] {
	if field.Spec.extend {
		panic("cannot call on extended field")
	}
	for _, input := range inputs {
		if input.Name == "" {
			panic("implicit input name cannot be empty")
		}
		if input.Resolver == nil {
			panic(fmt.Sprintf("implicit input %q resolver cannot be nil", input.Name))
		}
		field.Spec.ImplicitInputs = append(field.Spec.ImplicitInputs, input)
	}
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
	reason := FormatDescription(paras...)
	field.Spec.DeprecatedReason = &reason
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
func (field Field[T]) FieldDefinition(view call.View) *ast.FieldDefinition {
	if field.Spec.Type == nil {
		panic(fmt.Errorf("field %q has no type", field.Spec.Name))
	}
	return field.Spec.FieldDefinition(view)
}

func definition(kind ast.DefinitionKind, val Type, view call.View) *ast.Definition {
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

// findExpectedTypeName walks through Input wrappers to find the expected type
// name for ID-typed arguments. It handles Optional, DynamicOptional, and
// ArrayInput wrappers.
func findExpectedTypeName(input Input) string {
	// Direct check.
	if idTyped, ok := input.(interface{ ExpectedTypeName() string }); ok {
		if name := idTyped.ExpectedTypeName(); name != "" {
			return name
		}
	}
	// Unwrap via reflection for generic wrappers like Optional[ID[T]]
	// or ArrayInput[ID[T]] that we can't type-assert directly.
	v := reflect.ValueOf(input)
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanInterface() {
				continue
			}
			if inner, ok := f.Interface().(Input); ok {
				if name := findExpectedTypeName(inner); name != "" {
					return name
				}
			}
		}
	case reflect.Slice:
		// For ArrayInput[ID[T]], check the element type.
		elemType := v.Type().Elem()
		if elemType.Kind() == reflect.Struct || elemType.Kind() == reflect.Interface {
			zero := reflect.New(elemType).Elem()
			if zero.CanInterface() {
				if inner, ok := zero.Interface().(Input); ok {
					if name := findExpectedTypeName(inner); name != "" {
						return name
					}
				}
			}
		}
	}
	return ""
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
			ExperimentalReason: field.Field.Tag.Get("experimental"),
			Sensitive:          field.Field.Tag.Get("sensitive") == "true",
			Internal:           field.Field.Tag.Get("internal") == "true",
		}
		// Add @expectedType directive for ID-typed arguments.
		// Walk through wrapper types (Optional, DynamicOptional, ArrayInput)
		// to find the underlying type and check for ExpectedTypeName.
		expectedTypeName := findExpectedTypeName(input)
		if expectedTypeName != "" {
			spec.Directives = append(spec.Directives, ExpectedTypeDirective(expectedTypeName))
		}
		if dep, ok := field.Field.Tag.Lookup("deprecated"); ok {
			reason := dep
			spec.DeprecatedReason = &reason
		}

		if spec.Description == "" && spec.DeprecatedReason != nil {
			spec.Description = deprecationDescription(*spec.DeprecatedReason)
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
		if res, ok := fieldI.(AnyResult); ok {
			fieldI = res.Unwrap()
		}

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
	obj any,
	optIn bool,
	fieldName string,
) (res AnyResult, found bool, rerr error) {
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
			t, found, err := getField(ctx, fieldI, optIn, fieldName)
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

			if val, ok := val.(AnyResult); ok {
				return val, true, nil
			}

			t, err := builtinOrTyped(val)
			if err != nil {
				return nil, false, fmt.Errorf("get field %q: %w", name, err)
			}
			if !t.Type().NonNull && objV.Field(i).IsZero() {
				return nil, true, nil
			}

			retVal, err := NewResultForCurrentCall(ctx, t)
			if err != nil {
				return nil, false, fmt.Errorf("get field %q: %w", name, err)
			}

			return retVal, true, nil
		}
	}
	return nil, false, nil
}

func (specs InputSpecs) Decode(inputs map[string]Input, dest any, view call.View) error {
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
		err := setter.SetField(field)
		if err != nil {
			return fmt.Errorf("assign: Setter.SetField %T to %s: %w", val, field.Type(), err)
		}
		return nil
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
