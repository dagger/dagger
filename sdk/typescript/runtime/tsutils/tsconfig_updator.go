package tsutils

import (
	"fmt"
	"path/filepath"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	daggerLibPathAlias       = "@dagger.io/dagger"
	daggerTelemetryPathAlias = "@dagger.io/dagger/telemetry"
	daggerClientPathAlias    = "@dagger.io/client"

	daggerLibPath          = "./sdk/index.ts"
	daggerTelemetryLibPath = "./sdk/telemetry.ts"
)

// Generate a default tsconfig.json file for a module
func DefaultTSConfigForModule() string {
	return DefaultTSConfigJSON
}

func DefaultTSConfigForClient(clientDir string) (string, error) {
	defaultConfig := DefaultTSConfigJSON

	// Add path."@dagger.io/client"=[<path to client dir>]
	tsConfig, err := sjson.Set(string(defaultConfig),
		"compilerOptions.paths."+gjson.Escape(daggerClientPathAlias),
		// We explicitely add `./` so tsx can correctly interpret the path.
		[]string{"./" + filepath.Join(clientDir, "client.gen.ts")},
	)
	if err != nil {
		return "", fmt.Errorf("failed to update tsconfig paths %s: %w", daggerClientPathAlias, err)
	}

	return tsConfig, nil
}

// Update the tsconfig.json file for a module
func UpdateTSConfigForModule(tsConfig string) (string, error) {
	tsConfig = removeJSONComments(tsConfig)

	// Add path."@dagger.io/dagger"=["./sdk/index.ts"]
	tsConfig, err := sjson.Set(tsConfig,
		"compilerOptions.paths."+gjson.Escape(daggerLibPathAlias),
		[]string{daggerLibPath},
	)
	if err != nil {
		return "", fmt.Errorf("failed to update tsconfig paths: %w", err)
	}

	// Add path."@dagger.io/dagger/telemetry"=["./sdk/telemetry.ts"]
	tsConfig, err = sjson.Set(tsConfig,
		"compilerOptions.paths."+gjson.Escape(daggerTelemetryPathAlias),
		[]string{daggerTelemetryLibPath},
	)
	if err != nil {
		return "", fmt.Errorf("failed to update tsconfig paths %s: %w", daggerTelemetryPathAlias, err)
	}

	// Add compilerOptions.experimentalDecorators=true
	tsConfig, err = sjson.Set(tsConfig, "compilerOptions.experimentalDecorators", true)
	if err != nil {
		return "", fmt.Errorf("failed to update tsconfig experimentalDecorators: %w", err)
	}

	return tsConfig, nil
}

func UpdateTSConfigForClient(tsConfig string, clientDir string, isRemote bool) (string, error) {
	tsConfig = removeJSONComments(tsConfig)

	// Add path."@dagger.io/client"=[<path to client dir>]
	tsConfig, err := sjson.Set(tsConfig,
		"compilerOptions.paths."+gjson.Escape(daggerClientPathAlias),
		// We explicitely add `./` so tsx can correctly interpret the path.
		[]string{"./" + filepath.Join(clientDir, "client.gen.ts")},
	)
	if err != nil {
		return "", fmt.Errorf("failed to update tsconfig paths %s: %w", daggerClientPathAlias, err)
	}

	// If the dagger library is remote, we don't need to override @dagger.io/dagger
	// and @dagger.io/dagger/telemetry
	if isRemote {
		return tsConfig, nil
	}

	// Add path."@dagger.io/dagger/telemetry"=["./sdk/telemetry.ts"]
	tsConfig, err = sjson.Set(tsConfig,
		"compilerOptions.paths."+gjson.Escape(daggerTelemetryPathAlias),
		[]string{daggerTelemetryLibPath},
	)
	if err != nil {
		return "", fmt.Errorf("failed to update tsconfig paths %s: %w", daggerTelemetryPathAlias, err)
	}

	// Add path."@dagger.io/dagger"=["./sdk/index.ts"]
	tsConfig, err = sjson.Set(tsConfig,
		"compilerOptions.paths."+gjson.Escape(daggerLibPathAlias),
		[]string{daggerLibPath},
	)
	if err != nil {
		return "", fmt.Errorf("failed to update tsconfig paths: %w", err)
	}

	return tsConfig, nil
}
