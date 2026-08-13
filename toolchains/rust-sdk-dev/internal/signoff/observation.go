package signoff

import (
	"context"
	"fmt"
	"sync"
)

// InfrastructureFailureClass is the only retryable operational vocabulary.
type InfrastructureFailureClass string

const (
	FailureOrchestrationTransport InfrastructureFailureClass = "orchestration-transport"
	FailureImmutableRemoteFetch   InfrastructureFailureClass = "immutable-remote-fetch"
	FailureRunnerCapacity         InfrastructureFailureClass = "runner-capacity"
)

// OutcomeKind separates subject assertions from the closed infrastructure vocabulary.
type OutcomeKind string

const (
	OutcomePassed         OutcomeKind = "passed"
	OutcomeAssertion      OutcomeKind = "assertion-failed"
	OutcomeInfrastructure OutcomeKind = "infrastructure-failed"
)

// AttemptOutcome is the bounded raw result used before Rust canonical admission.
type AttemptOutcome struct {
	Kind                OutcomeKind
	InfrastructureClass InfrastructureFailureClass
}

// RetryPolicy permits only named infrastructure classes within a total attempt bound.
type RetryPolicy struct {
	MaximumAttempts uint32
	Retryable       map[InfrastructureFailureClass]struct{}
}

// RunAttempts retains every attempt and stops permanently after pass, assertion, or exhaustion.
func RunAttempts(policy RetryPolicy, run func(attempt uint32) AttemptOutcome) ([]AttemptOutcome, error) {
	if policy.MaximumAttempts == 0 {
		return nil, fmt.Errorf("retry policy requires a positive attempt bound")
	}
	for class := range policy.Retryable {
		if !knownInfrastructureFailure(class) {
			return nil, fmt.Errorf("retry policy contains an unknown infrastructure class")
		}
	}
	outcomes := make([]AttemptOutcome, 0, policy.MaximumAttempts)
	for attempt := uint32(1); attempt <= policy.MaximumAttempts; attempt++ {
		outcome := run(attempt)
		outcomes = append(outcomes, outcome)
		switch outcome.Kind {
		case OutcomePassed, OutcomeAssertion:
			return outcomes, nil
		case OutcomeInfrastructure:
			if !knownInfrastructureFailure(outcome.InfrastructureClass) {
				return nil, fmt.Errorf("attempt returned an unknown infrastructure class")
			}
			if _, retryable := policy.Retryable[outcome.InfrastructureClass]; !retryable || attempt == policy.MaximumAttempts {
				return outcomes, nil
			}
		default:
			return nil, fmt.Errorf("attempt returned an unknown outcome")
		}
	}
	return outcomes, nil
}

func knownInfrastructureFailure(class InfrastructureFailureClass) bool {
	return class == FailureOrchestrationTransport ||
		class == FailureImmutableRemoteFetch ||
		class == FailureRunnerCapacity
}

// IndexedResult preserves registry order independently from case completion order.
type IndexedResult[T any] struct {
	Index int
	Value T
}

// ExecuteBounded runs every case despite sibling failure and returns stable indexed results.
func ExecuteBounded[T any](
	ctx context.Context,
	programs []Program,
	maximum int,
	run func(context.Context, Program) T,
) ([]IndexedResult[T], error) {
	if maximum <= 0 {
		return nil, fmt.Errorf("case fan-out requires positive bounded concurrency")
	}
	results := make([]IndexedResult[T], len(programs))
	slots := make(chan struct{}, maximum)
	var group sync.WaitGroup
	for index, program := range programs {
		group.Add(1)
		go func() {
			defer group.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[index] = IndexedResult[T]{Index: index, Value: run(ctx, program)}
		}()
	}
	group.Wait()
	return results, nil
}
