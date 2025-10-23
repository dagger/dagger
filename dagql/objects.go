package dagql

import (
	"context"
	"encoding/json"
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
	"github.com/dagger/dagger/engine"
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
				Func: func(ctx context.Context, self ObjectResult[T], args map[string]Input, view call.View) (AnyResult, error) {
					id := NewDynamicID[T](self.ID(), opts.Typed)
					return NewResultForCurrentID(ctx, id)
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

func (class Class[T]) Field(name string, view call.View) (Field[T], bool) {
	class.fieldsL.Lock()
	defer class.fieldsL.Unlock()
	return class.fieldLocked(name, view)
}

func (class Class[T]) FieldSpec(name string, view call.View) (FieldSpec, bool) {
	field, ok := class.Field(name, view)
	if !ok {
		return FieldSpec{}, false
	}
	return *field.Spec, true
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

// TypeDefinition returns the schema definition of the class.
//
// The definition is derived from the type name, description, and fields. The
// type may implement Definitive or Descriptive to provide more information.
//
// Each currently defined field is installed on the returned definition.
func (class Class[T]) TypeDefinition(view call.View) *ast.Definition {
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

	self, ok := UnwrapAs[T](val)
	if !ok {
		return nil, fmt.Errorf("cannot instantiate %T with %T", class, val)
	}

	return ObjectResult[T]{
		Result: Result[T]{
			constructor: val.ID(),
			self:        self,
		},
		class: class,
	}, nil
}

// Call calls a field on the class against an instance.
func (class Class[T]) Call(
	ctx context.Context,
	srv *Server,
	node ObjectResult[T],
	fieldName string,
	view call.View,
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
	// a callback that should always run after the field is called and additional
	// caching metadata
	var postCall cache.PostCallFunc
	var safeToPersistCache bool
	if val != nil {
		postCall = val.GetPostCall()
		safeToPersistCache = val.IsSafeToPersistCache()
	}

	// they can also return types that need to run a callback when they are
	// removed from the cache (to clean up or release any state)
	var onRelease cache.OnReleaseFunc
	if onReleaser, ok := UnwrapAs[OnReleaser](val); ok {
		onRelease = onReleaser.OnRelease
	}

	return &CacheValWithCallbacks{
		Value:              val,
		PostCall:           postCall,
		OnRelease:          onRelease,
		SafeToPersistCache: safeToPersistCache,
	}, nil
}

type Result[T Typed] struct {
	constructor        *call.ID
	self               T
	postCall           cache.PostCallFunc
	safeToPersistCache bool
}

var _ AnyResult = Result[Typed]{}

func (o Result[T]) Type() *ast.Type {
	return o.self.Type()
}

// ID returns the ID of the instance.
func (r Result[T]) ID() *call.ID {
	return r.constructor
}

func (r Result[T]) Self() T {
	return r.self
}

func (r Result[T]) SetField(field reflect.Value) error {
	return assign(field, r.self)
}

// Unwrap returns the inner value of the instance.
func (r Result[T]) Unwrap() Typed {
	return r.self
}

func (r Result[T]) DerefValue() (AnyResult, bool) {
	derefableSelf, ok := any(r.self).(DerefableResult)
	if !ok {
		return r, true
	}
	return derefableSelf.DerefToResult(r.constructor, r.postCall)
}

func (r Result[T]) NthValue(nth int) (AnyResult, error) {
	enumerableSelf, ok := any(r.self).(Enumerable)
	if !ok {
		return nil, fmt.Errorf("cannot get %dth value from %T", nth, r.self)
	}
	return enumerableSelf.NthValue(nth, r.constructor)
}

func (r Result[T]) WithPostCall(fn cache.PostCallFunc) AnyResult {
	r.postCall = fn
	return r
}

func (r Result[T]) ResultWithPostCall(fn cache.PostCallFunc) Result[T] {
	r.postCall = fn
	return r
}

func (r Result[T]) WithSafeToPersistCache(safe bool) AnyResult {
	r.safeToPersistCache = safe
	return r
}

func (r Result[T]) IsSafeToPersistCache() bool {
	return r.safeToPersistCache
}

// WithDigest returns an updated instance with the given metadata set.
// customDigest overrides the default digest of the instance to the provided value.
// NOTE: customDigest must be used with care as any instances with the same digest
// will be considered equivalent and can thus replace each other in the cache.
// Generally, customDigest should be used when there's a content-based digest available
// that won't be caputured by the default, call-chain derived digest.
func (r Result[T]) WithDigest(customDigest digest.Digest) Result[T] {
	return Result[T]{
		constructor: r.constructor.WithDigest(customDigest),
		self:        r.self,
	}
}

// String returns the instance in Class@sha256:... format.
func (r Result[T]) String() string {
	return fmt.Sprintf("%s@%s", r.self.Type().Name(), r.constructor.Digest())
}

func (r Result[T]) GetPostCall() cache.PostCallFunc {
	return r.postCall
}

func (r Result[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.ID())
}

type ObjectResult[T Typed] struct {
	Result[T]
	class Class[T]
}

var _ AnyObjectResult = ObjectResult[Typed]{}

func (r ObjectResult[T]) MarshalJSON() ([]byte, error) {
	return r.Result.MarshalJSON()
}

func (r ObjectResult[T]) DerefValue() (AnyResult, bool) {
	derefableSelf, ok := any(r.self).(DerefableResult)
	if !ok {
		return r, true
	}
	return derefableSelf.DerefToResult(r.constructor, r.postCall)
}

func (r ObjectResult[T]) SetField(field reflect.Value) error {
	return assign(field, r.Result)
}

// ObjectType returns the ObjectType of the instance.
func (r ObjectResult[T]) ObjectType() ObjectType {
	return r.class
}

func (r ObjectResult[T]) WithObjectDigest(customDigest digest.Digest) ObjectResult[T] {
	return ObjectResult[T]{
		Result: Result[T]{
			constructor: r.constructor.WithDigest(customDigest),
			self:        r.self,
		},
		class: r.class,
	}
}

func NoopDone(res AnyResult, cached bool, rerr error) {}

// Select calls the field on the instance specified by the selector
func (r ObjectResult[T]) Select(ctx context.Context, s *Server, sel Selector) (AnyResult, error) {
	preselectResult, err := r.preselect(ctx, s, sel)
	if err != nil {
		return nil, err
	}
	return r.call(ctx, s,
		preselectResult.newID,
		preselectResult.inputArgs,
		preselectResult.cacheKey,
	)
}

type preselectResult struct {
	inputArgs map[string]Input
	newID     *call.ID
	cacheKey  CacheKey
}

// sortArgsToSchema sorts the arguments to match the schema definition order.
func (r ObjectResult[T]) sortArgsToSchema(fieldSpec *FieldSpec, view call.View, idArgs []*call.Argument) {
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

func (r ObjectResult[T]) preselect(ctx context.Context, s *Server, sel Selector) (*preselectResult, error) {
	view := sel.View
	field, ok := r.class.Field(sel.Field, view)
	if !ok {
		return nil, fmt.Errorf("Select: %s has no such field: %q", r.class.TypeName(), sel.Field)
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

	newID := r.constructor.Append(
		astType,
		sel.Field,
		call.WithView(view),
		call.WithModule(field.Spec.Module),
		call.WithNth(sel.Nth),
		call.WithArgs(idArgs...),
	)

	cacheKey := newCacheKey(ctx, newID, field.Spec)
	if field.Spec.GetCacheConfig != nil {
		cacheCfgCtx := idToContext(ctx, newID)
		cacheCfgCtx = srvToContext(cacheCfgCtx, s)
		cacheCfgResp, err := field.Spec.GetCacheConfig(cacheCfgCtx, r, inputArgs, view, GetCacheConfigRequest{
			CacheKey: cacheKey,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to compute cache key for %s.%s: %w", r.self.Type().Name(), sel.Field, err)
		}

		if len(cacheCfgResp.UpdatedArgs) > 0 {
			maps.Copy(inputArgs, cacheCfgResp.UpdatedArgs)
			for argName, argInput := range cacheCfgResp.UpdatedArgs {
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
			newID = r.constructor.Append(
				astType,
				sel.Field,
				call.WithView(view),
				call.WithModule(field.Spec.Module),
				call.WithNth(sel.Nth),
				call.WithArgs(idArgs...),
			)
		}

		if cacheCfgResp.CacheKey.CallKey != cacheKey.CallKey {
			cacheKey.CallKey = cacheCfgResp.CacheKey.CallKey
			newID = newID.WithDigest(digest.Digest(cacheKey.CallKey))
		}

		cacheKey.TTL = cacheCfgResp.CacheKey.TTL
		cacheKey.DoNotCache = cacheCfgResp.CacheKey.DoNotCache
	}

	return &preselectResult{
		inputArgs: inputArgs,
		newID:     newID,
		cacheKey:  cacheKey,
	}, nil
}

func newCacheKey(ctx context.Context, id *call.ID, fieldSpec *FieldSpec) CacheKey {
	cacheKey := CacheKey{
		CallKey:    string(id.Digest()),
		TTL:        fieldSpec.TTL,
		DoNotCache: fieldSpec.DoNotCache != "",
	}

	// dedupe concurrent calls only if the ID digest is the same and if the two calls are from the same client
	// we don't want to dedupe across clients since:
	// 1. it creates problems when one clients closes and others were waiting on the result
	// 2. it makes it easy to accidentally leak clients specific information that isn't yet precisely scoped in the ID
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		// not expected to happen, fallback behavior is just that there's no deduping of concurrent calls
		slog.Warn("failed to get client metadata from context for call", "err", err)
	} else {
		cacheKey.ConcurrencyKey = clientMD.ClientID
	}

	return cacheKey
}

// Call calls the field on the instance specified by the ID.
func (r ObjectResult[T]) Call(ctx context.Context, s *Server, newID *call.ID) (AnyResult, error) {
	fieldName := newID.Field()
	view := newID.View()
	field, ok := r.class.Field(fieldName, view)
	if !ok {
		return nil, fmt.Errorf("Call: %s has no such field: %q", r.class.TypeName(), fieldName)
	}

	inputArgs, err := ExtractIDArgs(field.Spec.Args, newID)
	if err != nil {
		return nil, err
	}

	cacheKey := newCacheKey(ctx, newID, field.Spec)
	return r.call(ctx, s, newID, inputArgs, cacheKey)
}

func ExtractIDArgs(specs InputSpecs, id *call.ID) (map[string]Input, error) {
	idArgs := id.Args()
	view := id.View()

	inputArgs := make(map[string]Input, len(idArgs))
	for _, argSpec := range specs.Inputs(view) {
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

	return inputArgs, nil
}

func (r ObjectResult[T]) call(
	ctx context.Context,
	s *Server,
	newID *call.ID,
	inputArgs map[string]Input,
	cacheKey CacheKey,
) (AnyResult, error) {
	ctx = idToContext(ctx, newID)
	ctx = srvToContext(ctx, s)
	var opts []CacheCallOpt
	if s.telemetry != nil {
		opts = append(opts, WithTelemetry(func(ctx context.Context) (context.Context, func(AnyResult, bool, error)) {
			return s.telemetry(ctx, r, newID)
		}))
	}

	res, err := s.Cache.GetOrInitializeWithCallbacks(ctx, cacheKey, func(ctx context.Context) (*CacheValWithCallbacks, error) {
		valWithCallbacks, err := r.class.Call(ctx, s, r, newID.Field(), newID.View(), inputArgs)
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
			Value:              val,
			PostCall:           valWithCallbacks.PostCall,
			OnRelease:          valWithCallbacks.OnRelease,
			SafeToPersistCache: valWithCallbacks.SafeToPersistCache,
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
	if idable, ok := val.(IDable); ok && idable != nil && !cacheKey.DoNotCache {
		valID := idable.ID()
		if valID == nil {
			return nil, fmt.Errorf("impossible: nil ID returned for value: %+v (%T)", val, val)
		}

		// only need to add a new cache key if the returned val has a different custom digest than the original
		digestChanged := valID.Digest() != newID.Digest()

		// Corner case: the `id` field on an object returns an IDable value (IDs are themselves both values and IDable).
		// However, if we cached `val` in this case, we would be caching <id digest> -> <id value>, which isn't what we
		// want. Instead, we only want to cache <id digest> -> <actual object value>.
		// To avoid this, we check that the returned IDable type is the actual object type.
		matchesType := valID.Type().ToAST().Name() == val.Type().Name()

		if digestChanged && matchesType {
			newID = valID
			_, err := s.Cache.GetOrInitializeValue(ctx, cache.CacheKey[CacheKeyType]{
				CallKey: string(valID.Digest()),
			}, val)
			if err != nil {
				return nil, err
			}
		}
	}

	return val, nil
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
	return FuncWithCacheKey(name, fn, nil)
}

// FuncWithCacheKey is like Func but allows specifying a custom digest that will be used to cache the operation in dagql.
func FuncWithCacheKey[T Typed, A any, R any](
	name string,
	fn FuncHandler[T, A, R],
	cacheFn GetCacheConfigFunc[T, A],
) Field[T] {
	return NodeFuncWithCacheKey(name, func(ctx context.Context, self ObjectResult[T], args A) (R, error) {
		return fn(ctx, self.Self(), args)
	}, cacheFn)
}

// NodeFunc is the same as Func, except it passes the ObjectResult instead of the
// receiver so that you can access its ID.
func NodeFunc[T Typed, A any, R any](name string, fn NodeFuncHandler[T, A, R]) Field[T] {
	return NodeFuncWithCacheKey(name, fn, nil)
}

// NodeFuncWithCacheKey is like NodeFunc but allows specifying a custom digest that will be used to cache the operation in dagql.
func NodeFuncWithCacheKey[T Typed, A any, R any](
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

			return NewResultForCurrentID(ctx, res)
		},
	}

	if cacheFn != nil {
		field.Spec.GetCacheConfig = func(ctx context.Context, self AnyResult, argVals map[string]Input, view call.View, req GetCacheConfigRequest) (*GetCacheConfigResponse, error) {
			if argsErr != nil {
				// this error is deferred until runtime, since it's better (at least
				// more testable) than panicking
				return nil, argsErr
			}
			var args A
			if err := spec.Args.Decode(argVals, &args, view); err != nil {
				return nil, err
			}
			inst, ok := self.(ObjectResult[T])
			if !ok {
				return nil, fmt.Errorf("expected instance of %T, got %T", field, self)
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

	// If set, the result of this field will never be cached and not have concurrent equal
	// calls deduped. The string value is a reason why the field should not be cached.
	DoNotCache string

	// If set, the result of this field will be cached for the given TTL (in seconds).
	TTL int64

	// If set, this GetCacheConfig will be called before ID evaluation to make
	// any dynamic adjustments to the cache key or args
	GetCacheConfig GenericGetCacheConfigFunc

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
		fields = append(fields, Field[T]{
			Spec: &FieldSpec{
				Name:               name,
				Type:               field.Value,
				Description:        field.Field.Tag.Get("doc"),
				DeprecatedReason:   field.Field.Tag.Get("deprecated"),
				ExperimentalReason: field.Field.Tag.Get("experimental"),
			},
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
}

type GenericGetCacheConfigFunc func(
	context.Context,
	AnyResult,
	map[string]Input,
	call.View,
	GetCacheConfigRequest,
) (*GetCacheConfigResponse, error)

type GetCacheConfigFunc[T Typed, A any] func(
	context.Context,
	ObjectResult[T],
	A,
	GetCacheConfigRequest,
) (*GetCacheConfigResponse, error)

type GetCacheConfigRequest struct {
	CacheKey CacheKey
}

type GetCacheConfigResponse struct {
	CacheKey    CacheKey
	UpdatedArgs map[string]Input
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

			retVal, err := NewResultForCurrentID(ctx, t)
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
