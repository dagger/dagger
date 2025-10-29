package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"dagger.io/dagger/telemetry"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	bksolver "github.com/dagger/dagger/internal/buildkit/solver"
	llberror "github.com/dagger/dagger/internal/buildkit/solver/llbsolver/errdefs"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	bkworker "github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
)

const MaxFunctionCacheTTLSeconds = 7 * 24 * 60 * 60 // 1 week
const MinFunctionCacheTTLSeconds = 1

type ModuleFunction struct {
	mod    *Module
	objDef *ObjectTypeDef // may be nil for special functions like the module definition function call

	metadata   *Function
	returnType ModType
	args       map[string]*UserModFunctionArg
}

var _ Callable = &ModuleFunction{}

type UserModFunctionArg struct {
	metadata *FunctionArg
	modType  ModType
}

func NewModFunction(
	ctx context.Context,
	mod *Module,
	objDef *ObjectTypeDef,
	metadata *Function,
) (*ModuleFunction, error) {
	returnType, ok, err := mod.ModTypeFor(ctx, metadata.ReturnType, true)
	if err != nil {
		return nil, fmt.Errorf("get mod type for function %q return type: %w", metadata.Name, err)
	}
	if !ok {
		return nil, fmt.Errorf("find mod type for function %q return type: %q", metadata.Name, metadata.ReturnType.ToType())
	}

	argTypes := make(map[string]*UserModFunctionArg, len(metadata.Args))
	for _, argMetadata := range metadata.Args {
		argModType, ok, err := mod.ModTypeFor(ctx, argMetadata.TypeDef, true)
		if err != nil {
			return nil, fmt.Errorf("get mod type for function %q arg %q type: %w", metadata.Name, argMetadata.Name, err)
		}
		if !ok {
			return nil, fmt.Errorf("find mod type for function %q arg %q type: %q", metadata.Name, argMetadata.Name, argMetadata.TypeDef.ToType())
		}
		argTypes[argMetadata.Name] = &UserModFunctionArg{
			metadata: argMetadata,
			modType:  argModType,
		}
	}

	return &ModuleFunction{
		mod:        mod,
		objDef:     objDef,
		metadata:   metadata,
		returnType: returnType,
		args:       argTypes,
	}, nil
}

type CallOpts struct {
	Inputs         []CallInput
	ParentTyped    dagql.AnyResult
	ParentFields   map[string]any
	SkipSelfSchema bool
	Server         *dagql.Server

	// If set, persistently cache the result of the function call using this
	// key rather than the one provided to use through CurrentStorageKey(ctx)
	// from the cache.
	//
	// This is currently only used for the special function call made directly
	// that retrieves module typedefs.
	// TODO:(sipsma) remove this nonsense once all SDKs have migrated to the new
	// way of obtaining module typedefs that doesn't involve a function call.
	OverrideStorageKey string
}

type CallInput struct {
	Name  string
	Value dagql.Typed
}

func (fn *ModuleFunction) recordCall(ctx context.Context) {
	mod := fn.mod
	if fn.metadata.Name == "" {
		return
	}
	props := map[string]string{
		"target_function": fn.metadata.Name,
	}
	moduleAnalyticsProps(mod, "target_", props)
	query, err := CurrentQuery(ctx)
	if err != nil {
		slog.Error("get current query for module call analytics", "err", err)
		return
	}
	if caller, err := query.CurrentModule(ctx); err == nil {
		props["caller_type"] = "module"
		moduleAnalyticsProps(caller, "caller_", props)
	} else if dagql.IsInternal(ctx) {
		props["caller_type"] = "internal"
	} else {
		props["caller_type"] = "direct"
	}
	analytics.Ctx(ctx).Capture(ctx, "module_call", props)
}

