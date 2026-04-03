package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/worker"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/sourcegraph/conc/pool"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
	"github.com/vito/dang/pkg/ioctx"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func init() {
	buildkit.RegisterCustomOp(DangEvalOp{})
}

const dangEvalOutputFilename = "output.json"

// DangEvalOp is a buildkit custom op that evaluates a Dang module function
// call. All fields are JSON-serializable. On cache miss, Exec reconstructs
// the module infrastructure from the dagql server and runs the Dang
// interpreter. On cache hit, the cached result is returned directly.
type DangEvalOp struct {
	CacheDigest digest.Digest `json:"cacheDigest"`

	// IDs for reconstructing the module infrastructure in Exec.
	ModSourceID  *call.ID `json:"modSourceID"`
	SchemaFileID *call.ID `json:"schemaFileID"`

	// Module source subpath (where .dang files live).
	SourceSubpath string `json:"sourceSubpath"`

	// Execution metadata for the nested client.
	ExecMD *buildkit.ExecutionMetadata `json:"execMD"`

	// Function call data.
	ParentName string                       `json:"parentName"`
	FnName     string                       `json:"fnName"`
	ParentJSON json.RawMessage              `json:"parentJSON"`
	InputArgs  []*core.FunctionCallArgValue `json:"inputArgs"`
}

func (op DangEvalOp) Name() string {
	return "dagop.dang-eval"
}

func (op DangEvalOp) Backend() buildkit.CustomOpBackend {
	return &op
}

func (op DangEvalOp) Digest() (digest.Digest, error) {
	return op.CacheDigest, nil
}

func (op DangEvalOp) CacheMap(ctx context.Context, cm *solver.CacheMap) (*solver.CacheMap, error) {
	cm.Digest = op.CacheDigest
	for i, dep := range cm.Deps {
		dep.PreprocessFunc = nil
		dep.ComputeDigestFunc = nil
		cm.Deps[i] = dep
	}
	return cm, nil
}

func (op DangEvalOp) Exec(ctx context.Context, g bksession.Group, inputs []solver.Result, opt buildkit.OpOpts) ([]solver.Result, error) {
	query, ok := opt.Server.Root().Unwrap().(*core.Query)
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}
	ctx = core.ContextWithQuery(ctx, query)

	// Load module source from its ID.
	modSourceObj, err := opt.Server.LoadType(ctx, op.ModSourceID)
	if err != nil {
		return nil, fmt.Errorf("load module source: %w", err)
	}
	modSource := modSourceObj.Unwrap().(*core.ModuleSource)

	// Load schema introspection file from its ID.
	schemaObj, err := opt.Server.LoadType(ctx, op.SchemaFileID)
	if err != nil {
		return nil, fmt.Errorf("load schema file: %w", err)
	}
	schemaFile := schemaObj.Unwrap().(*core.File)

	// Use the cause span context for trace propagation to nested clients.
	// The buildkit vertex span's parent can be wrong under parallel
	// execution (MultiSpan first-wins race), but the cause context
	// (from WithTracePropagation) always has the correct parent.
	traceCtx := ctx
	if opt.CauseCtx.IsValid() {
		traceCtx = trace.ContextWithRemoteSpanContext(ctx, opt.CauseCtx)
	}

	// Run the Dang evaluation.
	output, err := op.eval(ctx, traceCtx, query, modSource, schemaFile)
	if err != nil {
		return nil, err
	}

	// Write output to a buildkit snapshot for persistent caching.
	return writeSnapshot(ctx, g, opt, op.Name(), dangEvalOutputFilename, output)
}

