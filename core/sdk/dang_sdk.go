package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/identity"
	telemetry "github.com/dagger/otel-go"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
	"github.com/vito/dang/pkg/ioctx"
	"go.opentelemetry.io/otel/propagation"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type dangSDK struct {
	root      *core.Query
	rawConfig map[string]any
}

type dangSDKConfig struct {
}

func (sdk *dangSDK) AsRuntime() (core.Runtime, bool) {
	return sdk, true
}

func (sdk *dangSDK) AsModuleTypes() (core.ModuleTypes, bool) {
	return nil, false
}

func (sdk *dangSDK) AsCodeGenerator() (core.CodeGenerator, bool) {
	return sdk, true
}

func (sdk *dangSDK) AsClientGenerator() (core.ClientGenerator, bool) {
	return sdk, true
}

func (sdk *dangSDK) RequiredClientGenerationFiles(_ context.Context) (dagql.Array[dagql.String], error) {
	return dagql.NewStringArray(), nil
}

func (sdk *dangSDK) GenerateClient(
	ctx context.Context,
	modSource dagql.ObjectResult[*core.ModuleSource],
	deps *core.ModDeps,
	outputDir string,
) (inst dagql.ObjectResult[*core.Directory], err error) {
	return inst, fmt.Errorf("dang SDK does not have a client to generate")
}

func (sdk *dangSDK) Codegen(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ *core.GeneratedCode, rerr error) {
	return &core.GeneratedCode{
		// no-op
		Code: source.Self().ContextDirectory,
	}, nil
}

func (sdk *dangSDK) Runtime(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.ObjectResult[*core.ModuleSource],
) (core.ModuleRuntime, error) {
	return &DangRuntime{
		root:      sdk.root,
		modSource: source,
	}, nil
}

// DangRuntime is a native Dang runtime that doesn't use containers
type DangRuntime struct {
	root      *core.Query
	modSource dagql.ObjectResult[*core.ModuleSource]
}

func (r *DangRuntime) AsContainer() (dagql.ObjectResult[*core.Container], bool) {
	// Dang runtime doesn't use containers
	return dagql.ObjectResult[*core.Container]{}, false
}

