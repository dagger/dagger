package signoff

import (
	"os"
	"testing"
)

func checkedScenarioInputs(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	registry, err := os.ReadFile("../../../../sdk/rust/completeness/conformance-scenario-realizations.json")
	if err != nil {
		t.Fatalf("read scenario realization registry: %v", err)
	}
	candidates, err := os.ReadFile("../../../../sdk/rust/completeness/conformance-scenario-candidates.json")
	if err != nil {
		t.Fatalf("read scenario candidate queue: %v", err)
	}
	runner, err := os.ReadFile("../../testdata/scenario_conformance.rs")
	if err != nil {
		t.Fatalf("read scenario runner: %v", err)
	}
	return registry, candidates, runner
}

func TestCheckedScenarioRegistryIsSourceBoundAndStillPartial(t *testing.T) {
	t.Parallel()
	registry, candidates, runner := checkedScenarioInputs(t)
	decoded, err := DecodeScenarioRealizations(registry, candidates, runner)
	if err != nil {
		t.Fatalf("decode checked realization registry: %v", err)
	}
	if len(decoded.Registrations) != 0 {
		t.Fatalf("checked registry has %d reviewed realizations, want 0 before realization review", len(decoded.Registrations))
	}

	if _, err := DecodeScenarioRealizations(registry, append(candidates, '\n'), runner); err == nil {
		t.Fatalf("accepted a different scenario candidate queue")
	}
	if _, err := DecodeScenarioRealizations(registry, candidates, append(runner, '\n')); err == nil {
		t.Fatalf("accepted different Rust runner source")
	}
}

func TestScenarioRealizationsAttachOneClosedRustExecutor(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../../../sdk/rust/completeness/conformance-observable-programs.json")
	if err != nil {
		t.Fatalf("read observable routes: %v", err)
	}
	observable, err := DecodeObservablePrograms(data)
	if err != nil {
		t.Fatalf("decode observable routes: %v", err)
	}
	registry, err := CompleteProgramRegistry(observable)
	if err != nil {
		t.Fatalf("join program registry: %v", err)
	}
	var scenarioID, programKey string
	for candidateID, candidateProgram := range observable.CasePrograms {
		spec := registry[candidateProgram]
		if spec.Boundary != BoundaryGeneratedCore {
			scenarioID, programKey = candidateID, candidateProgram
			break
		}
	}
	spec := registry[programKey]
	realization := ScenarioRealization{
		ScenarioID: scenarioID, Kind: RealizationReviewedFixture,
		RealizationID: "realization/reviewed/0001", FixtureID: spec.Program.Value,
	}
	joined, err := ApplyScenarioRealizations(registry, observable, ScenarioRealizationCatalog{
		TargetDigest:  observable.TargetDigest,
		Registrations: map[string]ScenarioRealization{scenarioID: realization},
	})
	if err != nil {
		t.Fatalf("apply reviewed realization: %v", err)
	}
	executor := joined[programKey].Executor
	if executor == nil || executor.Kind != ExecutorScenarioConformance ||
		executor.Selector != realization.RealizationID || executor.Expected.Operation != scenarioID {
		t.Fatalf("joined executor differs from reviewed realization: %#v", executor)
	}

	realization.FixtureID = "fixture/integration/wrong"
	if _, err := ApplyScenarioRealizations(registry, observable, ScenarioRealizationCatalog{
		TargetDigest:  observable.TargetDigest,
		Registrations: map[string]ScenarioRealization{scenarioID: realization},
	}); err == nil {
		t.Fatalf("accepted a realization bound to a different reviewed fixture")
	}
}
