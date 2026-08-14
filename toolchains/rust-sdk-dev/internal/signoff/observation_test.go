package signoff

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
		MaximumAttempts: 2,
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
		MaximumAttempts: 2,
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

func TestPolicyAttemptsUseFreshNumbersTimeoutsAndInfrastructureOnlyRetry(t *testing.T) {
	t.Parallel()
	policy := ExecutionPolicy{
		Timeout: 10 * time.Millisecond,
		Retry: RetryPolicy{
			MaximumAttempts: 2,
			Retryable: map[InfrastructureFailureClass]struct{}{
				FailureOrchestrationTransport: {},
			},
		},
		Network: NetworkManifestAndEngine, Concurrency: ConcurrencyExclusiveMutation,
	}
	var observed []uint32
	var firstDeadline bool
	results, err := ExecutePolicyAttempts(context.Background(), policy, func(ctx context.Context, attempt uint32) (string, error) {
		observed = append(observed, attempt)
		if attempt == 1 {
			deadline, ok := ctx.Deadline()
			if !ok {
				return "", errors.New("attempt context has no deadline")
			}
			firstDeadline = !deadline.IsZero()
			<-ctx.Done()
			return "", MarkInfrastructureFailure(FailureOrchestrationTransport, ctx.Err())
		}
		return fmt.Sprintf("namespace-%d", attempt), nil
	})
	if err != nil {
		t.Fatalf("execute policy attempts: %v", err)
	}
	if len(results) != 2 || len(observed) != 2 || observed[0] != 1 || observed[1] != 2 {
		t.Fatalf("attempt identities were not fresh and contiguous: results=%+v observed=%v", results, observed)
	}
	if results[0].Outcome.Kind != OutcomeInfrastructure || results[1].Outcome.Kind != OutcomePassed || results[1].Value != "namespace-2" {
		t.Fatalf("attempt history does not retain infrastructure then pass: %+v", results)
	}
	if results[0].Err == nil || results[1].Err != nil {
		t.Fatalf("attempt history did not retain the actual error boundary: %+v", results)
	}
	if !firstDeadline || results[0].Elapsed <= 0 {
		t.Fatalf("first attempt did not receive its declared timeout: deadline=%t elapsed=%s", firstDeadline, results[0].Elapsed)
	}

	calls := 0
	assertions, err := ExecutePolicyAttempts(context.Background(), policy, func(context.Context, uint32) (string, error) {
		calls++
		return "", errors.New("semantic mismatch")
	})
	if err != nil {
		t.Fatalf("execute assertion attempt: %v", err)
	}
	if calls != 1 || len(assertions) != 1 || assertions[0].Outcome.Kind != OutcomeAssertion || assertions[0].Err == nil {
		t.Fatalf("assertion failure was retried: calls=%d results=%+v", calls, assertions)
	}
}

func TestPolicyAttemptsPropagateOuterCancellationInsteadOfClaimingAssertion(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	policy := ExecutionPolicy{
		Timeout: time.Second,
		Retry:   RetryPolicy{MaximumAttempts: 1, Retryable: map[InfrastructureFailureClass]struct{}{}},
		Network: NetworkEngineOnly, Concurrency: ConcurrencySharedReadOnly,
	}
	_, err := ExecutePolicyAttempts(ctx, policy, func(ctx context.Context, _ uint32) (struct{}, error) {
		return struct{}{}, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("outer cancellation became case evidence: %v", err)
	}
}

func TestPolicySchedulerMakesExclusiveMutationGloballyExclusive(t *testing.T) {
	t.Parallel()
	values := make([]ScheduledValue[ConcurrencyClass], 0, 25)
	for index := 0; index < 25; index++ {
		class := ConcurrencyIsolatedWorkspace
		if index == 8 || index == 16 {
			class = ConcurrencyExclusiveMutation
		}
		values = append(values, ScheduledValue[ConcurrencyClass]{Value: class, Class: class})
	}
	var active atomic.Int32
	var exclusive atomic.Int32
	var violation atomic.Bool
	var peak atomic.Int32
	var gate sync.WaitGroup
	gate.Add(1)
	go func() {
		time.Sleep(time.Millisecond)
		gate.Done()
	}()
	results, err := ExecutePolicyBounded(context.Background(), values, 6, func(_ context.Context, class ConcurrencyClass) string {
		gate.Wait()
		current := active.Add(1)
		defer active.Add(-1)
		if class == ConcurrencyExclusiveMutation {
			exclusive.Add(1)
			defer exclusive.Add(-1)
			if current != 1 {
				violation.Store(true)
			}
		} else if exclusive.Load() != 0 {
			violation.Store(true)
		}
		for {
			prior := peak.Load()
			if prior >= current || peak.CompareAndSwap(prior, current) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		return string(class)
	})
	if err != nil {
		t.Fatalf("execute policy scheduler: %v", err)
	}
	if len(results) != len(values) || violation.Load() || peak.Load() > 6 || peak.Load() < 2 {
		t.Fatalf("scheduler isolation or fan-out mismatch: results=%d violation=%t peak=%d", len(results), violation.Load(), peak.Load())
	}
	for index, result := range results {
		if result.Index != index || result.Value != string(values[index].Value) {
			t.Fatalf("scheduler lost stable result %d: %+v", index, result)
		}
	}
}
