package dagql

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"

	"github.com/dagger/dagger/dagql/call"
)

type SessionCache struct {
	cache *DagqlCache

	results []CacheResult
	mu      sync.Mutex

	seenKeys sync.Map
}

func NewSessionCache(
	baseCache *DagqlCache,
) *SessionCache {
	return &SessionCache{
		cache: baseCache,
	}
}

/* TODO: rm if no longer needed
type CacheCallOpt interface {
	SetCacheCallOpt(*CacheCallOpts)
}

type CacheCallOpts struct {
	Telemetry TelemetryFunc
}

type TelemetryFunc func(context.Context) (context.Context, func(Typed, bool, error))

func (o CacheCallOpts) SetCacheCallOpt(opts *CacheCallOpts) {
	*opts = o
}

type CacheCallOptFunc func(*CacheCallOpts)

func (f CacheCallOptFunc) SetCacheCallOpt(opts *CacheCallOpts) {
	f(opts)
}

func WithTelemetry(telemetry TelemetryFunc) CacheCallOpt {
	return CacheCallOptFunc(func(opts *CacheCallOpts) {
		opts.Telemetry = telemetry
	})
}
*/

func (c *SessionCache) LoadID(
	ctx context.Context,
	s *Server,
	parent Object,
	fieldSpec *FieldSpec,
	cacheSpec CacheSpec,
	callID *call.ID,
	// opts ...CacheCallOpt,
) (res Result, _ *call.ID, rerr error) {
	/* TODO: rm if no longer needed
	var o CacheCallOpts
	for _, opt := range opts {
		opt.SetCacheCallOpt(&o)
	}
	*/

	view := View(callID.View())
	idArgs := callID.Args()
	inputArgs := make(map[string]Input, len(idArgs))
	for _, argSpec := range fieldSpec.Args.Inputs(view) {
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

		default:
			// explicitly include as null
			inputArgs[argSpec.Name] = nil
		}
	}

	doNotCache := cacheSpec.DoNotCache != ""
	_, seen := c.seenKeys.LoadOrStore(callID.Digest(), struct{}{})
	if s.telemetry != nil && (!seen || doNotCache) {
		telemetryCtx, done := s.telemetry(ctx, parent, callID)
		defer func() {
			var val Typed
			var cached bool
			if res != nil {
				val = res
				// TODO: re-add cached = res.HitCache()
			}
			done(val, cached, rerr)
		}()
		ctx = telemetryCtx
	}

	callRes, err := c.cache.Call(ctx, s, parent, callID, inputArgs, callCacheParams{
		DoNotCache: doNotCache,
	})
	if err != nil {
		return nil, nil, err
	}
	if !doNotCache { // TODO: re-asses this condition in the new world, do we still want to track and release these as deps?
		c.mu.Lock()
		c.results = append(c.results, callRes)
		c.mu.Unlock()
	}

	return callRes, callID, nil
}

func (c *SessionCache) Select(
	ctx context.Context,
	s *Server,
	parent Object,
	constructor *call.ID, // TODO: method on Object?
	fieldSpec *FieldSpec, // TODO: method on Object?
	cacheSpec CacheSpec, // TODO: method on Object?
	sel Selector,
	// opts ...CacheCallOpt,
) (res Result, _ *call.ID, rerr error) {
	/* TODO: rm if no longer needed
	var o CacheCallOpts
	for _, opt := range opts {
		opt.SetCacheCallOpt(&o)
	}
	*/

	view := sel.View
	if fieldSpec.ViewFilter == nil {
		// fields in the global view shouldn't attach the current view to the
		// selector (since they're global from all perspectives)
		view = ""
	}

	idArgs := make([]*call.Argument, 0, len(sel.Args))
	inputArgs := make(map[string]Input, len(sel.Args))
	for _, argSpec := range fieldSpec.Args.Inputs(view) {
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

		default:
			// explicitly include as null
			inputArgs[argSpec.Name] = nil
		}
	}
	// TODO: it's better DX if it matches schema order
	sort.Slice(idArgs, func(i, j int) bool {
		return idArgs[i].Name() < idArgs[j].Name()
	})

	astType := fieldSpec.Type.Type()
	if sel.Nth != 0 {
		astType = astType.Elem
	}

	newID := constructor.Append(
		astType,
		sel.Field,
		string(view),
		fieldSpec.Module,
		sel.Nth,
		"",
		idArgs...,
	)

	doNotCache := cacheSpec.DoNotCache != ""
	if cacheSpec.GetCacheConfig != nil {
		origDgst := newID.Digest()

		cacheCfgCtx := idToContext(ctx, newID)
		cacheCfgCtx = srvToContext(cacheCfgCtx, s)
		cacheCfg, err := cacheSpec.GetCacheConfig(cacheCfgCtx, parent, inputArgs, view, CacheConfig{
			Digest: origDgst,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compute cache key for %s.%s: %w", parent.ObjectType().TypeName(), sel.Field, err)
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
			newID = constructor.Append(
				astType,
				sel.Field,
				string(view),
				fieldSpec.Module,
				sel.Nth,
				"",
				idArgs...,
			)
		}

		if cacheCfg.Digest != origDgst {
			newID = newID.WithDigest(cacheCfg.Digest)
		}
	}

	// TODO: dedupe chunk of code
	_, seen := c.seenKeys.LoadOrStore(newID.Digest(), struct{}{})
	if s.telemetry != nil && (!seen || doNotCache) {
		telemetryCtx, done := s.telemetry(ctx, parent, newID)
		defer func() {
			var val Typed
			var cached bool
			if res != nil {
				val = res
				// TODO: re-add cached = res.HitCache()
			}
			done(val, cached, rerr)
		}()
		ctx = telemetryCtx
	}

	callRes, err := c.cache.Call(ctx, s, parent, newID, inputArgs, callCacheParams{
		DoNotCache: doNotCache,
	})
	if err != nil {
		return nil, nil, err
	}

	if !doNotCache { // TODO: re-asses this condition in the new world, do we still want to track and release these as deps?
		c.mu.Lock()
		c.results = append(c.results, callRes)
		c.mu.Unlock()
	}

	return callRes, newID, nil
}

func (c *SessionCache) SelectNth(
	ctx context.Context,
	s *Server,
	enumRes Result,
	nth int,
) (Result, error) {
	return c.cache.SelectNth(ctx, s, enumRes, nth)
}

func (c *SessionCache) ReleaseAll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var rerr error
	for _, res := range c.results {
		rerr = errors.Join(rerr, res.Release(ctx))
	}
	c.results = nil

	return rerr
}