func (r *DangRuntime) Call(
	ctx context.Context,
	execMD *buildkit.ExecutionMetadata,
	fnCall *core.FunctionCall,
) (res []byte, clientID string, rerr error) {
	defer func() {
		if rerr != nil {
			rerr = convertError(rerr)
		}
	}()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, "", err
	}

	execMD.CallerClientID = clientMetadata.ClientID
	execMD.SessionID = clientMetadata.SessionID
	execMD.AllowedLLMModules = clientMetadata.AllowedLLMModules

	if execMD.CallID == nil {
		execMD.CallID = dagql.CurrentID(ctx)
	}
	if execMD.ExecID == "" {
		execMD.ExecID = identity.NewID()
	}
	if execMD.SecretToken == "" {
		execMD.SecretToken = identity.NewID()
	}
	execMD.ClientStableID = identity.NewID()
	if execMD.EncodedModuleID == "" {
		mod := fnCall.Module
		if mod.ResultID == nil {
			return nil, "", fmt.Errorf("current module has no instance ID")
		}
		execMD.EncodedModuleID, err = mod.ResultID.Encode()
		if err != nil {
			return nil, "", err
		}
	}

	if execMD.HostAliases == nil {
		execMD.HostAliases = make(map[string][]string)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("failed to open listener for dang SDK: %w", err)
	}
	defer l.Close()

	q := r.root

	http2Srv := &http2.Server{}
	httpSrv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: h2c.NewHandler(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			telemetry.Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
			q.ServeHTTPToNestedClient(resp, req, execMD)
		}), http2Srv),
	}
	if err := http2.ConfigureServer(httpSrv, http2Srv); err != nil {
		return nil, "", fmt.Errorf("configure nested client http2 server: %w", err)
	}

	srvCtx, srvCancel := context.WithCancelCause(ctx)
	defer srvCancel(errors.New("runtime cleanup"))

	srvPool := pool.New().WithContext(srvCtx).WithCancelOnError()
	srvPool.Go(func(_ context.Context) error {
		err := httpSrv.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("serve nested client listener: %w", err)
		}
		return nil
	})

	gqlClient := graphql.NewClient(fmt.Sprintf("http://%s/query", l.Addr()), nil)

	schemaJSONFile, err := fnCall.Module.Deps.SchemaIntrospectionJSONFile(ctx, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get current served deps: %w", err)
	}
	var intro introspection.Response
	f, err := schemaJSONFile.Self().Open(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open schema JSON file: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&intro); err != nil {
		return nil, "", fmt.Errorf("failed to decode schema JSON: %w", err)
	}

	// Set up the Dagger import config so dang code can use `import Dag` implicitly
	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
		Name:       "Dagger",
		Client:     gqlClient,
		Schema:     intro.Schema,
		AutoImport: true,
	})

	stdio := telemetry.SpanStdio(ctx, core.InstrumentationLibrary)
	ctx = ioctx.StdoutToContext(ctx, stdio.Stdout)
	ctx = ioctx.StderrToContext(ctx, stdio.Stderr)

	parentName := fnCall.ParentName
	fnName := fnCall.Name
	parentJSON := fnCall.Parent
	fnArgs := fnCall.InputArgs

	modCtx := r.modSource.Self().ContextDirectory

	inputArgs := make(map[string][]byte)
	for _, fnArg := range fnArgs {
		argName := fnArg.Name
		argValue := fnArg.Value
		inputArgs[argName] = []byte(argValue)
	}

	var env dang.EvalEnv
	err = modCtx.Self().Mount(ctx, func(path string) error {
		modSrcDir := filepath.Join(path, r.modSource.Self().SourceSubpath)
		env, err = dang.RunDir(ctx, modSrcDir, false /* debug */)
		if err != nil {
			return fmt.Errorf("failed to run dir: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("run: %w", err)
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, "", fmt.Errorf("get current dagql server: %w", err)
	}

	// initializing module
	if parentName == "" {
		dagMod, err := r.initModule(ctx, srv, env)
		if err != nil {
			return nil, "", fmt.Errorf("failed to init module: %w", err)
		}
		jsonBytes, err := json.Marshal(dagMod)
		if err != nil {
			return nil, "", fmt.Errorf("failed to marshal module: %w", err)
		}
		return jsonBytes, clientID, nil
	}

	parentModBase, found := env.Get(parentName)
	if !found {
		return nil, "", fmt.Errorf("unknown parent type: %s", parentName)
	}
	var parentState map[string]any
	dec := json.NewDecoder(bytes.NewReader(parentJSON))
	dec.UseNumber()
	if err := dec.Decode(&parentState); err != nil {
		return nil, "", fmt.Errorf("failed to unmarshal parent JSON: %w", err)
	}

	parentConstructor := parentModBase.(*dang.ConstructorFunction)
	parentModType := parentConstructor.ClassType

	var fnType *hm.FunctionType

	if fnName == "" {
		fnType = parentConstructor.FnType
	} else {
		fnScheme, found := parentModType.SchemeOf(fnName)
		if !found {
			return nil, "", fmt.Errorf("unknown function: %s", fnName)
		}
		t, mono := fnScheme.Type()
		if !mono {
			return nil, "", fmt.Errorf("non-monotype function %s", fnName)
		}
		var ok bool
		fnType, ok = t.(*hm.FunctionType)
		if !ok {
			return nil, "", fmt.Errorf("expected function type, got %T", fnScheme)
		}
	}

	var args dang.Record
	argMap := make(map[string]dang.Value, len(args))
	for _, arg := range fnType.Arg().(*dang.RecordType).Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return nil, "", fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		jsonValue, provided := inputArgs[arg.Key]
		if !provided {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(jsonValue))
		dec.UseNumber()
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal input argument %s: %w", arg.Key, err)
		}
		dangVal, err := anyToDang(ctx, env, val, argType)
		if err != nil {
			return nil, "", fmt.Errorf("failed to convert input argument %s to dang value: %w", arg.Key, err)
		}
		argMap[arg.Key] = dangVal
		args = append(args, dang.Keyed[dang.Node]{
			Key:   arg.Key,
			Value: &dang.ValueNode{Val: dangVal},
		})
	}

	var result dang.Value
	if fnName == "" {
		result, err = parentConstructor.Call(ctx, env, argMap)
		if err != nil {
			return nil, "", fmt.Errorf("failed to call parent constructor: %w", err)
		}
	} else {
		parentModEnv := dang.NewModuleValue(parentModType)
		parentModEnv.Set("self", parentModEnv)

		for name, value := range parentState {
			scheme, found := parentModType.SchemeOf(name)
			if !found {
				return nil, "", fmt.Errorf("unknown field: %s", name)
			}
			fieldType, isMono := scheme.Type()
			if !isMono {
				return nil, "", fmt.Errorf("non-monotype argument %s", name)
			}
			dangVal, err := anyToDang(ctx, env, value, fieldType)
			if err != nil {
				return nil, "", fmt.Errorf("failed to convert parent state %s to dang value: %w", name, err)
			}
			parentModEnv.Set(name, dangVal)
		}

		bodyEnv := dang.CreateCompositeEnv(parentModEnv, env)
		_, err := dang.EvaluateFormsWithPhases(ctx, parentConstructor.ClassBodyForms, bodyEnv)
		if err != nil {
			return nil, "", fmt.Errorf("evaluating class body for %s: %w", parentConstructor.ClassName, err)
		}

		call := &dang.FunCall{
			Fun: &dang.Select{
				Receiver: &dang.ValueNode{Val: parentModEnv},
				Field:    &dang.Symbol{Name: fnName},
			},
			Args: args,
		}
		result, err = call.Eval(ctx, env)
		if err != nil {
			return nil, "", fmt.Errorf("failed to evaluate call: %w", err)
		}
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return jsonBytes, execMD.ClientID, nil
}

func (r *DangRuntime) initModule(ctx context.Context, srv *dagql.Server, env dang.EvalEnv) (res dagql.ObjectResult[*core.Module], _ error) {
	sels := []dagql.Selector{
		{
			Field: "module",
		},
	}

	// Handle module-level description if present
	if descBinding, found := env.Get("description"); found {
		sels = append(sels, dagql.Selector{
			Field: "withDescription",
			Args: []dagql.NamedInput{
				{
					Name:  "description",
					Value: dagql.String(descBinding.String()),
				},
			},
		})
	}

	binds := env.Bindings(dang.PublicVisibility)
	for _, binding := range binds {
		log.Println("Binding:", binding.Key)
		switch val := binding.Value.(type) {
		case *dang.ConstructorFunction:
			// Classes/objects - register as TypeDefs with their methods
			objDef, err := createObjectTypeDef(ctx, srv, binding.Key, val)
			if err != nil {
				return res, fmt.Errorf("failed to create object %s: %w", binding.Key, err)
			}
			directives := ProcessedDirectives{}
			for _, slot := range val.Parameters {
				slotName := slot.Name.Name
				for _, dir := range slot.Directives {
					if directives[slotName] == nil {
						directives[slotName] = map[string]map[string]any{}
					}
					for _, arg := range dir.Args {
						if directives[slotName][dir.Name] == nil {
							directives[slotName][dir.Name] = map[string]any{}
						}
						val, err := evalConstantValue(arg.Value)
						if err != nil {
							return res, fmt.Errorf("failed to evaluate directive argument %s.%s.%s: %w", slotName, dir.Name, arg.Key, err)
						}
						directives[slotName][dir.Name][arg.Key] = val
					}
				}
			}
			fnDef, err := createFunction(ctx, srv, binding.Key, val.FnType, directives)
			if err != nil {
				return res, fmt.Errorf("failed to create constructor for %s: %w", binding.Key, err)
			}

			// Apply withConstructor to the objDef
			var objDefWithCtor dagql.ObjectResult[*core.TypeDef]
			if err := srv.Select(ctx, objDef, &objDefWithCtor, dagql.Selector{
				Field: "withConstructor",
				Args:  []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](fnDef.ID())}},
			}); err != nil {
				return res, fmt.Errorf("failed to add constructor to object: %w", err)
			}

			sels = append(sels, dagql.Selector{
				Field: "withObject",
				Args:  []dagql.NamedInput{{Name: "object", Value: dagql.NewID[*core.TypeDef](objDefWithCtor.ID())}},
			})

		default:
			// Other values (functions, constants, etc.) - for now skip
			// In the Dagger SDK, everything needs to be structured as objects
			slog.Info("skipping non-class public binding", "name", binding.Key, "type", fmt.Sprintf("%T", val))
		}
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to select module: %w", err)
	}

	return res, nil
}

