package signoff

import (
	"os"
	"testing"
)

func TestObservableRegistryMatchesEveryApplicableIntegrationFixture(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../../../sdk/rust/completeness/conformance-observable-programs.json")
	if err != nil {
		t.Fatalf("read observable program registry: %v", err)
	}
	observable, err := DecodeObservablePrograms(data)
	if err != nil {
		t.Fatalf("decode observable program registry: %v", err)
	}
	registry, err := CompleteProgramRegistry(observable)
	if err != nil {
		t.Fatalf("join complete program registry: %v", err)
	}
	if len(observable.Programs) != 612 || len(registry) != 675 {
		t.Fatalf("program counts: observable=%d complete=%d, want 612 and 675", len(observable.Programs), len(registry))
	}
	if err := RequireConcretePrograms(registry); err == nil {
		t.Fatalf("route-only registry must not be admitted as executable conformance")
	}
	concrete := 0
	for _, spec := range registry {
		if spec.Executor != nil {
			concrete++
		}
	}
	if concrete != 63 {
		t.Fatalf("concrete program count: got %d, want 63", concrete)
	}
	counts := map[ProgramBoundary]int{}
	for _, spec := range observable.Programs {
		wantWorkspace := WorkspaceBaselineBranch
		if spec.Boundary == BoundaryPackagedRuntime {
			wantWorkspace = WorkspaceExternalPackage
		}
		if spec.Program.Kind != ProgramIntegration || spec.Workspace != wantWorkspace {
			t.Fatalf("observable program %q escaped its Rust-owned isolated boundary", spec.Program.Key())
		}
		counts[spec.Boundary]++
	}
	if counts[BoundarySharedCLI] != 154 || counts[BoundaryModuleRuntime] != 383 || counts[BoundaryPackagedRuntime] != 75 {
		t.Fatalf("unexpected observable boundary partition: %#v", counts)
	}
	ordered := OrderedPrograms(registry)
	if len(ordered) != 675 {
		t.Fatalf("ordered complete registry has %d programs", len(ordered))
	}
	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].Key() >= ordered[index].Key() {
			t.Fatalf("program registry is not strictly ordered")
		}
	}
}

func TestObservableDecoderRejectsUnreviewedRoutes(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../../../sdk/rust/completeness/conformance-observable-programs.json")
	if err != nil {
		t.Fatalf("read observable program registry: %v", err)
	}
	for _, mutate := range []func([]byte) []byte{
		func(data []byte) []byte { return append(data, []byte("{}")...) },
		func(data []byte) []byte {
			return replaceOnce(data, []byte(`"format_version": "1.0.0"`), []byte(`"format_version": "2.0.0"`))
		},
		func(data []byte) []byte {
			return replaceOnce(data, []byte(`"boundary": "shared-baseline-cli"`), []byte(`"boundary": "shell"`))
		},
		func(data []byte) []byte {
			return replaceOnce(data, []byte(`"fixture_id": "fixture/integration/`), []byte(`"fixture_id": "fixture/foreign/`))
		},
	} {
		if _, err := DecodeObservablePrograms(mutate(append([]byte(nil), data...))); err == nil {
			t.Fatalf("accepted malformed observable registry")
		}
	}
}

func replaceOnce(data, old, replacement []byte) []byte {
	for index := 0; index+len(old) <= len(data); index++ {
		match := true
		for offset := range old {
			if data[index+offset] != old[offset] {
				match = false
				break
			}
		}
		if match {
			result := make([]byte, 0, len(data)-len(old)+len(replacement))
			result = append(result, data[:index]...)
			result = append(result, replacement...)
			result = append(result, data[index+len(old):]...)
			return result
		}
	}
	return data
}
