package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// A SDK module that implements the `ClientGenerator` interface
type clientGeneratorModule struct {
	mod *module

	funcs map[string]*core.Function
}

func (sdk *clientGeneratorModule) RequiredClientGenerationFiles(
	ctx context.Context,
) (res dagql.Array[dagql.String], err error) {
	// Return an empty array if the SDK doesn't implement the
	// `requiredClientGenerationFiles` function.
	if _, ok := sdk.funcs["requiredClientGenerationFiles"]; !ok {
		return dagql.NewStringArray(), nil
	}

	sdkInst, err := sdk.mod.instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sdk module %s required client generation files: %w", sdk.mod.mod.Self().Name(), err)
	}
	dag := sdkInst.dag

	err = dag.Select(ctx, sdkInst.sdk, &res, dagql.Selector{
		Field: "requiredClientGenerationFiles",
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get required client generation files: %w", err)
	}

	return res, nil
}

func (sdk *clientGeneratorModule) GenerateClient(
	ctx context.Context,
	modSource dagql.ObjectResult[*core.ModuleSource],
	schemaJSONFile dagql.Result[*core.File],
	outputDir string,
) (inst dagql.ObjectResult[*core.Directory], err error) {
	_, ok := sdk.funcs["generateClient"]
	if !ok {
		return inst, fmt.Errorf("generateClient is not implemented by this SDK")
	}

	sdkInst, err := sdk.mod.instantiate(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to initialize sdk module %s generate client: %w", sdk.mod.mod.Self().Name(), err)
	}
	dag := sdkInst.dag

	modSource, err = scopeSourceForSDKOperation(ctx, modSource, "generateClient", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module source for sdk module %s generate client: %w", sdk.mod.mod.Self().Name(), err)
	}
	modSourceID, err := modSource.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get scoped module source ID for sdk module %s generate client: %w", sdk.mod.mod.Self().Name(), err)
	}
	schemaJSONFileID, err := schemaJSONFile.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json ID during module client generation: %w", err)
	}

	generateClientsArgs := []dagql.NamedInput{
		{
			Name:  "modSource",
			Value: dagql.NewID[*core.ModuleSource](modSourceID),
		},
		{
			Name:  "introspectionJson",
			Value: dagql.NewID[*core.File](schemaJSONFileID),
		},
		{
			Name:  "outputDir",
			Value: dagql.String(outputDir),
		},
	}

	err = dag.Select(ctx, sdkInst.sdk, &inst, dagql.Selector{
		Field: "generateClient",
		Args:  generateClientsArgs,
	})
	if err != nil {
		return inst, fmt.Errorf("failed to call sdk module generate client: %w", err)
	}
	return inst, nil
}