func (op DangEvalOp) eval(
	ctx context.Context,
	traceCtx context.Context,
	query *core.Query,
	modSource *core.ModuleSource,
	schemaFile *core.File,
) ([]byte, error) {
	execMD := op.ExecMD

	// Set up nested HTTP client.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	defer l.Close()

	http2Srv := &http2.Server{}
	httpSrv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: h2c.NewHandler(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			// Use traceCtx (derived from the cause span context) for
			// trace propagation. The buildkit vertex ctx can have a
			// misparented span under parallel execution.
			telemetry.Propagator.Inject(traceCtx, propagation.HeaderCarrier(req.Header))
			query.ServeHTTPToNestedClient(resp, req, execMD)
		}), http2Srv),
	}
	if err := http2.ConfigureServer(httpSrv, http2Srv); err != nil {
		return nil, fmt.Errorf("configure http2: %w", err)
	}

	srvCtx, srvCancel := context.WithCancelCause(ctx)
	defer srvCancel(errors.New("dang eval cleanup"))

	srvPool := pool.New().WithContext(srvCtx).WithCancelOnError()
	srvPool.Go(func(_ context.Context) error {
		err := httpSrv.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	})

	gqlClient := graphql.NewClient(fmt.Sprintf("http://%s/query", l.Addr()), nil)

	// Parse schema introspection.
	var intro introspection.Response
	f, err := schemaFile.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("open schema file: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&intro); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
		Name:       "Dagger",
		Client:     gqlClient,
		Schema:     intro.Schema,
		AutoImport: true,
	})

	// Attach stdio to the trace context so print() logs land under
	// the correct span regardless of buildkit vertex misparenting.
	stdio := telemetry.SpanStdio(traceCtx, core.InstrumentationLibrary)
	ctx = ioctx.StdoutToContext(ctx, stdio.Stdout)
	ctx = ioctx.StderrToContext(ctx, stdio.Stderr)

	// Load and run the Dang source.
	modCtx := modSource.ContextDirectory
	var env dang.EvalEnv
	err = modCtx.Self().Mount(ctx, func(path string) error {
		modSrcDir := filepath.Join(path, op.SourceSubpath)
		env, err = dang.RunDir(ctx, modSrcDir, false)
		if err != nil {
			return fmt.Errorf("run dir: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("mount source: %w", err)
	}

	// Module initialization: register typedefs.
	if op.ParentName == "" {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("get dagql server: %w", err)
		}
		dagMod, err := initModule(ctx, srv, env)
		if err != nil {
			return nil, fmt.Errorf("init module: %w", err)
		}
		return json.Marshal(dagMod)
	}

	// Dispatch the function/constructor call.
	result, err := op.callFunction(ctx, env)
	if err != nil {
		return nil, err
	}

	// Flush telemetry before returning so that spans/logs from GraphQL
	// requests made during the evaluation are fully written to the DB.
	// Without this, the BatchSpanProcessor's 100ms buffer can race
	// with captureLogs which walks the span tree immediately after the
	// tool call completes.
	if flushErr := query.Server.FlushSessionTelemetry(ctx); flushErr != nil {
		slog.Debug("failed to flush telemetry after Dang eval", "error", flushErr)
	}

	return json.Marshal(result)
}

// callFunction dispatches a constructor or method call against the Dang environment.
func (op DangEvalOp) callFunction(ctx context.Context, env dang.EvalEnv) (dang.Value, error) {
	inputArgs := make(map[string][]byte, len(op.InputArgs))
	for _, arg := range op.InputArgs {
		inputArgs[arg.Name] = []byte(arg.Value)
	}

	parentModBase, found := env.Get(op.ParentName)
	if !found {
		return nil, fmt.Errorf("unknown parent type: %s", op.ParentName)
	}

	var parentState map[string]any
	dec := json.NewDecoder(bytes.NewReader(op.ParentJSON))
	dec.UseNumber()
	if err := dec.Decode(&parentState); err != nil {
		return nil, fmt.Errorf("unmarshal parent: %w", err)
	}

	parentConstructor := parentModBase.(*dang.ConstructorFunction)
	parentModType := parentConstructor.ClassType

	var fnType *hm.FunctionType
	if op.FnName == "" {
		fnType = parentConstructor.FnType
	} else {
		fnScheme, found := parentModType.SchemeOf(op.FnName)
		if !found {
			return nil, fmt.Errorf("unknown function: %s", op.FnName)
		}
		t, mono := fnScheme.Type()
		if !mono {
			return nil, fmt.Errorf("non-monotype function %s", op.FnName)
		}
		var ok bool
		fnType, ok = t.(*hm.FunctionType)
		if !ok {
			return nil, fmt.Errorf("expected function type, got %T", fnScheme)
		}
	}

	var args dang.Record
	argMap := make(map[string]dang.Value, len(args))
	for _, arg := range fnType.Arg().(*dang.RecordType).Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return nil, fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		jsonValue, provided := inputArgs[arg.Key]
		if !provided {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(jsonValue))
		dec.UseNumber()
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, fmt.Errorf("unmarshal arg %s: %w", arg.Key, err)
		}
		dangVal, err := anyToDang(ctx, env, val, argType)
		if err != nil {
			return nil, fmt.Errorf("convert arg %s: %w", arg.Key, err)
		}
		argMap[arg.Key] = dangVal
		args = append(args, dang.Keyed[dang.Node]{
			Key:   arg.Key,
			Value: &dang.ValueNode{Val: dangVal},
		})
	}

	if op.FnName == "" {
		return parentConstructor.Call(ctx, env, argMap)
	}

	parentModEnv := dang.NewModuleValue(parentModType)
	parentModEnv.SetDynamicScope(parentModEnv)

	for name, value := range parentState {
		scheme, found := parentModType.SchemeOf(name)
		if !found {
			return nil, fmt.Errorf("unknown field: %s", name)
		}
		fieldType, isMono := scheme.Type()
		if !isMono {
			return nil, fmt.Errorf("non-monotype field %s", name)
		}
		dangVal, err := anyToDang(ctx, env, value, fieldType)
		if err != nil {
			return nil, fmt.Errorf("convert field %s: %w", name, err)
		}
		parentModEnv.Set(name, dangVal)
	}

	bodyEnv := dang.CreateCompositeEnv(parentModEnv, env)
	_, err := dang.EvaluateFormsWithPhases(ctx, parentConstructor.ClassBodyForms, bodyEnv)
	if err != nil {
		return nil, fmt.Errorf("evaluating class body for %s: %w", parentConstructor.ClassName, err)
	}

	call := &dang.FunCall{
		Fun: &dang.Select{
			Receiver: &dang.ValueNode{Val: parentModEnv},
			Field:    &dang.Symbol{Name: op.FnName},
		},
		Args: args,
	}
	return call.Eval(ctx, env)
}