// setCallInputs sets the call inputs for the function call.
//
// It first loads the argument set by the user.
// Then the default values.
// Finally the contextual arguments.
func (fn *ModuleFunction) setCallInputs(ctx context.Context, opts *CallOpts) ([]*FunctionCallArgValue, error) {
	callInputs := make([]*FunctionCallArgValue, len(opts.Inputs))
	hasArg := map[string]bool{}

	for i, input := range opts.Inputs {
		normalizedName := gqlArgName(input.Name)
		arg, ok := fn.args[normalizedName]
		if !ok {
			return nil, fmt.Errorf("find arg %q", input.Name)
		}

		name := arg.metadata.OriginalName

		converted, err := arg.modType.ConvertToSDKInput(ctx, input.Value)
		if err != nil {
			return nil, fmt.Errorf("convert arg %q: %w", input.Name, err)
		}

		if len(arg.metadata.Ignore) > 0 && !arg.metadata.isContextual() { // contextual args already have ignore applied
			converted, err = fn.applyIgnoreOnDir(ctx, opts.Server, arg.metadata, converted)
			if err != nil {
				return nil, fmt.Errorf("apply ignore pattern on arg %q: %w", input.Name, err)
			}
		}

		encoded, err := json.Marshal(converted)
		if err != nil {
			return nil, fmt.Errorf("marshal arg %q: %w", input.Name, err)
		}

		callInputs[i] = &FunctionCallArgValue{
			Name:  name,
			Value: encoded,
		}

		hasArg[name] = true
	}

	// Load default value
	for _, arg := range fn.metadata.Args {
		name := arg.OriginalName
		if hasArg[name] {
			continue
		}
		userDefault, hasUserDefault, err := fn.UserDefault(ctx, arg.Name)
		if err != nil {
			return nil, fmt.Errorf("load user defaults for function %q: %w", fn.metadata.Name, err)
		}
		hasModuleDefault := (arg.DefaultValue != nil)

		var defaultInput *FunctionCallArgValue
		if hasUserDefault {
			// 1. User-defined user default
			userDefaultInput, err := userDefault.CallInput()
			if err != nil {
				return nil, err
			}
			defaultInput = userDefaultInput
		} else if hasModuleDefault {
			// 2. Module-defined default
			defaultInput = &FunctionCallArgValue{
				Name:  name,
				Value: arg.DefaultValue,
			}
		} else {
			// 3. No default. moving on
			continue
		}
		callInputs = append(callInputs, defaultInput)
		hasArg[name] = true
	}
	return callInputs, nil
}

// Load the user defaults for this function, and apply them to the function's
// typedefs. This makes the user defaults visible in typedef introspection.
// It does not affect applying user defaults *at function call*
func (fn *ModuleFunction) mergeUserDefaultsTypeDefs(ctx context.Context) error {
	for argName, arg := range fn.args {
		argDefault, ok, err := fn.UserDefault(ctx, argName)
		if err != nil {
			return fmt.Errorf("load user default for %s.%s: %w", fn.mod.NameField, fn.metadata.Name, err)
		}
		if !ok {
			continue
		}
		uiFnName := fn.mod.Name()
		if fn.metadata.Name != "" {
			uiFnName += "." + fn.metadata.Name
		}
		console(ctx, "user default: %s(%s=%q)", uiFnName, argName, argDefault.UserInput)
		if argDefault.IsObject() {
			// FIXME (cosmetic): expose the user default value to the client, without
			// breaking other things
			arg.metadata.TypeDef.Optional = true
		} else {
			defaultJSON, err := argDefault.UserDefaultPrimitive.JSONValue()
			if err != nil {
				return err
			}
			arg.metadata.DefaultValue = defaultJSON
		}
	}
	return nil
}

// Print text directly on the user's console
func console(ctx context.Context, msg string, args ...any) {
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprintf(telemetry.GlobalWriter(ctx, ""), msg, args...)
}

// A user-defined default value that is a primitive type (not an object)
// Unlike default objects, it can be safely manipulated without making
// nested dagql query
type UserDefaultPrimitive struct {
	Function  *ModuleFunction
	Arg       *FunctionArg
	UserInput string
}

func (udp *UserDefaultPrimitive) JSONValue() (JSON, error) {
	value, err := udp.Value()
	if err != nil {
		return nil, err
	}
	if jsonValue, ok := value.(JSON); ok {
		return jsonValue, nil
	}
	jsonValue, err := json.Marshal(value)
	if err != nil {
		return nil, udp.errorf(err, "marshal to json")
	}
	return JSON(jsonValue), nil
}

func (udp *UserDefaultPrimitive) CallInput() (*FunctionCallArgValue, error) {
	jsonValue, err := udp.JSONValue()
	if err != nil {
		return nil, udp.errorf(err, "get json value")
	}
	return &FunctionCallArgValue{
		Name:  udp.Arg.Name,
		Value: jsonValue,
	}, nil
}

