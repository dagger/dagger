package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/engine/slog"
)

const (
	MaxFunctionCacheTTLSeconds = 7 * 24 * 60 * 60 // 1 week
	MinFunctionCacheTTLSeconds = 1
)

type ModuleFunction struct {
	mod    dagql.ObjectResult[*Module]
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
	mod dagql.ObjectResult[*Module],
	objDef *ObjectTypeDef,
	metadata *Function,
) (*ModuleFunction, error) {
	modInst := NewUserMod(mod)
	returnType, ok, err := modInst.ModTypeFor(ctx, metadata.ReturnType.Self(), true)
	if err != nil {
		return nil, fmt.Errorf("get mod type for function %q return type: %w", metadata.Name, err)
	}
	if !ok {
		return nil, fmt.Errorf("find mod type for function %q return type: %q", metadata.Name, metadata.ReturnType.Self().ToType())
	}

	argTypes := make(map[string]*UserModFunctionArg, len(metadata.Args))
	for _, argMetadataRes := range metadata.Args {
		argMetadata := argMetadataRes.Self()
		argModType, ok, err := modInst.ModTypeFor(ctx, argMetadata.TypeDef.Self(), true)
		if err != nil {
			return nil, fmt.Errorf("get mod type for function %q arg %q type: %w", metadata.Name, argMetadata.Name, err)
		}
		if !ok {
			return nil, fmt.Errorf("find mod type for function %q arg %q type: %q", metadata.Name, argMetadata.Name, argMetadata.TypeDef.Self().ToType())
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
}

type CallInput struct {
	Name  string
	Value dagql.Typed
}

func (fn *ModuleFunction) recordCall(ctx context.Context) {
	mod := fn.mod.Self()
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
		moduleAnalyticsProps(caller.Self(), "caller_", props)
	} else if dagql.IsInternal(ctx) {
		props["caller_type"] = "internal"
	} else {
		props["caller_type"] = "direct"
	}
	analytics.Ctx(ctx).Capture(ctx, "module_call", props)
}

func (fn *ModuleFunction) cacheImplicitInputs() []dagql.ImplicitInput {
	if fn == nil || fn.mod.Self() == nil || fn.metadata == nil {
		return nil
	}

	var implicitInputs []dagql.ImplicitInput
	cachePolicy := fn.metadata.derivedCachePolicy(fn.mod.Self())
	if cachePolicy == FunctionCachePolicyPerSession {
		implicitInputs = append(implicitInputs, dagql.PerSessionInput)
	}

	// Mix the module variant digest into the cache key so that the same module
	// source loaded with different legacy customizations (which rewrite primitive
	// defaults) does not share cached function results across variants.
	if variant := fn.mod.Self().AsModuleVariantDigest; variant != "" {
		implicitInputs = append(implicitInputs, dagql.ImplicitInput{
			Name: "cachePerModuleVariant",
			Resolver: func(context.Context, map[string]dagql.Input) (dagql.Input, error) {
				return dagql.NewString(variant), nil
			},
		})
	}

	return implicitInputs
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
	for _, argRes := range fn.metadata.Args {
		arg := argRes.Self()
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
			userDefaultInput, err := userDefault.CallInput(ctx)
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
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("current dagql server: %w", err)
	}
	updatedMetadata := fn.metadata
	for argName, arg := range fn.args {
		argDefault, ok, err := fn.UserDefault(ctx, argName)
		if err != nil {
			return fmt.Errorf("load user default for %s.%s: %w", fn.mod.Self().NameField, fn.metadata.Name, err)
		}
		if !ok {
			continue
		}
		uiFnName := fn.mod.Self().Name()
		if fn.metadata.Name != "" {
			uiFnName += "." + fn.metadata.Name
		}
		console(ctx, "user default: %s(%s=%q)", uiFnName, argName, argDefault.UserInput)
		currentArgRes, ok := updatedMetadata.LookupArg(argName)
		if !ok {
			return fmt.Errorf("find function arg %q on %s", argName, uiFnName)
		}
		updatedArgRes := currentArgRes
		argTypeDef := currentArgRes.Self().TypeDef.Self()
		if argDefault.IsObject() || (argDefault.IsList() &&
			argTypeDef != nil &&
			argTypeDef.Kind == TypeDefKindList &&
			argTypeDef.AsList.Valid &&
			argTypeDef.AsList.Value.Self() != nil &&
			argTypeDef.AsList.Value.Self().ElementTypeDef.Self() != nil &&
			argTypeDef.AsList.Value.Self().ElementTypeDef.Self().Kind == TypeDefKindObject) {
			var optionalType dagql.ObjectResult[*TypeDef]
			if err := dag.Select(ctx, currentArgRes.Self().TypeDef, &optionalType, dagql.Selector{
				Field: "withOptional",
				Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
			}); err != nil {
				return fmt.Errorf("optionalize user-default arg %q: %w", argName, err)
			}
			if optionalType.Self().Optional && !currentArgRes.Self().TypeDef.Self().Optional {
				optionalTypeID, err := optionalType.ID()
				if err != nil {
					return fmt.Errorf("resolve optional type ID for user default arg %q: %w", argName, err)
				}
				if err := dag.Select(ctx, currentArgRes, &updatedArgRes, dagql.Selector{
					Field: "__withTypeDef",
					Args:  []dagql.NamedInput{{Name: "typeDef", Value: dagql.NewID[*TypeDef](optionalTypeID)}},
				}); err != nil {
					return fmt.Errorf("update function arg %q type def: %w", argName, err)
				}
			}
		} else {
			defaultJSON, err := argDefault.UserDefaultPrimitive.JSONValue()
			if err != nil {
				return err
			}
			if err := dag.Select(ctx, currentArgRes, &updatedArgRes, dagql.Selector{
				Field: "__withDefaultValue",
				Args:  []dagql.NamedInput{{Name: "defaultValue", Value: defaultJSON}},
			}); err != nil {
				return fmt.Errorf("update function arg %q default value: %w", argName, err)
			}
		}
		updatedMetadata = updatedMetadata.WithArg(updatedArgRes)
		arg.metadata = updatedArgRes.Self()
	}
	fn.metadata = updatedMetadata
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
	switch udp.Arg.TypeDef.Self().Kind {
	case TypeDefKindString:
		return udp.UserInput, nil
	case TypeDefKindList:
		return strings.Split(udp.UserInput, ","), nil
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
		udp.Function.mod.Self().Name(),
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
	typeDef := udp.Arg.TypeDef.Self().WithOptional(true)
	return typeDef.ToInput().Decoder().DecodeInput(value)
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
	return ud.Arg.TypeDef.Self().Kind == TypeDefKindObject
}

func (ud *UserDefault) IsList() bool {
	return ud.Arg.TypeDef.Self().Kind == TypeDefKindList
}

func (ud *UserDefault) CallInput(ctx context.Context) (*FunctionCallArgValue, error) {
	if !ud.IsObject() &&
		(!ud.IsList() ||
			!ud.Arg.TypeDef.Self().AsList.Valid ||
			ud.Arg.TypeDef.Self().AsList.Value.Self() == nil ||
			ud.Arg.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self() == nil ||
			ud.Arg.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self().Kind != TypeDefKindObject) {
		return ud.UserDefaultPrimitive.CallInput()
	}
	value, err := ud.Value(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ud.errorf(err, "marshal object default")
	}
	return &FunctionCallArgValue{
		Name:  ud.Arg.Name,
		Value: encoded,
	}, nil
}

func (ud *UserDefault) Value(ctx context.Context) (any, error) {
	if !ud.IsObject() && !ud.IsList() {
		return ud.UserDefaultPrimitive.Value()
	}
	// List of non-object elements (e.g. []string) is handled by the primitive path
	if ud.IsList() &&
		ud.Arg.TypeDef.Self().AsList.Valid &&
		ud.Arg.TypeDef.Self().AsList.Value.Self() != nil &&
		ud.Arg.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self() != nil &&
		ud.Arg.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self().Kind != TypeDefKindObject {
		return ud.UserDefaultPrimitive.Value()
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}
	mainClient, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("access main client: %w", err)
	}
	mainCtx := engine.ContextWithClientMetadata(ctx, mainClient)
	// Resolve object from user-supplied "address"
	srv := dagql.CurrentDagqlServer(mainCtx)

	resolveOne := func(userInput, typename string) (any, error) {
		var result dagql.AnyObjectResult
		if err := srv.Select(mainCtx, srv.Root(), &result,
			dagql.Selector{
				Field: "address",
				Args: []dagql.NamedInput{{
					Name:  "value",
					Value: dagql.NewString(userInput),
				}},
			},
			dagql.Selector{
				Field: strings.ToLower(typename),
			},
		); err != nil {
			return nil, ud.errorf(err, "resolve object (%q)", typename)
		}

		id, err := result.Select(mainCtx, srv, dagql.Selector{
			Field: "id",
		})
		if err != nil {
			return nil, ud.errorf(err, "get object ID")
		}
		return id.Unwrap(), nil
	}

	if ud.IsList() {
		// "Secret" -> "secret", "GitRef" -> "gitRef", etc (from the element type)
		typename := ud.Arg.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self().ToType().Name()
		elements := strings.Split(ud.UserInput, ",")
		ids := make([]any, 0, len(elements))
		for _, elem := range elements {
			id, err := resolveOne(strings.TrimSpace(elem), typename)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, nil
	}

	// "Secret" -> "secret", "GitRef" -> "gitRef", etc
	typename := ud.Arg.TypeDef.Self().ToType().Name()
	return resolveOne(ud.UserInput, typename)
}

func (ud *UserDefault) DagqlID(ctx context.Context) (dagql.Input, error) {
	if !ud.IsObject() && !ud.IsList() {
		return nil, ud.errorf(nil, "DagqlID(): primitive type has no ID")
	}
	value, err := ud.Value(ctx)
	if err != nil {
		return nil, ud.errorf(err, "DagqlInput(): decode value")
	}
	// Object case: single ID
	if id, isID := value.(dagql.IDType); isID {
		return id, nil
	}
	// List of objects case: build a DynamicArrayInput from the resolved IDs
	if ids, isList := value.([]any); isList {
		arr := dagql.DynamicArrayInput{
			Elem: ud.Arg.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self().ToInput(),
		}
		for _, rawID := range ids {
			input, ok := rawID.(dagql.Input)
			if !ok {
				return nil, ud.errorf(nil, "DagqlID(): list element is not an input: %T", rawID)
			}
			arr.Values = append(arr.Values, input)
		}
		return arr, nil
	}
	return nil, ud.errorf(nil, "DagqlID(): not an id: %q", value)
}

func (ud *UserDefault) String() string {
	fn := ud.Function
	s := fn.mod.Self().Name()
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

	isConstructor := fn.metadata.Name == ""

	// TODO(workspace-env-expansion): Implement ${VAR} expansion for workspace config string values.
	//
	// Spec:
	// - $VAR and ${VAR} in string config values should resolve to system environment variables
	// - NO cascading: $FOO always means the system env var FOO, never another config key
	// - Resolution should go through the Workspace type (gateway for client context access)
	// - The Workspace type should expose a method for resolving system env vars,
	//   which can later be gated by interactive prompts, allow/deny policies, value injection
	// - Consider docker-compose's variable substitution spec as reference for syntax
	// - For now, string values pass through as-is (no expansion)

	// PATH A: Workspace config (constructor only, no EnvFile)
	if fn.mod.Self().WorkspaceConfig != nil && isConstructor {
		val, ok := lookupConfigCaseInsensitive(fn.mod.Self().WorkspaceConfig, arg.Self().OriginalName, arg.Self().Name)
		if ok {
			return fn.newUserDefault(arg.Self(), configValueToString(val)), true, nil
		}
		// Not in workspace config — only fall through to .env if enabled
		if !fn.mod.Self().DefaultsFromDotEnv {
			return nil, false, nil
		}
	}

	// PATH B: existing .env pipeline (completely unchanged)
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
	userInput, ok, err := defaults.LookupCaseInsensitive(mainCtx, arg.Self().Name)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return fn.newUserDefault(arg.Self(), userInput), true, nil
}

func (fn *ModuleFunction) UserDefaults(ctx context.Context) (*EnvFile, error) {
	objDefaults, err := fn.mod.Self().ObjectUserDefaults(ctx, fn.objDef.OriginalName)
	if err != nil {
		return nil, err
	}
	isConstructor := (fn.metadata.Name == "")
	if isConstructor {
		return objDefaults, nil
	}
	return objDefaults.Namespace(ctx, fn.metadata.OriginalName)
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func (fn *ModuleFunction) DynamicInputsForCall(
	ctx context.Context,
	parent dagql.AnyResult,
	args map[string]dagql.Input,
	view call.View,
	req *dagql.CallRequest,
) error {
	var ctxArgs []*FunctionArg
	var workspaceArgs []*FunctionArg
	var userDefaults []*UserDefault

	// The decoded args map includes schema defaults, but the call ID only
	// contains arguments the caller explicitly provided. Use the call ID so
	// .env user defaults can still override schema defaults like Python "= None".
	explicitArgs := map[string]bool{}
	for _, idArg := range req.Args {
		explicitArgs[idArg.Name] = true
	}

	for _, argMetadataRes := range fn.metadata.Args {
		argMetadata := argMetadataRes.Self()
		if explicitArgs[argMetadata.Name] {
			// was explicitly set by the user, skip
			continue
		}
		if argMetadata.TypeDef.Self().Kind != TypeDefKindObject &&
			(argMetadata.TypeDef.Self().Kind != TypeDefKindList ||
				!argMetadata.TypeDef.Self().AsList.Valid ||
				argMetadata.TypeDef.Self().AsList.Value.Self() == nil ||
				argMetadata.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self() == nil ||
				argMetadata.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self().Kind != TypeDefKindObject) {
			// Only default objects need processing at this time.
			// Primitive default values were already processes earlier
			//  in the flow.
			// This applies to both types of object defaults:
			//  1) "contextual args" from `defaultPath` annotations
			//  2) "user defaults" from user-defined .env
			//  3) "workspace args" that are automatically injected
			continue
		}
		// Check for Workspace arguments first - they're always injected
		if argMetadata.IsWorkspace() {
			workspaceArgs = append(workspaceArgs, argMetadata)
			continue
		}
		userDefault, hasUserDefault, err := fn.UserDefault(ctx, argMetadata.Name)
		if err != nil {
			return fmt.Errorf("%s.%s(%s=): load user default: %w",
				fn.mod.Self().Name(),
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

	if len(ctxArgs) > 0 || len(userDefaults) > 0 || len(workspaceArgs) > 0 {
		type argInput struct {
			argName string
			val     dagql.IDType
		}

		srv := dagql.CurrentDagqlServer(ctx)
		eg, ctx := errgroup.WithContext(ctx)

		// Process "contextual arguments", aka objects with a `defaultPath`
		ctxArgVals := make([]*argInput, len(ctxArgs))
		for i, arg := range ctxArgs {
			eg.Go(func() error {
				ctxVal, err := fn.loadContextualArg(ctx, srv, arg)
				if err != nil {
					return fmt.Errorf("load contextual arg %q: %w", arg.Name, err)
				}

				ctxArgVals[i] = &argInput{
					argName: arg.Name,
					val:     ctxVal,
				}

				return nil
			})
		}

		// Process workspace arguments - automatically inject workspace when not set
		workspaceArgVals := make([]*argInput, len(workspaceArgs))
		for i, arg := range workspaceArgs {
			eg.Go(func() error {
				wsVal, err := fn.loadWorkspaceArg(ctx, srv)
				if err != nil {
					return fmt.Errorf("load workspace arg %q: %w", arg.Name, err)
				}

				workspaceArgVals[i] = &argInput{
					argName: arg.Name,
					val:     wsVal,
				}

				return nil
			})
		}

		// Process user-defined user defaults for objects (and lists of objects)
		type userDefaultArgInput struct {
			argName string
			val     dagql.Input
		}
		userDefaultVals := make([]*userDefaultArgInput, len(userDefaults))
		for i, userDefault := range userDefaults {
			eg.Go(func() error {
				input, err := userDefault.DagqlID(ctx)
				if err != nil {
					return err
				}
				arg := userDefault.Arg
				userDefaultVals[i] = &userDefaultArgInput{
					argName: arg.Name,
					val:     input,
				}
				return nil
			})
		}

		if err := eg.Wait(); err != nil {
			return err
		}

		for _, arg := range ctxArgVals {
			if arg == nil {
				continue
			}
			args[arg.argName] = dagql.Opt(arg.val)
			if err := req.SetArgInput(ctx, arg.argName, dagql.Opt(arg.val), false); err != nil {
				return err
			}
		}
		for _, arg := range workspaceArgVals {
			if arg == nil {
				continue
			}
			args[arg.argName] = dagql.Opt(arg.val)
			if err := req.SetArgInput(ctx, arg.argName, dagql.Opt(arg.val), false); err != nil {
				return err
			}
		}
		for _, arg := range userDefaultVals {
			if arg != nil {
				args[arg.argName] = dagql.Opt(arg.val)
				if err := req.SetArgInput(ctx, arg.argName, dagql.Opt(arg.val), false); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (fn *ModuleFunction) loadFunctionRuntime(ctx context.Context) (_ ModuleRuntime, rerr error) {
	// hide all this internal plumbing making up the call
	ctx, hideSpan := Tracer(ctx).Start(ctx, "load sdk runtime", telemetry.Internal())
	defer telemetry.EndWithCause(hideSpan, &rerr)

	mod := fn.mod.Self()
	if mod.Runtime.Valid {
		return &ContainerRuntime{Container: mod.Runtime.Value}, nil
	}

	if !mod.Source.Valid {
		return nil, fmt.Errorf("no source")
	}

	runtimeImpl, ok := mod.Source.Value.Self().SDKImpl.AsRuntime()
	if !ok {
		return nil, fmt.Errorf("no runtime implemented")
	}

	runtime, err := runtimeImpl.Runtime(ctx, mod.Deps, mod.Source.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to load runtime: %w", err)
	}

	return runtime, nil
}

func (fn *ModuleFunction) Call(ctx context.Context, opts *CallOpts) (t dagql.AnyResult, rerr error) {
	mod := fn.mod.Self()

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

	curCall := dagql.CurrentCall(ctx)
	execMD := engineutil.ExecutionMetadata{
		ClientID:          identity.NewID(),
		Call:              curCall,
		ExecID:            identity.NewID(),
		Internal:          true,
		LockMode:          clientMetadata.LockMode,
		AllowedLLMModules: clientMetadata.AllowedLLMModules,
	}
	if curCall != nil {
		callDigest, err := curCall.RecipeDigest(ctx)
		if err != nil {
			return nil, fmt.Errorf("compute function exec call digest: %w", err)
		}
		execMD.CallDigest = callDigest
	}

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

	modID, err := fn.mod.ID()
	if err != nil {
		return nil, fmt.Errorf("get module ID: %w", err)
	}
	execMD.EncodedModuleID, err = modID.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode module ID: %w", err)
	}

	implementationScopedMod, err := ImplementationScopedModule(ctx, fn.mod)
	if err != nil {
		return nil, fmt.Errorf("get implementation-scoped module: %w", err)
	}
	implementationScopedModID, err := implementationScopedMod.ID()
	if err != nil {
		return nil, fmt.Errorf("get implementation-scoped module ID: %w", err)
	}
	execMD.EncodedContentModuleID, err = implementationScopedModID.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode implementation-scoped module ID: %w", err)
	}

	fnCall := &FunctionCall{
		Name:      fn.metadata.OriginalName,
		Parent:    parentJSON,
		InputArgs: callInputs,
	}
	if opts.ParentTyped != nil {
		parentID, err := opts.ParentTyped.ID()
		if err != nil {
			return nil, fmt.Errorf("get parent ID: %w", err)
		}
		fnCall.ParentID = parentID
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

	// hide all this internal plumbing making up the call
	hideCtx := dagql.WithSkip(ctx)

	runtime, err := fn.loadFunctionRuntime(hideCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to load runtime: %w", err)
	}

	// Delegate the actual function execution to the runtime
	outputBytes, clientID, err := runtime.Call(ctx, &execMD, fnCall)
	if err != nil {
		return nil, err
	}
	_ = clientID

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

	if returnValue != nil && fn.hasWorkspaceArgs() {
		returnType := fn.returnType
		for {
			nullable, ok := returnType.(*NullableType)
			if !ok {
				break
			}
			returnType = nullable.Inner
		}
		if _, ok := returnType.(*ModuleObjectType); ok {
			returnedContent := NewCollectedContent()
			if err := fn.returnType.CollectContent(ctx, returnValue, returnedContent); err != nil {
				return nil, fmt.Errorf("collect content: %w", err)
			}

			// If this function accepts Workspace args and returns a user module
			// object, set a content digest on the result derived from all content
			// it returned. This ensures downstream calls that reference this
			// result get a different cache key when the underlying content
			// changes.
			returnValue, err = returnValue.WithContentDigestAny(ctx, returnedContent.Digest())
			if err != nil {
				return nil, fmt.Errorf("set content digest on module function return value: %w", err)
			}
		}
	}

	return returnValue, nil
}

// hasWorkspaceArgs returns true if any of the function's arguments are of type Workspace.
func (fn *ModuleFunction) hasWorkspaceArgs() bool {
	for _, argRes := range fn.metadata.Args {
		if argRes.Self().IsWorkspace() {
			return true
		}
	}
	return false
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

	source := mod.Source.Value.Self()
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

// loadContainerFromAddress loads a Container from a given address using the Address API.
func loadContainerFromAddress(ctx context.Context, dag *dagql.Server, address string) (dagql.IDType, error) {
	var addr dagql.ObjectResult[*Address]
	err := dag.Select(ctx, dag.Root(), &addr,
		dagql.Selector{
			Field: "address",
			Args: []dagql.NamedInput{
				{Name: "value", Value: dagql.String(address)},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("load address %q for container default: %w", address, err)
	}

	var ctr dagql.ObjectResult[*Container]
	err = dag.Select(ctx, addr, &ctr,
		dagql.Selector{Field: "container"},
	)
	if err != nil {
		return nil, fmt.Errorf("load container from address %q: %w", address, err)
	}

	ctrID, err := ctr.ID()
	if err != nil {
		return nil, fmt.Errorf("get contextual container ID: %w", err)
	}
	return dagql.NewID[*Container](ctrID), nil
}

// loadContextualArg loads a contextual argument from the module context directory or address.
//
// For Directory, it will load the directory from the module context directory.
// For File, it will load the directory containing the file and then query the file ID from this directory.
// For Container, it will load from the given address (e.g. "alpine:latest").
//
// This functions returns the ID of the loaded object.
func (fn *ModuleFunction) loadContextualArg(
	ctx context.Context,
	dag *dagql.Server,
	arg *FunctionArg,
) (dagql.IDType, error) {
	if arg.TypeDef.Self().Kind != TypeDefKindObject {
		return nil, fmt.Errorf("contextual argument %q must be an object", arg.OriginalName)
	}
	if dag == nil {
		return nil, fmt.Errorf("dagql server is nil but required for contextual argument %q", arg.OriginalName)
	}

	// Handle Container types with DefaultAddress
	if arg.DefaultAddress != "" {
		if arg.TypeDef.Self().AsObject.Value.Self().Name != "Container" {
			return nil, fmt.Errorf("defaultAddress can only be used with Container type, not %s", arg.TypeDef.Self().AsObject.Value.Self().Name)
		}
		return loadContainerFromAddress(ctx, dag, arg.DefaultAddress)
	}

	if arg.DefaultPath == "" {
		return nil, fmt.Errorf("argument %q is not a contextual argument", arg.OriginalName)
	}

	// Legacy compat: resolve +defaultPath from workspace root for migrated
	// blueprints/toolchains instead of the module's own source directory.
	if fn.mod.Self().LegacyDefaultPath {
		return fn.loadLegacyDefaultPathArg(ctx, dag, arg)
	}

	switch arg.TypeDef.Self().AsObject.Value.Self().Name {
	case "Directory":
		dir, err := fn.mod.Self().ContextSource.Value.Self().LoadContextDir(ctx, dag, arg.DefaultPath, CopyFilter{
			Exclude: arg.Ignore,
		})
		if err != nil {
			return nil, fmt.Errorf("load contextual directory %q: %w", arg.DefaultPath, err)
		}
		dirID, err := dir.ID()
		if err != nil {
			return nil, fmt.Errorf("get contextual directory ID %q: %w", arg.DefaultPath, err)
		}
		return dagql.NewID[*Directory](dirID), nil

	case "File":
		f, err := fn.mod.Self().ContextSource.Value.Self().LoadContextFile(ctx, dag, arg.DefaultPath)
		if err != nil {
			return nil, fmt.Errorf("load contextual file %q: %w", arg.DefaultPath, err)
		}
		fileID, err := f.ID()
		if err != nil {
			return nil, fmt.Errorf("get contextual file ID %q: %w", arg.DefaultPath, err)
		}
		return dagql.NewID[*File](fileID), nil

	case "GitRepository", "GitRef":
		return fn.loadContextualGitArg(ctx, dag, arg)
	}

	return nil, fmt.Errorf("unknown contextual argument type %q", arg.TypeDef.Self().AsObject.Value.Self().Name)
}

// loadContextualGitArg resolves a +defaultPath argument for GitRepository or
// GitRef types. For local modules with a local git path (. or .git), it uses
// a cache-keyed selector to prevent errant reloads. Otherwise it loads from
// the module context or parses a remote git URL.
func (fn *ModuleFunction) loadContextualGitArg(
	ctx context.Context,
	dag *dagql.Server,
	arg *FunctionArg,
) (dagql.IDType, error) {
	isLocalMod := fn.mod.Self().ContextSource.Value.Self().Kind == ModuleSourceKindLocal
	cleanedPath := filepath.Clean(strings.Trim(arg.DefaultPath, "/"))
	isLocalGit := cleanedPath == "." || cleanedPath == ".git"

	if isLocalMod && isLocalGit {
		switch arg.TypeDef.Self().AsObject.Value.Self().Name {
		case "GitRepository":
			repo, err := fn.mod.Self().ContextSource.Value.Self().LoadContextGit(ctx, dag)
			if err != nil {
				return nil, fmt.Errorf("load contextual git repository %q: %w", arg.DefaultPath, err)
			}
			repoID, err := repo.ID()
			if err != nil {
				return nil, fmt.Errorf("get contextual git repository ID %q: %w", arg.DefaultPath, err)
			}
			return dagql.NewID[*GitRepository](repoID), nil

		case "GitRef":
			repo, err := fn.mod.Self().ContextSource.Value.Self().LoadContextGit(ctx, dag)
			if err != nil {
				return nil, fmt.Errorf("load contextual git ref %q: %w", arg.DefaultPath, err)
			}
			var gitRef dagql.ObjectResult[*GitRef]
			err = dag.Select(ctx, repo, &gitRef,
				dagql.Selector{Field: "head"},
			)
			if err != nil {
				return nil, fmt.Errorf("load contextual git ref %q: %w", arg.DefaultPath, err)
			}
			gitRefID, err := gitRef.ID()
			if err != nil {
				return nil, fmt.Errorf("get contextual git ref ID %q: %w", arg.DefaultPath, err)
			}
			return dagql.NewID[*GitRef](gitRefID), nil
		}
	}

	var git dagql.ObjectResult[*GitRepository]
	if isLocalGit {
		var err error
		git, err = fn.mod.Self().ContextSource.Value.Self().LoadContextGit(ctx, dag)
		if err != nil {
			return nil, err
		}
	} else if gitURL, err := gitutil.ParseURL(arg.DefaultPath); err == nil {
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

	switch arg.TypeDef.Self().AsObject.Value.Self().Name {
	case "GitRepository":
		gitID, err := git.ID()
		if err != nil {
			return nil, fmt.Errorf("get contextual git repository ID: %w", err)
		}
		return dagql.NewID[*GitRepository](gitID), nil
	case "GitRef":
		var gitRef dagql.ObjectResult[*GitRef]
		err := dag.Select(ctx, git, &gitRef,
			dagql.Selector{Field: "head"},
		)
		if err != nil {
			return nil, fmt.Errorf("load contextual git ref: %w", err)
		}
		gitRefID, err := gitRef.ID()
		if err != nil {
			return nil, fmt.Errorf("get contextual git ref ID: %w", err)
		}
		return dagql.NewID[*GitRef](gitRefID), nil
	default:
		return nil, fmt.Errorf("unknown git contextual argument type %q", arg.TypeDef.Self().AsObject.Value.Self().Name)
	}
}

// loadLegacyDefaultPathArg resolves a +defaultPath argument from the workspace
// root instead of the module's own source directory. Used for legacy
// blueprints/toolchains that relied on ContextSource injection.
func (fn *ModuleFunction) loadLegacyDefaultPathArg(
	ctx context.Context,
	dag *dagql.Server,
	arg *FunctionArg,
) (dagql.IDType, error) {
	switch arg.TypeDef.Self().AsObject.Value.Self().Name {
	case "Directory":
		var dir dagql.ObjectResult[*Directory]
		err := dag.Select(ctx, dag.Root(), &dir,
			dagql.Selector{
				Field: "currentWorkspace",
				Args: []dagql.NamedInput{
					{Name: "skipMigrationCheck", Value: dagql.Boolean(true)},
				},
			},
			dagql.Selector{
				Field: "directory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(arg.DefaultPath)},
					{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(arg.Ignore...))},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("load legacy default directory %q: %w", arg.DefaultPath, err)
		}
		dirID, err := dir.ID()
		if err != nil {
			return nil, fmt.Errorf("get legacy default directory ID %q: %w", arg.DefaultPath, err)
		}
		return dagql.NewID[*Directory](dirID), nil

	case "File":
		var f dagql.ObjectResult[*File]
		err := dag.Select(ctx, dag.Root(), &f,
			dagql.Selector{
				Field: "currentWorkspace",
				Args: []dagql.NamedInput{
					{Name: "skipMigrationCheck", Value: dagql.Boolean(true)},
				},
			},
			dagql.Selector{
				Field: "file",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(arg.DefaultPath)},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("load legacy default file %q: %w", arg.DefaultPath, err)
		}
		fileID, err := f.ID()
		if err != nil {
			return nil, fmt.Errorf("get legacy default file ID %q: %w", arg.DefaultPath, err)
		}
		return dagql.NewID[*File](fileID), nil

	case "GitRepository", "GitRef":
		// For git, the legacy path can use the same logic as the regular
		// contextual path — git doesn't resolve relative to the module
		// source directory.
		return fn.loadContextualGitArg(ctx, dag, arg)

	default:
		return nil, fmt.Errorf("legacy-default-path does not support type %q; port to workspace API",
			arg.TypeDef.Self().AsObject.Value.Self().Name)
	}
}

// loadWorkspaceArg loads a workspace argument by resolving it through the
// currentWorkspace query. The workspace is automatically injected into
// module functions that declare a Workspace parameter.
func (fn *ModuleFunction) loadWorkspaceArg(
	ctx context.Context,
	dag *dagql.Server,
) (dagql.IDType, error) {
	if dag == nil {
		return nil, fmt.Errorf("dagql server is nil but required for workspace argument")
	}

	var ws dagql.ObjectResult[*Workspace]
	err := dag.Select(ctx, dag.Root(), &ws,
		dagql.Selector{
			Field: "currentWorkspace",
			Args: []dagql.NamedInput{
				{Name: "skipMigrationCheck", Value: dagql.Boolean(true)},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}

	wsID, err := ws.ID()
	if err != nil {
		return nil, fmt.Errorf("get workspace ID: %w", err)
	}
	return dagql.NewID[*Workspace](wsID), nil
}

func (fn *ModuleFunction) applyIgnoreOnDir(ctx context.Context, dag *dagql.Server, arg *FunctionArg, value any) (any, error) {
	if kind := arg.TypeDef.Self().Kind; kind != TypeDefKindObject {
		return nil, fmt.Errorf("[kind=%v] argument %q must be of type Directory to apply ignore pattern: [%s]", kind, arg.OriginalName, strings.Join(arg.Ignore, ","))
	}
	if objName := arg.TypeDef.Self().AsObject.Value.Self().Name; objName != "Directory" {
		return nil, fmt.Errorf("[ObjName=%v] argument %q must be of type Directory to apply ignore pattern: [%s]", objName, arg.OriginalName, strings.Join(arg.Ignore, ","))
	}

	if dag == nil {
		return nil, fmt.Errorf("dagql server is nil but required to ignore pattern on directory %q", arg.OriginalName)
	}

	applyIgnore := func(dir dagql.IDable) (JSON, error) {
		var ignoredDir dagql.Result[*Directory]
		dirID, err := dir.ID()
		if err != nil {
			return nil, fmt.Errorf("get directory ID for ignore on %q: %w", arg.OriginalName, err)
		}

		err = dag.Select(ctx, dag.Root(), &ignoredDir,
			dagql.Selector{
				Field: "directory",
			},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "source", Value: dagql.NewID[*Directory](dirID)},
					{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(arg.Ignore...))},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("apply ignore pattern on directory %q: %w", arg.OriginalName, err)
		}

		ignoredDirID, err := ignoredDir.ID()
		if err != nil {
			return nil, fmt.Errorf("apply ignore pattern on directory %q: %w", arg.Name, err)
		}
		encodedDirID, err := ignoredDirID.Encode()
		if err != nil {
			return nil, fmt.Errorf("encode ignored directory ID for %q: %w", arg.Name, err)
		}

		return JSON(encodedDirID), nil
	}

	switch value := value.(type) {
	case DynamicID:
		return applyIgnore(value)
	case dagql.ID[*Directory]:
		return applyIgnore(value)
	case dagql.Optional[dagql.IDType]:
		if !value.Valid {
			return nil, nil
		}
		id := value.Value
		if dirid, ok := id.(dagql.ID[*Directory]); ok {
			return applyIgnore(dirid)
		}
		return nil, fmt.Errorf("not a directory id: %#v", id)
	case dagql.DynamicOptional:
		if !value.Valid {
			return nil, nil
		}
		switch id := value.Value.(type) {
		case DynamicID:
			return applyIgnore(id)
		case dagql.ID[*Directory]:
			return applyIgnore(id)
		case dagql.IDType:
			if dirid, ok := id.(dagql.ID[*Directory]); ok {
				return applyIgnore(dirid)
			}
			return nil, fmt.Errorf("not a directory id: %#v", id)
		default:
			return nil, fmt.Errorf("not a directory id: %#v", value.Value)
		}
	default:
		return nil, fmt.Errorf("argument %q must be of type Directory to apply ignore pattern ([%s]) but type is %#v", arg.OriginalName, strings.Join(arg.Ignore, ", "), value)
	}
}

// lookupConfigCaseInsensitive performs a case-insensitive lookup in a workspace
// config map, trying each of the provided names in order.
func lookupConfigCaseInsensitive(m map[string]any, names ...string) (any, bool) {
	for _, name := range names {
		for k, v := range m {
			if strings.EqualFold(k, name) {
				return v, true
			}
		}
	}
	return nil, false
}

// configValueToString converts a typed config value to its string representation.
// Strings pass through as-is, bools become "true"/"false", numbers their decimal
// representation, and arrays are JSON-encoded.
func configValueToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case int:
		return strconv.Itoa(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case []any:
		encoded, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(encoded)
	case []string:
		encoded, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(encoded)
	default:
		return fmt.Sprint(v)
	}
}
