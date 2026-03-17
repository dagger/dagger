package tsutils

import (
	"fmt"
	"typescript-sdk/tsdistconsts"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var unstableFlags = []string{
	"bare-node-builtins",
	"sloppy-imports",
	"node-globals",
	"byonm",
}

func updateDenoConfigForDagger(denoConfig string) (string, error) {
	denoConfig = removeJSONComments(denoConfig)

	// Add imports.typescript
	denoConfig, err := setIfNotExists(denoConfig, "imports.typescript", "npm:typescript@"+tsdistconsts.DefaultTypeScriptVersion)
	if err != nil {
		return "", fmt.Errorf("failed to set typescript in deno.json")
	}

	// Add nodeModulesDir="auto"
	denoConfig, err = sjson.Set(denoConfig, "nodeModulesDir", "auto")
	if err != nil {
		return "", fmt.Errorf("failed to update deno config nodeModulesDir: %w", err)
	}

	// Add unstable flags if they do not exist
	for _, flag := range unstableFlags {
		denoConfig, err = appendIfNotExists(denoConfig, "unstable", flag)
		if err != nil {
			return "", fmt.Errorf("failed to set unstable flag %s in deno.json: %w", flag, err)
		}
	}

	return denoConfig, nil
}

func UpdateDenoConfigForModule(denoConfig string) (string, error) {
	denoConfig, err := updateDenoConfigForDagger(denoConfig)
	if err != nil {
		return "", fmt.Errorf("failed to update deno config for dagger: %w", err)
	}

	// Add compilerOptions.experimentalDecorators=true
	denoConfig, err = sjson.Set(denoConfig, "compilerOptions.experimentalDecorators", true)
	if err != nil {
		return "", fmt.Errorf("failed to update deno config experimentalDecorators: %w", err)
	}

	// Add imports."@dagger.io/dagger"="./sdk/index.ts"
	denoConfig, err = sjson.Set(denoConfig,
		"imports."+gjson.Escape(daggerLibPathAlias),
		daggerLibPath,
	)
	if err != nil {
		return "", fmt.Errorf("failed to update deno config paths: %w", err)
	}

	// Add imports."@dagger.io/dagger/telemetry"="./sdk/telemetry.ts"
	denoConfig, err = sjson.Set(denoConfig,
		"imports."+gjson.Escape(daggerTelemetryPathAlias),
		daggerTelemetryLibPath,
	)
	if err != nil {
		return "", fmt.Errorf("failed to update deno config paths %s: %w", daggerTelemetryPathAlias, err)
	}

	return denoConfig, nil
}

func UpdateDenoConfigForClient(denoConfig string, isRemote bool) (string, error) {
	denoConfig, err := updateDenoConfigForDagger(denoConfig)
	if err != nil {
		return "", fmt.Errorf("failed to update deno config for dagger: %w", err)
	}

	// If the dagger library is remote, we don't need to override @dagger.io/dagger
	// and @dagger.io/dagger/telemetry
	if isRemote {
		return denoConfig, nil
	}

	// Add imports."@dagger.io/dagger"="./sdk/index.ts"
	denoConfig, err = sjson.Set(denoConfig,
		"imports."+gjson.Escape(daggerLibPathAlias),
		daggerLibPath,
	)
	if err != nil {
		return "", fmt.Errorf("failed to update deno config paths: %w", err)
	}

	// Add imports."@dagger.io/dagger/telemetry"="./sdk/telemetry.ts"
	denoConfig, err = sjson.Set(denoConfig,
		"imports."+gjson.Escape(daggerTelemetryPathAlias),
		daggerTelemetryLibPath,
	)
	if err != nil {
		return "", fmt.Errorf("failed to update deno config paths %s: %w", daggerTelemetryPathAlias, err)
	}

	return denoConfig, nil
}