func (udp *UserDefaultPrimitive) Value() (any, error) {
	switch udp.Arg.TypeDef.Kind {
	case TypeDefKindString:
		return udp.UserInput, nil
	case TypeDefKindInteger:
		v, err := strconv.Atoi(udp.UserInput)
		if err != nil {
			return nil, udp.errorf(err, "parse as integer")
		}
		return v, nil
	case TypeDefKindObject:
		return nil, fmt.Errorf("can't get primitive value from object default value")
	}
	// Default: interpret user input as raw JSON
	if v := []byte(udp.UserInput); json.Valid(v) {
		return JSON(v), nil
	}
	return nil, udp.errorf(nil, "not valid JSON: '%s'", udp.UserInput)
}

func (udp *UserDefaultPrimitive) errorf(err error, msg string, args ...any) error {
	fullMessage := fmt.Sprintf("user defaults %s.%s(%s=...): %s",
		udp.Function.mod.Name(),
		udp.Function.metadata.Name,
		udp.Arg.Name,
		fmt.Sprintf(msg, args...),
	)
	if err == nil {
		return errors.New(fullMessage)
	}
	return fmt.Errorf("%s: %w", fullMessage, err)
}

func (udp *UserDefaultPrimitive) DagqlInput() (dagql.Input, error) {
	value, err := udp.Value()
	if err != nil {
		return nil, err
	}
	arg := udp.Arg.Clone()
	arg.TypeDef.Optional = true
	return arg.TypeDef.ToInput().Decoder().DecodeInput(value)
}

func (fn *ModuleFunction) newUserDefault(arg *FunctionArg, userInput string) *UserDefault {
	return &UserDefault{
		UserDefaultPrimitive{
			Function:  fn,
			Arg:       arg,
			UserInput: userInput,
		},
	}
}

type UserDefault struct {
	UserDefaultPrimitive
}

func (ud *UserDefault) IsObject() bool {
	return ud.Arg.TypeDef.Kind == TypeDefKindObject
}

func (ud *UserDefault) Value(ctx context.Context) (any, error) {
	if !ud.IsObject() {
		return ud.UserDefaultPrimitive.Value()
	}
	// Resolve object from user-supplied "address"
	srv := dagql.CurrentDagqlServer(ctx)
	// "Secret" -> "secret", "GitRef" -> "gitRef", etc
	typename := ud.Arg.TypeDef.ToType().Name()
	typename = strings.ToLower(typename[0:1]) + typename[1:]
	var result dagql.AnyResult
	if err := srv.Select(ctx, srv.Root(), &result,
		dagql.Selector{
			Field: "address",
			Args: []dagql.NamedInput{{
				Name:  "value",
				Value: dagql.NewString(ud.UserInput),
			}},
		},
		dagql.Selector{
			Field: strings.ToLower(typename),
		},
		dagql.Selector{
			Field: "id",
		},
	); err != nil {
		return nil, ud.errorf(err, "resolve object (%q)", typename)
	}
	return result.Unwrap(), nil
}

func (ud *UserDefault) DagqlID(ctx context.Context) (dagql.IDType, error) {
	if !ud.IsObject() {
		return nil, ud.errorf(nil, "DagqlID(): primitive type has not ID")
	}
	value, err := ud.Value(ctx)
	if err != nil {
		return nil, ud.errorf(err, "DagqlInput(): decode value")
	}
	id, isID := value.(dagql.IDType)
	if isID {
		return id, nil
	}
	return nil, ud.errorf(nil, "DagqlID(): not an id: %q", value)
}

func (ud *UserDefault) String() string {
	fn := ud.Function
	s := fn.mod.Name()
	if fnName := fn.metadata.Name; fnName != "" {
		s += ("." + fnName)
	}
	s += fmt.Sprintf("(%s=%q)", ud.Arg.Name, ud.UserInput)
	return s
}

// Lookup a user default for this function
func (fn *ModuleFunction) UserDefault(ctx context.Context, argName string) (*UserDefault, bool, error) {
	// Lookup the argument typedef
	arg, ok := fn.metadata.LookupArg(argName)
	if !ok {
		return nil, false, fmt.Errorf("lookup default: function %q has no argument %q", fn.metadata.Name, argName)
	}
	// Lookup user default for the requested arg
	// We need access to the main client's context for resolving system env variables
	// (otherwise we may resolve them in the module container's context)
	// so we upgrade the context to the main client.
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get current query: %w", err)
	}
	mainClient, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("access main client: %w", err)
	}
	mainCtx := engine.ContextWithClientMetadata(ctx, mainClient)
	// Get all defaults for this function
	// FIXME: we shouldn't need the main client context here (we don't need to evaluate env values yet)
	defaults, err := fn.UserDefaults(mainCtx)
	if err != nil {
		return nil, false, fmt.Errorf("lookup defaults for function %q: %w", fn.metadata.Name, err)
	}
	userInput, ok, err := defaults.LookupCaseInsensitive(mainCtx, arg.Name)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return fn.newUserDefault(arg, userInput), true, nil
}

