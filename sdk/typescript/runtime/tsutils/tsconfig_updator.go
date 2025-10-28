package tsutils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type TSConfig map[string]any
type CompilerOptions = map[string]any
type CompilerOptionsPath = map[string][]string

const (
	daggerLibPathAlias       = "@dagger.io/dagger"
	daggerTelemetryPathAlias = "@dagger.io/dagger/telemetry"
	daggerClientPathAlias    = "@dagger.io/client"

	daggerLibPath          = "./sdk/index.ts"
	daggerTelemetryLibPath = "./sdk/telemetry.ts"
)

var defaultTSConfig = TSConfig{
	"compilerOptions": CompilerOptions{
		"target":                 "ES2022",
		"moduleResolution":       "Node",
		"experimentalDecorators": true,
		"strict":                 true,
		"skipLibCheck":           true,
		"paths": CompilerOptionsPath{
			daggerLibPathAlias: {
				daggerLibPath,
			},
			daggerTelemetryPathAlias: {
				daggerTelemetryLibPath,
			},
		},
	},
}

// Generate a default tsconfig.json file for a module
func DefaultTSConfigForModule() (string, error) {
	res, err := json.MarshalIndent(defaultTSConfig, "", "  ")
	if err != nil {
		return "", err
	}

	return string(res), nil
}

func DefaultTSConfigForClient(clientDir string) (string, error) {
	defaultConfig, err := json.MarshalIndent(defaultTSConfig, "", "  ")
	if err != nil {
		return "", err
	}

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

func removeJSONComments(input string) string {
	var out bytes.Buffer
	lines := strings.Split(input, "\n")
	for _, line := range lines {
		// remove everything after // (simple approach)
		if idx := strings.Index(strings.TrimSpace(line), "//"); idx >= 0 {
			line = line[:idx]
		}
		out.WriteString(line + "\n")
	}
	return out.String()
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

func UpdateTSConfigForClient(tsConfig string, clientDir string) (string, error) {
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

	// Add path."@dagger.io/client"=[<path to client dir>]
	tsConfig, err = sjson.Set(tsConfig,
		"compilerOptions.paths."+gjson.Escape(daggerClientPathAlias),
		// We explicitely add `./` so tsx can correctly interpret the path.
		[]string{"./" + filepath.Join(clientDir, "client.gen.ts")},
	)
	if err != nil {
		return "", fmt.Errorf("failed to update tsconfig paths %s: %w", daggerClientPathAlias, err)
	}

	return tsConfig, nil
}
