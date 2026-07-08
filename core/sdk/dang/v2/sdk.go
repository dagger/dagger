// Package dangv2 evaluates Dagger modules written against Dang v2
// (github.com/vito/dang/v2: `.{ }` is dot-block application, `.{{ }}` is
// selection).
//
// This is the LIVING implementation: new Dang SDK features land here only.
// Older majors are frozen snapshots of this package; see
// core/sdk/dang/README.md for the maintenance policy.
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

// Impl is the Dang v2 implementation behind the dang SDK's version dispatch.
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

	clientMetadata, nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return inst, err
	}

	runner := dangSourceRunner(func(ctx context.Context, modSrcDir string) (dang.ValueScope, error) {
		return dang.RunDir(ctx, modSrcDir, false)
	})
	if src.Self().SDK.ExperimentalFeatureEnabled(core.ModuleSourceExperimentalFeatureSelfCalls) {
		runner = runDangDirForModuleTypes
	}

	_, err = evalDangSource(ctx, query, src, schemaJSONFile, nestedClientMetadata, clientMetadata.ClientID, true, nil, scopedMod, runner, func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
		inst, err = initDangModule(ctx, dag, env)
		if err != nil {
			return nil, fmt.Errorf("init module: %w", err)
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

	clientMetadata, nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
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
	outputBytes, err := r.eval(ctx, query, schemaJSONFile, nestedClientMetadata, clientMetadata.ClientID, true, fnCall, moduleContext)
	if err != nil {
		return err
	}
	return fnCall.ReturnValue(ctx, core.JSON(outputBytes))
}