func initModule(ctx context.Context, srv *dagql.Server, env dang.EvalEnv) (res dagql.ObjectResult[*core.Module], _ error) {
	sels := []dagql.Selector{
		{
			Field: "module",
		},
	}

	binds := env.Bindings(dang.PublicVisibility)
	for _, binding := range binds {
		switch val := binding.Value.(type) {
		case *dang.ConstructorFunction:
			objDef, err := createObjectTypeDef(ctx, srv, binding.Key, val, env)
			if err != nil {
				return res, fmt.Errorf("failed to create object %s: %w", binding.Key, err)
			}
			fnDef, err := createFunction(ctx, srv, val.ClassType, binding.Key, val.FnType, env)
			if err != nil {
				return res, fmt.Errorf("failed to create constructor for %s: %w", binding.Key, err)
			}

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

		case *dang.ModuleValue:
			mod, ok := val.Mod.(*dang.Module)
			if !ok {
				slog.Warn("skipping non-module module value", "name", binding.Key)
				break
			}
			switch mod.Kind {
			case dang.EnumKind:
				enumDef, err := createEnumTypeDef(ctx, srv, binding.Key, val)
				if err != nil {
					return res, fmt.Errorf("failed to create enum %s: %w", binding.Key, err)
				}
				sels = append(sels, dagql.Selector{
					Field: "withEnum",
					Args:  []dagql.NamedInput{{Name: "enum", Value: dagql.NewID[*core.TypeDef](enumDef.ID())}},
				})
			case dang.ScalarKind:
				// Scalars are registered with the module, but we don't need to create TypeDefs for them
				// They're already handled as basic string types in dangTypeToTypeDef
				slog.Info("skipping scalar module value (handled as string type)", "name", binding.Key)
			case dang.InterfaceKind:
				interfaceDef, err := createInterfaceTypeDef(ctx, srv, binding.Key, val, env)
				if err != nil {
					return res, fmt.Errorf("failed to create interface %s: %w", binding.Key, err)
				}
				sels = append(sels, dagql.Selector{
					Field: "withInterface",
					Args:  []dagql.NamedInput{{Name: "iface", Value: dagql.NewID[*core.TypeDef](interfaceDef.ID())}},
				})
			default:
				// NB: plain object types are represented by ConstructorFunction
				slog.Warn("unknown module kind", "name", binding.Key, "kind", mod.Kind)
			}

		default:
			slog.Info("skipping non-class public binding", "name", binding.Key, "type", fmt.Sprintf("%T", val))
		}
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to select module: %w", err)
	}

	return res, nil
}