// arg => directive => directive args
type ProcessedDirectives = map[string]map[string]map[string]any

func createFunction(ctx context.Context, srv *dagql.Server, name string, fn *hm.FunctionType, directives ProcessedDirectives) (dagql.ObjectResult[*core.Function], error) {
	var res dagql.ObjectResult[*core.Function]

	// Convert Dang function type to Dagger TypeDef
	retTypeDef, err := dangTypeToTypeDef(ctx, srv, fn.Ret(false))
	if err != nil {
		return res, fmt.Errorf("failed to convert return type for %s: %w", fn, err)
	}

	// Start building function with name and return type
	sels := []dagql.Selector{
		{
			Field: "function",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(name)},
				{Name: "returnType", Value: dagql.NewID[*core.TypeDef](retTypeDef.ID())},
			},
		},
	}

	for _, arg := range fn.Arg().(*dang.RecordType).Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return res, fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		typeDef, err := dangTypeToTypeDef(ctx, srv, argType)
		if err != nil {
			return res, fmt.Errorf("failed to convert argument type for %s: %w", arg.Key, err)
		}

		// Check if arg is non-null, if not mark it as optional
		if _, isNonNull := argType.(hm.NonNullType); !isNonNull {
			// The typeDef should already be optional from dangTypeToTypeDef,
			// but we need to ensure it by chaining withOptional
			var optTypeDef dagql.ObjectResult[*core.TypeDef]
			if err := srv.Select(ctx, typeDef, &optTypeDef, dagql.Selector{
				Field: "withOptional",
				Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
			}); err != nil {
				return res, fmt.Errorf("failed to make argument optional: %w", err)
			}
			typeDef = optTypeDef
		}

		// Build withArg selector
		argArgs := []dagql.NamedInput{
			{Name: "name", Value: dagql.String(arg.Key)},
			{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](typeDef.ID())},
		}

		// Check for directives on this argument using processed directives
		if argDirectives, hasDirectives := directives[arg.Key]; hasDirectives {
			if defaultPath, hasDefaultPath := argDirectives["defaultPath"]; hasDefaultPath {
				if path, ok := defaultPath["path"].(string); ok {
					argArgs = append(argArgs, dagql.NamedInput{Name: "defaultPath", Value: dagql.String(path)})
				}
			}
			if ignorePatterns, hasIgnorePatterns := argDirectives["ignorePatterns"]; hasIgnorePatterns {
				ignore, hasIgnore := ignorePatterns["patterns"]
				if ignore, ok := ignore.([]any); ok {
					var ignorePatterns []string
					for _, pattern := range ignore {
						if str, ok := pattern.(string); ok {
							ignorePatterns = append(ignorePatterns, str)
						} else {
							return res, fmt.Errorf("invalid ignore argument %s: %T (expected string)", arg.Key, pattern)
						}
					}
					if len(ignorePatterns) > 0 {
						argArgs = append(argArgs, dagql.NamedInput{Name: "ignore", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(ignorePatterns...))})
					}
				} else if hasIgnore {
					return res, fmt.Errorf("invalid ignore directive for argument %s: %T (expected []any)", arg.Key, ignore)
				}
			}
		}

		sels = append(sels, dagql.Selector{
			Field: "withArg",
			Args:  argArgs,
		})
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to create function: %w", err)
	}

	return res, nil
}