func (fn *ModuleFunction) UserDefaults(ctx context.Context) (*EnvFile, error) {
	objDefaults, err := fn.mod.ObjectUserDefaults(ctx, fn.objDef.OriginalName)
	if err != nil {
		return nil, err
	}
	isConstructor := (fn.metadata.Name == "")
	if isConstructor {
		return objDefaults, nil
	}
	return objDefaults.Namespace(ctx, fn.metadata.OriginalName)
}

func (fn *ModuleFunction) CacheConfigForCall(
	ctx context.Context,
	parent dagql.AnyResult,
	args map[string]dagql.Input,
	view call.View,
	req dagql.GetCacheConfigRequest,
) (*dagql.GetCacheConfigResponse, error) {
	cacheCfgResp, err := fn.mod.CacheConfigForCall(ctx, parent, args, view, req)
	if err != nil {
		return nil, err
	}

	dgstInputs := []string{cacheCfgResp.CacheKey.CallKey}

	var ctxArgs []*FunctionArg
	var userDefaults []*UserDefault

	for _, argMetadata := range fn.metadata.Args {
		if args[argMetadata.Name] != nil {
			// was explicitly set by the user, skip
			continue
		}
		if argMetadata.TypeDef.Kind != TypeDefKindObject {
			// Only default objects need processing at this time.
			// Primitive default values were already processes earlier
			//  in the flow.
			// This applies to both types of object defaults:
			//  1) "contextual args" from `defaultPath` annotations
			//  2) "user defaults" from user-defined .env
			continue
		}
		userDefault, hasUserDefault, err := fn.UserDefault(ctx, argMetadata.Name)
		if err != nil {
			return nil, fmt.Errorf("%s.%s(%s=): load user default: %w",
				fn.mod.Name(),
				fn.metadata.Name,
				argMetadata.Name,
				err,
			)
		}
		if hasUserDefault {
			userDefaults = append(userDefaults, userDefault)
		} else if argMetadata.isContextual() {
			ctxArgs = append(ctxArgs, argMetadata)
		}
	}

	if len(ctxArgs) > 0 || len(userDefaults) > 0 {
		cacheCfgResp.UpdatedArgs = make(map[string]dagql.Input)
		var mu sync.Mutex
		type argInput struct {
			name string
			val  dagql.IDType
		}

		srv := dagql.CurrentDagqlServer(ctx)
		eg, ctx := errgroup.WithContext(ctx)

		// Process "contextual arguments", aka objects with a `defaulPath`
		ctxArgVals := make([]*argInput, len(ctxArgs))
		for i, arg := range ctxArgs {
			eg.Go(func() error {
				ctxVal, err := fn.loadContextualArg(ctx, srv, arg)
				if err != nil {
					return fmt.Errorf("load contextual arg %q: %w", arg.Name, err)
				}

				ctxArgVals[i] = &argInput{
					name: arg.OriginalName,
					val:  ctxVal,
				}
				mu.Lock()
				cacheCfgResp.UpdatedArgs[arg.Name] = dagql.Opt(ctxVal)
				mu.Unlock()

				return nil
			})
		}

		// Process user-defined user defaults for objects
		userDefaultVals := make([]*argInput, len(userDefaults))
		for i, userDefault := range userDefaults {
			i, userDefault := i, userDefault
			eg.Go(func() error {
				id, err := userDefault.DagqlID(ctx)
				if err != nil {
					return err
				}
				arg := userDefault.Arg
				userDefaultVals[i] = &argInput{
					name: arg.OriginalName,
					val:  id,
				}
				mu.Lock()
				cacheCfgResp.UpdatedArgs[arg.Name] = dagql.Opt(id)
				mu.Unlock()
				return nil
			})
		}

		if err := eg.Wait(); err != nil {
			return nil, err
		}

		for _, arg := range ctxArgVals {
			dgstInputs = append(dgstInputs, arg.name, arg.val.ID().Digest().String())
		}
		for _, arg := range userDefaultVals {
			if arg != nil {
				dgstInputs = append(dgstInputs, arg.name, arg.val.ID().Digest().String())
			}
		}
	}

	if cachePolicy := fn.metadata.derivedCachePolicy(fn.mod); cachePolicy == FunctionCachePolicyPerSession {
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, err
		}
		dgstInputs = append(dgstInputs, clientMetadata.SessionID)
	}

	cacheCfgResp.CacheKey.CallKey = hashutil.HashStrings(dgstInputs...).String()
	return cacheCfgResp, nil
}

