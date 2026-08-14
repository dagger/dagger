package signoff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
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

// InfrastructureError marks one typed operational failure without parsing process text.
type InfrastructureError struct {
	Class InfrastructureFailureClass
	Err   error
}

func (failure *InfrastructureError) Error() string {
	if failure.Err == nil {
		return "typed infrastructure failure"
	}
	return failure.Err.Error()
}

func (failure *InfrastructureError) Unwrap() error {
	return failure.Err
}

// MarkInfrastructureFailure lets a production boundary name only the closed retry vocabulary.
func MarkInfrastructureFailure(class InfrastructureFailureClass, err error) error {
	if err == nil {
		return nil
	}
	return &InfrastructureError{Class: class, Err: err}
}

// ClassifyAttemptError keeps semantic assertion failures terminal. Only typed failures and
// standard-library transport closure identities enter the infrastructure retry vocabulary.
func ClassifyAttemptError(err error) AttemptOutcome {
	if err == nil {
		return AttemptOutcome{Kind: OutcomePassed}
	}
	var failure *InfrastructureError
	if errors.As(err, &failure) && knownInfrastructureFailure(failure.Class) {
		return AttemptOutcome{Kind: OutcomeInfrastructure, InfrastructureClass: failure.Class}
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return AttemptOutcome{Kind: OutcomeInfrastructure, InfrastructureClass: FailureOrchestrationTransport}
	}
	return AttemptOutcome{Kind: OutcomeAssertion}
}

// RetryPolicy permits only named infrastructure classes within a total attempt bound.
type RetryPolicy struct {
	MaximumAttempts uint32
	Retryable       map[InfrastructureFailureClass]struct{}
}

// RunAttempts retains every attempt and stops permanently after pass, assertion, or exhaustion.
func RunAttempts(policy RetryPolicy, run func(attempt uint32) AttemptOutcome) ([]AttemptOutcome, error) {
	if err := validateRetryPolicy(policy); err != nil {
		return nil, err
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

// AttemptResult retains one isolated attempt's value, typed outcome, and elapsed duration.
type AttemptResult[T any] struct {
	Attempt uint32
	Value   T
	Outcome AttemptOutcome
	Err     error
	Elapsed time.Duration
}

// ExecutePolicyAttempts applies the admitted per-attempt timeout and retry policy. The one-based
// attempt number is the only namespace input production runners may use for fresh mutable state.
func ExecutePolicyAttempts[T any](
	ctx context.Context,
	policy ExecutionPolicy,
	run func(context.Context, uint32) (T, error),
) ([]AttemptResult[T], error) {
	if policy.Timeout <= 0 {
		return nil, fmt.Errorf("case execution requires a positive per-attempt timeout")
	}
	if err := validateRetryPolicy(policy.Retry); err != nil {
		return nil, err
	}
	results := make([]AttemptResult[T], 0, policy.Retry.MaximumAttempts)
	_, err := RunAttempts(policy.Retry, func(attempt uint32) AttemptOutcome {
		attemptCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
		started := time.Now()
		value, runErr := run(attemptCtx, attempt)
		elapsed := time.Since(started)
		cancel()
		outcome := ClassifyAttemptError(runErr)
		results = append(results, AttemptResult[T]{
			Attempt: attempt,
			Value:   value,
			Outcome: outcome,
			Err:     runErr,
			Elapsed: elapsed,
		})
		return outcome
	})
	if ctx.Err() != nil {
		return results, ctx.Err()
	}
	return results, err
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

// ScheduledValue couples work with the isolation contract admitted for it.
type ScheduledValue[T any] struct {
	Value T
	Class ConcurrencyClass
}

// ExecutePolicyBounded preserves stable ordering while ensuring exclusive-mutation work never
// overlaps any sibling. Shared-read-only and isolated-workspace work may use the bounded fan-out.
func ExecutePolicyBounded[T any, R any](
	ctx context.Context,
	values []ScheduledValue[T],
	maximum int,
	run func(context.Context, T) R,
) ([]IndexedResult[R], error) {
	if maximum <= 0 {
		return nil, fmt.Errorf("case fan-out requires positive bounded concurrency")
	}
	for _, value := range values {
		if value.Class != ConcurrencySharedReadOnly && value.Class != ConcurrencyIsolatedWorkspace && value.Class != ConcurrencyExclusiveMutation {
			return nil, fmt.Errorf("case fan-out contains an unknown concurrency class")
		}
	}
	results := make([]IndexedResult[R], len(values))
	slots := make(chan struct{}, maximum)
	var isolation sync.RWMutex
	var group sync.WaitGroup
	for index, scheduled := range values {
		group.Add(1)
		go func() {
			defer group.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if scheduled.Class == ConcurrencyExclusiveMutation {
				isolation.Lock()
				defer isolation.Unlock()
			} else {
				isolation.RLock()
				defer isolation.RUnlock()
			}
			results[index] = IndexedResult[R]{Index: index, Value: run(ctx, scheduled.Value)}
		}()
	}
	group.Wait()
	return results, nil
}