func createFunction(ctx context.Context, srv *dagql.Server, mod *dang.Module, name string, fn *hm.FunctionType, env dang.EvalEnv) (dagql.ObjectResult[*core.Function], error) {
	var res dagql.ObjectResult[*core.Function]

	retTypeDef, err := dangTypeToTypeDef(ctx, srv, fn.Ret(false), env)
	if err != nil {
		return res, fmt.Errorf("failed to convert return type for %s: %w", fn, err)
	}

	sels := []dagql.Selector{
		{
			Field: "function",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(name)},
				{Name: "returnType", Value: dagql.NewID[*core.TypeDef](retTypeDef.ID())},
			},
		},
	}

	if desc, ok := mod.GetDocString(name); ok {
		sels = append(sels, dagql.Selector{
			Field: "withDescription",
			Args:  []dagql.NamedInput{{Name: "description", Value: dagql.String(desc)}},
		})
	}

	// Apply @check and @generate directives on the function.
	for _, directive := range mod.GetDirectives(name) {
		switch directive.Name {
		case "check":
			sels = append(sels, dagql.Selector{
				Field: "withCheck",
			})
		case "generate":
			sels = append(sels, dagql.Selector{
				Field: "withGenerator",
			})
		}
	}

	args := fn.Arg().(*dang.RecordType)
	for _, arg := range args.Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return res, fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		typeDef, err := dangTypeToTypeDef(ctx, srv, argType, env)
		if err != nil {
			return res, fmt.Errorf("failed to convert argument type for %s: %w", arg.Key, err)
		}

		if _, isNonNull := argType.(hm.NonNullType); !isNonNull {
			var optTypeDef dagql.ObjectResult[*core.TypeDef]
			if err := srv.Select(ctx, typeDef, &optTypeDef, dagql.Selector{
				Field: "withOptional",
				Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
			}); err != nil {
				return res, fmt.Errorf("failed to make argument optional: %w", err)
			}
			typeDef = optTypeDef
		}

		argArgs := []dagql.NamedInput{
			{Name: "name", Value: dagql.String(arg.Key)},
			{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](typeDef.ID())},
		}

		if doc := args.DocStrings[arg.Key]; doc != "" {
			argArgs = append(argArgs, dagql.NamedInput{Name: "description", Value: dagql.String(doc)})
		}

		for _, argDirs := range args.Directives {
			if argDirs.Key != arg.Key {
				continue
			}
			for _, dir := range argDirs.Value {
				switch dir.Name {
				case "defaultPath":
					for _, arg := range dir.Args {
						if arg.Key == "path" { // TODO: positional
							val, err := evalConstantValue(arg.Value)
							if err != nil {
								return res, fmt.Errorf("failed to evaluate directive argument %s.%s.%s: %w", arg.Key, dir.Name, arg.Key, err)
							}
							if path, ok := val.(string); ok {
								argArgs = append(argArgs, dagql.NamedInput{Name: "defaultPath", Value: dagql.String(path)})
							}
						}
					}
				case "ignorePatterns":
					for _, arg := range dir.Args {
						if arg.Key == "patterns" {
							val, err := evalConstantValue(arg.Value)
							if err != nil {
								return res, fmt.Errorf("failed to evaluate directive argument %s.%s.%s: %w", arg.Key, dir.Name, arg.Key, err)
							}
							if ignore, ok := val.([]any); ok {
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
							} else {
								return res, fmt.Errorf("invalid ignore directive for argument %s: %T (expected []any)", arg.Key, ignore)
							}
						}
					}
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

func createObjectTypeDef(ctx context.Context, srv *dagql.Server, name string, module *dang.ConstructorFunction, env dang.EvalEnv) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	classMod := module.ClassType

	withObjectArgs := []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}}
	if desc := classMod.GetModuleDocString(); desc != "" {
		withObjectArgs = append(withObjectArgs, dagql.NamedInput{Name: "description", Value: dagql.String(desc)})
	}

	sels := []dagql.Selector{
		{Field: "typeDef"},
		{
			Field: "withObject",
			Args:  withObjectArgs,
		},
	}

	for name, scheme := range classMod.Bindings(dang.PublicVisibility) {
		slotType, isMono := scheme.Type()
		if !isMono {
			return res, fmt.Errorf("non-monotype method %s", name)
		}
		switch x := slotType.(type) {
		case *hm.FunctionType:
			fn := x
			fnDef, err := createFunction(ctx, srv, classMod, name, fn, env)
			if err != nil {
				return res, fmt.Errorf("failed to create method %s for %s: %w", name, name, err)
			}

			sels = append(sels, dagql.Selector{
				Field: "withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](fnDef.ID())}},
			})
		default:
			fieldDef, err := dangTypeToTypeDef(ctx, srv, slotType, env)
			if err != nil {
				return res, fmt.Errorf("failed to create field %s: %w", name, err)
			}

			fieldArgs := []dagql.NamedInput{
				{Name: "name", Value: dagql.String(name)},
				{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](fieldDef.ID())},
			}

			if desc, ok := classMod.GetDocString(name); ok {
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

func dangTypeToTypeDef(ctx context.Context, srv *dagql.Server, dangType hm.Type, env dang.EvalEnv) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	sels := []dagql.Selector{{Field: "typeDef"}}

	if nonNull, isNonNull := dangType.(hm.NonNullType); isNonNull {
		inner, err := dangTypeToTypeDef(ctx, srv, nonNull.Type, env)
		if err != nil {
			return res, fmt.Errorf("failed to convert non-null type: %w", err)
		}
		if err := srv.Select(ctx, inner, &res, dagql.Selector{
			Field: "withOptional",
			Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(false)}},
		}); err != nil {
			return res, err
		}
		return res, nil
	} else {
		sels = append(sels, dagql.Selector{
			Field: "withOptional",
			Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
		})
	}

	switch t := dangType.(type) {
	case dang.ListType:
		elemTypeDef, err := dangTypeToTypeDef(ctx, srv, t.Type, env)
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
			return res, fmt.Errorf("cannot directly expose ad-hoc object type: %s", t)
		default:
			// Default: assume object type.
			sel := dagql.Selector{
				Field: "withObject",
				Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(t.Named)}},
			}
			// Check if this is an enum, scalar, or interface by looking up in the environment.
			if val, found := env.Get(t.Named); found {
				if modVal, ok := val.(*dang.ModuleValue); ok {
					if mod, ok := modVal.Mod.(*dang.Module); ok {
						switch mod.Kind {
						case dang.EnumKind:
							sel = dagql.Selector{
								Field: "withEnum",
								Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(t.Named)}},
							}
						case dang.ScalarKind:
							// Scalars are exposed as strings in the Dagger SDK.
							sel = dagql.Selector{
								Field: "withKind",
								Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindString}},
							}
						case dang.InterfaceKind:
							sel = dagql.Selector{
								Field: "withInterface",
								Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(t.Named)}},
							}
						}
					}
				}
			}
			sels = append(sels, sel)
		}

	default:
		slog.Info("unknown type, defaulting to string", "type", fmt.Sprintf("%T", dangType), "value", dangType.String())
		return res, fmt.Errorf("unknown type: %T: %s", dangType, dangType)
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to select typedef: %w", err)
	}

	return res, nil
}

