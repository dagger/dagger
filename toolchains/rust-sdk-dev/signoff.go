package main

import (
	"context"
	"encoding/json"
	"fmt"

	"dagger/rust-sdk-dev/internal/dagger"
)

const (
	signoffPlanPath       = "/artifact/plan.json"
	signoffPayloadPath    = "/artifact/engine.oci.tar.zst"
	signoffBundlePath     = "/artifact/exact-target.tar"
	signoffManifestPath   = "/artifact/manifest.json"
	signoffImportedPath   = "/artifact/imported-engine.oci.tar.zst"
	signoffCliPath        = "/usr/local/bin/dagger"
	signoffArtifactBinary = "dagger-rust-sdk-signoff"
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
