package main

import (
	"context"
	"encoding/json"
	"fmt"

	"dagger/engine-dev/build"
	"dagger/engine-dev/internal/dagger"
)

const (
	typescriptDirPath     = "sdk/typescript"
	telemetryDirPath      = typescriptDirPath + "/telemetry"
	typescriptPackagePath = typescriptDirPath + "/package.json"
	telemetryPackagePath  = telemetryDirPath + "/package.json"
	telemetryPackageName  = "@dagger.io/telemetry"
	telemetryTestVersion  = "99.99.99"
)

// Check that the TypeScript SDK build resolves telemetry from the local split package.
// +check
func (dev *EngineDev) CheckTsSdkUsesLocalTelemetry(ctx context.Context) error {
	sourceWithVersion, err := withTypescriptTelemetryVersion(ctx, dev.Source, telemetryTestVersion)
	if err != nil {
		return err
	}

	if err := build.ValidateTypescriptSDKContent(ctx, sourceWithVersion); err != nil {
		return fmt.Errorf(
			"typescript sdk build should succeed with local telemetry version %q: %w",
			telemetryTestVersion,
			err,
		)
	}

	if err := build.ValidateTypescriptSDKContent(ctx, sourceWithVersion.WithoutDirectory(telemetryDirPath)); err == nil {
		return fmt.Errorf("expected typescript sdk build to fail when local telemetry package is missing")
	}

	return nil
}

func withTypescriptTelemetryVersion(
	ctx context.Context,
	source *dagger.Directory,
	version string,
) (*dagger.Directory, error) {
	source, err := mutateJSONFile(ctx, source, typescriptPackagePath, func(root map[string]any) error {
		dependenciesAny, ok := root["dependencies"]
		if !ok {
			return fmt.Errorf("dependencies field missing")
		}
		dependencies, ok := dependenciesAny.(map[string]any)
		if !ok {
			return fmt.Errorf("dependencies field is not an object")
		}
		if _, ok := dependencies[telemetryPackageName]; !ok {
			return fmt.Errorf("%s dependency missing", telemetryPackageName)
		}

		dependencies[telemetryPackageName] = version
		return nil
	})
	if err != nil {
		return nil, err
	}

	return mutateJSONFile(ctx, source, telemetryPackagePath, func(root map[string]any) error {
		root["version"] = version
		return nil
	})
}

func mutateJSONFile(
	ctx context.Context,
	source *dagger.Directory,
	path string,
	mutate func(map[string]any) error,
) (*dagger.Directory, error) {
	content, err := source.File(path).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := mutate(root); err != nil {
		return nil, fmt.Errorf("update %s: %w", path, err)
	}

	updated, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", path, err)
	}

	return source.WithNewFile(path, string(updated)+"\n"), nil
}