// createEnumTypeDef creates a Dagger enum TypeDef from a Dang enum ModuleValue.
func createEnumTypeDef(ctx context.Context, srv *dagql.Server, name string, enumMod *dang.ModuleValue) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	sels := []dagql.Selector{
		{Field: "typeDef"},
		{
			Field: "withEnum",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
		},
	}

	// Only include actual enum values, not accessors like values().
	for memberName, val := range enumMod.Values {
		if _, ok := val.(dang.EnumValue); !ok {
			continue
		}
		sels = append(sels, dagql.Selector{
			Field: "withEnumMember",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(memberName)}},
		})
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to create enum typedef: %w", err)
	}

	return res, nil
}

// createInterfaceTypeDef creates a Dagger interface TypeDef from a Dang interface ModuleValue.
func createInterfaceTypeDef(ctx context.Context, srv *dagql.Server, name string, interfaceMod *dang.ModuleValue, env dang.EvalEnv) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	mod, ok := interfaceMod.Mod.(*dang.Module)
	if !ok {
		return res, fmt.Errorf("expected *dang.Module for interface %s, got %T", name, interfaceMod.Mod)
	}

	sels := []dagql.Selector{
		{Field: "typeDef"},
		{
			Field: "withInterface",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
		},
	}

	for fieldName, scheme := range mod.Bindings(dang.PublicVisibility) {
		fieldType, isMono := scheme.Type()
		if !isMono {
			return res, fmt.Errorf("non-monotype field %s in interface %s", fieldName, name)
		}
		switch x := fieldType.(type) {
		case *hm.FunctionType:
			fnDef, err := createFunction(ctx, srv, mod, fieldName, x, env)
			if err != nil {
				return res, fmt.Errorf("failed to create method %s for interface %s: %w", fieldName, name, err)
			}
			sels = append(sels, dagql.Selector{
				Field: "withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](fnDef.ID())}},
			})
		default:
			fieldTypeDef, err := dangTypeToTypeDef(ctx, srv, fieldType, env)
			if err != nil {
				return res, fmt.Errorf("failed to create field %s for interface %s: %w", fieldName, name, err)
			}

			fieldArgs := []dagql.NamedInput{
				{Name: "name", Value: dagql.String(fieldName)},
				{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](fieldTypeDef.ID())},
			}
			if desc, ok := mod.GetDocString(fieldName); ok {
				fieldArgs = append(fieldArgs, dagql.NamedInput{Name: "description", Value: dagql.String(desc)})
			}

			sels = append(sels, dagql.Selector{
				Field: "withField",
				Args:  fieldArgs,
			})
		}
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to create interface typedef: %w", err)
	}

	return res, nil
}

