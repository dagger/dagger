package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	signoffScannerImage   = "aquasec/trivy:0.69.3@sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c"
	signoffArtifactDomain = "dagger-rust-sdk-artifact-v1\x00"
)

// RustSignoffArtifact is one exportable exact-target bundle and its retained build graph.
// The target and CLI stay private because callers must not bypass Rust admission by supplying
// graph objects detached from the verified portable bytes.
type RustSignoffArtifact struct {
	Bundle        *dagger.File
	ManifestJSON  string
	PayloadDigest string
	Payload       *dagger.File      // +private
	Manifest      *dagger.File      // +private
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
	manifest  *dagger.File
}

type installedSignoffBaseline struct {
	runner  *dagger.Container
	service *dagger.Service
}

type signoffPlanWire struct {
	FormatVersion       string          `json:"format_version"`
	TargetDigest        string          `json:"target_digest"`
	SubjectRevision     string          `json:"subject_revision"`
	CaseCatalogDigest   string          `json:"case_catalog_digest"`
	ClosureBundleDigest string          `json:"closure_bundle_digest"`
	MaximumConcurrency  uint32          `json:"maximum_concurrency"`
	ArtifactPlan        json.RawMessage `json:"artifact_plan"`
}

type signoffArtifactPlanWire struct {
	Materialization json.RawMessage `json:"materialization"`
}

type signoffCatalogWire struct {
	FormatVersion string `json:"format_version"`
	TargetDigest  string `json:"target_digest"`
	Cases         []any  `json:"cases"`
}

type signoffClosureWire struct {
	FormatVersion string `json:"format_version"`
	TargetDigest  string `json:"target_digest"`
	BundleDigest  string `json:"bundle_digest"`
}

type signoffPlatformWire struct {
	FormatVersion      string         `json:"format_version"`
	TargetDigest       string         `json:"target_digest"`
	MatrixDigest       string         `json:"matrix_digest"`
	NativeObservations map[string]any `json:"native_observations"`
}

type admittedSignoffInputs struct {
	plan             signoffPlanWire
	artifactPlanJSON string
	routes           []signoffmodel.CaseRoute
	registry         map[string]signoffmodel.ProgramSpec
	platformDigest   string
}

type rawSignoffCase struct {
	CaseID            string   `json:"case_id"`
	Program           string   `json:"program"`
	Boundary          string   `json:"boundary"`
	AttemptOutcomes   []string `json:"attempt_outcomes"`
	ObservationDigest string   `json:"observation_digest,omitempty"`
	ElapsedMillis     uint64   `json:"elapsed_millis"`
}

