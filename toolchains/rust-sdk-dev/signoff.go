package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"dagger/rust-sdk-dev/internal/dagger"
	signoffmodel "dagger/rust-sdk-dev/internal/signoff"
)

const (
	signoffPlanPath       = "/artifact/plan.json"
	signoffPayloadPath    = "/artifact/engine.oci.tar.zst"
	signoffBundlePath     = "/artifact/exact-target.tar"
	signoffManifestPath   = "/artifact/manifest.json"
	signoffImportedPath   = "/artifact/imported-engine.oci.tar.zst"
	signoffCliPath        = "/usr/local/bin/dagger"
	signoffArtifactBinary = "dagger-rust-sdk-signoff"
	signoffEngineAlias    = "dagger-engine"
	signoffEngineEndpoint = "tcp://dagger-engine:1234"
)

// RustSignoffArtifact is one exportable exact-target bundle and its retained build graph.
// The target and CLI stay private because callers must not bypass Rust admission by supplying
// graph objects detached from the verified portable bytes.
type RustSignoffArtifact struct {
	Bundle        *dagger.File
	ManifestJSON  string
	PayloadDigest string
	Payload       *dagger.File      // +private
	Target        *dagger.Container // +private
	CLI           *dagger.File      // +private
}

type signoffManifestIdentity struct {
	PayloadDigest string `json:"payload_digest"`
}

type verifiedSignoffTarget struct {
	container *dagger.Container
	cli       *dagger.File
	payload   *dagger.File
}

type installedSignoffBaseline struct {
	runner  *dagger.Container
	service *dagger.Service
}

// SignoffArtifact constructs and exports one focused target without starting an engine service.
func (t *RustSdkDev) SignoffArtifact(
	ctx context.Context,
	planJSON string,
) (*RustSignoffArtifact, error) {
	content, err := t.EngineContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("build reusable Rust SDK content: %w", err)
	}
	target := content.Engine.ContainerWithFocusedRustSdkcontent(
		content.Built,
		focusedEngineBaseImage,
		focusedEngineBaseCommit,
		coreTargetRepository,
		coreTargetRevision,
		dagger.DaggerEngineContainerWithFocusedRustSdkcontentOpts{Version: coreTargetVersion},
	)
	payload := target.AsTarball(dagger.ContainerAsTarballOpts{
		ForcedCompression: dagger.ImageLayerCompressionZstd,
	})
	assembler := t.artifactTool(planJSON, payload).WithExec([]string{
		"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
		"--locked", "--", "artifact-build",
		"--plan", signoffPlanPath,
		"--payload", signoffPayloadPath,
		"--bundle-output", signoffBundlePath,
		"--manifest-output", signoffManifestPath,
	})
	manifestJSON, err := assembler.File(signoffManifestPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("assemble exact-target artifact: %w", err)
	}
	var identity signoffManifestIdentity
	if err := json.Unmarshal([]byte(manifestJSON), &identity); err != nil || !isCanonicalSHA256(identity.PayloadDigest) {
		return nil, fmt.Errorf("artifact assembler returned a malformed payload identity")
	}
	return &RustSignoffArtifact{
		Bundle:        assembler.File(signoffBundlePath),
		ManifestJSON:  manifestJSON,
		PayloadDigest: identity.PayloadDigest,
		Payload:       payload,
		Target:        target,
		CLI:           target.File(signoffCliPath),
	}, nil
}

// importSignoffArtifact verifies the complete host bundle before the sole container import site.
// The Import branch deliberately has no access to engine, CLI, Go-runtime, or Rust-content
// builders, so an error cannot fall back to reconstructing the target.
func (t *RustSdkDev) importSignoffArtifact(
	ctx context.Context,
	planJSON string,
	bundle *dagger.File,
) (*verifiedSignoffTarget, error) {
	if bundle == nil {
		return nil, fmt.Errorf("exact-target artifact bundle is required for import")
	}
	verifier := t.artifactTool(planJSON, bundle).WithExec([]string{
		"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
		"--locked", "--", "artifact-import",
		"--plan", signoffPlanPath,
		"--bundle", signoffPayloadPath,
		"--payload-output", signoffImportedPath,
	})
	verifiedPayload := verifier.File(signoffImportedPath)
	imported := dag.Container().Import(verifiedPayload)
	return &verifiedSignoffTarget{
		container: imported,
		cli:       imported.File(signoffCliPath),
		payload:   verifiedPayload,
	}, nil
}

