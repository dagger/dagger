package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"dagger/rust-sdk-dev/internal/dagger"
	signoffmodel "dagger/rust-sdk-dev/internal/signoff"
)

const (
	signoffSeedPath                = "/artifact/seed.json"
	signoffPlanPath                = "/artifact/plan.json"
	signoffCatalogPath             = "/artifact/catalog.json"
	signoffClosurePath             = "/artifact/closure.json"
	signoffPlatformPath            = "/artifact/platform.json"
	signoffAdmissionPath           = "/artifact/facade-admission.json"
	signoffPayloadPath             = "/artifact/engine.oci.tar.zst"
	signoffBundlePath              = "/artifact/exact-target.tar"
	signoffManifestPath            = "/artifact/manifest.json"
	signoffBuildReceiptPath        = "/artifact/build-receipt.json"
	signoffBuildObservationPath    = "/artifact/build-observation.json"
	signoffDependencyPath          = "/artifact/rust-dependency.json"
	signoffRederivedSeedPath       = "/tmp/dagger-rust-sdk-artifact-seed.json"
	signoffImportedPath            = "/artifact/imported-engine.oci.tar.zst"
	signoffImportObservationPath   = "/artifact/import-observation.json"
	signoffImportReceiptPath       = "/artifact/import-receipt.json"
	signoffScanInputPath           = "/scan-input"
	signoffScanOutputPath          = "/artifact/scanner-observation.json"
	signoffSecretInputPath         = "/secret-input"
	signoffSecretSeedPath          = "/run/secrets/rust-signoff-canary-seed"
	signoffSecretOutputPath        = "/artifact/secret-report.json"
	signoffPackagedScanPath        = "/artifact/packaged-scan.json"
	signoffPackagedInputPath       = "/packaged-input"
	signoffEvidenceOutputPath      = "/tmp/signoff-evidence.b64"
	signoffCliPath                 = "/usr/local/bin/dagger"
	signoffEnginePath              = "/usr/local/bin/dagger-engine"
	signoffGoManifestEnv           = "DAGGER_GO_SDK_MANIFEST_DIGEST"
	signoffRustManifestEnv         = "DAGGER_RUST_SDK_MANIFEST_DIGEST"
	signoffRustDescriptorEnv       = "DAGGER_RUST_SDK_DESCRIPTOR_DIGEST"
	signoffRustDependencyEnv       = "DAGGER_RUST_SDK_DEPENDENCY_DESCRIPTOR"
	signoffRustDependencyDigestEnv = "DAGGER_RUST_SDK_DEPENDENCY_DESCRIPTOR_DIGEST"
	signoffArtifactBinary          = "dagger-rust-sdk-signoff"
	signoffEngineAlias             = "dagger-engine"
	signoffEngineEndpoint          = "tcp://dagger-engine:1234"
	signoffDependencyProofPath     = "/work/modules/signoff-dependency/Cargo.toml"
	signoffCleanupTimeout          = 2 * time.Minute
	signoffPackagedOutputMaxBytes  = 256 * 1024 * 1024
	signoffScannerImage            = "aquasec/trivy:0.69.3@sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c"
	signoffDatabaseArtifactDigest  = "sha256:10a3832219beaf45a3eb86065e30b39e528ae9c1650aa5f733d4666afd0712c5"
	signoffDatabaseRepository      = "ghcr.io/aquasecurity/trivy-db@" + signoffDatabaseArtifactDigest
	signoffArtifactDomain          = "dagger-rust-sdk-artifact-v1\x00"
	signoffRepositoryRoot          = "/src"
	signoffHostProfilePath         = "/src/sdk/rust/completeness/signoff-host-profile.json"
	signoffPreflightPath           = "/src/sdk/rust/completeness/evidence/signoff-host-preflight.json"
)

// RustSignoffArtifact is one exportable exact-target bundle and its retained build graph.
// The target and CLI stay private because callers must not bypass Rust admission by supplying
// graph objects detached from the verified portable bytes.
type RustSignoffArtifact struct {
	Bundle           *dagger.File
	PlanJSON         string
	ManifestJSON     string
	BuildReceiptJSON string
	PayloadDigest    string
	Payload          *dagger.File      // +private
	Manifest         *dagger.File      // +private
	BuildReceipt     *dagger.File      // +private
	Target           *dagger.Container // +private
	CLI              *dagger.File      // +private
}

type signoffManifestIdentity struct {
	PayloadDigest string `json:"payload_digest"`
}

type signoffArtifactBuildObservation struct {
	FormatVersion                string            `json:"format_version"`
	Events                       []json.RawMessage `json:"events"`
	ConstructionCount            uint32            `json:"construction_count"`
	ImportCount                  uint32            `json:"import_count"`
	ComponentBuildCounts         map[string]uint32 `json:"component_build_counts"`
	ForbiddenWorkCounts          map[string]uint32 `json:"forbidden_work_counts"`
	MaterializationElapsedMillis uint64            `json:"materialization_elapsed_millis"`
}

type verifiedSignoffTarget struct {
	container           *dagger.Container
	cli                 *dagger.File
	payload             *dagger.File
	manifest            *dagger.File
	importReceipt       *dagger.File
	importReceiptJSON   string
	importReceiptDigest string
	components          signoffmodel.TargetComponentIdentities
}

type signoffArtifactImportObservation struct {
	FormatVersion                string            `json:"format_version"`
	Events                       []json.RawMessage `json:"events"`
	ConstructionCount            uint32            `json:"construction_count"`
	ImportCount                  uint32            `json:"import_count"`
	ComponentBuildCounts         map[string]uint32 `json:"component_build_counts"`
	ForbiddenWorkCounts          map[string]uint32 `json:"forbidden_work_counts"`
	VerifiedComponentDigests     map[string]string `json:"verified_component_digests"`
	MaterializationElapsedMillis uint64            `json:"materialization_elapsed_millis"`
}

type signoffReceiptIdentity struct {
	ReceiptDigest string `json:"receipt_digest"`
}

type installedSignoffBaseline struct {
	initial *dagger.Container
	runner  *dagger.Container
	service *dagger.Service
	cli     *dagger.File
}

type signoffBaselineFacts struct {
	CleanGitWorkspace             bool
	ArtifactCLIOnlyOnPath         bool
	HostCLIVisible                bool
	StaleInstalledConfig          bool
	ServiceStartsBeforeValidation uint32
}

type signoffCanary struct {
	category string
	env      string
	value    string
}

type signoffCanarySet struct {
	seedHex  string
	canaries []signoffCanary
}

type signoffPlanWire struct {
	FormatVersion       string          `json:"format_version"`
	TargetDigest        string          `json:"target_digest"`
	SubjectRevision     string          `json:"subject_revision"`
	CaseCatalogDigest   string          `json:"case_catalog_digest"`
	ClosureBundleDigest string          `json:"closure_bundle_digest"`
	MaximumConcurrency  uint32          `json:"maximum_concurrency"`
	ExpectedExecutions  uint32          `json:"expected_case_executions"`
	ArtifactPlan        json.RawMessage `json:"artifact_plan"`
}

type signoffFacadeAdmissionWire struct {
	ProjectionDigest       string          `json:"projection_digest"`
	FormatVersion          string          `json:"format_version"`
	RunPlanDigest          string          `json:"run_plan_digest"`
	TargetDigest           string          `json:"target_digest"`
	SubjectRevision        string          `json:"subject_revision"`
	Platform               json.RawMessage `json:"platform"`
	HostProfileDigest      string          `json:"host_profile_digest"`
	PreflightDigest        string          `json:"preflight_digest"`
	ArtifactPlan           json.RawMessage `json:"artifact_plan"`
	ArtifactBundleDigest   string          `json:"artifact_bundle_digest"`
	ArtifactManifestDigest string          `json:"artifact_manifest_digest"`
	ArtifactPayloadDigest  string          `json:"artifact_payload_digest"`
	ClosureBundleDigest    string          `json:"closure_bundle_digest"`
	PlatformMatrixDigest   string          `json:"platform_matrix_digest"`
	CaseCatalogDigest      string          `json:"case_catalog_digest"`
	RouteRegistryDigest    string          `json:"route_registry_digest"`
	NetworkPolicies        json.RawMessage `json:"network_policies"`
	MaximumConcurrency     uint32          `json:"maximum_concurrency"`
	ExpectedExecutions     uint32          `json:"expected_case_executions"`
	TotalBudget            uint64          `json:"total_budget"`
	Routes                 json.RawMessage `json:"routes"`
}

type signoffArtifactPlanWire struct {
	Materialization                json.RawMessage                       `json:"materialization"`
	RustDescriptorDigest           string                                `json:"rust_descriptor_digest"`
	RustDependency                 signoffmodel.RustDependencyDescriptor `json:"rust_dependency"`
	RustDependencyDescriptorDigest string                                `json:"rust_dependency_descriptor_digest"`
}

type admittedSignoffInputs struct {
	plan                   signoffPlanWire
	artifactPlanJSON       string
	artifactBundleDigest   string
	artifactManifestDigest string
	artifactPayloadDigest  string
	routes                 []signoffmodel.CaseRoute
	registry               map[string]signoffmodel.ProgramSpec
	executionGroups        []signoffmodel.CaseExecutionGroup
	platformDigest         string
	subject                signoffmodel.ArtifactSubject
	subjectRoot            *dagger.Directory
}

type rawSignoffCase struct {
	CaseID                    string          `json:"case_id"`
	Program                   string          `json:"program"`
	Boundary                  string          `json:"boundary"`
	ExecutionSelector         string          `json:"execution_selector"`
	Executed                  bool            `json:"executed"`
	AttemptOutcomes           []string        `json:"attempt_outcomes"`
	ObservationDigest         string          `json:"observation_digest,omitempty"`
	StandaloneExampleEvidence json.RawMessage `json:"standalone_example_evidence,omitempty"`
	ElapsedMillis             uint64          `json:"elapsed_millis"`
	retainedStdout            string
	retainedStderr            string
	retainedCacheKey          string
	retainedErrors            []signoffErrorEvidence
	retainedWorkspace         *dagger.Directory
	structuredObservation     json.RawMessage
}

type standaloneExampleEvidence struct {
	FixtureDigest       string            `json:"fixture_digest"`
	ResolvedImages      map[string]string `json:"resolved_images"`
	OutputPath          string            `json:"output_path"`
	OutputDigest        string            `json:"output_digest"`
	OutputSizeBytes     int               `json:"output_size_bytes"`
	OutputFormat        string            `json:"output_format"`
	CredentialUses      uint32            `json:"credential_uses"`
	PublicationAttempts uint32            `json:"publication_attempts"`
}