type rawSignoffFacadeObservation struct {
	FormatVersion                string           `json:"format_version"`
	TargetDigest                 string           `json:"target_digest"`
	SubjectRevision              string           `json:"subject_revision"`
	CaseCatalogDigest            string           `json:"case_catalog_digest"`
	ClosureBundleDigest          string           `json:"closure_bundle_digest"`
	PlatformMatrixDigest         string           `json:"platform_matrix_digest"`
	ArtifactManifestDigest       string           `json:"artifact_manifest_digest"`
	ArtifactPayloadDigest        string           `json:"artifact_payload_digest"`
	ScannerReportDigest          string           `json:"scanner_report_digest"`
	EngineIdentityDigest         string           `json:"engine_identity_digest"`
	BaselineDigest               string           `json:"baseline_digest"`
	ArtifactConstructions        uint32           `json:"artifact_constructions"`
	ArtifactImports              uint32           `json:"artifact_imports"`
	OrchestrationEngineStarts    uint32           `json:"orchestration_engine_starts"`
	ExactTargetEngineStarts      uint32           `json:"exact_target_engine_starts"`
	ExactTargetEngineStops       uint32           `json:"exact_target_engine_stops"`
	RustBaselineMaterializations uint32           `json:"rust_baseline_materializations"`
	UnrelatedActions             uint32           `json:"unrelated_actions"`
	Cases                        []rawSignoffCase `json:"cases"`
	ArtifactMillis               uint64           `json:"artifact_millis"`
	SecurityScanMillis           uint64           `json:"security_scan_millis"`
	EngineStartupMillis          uint64           `json:"engine_startup_millis"`
	RustInstallationMillis       uint64           `json:"rust_installation_millis"`
	CaseExecutionMillis          uint64           `json:"case_execution_millis"`
	CleanupMillis                uint64           `json:"cleanup_millis"`
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
	// Canonical three-OS portable platform matrix.
	platformJSON string,
	// Previously exported exact-target bundle; absent only for a non-authoritative Build run.
	// +optional
	artifact *dagger.File,
) (string, error) {
	inputs, err := t.admitSignoffInputs(ctx, planJSON, catalogJSON, closureJSON, platformJSON, artifact != nil)
	if err != nil {
		return "", err
	}

	source := t.Source().
		WithFile(
			"crates/dagger-sdk/examples/signoff_core_conformance.rs",
			t.Ws.File("toolchains/rust-sdk-dev/testdata/core_conformance.rs"),
		).
		WithFile(
			"crates/dagger-sdk/examples/signoff_scenario_conformance.rs",
			t.Ws.File("toolchains/rust-sdk-dev/testdata/scenario_conformance.rs"),
		)
	artifactStarted := time.Now()
	var target *verifiedSignoffTarget
	var constructions, imports uint32
	if artifact == nil {
		built, err := t.SignoffArtifact(ctx, inputs.artifactPlanJSON)
		if err != nil {
			return "", err
		}
		target = &verifiedSignoffTarget{
			container: built.Target,
			cli:       built.CLI,
			payload:   built.Payload,
			manifest:  built.Manifest,
		}
		constructions = 1
	} else {
		target, err = t.importSignoffArtifact(ctx, inputs.artifactPlanJSON, artifact)
		if err != nil {
			return "", err
		}
		imports = 1
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
	artifactMillis := positiveMillis(time.Since(artifactStarted))

	scanStarted := time.Now()
	scannerReport, err := scanSignoffPayload(ctx, target.payload)
	if err != nil {
		return "", err
	}
	scannerDigest := sha256.Sum256([]byte(scannerReport))
	scanMillis := positiveMillis(time.Since(scanStarted))

	engineStarted := time.Now()
	baseline := target.installedRustBaseline(source)
	installedConfig, err := baseline.runner.File("/work/dagger.toml").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("materialize sole installed Rust baseline: %w", err)
	}
	engineStartupMillis := positiveMillis(time.Since(engineStarted))
	installStarted := time.Now()
	baselineDirectoryDigest, err := baseline.runner.Directory("/work").Digest(ctx)
	if err != nil {
		return "", fmt.Errorf("identify sole installed Rust baseline: %w", err)
	}
	installationMillis := positiveMillis(time.Since(installStarted))
	baselineIdentity := sha256.Sum256([]byte(installedConfig + "\x00" + baselineDirectoryDigest))
	engineIdentity := sha256.Sum256([]byte(inputs.plan.TargetDigest + "\x00" + coreTargetRevision + "\x00" + coreTargetVersion + "\x00" + manifestContents))

	programs := make([]signoffmodel.Program, len(inputs.routes))
	caseByProgram := make(map[string]string, len(inputs.routes))
	for index, route := range inputs.routes {
		programs[index] = route.Program
		caseByProgram[route.Program.Key()] = route.CaseID
	}
	caseStarted := time.Now()
	results, err := signoffmodel.ExecuteBounded(ctx, programs, int(inputs.plan.MaximumConcurrency), func(runCtx context.Context, program signoffmodel.Program) rawSignoffCase {
		spec := inputs.registry[program.Key()]
		return runSignoffCase(runCtx, baseline, caseByProgram[program.Key()], program, spec)
	})
	if err != nil {
		return "", fmt.Errorf("execute bounded sign-off catalog: %w", err)
	}
	cases := make([]rawSignoffCase, len(results))
	for _, result := range results {
		cases[result.Index] = result.Value
	}
	caseMillis := positiveMillis(time.Since(caseStarted))

	cleanupStarted := time.Now()
	if err := baseline.stop(ctx); err != nil {
		return "", fmt.Errorf("stop sole exact-target engine: %w", err)
	}
	cleanupMillis := positiveMillis(time.Since(cleanupStarted))

	observation := rawSignoffFacadeObservation{
		FormatVersion: "1.0.0", TargetDigest: inputs.plan.TargetDigest,
		SubjectRevision: inputs.plan.SubjectRevision, CaseCatalogDigest: inputs.plan.CaseCatalogDigest,
		ClosureBundleDigest: inputs.plan.ClosureBundleDigest, PlatformMatrixDigest: inputs.platformDigest,
		ArtifactManifestDigest: manifestDigest, ArtifactPayloadDigest: expectedPayload,
		ScannerReportDigest:   fmt.Sprintf("sha256:%x", scannerDigest),
		EngineIdentityDigest:  fmt.Sprintf("sha256:%x", engineIdentity),
		BaselineDigest:        fmt.Sprintf("sha256:%x", baselineIdentity),
		ArtifactConstructions: constructions, ArtifactImports: imports,
		OrchestrationEngineStarts: 1, ExactTargetEngineStarts: 1, ExactTargetEngineStops: 1,
		RustBaselineMaterializations: 1, UnrelatedActions: 0, Cases: cases,
		ArtifactMillis: artifactMillis, SecurityScanMillis: scanMillis,
		EngineStartupMillis: engineStartupMillis, RustInstallationMillis: installationMillis,
		CaseExecutionMillis: caseMillis, CleanupMillis: cleanupMillis,
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return "", fmt.Errorf("encode raw Rust sign-off observation: %w", err)
	}
	return string(encoded), nil
}