func (t *RustSdkDev) artifactTool(planJSON string, source *dagger.File) *dagger.Container {
	return t.DevContainer(false).
		WithNewFile(signoffPlanPath, planJSON).
		WithMountedFile(signoffPayloadPath, source)
}

// installedRustBaseline owns the sole exact-target service and SDK installation sites.
// Its input type can be produced only after the artifact verifier, so graph construction cannot
// start a service and then discover that the retained bytes belong to another target.
func (target *verifiedSignoffTarget) installedRustBaseline() *installedSignoffBaseline {
	service := target.container.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{
			Args:                     []string{"--addr", "tcp://0.0.0.0:1234"},
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})
	runner := dag.Container().
		From(goHelperImage+"@"+goHelperDigest).
		WithDirectory("/work", dag.Directory()).
		WithWorkdir("/work").
		WithMountedFile(signoffCliPath, target.cli).
		WithEnvVariable("PATH", "/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin").
		WithoutEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN").
		WithServiceBinding(signoffEngineAlias, service).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", signoffEngineEndpoint).
		WithExec([]string{"git", "init"}).
		WithExec([]string{"git", "config", "user.name", "Rust SDK Sign-off"}).
		WithExec([]string{"git", "config", "user.email", "rust-sdk-signoff@dagger.invalid"}).
		WithExec([]string{"git", "commit", "--allow-empty", "-m", "initialize exact baseline"}).
		WithExec([]string{"dagger", "-y", "sdk", "install", "--here", "rust"})
	return &installedSignoffBaseline{runner: runner, service: service}
}

// programBranch derives every mutable coordinate from the reviewed program and attempt while
// retaining the exact CLI, engine service, installed config, and packaged dependency graph.
func (baseline *installedSignoffBaseline) programBranch(
	program signoffmodel.Program,
	attempt uint32,
) (*dagger.Container, error) {
	if attempt == 0 {
		return nil, fmt.Errorf("sign-off case attempt must be one-based")
	}
	spec, ok := signoffmodel.FixedProgramRegistry()[program.Key()]
	if !ok || spec.Program != program {
		return nil, fmt.Errorf("unknown fixed sign-off program %q", program.Key())
	}
	identity := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", program.Key(), attempt)))
	namespace := fmt.Sprintf("%x", identity)
	workspace := "/work/cases/" + namespace
	runner := baseline.runner.
		WithExec([]string{"mkdir", "-p", workspace}).
		WithWorkdir(workspace).
		WithEnvVariable("RUST_SDK_SIGNOFF_PROGRAM", program.Key()).
		WithEnvVariable("RUST_SDK_SIGNOFF_ENVIRONMENT", namespace).
		WithEnvVariable("RUST_SDK_SIGNOFF_SESSION", "session-"+namespace).
		WithEnvVariable("CARGO_TARGET_DIR", "/tmp/cargo-target-"+namespace).
		WithMountedCache(
			"/var/cache/rust-signoff",
			dag.CacheVolume("rust-signoff-"+namespace),
		)
	if spec.Boundary == signoffmodel.BoundaryStableConnector {
		// The distribution path must first try its compiled release. The exact artifact CLI
		// remains discoverable only through PATH for the beta compatibility transition.
		runner = runner.WithoutEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN")
	}
	return runner, nil
}

// stop terminates the sole exact-target service on both success and failed fan-out paths.
func (baseline *installedSignoffBaseline) stop(ctx context.Context) error {
	_, err := baseline.service.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
	return err
}
