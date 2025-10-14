package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type CallHook interface {
	Call(ctx context.Context, fn *ModuleFunction, opts *CallOpts) (t dagql.AnyResult, ok bool, rerr error)
}

var callHooks []CallHook

func init() {
	callHooks = []CallHook{
		&CloudCallHook{},
	}
}

type CloudCallHook struct{}

func (h *CloudCallHook) Call(ctx context.Context, fn *ModuleFunction, opts *CallOpts) (t dagql.AnyResult, ok bool, rerr error) {
	objName := opts.ParentModType.typeDef.Name
	fieldName := fn.metadata.Name

	log := slog.Default().With(
		"object", objName,
		"function", fieldName,
	)

	ok, err := checkValidMod(ctx, fn.mod)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		log.Debug("skipping call with cloud hook due to invalid module source")
		return nil, false, nil
	}

	for _, input := range opts.Inputs {
		ok, err := checkValidInputValue(ctx, fn.args[input.Name].modType, input.Value)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			log.Debug("skipping call with cloud hook due to invalid input", "input", input.Name)
			return nil, false, nil
		}
	}

	if !checkValidReturn(fn.returnType.TypeDef()) {
		log.Debug("skipping call with cloud hook due to invalid return type", "returnType", fn.returnType.TypeDef())
		return nil, false, nil
	}

	ok, err = checkValidInput(ctx, opts.ParentModType, opts.ParentTyped)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		log.Debug("skipping call with cloud hook due to invalid parent")
		return nil, false, nil
	}

	// all valid, handle the call
	log.Info("handling call with cloud hook")

	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("current query: %w", err)
	}
	grpcCaller, err := q.NonModuleParentClientSessionCaller(ctx) // TODO: rewrite to just SessionCaller, or just use the bk client, etc.
	if err != nil {
		return nil, false, fmt.Errorf("get session caller: %w", err)
	}
	callerClientMD, err := q.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("main client metadata: %w", err)
	}
	callerCtx := engine.ContextWithClientMetadata(ctx, callerClientMD)
	spanExporter, err := q.CurrentSpanExporter(callerCtx)
	if err != nil {
		return nil, false, fmt.Errorf("current span exporter: %w", err)
	}
	logExporter, err := q.CurrentLogExporter(callerCtx)
	if err != nil {
		return nil, false, fmt.Errorf("current log exporter: %w", err)
	}
	metricExporter, err := q.CurrentMetricsExporter(callerCtx)
	if err != nil {
		return nil, false, fmt.Errorf("current metric exporter: %w", err)
	}

	c, _, err := client.ConnectE2E(ctx, client.Params{
		RunnerHost: "dagger-cloud://default-engine-config.dagger.cloud",
		// RunnerHost: "unix:///var/run/dagger/engine.sock",

		Module:   fn.mod.Source.Value.Self().AsString(),
		Function: fieldName,
		// ExecCmd:  []string{"TODO2"},

		CloudToken:      callerClientMD.CloudToken,
		CloudBasicToken: callerClientMD.CloudBasicToken,
		CloudOrgID:      callerClientMD.CloudOrg,

		EngineTrace:   spanExporter,
		EngineLogs:    logExporter,
		EngineMetrics: []sdkmetric.Exporter{metricExporter},

		ExistingSessionConn: grpcCaller.Conn(),
	})
	if err != nil {
		return nil, false, fmt.Errorf("e2e connect: %w", err)
	}
	defer func() {
		err := c.Close()
		if err != nil && rerr == nil {
			rerr = fmt.Errorf("close client: %w", err)
		}
	}()

	fields, err := json.Marshal(opts.ParentFields)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal parent fields: %w", err)
	}

	inputs, err := json.Marshal(opts.Inputs)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal inputs: %w", err)
	}

	query := c.Dagger().QueryBuilder()
	if fn.mod.Source.Value.Self().Kind == ModuleSourceKindGit {
		query = query.Select("moduleSource").
			Arg("refString", fn.mod.Source.Value.Self().Git.Symbolic). // XXX: ain't right
			Arg("refPin", fn.mod.Source.Value.Self().Git.Commit).
			Arg("requireKind", fn.mod.Source.Value.Self().Kind)
	} else {
		query = query.Select("moduleSource").
			Arg("refString", filepath.Join(
				fn.mod.Source.Value.Self().Local.ContextDirectoryPath,
				fn.mod.Source.Value.Self().SourceRootSubpath,
			))
	}
	query = query.Select("asModule")
	query = query.Select("call").
		Arg("object", objName).
		Arg("field", fieldName).
		Arg("parent", string(fields)).
		Arg("inputs", string(inputs))

	// CALL!

	var bind string
	err = query.Bind(&bind).Execute(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to make query: %w", err)
	}

	var result any
	dec := json.NewDecoder(strings.NewReader(bind))
	dec.UseNumber()
	err = dec.Decode(&result)
	if err != nil {
		return nil, false, fmt.Errorf("failed to decode result %q: %w", bind, err)
	}

	input, err := fn.returnType.TypeDef().ToInput().Decoder().DecodeInput(result)
	if err != nil {
		return nil, false, fmt.Errorf("failed to decode result %q: %w", result, err)
	}

	t, err = dagql.NewResultForCurrentID(ctx, input)
	return t, true, err
}

func checkValidReturn(typeDef *TypeDef) bool {
	// TODO: allow returning your own module objects
	switch typeDef.Kind {
	case TypeDefKindVoid:
		return true
	case TypeDefKindBoolean, TypeDefKindFloat, TypeDefKindInteger, TypeDefKindString:
		return true
	case TypeDefKindScalar, TypeDefKindEnum:
		return true
	// NOTE: we don't seem to support this is args right now, but theoretically
	// these are seralizable
	// case TypeDefKindInput:
	// 	return true
	case TypeDefKindList:
		return checkValidReturn(typeDef.Underlying())
	}
	return false
}

func checkValidInputValue(ctx context.Context, parent ModType, value dagql.Typed) (bool, error) {
	// HACK: find all core ids in the input by converting to/from json via sdk
	// this is kinda gross, but it works, because while the IDs returned via
	// ConvertFromSDKResult are *wrong* for our own module IDs, they're correct
	// for the Core IDs - which are the ones we care about

	v, err := parent.ConvertToSDKInput(ctx, value)
	if err != nil {
		return false, err
	}

	dt, err := json.Marshal(v)
	if err != nil {
		return false, fmt.Errorf("marshal input to json: %w", err)
	}
	var v2 any
	if err := json.Unmarshal(dt, &v2); err != nil {
		return false, fmt.Errorf("unmarshal input from json: %w", err)
	}

	result, err := parent.ConvertFromSDKResult(ctx, v2)
	if err != nil {
		return false, err
	}

	return checkValidInput(ctx, parent, result)
}

func checkValidInput(ctx context.Context, parent ModType, value dagql.AnyResult) (bool, error) {
	returnedIDs := map[digest.Digest]*resource.ID{}
	if err := parent.CollectCoreIDs(ctx, value, returnedIDs); err != nil {
		return false, fmt.Errorf("collect IDs: %w", err)
	}

	for _, id := range returnedIDs {
		if !id.Call().IsRemoteable {
			slog.Debug("skipping call with cloud hook due to non-remoteable ID", "id", id.Display())
			return false, nil
		}
	}
	return true, nil
}

func checkValidMod(ctx context.Context, module *Module) (bool, error) {
	source := module.Source.Value.Self()
	if source.Kind == ModuleSourceKindGit || source.Kind == ModuleSourceKindLocal {
		return true, nil
	}

	return false, nil
}
