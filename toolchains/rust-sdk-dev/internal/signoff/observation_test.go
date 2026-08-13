package signoff

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestBoundedExecutionRetainsStableIndexesAndEverySibling(t *testing.T) {
	t.Parallel()
	programs := make([]Program, 0, len(FixedProgramRegistry()))
	for _, spec := range FixedProgramRegistry() {
		programs = append(programs, spec.Program)
	}
	var active atomic.Int32
	var peak atomic.Int32
	results, err := ExecuteBounded(context.Background(), programs, 4, func(_ context.Context, program Program) string {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := peak.Load()
			if observed >= current || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		return program.Key()
	})
	if err != nil {
		t.Fatalf("execute bounded programs: %v", err)
	}
	if len(results) != len(programs) || peak.Load() > 4 {
		t.Fatalf("bounded result count=%d peak=%d", len(results), peak.Load())
	}
	for index, result := range results {
		if result.Index != index || result.Value != programs[index].Key() {
			t.Fatalf("result %d lost stable registry indexing: %+v", index, result)
		}
	}
}

func TestRetryStopsAfterAssertionAndRetainsInfrastructureHistory(t *testing.T) {
	t.Parallel()
	policy := RetryPolicy{
		MaximumAttempts: 3,
		Retryable: map[InfrastructureFailureClass]struct{}{
			FailureOrchestrationTransport: {},
		},
	}
	outcomes, err := RunAttempts(policy, func(attempt uint32) AttemptOutcome {
		if attempt == 1 {
			return AttemptOutcome{Kind: OutcomeInfrastructure, InfrastructureClass: FailureOrchestrationTransport}
		}
		return AttemptOutcome{Kind: OutcomeAssertion}
	})
	if err != nil {
		t.Fatalf("run closed attempts: %v", err)
	}
	if len(outcomes) != 2 || outcomes[0].Kind != OutcomeInfrastructure || outcomes[1].Kind != OutcomeAssertion {
		t.Fatalf("retry history was rewritten or assertion was not absorbing: %+v", outcomes)
	}
}

func TestUnknownAndUndeclaredInfrastructureNeverRetry(t *testing.T) {
	t.Parallel()
	policy := RetryPolicy{
		MaximumAttempts: 3,
		Retryable: map[InfrastructureFailureClass]struct{}{
			FailureImmutableRemoteFetch: {},
		},
	}
	calls := 0
	outcomes, err := RunAttempts(policy, func(uint32) AttemptOutcome {
		calls++
		return AttemptOutcome{Kind: OutcomeInfrastructure, InfrastructureClass: FailureRunnerCapacity}
	})
	if err != nil {
		t.Fatalf("run closed attempts: %v", err)
	}
	if calls != 1 || len(outcomes) != 1 {
		t.Fatalf("undeclared infrastructure failure retried: calls=%d outcomes=%d", calls, len(outcomes))
	}
	if _, err := RunAttempts(policy, func(uint32) AttemptOutcome {
		return AttemptOutcome{Kind: OutcomeKind("transient")}
	}); err == nil {
		t.Fatalf("unknown catch-all transient outcome was accepted")
	}
}
