package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// Run the Dang evaluation.
	output, err := op.eval(ctx, query, modSource, schemaFile)
	if err != nil {
		return nil, err
	}

	// Write output to a buildkit snapshot for persistent caching.
	return writeSnapshot(ctx, g, opt, op.Name(), dangEvalOutputFilename, output)
}

func (op DangEvalOp) eval(
	ctx context.Context,
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
			telemetry.Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
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

	stdio := telemetry.SpanStdio(ctx, core.InstrumentationLibrary)
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

	// Build input args map.
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

	var result dang.Value
	if op.FnName == "" {
		result, err = parentConstructor.Call(ctx, env, argMap)
		if err != nil {
			return nil, fmt.Errorf("call constructor: %w", err)
		}
	} else {
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
		result, err = call.Eval(ctx, env)
		if err != nil {
			return nil, fmt.Errorf("eval call: %w", err)
		}
	}

	return json.Marshal(result)
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
