package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	dangv2 "github.com/dagger/dagger/core/sdk/dang/v2"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Which interface the engine ended up driving a module through. The choice is
// not otherwise visible: a fat manifest carries both an entrypoint and a
// runtime, so only one of its two keys is honored.
//
// These spans are internal, so they stay out of normal output and show up when
// inspecting a trace or at -vvv.
const (
	// interfaceEntrypoint is ModuleEntrypoint, called directly.
	interfaceEntrypoint = "module entrypoint interface"
	// interfaceLegacyRuntime is a runtime named by runtime.source, with no
	// entrypoint in play.
	interfaceLegacyRuntime = "legacy runtime interface"
)

func recordInterface(ctx context.Context, iface, source string) {
	_, span := core.Tracer(ctx).Start(ctx,
		fmt.Sprintf("%s: %s", iface, source),
		telemetry.Internal(),
		trace.WithAttributes(
			attribute.String("dagger.io/module.interface", iface),
			attribute.String("dagger.io/module.interface.source", source),
		),
	)
	span.End()
}

type entrypointSDK struct{}

func (l *Loader) entrypointForModule(
	ctx context.Context,
	src *core.ModuleSource,
) (core.SDK, error) {
	if src.Entrypoint == nil {
		return nil, fmt.Errorf("module entrypoint is not configured")
	}
	switch src.Entrypoint.Kind {
	case modules.ModuleEntrypointKindDang:
		recordInterface(ctx, interfaceEntrypoint, src.Entrypoint.Source)
		return &entrypointSDK{}, nil
	default:
		return nil, fmt.Errorf("unsupported module entrypoint kind %q", src.Entrypoint.Kind)
	}
}

func (sdk *entrypointSDK) CloneForModuleSource(*core.ModuleSource) core.SDK {
	if sdk == nil {
		return nil
	}
	clone := *sdk
	return &clone
}

func (sdk *entrypointSDK) AsRuntime() (core.Runtime, bool) {
	return sdk, true
}

func (sdk *entrypointSDK) AsModuleTypes() (core.ModuleTypes, bool) {
	return sdk, true
}

func (sdk *entrypointSDK) AsCodeGenerator() (core.CodeGenerator, bool) {
	return nil, false
}

func (sdk *entrypointSDK) AsClientGenerator() (core.ClientGenerator, bool) {
	return nil, false
}

func (sdk *entrypointSDK) AsModuleInitializer() (core.ModuleInitializer, bool) {
	return nil, false
}

func (sdk *entrypointSDK) AsClientInitializer() (core.ClientInitializer, bool) {
	return nil, false
}

func (sdk *entrypointSDK) AsRuntimeTarget() (core.RuntimeTarget, bool) {
	return nil, false
}

func (sdk *entrypointSDK) AsModule() (dagql.ObjectResult[*core.Module], bool) {
	return dagql.ObjectResult[*core.Module]{}, false
}

func (sdk *entrypointSDK) AttachDependencyResults(
	context.Context,
	func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (sdk *entrypointSDK) AlwaysEnablesSelfCalls() bool {
	return true
}

func (sdk *entrypointSDK) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (core.ModuleRuntime, error) {
	return dangv2.NewEntrypointRuntime(deps, source), nil
}

func (sdk *entrypointSDK) ModuleTypes(
	ctx context.Context,
	deps *core.SchemaBuilder,
	src dagql.ObjectResult[*core.ModuleSource],
	partiallyInitializedMod *core.Module,
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("get Dagger server for module entrypoint: %w", err)
	}

	src, err = scopeSourceForSDKOperation(ctx, src, "entrypointTypes", dag)
	if err != nil {
		return inst, fmt.Errorf("scope module entrypoint source: %w", err)
	}
	scopedMod, err := ScopeModuleForSDKOperation(ctx, partiallyInitializedMod, "entrypointTypes", dag)
	if err != nil {
		return inst, fmt.Errorf("scope module for entrypoint types: %w", err)
	}

	return dangv2.EntrypointModuleTypes(ctx, deps, src, scopedMod)
}
