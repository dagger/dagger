package signoff

import (
	"testing"
	"time"
)

func TestProgramPolicyRejectsNetworkWideningAndUnknownIsolation(t *testing.T) {
	t.Parallel()
	base := ExecutionPolicy{
		Timeout: time.Minute,
		Retry:   RetryPolicy{MaximumAttempts: 1, Retryable: map[InfrastructureFailureClass]struct{}{}},
		Network: NetworkEngineOnly, Concurrency: ConcurrencyIsolatedWorkspace,
	}
	if err := base.ValidateFor(Program{Kind: ProgramModuleAuthoring, Value: "registration"}); err != nil {
		t.Fatalf("validate engine-only module policy: %v", err)
	}
	widened := base
	widened.Network = NetworkManifestAndEngine
	if err := widened.ValidateFor(Program{Kind: ProgramModuleAuthoring, Value: "registration"}); err == nil {
		t.Fatalf("engine-only module admitted manifest network access")
	}
	remote := base
	remote.Network = NetworkImmutableRemote
	if err := remote.ValidateFor(Program{Kind: ProgramStandaloneClient, Value: "pinned-remote-client"}); err != nil {
		t.Fatalf("validate immutable remote client policy: %v", err)
	}
	unknown := base
	unknown.Concurrency = ConcurrencyClass("best-effort")
	if err := unknown.ValidateFor(Program{Kind: ProgramModuleAuthoring, Value: "registration"}); err == nil {
		t.Fatalf("unknown concurrency class was admitted")
	}
}

func TestCatalogRetryVocabularyMapsOnlyToFacadeOutcomes(t *testing.T) {
	t.Parallel()
	for catalog, expected := range map[string]InfrastructureFailureClass{
		"orchestration-transport-lost":          FailureOrchestrationTransport,
		"immutable-remote-unavailable":          FailureImmutableRemoteFetch,
		"workspace-materialization-interrupted": FailureRunnerCapacity,
	} {
		actual, err := catalogInfrastructureFailure(catalog)
		if err != nil || actual != expected {
			t.Fatalf("map catalog retry class %q: actual=%q err=%v", catalog, actual, err)
		}
	}
	if _, err := catalogInfrastructureFailure("transient"); err == nil {
		t.Fatalf("catch-all transient retry class was admitted")
	}
}