// evalConstantValue converts AST nodes to Go values for directive arguments
func evalConstantValue(node dang.Node) (any, error) {
	switch n := node.(type) {
	case *dang.String:
		return n.Value, nil
	case *dang.Int:
		return n.Value, nil
	case *dang.Boolean:
		return n.Value, nil
	case *dang.List:
		var elements []any
		for _, elem := range n.Elements {
			if evalElem, err := evalConstantValue(elem); err == nil {
				elements = append(elements, evalElem)
			} else {
				return nil, fmt.Errorf("failed to evaluate list element: %w", err)
			}
		}
		return elements, nil
	default:
		// For more complex nodes, we could try full evaluation
		// but for now, directive arguments should be simple literals
		return nil, fmt.Errorf("unsupported directive argument type: %T", node)
	}
}

func createObjectTypeDef(ctx context.Context, srv *dagql.Server, name string, module *dang.ConstructorFunction) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	// Start with typeDef.withObject
	sels := []dagql.Selector{
		{Field: "typeDef"},
		{
			Field: "withObject",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
		},
	}

	// Process public methods in the class
	for name, scheme := range module.ClassType.Bindings(dang.PublicVisibility) {
		slotType, isMono := scheme.Type()
		if !isMono {
			return res, fmt.Errorf("non-monotype method %s", name)
		}
		switch x := slotType.(type) {
		case *hm.FunctionType:
			fn := x
			// TODO: figure out the directives locally
			fnDef, err := createFunction(ctx, srv, name, fn, nil)
			if err != nil {
				return res, fmt.Errorf("failed to create method %s for %s: %w", name, name, err)
			}

			// If there's a docstring, apply withDescription to the function
			if desc, ok := module.ClassType.GetDocString(name); ok {
				var descFnDef dagql.ObjectResult[*core.Function]
				if err := srv.Select(ctx, fnDef, &descFnDef, dagql.Selector{
					Field: "withDescription",
					Args:  []dagql.NamedInput{{Name: "description", Value: dagql.String(desc)}},
				}); err != nil {
					return res, fmt.Errorf("failed to add description to function: %w", err)
				}
				fnDef = descFnDef
			}

			sels = append(sels, dagql.Selector{
				Field: "withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](fnDef.ID())}},
			})
		default:
			fieldDef, err := dangTypeToTypeDef(ctx, srv, slotType)
			if err != nil {
				return res, fmt.Errorf("failed to create field %s: %w", name, err)
			}

			fieldArgs := []dagql.NamedInput{
				{Name: "name", Value: dagql.String(name)},
				{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](fieldDef.ID())},
			}

			if desc, ok := module.ClassType.GetDocString(name); ok {
				fieldArgs = append(fieldArgs, dagql.NamedInput{Name: "description", Value: dagql.String(desc)})
			}

			sels = append(sels, dagql.Selector{
				Field: "withField",
				Args:  fieldArgs,
			})
		}
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to create object typedef: %w", err)
	}

	return res, nil
}