func (t *RustSdkDev) admitSignoffInputs(
	ctx context.Context,
	planJSON, catalogJSON, closureJSON, platformJSON string,
	hasArtifact bool,
) (*admittedSignoffInputs, error) {
	for name, value := range map[string]string{
		"plan": planJSON, "catalog": catalogJSON, "closure": closureJSON, "platform": platformJSON,
	} {
		if err := requireCanonicalJSON([]byte(value)); err != nil {
			return nil, fmt.Errorf("%s sign-off input is not canonical: %w", name, err)
		}
	}
	var plan signoffPlanWire
	var catalog signoffCatalogWire
	var closure signoffClosureWire
	var platform signoffPlatformWire
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return nil, fmt.Errorf("decode sign-off plan: %w", err)
	}
	if err := json.Unmarshal([]byte(catalogJSON), &catalog); err != nil {
		return nil, fmt.Errorf("decode sign-off catalog: %w", err)
	}
	if err := json.Unmarshal([]byte(closureJSON), &closure); err != nil {
		return nil, fmt.Errorf("decode implementation closure: %w", err)
	}
	if err := json.Unmarshal([]byte(platformJSON), &platform); err != nil {
		return nil, fmt.Errorf("decode portable platform matrix: %w", err)
	}
	if plan.FormatVersion != "1.0.0" || catalog.FormatVersion != "1.0.0" || closure.FormatVersion != "1.0.0" || platform.FormatVersion != "1.0.0" ||
		!isCanonicalSHA256(plan.TargetDigest) || len(plan.SubjectRevision) != 40 || plan.TargetDigest != catalog.TargetDigest ||
		plan.TargetDigest != closure.TargetDigest || plan.TargetDigest != platform.TargetDigest || plan.ClosureBundleDigest != closure.BundleDigest ||
		!isCanonicalSHA256(plan.CaseCatalogDigest) || !isCanonicalSHA256(platform.MatrixDigest) || len(catalog.Cases) != 672 ||
		len(platform.NativeObservations) != 3 || plan.MaximumConcurrency == 0 || plan.MaximumConcurrency > 64 {
		return nil, fmt.Errorf("sign-off plan catalog closure or platform identity is incomplete")
	}
	artifactPlanJSON, err := canonicalizeJSON(plan.ArtifactPlan)
	if err != nil {
		return nil, fmt.Errorf("canonicalize nested artifact plan: %w", err)
	}
	if err := validateMaterialization(plan.ArtifactPlan, hasArtifact); err != nil {
		return nil, err
	}
	observableJSON, err := t.Ws.File("sdk/rust/completeness/conformance-observable-programs.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read closed observable sign-off registry: %w", err)
	}
	observable, err := signoffmodel.DecodeObservablePrograms([]byte(observableJSON))
	if err != nil {
		return nil, err
	}
	if observable.CaseCatalogDigest != plan.CaseCatalogDigest {
		return nil, fmt.Errorf("observable registry and run plan name different case catalogs")
	}
	scenarioCandidates, err := t.Ws.File("sdk/rust/completeness/conformance-scenario-candidates.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Rust scenario candidate queue: %w", err)
	}
	scenarioRegistry, err := t.Ws.File("sdk/rust/completeness/conformance-scenario-realizations.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Rust scenario realization registry: %w", err)
	}
	scenarioRunner, err := t.Ws.File("toolchains/rust-sdk-dev/testdata/scenario_conformance.rs").Contents(ctx)
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
	routes, err := signoffmodel.DecodeCaseRoutes([]byte(catalogJSON), registry)
	if err != nil {
		return nil, err
	}
	return &admittedSignoffInputs{
		plan: plan, artifactPlanJSON: artifactPlanJSON, routes: routes,
		registry: registry, platformDigest: platform.MatrixDigest,
	}, nil
}

