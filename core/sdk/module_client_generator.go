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

	err = sdk.mod.dag.Select(ctx, sdk.mod.sdk, &res, dagql.Selector{
		Field: "requiredClientGenerationFiles",
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get required client generation files: %w", err)
	}

	return res, nil
}

func (sdk *clientGeneratorModule) GenerateClient(
	ctx context.Context,
	modSource dagql.Instance[*core.ModuleSource],
	deps *core.ModDeps,
	outputDir string,
	dev bool,
) (inst dagql.Instance[*core.Directory], err error) {
	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during module client generation: %w", err)
	}

	fct, ok := sdk.funcs["generateClient"]
	if !ok {
		return inst, fmt.Errorf("generateClient is not implemented by this SDK")
	}

	generateClientsArgs := []dagql.NamedInput{
		{
			Name:  "modSource",
			Value: dagql.NewID[*core.ModuleSource](modSource.ID()),
		},
		{
			Name:  "introspectionJson",
			Value: dagql.NewID[*core.File](schemaJSONFile.ID()),
		},
		{
			Name:  "outputDir",
			Value: dagql.String(outputDir),
		},
	}

	_, devFlagExist := fct.LookupArg("dev")
	if devFlagExist {
		generateClientsArgs = append(generateClientsArgs, dagql.NamedInput{
			Name:  "dev",
			Value: dagql.NewBoolean(dev),
		})
	}

	err = sdk.mod.dag.Select(ctx, sdk.mod.sdk, &inst, dagql.Selector{
		Field: "generateClient",
		Args:  generateClientsArgs,
	})
	if err != nil {
		return inst, fmt.Errorf("failed to call sdk module generate client: %w", err)
	}
	return inst, nil
}