func dangTypeToTypeDef(ctx context.Context, srv *dagql.Server, dangType hm.Type) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	// Start with a base typeDef selector
	sels := []dagql.Selector{{Field: "typeDef"}}

	if nonNull, isNonNull := dangType.(hm.NonNullType); isNonNull {
		// Handle non-null wrapper - recurse for the inner type, then add withOptional(false)
		inner, err := dangTypeToTypeDef(ctx, srv, nonNull.Type)
		if err != nil {
			return res, fmt.Errorf("failed to convert non-null type: %w", err)
		}
		// Chain withOptional onto the inner result
		if err := srv.Select(ctx, inner, &res, dagql.Selector{
			Field: "withOptional",
			Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(false)}},
		}); err != nil {
			return res, err
		}
		return res, nil
	}

	// Set as optional by default
	sels = append(sels, dagql.Selector{
		Field: "withOptional",
		Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
	})

	switch t := dangType.(type) {
	case dang.ListType:
		// First get the element type
		elemTypeDef, err := dangTypeToTypeDef(ctx, srv, t.Type)
		if err != nil {
			return res, fmt.Errorf("failed to convert list element type: %w", err)
		}
		sels = append(sels, dagql.Selector{
			Field: "withListOf",
			Args: []dagql.NamedInput{
				{Name: "elementType", Value: dagql.NewID[*core.TypeDef](elemTypeDef.ID())},
			},
		})

	case *dang.Module:
		// Check for basic types and object/class types
		switch t.Named {
		case "String":
			sels = append(sels, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindString}},
			})
		case "Int":
			sels = append(sels, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindInteger}},
			})
		case "Boolean":
			sels = append(sels, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindBoolean}},
			})
		case "Void":
			sels = append(sels, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindVoid}},
			})
		case "":
			// ad-hoc object type like {{foo: 1}}
			return res, fmt.Errorf("cannot directly expose ad-hoc object type: %s", t)
		default:
			// assume object (TODO?)
			sels = append(sels, dagql.Selector{
				Field: "withObject",
				Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(t.Named)}},
			})
		}

	default:
		// For type variables and other complex types, default to string for now
		// TODO: Handle type variables more gracefully
		slog.Info("unknown type, defaulting to string", "type", fmt.Sprintf("%T", dangType), "value", fmt.Sprintf("%s", dangType))
		return res, fmt.Errorf("unknown type: %T: %s", dangType, dangType)
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to select typedef: %w", err)
	}

	return res, nil
}

