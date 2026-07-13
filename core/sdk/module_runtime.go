package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
)

// A SDK module that implements the `Runtime` interface
type runtimeModule struct {
	mod *module
}

func (sdk *runtimeModule) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ core.ModuleRuntime, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load runtime")
	defer telemetry.EndWithCause(span, &rerr)

	sdkInst, err := sdk.mod.instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	dag := sdkInst.dag

	source, err = scopeSourceForSDKOperation(ctx, source, "runtime", dag)
	if err != nil {
		return nil, fmt.Errorf("failed to scope module source for sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	sourceID, err := source.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get scoped module source ID for sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	schemaJSONFileID, err := schemaJSONFile.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json ID during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	runtimeArgs := []dagql.NamedInput{
		{
			Name:  "modSource",
			Value: dagql.NewID[*core.ModuleSource](sourceID),
		},
		{
			Name:  "introspectionJson",
			Value: dagql.NewID[*core.File](schemaJSONFileID),
		},
	}
	gitCredInput, err := sdk.mod.gitCredentialsInput(ctx, dag, "moduleRuntime", source)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare git credentials for sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	if gitCredInput != nil {
		runtimeArgs = append(runtimeArgs, *gitCredInput)
	}

	var inst dagql.ObjectResult[*core.Container]
	err = dag.Select(ctx, sdkInst.sdk, &inst,
		dagql.Selector{
			Field: "moduleRuntime",
			Args:  runtimeArgs,
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
		return nil, fmt.Errorf("failed to call sdk moduleRuntime: %w", err)
	}

	// backstop: user code runs in this container, so a leaky runtime must
	// fail closed rather than expose the credential socket
	for _, sock := range inst.Self().Sockets {
		if sock.Source.Self() != nil && sock.Source.Self().Kind == core.SocketKindGitCredential {
			return nil, fmt.Errorf("sdk module %s returned a runtime container that still mounts the git-credential socket", sdk.mod.mod.Self().Name())
		}
	}
	return &core.ContainerRuntime{Container: inst}, nil
}
