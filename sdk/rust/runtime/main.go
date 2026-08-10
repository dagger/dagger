// Package main exposes the module-backed ABI adapter for the built-in Rust SDK.
package main

import (
	"context"
	"fmt"

	"rust-sdk/internal/dagger"
	"rust-sdk/internal/metadata"
)

const (
	sdkRuntimePath          = "runtime"
	engineToolPath          = "dist/dagger-rust-engine"
	engineDescriptorPath    = "dist/engine-source.json"
	clientGenerationPath    = "dist/client-generation.json"
	operationRequestPath    = "/var/lib/dagger/rust/request.json"
	operationSchemaPath     = "/var/lib/dagger/rust/schema.json"
	operationProjectPath    = "/var/lib/dagger/rust/project"
	operationDescriptorPath = "/usr/local/share/dagger/rust/engine-source.json"
	operationToolPath       = "/usr/local/bin/dagger-rust-engine"
)

// RustSDK is immutable packaged state. Operation methods build fresh Dagger object
// graphs from this source instead of retaining mutable container or project handles.
type RustSDK struct {
	SDKSourceDir *dagger.Directory // +private
}

// New constructs the built-in adapter from the complete packaged SDK content root.
func New(
	// Complete engine-packaged Rust SDK content, including runtime and dist metadata.
	sdkSourceDir *dagger.Directory,
) (*RustSDK, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &RustSDK{SDKSourceDir: sdkSourceDir}, nil
}

// engineDescriptor returns the opaque Rust-owned descriptor. Go forwards these bytes
// to the packaged tool and deliberately does not reinterpret dependency provenance.
func (sdk *RustSDK) engineDescriptor() *dagger.File {
	return sdk.SDKSourceDir.File(engineDescriptorPath)
}

func (sdk *RustSDK) engineTool() *dagger.File {
	return sdk.SDKSourceDir.File(engineToolPath)
}

func (sdk *RustSDK) clientGenerationMetadata(ctx context.Context) (metadata.ClientGeneration, error) {
	contents, err := sdk.SDKSourceDir.File(clientGenerationPath).Contents(ctx)
	if err != nil {
		return metadata.ClientGeneration{}, fmt.Errorf("read packaged client-generation metadata: %w", err)
	}
	return metadata.DecodeClientGeneration([]byte(contents))
}