func runSignoffCase(
	ctx context.Context,
	baseline *installedSignoffBaseline,
	caseID string,
	program signoffmodel.Program,
	spec signoffmodel.ProgramSpec,
) rawSignoffCase {
	started := time.Now()
	result := rawSignoffCase{CaseID: caseID, Program: program.Key(), Boundary: string(spec.Boundary)}
	runner, err := baseline.programBranch(program, spec, 1)
	if err == nil {
		executor := spec.Executor
		if executor == nil {
			// A catalog route without a concrete production assertion is deliberately a
			// failed case. Boundary reachability cannot prove the route's semantic predicate.
			err = fmt.Errorf("sign-off program %q has no concrete production executor", program.Key())
		} else if executor.Expected.Category == "" || executor.Expected.Operation == "" ||
			(executor.Kind != signoffmodel.ExecutorScenarioConformance && executor.Selector != program.Value) {
			err = fmt.Errorf("sign-off program %q has an incomplete concrete executor", program.Key())
		} else {
			switch executor.Kind {
			case signoffmodel.ExecutorCoreConformance:
				result.ObservationDigest, err = runCoreConformanceCase(ctx, runner, *executor)
			case signoffmodel.ExecutorEngineIntegration:
				result.ObservationDigest, err = runEngineIntegrationSignoffCase(ctx, runner, *executor)
			case signoffmodel.ExecutorScenarioConformance:
				result.ObservationDigest, err = runScenarioConformanceCase(ctx, runner, *executor)
			default:
				err = fmt.Errorf("sign-off program %q names unknown executor %q", program.Key(), executor.Kind)
			}
		}
	}
	if err != nil {
		result.AttemptOutcomes = []string{"assertion-failed"}
	} else {
		result.AttemptOutcomes = []string{"passed"}
	}
	result.ElapsedMillis = positiveMillis(time.Since(started))
	return result
}

type scenarioConformanceObservation struct {
	RealizationID   string `json:"realization_id"`
	ScenarioID      string `json:"scenario_id"`
	RealizationKind string `json:"realization_kind"`
	Observation     string `json:"observation"`
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
) (string, error) {
	stdout, err := runner.
		WithEnvVariable("DAGGER_RUST_SCENARIO_REALIZATION", executor.Selector).
		WithExec([]string{
			"dagger", "run", "cargo", "run", "--manifest-path", "/src/sdk/rust/Cargo.toml",
			"-p", "dagger-sdk", "--example", "signoff_scenario_conformance", "--locked",
		}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	var evidence scenarioConformanceObservationSet
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &evidence); err != nil {
		return "", fmt.Errorf("decode selected Rust scenario observation: %w", err)
	}
	if evidence.FormatVersion != 1 || evidence.TargetRevision != coreTargetRevision || evidence.TargetVersion != coreTargetVersion {
		return "", fmt.Errorf("selected Rust scenario observation names a different exact target")
	}
	if len(evidence.Observations) != 1 {
		return "", fmt.Errorf("selected Rust scenario executor returned %d observations, want exactly one", len(evidence.Observations))
	}
	observation := evidence.Observations[0]
	if observation.RealizationID != executor.Selector || observation.ScenarioID != executor.Expected.Operation ||
		observation.RealizationKind != executor.Expected.Category || strings.TrimSpace(observation.Observation) == "" {
		return "", fmt.Errorf("selected Rust scenario observation differs from executor %q", executor.Selector)
	}
	digest := sha256.Sum256([]byte(stdout))
	return fmt.Sprintf("sha256:%x", digest), nil
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
) (string, error) {
	stdout, err := runner.
		WithEnvVariable("DAGGER_RUST_SIGNOFF_SELECTOR", executor.Selector).
		WithExec([]string{
			"dagger", "run", "cargo", "run", "--manifest-path", "/src/sdk/rust/Cargo.toml",
			"-p", "dagger-sdk", "--example", "signoff_core_conformance", "--locked",
		}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	var evidence coreConformanceObservationSet
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &evidence); err != nil {
		return "", fmt.Errorf("decode selected Rust core observation: %w", err)
	}
	if evidence.FormatVersion != 1 || evidence.TargetRevision != coreTargetRevision || evidence.TargetVersion != coreTargetVersion {
		return "", fmt.Errorf("selected Rust core observation names a different exact target")
	}
	if len(evidence.Observations) != 1 {
		return "", fmt.Errorf("selected Rust core executor returned %d observations, want exactly one", len(evidence.Observations))
	}
	observation := evidence.Observations[0]
	if observation.Selector != executor.Selector || observation.Category != executor.Expected.Category || observation.Operation != executor.Expected.Operation {
		return "", fmt.Errorf("selected Rust core observation differs from executor %q", executor.Selector)
	}
	digest := sha256.Sum256([]byte(stdout))
	return fmt.Sprintf("sha256:%x", digest), nil
}

