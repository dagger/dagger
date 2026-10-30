// Package dangv2 evaluates Dagger modules written in Dang with the
// github.com/vito/dang/v2 library, which parses every Dang language version
// since 1.0: each module's source is parsed as the version it is written in
// (ModuleSource.DangVersion). See core/sdk/dang/README.md.
package dangv2

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/vito/dang/v2/pkg/dang"
)

// Impl is the Dang implementation behind the dang SDK.
type Impl struct{}

func (Impl) ModuleTypes(
	ctx context.Context,
	deps *core.SchemaBuilder,
	src dagql.ObjectResult[*core.ModuleSource],
	scopedMod dagql.ObjectResult[*core.Module],
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for dang module sdk module types: %w", err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during dang module sdk module types: %w", err)
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, fmt.Errorf("current query: %w", err)
	}

	nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return inst, err
	}

	runner := dangSourceRunner(func(ctx context.Context, modSrcDir string) (dang.ValueScope, error) {
		return dang.RunDir(ctx, modSrcDir, false)
	})
	// When self-calls are enabled the module's own types (and root fields like
	// `tuiQa`) only exist in the *runtime* schema, after this ModuleTypes pass
	// has installed them. So the declaration-only runner is required here: full
	// inference would try to resolve self-referencing bodies against the deps
	// schema, which does not yet carry the module's own API. The Dang SDK always
	// enables self-calls (AlwaysEnablesSelfCalls), so gate on SelfCallsEnabled
	// rather than the raw experimental flag.
	declareOnly := src.Self().SelfCallsEnabled()
	if isDang1Module(src.Self()) {
		// The SDK of the Dang v1 line declared only when the module enabled
		// self calls itself, and declaring alone types fewer fields (a `pub`
		// field with no annotation and a computed value has no type until
		// its body is inferred), so a Dang 1.0 module keeps that.
		sdk := src.Self().SDK
		declareOnly = sdk != nil && sdk.ExperimentalFeatureEnabled(core.ModuleSourceExperimentalFeatureSelfCalls)
	}
	if declareOnly {
		runner = runDangDirForModuleTypes
	}

	_, err = evalDangSource(ctx, query, src, schemaJSONFile, nestedClientMetadata, true /* inert attachables */, nil, scopedMod, true /* for typedefs */, runner, func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
		inst, err = initDangModule(ctx, dag, env)
		if err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func (Impl) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (core.ModuleRuntime, error) {
	return &runtime{
		deps:      deps,
		modSource: source,
	}, nil
}

// runtime is a native Dang runtime that doesn't use containers
type runtime struct {
	deps      *core.SchemaBuilder
	modSource dagql.ObjectResult[*core.ModuleSource]
}

func (r *runtime) AsContainer() (dagql.ObjectResult[*core.Container], bool) {
	// Dang runtime doesn't use containers
	return dagql.ObjectResult[*core.Container]{}, false
}

func (r *runtime) Call(
	ctx context.Context,
	_ *engineutil.ExecutionMetadata,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
) (rerr error) {
	defer func() {
		if rerr != nil {
			rerr = dangshared.ConvertError(rerr)
		}
	}()

	nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("current query: %w", err)
	}
	schemaJSONFile, err := r.deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return fmt.Errorf("get schema introspection: %w", err)
	}
	outputBytes, err := r.eval(ctx, query, schemaJSONFile, nestedClientMetadata, true /* inert attachables */, fnCall, moduleContext)
	if err != nil {
		return err
	}
	return fnCall.ReturnValue(ctx, core.JSON(outputBytes))
}