type signoffProcessEvidence struct {
	CaseID  string `json:"case_id"`
	Program string `json:"program"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
}

type signoffErrorsAndDebugEvidence struct {
	Errors []signoffErrorEvidence `json:"errors"`
	Debug  []string               `json:"debug"`
}

type signoffErrorEvidence struct {
	Type  string `json:"type"`
	Error string `json:"error"`
	Debug string `json:"debug"`
}

type signoffCacheEvidence struct {
	CacheKeys  []string        `json:"cache_keys"`
	Provenance json.RawMessage `json:"provenance"`
}

type signoffReportsEvidence struct {
	ArtifactManifest   json.RawMessage `json:"artifact_manifest"`
	ScannerObservation json.RawMessage `json:"scanner_observation"`
	RawScannerEvidence string          `json:"raw_scanner_evidence"`
}

type signoffScannerResult struct {
	observation string
	evidence    []byte
}

type rawSignoffExecution struct {
	Cases         map[string]rawSignoffCase
	ElapsedMillis uint64
	Err           error
}

type rawSignoffFacadeObservation struct {
	FormatVersion                          string                                `json:"format_version"`
	TargetDigest                           string                                `json:"target_digest"`
	SubjectRevision                        string                                `json:"subject_revision"`
	CaseCatalogDigest                      string                                `json:"case_catalog_digest"`
	ClosureBundleDigest                    string                                `json:"closure_bundle_digest"`
	PlatformMatrixDigest                   string                                `json:"platform_matrix_digest"`
	ArtifactManifestDigest                 string                                `json:"artifact_manifest_digest"`
	ArtifactPayloadDigest                  string                                `json:"artifact_payload_digest"`
	ArtifactImportReceipt                  json.RawMessage                       `json:"artifact_import_receipt"`
	ArtifactImportReceiptDigest            string                                `json:"artifact_import_receipt_digest"`
	ScannerResultDigest                    string                                `json:"scanner_result_digest"`
	ScannerObservation                     json.RawMessage                       `json:"scanner_observation"`
	SecretReport                           json.RawMessage                       `json:"secret_report"`
	StableConnector                        json.RawMessage                       `json:"stable_connector"`
	EngineObservationDigest                string                                `json:"engine_observation_digest"`
	BaselineObservationDigest              string                                `json:"baseline_observation_digest"`
	BaselineDirectoryDigest                string                                `json:"baseline_directory_digest"`
	InstalledConfigDigest                  string                                `json:"installed_config_digest"`
	CleanGitWorkspace                      bool                                  `json:"clean_git_workspace"`
	ArtifactCLIOnlyOnPath                  bool                                  `json:"artifact_cli_only_on_path"`
	HostCLIVisible                         bool                                  `json:"host_cli_visible"`
	StaleInstalledConfig                   bool                                  `json:"stale_installed_config"`
	ServiceStartsBeforeValidation          uint32                                `json:"service_starts_before_validation"`
	Dependency                             signoffmodel.DependencyObservation    `json:"dependency"`
	VerifiedComponentDigests               map[string]string                     `json:"verified_component_digests"`
	VerifiedRustDescriptorDigest           string                                `json:"verified_rust_descriptor_digest"`
	VerifiedRustDependency                 signoffmodel.RustDependencyDescriptor `json:"verified_rust_dependency"`
	VerifiedRustDependencyDescriptorDigest string                                `json:"verified_rust_dependency_descriptor_digest"`
	RunnerImageDigest                      string                                `json:"runner_image_digest"`
	ArtifactConstructions                  uint32                                `json:"artifact_constructions"`
	ArtifactImports                        uint32                                `json:"artifact_imports"`
	EngineComponentBuilds                  uint32                                `json:"engine_component_builds"`
	CLIComponentBuilds                     uint32                                `json:"cli_component_builds"`
	GoRuntimeComponentBuilds               uint32                                `json:"go_runtime_component_builds"`
	RustSDKComponentBuilds                 uint32                                `json:"rust_sdk_component_builds"`
	OrchestrationEngineStarts              uint32                                `json:"orchestration_engine_starts"`
	ExactTargetEngineStarts                uint32                                `json:"exact_target_engine_starts"`
	ExactTargetEngineStops                 uint32                                `json:"exact_target_engine_stops"`
	ExactTargetChildReaps                  uint32                                `json:"exact_target_child_reaps"`
	RustBaselineMaterializations           uint32                                `json:"rust_baseline_materializations"`
	CaseExecutions                         uint32                                `json:"case_executions"`
	ClosureReplays                         uint32                                `json:"closure_replays"`
	UnrelatedActions                       uint32                                `json:"unrelated_actions"`
	Cases                                  []rawSignoffCase                      `json:"cases"`
	ArtifactMillis                         uint64                                `json:"artifact_millis"`
	SecurityScanMillis                     uint64                                `json:"security_scan_millis"`
	EngineStartupMillis                    uint64                                `json:"engine_startup_millis"`
	RustInstallationMillis                 uint64                                `json:"rust_installation_millis"`
	CaseExecutionMillis                    uint64                                `json:"case_execution_millis"`
	RunnableExecutionMillis                uint64                                `json:"runnable_execution_millis"`
	CleanupMillis                          uint64                                `json:"cleanup_millis"`
	TotalMillis                            uint64                                `json:"total_millis"`
}

// Signoff runs the complete closed Rust case catalog against one reusable exact-target artifact.
//
// The returned JSON is a raw adapter observation. Rust policy remains solely responsible for
// deriving the atomic verdict and any later status transition.
func (t *RustSdkDev) Signoff(
	ctx context.Context,
	// Canonical Rust-owned immutable run plan.
	planJSON string,
	// Canonical complete case catalog.
	catalogJSON string,
	// Canonical engine-free implementation closure.
	closureJSON string,
	// Canonical current Linux/macOS native-platform evidence set.
	platformJSON string,
	// Previously exported exact-target bundle. Authoritative sign-off is import-only.
	artifact *dagger.File,
) (string, error) {
	if artifact == nil {
		return "", fmt.Errorf("authoritative Rust SDK sign-off requires the retained artifact bundle")
	}
	inputs, err := t.admitSignoffInputs(ctx, planJSON, catalogJSON, closureJSON, platformJSON, artifact)
	if err != nil {
		return "", err
	}
	canaries, err := newSignoffCanarySet()
	if err != nil {
		return "", err
	}
	sourceEvidence, err := collectDirectoryEvidence(
		ctx,
		inputs.subjectRoot.Filter(dagger.DirectoryFilterOpts{Include: []string{
			"toolchains/rust-sdk-dev/testdata/core_conformance.rs",
			"toolchains/rust-sdk-dev/testdata/scenario_conformance.rs",
			"sdk/rust/examples/**",
		}}),
		"exact subject case sources",
	)
	if err != nil {
		return "", err
	}
	artifactEntryEvidence, err := collectArtifactEntryEvidence(ctx, artifact)
	if err != nil {
		return "", err
	}

	source := inputs.subjectRoot.Directory("sdk/rust").
		WithFile(
			"crates/dagger-sdk/examples/signoff_core_conformance.rs",
			inputs.subjectRoot.File("toolchains/rust-sdk-dev/testdata/core_conformance.rs"),
		).
		WithFile(
			"crates/dagger-sdk/examples/signoff_scenario_conformance.rs",
			inputs.subjectRoot.File("toolchains/rust-sdk-dev/testdata/scenario_conformance.rs"),
		)
	artifactStarted := time.Now()
	target, err := t.importSignoffArtifact(ctx, inputs.subject, inputs.artifactPlanJSON, artifact)
	if err != nil {
		return "", err
	}
	manifestContents, err := target.manifest.Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("read admitted exact-target manifest: %w", err)
	}
	var manifest signoffManifestIdentity
	if err := json.Unmarshal([]byte(manifestContents), &manifest); err != nil || !isCanonicalSHA256(manifest.PayloadDigest) {
		return "", fmt.Errorf("admitted exact-target manifest has no canonical payload identity")
	}
	manifestDigest, expectedPayload, err := artifactIdentities(
		inputs.plan.ArtifactPlan,
		[]byte(manifestContents),
		manifest.PayloadDigest,
	)
	if err != nil {
		return "", err
	}
	if manifestDigest != inputs.artifactManifestDigest || expectedPayload != inputs.artifactPayloadDigest {
		return "", fmt.Errorf("imported artifact identities differ from Rust pre-target admission")
	}
	artifactMillis := positiveMillis(time.Since(artifactStarted))

	scanStarted := time.Now()
	scanner, err := t.scanSignoffPayload(ctx, inputs.subjectRoot, target.payload)
	if err != nil {
		return "", err
	}
	var scannerIdentity struct {
		ScannerResultDigest string `json:"scanner_result_digest"`
	}
	if err := json.Unmarshal([]byte(scanner.observation), &scannerIdentity); err != nil || !isCanonicalSHA256(scannerIdentity.ScannerResultDigest) {
		return "", fmt.Errorf("translated exact-payload scan has no canonical result identity")
	}
	scanMillis := positiveMillis(time.Since(scanStarted))

	engineStarted := time.Now()
	baseline := target.installedRustBaseline(source, canaries)
	var cleanupMillis uint64
	cleaned := false
	finishBaseline := func(prior error) error {
		if cleaned {
			return prior
		}
		cleaned = true
		cleanupStarted := time.Now()
		cleanupCtx, cancelCleanup := context.WithTimeout(
			context.WithoutCancel(ctx),
			signoffCleanupTimeout,
		)
		defer cancelCleanup()
		stopErr := baseline.stop(cleanupCtx)
		cleanupMillis = positiveMillis(time.Since(cleanupStarted))
		if prior != nil && stopErr != nil {
			return errors.Join(prior, fmt.Errorf("stop sole exact-target engine: %w", stopErr))
		}
		if prior != nil {
			return prior
		}
		if stopErr != nil {
			return fmt.Errorf("stop sole exact-target engine: %w", stopErr)
		}
		return nil
	}
	baselineFacts, err := baseline.observeInitialFacts(ctx)
	if err != nil {
		return "", finishBaseline(err)
	}
	runtimeVersion, err := baseline.startAndProbe(ctx)
	if err != nil {
		return "", finishBaseline(fmt.Errorf("start and identify sole exact-target engine: %w", err))
	}
	engineStartupMillis := positiveMillis(time.Since(engineStarted))
	installStarted := time.Now()
	installedConfig, err := baseline.runner.File("/work/dagger.toml").Contents(ctx)
	if err != nil {
		return "", finishBaseline(fmt.Errorf("materialize sole installed Rust baseline: %w", err))
	}
	baselineDirectoryDigest, err := baseline.runner.Directory("/work").Digest(ctx)
	if err != nil {
		return "", finishBaseline(fmt.Errorf("identify sole installed Rust baseline: %w", err))
	}
	installedConfigIdentity := sha256.Sum256([]byte(installedConfig))
	installedConfigDigest := fmt.Sprintf("sha256:%x", installedConfigIdentity)
	baselineIdentityInput := make([]byte, 0, len(installedConfigDigest)+len(baselineDirectoryDigest)+10)
	baselineIdentityInput = append(baselineIdentityInput, installedConfigDigest...)
	baselineIdentityInput = append(baselineIdentityInput, 0)
	baselineIdentityInput = append(baselineIdentityInput, baselineDirectoryDigest...)
	baselineIdentityInput = append(baselineIdentityInput, 0,
		boolByte(baselineFacts.CleanGitWorkspace),
		boolByte(baselineFacts.ArtifactCLIOnlyOnPath),
		boolByte(baselineFacts.HostCLIVisible),
		boolByte(baselineFacts.StaleInstalledConfig),
		byte(baselineFacts.ServiceStartsBeforeValidation>>24),
		byte(baselineFacts.ServiceStartsBeforeValidation>>16),
		byte(baselineFacts.ServiceStartsBeforeValidation>>8),
		byte(baselineFacts.ServiceStartsBeforeValidation),
	)
	baselineIdentity := sha256.Sum256(baselineIdentityInput)
	engineIdentity := sha256.Sum256([]byte(inputs.plan.TargetDigest + "\x00" + coreTargetRevision + "\x00" + runtimeVersion + "\x00" + manifestContents))
	var artifactPlan signoffArtifactPlanWire
	if err := json.Unmarshal(inputs.plan.ArtifactPlan, &artifactPlan); err != nil ||
		!isCanonicalSHA256(artifactPlan.RustDescriptorDigest) ||
		!isCanonicalSHA256(artifactPlan.RustDependencyDescriptorDigest) {
		return "", finishBaseline(fmt.Errorf("read admitted Rust dependency descriptor identity"))
	}
	installedDependency, err := baseline.observeInstalledDependency(ctx)
	if err != nil {
		return "", finishBaseline(err)
	}
	if installedDependency.Descriptor != artifactPlan.RustDependency ||
		installedDependency.DescriptorDigest != artifactPlan.RustDependencyDescriptorDigest {
		return "", finishBaseline(fmt.Errorf("installed Rust dependency differs from the admitted artifact"))
	}
	var verifiedRustDependency signoffmodel.RustDependencyDescriptor
	if err := json.Unmarshal([]byte(target.components.RustDependency), &verifiedRustDependency); err != nil {
		return "", finishBaseline(fmt.Errorf("decode imported Rust dependency descriptor: %w", err))
	}
	generatedEvidence, err := collectDirectoryEvidence(
		ctx,
		baseline.runner.Directory("/work").Filter(dagger.DirectoryFilterOpts{Exclude: []string{
			".git/**",
			"cases/**",
		}}),
		"installed generated and packaged Rust files",
	)
	if err != nil {
		return "", finishBaseline(err)
	}
	installationMillis := positiveMillis(time.Since(installStarted))

	executionGroups := inputs.executionGroups
	programs := make([]signoffmodel.ScheduledValue[signoffmodel.Program], len(executionGroups))
	groupByProgram := make(map[string]int, len(executionGroups))
	for index, group := range executionGroups {
		programs[index] = signoffmodel.ScheduledValue[signoffmodel.Program]{
			Value: group.Representative.Program,
			Class: group.Representative.Policy.Concurrency,
		}
		groupByProgram[group.Representative.Program.Key()] = index
	}
	results, err := signoffmodel.ExecutePolicyBounded(ctx, programs, int(inputs.plan.MaximumConcurrency), func(runCtx context.Context, program signoffmodel.Program) rawSignoffExecution {
		group := executionGroups[groupByProgram[program.Key()]]
		return runSignoffExecutionGroup(runCtx, baseline, group, inputs.registry)
	})
	if err != nil {
		return "", finishBaseline(fmt.Errorf("execute bounded sign-off catalog: %w", err))
	}
	casePosition := make(map[string]int, len(inputs.routes))
	for index, route := range inputs.routes {
		casePosition[route.CaseID] = index
	}
	cases := make([]rawSignoffCase, len(inputs.routes))
	var runnableMillis uint64
	for _, result := range results {
		group := executionGroups[result.Index]
		if result.Value.Err != nil {
			return "", finishBaseline(fmt.Errorf("execute reviewed Rust case group %q: %w", group.Representative.CaseID, result.Value.Err))
		}
		runnableMillis += result.Value.ElapsedMillis
		if len(result.Value.Cases) != len(group.Members) {
			return "", finishBaseline(fmt.Errorf("reviewed Rust execution returned %d case results, want %d", len(result.Value.Cases), len(group.Members)))
		}
		for _, member := range group.Members {
			observation, ok := result.Value.Cases[member.CaseID]
			if !ok {
				return "", finishBaseline(fmt.Errorf("reviewed Rust execution omitted case %q", member.CaseID))
			}
			cases[casePosition[member.CaseID]] = observation
		}
	}
	var caseMillis uint64
	processEvidence := make([]signoffProcessEvidence, 0, len(executionGroups))
	cacheKeys := make([]string, 0, len(executionGroups))
	debugEvidence := make([]string, 0, len(cases))
	errorEvidence := make([]signoffErrorEvidence, 0)
	var stableConnector json.RawMessage
	packagedWorkspaces := make(map[string]*dagger.Directory, 3)
	for _, observation := range cases {
		caseMillis += observation.ElapsedMillis
		if observation.Executed {
			if observation.retainedCacheKey == "" {
				return "", finishBaseline(fmt.Errorf("sign-off case %q omitted its actual cache key", observation.CaseID))
			}
			processEvidence = append(processEvidence, signoffProcessEvidence{
				CaseID: observation.CaseID, Program: observation.Program,
				Stdout: observation.retainedStdout, Stderr: observation.retainedStderr,
			})
			cacheKeys = append(cacheKeys, observation.retainedCacheKey)
		}
		debugEvidence = append(debugEvidence, fmt.Sprintf("%#v", observation.AttemptOutcomes))
		errorEvidence = append(errorEvidence, observation.retainedErrors...)
		if observation.Program == string(signoffmodel.ProgramStableConnector) && len(observation.structuredObservation) != 0 {
			if len(stableConnector) != 0 {
				return "", finishBaseline(fmt.Errorf("sign-off returned duplicate stable connector evidence"))
			}
			stableConnector = append(json.RawMessage(nil), observation.structuredObservation...)
		}
		if strings.HasPrefix(observation.Program, string(signoffmodel.ProgramStandaloneExample)+"/") && observation.Executed {
			if observation.retainedWorkspace == nil {
				return "", finishBaseline(fmt.Errorf("standalone example %q omitted its actual executed workspace", observation.Program))
			}
			if _, duplicate := packagedWorkspaces[observation.Program]; duplicate {
				return "", finishBaseline(fmt.Errorf("standalone example %q returned duplicate workspaces", observation.Program))
			}
			packagedWorkspaces[observation.Program] = observation.retainedWorkspace
		}
	}
	if len(stableConnector) == 0 {
		return "", finishBaseline(fmt.Errorf("sign-off omitted structured stable connector evidence"))
	}
	packagedScan, err := t.packagedSignoffScan(ctx, inputs.subjectRoot, canaries, packagedWorkspaces)
	if err != nil {
		return "", finishBaseline(err)
	}

	if err := finishBaseline(nil); err != nil {
		return "", err
	}

	observation := rawSignoffFacadeObservation{
		FormatVersion: "1.0.0", TargetDigest: inputs.plan.TargetDigest,
		SubjectRevision: inputs.plan.SubjectRevision, CaseCatalogDigest: inputs.plan.CaseCatalogDigest,
		ClosureBundleDigest: inputs.plan.ClosureBundleDigest, PlatformMatrixDigest: inputs.platformDigest,
		ArtifactManifestDigest: manifestDigest, ArtifactPayloadDigest: expectedPayload,
		ArtifactImportReceipt:         json.RawMessage(target.importReceiptJSON),
		ArtifactImportReceiptDigest:   target.importReceiptDigest,
		ScannerResultDigest:           scannerIdentity.ScannerResultDigest,
		ScannerObservation:            json.RawMessage(scanner.observation),
		SecretReport:                  json.RawMessage("null"),
		StableConnector:               stableConnector,
		EngineObservationDigest:       fmt.Sprintf("sha256:%x", engineIdentity),
		BaselineObservationDigest:     fmt.Sprintf("sha256:%x", baselineIdentity),
		BaselineDirectoryDigest:       baselineDirectoryDigest,
		InstalledConfigDigest:         installedConfigDigest,
		CleanGitWorkspace:             baselineFacts.CleanGitWorkspace,
		ArtifactCLIOnlyOnPath:         baselineFacts.ArtifactCLIOnlyOnPath,
		HostCLIVisible:                baselineFacts.HostCLIVisible,
		StaleInstalledConfig:          baselineFacts.StaleInstalledConfig,
		ServiceStartsBeforeValidation: baselineFacts.ServiceStartsBeforeValidation,
		Dependency:                    installedDependency,
		VerifiedComponentDigests: map[string]string{
			"engine":     target.components.Engine,
			"cli":        target.components.CLI,
			"go-runtime": target.components.GoRuntime,
			"rust-sdk":   target.components.RustSDK,
		},
		VerifiedRustDescriptorDigest:           target.components.RustDescriptor,
		VerifiedRustDependency:                 verifiedRustDependency,
		VerifiedRustDependencyDescriptorDigest: target.components.RustDependencyDigest,
		RunnerImageDigest:                      rustSdkImageDigest,
		ArtifactConstructions:                  0, ArtifactImports: 1,
		EngineComponentBuilds: 0, CLIComponentBuilds: 0,
		GoRuntimeComponentBuilds: 0, RustSDKComponentBuilds: 0,
		OrchestrationEngineStarts: 1, ExactTargetEngineStarts: 1, ExactTargetEngineStops: 1,
		ExactTargetChildReaps:        1,
		RustBaselineMaterializations: 1, CaseExecutions: uint32(len(executionGroups)),
		ClosureReplays: 0, UnrelatedActions: 0, Cases: cases,
		ArtifactMillis: artifactMillis, SecurityScanMillis: scanMillis,
		EngineStartupMillis: engineStartupMillis, RustInstallationMillis: installationMillis,
		CaseExecutionMillis: caseMillis, RunnableExecutionMillis: runnableMillis,
		CleanupMillis: cleanupMillis,
	}
	casesEvidence, err := json.Marshal(cases)
	if err != nil {
		return "", fmt.Errorf("encode retained case diagnostics: %w", err)
	}
	draftBytes, err := json.Marshal(observation)
	if err != nil {
		return "", fmt.Errorf("encode draft Rust sign-off observation: %w", err)
	}
	draft, err := canonicalizeJSON(draftBytes)
	if err != nil {
		return "", fmt.Errorf("canonicalize draft Rust sign-off observation: %w", err)
	}
	provenance, err := inputs.subjectRoot.File("sdk/rust/completeness/security-provenance.json").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("read retained security provenance: %w", err)
	}
	processBytes, err := json.Marshal(processEvidence)
	if err != nil {
		return "", fmt.Errorf("encode retained stdout and stderr: %w", err)
	}
	errorsAndDebug, err := json.Marshal(signoffErrorsAndDebugEvidence{
		Errors: errorEvidence,
		Debug:  debugEvidence,
	})
	if err != nil {
		return "", fmt.Errorf("encode retained errors and Debug output: %w", err)
	}
	cacheAndProvenance, err := json.Marshal(signoffCacheEvidence{
		CacheKeys:  cacheKeys,
		Provenance: json.RawMessage(provenance),
	})
	if err != nil {
		return "", fmt.Errorf("encode actual cache keys and provenance: %w", err)
	}
	reports, err := json.Marshal(signoffReportsEvidence{
		ArtifactManifest:   json.RawMessage(manifestContents),
		ScannerObservation: json.RawMessage(scanner.observation),
		RawScannerEvidence: string(scanner.evidence),
	})
	if err != nil {
		return "", fmt.Errorf("encode retained sign-off reports: %w", err)
	}
	secretStarted := time.Now()
	secretReport, err := t.secretSignoffReport(ctx, inputs.subjectRoot, canaries, packagedScan, signoffmodel.SecretEvidenceDomains{
		SourceFiles:            sourceEvidence,
		GeneratedPackagedFiles: generatedEvidence,
		ArtifactEntries:        artifactEntryEvidence,
		CacheAndProvenance:     cacheAndProvenance,
		ProcessOutput:          processBytes,
		ErrorsAndDebug:         errorsAndDebug,
		DiagnosticsAndTraces:   casesEvidence,
		Reports:                reports,
		DraftVerdict:           []byte(draft),
	})
	if err != nil {
		return "", err
	}
	observation.SecretReport = json.RawMessage(secretReport)
	observation.SecurityScanMillis += positiveMillis(time.Since(secretStarted))
	observation.TotalMillis = observation.ArtifactMillis + observation.SecurityScanMillis +
		observation.EngineStartupMillis + observation.RustInstallationMillis +
		observation.CaseExecutionMillis + observation.CleanupMillis
	encoded, err := json.Marshal(observation)
	if err != nil {
		return "", fmt.Errorf("encode raw Rust sign-off observation: %w", err)
	}
	canonical, err := canonicalizeJSON(encoded)
	if err != nil {
		return "", fmt.Errorf("canonicalize raw Rust sign-off observation: %w", err)
	}
	return canonical, nil
}

func runSignoffExecutionGroup(
	ctx context.Context,
	baseline *installedSignoffBaseline,
	group signoffmodel.CaseExecutionGroup,
	registry map[string]signoffmodel.ProgramSpec,
) rawSignoffExecution {
	result := rawSignoffExecution{Cases: make(map[string]rawSignoffCase, len(group.Members))}
	representative := group.Representative
	attempts, attemptErr := signoffmodel.ExecutePolicyAttempts(ctx, representative.Policy, func(attemptCtx context.Context, attempt uint32) (signoffGroupAttempt, error) {
		return executeSignoffGroupAttempt(attemptCtx, baseline, group, registry, attempt)
	})
	if attemptErr != nil || len(attempts) == 0 {
		result.Err = attemptErr
		if attemptErr != nil {
			return result
		}
		for _, member := range group.Members {
			result.Cases[member.CaseID] = failedRawSignoffCase(member, registry[member.Program.Key()], "assertion-failed")
		}
		result.ElapsedMillis = 1
		return result
	}
	outcomes := make([]string, 0, len(attempts))
	var elapsed uint64
	retainedStdout := make([]string, 0, len(attempts))
	retainedStderr := make([]string, 0, len(attempts))
	retainedCacheKeys := make([]string, 0, len(attempts))
	retainedErrors := make([]signoffErrorEvidence, 0)
	for _, attempt := range attempts {
		outcomes = append(outcomes, rawAttemptOutcome(attempt.Outcome))
		elapsed += positiveMillis(attempt.Elapsed)
		retainedStdout = append(retainedStdout, attempt.Value.retainedStdout)
		retainedStderr = append(retainedStderr, attempt.Value.retainedStderr)
		retainedCacheKeys = append(retainedCacheKeys, attempt.Value.retainedCacheKey)
		if attempt.Err != nil {
			retainedErrors = append(retainedErrors, signoffErrorEvidence{
				Type: fmt.Sprintf("%T", attempt.Err), Error: attempt.Err.Error(),
				Debug: fmt.Sprintf("%#v", attempt.Err),
			})
		}
	}
	final := attempts[len(attempts)-1]
	for index, member := range group.Members {
		memberSpec := registry[member.Program.Key()]
		observation := rawSignoffCase{
			CaseID: member.CaseID, Program: member.Program.Key(), Boundary: string(memberSpec.Boundary),
			Executed: index == 0, AttemptOutcomes: append([]string(nil), outcomes...), ElapsedMillis: elapsed,
		}
		if memberSpec.Executor != nil {
			observation.ExecutionSelector = memberSpec.Executor.Selector
		}
		if final.Outcome.Kind == signoffmodel.OutcomePassed {
			if digest, ok := final.Value.observations[member.CaseID]; ok {
				observation.ObservationDigest = digest
				if structured, present := final.Value.structuredObservations[member.CaseID]; present {
					observation.structuredObservation = append(json.RawMessage(nil), structured...)
					if member.Program.Kind == signoffmodel.ProgramStandaloneExample {
						observation.StandaloneExampleEvidence = append(json.RawMessage(nil), structured...)
					}
				}
			} else {
				// The reviewed executor must return one observation for every grouped row.
				// Preserve a terminal assertion rather than manufacturing a passing digest.
				observation.AttemptOutcomes[len(observation.AttemptOutcomes)-1] = "assertion-failed"
			}
		}
		if index == 0 {
			observation.retainedStdout = strings.Join(retainedStdout, "\n")
			observation.retainedStderr = strings.Join(retainedStderr, "\n")
			observation.retainedCacheKey = strings.Join(retainedCacheKeys, "\n")
			observation.retainedErrors = append([]signoffErrorEvidence(nil), retainedErrors...)
			observation.retainedWorkspace = final.Value.retainedWorkspace
		}
		result.Cases[member.CaseID] = observation
	}
	result.ElapsedMillis = elapsed
	return result
}

type signoffGroupAttempt struct {
	observations           map[string]string
	structuredObservations map[string]json.RawMessage
	retainedStdout         string
	retainedStderr         string
	retainedCacheKey       string
	retainedWorkspace      *dagger.Directory
}

func executeSignoffGroupAttempt(
	ctx context.Context,
	baseline *installedSignoffBaseline,
	group signoffmodel.CaseExecutionGroup,
	registry map[string]signoffmodel.ProgramSpec,
	attempt uint32,
) (signoffGroupAttempt, error) {
	representative := group.Representative
	spec := registry[representative.Program.Key()]
	if spec.Executor == nil {
		return signoffGroupAttempt{}, fmt.Errorf("sign-off program %q has no concrete production executor", representative.Program.Key())
	}
	if spec.Executor.Kind != signoffmodel.ExecutorScenarioConformance && len(group.Members) != 1 {
		return signoffGroupAttempt{}, fmt.Errorf("non-scenario executor %q cannot prove grouped cases", spec.Executor.Selector)
	}
	runner, cacheKey, err := baseline.programBranch(representative.Program, spec, representative.Policy, attempt)
	if err != nil {
		return signoffGroupAttempt{}, err
	}
	if spec.Executor.Kind == signoffmodel.ExecutorScenarioConformance {
		observations, structured, stdout, stderr, err := runScenarioConformanceCase(ctx, runner, *spec.Executor, group.Members, registry)
		return signoffGroupAttempt{
			observations: observations, structuredObservations: structured,
			retainedStdout: stdout, retainedStderr: stderr, retainedCacheKey: cacheKey,
		}, err
	}
	digest, structured, stdout, stderr, workspace, err := runSignoffCaseAttempt(
		ctx, runner, representative, spec,
	)
	structuredObservations := make(map[string]json.RawMessage)
	if len(structured) != 0 {
		structuredObservations[representative.CaseID] = structured
	}
	return signoffGroupAttempt{
		observations:           map[string]string{representative.CaseID: digest},
		structuredObservations: structuredObservations,
		retainedStdout:         stdout, retainedStderr: stderr, retainedCacheKey: cacheKey,
		retainedWorkspace: workspace,
	}, err
}

func rawAttemptOutcome(outcome signoffmodel.AttemptOutcome) string {
	switch outcome.Kind {
	case signoffmodel.OutcomePassed:
		return "passed"
	case signoffmodel.OutcomeInfrastructure:
		switch outcome.InfrastructureClass {
		case signoffmodel.FailureOrchestrationTransport:
			return "orchestration-transport"
		case signoffmodel.FailureImmutableRemoteFetch:
			return "immutable-remote-fetch"
		case signoffmodel.FailureRunnerCapacity:
			return "runner-capacity"
		}
	}
	return "assertion-failed"
}

func failedRawSignoffCase(route signoffmodel.CaseRoute, spec signoffmodel.ProgramSpec, outcome string) rawSignoffCase {
	return rawSignoffCase{
		CaseID: route.CaseID, Program: route.Program.Key(), Boundary: string(spec.Boundary),
		AttemptOutcomes: []string{outcome}, ElapsedMillis: 1,
	}
}

func (t *RustSdkDev) admitSignoffInputs(
	ctx context.Context,
	planJSON, catalogJSON, closureJSON, platformJSON string,
	artifact *dagger.File,
) (*admittedSignoffInputs, error) {
	if artifact == nil {
		return nil, fmt.Errorf("retained artifact bundle is required for pre-target admission")
	}
	for name, value := range map[string]string{
		"plan": planJSON, "catalog": catalogJSON, "closure": closureJSON, "platform": platformJSON,
	} {
		if err := requireCanonicalJSON([]byte(value)); err != nil {
			return nil, fmt.Errorf("%s sign-off input is not canonical: %w", name, err)
		}
	}
	subject, err := signoffmodel.AdmitArtifactPlanSubject(t.EngineRepository, []byte(planJSON))
	if err != nil {
		return nil, err
	}
	subjectRoot := dag.Git(subject.Repository).
		Commit(subject.Revision).
		Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	admitter := rustBaseContainer().
		WithDirectory(signoffRepositoryRoot, subjectRoot).
		WithWorkdir(signoffRepositoryRoot+"/sdk/rust").
		WithNewFile(signoffPlanPath, planJSON).
		WithNewFile(signoffCatalogPath, catalogJSON).
		WithNewFile(signoffClosurePath, closureJSON).
		WithNewFile(signoffPlatformPath, platformJSON).
		WithMountedFile(signoffBundlePath, artifact).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
			"--locked", "--", "facade-admit",
			"--root", signoffRepositoryRoot,
			"--plan", signoffPlanPath,
			"--bundle", signoffBundlePath,
			"--catalog", signoffCatalogPath,
			"--closure", signoffClosurePath,
			"--platform", signoffPlatformPath,
			"--host-profile", signoffHostProfilePath,
			"--preflight", signoffPreflightPath,
			"--output", signoffAdmissionPath,
		})
	projectionJSON, err := admitter.File(signoffAdmissionPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("run Rust-owned pre-target sign-off admission: %w", err)
	}
	if err := requireCanonicalJSON([]byte(projectionJSON)); err != nil {
		return nil, fmt.Errorf("Rust facade admission projection is not canonical: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(projectionJSON))
	decoder.DisallowUnknownFields()
	var admission signoffFacadeAdmissionWire
	if err := decoder.Decode(&admission); err != nil {
		return nil, fmt.Errorf("decode Rust facade admission projection: %w", err)
	}
	var suffix any
	if err := decoder.Decode(&suffix); err != io.EOF {
		return nil, fmt.Errorf("Rust facade admission projection has trailing data")
	}
	for name, digest := range map[string]string{
		"projection":        admission.ProjectionDigest,
		"run plan":          admission.RunPlanDigest,
		"target":            admission.TargetDigest,
		"host profile":      admission.HostProfileDigest,
		"preflight":         admission.PreflightDigest,
		"artifact bundle":   admission.ArtifactBundleDigest,
		"artifact manifest": admission.ArtifactManifestDigest,
		"artifact payload":  admission.ArtifactPayloadDigest,
		"closure":           admission.ClosureBundleDigest,
		"platform matrix":   admission.PlatformMatrixDigest,
		"case catalog":      admission.CaseCatalogDigest,
		"route registry":    admission.RouteRegistryDigest,
	} {
		if !isCanonicalSHA256(digest) {
			return nil, fmt.Errorf("Rust facade admission returned malformed %s identity", name)
		}
	}
	if admission.FormatVersion != "1.0.0" || len(admission.SubjectRevision) != 40 ||
		admission.SubjectRevision != subject.Revision ||
		admission.MaximumConcurrency == 0 || admission.MaximumConcurrency > 64 ||
		admission.ExpectedExecutions == 0 || admission.TotalBudget == 0 ||
		len(admission.Platform) == 0 || len(admission.NetworkPolicies) == 0 || len(admission.Routes) == 0 {
		return nil, fmt.Errorf("Rust facade admission projection is incomplete")
	}
	artifactPlanJSON, err := canonicalizeJSON(admission.ArtifactPlan)
	if err != nil {
		return nil, fmt.Errorf("canonicalize admitted artifact plan: %w", err)
	}
	if err := validateMaterialization(admission.ArtifactPlan, true); err != nil {
		return nil, err
	}
	plan := signoffPlanWire{
		FormatVersion: admission.FormatVersion, TargetDigest: admission.TargetDigest,
		SubjectRevision: admission.SubjectRevision, CaseCatalogDigest: admission.CaseCatalogDigest,
		ClosureBundleDigest: admission.ClosureBundleDigest,
		MaximumConcurrency:  admission.MaximumConcurrency, ExpectedExecutions: admission.ExpectedExecutions,
		ArtifactPlan: admission.ArtifactPlan,
	}
	observableJSON, err := subjectRoot.File("sdk/rust/completeness/conformance-observable-programs.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read closed observable sign-off registry: %w", err)
	}
	observable, err := signoffmodel.DecodeObservablePrograms([]byte(observableJSON))
	if err != nil {
		return nil, err
	}
	if observable.CaseCatalogDigest != admission.CaseCatalogDigest {
		return nil, fmt.Errorf("observable registry and run plan name different case catalogs")
	}
	scenarioCandidates, err := subjectRoot.File("sdk/rust/completeness/conformance-scenario-candidates.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Rust scenario candidate queue: %w", err)
	}
	scenarioRegistry, err := subjectRoot.File("sdk/rust/completeness/conformance-scenario-realizations.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Rust scenario realization registry: %w", err)
	}
	scenarioRunner, err := subjectRoot.File("toolchains/rust-sdk-dev/testdata/scenario_conformance.rs").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Rust scenario runner source: %w", err)
	}
	realizations, err := signoffmodel.DecodeScenarioRealizations(
		[]byte(scenarioRegistry), []byte(scenarioCandidates), []byte(scenarioRunner),
	)
	if err != nil {
		return nil, err
	}
	registry, err := signoffmodel.CompleteProgramRegistry(observable)
	if err != nil {
		return nil, err
	}
	registry, err = signoffmodel.ApplyScenarioRealizations(registry, observable, realizations)
	if err != nil {
		return nil, err
	}
	if err := signoffmodel.RequireConcretePrograms(registry); err != nil {
		return nil, err
	}
	routes, err := signoffmodel.DecodeFacadeAdmissionRoutes(admission.Routes, registry)
	if err != nil {
		return nil, err
	}
	executionGroups, err := signoffmodel.GroupCaseExecutions(routes, registry)
	if err != nil {
		return nil, fmt.Errorf("group reviewed Rust case executions: %w", err)
	}
	if uint32(len(executionGroups)) != plan.ExpectedExecutions {
		return nil, fmt.Errorf(
			"run plan declares %d Rust executions but the registry requires %d",
			plan.ExpectedExecutions,
			len(executionGroups),
		)
	}
	return &admittedSignoffInputs{
		plan: plan, artifactPlanJSON: artifactPlanJSON,
		artifactBundleDigest:   admission.ArtifactBundleDigest,
		artifactManifestDigest: admission.ArtifactManifestDigest,
		artifactPayloadDigest:  admission.ArtifactPayloadDigest,
		routes:                 routes, registry: registry, executionGroups: executionGroups,
		platformDigest: admission.PlatformMatrixDigest,
		subject:        subject, subjectRoot: subjectRoot,
	}, nil
}

func runSignoffCaseAttempt(
	ctx context.Context,
	runner *dagger.Container,
	route signoffmodel.CaseRoute,
	spec signoffmodel.ProgramSpec,
) (string, json.RawMessage, string, string, *dagger.Directory, error) {
	program := route.Program
	executor := spec.Executor
	if executor == nil {
		return "", nil, "", "", nil, fmt.Errorf("sign-off program %q has no concrete production executor", program.Key())
	}
	if executor.Expected.Category == "" || executor.Expected.Operation == "" || executor.Selector != program.Value {
		return "", nil, "", "", nil, fmt.Errorf("sign-off program %q has an incomplete concrete executor", program.Key())
	}
	switch executor.Kind {
	case signoffmodel.ExecutorCoreConformance:
		digest, stdout, stderr, err := runCoreConformanceCase(ctx, runner, *executor)
		return digest, nil, stdout, stderr, nil, err
	case signoffmodel.ExecutorEngineIntegration:
		digest, stdout, stderr, err := runEngineIntegrationSignoffCase(ctx, runner, *executor)
		return digest, nil, stdout, stderr, nil, err
	case signoffmodel.ExecutorStandaloneExample:
		return runStandaloneExampleSignoffCase(ctx, runner, route, *executor)
	case signoffmodel.ExecutorScenarioConformance:
		return "", nil, "", "", nil, fmt.Errorf("scenario conformance requires its complete reviewed execution group")
	default:
		return "", nil, "", "", nil, fmt.Errorf("sign-off program %q names unknown executor %q", program.Key(), executor.Kind)
	}
}

func readSignoffProcessEvidence(ctx context.Context, executed *dagger.Container) (string, string, error) {
	stdout, stdoutErr := executed.Stdout(ctx)
	stderr, stderrErr := executed.Stderr(ctx)
	if stdoutErr != nil || stderrErr != nil {
		return stdout, stderr, errors.Join(stdoutErr, stderrErr)
	}
	return stdout, stderr, nil
}

func runStandaloneExampleSignoffCase(
	ctx context.Context,
	runner *dagger.Container,
	route signoffmodel.CaseRoute,
	executor signoffmodel.ExecutorDefinition,
) (string, json.RawMessage, string, string, *dagger.Directory, error) {
	program := route.Program
	if executor.Expected.Category != "build-only-output" || executor.Expected.Operation != executor.Selector ||
		executor.Selector != "standalone-example/"+program.Value {
		return "", nil, "", "", nil, fmt.Errorf("standalone example executor %q has a mismatched expected observation", executor.Selector)
	}
	var command []string
	var outputPath string
	var outputFormat string
	var expectedImages map[string]struct{}
	switch program.Value {
	case "cli":
		command = []string{"cargo", "run", "--locked", "--manifest-path", "/src/sdk/rust/examples/cli/Cargo.toml"}
		outputPath = "build/cli"
		outputFormat = "executable"
		expectedImages = map[string]struct{}{"rust:1.97.1-slim-bookworm": {}}
	case "backend":
		command = []string{"cargo", "run", "--locked", "--manifest-path", "/src/sdk/rust/examples/backend/Cargo.toml", "--", "build", "--signoff-export"}
		outputPath = "build/backend-image.tar"
		outputFormat = "oci-gzip"
		expectedImages = map[string]struct{}{
			"gcr.io/distroless/static-debian12": {}, "rust:1.97.1-alpine3.22": {},
		}
	case "frontend":
		command = []string{"cargo", "run", "--locked", "--manifest-path", "/src/sdk/rust/examples/frontend/Cargo.toml", "--", "build", "--signoff-export"}
		outputPath = "build/frontend-image.tar"
		outputFormat = "oci-gzip"
		expectedImages = map[string]struct{}{
			"nginx:1.24.0-alpine3.17": {}, "rust:1.97.1": {},
		}
	default:
		return "", nil, "", "", nil, fmt.Errorf("unknown standalone example %q", program.Value)
	}
	executed := runner.WithExec(command)
	stdout, stderr, err := readSignoffProcessEvidence(ctx, executed)
	if err != nil {
		return "", nil, stdout, stderr, nil, err
	}
	output := executed.File(outputPath)
	size, err := output.Size(ctx)
	if err != nil {
		return "", nil, stdout, stderr, nil, fmt.Errorf("standalone example %q omitted %s: %w", program.Value, outputPath, err)
	}
	if size <= 0 || size > 256*1024*1024 {
		return "", nil, stdout, stderr, nil, fmt.Errorf("standalone example %q output size %d is outside its reviewed bound", program.Value, size)
	}
	outputDigest, err := identifySignoffFile(ctx, output)
	if err != nil {
		return "", nil, stdout, stderr, nil, err
	}
	resolvedImages, err := signoffmodel.ParseStandaloneResolvedImages(stdout, expectedImages)
	if err != nil {
		return "", nil, stdout, stderr, nil, fmt.Errorf("standalone example %q: %w", program.Value, err)
	}
	evidence := standaloneExampleEvidence{
		FixtureDigest: route.FixtureDigest, ResolvedImages: resolvedImages,
		OutputPath: outputPath, OutputDigest: outputDigest, OutputSizeBytes: size,
		OutputFormat: outputFormat, CredentialUses: 0, PublicationAttempts: 0,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return "", nil, stdout, stderr, nil, fmt.Errorf("encode standalone example evidence: %w", err)
	}
	canonical, err := canonicalizeJSON(encoded)
	if err != nil {
		return "", nil, stdout, stderr, nil, fmt.Errorf("canonicalize standalone example evidence: %w", err)
	}
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("sha256:%x", digest), json.RawMessage(canonical), stdout, stderr, executed.Directory("."), nil
}

func identifySignoffFile(ctx context.Context, file *dagger.File) (string, error) {
	const evidencePath = "/evidence/output"
	checksum, err := rustBaseContainer().
		WithMountedFile(evidencePath, file).
		WithExec([]string{"sha256sum", evidencePath}).
		Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("identify retained standalone output bytes: %w", err)
	}
	fields := strings.Fields(checksum)
	if len(fields) != 2 || fields[1] != evidencePath {
		return "", fmt.Errorf("retained standalone output checksum is malformed")
	}
	digest := "sha256:" + fields[0]
	if !isCanonicalSHA256(digest) {
		return "", fmt.Errorf("retained standalone output checksum is not canonical SHA-256")
	}
	return digest, nil
}

type scenarioConformanceObservation struct {
	CaseID          string `json:"case_id"`
	ContractDigest  string `json:"contract_digest"`
	ProofID         string `json:"proof_id"`
	RealizationID   string `json:"realization_id"`
	RealizationKind string `json:"realization_kind"`
	Observation     string `json:"observation"`
}

type scenarioConformanceContract struct {
	CaseID         string `json:"case_id"`
	ContractDigest string `json:"contract_digest"`
	ProofID        string `json:"proof_id"`
}

type scenarioConformanceObservationSet struct {
	FormatVersion  uint32                           `json:"format_version"`
	TargetRevision string                           `json:"target_revision"`
	TargetVersion  string                           `json:"target_version"`
	Observations   []scenarioConformanceObservation `json:"observations"`
}

func runScenarioConformanceCase(
	ctx context.Context,
	runner *dagger.Container,
	executor signoffmodel.ExecutorDefinition,
	members []signoffmodel.CaseRoute,
	registry map[string]signoffmodel.ProgramSpec,
) (map[string]string, map[string]json.RawMessage, string, string, error) {
	contracts := make([]scenarioConformanceContract, 0, len(members))
	expectedContracts := make(map[string]string, len(members))
	for _, member := range members {
		spec := registry[member.Program.Key()]
		contractDigest := spec.Executor.ContractDigest
		proofID := spec.Executor.ProofID
		if contractDigest == "" {
			digest := sha256.Sum256([]byte("dagger-rust-sdk-fixed-case-contract-v1\x00" + member.CaseID + "\x00" + spec.Executor.Expected.Category + "\x00" + spec.Executor.Expected.Operation))
			contractDigest = fmt.Sprintf("sha256:%x", digest)
			proofID = "probe/fixed/" + strings.ReplaceAll(member.Program.Key(), "/", "-")
		}
		contracts = append(contracts, scenarioConformanceContract{CaseID: member.CaseID, ContractDigest: contractDigest, ProofID: proofID})
		expectedContracts[member.CaseID] = contractDigest + "\x00" + proofID
	}
	encodedContracts, err := json.Marshal(contracts)
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("encode selected Rust scenario contracts: %w", err)
	}
	executed := runner.
		WithEnvVariable("DAGGER_RUST_SCENARIO_REALIZATION", executor.Selector).
		WithEnvVariable("DAGGER_RUST_SCENARIO_CONTRACTS", string(encodedContracts)).
		WithExec([]string{
			"dagger", "run", "cargo", "run", "--manifest-path", "/src/sdk/rust/Cargo.toml",
			"-p", "dagger-sdk", "--features", "signoff-observation",
			"--example", "signoff_scenario_conformance", "--locked",
		})
	stdout, stderr, err := readSignoffProcessEvidence(ctx, executed)
	if err != nil {
		return nil, nil, stdout, stderr, err
	}
	var evidence scenarioConformanceObservationSet
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &evidence); err != nil {
		return nil, nil, stdout, stderr, fmt.Errorf("decode selected Rust scenario observation: %w", err)
	}
	if evidence.FormatVersion != 1 || evidence.TargetRevision != coreTargetRevision || evidence.TargetVersion != coreTargetVersion {
		return nil, nil, stdout, stderr, fmt.Errorf("selected Rust scenario observation names a different exact target")
	}
	if len(evidence.Observations) != len(expectedContracts) {
		return nil, nil, stdout, stderr, fmt.Errorf("selected Rust scenario executor returned %d observations, want %d", len(evidence.Observations), len(expectedContracts))
	}
	results := make(map[string]string, len(evidence.Observations))
	structured := make(map[string]json.RawMessage)
	for _, observation := range evidence.Observations {
		expectedContract, ok := expectedContracts[observation.CaseID]
		if !ok || observation.ContractDigest+"\x00"+observation.ProofID != expectedContract || observation.RealizationID != executor.Selector ||
			executor.Expected.Operation != executor.Selector || observation.RealizationKind != executor.Expected.Category ||
			strings.TrimSpace(observation.Observation) == "" {
			return nil, nil, stdout, stderr, fmt.Errorf("selected Rust scenario observation differs from executor %q", executor.Selector)
		}
		var encoded []byte
		if executor.Selector == "realization/stable-connector" {
			canonical, err := canonicalizeJSON([]byte(observation.Observation))
			if err != nil {
				return nil, nil, stdout, stderr, fmt.Errorf("canonicalize structured stable connector observation: %w", err)
			}
			encoded = []byte(canonical)
			structured[observation.CaseID] = append(json.RawMessage(nil), encoded...)
		} else {
			var err error
			encoded, err = json.Marshal(observation)
			if err != nil {
				return nil, nil, stdout, stderr, fmt.Errorf("encode selected Rust scenario observation: %w", err)
			}
		}
		digest := sha256.Sum256(encoded)
		if _, duplicate := results[observation.CaseID]; duplicate {
			return nil, nil, stdout, stderr, fmt.Errorf("selected Rust scenario observation duplicated case %q", observation.CaseID)
		}
		results[observation.CaseID] = fmt.Sprintf("sha256:%x", digest)
	}
	return results, structured, stdout, stderr, nil
}

type coreConformanceObservation struct {
	Selector  string `json:"selector"`
	Category  string `json:"category"`
	Operation string `json:"operation"`
}

type coreConformanceObservationSet struct {
	FormatVersion  uint32                       `json:"format_version"`
	TargetRevision string                       `json:"target_revision"`
	TargetVersion  string                       `json:"target_version"`
	Observations   []coreConformanceObservation `json:"observations"`
}

func runCoreConformanceCase(
	ctx context.Context,
	runner *dagger.Container,
	executor signoffmodel.ExecutorDefinition,
) (string, string, string, error) {
	executed := runner.
		WithEnvVariable("DAGGER_RUST_SIGNOFF_SELECTOR", executor.Selector).
		WithExec([]string{
			"dagger", "run", "cargo", "run", "--manifest-path", "/src/sdk/rust/Cargo.toml",
			"-p", "dagger-sdk", "--example", "signoff_core_conformance", "--locked",
		})
	stdout, stderr, err := readSignoffProcessEvidence(ctx, executed)
	if err != nil {
		return "", stdout, stderr, err
	}
	var evidence coreConformanceObservationSet
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &evidence); err != nil {
		return "", stdout, stderr, fmt.Errorf("decode selected Rust core observation: %w", err)
	}
	if evidence.FormatVersion != 1 || evidence.TargetRevision != coreTargetRevision || evidence.TargetVersion != coreTargetVersion {
		return "", stdout, stderr, fmt.Errorf("selected Rust core observation names a different exact target")
	}
	if len(evidence.Observations) != 1 {
		return "", stdout, stderr, fmt.Errorf("selected Rust core executor returned %d observations, want exactly one", len(evidence.Observations))
	}
	observation := evidence.Observations[0]
	if observation.Selector != executor.Selector || observation.Category != executor.Expected.Category || observation.Operation != executor.Expected.Operation {
		return "", stdout, stderr, fmt.Errorf("selected Rust core observation differs from executor %q", executor.Selector)
	}
	digest := sha256.Sum256([]byte(stdout))
	return fmt.Sprintf("sha256:%x", digest), stdout, stderr, nil
}

func runEngineIntegrationSignoffCase(
	ctx context.Context,
	runner *dagger.Container,
	executor signoffmodel.ExecutorDefinition,
) (string, string, string, error) {
	if executor.Expected.Category != "case-pass" || executor.Expected.Operation != executor.Selector {
		return "", "", "", fmt.Errorf("engine-integration executor %q has a mismatched expected observation", executor.Selector)
	}
	// Every Dagger container branch is immutable, so retaining /work satisfies the reviewed
	// integration fixtures' absolute paths without allowing one case to mutate another.
	runner = runner.WithWorkdir("/work").WithEnvVariable("RUST_SDK_ENGINE_INTEGRATION_CASE", executor.Selector)
	var identity string
	var err error
	if executor.Selector == "resolution" {
		err = verifyInstalledRustResolution(ctx, runner)
		identity = "installed-rust-resolution-verified"
	} else {
		identity, err = runEngineIntegrationCase(ctx, runner, executor.Selector)
	}
	if err != nil {
		return "", identity, "", err
	}
	if strings.TrimSpace(identity) == "" {
		return "", identity, "", fmt.Errorf("engine-integration executor %q returned no asserted identity", executor.Selector)
	}
	return stableCaseObservation(executor.Selector, identity), identity, "", nil
}

func (t *RustSdkDev) packagedSignoffScan(
	ctx context.Context,
	policyRoot *dagger.Directory,
	canaries signoffCanarySet,
	workspaces map[string]*dagger.Directory,
) (string, error) {
	if policyRoot == nil {
		return "", fmt.Errorf("exact sign-off policy source is required for packaged scanning")
	}
	expected := []struct {
		program   string
		path      string
		digestArg string
	}{
		{program: "standalone-example/cli", path: "build/cli", digestArg: "--cli-digest"},
		{program: "standalone-example/backend", path: "build/backend-image.tar", digestArg: "--backend-digest"},
		{program: "standalone-example/frontend", path: "build/frontend-image.tar", digestArg: "--frontend-digest"},
	}
	if len(workspaces) != len(expected) {
		return "", fmt.Errorf("packaged sign-off workspaces are incomplete: got %d, want %d", len(workspaces), len(expected))
	}
	inputs := dag.Directory()
	arguments := []string{
		"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
		"--locked", "--", "packaged-scan",
		"--root", signoffPackagedInputPath,
		"--seed", signoffSecretSeedPath,
	}
	for _, artifact := range expected {
		workspace := workspaces[artifact.program]
		if workspace == nil {
			return "", fmt.Errorf("packaged sign-off workspace %q is unavailable", artifact.program)
		}
		file := workspace.File(artifact.path)
		size, err := file.Size(ctx)
		if err != nil {
			return "", fmt.Errorf("observe actual packaged output %q size: %w", artifact.path, err)
		}
		if size <= 0 || size > signoffPackagedOutputMaxBytes {
			return "", fmt.Errorf("actual packaged output %q exceeds its byte bound", artifact.path)
		}
		observed, err := rustBaseContainer().
			WithMountedFile("/actual", file).
			WithExec([]string{"sha256sum", "/actual"}).
			Stdout(ctx)
		if err != nil {
			return "", fmt.Errorf("independently identify actual packaged output %q: %w", artifact.path, err)
		}
		fields := strings.Fields(observed)
		if len(fields) != 2 || len(fields[0]) != 64 || !isCanonicalSHA256("sha256:"+fields[0]) {
			return "", fmt.Errorf("actual packaged output %q returned a malformed identity", artifact.path)
		}
		inputs = inputs.WithFile(artifact.path, file)
		arguments = append(arguments, artifact.digestArg, "sha256:"+fields[0])
	}
	arguments = append(arguments, "--output", signoffPackagedScanPath)
	translator := rustBaseContainer().
		WithDirectory(signoffRepositoryRoot, policyRoot).
		WithWorkdir(signoffRepositoryRoot+"/sdk/rust").
		WithMountedDirectory(signoffPackagedInputPath, inputs).
		WithMountedSecret(
			signoffSecretSeedPath,
			dag.SetSecret("rust-signoff-packaged-canary-seed", canaries.seedHex),
		).
		WithExec(arguments)
	report, err := translator.File(signoffPackagedScanPath).Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("inspect actual packaged sign-off outputs: %w", err)
	}
	if err := requireCanonicalJSON([]byte(report)); err != nil {
		return "", fmt.Errorf("packaged artifact scan is not canonical: %w", err)
	}
	return report, nil
}

func (t *RustSdkDev) secretSignoffReport(
	ctx context.Context,
	policyRoot *dagger.Directory,
	canaries signoffCanarySet,
	packagedScan string,
	evidence signoffmodel.SecretEvidenceDomains,
) (string, error) {
	if policyRoot == nil {
		return "", fmt.Errorf("exact sign-off policy source is required")
	}
	files, err := evidence.Files()
	if err != nil {
		return "", err
	}
	inputs := dag.Directory()
	for _, file := range files {
		inputs = inputs.WithNewFile(string(file.Domain)+".evidence", string(file.Bytes))
	}
	translator := rustBaseContainer().
		WithDirectory(signoffRepositoryRoot, policyRoot).
		WithWorkdir(signoffRepositoryRoot+"/sdk/rust").
		WithMountedDirectory(signoffSecretInputPath, inputs).
		WithNewFile(signoffPackagedScanPath, packagedScan).
		WithMountedSecret(
			signoffSecretSeedPath,
			dag.SetSecret("rust-signoff-canary-seed", canaries.seedHex),
		).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
			"--locked", "--", "secret-report",
			"--root", signoffSecretInputPath,
			"--seed", signoffSecretSeedPath,
			"--packaged-scan", signoffPackagedScanPath,
			"--output", signoffSecretOutputPath,
		})
	report, err := translator.File(signoffSecretOutputPath).Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("admit exact sign-off secret evidence: %w", err)
	}
	if err := requireCanonicalJSON([]byte(report)); err != nil {
		return "", fmt.Errorf("secret evidence report is not canonical: %w", err)
	}
	return report, nil
}

// collectDirectoryEvidence retains a reversible deterministic archive of actual files. Base64
// keeps the archive safe for GraphQL strings without replacing its contents with a digest.
func collectDirectoryEvidence(
	ctx context.Context,
	directory *dagger.Directory,
	label string,
) ([]byte, error) {
	if directory == nil {
		return nil, fmt.Errorf("%s are unavailable", label)
	}
	collector := rustBaseContainer().
		WithDirectory("/evidence", directory).
		WithExec([]string{"sh", "-euc", `
			test -n "$(find /evidence -type f -print -quit)"
			test -z "$(find /evidence -type l -print -quit)"
			tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -C /evidence -cf - . | base64 -w 0 > /tmp/signoff-evidence.b64
			test -s /tmp/signoff-evidence.b64
		`})
	contents, err := collector.File(signoffEvidenceOutputPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect %s: %w", label, err)
	}
	if contents == "" {
		return nil, fmt.Errorf("%s are unavailable", label)
	}
	return []byte(contents), nil
}

// collectArtifactEntryEvidence reads the admitted bundle's real sidecars and entry table while
// leaving the large payload on the separate exact-file scanner edge.
func collectArtifactEntryEvidence(ctx context.Context, bundle *dagger.File) ([]byte, error) {
	if bundle == nil {
		return nil, fmt.Errorf("exact artifact entries are unavailable")
	}
	collector := rustBaseContainer().
		WithMountedFile("/artifact/exact-target.tar", bundle).
		WithExec([]string{"sh", "-euc", `
			mkdir -p /evidence
			tar -tf /artifact/exact-target.tar > /evidence/entries.txt
			tar -xf /artifact/exact-target.tar -C /evidence manifest.json provenance.json checksums.sha256
			test -s /evidence/entries.txt
			test -s /evidence/manifest.json
			test -s /evidence/provenance.json
			test -s /evidence/checksums.sha256
			tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -C /evidence -cf - . | base64 -w 0 > /tmp/signoff-evidence.b64
		`})
	contents, err := collector.File(signoffEvidenceOutputPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect exact artifact entries: %w", err)
	}
	if contents == "" {
		return nil, fmt.Errorf("exact artifact entries are unavailable")
	}
	return []byte(contents), nil
}

func (t *RustSdkDev) scanSignoffPayload(
	ctx context.Context,
	policyRoot *dagger.Directory,
	payload *dagger.File,
) (signoffScannerResult, error) {
	if policyRoot == nil || payload == nil {
		return signoffScannerResult{}, fmt.Errorf("exact sign-off policy source and payload are required")
	}
	scanner := dag.Container().
		From(signoffScannerImage).
		WithEntrypoint([]string{}).
		WithMountedFile("/scan/engine.oci.tar.zst", payload).
		WithEnvVariable("RUST_SIGNOFF_TRIVY_DB", signoffDatabaseRepository).
		WithExec([]string{
			"sh", "-euc", `
				mkdir -p /scan-output
				trivy image --download-db-only --db-repository "$RUST_SIGNOFF_TRIVY_DB"
				start=$(date +%s)
				trivy image --skip-db-update --scanners=vuln --pkg-types=os,library --exit-code=0 --format=json --output=/scan-output/findings.json --input=/scan/engine.oci.tar.zst
				end=$(date +%s)
				elapsed=$(( (end - start) * 1000 ))
				if [ "$elapsed" -eq 0 ]; then elapsed=1; fi
				test "$(wc -c < /scan-output/findings.json)" -le 16777216
				trivy --version --format=json > /scan-output/scanner-version.json
				cp /root/.cache/trivy/db/metadata.json /scan-output/database-metadata.json
				(cd /root/.cache/trivy/db && sha256sum trivy.db metadata.json) > /scan-output/database-checksums.sha256
				sha256sum /scan/engine.oci.tar.zst > /scan-output/payload.sha256
				printf '%s\n' "$elapsed" > /scan-output/elapsed-millis
			`,
		})
	elapsed, err := scanner.File("/scan-output/elapsed-millis").Contents(ctx)
	if err != nil {
		return signoffScannerResult{}, fmt.Errorf("read exact-payload scan timing: %w", err)
	}
	rawEvidence, err := collectDirectoryEvidence(ctx, scanner.Directory("/scan-output"), "raw exact-payload scanner evidence")
	if err != nil {
		return signoffScannerResult{}, err
	}
	translator := rustBaseContainer().
		WithDirectory(signoffRepositoryRoot, policyRoot).
		WithWorkdir(signoffRepositoryRoot+"/sdk/rust").
		WithMountedDirectory(signoffScanInputPath, scanner.Directory("/scan-output")).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
			"--locked", "--", "scanner-translate",
			"--root", "/src",
			"--findings", signoffScanInputPath + "/findings.json",
			"--scanner-version", signoffScanInputPath + "/scanner-version.json",
			"--database-metadata", signoffScanInputPath + "/database-metadata.json",
			"--database-checksums", signoffScanInputPath + "/database-checksums.sha256",
			"--database-artifact-digest", signoffDatabaseArtifactDigest,
			"--payload-checksum", signoffScanInputPath + "/payload.sha256",
			"--elapsed-millis", strings.TrimSpace(elapsed),
			"--output", signoffScanOutputPath,
		})
	observation, err := translator.File(signoffScanOutputPath).Contents(ctx)
	if err != nil {
		return signoffScannerResult{}, fmt.Errorf("translate exact-payload scanner observation: %w", err)
	}
	if err := requireCanonicalJSON([]byte(observation)); err != nil {
		return signoffScannerResult{}, fmt.Errorf("translated scanner observation is not canonical: %w", err)
	}
	return signoffScannerResult{observation: observation, evidence: rawEvidence}, nil
}

func requireCanonicalJSON(data []byte) error {
	canonical, err := canonicalizeJSON(data)
	if err != nil {
		return err
	}
	if !bytes.Equal([]byte(canonical), data) {
		return fmt.Errorf("JSON bytes are not in canonical form")
	}
	return nil
}

func canonicalizeJSON(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if decoder.More() {
		return "", fmt.Errorf("JSON input contains multiple values")
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}

func validateMaterialization(artifactPlan json.RawMessage, hasArtifact bool) error {
	var wire signoffArtifactPlanWire
	if err := json.Unmarshal(artifactPlan, &wire); err != nil {
		return fmt.Errorf("decode artifact materialization: %w", err)
	}
	isBuild := string(wire.Materialization) == `"build"`
	isImport := len(wire.Materialization) > 0 && wire.Materialization[0] == '{'
	if (!hasArtifact && !isBuild) || (hasArtifact && !isImport) {
		return fmt.Errorf("artifact file presence differs from the declared materialization strategy")
	}
	return nil
}

func artifactIdentities(
	artifactPlan json.RawMessage,
	manifestBytes []byte,
	builtPayload string,
) (string, string, error) {
	var wire signoffArtifactPlanWire
	if err := json.Unmarshal(artifactPlan, &wire); err != nil {
		return "", "", fmt.Errorf("decode artifact identities: %w", err)
	}
	if string(wire.Materialization) == `"build"` {
		manifestDigest := sha256.Sum256(append([]byte(signoffArtifactDomain), manifestBytes...))
		return fmt.Sprintf("sha256:%x", manifestDigest), builtPayload, nil
	}
	var imported struct {
		Import struct {
			ManifestDigest string `json:"manifest_digest"`
			PayloadDigest  string `json:"payload_digest"`
		} `json:"import"`
	}
	if err := json.Unmarshal(wire.Materialization, &imported); err != nil || !isCanonicalSHA256(imported.Import.ManifestDigest) || !isCanonicalSHA256(imported.Import.PayloadDigest) {
		return "", "", fmt.Errorf("import strategy has malformed artifact identities")
	}
	if imported.Import.PayloadDigest != builtPayload {
		return "", "", fmt.Errorf("import strategy payload identity differs from admitted manifest")
	}
	return imported.Import.ManifestDigest, imported.Import.PayloadDigest, nil
}

func positiveMillis(duration time.Duration) uint64 {
	millis := uint64(duration / time.Millisecond)
	if millis == 0 {
		return 1
	}
	return millis
}

func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}

func signoffBuildObservation(elapsedMillis uint64) (string, error) {
	observation := signoffArtifactBuildObservation{
		FormatVersion: "1.0.0",
		Events: []json.RawMessage{
			json.RawMessage(`"construction-started"`),
			json.RawMessage(`{"component-built":{"component":"engine"}}`),
			json.RawMessage(`{"component-built":{"component":"cli"}}`),
			json.RawMessage(`{"component-built":{"component":"go-runtime"}}`),
			json.RawMessage(`{"component-built":{"component":"rust-sdk"}}`),
			json.RawMessage(`"payload-exported"`),
			json.RawMessage(`"manifest-verified"`),
			json.RawMessage(`"payload-verified"`),
			json.RawMessage(`"components-verified"`),
			json.RawMessage(`"artifact-ready"`),
		},
		ConstructionCount: 1,
		ImportCount:       0,
		ComponentBuildCounts: map[string]uint32{
			"engine": 1, "cli": 1, "go-runtime": 1, "rust-sdk": 1,
		},
		ForbiddenWorkCounts: map[string]uint32{
			"unrelated-sdk-build": 0, "unrelated-sdk-test": 0,
			"complete-go-test-suite": 0, "unscoped-generation": 0,
			"distribution-build": 0, "strategy-fallback": 0,
		},
		MaterializationElapsedMillis: elapsedMillis,
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return "", fmt.Errorf("encode exact-target build observation: %w", err)
	}
	canonical, err := canonicalizeJSON(encoded)
	if err != nil {
		return "", fmt.Errorf("canonicalize exact-target build observation: %w", err)
	}
	return canonical, nil
}

func signoffImportObservation(
	identities signoffmodel.TargetComponentIdentities,
	elapsedMillis uint64,
) (string, error) {
	observation := signoffArtifactImportObservation{
		FormatVersion: "1.0.0",
		Events: []json.RawMessage{
			json.RawMessage(`"bundle-supplied"`),
			json.RawMessage(`"manifest-verified"`),
			json.RawMessage(`"payload-verified"`),
			json.RawMessage(`"components-verified"`),
			json.RawMessage(`"container-imported"`),
			json.RawMessage(`"artifact-ready"`),
		},
		ConstructionCount: 0,
		ImportCount:       1,
		ComponentBuildCounts: map[string]uint32{
			"engine": 0, "cli": 0, "go-runtime": 0, "rust-sdk": 0,
		},
		ForbiddenWorkCounts: map[string]uint32{
			"unrelated-sdk-build": 0, "unrelated-sdk-test": 0,
			"complete-go-test-suite": 0, "unscoped-generation": 0,
			"distribution-build": 0, "strategy-fallback": 0,
		},
		VerifiedComponentDigests: map[string]string{
			"engine": identities.Engine, "cli": identities.CLI,
			"go-runtime": identities.GoRuntime, "rust-sdk": identities.RustSDK,
		},
		MaterializationElapsedMillis: elapsedMillis,
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return "", fmt.Errorf("encode exact-target import observation: %w", err)
	}
	canonical, err := canonicalizeJSON(encoded)
	if err != nil {
		return "", fmt.Errorf("canonicalize exact-target import observation: %w", err)
	}
	return canonical, nil
}

func newSignoffCanarySet() (signoffCanarySet, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return signoffCanarySet{}, fmt.Errorf("generate ephemeral sign-off canaries: %w", err)
	}
	categories := []struct {
		category string
		env      string
	}{
		{"Session", "RUST_SDK_SIGNOFF_SESSION"},
		{"Registry", "RUST_SDK_SIGNOFF_REGISTRY"},
		{"Git", "RUST_SDK_SIGNOFF_GIT"},
		{"Environment", "RUST_SDK_SIGNOFF_ENVIRONMENT"},
		{"Trace", "RUST_SDK_SIGNOFF_TRACE"},
		{"Url", "RUST_SDK_SIGNOFF_URL"},
	}
	canaries := make([]signoffCanary, 0, len(categories))
	for _, category := range categories {
		hasher := sha256.New()
		hasher.Write([]byte("dagger-rust-sdk-non-production-canary-v1\x00"))
		hasher.Write([]byte(category.category))
		hasher.Write(seed)
		canaries = append(canaries, signoffCanary{
			category: category.category,
			env:      category.env,
			value:    "dagger-canary-" + hex.EncodeToString(hasher.Sum(nil)),
		})
	}
	return signoffCanarySet{seedHex: hex.EncodeToString(seed), canaries: canaries}, nil
}

// SignoffArtifact constructs and exports one focused target without starting an engine service.
//
// The seed contains only independently derived, byte-free construction inputs. This method
// observes the four component identities from the retained graph and lets the Rust policy tool
// seal them into the Build plan before any artifact bytes are assembled.
func (t *RustSdkDev) SignoffArtifact(
	ctx context.Context,
	seedJSON string,
) (*RustSignoffArtifact, error) {
	materializationStarted := time.Now()
	subject, err := t.rederiveSignoffArtifactSeed(ctx, seedJSON)
	if err != nil {
		return nil, err
	}
	content, err := t.signoffEngineContent(ctx, subject)
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
	identities, err := t.observeSignoffTargetComponents(ctx, target)
	if err != nil {
		return nil, err
	}
	if identities.RustSDK != content.ManifestDigest || identities.RustDescriptor != content.DescriptorDigest ||
		identities.RustDependency != content.dependencyDescriptor ||
		identities.RustDependencyDigest != content.dependencyDescriptorDigest {
		return nil, fmt.Errorf("focused target Rust identities differ from the retained content graph")
	}
	planJSON, err := t.sealSignoffArtifactPlan(ctx, subject, seedJSON, identities)
	if err != nil {
		return nil, err
	}
	payload := target.AsTarball(dagger.ContainerAsTarballOpts{
		ForcedCompression: dagger.ImageLayerCompressionZstd,
	})
	if _, err := payload.Digest(ctx); err != nil {
		return nil, fmt.Errorf("materialize sole exact-target OCI payload: %w", err)
	}
	materializationMillis := positiveMillis(time.Since(materializationStarted))
	buildObservation, err := signoffBuildObservation(materializationMillis)
	if err != nil {
		return nil, err
	}
	assembler := t.artifactTool(subject, planJSON, payload).
		WithNewFile(signoffBuildObservationPath, buildObservation).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
			"--locked", "--", "artifact-build",
			"--plan", signoffPlanPath,
			"--payload", signoffPayloadPath,
			"--observation", signoffBuildObservationPath,
			"--bundle-output", signoffBundlePath,
			"--manifest-output", signoffManifestPath,
			"--receipt-output", signoffBuildReceiptPath,
		})
	manifestJSON, err := assembler.File(signoffManifestPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("assemble exact-target artifact: %w", err)
	}
	var identity signoffManifestIdentity
	if err := json.Unmarshal([]byte(manifestJSON), &identity); err != nil || !isCanonicalSHA256(identity.PayloadDigest) {
		return nil, fmt.Errorf("artifact assembler returned a malformed payload identity")
	}
	if err := requireCanonicalJSON([]byte(manifestJSON)); err != nil {
		return nil, fmt.Errorf("artifact manifest is not canonical: %w", err)
	}
	buildReceiptJSON, err := assembler.File(signoffBuildReceiptPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read canonical exact-target build receipt: %w", err)
	}
	if err := requireCanonicalJSON([]byte(buildReceiptJSON)); err != nil {
		return nil, fmt.Errorf("artifact build receipt is not canonical: %w", err)
	}
	return &RustSignoffArtifact{
		Bundle:           assembler.File(signoffBundlePath),
		PlanJSON:         planJSON,
		ManifestJSON:     manifestJSON,
		BuildReceiptJSON: buildReceiptJSON,
		PayloadDigest:    identity.PayloadDigest,
		Payload:          payload,
		Manifest:         assembler.File(signoffManifestPath),
		BuildReceipt:     assembler.File(signoffBuildReceiptPath),
		Target:           target,
		CLI:              target.File(signoffCliPath),
	}, nil
}

// rederiveSignoffArtifactSeed evaluates the subject revision's own Rust policy tool against an
// immutable Git tree and requires byte equality before any target component is constructed.
// Reading the output eagerly makes this a real admission boundary rather than a lazy sibling of
// the later build graph.
func (t *RustSdkDev) rederiveSignoffArtifactSeed(
	ctx context.Context,
	seedJSON string,
) (signoffmodel.ArtifactSubject, error) {
	subject, err := signoffmodel.AdmitArtifactSubject(t.EngineRepository, []byte(seedJSON))
	if err != nil {
		return signoffmodel.ArtifactSubject{}, err
	}
	immutableSource := dag.Git(subject.Repository).
		Commit(subject.Revision).
		Tree(dagger.GitRefTreeOpts{DiscardGitDir: false})
	tool := rustBaseContainer().
		WithDirectory(signoffRepositoryRoot, immutableSource).
		WithWorkdir(signoffRepositoryRoot + "/sdk/rust").
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
			"--locked", "--", "artifact-seed",
			"--root", signoffRepositoryRoot,
			"--repository", subject.Repository,
			"--output", signoffRederivedSeedPath,
		})
	rederived, err := tool.File(signoffRederivedSeedPath).Contents(ctx)
	if err != nil {
		return signoffmodel.ArtifactSubject{}, fmt.Errorf("rederive artifact seed from immutable subject: %w", err)
	}
	if err := signoffmodel.VerifyArtifactSeed([]byte(seedJSON), []byte(rederived)); err != nil {
		return signoffmodel.ArtifactSubject{}, err
	}
	if err := t.verifySignoffImplementationSource(ctx, subject); err != nil {
		return signoffmodel.ArtifactSubject{}, err
	}
	return subject, nil
}

// verifySignoffImplementationSource proves that the statically loaded local Dagger modules which
// interpret the immutable tree are themselves byte-identical to that revision. Without this
// boundary, an uncommitted engine-dev or generated-client edit could build committed source while
// silently changing how the target is constructed.
func (t *RustSdkDev) verifySignoffImplementationSource(
	ctx context.Context,
	subject signoffmodel.ArtifactSubject,
) error {
	include := []string{
		"engine/distconsts/**",
		"toolchains/cli-dev/**",
		"toolchains/engine-dev/**",
		"toolchains/go/**",
		"toolchains/rust-sdk-dev/**",
	}
	live := t.Ws.Directory("/").Filter(dagger.DirectoryFilterOpts{Include: include})
	immutable := dag.Git(subject.Repository).
		Commit(subject.Revision).
		Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).
		Filter(dagger.DirectoryFilterOpts{Include: include})
	liveDigest, err := live.Digest(ctx)
	if err != nil {
		return fmt.Errorf("identify live sign-off implementation source: %w", err)
	}
	immutableDigest, err := immutable.Digest(ctx)
	if err != nil {
		return fmt.Errorf("identify immutable sign-off implementation source: %w", err)
	}
	if liveDigest != immutableDigest {
		return fmt.Errorf("live sign-off implementation differs from the immutable subject tree")
	}
	return nil
}

// sealSignoffArtifactPlan derives component identities from the exact retained target graph.
// File hashes are computed together so inspecting the engine and CLI cannot introduce distinct
// construction paths. Manifest identities are read from the already retained Go and Rust
// content objects rather than reconstructed by a second SDK builder.
func (t *RustSdkDev) sealSignoffArtifactPlan(
	ctx context.Context,
	subject signoffmodel.ArtifactSubject,
	seedJSON string,
	identities signoffmodel.TargetComponentIdentities,
) (string, error) {
	if err := requireCanonicalJSON([]byte(seedJSON)); err != nil {
		return "", fmt.Errorf("artifact plan seed is not canonical: %w", err)
	}
	for name, digest := range map[string]string{
		"engine": identities.Engine, "CLI": identities.CLI, "Go runtime": identities.GoRuntime,
		"Rust SDK": identities.RustSDK, "Rust descriptor": identities.RustDescriptor,
		"Rust dependency descriptor": identities.RustDependencyDigest,
	} {
		if !isCanonicalSHA256(digest) {
			return "", fmt.Errorf("%s component identity is not canonical SHA-256", name)
		}
	}
	planner := t.signoffPolicyContainer(subject).
		WithNewFile(signoffSeedPath, seedJSON).
		WithNewFile(signoffDependencyPath, identities.RustDependency).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
			"--locked", "--", "artifact-plan",
			"--seed", signoffSeedPath,
			"--engine-content-digest", identities.Engine,
			"--cli-content-digest", identities.CLI,
			"--go-runtime-content-digest", identities.GoRuntime,
			"--rust-content-digest", identities.RustSDK,
			"--rust-descriptor-digest", identities.RustDescriptor,
			"--rust-dependency-descriptor", signoffDependencyPath,
			"--rust-dependency-descriptor-digest", identities.RustDependencyDigest,
			"--output", signoffPlanPath,
		})
	planJSON, err := planner.File(signoffPlanPath).Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("seal exact-target artifact plan: %w", err)
	}
	if err := requireCanonicalJSON([]byte(planJSON)); err != nil {
		return "", fmt.Errorf("sealed exact-target artifact plan is not canonical: %w", err)
	}
	return planJSON, nil
}

// observeSignoffTargetComponents independently reads identities from the exact target object.
// The two binaries share one checksum process, while embedded runtimes are observed through the
// immutable manifest and descriptor variables the engine will actually use at runtime.
func (t *RustSdkDev) observeSignoffTargetComponents(
	ctx context.Context,
	target *dagger.Container,
) (signoffmodel.TargetComponentIdentities, error) {
	hasher := dag.Container().
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithMountedFile(signoffEnginePath, target.File(signoffEnginePath)).
		WithMountedFile(signoffCliPath, target.File(signoffCliPath)).
		WithExec([]string{"sha256sum", signoffEnginePath, signoffCliPath})
	checksums, err := hasher.Stdout(ctx)
	if err != nil {
		return signoffmodel.TargetComponentIdentities{}, fmt.Errorf("identify exact engine and CLI bytes: %w", err)
	}
	fields := strings.Fields(checksums)
	if len(fields) != 4 || fields[1] != signoffEnginePath || fields[3] != signoffCliPath {
		return signoffmodel.TargetComponentIdentities{}, fmt.Errorf("component checksum observation is malformed")
	}
	identities := signoffmodel.TargetComponentIdentities{
		Engine: "sha256:" + fields[0],
		CLI:    "sha256:" + fields[2],
	}
	for name, destination := range map[string]*string{
		signoffGoManifestEnv:     &identities.GoRuntime,
		signoffRustManifestEnv:   &identities.RustSDK,
		signoffRustDescriptorEnv: &identities.RustDescriptor,
	} {
		value, err := target.EnvVariable(ctx, name)
		if err != nil {
			return signoffmodel.TargetComponentIdentities{}, fmt.Errorf("read focused target identity %s: %w", name, err)
		}
		*destination = value
	}
	for name, destination := range map[string]*string{
		signoffRustDependencyEnv:       &identities.RustDependency,
		signoffRustDependencyDigestEnv: &identities.RustDependencyDigest,
	} {
		value, err := target.EnvVariable(ctx, name)
		if err != nil {
			return signoffmodel.TargetComponentIdentities{}, fmt.Errorf("read focused target identity %s: %w", name, err)
		}
		*destination = value
	}
	for name, digest := range map[string]string{
		"engine": identities.Engine, "CLI": identities.CLI, "Go runtime": identities.GoRuntime,
		"Rust SDK": identities.RustSDK, "Rust descriptor": identities.RustDescriptor,
		"Rust dependency descriptor": identities.RustDependencyDigest,
	} {
		if !isCanonicalSHA256(digest) {
			return signoffmodel.TargetComponentIdentities{}, fmt.Errorf("%s target identity is not canonical SHA-256", name)
		}
	}
	return identities, nil
}

// importSignoffArtifact verifies the complete host bundle before the sole container import site.
// The Import branch deliberately has no access to engine, CLI, Go-runtime, or Rust-content
// builders, so an error cannot fall back to reconstructing the target.
func (t *RustSdkDev) importSignoffArtifact(
	ctx context.Context,
	subject signoffmodel.ArtifactSubject,
	planJSON string,
	bundle *dagger.File,
) (*verifiedSignoffTarget, error) {
	if bundle == nil {
		return nil, fmt.Errorf("exact-target artifact bundle is required for import")
	}
	importStarted := time.Now()
	verifier := t.artifactTool(subject, planJSON, bundle).WithExec([]string{
		"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
		"--locked", "--", "artifact-extract",
		"--plan", signoffPlanPath,
		"--bundle", signoffPayloadPath,
		"--payload-output", signoffImportedPath,
		"--manifest-output", signoffManifestPath,
	})
	verifiedPayload := verifier.File(signoffImportedPath)
	imported := dag.Container().Import(verifiedPayload)
	identities, err := t.observeSignoffTargetComponents(ctx, imported)
	if err != nil {
		return nil, err
	}
	if err := signoffmodel.VerifyTargetComponents([]byte(planJSON), identities); err != nil {
		return nil, err
	}
	observation, err := signoffImportObservation(identities, positiveMillis(time.Since(importStarted)))
	if err != nil {
		return nil, err
	}
	receiptBuilder := verifier.
		WithNewFile(signoffImportObservationPath, observation).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", signoffArtifactBinary,
			"--locked", "--", "artifact-import",
			"--plan", signoffPlanPath,
			"--bundle", signoffPayloadPath,
			"--observation", signoffImportObservationPath,
			"--receipt-output", signoffImportReceiptPath,
		})
	importReceiptJSON, err := receiptBuilder.File(signoffImportReceiptPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("admit actual exact-target import observation: %w", err)
	}
	if err := requireCanonicalJSON([]byte(importReceiptJSON)); err != nil {
		return nil, fmt.Errorf("artifact import receipt is not canonical: %w", err)
	}
	var receiptIdentity signoffReceiptIdentity
	if err := json.Unmarshal([]byte(importReceiptJSON), &receiptIdentity); err != nil || !isCanonicalSHA256(receiptIdentity.ReceiptDigest) {
		return nil, fmt.Errorf("artifact import receipt has a malformed identity")
	}
	return &verifiedSignoffTarget{
		container:           imported,
		cli:                 imported.File(signoffCliPath),
		payload:             verifiedPayload,
		manifest:            verifier.File(signoffManifestPath),
		importReceipt:       receiptBuilder.File(signoffImportReceiptPath),
		importReceiptJSON:   importReceiptJSON,
		importReceiptDigest: receiptIdentity.ReceiptDigest,
		components:          identities,
	}, nil
}

func (t *RustSdkDev) artifactTool(
	subject signoffmodel.ArtifactSubject,
	planJSON string,
	source *dagger.File,
) *dagger.Container {
	return t.signoffPolicyContainer(subject).
		WithNewFile(signoffPlanPath, planJSON).
		WithMountedFile(signoffPayloadPath, source)
}

// signoffPolicyContainer runs the policy implementation committed at the admitted subject. This
// prevents a live checkout edit from changing seed sealing, bundle assembly, or import admission
// while the payload continues to claim an older revision.
func (t *RustSdkDev) signoffPolicyContainer(
	subject signoffmodel.ArtifactSubject,
) *dagger.Container {
	root := dag.Git(subject.Repository).
		Commit(subject.Revision).
		Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	return rustBaseContainer().
		WithDirectory(signoffRepositoryRoot, root).
		WithWorkdir(signoffRepositoryRoot + "/sdk/rust")
}

// installedRustBaseline owns the sole exact-target service and SDK installation sites.
// Its input type can be produced only after the artifact verifier, so graph construction cannot
// start a service and then discover that the retained bytes belong to another target.
func (target *verifiedSignoffTarget) installedRustBaseline(source *dagger.Directory, canaries signoffCanarySet) *installedSignoffBaseline {
	service := target.container.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{
			Args:                     []string{"--addr", "tcp://0.0.0.0:1234"},
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})
	initial := dag.Container().
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithDirectory("/src/sdk/rust", source).
		WithDirectory("/work", dag.Directory()).
		WithWorkdir("/work").
		WithMountedFile(signoffCliPath, target.cli).
		WithEnvVariable("PATH", "/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin").
		WithoutEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN").
		WithExec([]string{"git", "init"}).
		WithExec([]string{"git", "config", "user.name", "Rust SDK Sign-off"}).
		WithExec([]string{"git", "config", "user.email", "rust-sdk-signoff@dagger.invalid"}).
		WithExec([]string{"git", "commit", "--allow-empty", "-m", "initialize exact baseline"})
	runner := initial.
		WithServiceBinding(signoffEngineAlias, service).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", signoffEngineEndpoint).
		WithExec([]string{"dagger", "-y", "sdk", "install", "--here", "rust"})
	for _, canary := range canaries.canaries {
		runner = runner.WithSecretVariable(
			canary.env,
			dag.SetSecret("rust-signoff-canary-"+strings.ToLower(canary.category), canary.value),
		)
	}
	return &installedSignoffBaseline{initial: initial, runner: runner, service: service, cli: target.cli}
}

// observeInitialFacts evaluates the clean pre-service runner rather than inferring isolation from
// graph construction. The exact artifact and Import receipt have already been admitted before this
// baseline can exist, so the structural pre-validation service-start count is zero.
func (baseline *installedSignoffBaseline) observeInitialFacts(ctx context.Context) (signoffBaselineFacts, error) {
	status, err := baseline.initial.WithExec([]string{"git", "status", "--porcelain"}).Stdout(ctx)
	if err != nil {
		return signoffBaselineFacts{}, fmt.Errorf("observe initial Git workspace: %w", err)
	}
	cliPath, err := baseline.initial.WithExec([]string{"sh", "-c", "command -v dagger"}).Stdout(ctx)
	if err != nil {
		return signoffBaselineFacts{}, fmt.Errorf("observe exact artifact CLI on PATH: %w", err)
	}
	ambientCLI, err := baseline.initial.WithExec([]string{
		"sh", "-c",
		"for candidate in /usr/local/go/bin/dagger /usr/bin/dagger /bin/dagger; do [ ! -e \"$candidate\" ] || printf '%s\\n' \"$candidate\"; done",
	}).Stdout(ctx)
	if err != nil {
		return signoffBaselineFacts{}, fmt.Errorf("observe ambient host CLI paths: %w", err)
	}
	staleConfig, err := baseline.initial.WithExec([]string{
		"sh", "-c",
		"for candidate in dagger.json dagger.toml .dagger; do [ ! -e \"$candidate\" ] || printf '%s\\n' \"$candidate\"; done",
	}).Stdout(ctx)
	if err != nil {
		return signoffBaselineFacts{}, fmt.Errorf("observe pre-existing Dagger configuration: %w", err)
	}
	return signoffBaselineFacts{
		CleanGitWorkspace:             status == "",
		ArtifactCLIOnlyOnPath:         strings.TrimSpace(cliPath) == signoffCliPath && ambientCLI == "",
		HostCLIVisible:                ambientCLI != "",
		StaleInstalledConfig:          staleConfig != "",
		ServiceStartsBeforeValidation: 0,
	}, nil
}

// startAndProbe starts the exact target explicitly and observes its public runtime version before
// SDK installation or case fan-out. The probe uses the imported CLI and service only; it cannot
// trigger a second artifact, engine constructor, or SDK builder.
func (baseline *installedSignoffBaseline) startAndProbe(ctx context.Context) (string, error) {
	started, err := baseline.service.Start(ctx)
	if err != nil {
		return "", err
	}
	baseline.service = started
	stdout, err := dag.Container().
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithMountedFile(signoffCliPath, baseline.cli).
		WithEnvVariable("PATH", "/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin").
		WithServiceBinding(signoffEngineAlias, started).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", signoffEngineEndpoint).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v0.0.0").
		WithExec(
			[]string{"dagger", "query", "-M"},
			dagger.ContainerWithExecOpts{Stdin: "{version}"},
		).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	if len(stdout) > 4096 {
		return "", fmt.Errorf("exact-target readiness observation exceeds its bound")
	}
	var observation struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(stdout), &observation); err != nil {
		return "", fmt.Errorf("decode exact-target readiness observation: %w", err)
	}
	if observation.Version != coreTargetVersion {
		return "", fmt.Errorf("exact-target readiness returned version %q, want %q", observation.Version, coreTargetVersion)
	}
	return observation.Version, nil
}

// observeInstalledDependency asks the already installed packaged Rust SDK to create one isolated
// no-generation module, then decodes the dependency it actually wrote. The proof branch never
// becomes the shared baseline used by cases, so observing the coordinate cannot perturb fan-out.
func (baseline *installedSignoffBaseline) observeInstalledDependency(
	ctx context.Context,
) (signoffmodel.DependencyObservation, error) {
	proof := baseline.runner.WithExec([]string{
		"dagger", "-y", "module", "init", "rust", "signoff-dependency",
		"--path", "modules/signoff-dependency", "--no-generate",
	})
	manifest, err := proof.File(signoffDependencyProofPath).Contents(ctx)
	if err != nil {
		return signoffmodel.DependencyObservation{}, fmt.Errorf("observe installed Rust dependency: %w", err)
	}
	observation, err := signoffmodel.ObserveInstalledRustDependency(manifest)
	if err != nil {
		return signoffmodel.DependencyObservation{}, err
	}
	return observation, nil
}

// programBranch derives every mutable coordinate from the reviewed program and attempt while
// retaining the exact CLI, engine service, installed config, and packaged dependency graph.
func (baseline *installedSignoffBaseline) programBranch(
	program signoffmodel.Program,
	spec signoffmodel.ProgramSpec,
	policy signoffmodel.ExecutionPolicy,
	attempt uint32,
) (*dagger.Container, string, error) {
	if attempt == 0 {
		return nil, "", fmt.Errorf("sign-off case attempt must be one-based")
	}
	if spec.Program != program {
		return nil, "", fmt.Errorf("sign-off program specification differs from %q", program.Key())
	}
	if err := policy.ValidateFor(program); err != nil {
		return nil, "", err
	}
	identity := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", program.Key(), attempt)))
	namespace := fmt.Sprintf("%x", identity)
	workspace := "/work/cases/" + namespace
	runner := baseline.runner.
		WithExec([]string{"mkdir", "-p", workspace}).
		WithWorkdir(workspace).
		WithEnvVariable("RUST_SDK_SIGNOFF_PROGRAM", program.Key()).
		WithEnvVariable("RUST_SDK_SIGNOFF_NAMESPACE", namespace).
		WithEnvVariable("RUST_SDK_SIGNOFF_ATTEMPT", fmt.Sprintf("%d", attempt)).
		WithEnvVariable("RUST_SDK_SIGNOFF_NETWORK_POLICY", string(policy.Network)).
		WithEnvVariable("CARGO_TARGET_DIR", "/tmp/cargo-target-"+namespace).
		WithMountedCache(
			"/var/cache/rust-signoff",
			dag.CacheVolume("rust-signoff-"+namespace),
		)
	if program.Kind == signoffmodel.ProgramStandaloneExample {
		runner = runner.WithDirectory(workspace, baseline.runner.Directory("/src/sdk/rust/examples/"+program.Value))
	}
	if spec.Boundary == signoffmodel.BoundaryStableConnector {
		// The distribution path must first try its compiled release. The exact artifact CLI
		// remains discoverable only through PATH for the beta compatibility transition.
		runner = runner.WithoutEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN")
	}
	return runner, "rust-signoff-" + namespace, nil
}

// stop terminates the sole exact-target service on both success and failed fan-out paths.
func (baseline *installedSignoffBaseline) stop(ctx context.Context) error {
	_, err := baseline.service.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
	return err
}