func (fn *ModuleFunction) loadFunctionRuntime(ctx context.Context) (runtime dagql.ObjectResult[*Container], err error) {
	mod := fn.mod
	srv := dagql.CurrentDagqlServer(ctx)

	modObj, err := dagql.NewObjectResultForID(mod, srv, mod.ResultID)
	if err != nil {
		return runtime, fmt.Errorf("failed to load module: %w", err)
	}

	err = srv.Select(ctx, modObj, &runtime,
		dagql.Selector{
			Field: "runtime",
		},
	)
	if err != nil {
		return runtime, fmt.Errorf("failed to load runtime: %w", err)
	}

	return runtime, nil
}

func (fn *ModuleFunction) Call(ctx context.Context, opts *CallOpts) (t dagql.AnyResult, rerr error) { //nolint: gocyclo
	mod := fn.mod

	lg := bklog.G(ctx).WithField("module", mod.Name()).WithField("function", fn.metadata.Name)
	if fn.objDef != nil {
		lg = lg.WithField("object", fn.objDef.Name)
	}
	ctx = bklog.WithLogger(ctx, lg)

	// Capture analytics for the function call.
	// Calls without function name are internal and excluded.
	fn.recordCall(ctx)

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	callID := dagql.CurrentID(ctx)
	execMD := buildkit.ExecutionMetadata{
		ClientID:          identity.NewID(),
		CallID:            callID,
		ExecID:            identity.NewID(),
		Internal:          true,
		ParentIDs:         map[digest.Digest]*resource.ID{},
		AllowedLLMModules: clientMetadata.AllowedLLMModules,
	}

	var cacheMixins []string
	if opts.OverrideStorageKey != "" {
		cacheMixins = append(cacheMixins, opts.OverrideStorageKey)
	} else {
		cacheMixins = append(cacheMixins, cache.CurrentStorageKey(ctx))
	}

	execMD.CacheMixin = hashutil.HashStrings(cacheMixins...)

	callInputs, err := fn.setCallInputs(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("set call inputs: %w", err)
	}

	bklog.G(ctx).Debug("function call")
	defer func() {
		bklog.G(ctx).Debug("function call done")
		if rerr != nil {
			bklog.G(ctx).WithError(rerr).Error("function call errored")
		}
	}()

	parentJSON, err := json.Marshal(opts.ParentFields)
	if err != nil {
		return nil, fmt.Errorf("marshal parent value: %w", err)
	}

	if opts.ParentTyped != nil {
		// collect any client resources stored in parent fields (secrets/sockets/etc.) and grant
		// this function client access
		parentModType, ok, err := mod.ModTypeFor(ctx, &TypeDef{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(fn.objDef),
		}, true)
		if err != nil {
			return nil, fmt.Errorf("get mod type for parent: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("find mod type for parent %q", fn.objDef.Name)
		}
		if err := parentModType.CollectCoreIDs(ctx, opts.ParentTyped, execMD.ParentIDs); err != nil {
			return nil, fmt.Errorf("collect IDs from parent fields: %w", err)
		}
	}

	if mod.ResultID != nil {
		execMD.EncodedModuleID, err = mod.ResultID.Encode()
		if err != nil {
			return nil, fmt.Errorf("encode module ID: %w", err)
		}
	}

	fnCall := &FunctionCall{
		Name:      fn.metadata.OriginalName,
		Parent:    parentJSON,
		ParentID:  callID.Receiver(),
		InputArgs: callInputs,
	}
	if envID, ok := EnvIDFromContext(ctx); ok {
		fnCall.EnvID = envID
	}
	if fn.objDef != nil {
		fnCall.ParentName = fn.objDef.OriginalName
	}
	execMD.EncodedFunctionCall, err = json.Marshal(fnCall)
	if err != nil {
		return nil, fmt.Errorf("marshal function call: %w", err)
	}

	srv := dagql.CurrentDagqlServer(ctx)

	runtime, err := fn.loadFunctionRuntime(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load runtime: %w", err)
	}

	var metaDir dagql.ObjectResult[*Directory]
	err = srv.Select(ctx, srv.Root(), &metaDir,
		dagql.Selector{
			Field: "directory",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create mod metadata directory: %w", err)
	}

	var ctr dagql.ObjectResult[*Container]
	err = srv.Select(ctx, runtime, &ctr,
		dagql.Selector{
			Field: "withMountedDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(modMetaDirPath)},
				{Name: "source", Value: dagql.NewID[*Directory](metaDir.ID())},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("exec function: %w", err)
	}

	execCtx := ctx
	execCtx = dagql.WithSkip(execCtx) // this span shouldn't be shown (it's entirely useless)
	err = srv.Select(execCtx, ctr, &ctr,
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{Name: "args", Value: dagql.ArrayInput[dagql.String]{}},
				{Name: "useEntrypoint", Value: dagql.NewBoolean(true)},
				{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(true)},
				{Name: "execMD", Value: dagql.NewSerializedString(&execMD)},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("exec function: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("get buildkit client: %w", err)
	}

	_, err = ctr.Self().Evaluate(ctx)
	if err != nil {
		id, ok, extractErr := extractError(ctx, bk, err)
		if extractErr != nil {
			// if the module hasn't provided us with a nice error, just return the
			// original error
			return nil, err
		}
		if ok {
			errInst, err := id.Load(ctx, opts.Server)
			if err != nil {
				return nil, fmt.Errorf("load error instance: %w", err)
			}
			dagErr := errInst.Self().Clone()
			originCtx := trace.SpanContextFromContext(
				telemetry.Propagator.Extract(
					context.Background(),
					telemetry.AnyMapCarrier(dagErr.Extensions()),
				),
			)
			if !originCtx.IsValid() {
				// If the Error doesn't already have an origin, inject the current trace
				// context as its origin.
				tm := propagation.MapCarrier{}
				telemetry.Propagator.Inject(ctx, tm)
				for _, key := range tm.Keys() {
					val := tm.Get(key)
					valJSON, err := json.Marshal(val)
					if err != nil {
						return nil, fmt.Errorf("marshal value: %w", err)
					}
					dagErr.Values = append(dagErr.Values, &ErrorValue{
						Name:  key,
						Value: JSON(valJSON),
					})
				}
			}
			return nil, dagErr
		}
		if fn.metadata.OriginalName == "" {
			return nil, fmt.Errorf("call constructor: %w", err)
		} else {
			return nil, fmt.Errorf("call function %q: %w", fn.metadata.OriginalName, err)
		}
	}

	ctrOutputDir, err := ctr.Self().Directory(ctx, modMetaDirPath)
	if err != nil {
		return nil, fmt.Errorf("get function output directory: %w", err)
	}

	result, err := ctrOutputDir.Evaluate(ctx)
	if err != nil {
		return nil, fmt.Errorf("evaluate function: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("function returned nil result")
	}

	// Read the output of the function
	outputBytes, err := result.Ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: modMetaOutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("read function output file: %w", err)
	}

	var returnValueAny any
	dec := json.NewDecoder(strings.NewReader(string(outputBytes)))
	dec.UseNumber()
	if err := dec.Decode(&returnValueAny); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	returnValue, err := fn.returnType.ConvertFromSDKResult(ctx, returnValueAny)
	if err != nil {
		return nil, fmt.Errorf("convert return value: %w", err)
	}

	safeToPersistCache := true
	if returnValue != nil {
		// Get the client ID actually used during the function call - this might not
		// be the same as execMD.ClientID if the function call was cached at the
		// buildkit level
		clientID, err := ctr.Self().usedClientID(ctx)
		if err != nil {
			return nil, fmt.Errorf("could not get used client id")
		}

		// If the function returned anything that's isolated per-client, this caller client should
		// have access to it now since it was returned to them (i.e. secrets/sockets/etc).
		returnedIDs := map[digest.Digest]*resource.ID{}
		if err := fn.returnType.CollectCoreIDs(ctx, returnValue, returnedIDs); err != nil {
			return nil, fmt.Errorf("collect IDs: %w", err)
		}

		// Function calls are cached per-session, but every client caller needs to add
		// secret/socket/etc. resources from the result to their store.
		returnedIDsList := make([]*resource.ID, 0, len(returnedIDs))
		for _, id := range returnedIDs {
			returnedIDsList = append(returnedIDsList, id)
		}
		secretTransferPostCall, err := ResourceTransferPostCall(ctx, query, clientID, returnedIDsList...)
		if err != nil {
			return nil, fmt.Errorf("create secret transfer post call: %w", err)
		}
		if secretTransferPostCall != nil {
			// this being non-nil indicates there were secrets created by direct SetSecret calls in the
			// returned value. This means we cannot use a persistently cached result, so invalidate the
			// cache for this call in the future.
			safeToPersistCache = false
		}

		returnValue = returnValue.WithPostCall(secretTransferPostCall)
	}
	if returnValue != nil {
		returnValue = returnValue.WithSafeToPersistCache(safeToPersistCache)
	}

	return returnValue, nil
}

func extractError(ctx context.Context, client *buildkit.Client, baseErr error) (dagql.ID[*Error], bool, error) {
	var id dagql.ID[*Error]

	var execErr *llberror.ExecError
	if errors.As(baseErr, &execErr) {
		defer func() {
			execErr.Release()
			execErr.OwnerBorrowed = true
		}()
	}

	var ierr buildkit.RichError
	if !errors.As(baseErr, &ierr) {
		return id, false, nil
	}

	// get the mnt containing module response data (in this case, the error ID)
	var metaMountResult bksolver.Result
	var foundMounts []string
	for i, mnt := range ierr.Mounts {
		foundMounts = append(foundMounts, mnt.Dest)
		if mnt.Dest == modMetaDirPath {
			metaMountResult = execErr.Mounts[i]
			break
		}
	}
	if metaMountResult == nil {
		slog.Warn("find meta mount", "mounts", foundMounts, "want", modMetaDirPath)
		return id, false, nil
	}

	workerRef, ok := metaMountResult.Sys().(*bkworker.WorkerRef)
	if !ok {
		return id, false, errors.Join(baseErr, fmt.Errorf("invalid ref type: %T", metaMountResult.Sys()))
	}
	mntable, err := workerRef.ImmutableRef.Mount(ctx, true, bksession.NewGroup(client.ID()))
	if err != nil {
		return id, false, errors.Join(err, baseErr)
	}

	idBytes, err := buildkit.ReadSnapshotPath(ctx, client, mntable, modMetaErrorPath, -1)
	if err != nil {
		return id, false, errors.Join(err, baseErr)
	}

	if err := id.Decode(string(idBytes)); err != nil {
		return id, false, errors.Join(err, baseErr)
	}

	return id, true, nil
}

func (fn *ModuleFunction) ReturnType() (ModType, error) {
	return fn.returnType, nil
}

func (fn *ModuleFunction) ArgType(argName string) (ModType, error) {
	arg, ok := fn.args[gqlArgName(argName)]
	if !ok {
		return nil, fmt.Errorf("find arg %q", argName)
	}
	return arg.modType, nil
}

func moduleAnalyticsProps(mod *Module, prefix string, props map[string]string) {
	props[prefix+"module_name"] = mod.Name()

	source := mod.ContextSource.Value.Self()
	switch source.Kind {
	case ModuleSourceKindLocal:
		props[prefix+"source_kind"] = "local"
		props[prefix+"local_subpath"] = source.SourceRootSubpath
	case ModuleSourceKindGit:
		git := source.Git
		props[prefix+"source_kind"] = "git"
		props[prefix+"git_symbolic"] = git.Symbolic
		props[prefix+"git_clone_url"] = git.CloneRef // todo(guillaume): remove as deprecated
		props[prefix+"git_clone_ref"] = git.CloneRef
		props[prefix+"git_subpath"] = source.SourceRootSubpath
		props[prefix+"git_version"] = git.Version
		props[prefix+"git_commit"] = git.Commit
		props[prefix+"git_html_repo_url"] = git.HTMLRepoURL
	}
}

// loadContextualArg loads a contextual argument from the module context directory.
//
// For Directory, it will load the directory from the module context directory.
// For file, it will loa the directory containing the file and then query the file ID from this directory.
//
// This functions returns the ID of the loaded object.
func (fn *ModuleFunction) loadContextualArg(
	ctx context.Context,
	dag *dagql.Server,
	arg *FunctionArg,
) (dagql.IDType, error) {
	if arg.TypeDef.Kind != TypeDefKindObject {
		return nil, fmt.Errorf("contextual argument %q must be an object", arg.OriginalName)
	}
	if dag == nil {
		return nil, fmt.Errorf("dagql server is nil but required for contextual argument %q", arg.OriginalName)
	}

	if arg.DefaultPath == "" {
		return nil, fmt.Errorf("argument %q is not a contextual argument", arg.OriginalName)
	}

	switch arg.TypeDef.AsObject.Value.Name {
	case "Directory":
		dir, err := fn.mod.ContextSource.Value.Self().LoadContextDir(ctx, dag, arg.DefaultPath, nil, arg.Ignore)
		if err != nil {
			return nil, fmt.Errorf("load contextual directory %q: %w", arg.DefaultPath, err)
		}
		return dagql.NewID[*Directory](dir.ID()), nil

	case "File":
		file, err := fn.mod.ContextSource.Value.Self().LoadContextFile(ctx, dag, arg.DefaultPath)
		if err != nil {
			return nil, fmt.Errorf("load contextual file %q: %w", arg.DefaultPath, err)
		}
		return dagql.NewID[*File](file.ID()), nil

	case "GitRepository", "GitRef":
		var git dagql.ObjectResult[*GitRepository]

		cleanedPath := filepath.Clean(strings.Trim(arg.DefaultPath, "/"))
		if cleanedPath == "." || cleanedPath == ".git" {
			// handle getting the git repo from the current module context
			var err error
			git, err = fn.mod.ContextSource.Value.Self().LoadContextGit(ctx, dag)
			if err != nil {
				return nil, err
			}
		} else if gitURL, err := gitutil.ParseURL(arg.DefaultPath); err == nil {
			// handle an arbitrary git URL
			args := []dagql.NamedInput{
				{Name: "url", Value: dagql.String(gitURL.String())},
			}
			if gitURL.Fragment != nil {
				args = append(args, dagql.NamedInput{Name: "ref", Value: dagql.String(gitURL.Fragment.Ref)})
			}

			err := dag.Select(ctx, dag.Root(), &git,
				dagql.Selector{Field: "git", Args: args},
			)
			if err != nil {
				return nil, fmt.Errorf("load contextual git repository: %w", err)
			}
		} else {
			return nil, fmt.Errorf("parse git URL %q: %w", arg.DefaultPath, err)
		}

		switch arg.TypeDef.AsObject.Value.Name {
		case "GitRepository":
			return dagql.NewID[*GitRepository](git.ID()), nil

		case "GitRef":
			var gitRef dagql.ObjectResult[*GitRef]
			err := dag.Select(ctx, git, &gitRef,
				dagql.Selector{
					Field: "head",
				},
			)
			if err != nil {
				return nil, fmt.Errorf("load contextual git ref: %w", err)
			}

			return dagql.NewID[*GitRef](gitRef.ID()), nil
		}
	}

	return nil, fmt.Errorf("unknown contextual argument type %q", arg.TypeDef.AsObject.Value.Name)
}

func (fn *ModuleFunction) applyIgnoreOnDir(ctx context.Context, dag *dagql.Server, arg *FunctionArg, value any) (any, error) {
	if kind := arg.TypeDef.Kind; kind != TypeDefKindObject {
		return nil, fmt.Errorf("[kind=%v] argument %q must be of type Directory to apply ignore pattern: [%s]", kind, arg.OriginalName, strings.Join(arg.Ignore, ","))
	}
	if objName := arg.TypeDef.AsObject.Value.Name; objName != "Directory" {
		return nil, fmt.Errorf("[ObjName=%v] argument %q must be of type Directory to apply ignore pattern: [%s]", objName, arg.OriginalName, strings.Join(arg.Ignore, ","))
	}

	if dag == nil {
		return nil, fmt.Errorf("dagql server is nil but required to ignore pattern on directory %q", arg.OriginalName)
	}

	applyIgnore := func(dir dagql.IDable) (JSON, error) {
		var ignoredDir dagql.Result[*Directory]

		err := dag.Select(ctx, dag.Root(), &ignoredDir,
			dagql.Selector{
				Field: "directory",
			},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "source", Value: dagql.NewID[*Directory](dir.ID())},
					{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(arg.Ignore...))},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("apply ignore pattern on directory %q: %w", arg.OriginalName, err)
		}

		dirID, err := ignoredDir.ID().Encode()
		if err != nil {
			return nil, fmt.Errorf("apply ignore pattern on directory %q: %w", arg.Name, err)
		}

		return JSON(dirID), nil
	}

	switch value := value.(type) {
	case DynamicID:
		return applyIgnore(value)
	case dagql.ID[*Directory]:
		return applyIgnore(value)
	case dagql.Optional[dagql.IDType]:
		id := value.Value
		if dirid, ok := id.(dagql.ID[*Directory]); ok {
			return applyIgnore(dirid)
		}
		return nil, fmt.Errorf("not a directory id: %#v", id)
	default:
		return nil, fmt.Errorf("argument %q must be of type Directory to apply ignore pattern ([%s]) but type is %#v", arg.OriginalName, strings.Join(arg.Ignore, ", "), value)
	}
}
