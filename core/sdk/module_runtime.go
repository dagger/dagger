package sdk

import (
	"context"
	"fmt"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

// A SDK module that implements the `Runtime` interface
type runtimeModule struct {
	mod *module
}

func (sdk *runtimeModule) Runtime(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.ObjectResult[*core.ModuleSource],
) (inst dagql.ObjectResult[*core.Container], rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load runtime")
	defer telemetry.EndWithCause(span, &rerr)

	targetSrc := source.Self()
	targetSDKSource := ""
	if targetSrc.SDK != nil {
		targetSDKSource = targetSrc.SDK.Source
	}

	sdkMod := sdk.mod.mod.Self()
	sdkModSource := ""
	sdkModPin := ""
	if sdkMod.Source.Valid {
		sdkModSource = sdkMod.Source.Value.Self().AsString()
		sdkModPin = sdkMod.Source.Value.Self().Pin()
	}

	toolchainDebug(
		ctx,
		"toolchain-debug sdk moduleRuntime start",
		"sdk_module", sdkMod.Name(),
		"sdk_source", sdkModSource,
		"sdk_pin", sdkModPin,
		"target_module", targetSrc.ModuleOriginalName,
		"target_source", targetSrc.AsString(),
		"target_pin", targetSrc.Pin(),
		"target_sdk_source", targetSDKSource,
	)

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	dag, err := sdk.mod.dag(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for sdk module %s: %w", sdk.mod.mod.Self().Name(), err)
	}

	err = dag.Select(ctx, sdk.mod.sdk, &inst,
		dagql.Selector{
			Field: "moduleRuntime",
			Args: []dagql.NamedInput{
				{
					Name:  "modSource",
					Value: dagql.NewID[*core.ModuleSource](source.ID()),
				},
				{
					Name:  "introspectionJson",
					Value: dagql.NewID[*core.File](schemaJSONFile.ID()),
				},
			},
		},
		dagql.Selector{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(RuntimeWorkdirPath),
				},
			},
		},
	)
	if err != nil {
		slog.WarnContext(
			ctx,
			"toolchain-debug sdk moduleRuntime failed",
			"sdk_module", sdkMod.Name(),
			"sdk_source", sdkModSource,
			"sdk_pin", sdkModPin,
			"target_module", targetSrc.ModuleOriginalName,
			"target_source", targetSrc.AsString(),
			"target_pin", targetSrc.Pin(),
			"target_sdk_source", targetSDKSource,
			"error", err,
		)
		return inst, fmt.Errorf("failed to call sdk moduleRuntime: %w", err)
	}

	toolchainDebug(
		ctx,
		"toolchain-debug sdk moduleRuntime success",
		"sdk_module", sdkMod.Name(),
		"sdk_source", sdkModSource,
		"sdk_pin", sdkModPin,
		"target_module", targetSrc.ModuleOriginalName,
		"target_source", targetSrc.AsString(),
		"target_pin", targetSrc.Pin(),
		"target_sdk_source", targetSDKSource,
	)

	return inst, nil
}