func runEngineIntegrationSignoffCase(
	ctx context.Context,
	runner *dagger.Container,
	executor signoffmodel.ExecutorDefinition,
) (string, error) {
	if executor.Expected.Category != "case-pass" || executor.Expected.Operation != executor.Selector {
		return "", fmt.Errorf("engine-integration executor %q has a mismatched expected observation", executor.Selector)
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
		return "", err
	}
	if strings.TrimSpace(identity) == "" {
		return "", fmt.Errorf("engine-integration executor %q returned no asserted identity", executor.Selector)
	}
	return stableCaseObservation(executor.Selector, identity), nil
}

func scanSignoffPayload(ctx context.Context, payload *dagger.File) (string, error) {
	scanner := dag.Container().
		From(signoffScannerImage).
		WithEntrypoint([]string{}).
		WithMountedFile("/scan/engine.oci.tar.zst", payload).
		WithMountedCache("/root/.cache/trivy", dag.CacheVolume("rust-signoff-trivy-0.69.3")).
		WithExec([]string{
			"trivy", "image", "--input", "/scan/engine.oci.tar.zst", "--scanners", "vuln",
			"--format", "json", "--output", "/scan/report.json", "--exit-code", "1",
			"--severity", "HIGH,CRITICAL",
		}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	exitCode, err := scanner.ExitCode(ctx)
	if err != nil {
		return "", fmt.Errorf("inspect exact-payload scanner outcome: %w", err)
	}
	report, reportErr := scanner.File("/scan/report.json").Contents(ctx)
	if reportErr != nil {
		return "", fmt.Errorf("read exact-payload scanner report: %w", reportErr)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("exact-payload vulnerability gate failed")
	}
	return report, nil
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
		Manifest:      assembler.File(signoffManifestPath),
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
		"--manifest-output", signoffManifestPath,
	})
	verifiedPayload := verifier.File(signoffImportedPath)
	imported := dag.Container().Import(verifiedPayload)
	return &verifiedSignoffTarget{
		container: imported,
		cli:       imported.File(signoffCliPath),
		payload:   verifiedPayload,
		manifest:  verifier.File(signoffManifestPath),
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
func (target *verifiedSignoffTarget) installedRustBaseline(source *dagger.Directory) *installedSignoffBaseline {
	service := target.container.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{
			Args:                     []string{"--addr", "tcp://0.0.0.0:1234"},
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})
	runner := dag.Container().
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithDirectory("/src/sdk/rust", source).
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
	spec signoffmodel.ProgramSpec,
	attempt uint32,
) (*dagger.Container, error) {
	if attempt == 0 {
		return nil, fmt.Errorf("sign-off case attempt must be one-based")
	}
	if spec.Program != program {
		return nil, fmt.Errorf("sign-off program specification differs from %q", program.Key())
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
