package main

import (
	"context"
	"fmt"
	"path"
	"strings"

	"rust-sdk/internal/dagger"
	"rust-sdk/internal/metadata"
)

// ModuleRuntime verifies committed generation for current modules and regenerates
// only inside a private snapshot for legacy modules before compiling the fixed target.
func (sdk *RustSDK) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	// +optional
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	mode := "checked-generated"
	configFormat := "current"
	if introspectionJSON != nil {
		mode = "legacy-runtime-codegen"
		configFormat = "legacy"
	}
	identity, err := moduleIdentity(ctx, modSource, configFormat)
	if err != nil {
		return nil, err
	}

	project := dag.Directory().WithDirectory(operationWorkspaceRoot, modSource.ContextDirectory())
	if introspectionJSON != nil {
		request, _, err := sdk.generationRequest(ctx, "generate-module", identity, introspectionJSON, identity.SourceSubpath)
		if err != nil {
			return nil, err
		}
		execution, err := sdk.executeOperation(ctx, request, introspectionJSON, modSource.ContextDirectory(), "generation")
		if err != nil {
			return nil, fmt.Errorf("generate private legacy Rust runtime source: %w", err)
		}
		project = execution.project
	}

	descriptor, err := sdk.descriptorMetadata(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := sdk.runtimePolicyMetadata(ctx)
	if err != nil {
		return nil, err
	}
	rustTarget, err := runtimeTarget(ctx, policy)
	if err != nil {
		return nil, err
	}
	runtimeRequest := map[string]any{
		"format_version": 1,
		"target":         targetIdentity(descriptor),
		"module": map[string]any{
			"name":           identity.Name,
			"original_name":  identity.OriginalName,
			"source_subpath": identity.SourceSubpath,
			"config_format":  identity.ConfigFormat,
			"source_digest":  identity.SourceDigest,
		},
		"mode":               mode,
		"operation_manifest": path.Join(identity.SourceSubpath, ".dagger/rust/operation-manifest.json"),
		"base_image_digest":  policy.RuntimeBaseDigest,
		"rust_target":        rustTarget,
	}
	requestBytes, err := metadata.CanonicalJSON(runtimeRequest)
	if err != nil {
		return nil, fmt.Errorf("encode Rust runtime request: %w", err)
	}

	builder := dag.Container().From(policy.BuildImage).
		WithFile(operationToolPath, sdk.engineTool(), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithFile(operationDescriptorPath, sdk.engineDescriptor()).
		WithFile(runtimePolicyMountPath, sdk.SDKSourceDir.File(runtimePolicyPath)).
		WithDirectory(operationProjectPath, project).
		WithNewFile(operationRequestPath, string(requestBytes)).
		WithExec([]string{
			operationToolPath, "verify-runtime",
			"--request", operationRequestPath,
			"--descriptor", operationDescriptorPath,
			"--policy", runtimePolicyMountPath,
			"--project", operationProjectPath,
			"--output", runtimePlanPath,
		}, dagger.ContainerWithExecOpts{
			Expect:         dagger.ReturnTypeAny,
			RedirectStderr: runtimeDiagnosticPath,
		})
	// Querying the exit code preserves the failed container so its bounded Rust
	// diagnostic remains readable. Sync would collapse that typed failure into an
	// opaque execution error before the adapter can validate and project it.
	verified := builder
	verificationExit, err := verified.ExitCode(ctx)
	if err != nil {
		return nil, safeRuntimeFailure("verification", err)
	}
	if verificationExit != 0 {
		contents, readErr := verified.File(runtimeDiagnosticPath).Contents(ctx)
		if readErr != nil {
			return nil, safeRuntimeFailure("verification", readErr)
		}
		return nil, safeRuntimeDiagnostic("verification", []byte(contents))
	}
	planBytes, err := verified.File(runtimePlanPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read verified Rust runtime plan: %w", err)
	}
	plan, err := metadata.DecodeRuntimeBuildPlan([]byte(planBytes))
	if err != nil {
		return nil, err
	}

	cacheKey := strings.TrimPrefix(metadata.DigestBytes([]byte(
		descriptor.RustToolchain+"\x00"+rustTarget+"\x00"+identity.SourceDigest,
	)), "sha256:")
	builder = verified.
		WithMountedCache("/usr/local/cargo/registry", dag.CacheVolume("rust-registry-"+cacheKey)).
		WithMountedCache("/usr/local/cargo/git", dag.CacheVolume("rust-git-"+cacheKey)).
		WithMountedCache(policy.CargoTargetDir, dag.CacheVolume("rust-target-"+cacheKey)).
		WithWorkdir(operationProjectPath).
		WithExec(append([]string{"/usr/local/cargo/bin/cargo"}, plan.CargoArgs...))
	built, err := builder.Sync(ctx)
	if err != nil {
		return nil, safeRuntimeFailure("Cargo build", err)
	}

	// Cross-target Cargo output includes the target triple. Copying the verified file
	// into the fixed promotion path lets finalization reject every alternate binary.
	compiledPath := path.Join(policy.CargoTargetDir, rustTarget, plan.BinaryRelativePath)
	built = built.
		WithFile(policy.RuntimeBinaryPath, built.File(compiledPath), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithExec([]string{"/usr/bin/strip", "--strip-unneeded", policy.RuntimeBinaryPath}).
		WithExec([]string{
			operationToolPath, "finalize-runtime",
			"--plan", runtimePlanPath,
			"--policy", runtimePolicyMountPath,
			"--binary", policy.RuntimeBinaryPath,
			"--output", runtimeProvenancePath,
		})
	finalized, err := built.Sync(ctx)
	if err != nil {
		return nil, safeRuntimeFailure("finalization", err)
	}

	return dag.Container().From(policy.RuntimeBaseImage).
		WithFile(policy.RuntimeInstallPath, finalized.File(policy.RuntimeBinaryPath), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithFile(policy.ProvenanceInstallPath, finalized.File(runtimeProvenancePath)).
		WithoutDefaultArgs().
		WithEntrypoint([]string{policy.RuntimeInstallPath}).
		WithWorkdir("/scratch"), nil
}

func runtimeTarget(ctx context.Context, policy metadata.RuntimePolicy) (string, error) {
	platform, err := dag.DefaultPlatform(ctx)
	if err != nil {
		return "", fmt.Errorf("read engine platform: %w", err)
	}
	switch {
	case strings.Contains(string(platform), "amd64"):
		return policy.LinuxAMD64Target, nil
	case strings.Contains(string(platform), "arm64"):
		return policy.LinuxARM64Target, nil
	default:
		return "", fmt.Errorf("Rust runtime does not support engine platform %q", platform)
	}
}

// Runtime process errors may contain caller-authored registry URLs or compiler source
// excerpts. The stable phase is safe to return; detailed execution remains in Dagger's
// protected execution record rather than becoming an SDK diagnostic payload.
func safeRuntimeFailure(phase string, _ error) error {
	return fmt.Errorf("Rust runtime %s failed", phase)
}

func safeRuntimeDiagnostic(phase string, contents []byte) error {
	diagnostic, err := metadata.DecodeEngineDiagnostic(contents)
	if err != nil {
		return safeRuntimeFailure(phase, err)
	}
	if diagnostic.Coordinate == "" {
		return fmt.Errorf("Rust runtime %s failed [%s]: %s", phase, diagnostic.Code, diagnostic.Message)
	}
	return fmt.Errorf(
		"Rust runtime %s failed [%s at %s]: %s",
		phase, diagnostic.Code, diagnostic.Coordinate, diagnostic.Message,
	)
}
