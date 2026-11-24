package sdk

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
)

// moduleRuntime's introspection argument, as the legacy naming rules spell it
// in the SDK module's schema; see (*module).argName.
const introspectionJSONArgName = "introspectionJson"

// A SDK module that implements the `Runtime` interface
type runtimeModule struct {
	mod *module
}

func (sdk *runtimeModule) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ core.ModuleRuntime, rerr error) {
	// Encapsulated: the SDK's work is its own, and only a step of it that
	// failed shows (see dagui's spanWalker.passesOnFailure).
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load runtime", telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	inst, err := sdk.runtimeContainer(ctx, deps, source)
	if err != nil {
		return nil, err
	}
	if cfg := source.Self().SDK; sdk.mod.debugTerminal && cfg != nil && cfg.Debug {
		dag, err := sdk.mod.root.Server.Server(ctx)
		if err != nil {
			return nil, err
		}
		if err := dag.Select(ctx, inst, &inst, dagql.Selector{Field: "terminal"}); err != nil {
			return nil, fmt.Errorf("failed to enable %s runtime terminal: %w", sdk.mod.mod.Self().Name(), err)
		}
	}
	return &core.ContainerRuntime{Container: inst}, nil
}

// PrebuildRuntime builds the runtime of a module that skips runtime codegen
// (skipRuntimeCodegen): moduleRuntime reads only the module's own files then,
// not its dependencies' schema, so it builds while they load, and Runtime
// finds it built.
func (sdk *runtimeModule) PrebuildRuntime(
	ctx context.Context,
	source dagql.ObjectResult[*core.ModuleSource],
) (rerr error) {
	if sdk.mod.moduleSDKOpts.moduleTypes != nil {
		return nil
	}
	sdkInst, err := sdk.mod.instantiate(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	scoped, err := scopeSourceForSDKOperation(ctx, source, "runtime", sdkInst.dag)
	if err != nil {
		return fmt.Errorf("failed to scope module source for sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	if !sdk.skipRuntimeCodegen(scoped) {
		return nil
	}

	// Not internal: the build's own spans are recorded under this one.
	// Encapsulated, as the runtime's load is.
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: build runtime", telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	inst, err := sdk.runtimeContainer(ctx, nil, source)
	if err != nil {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	// Container execs are lazy: evaluate the runtime so the build runs now.
	return cache.Evaluate(ctx, inst)
}

// runtimeContainer is the SDK's moduleRuntime of the implementation-scoped
// source. deps is read only when the runtime needs codegen.
func (sdk *runtimeModule) runtimeContainer(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (inst dagql.ObjectResult[*core.Container], _ error) {
	sdkInst, err := sdk.mod.instantiate(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to initialize sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	dag := sdkInst.dag

	source, err = scopeSourceForSDKOperation(ctx, source, "runtime", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module source for sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	if sdk.skipRuntimeCodegen(source) {
		if err := requireGeneratedFiles(ctx, dag, source, sdk.mod.generatedFiles); err != nil {
			return inst, err
		}
	}

	sourceID, err := source.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get scoped module source ID for sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	args := []dagql.NamedInput{
		{
			Name:  sdk.mod.argName("modSource"),
			Value: dagql.NewID[*core.ModuleSource](sourceID),
		},
	}

	if !sdk.skipRuntimeCodegen(source) {
		schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
		}
		schemaJSONFileID, err := schemaJSONFile.ID()
		if err != nil {
			return inst, fmt.Errorf("failed to get schema introspection json ID during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
		}
		args = append(args, dagql.NamedInput{
			Name:  sdk.mod.argName(introspectionJSONArgName),
			Value: dagql.NewID[*core.File](schemaJSONFileID),
		})
	}

	err = sdkInst.selectSDK(ctx, &inst,
		dagql.Selector{
			Field: "moduleRuntime",
			Args:  args,
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
		return inst, fmt.Errorf("failed to call sdk moduleRuntime: %w", err)
	}
	return inst, nil
}

// skipRuntimeCodegen reports whether the moduleRuntime call may omit the
// introspection JSON: the config format rules out runtime codegen and the
// SDK declared it builds from committed files (optional introspectionJson).
func (sdk *runtimeModule) skipRuntimeCodegen(src dagql.ObjectResult[*core.ModuleSource]) bool {
	return !useRuntimeCodegen(src) && sdk.mod.RuntimeTrustsCommittedFiles()
}

// requireGeneratedFiles verifies that a module building from committed files
// has each of files, from its source directory.
func requireGeneratedFiles(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
	files []string,
) error {
	for _, name := range files {
		rel := filepath.Join(src.Self().SourceSubpath, name)
		var exists dagql.Boolean
		if err := dag.Select(ctx, src.Self().ContextDirectory, &exists,
			dagql.Selector{
				Field: "exists",
				Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(rel)}},
			},
		); err != nil {
			return fmt.Errorf("check generated file %q: %w", rel, err)
		}
		if !bool(exists) {
			return &core.MissingGeneratedFileError{Module: src.Self().ModuleOriginalName, Path: rel}
		}
	}
	return nil
}