func convertError(rerr error) *core.Error {
	var gqlErr *gqlerror.Error
	if errors.As(rerr, &gqlErr) {
		dagErr := core.NewError(gqlErr.Message)
		if gqlErr.Extensions != nil {
			keys := make([]string, 0, len(gqlErr.Extensions))
			for k := range gqlErr.Extensions {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				val, err := json.Marshal(gqlErr.Extensions[k])
				if err != nil {
					fmt.Println("failed to marshal error value:", err)
				}
				dagErr = dagErr.WithValue(k, core.JSON(val))
			}
		}
		return dagErr
	}
	return core.NewError(rerr.Error())
}

func anyToDang(ctx context.Context, env dang.EvalEnv, val any, fieldType hm.Type) (dang.Value, error) {
	if nonNull, ok := fieldType.(hm.NonNullType); ok {
		return anyToDang(ctx, env, val, nonNull.Type)
	}
	switch v := val.(type) {
	case string:
		if modType, ok := fieldType.(*dang.Module); ok && modType != dang.StringType {
			sel := &dang.FunCall{
				Fun: &dang.Select{
					Field: &dang.Symbol{Name: fmt.Sprintf("load%sFromID", modType.Named)},
				},
				Args: dang.Record{
					dang.Keyed[dang.Node]{
						Key:   "id",
						Value: &dang.String{Value: v},
					},
				},
			}
			return sel.Eval(ctx, env)
		}
		return dang.StringValue{Val: v}, nil
	case int:
		return dang.IntValue{Val: v}, nil
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return nil, fmt.Errorf("failed to convert json.Number to int64: %w", err)
		}
		return dang.IntValue{Val: int(i)}, nil
	case bool:
		return dang.BoolValue{Val: v}, nil
	case []any:
		listT, isList := fieldType.(dang.ListType)
		if !isList {
			return nil, fmt.Errorf("expected list type, got %T", fieldType)
		}
		vals := dang.ListValue{
			ElemType: listT,
		}
		for _, item := range v {
			val, err := anyToDang(ctx, env, item, listT.Type)
			if err != nil {
				return nil, fmt.Errorf("failed to convert list item: %w", err)
			}
			vals.Elements = append(vals.Elements, val)
		}
		return vals, nil
	case map[string]any:
		mod, isMod := fieldType.(dang.Env)
		if !isMod {
			return nil, fmt.Errorf("expected module type, got %T", fieldType)
		}
		modVal := dang.NewModuleValue(mod)
		for name, val := range v {
			expectedT, found := mod.SchemeOf(name)
			if !found {
				return nil, fmt.Errorf("module %q does not have a scheme for %q", mod.Name(), name)
			}
			t, isMono := expectedT.Type()
			if !isMono {
				return nil, fmt.Errorf("expected monomorphic type, got %T", t)
			}
			dangVal, err := anyToDang(ctx, env, val, t)
			if err != nil {
				return nil, fmt.Errorf("failed to convert map item %q: %w", name, err)
			}
			mod.Add(name, hm.NewScheme(nil, dangVal.Type()))
			modVal.Set(name, dangVal)
		}
		return modVal, nil
	case nil:
		return dang.NullValue{}, nil
	default:
		return nil, fmt.Errorf("unsupported type %T", val)
	}
}
