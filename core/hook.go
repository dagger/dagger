package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
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

	if !strings.Contains(strings.ToLower(fieldName), "scale") {
		slog.Debug(
			"skipping call with cloud hook due to field name",
			"object", objName,
			"function", fieldName,
		)
		return nil, false, nil
	}

	ok, err := checkValidMod(ctx, fn.mod)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		slog.Debug(
			"skipping call with cloud hook due to invalid module source",
			"object", objName,
			"function", fieldName,
		)
		return nil, false, nil
	}

	for _, input := range opts.Inputs {
		if !checkValidInput(fn.args[input.Name].modType.TypeDef(), input) {
			slog.Debug(
				"skipping call with cloud hook due to invalid input",
				"object", objName,
				"function", fieldName,
				"input", input.Name,
			)
			return nil, false, nil
		}
	}

	if !checkValidReturn(fn.returnType.TypeDef()) {
		slog.Debug(
			"skipping call with cloud hook due to invalid return type",
			"object", objName,
			"function", fieldName,
			"returnType", fn.returnType.TypeDef(),
		)
		return nil, false, nil
	}

	ok, err = checkValidParent(ctx, opts.ParentModType, opts.ParentTyped, opts.ParentFields)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		slog.Debug(
			"skipping call with cloud hook due to invalid parent",
			"object", objName,
			"function", fieldName,
		)
		return nil, false, nil
	}

	// all valid, handle the call
	slog.Info(
		"handling call with cloud hook",
		"object", objName,
		"function", fieldName,
	)

	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("current query: %w", err)
	}
	spanExporter, err := q.CurrentSpanExporter(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("current span exporter: %w", err)
	}
	logExporter, err := q.CurrentLogExporter(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("current log exporter: %w", err)
	}
	metricExporter, err := q.CurrentMetricsExporter(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("current metric exporter: %w", err)
	}
	grpcCaller, err := q.NonModuleParentClientSessionCaller(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get session caller: %w", err)
	}
	callerClientMD, err := q.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("main client metadata: %w", err)
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
			Arg("refPin", fn.mod.Source.Value.Self().Git.Commit)
		// XXX: something is wrong with enum passing.
		// Arg("requireKind", fn.mod.Source.Value.Self().Kind)
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

	var out any
	err = query.Bind(&out).Execute(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to make query: %w", err)
	}

	input, err := fn.returnType.TypeDef().ToInput().Decoder().DecodeInput(out)
	if err != nil {
		return nil, false, fmt.Errorf("failed to decode result: %w", err)
	}

	t, err = dagql.NewResultForCurrentID(ctx, input)
	return t, true, err
}

func checkValidInput(typeDef *TypeDef, input CallInput) bool {
	value := input.Value
	if _, ok := value.(dagql.ScalarType); ok {
		return true
	}
	return false
}

func checkValidReturn(typeDef *TypeDef) bool {
	switch typeDef.Kind {
	case TypeDefKindVoid:
		return true
	case TypeDefKindBoolean, TypeDefKindFloat, TypeDefKindInteger, TypeDefKindString, TypeDefKindScalar:
		return true
	case TypeDefKindList:
		return checkValidReturn(typeDef.Underlying())
	}
	return false
}

func checkValidParent(ctx context.Context, parent *ModuleObjectType, value dagql.AnyResult, fields map[string]any) (bool, error) {
	returnedIDs := map[digest.Digest]*resource.ID{}
	if err := parent.CollectCoreIDs(ctx, value, returnedIDs); err != nil {
		return false, fmt.Errorf("collect IDs: %w", err)
	}

	if len(returnedIDs) == 0 {
		// no IDs! always transferrable :)
		return true, nil
	}

	return false, nil
}

func checkValidMod(ctx context.Context, module *Module) (bool, error) {
	source := module.Source.Value.Self()
	if source.Kind == ModuleSourceKindGit || source.Kind == ModuleSourceKindLocal {
		return true, nil
	}

	return false, nil
}