// writeSnapshot writes data to a buildkit snapshot file and returns it as a solver result.
func writeSnapshot(
	ctx context.Context,
	g bksession.Group,
	opt buildkit.OpOpts,
	opName string,
	filename string,
	data []byte,
) ([]solver.Result, error) {
	query, ok := opt.Server.Root().Unwrap().(*core.Query)
	if !ok {
		return nil, fmt.Errorf("server root was %T", opt.Server.Root())
	}

	ref, err := query.BuildkitCache().New(ctx, nil, g,
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(client.UsageRecordTypeRegular),
		bkcache.WithDescription(opName))
	if err != nil {
		return nil, fmt.Errorf("create cache ref: %w", err)
	}
	defer func() {
		if ref != nil {
			ref.Release(context.WithoutCancel(ctx))
		}
	}()

	mount, err := ref.Mount(ctx, false, g)
	if err != nil {
		return nil, fmt.Errorf("mount: %w", err)
	}
	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	if err != nil {
		return nil, fmt.Errorf("local mount: %w", err)
	}
	defer func() {
		if lm != nil {
			lm.Unmount()
		}
	}()

	if err := os.WriteFile(filepath.Join(dir, filename), data, 0o644); err != nil {
		return nil, fmt.Errorf("write output: %w", err)
	}

	lm.Unmount()
	lm = nil

	snap, err := ref.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit snapshot: %w", err)
	}
	ref = nil

	return []solver.Result{worker.NewWorkerRefResult(snap, opt.Worker)}, nil
}

// solveDangEval creates a DangEvalOp, solves it through buildkit, and returns
// the result. On cache hit the Dang evaluation is skipped entirely.
func solveDangEval(
	ctx context.Context,
	callID *call.ID,
	cacheMixin digest.Digest,
	modSource dagql.ObjectResult[*core.ModuleSource],
	schemaFile dagql.Result[*core.File],
	execMD *buildkit.ExecutionMetadata,
	fnCall *core.FunctionCall,
) ([]byte, error) {
	cacheDigest := digest.FromString(strings.Join([]string{
		engine.BaseVersion(engine.Version),
		callID.Digest().String(),
		cacheMixin.String(),
	}, "\x00"))

	op := &DangEvalOp{
		CacheDigest:   cacheDigest,
		ModSourceID:   modSource.ID(),
		SchemaFileID:  schemaFile.ID(),
		SourceSubpath: modSource.Self().SourceSubpath,
		ExecMD:        execMD,
		ParentName:    fnCall.ParentName,
		FnName:        fnCall.Name,
		ParentJSON:    json.RawMessage(fnCall.Parent),
		InputArgs:     fnCall.InputArgs,
	}

	st, err := buildkit.NewCustomLLB(ctx, callID, op, nil,
		llb.WithCustomNamef("%s %s", op.Name(), callID.Name()),
		buildkit.WithTracePropagation(ctx),
		buildkit.WithPassthrough(),
		llb.SkipEdgeMerge,
	)
	if err != nil {
		return nil, fmt.Errorf("create dang eval LLB: %w", err)
	}

	f, err := core.NewFileSt(ctx, st, dangEvalOutputFilename, core.Platform{}, nil)
	if err != nil {
		return nil, fmt.Errorf("create output file: %w", err)
	}

	output, err := f.Contents(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("read output: %w", err)
	}

	return output, nil
}

func anyToDang(ctx context.Context, env dang.EvalEnv, val any, fieldType hm.Type) (dang.Value, error) {
	if nonNull, ok := fieldType.(hm.NonNullType); ok {
		return anyToDang(ctx, env, val, nonNull.Type)
	}
	switch v := val.(type) {
	case string:
		if modType, ok := fieldType.(*dang.Module); ok && modType != dang.StringType {
			// Check if this is an enum type.
			if modType.Kind == dang.EnumKind {
				if enumVal, found := env.Get(modType.Named); found {
					if enumMod, ok := enumVal.(*dang.ModuleValue); ok {
						if val, found := enumMod.Get(v); found {
							return val, nil
						}
						return nil, fmt.Errorf("unknown enum value %s.%s", modType.Named, v)
					}
				}
				return nil, fmt.Errorf("enum type %s not found in environment", modType.Named)
			}

			// Check if this is a scalar type.
			if modType.Kind == dang.ScalarKind {
				return dang.ScalarValue{Val: v, ScalarType: modType}, nil
			}

			// Otherwise, assume it's an object ID.
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
		// When reconstructing from serialized state, we directly set fields
		// rather than calling the constructor. This is necessary because:
		// 1. Constructor arg names may differ from field names (explicit new())
		// 2. The constructor may have side effects we don't want to re-run
		// 3. The serialized state represents the object's fields, not constructor args
		modVal := dang.NewModuleValue(mod)
		modVal.SetDynamicScope(modVal)
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
			modVal.Set(name, dangVal)
		}
		// For named types, evaluate the class body to set up computed properties and methods
		if mod.Name() != "" {
			constructor, ok := env.Get(mod.Name())
			if ok {
				if constructorFn, ok := constructor.(*dang.ConstructorFunction); ok {
					bodyEnv := dang.CreateCompositeEnv(modVal, env)
					_, err := dang.EvaluateFormsWithPhases(ctx, constructorFn.ClassBodyForms, bodyEnv)
					if err != nil {
						return nil, fmt.Errorf("evaluating class body for %s: %w", mod.Name(), err)
					}
				}
			}
		}
		return modVal, nil
	case nil:
		return dang.NullValue{}, nil
	default:
		return nil, fmt.Errorf("unsupported type %T", val)
	}
}
