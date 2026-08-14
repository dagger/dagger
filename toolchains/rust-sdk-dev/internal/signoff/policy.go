package signoff

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NetworkPolicy is the closed external-connectivity boundary admitted for one case.
type NetworkPolicy string

const (
	NetworkEngineOnly        NetworkPolicy = "network/engine-only"
	NetworkImmutableRemote   NetworkPolicy = "network/immutable-remote"
	NetworkManifestAndEngine NetworkPolicy = "network/manifest-and-engine"
	NetworkReadOnlyPublic    NetworkPolicy = "network/read-only-public-dependencies"
)

// ConcurrencyClass is the isolation contract enforced by the case scheduler.
type ConcurrencyClass string

const (
	ConcurrencySharedReadOnly    ConcurrencyClass = "shared-read-only"
	ConcurrencyIsolatedWorkspace ConcurrencyClass = "isolated-workspace"
	ConcurrencyExclusiveMutation ConcurrencyClass = "exclusive-mutation"
)

// ExecutionPolicy carries the Rust-admitted runtime bounds into one physical execution.
type ExecutionPolicy struct {
	Timeout     time.Duration
	Retry       RetryPolicy
	Network     NetworkPolicy
	Concurrency ConcurrencyClass
}

type executionPolicyWire struct {
	Timeout uint64 `json:"timeout"`
	Retry   struct {
		MaximumAttempts uint32   `json:"maximum_attempts"`
		Retryable       []string `json:"retryable"`
	} `json:"retry"`
	Network          string `json:"network"`
	ConcurrencyClass string `json:"concurrency_class"`
}

func decodeExecutionPolicy(wire executionPolicyWire) (ExecutionPolicy, error) {
	if wire.Timeout == 0 || wire.Timeout > uint64((24*time.Hour)/time.Millisecond) {
		return ExecutionPolicy{}, fmt.Errorf("case timeout is zero or exceeds the production bound")
	}
	retry := RetryPolicy{
		MaximumAttempts: wire.Retry.MaximumAttempts,
		Retryable:       make(map[InfrastructureFailureClass]struct{}, len(wire.Retry.Retryable)),
	}
	previous := ""
	for _, raw := range wire.Retry.Retryable {
		if raw <= previous {
			return ExecutionPolicy{}, fmt.Errorf("case retry classes are duplicated or non-canonical")
		}
		class, err := catalogInfrastructureFailure(raw)
		if err != nil {
			return ExecutionPolicy{}, err
		}
		retry.Retryable[class] = struct{}{}
		previous = raw
	}
	if err := validateRetryPolicy(retry); err != nil {
		return ExecutionPolicy{}, err
	}
	network := NetworkPolicy(wire.Network)
	if network != NetworkEngineOnly && network != NetworkImmutableRemote && network != NetworkManifestAndEngine && network != NetworkReadOnlyPublic {
		return ExecutionPolicy{}, fmt.Errorf("case names unknown network policy %q", wire.Network)
	}
	concurrency := ConcurrencyClass(wire.ConcurrencyClass)
	if concurrency != ConcurrencySharedReadOnly && concurrency != ConcurrencyIsolatedWorkspace && concurrency != ConcurrencyExclusiveMutation {
		return ExecutionPolicy{}, fmt.Errorf("case names unknown concurrency class %q", wire.ConcurrencyClass)
	}
	policy := ExecutionPolicy{
		Timeout:     time.Duration(wire.Timeout) * time.Millisecond,
		Retry:       retry,
		Network:     network,
		Concurrency: concurrency,
	}
	return policy, nil
}

func catalogInfrastructureFailure(raw string) (InfrastructureFailureClass, error) {
	switch raw {
	case "orchestration-transport-lost":
		return FailureOrchestrationTransport, nil
	case "immutable-remote-unavailable":
		return FailureImmutableRemoteFetch, nil
	case "workspace-materialization-interrupted":
		return FailureRunnerCapacity, nil
	default:
		return "", fmt.Errorf("case names unknown retryable infrastructure class %q", raw)
	}
}

func validateRetryPolicy(policy RetryPolicy) error {
	if policy.MaximumAttempts == 0 || policy.MaximumAttempts > 2 {
		return fmt.Errorf("retry policy attempt bound is outside the closed production range")
	}
	if (policy.MaximumAttempts == 1) != (len(policy.Retryable) == 0) {
		return fmt.Errorf("retry policy attempt bound and retryable classes disagree")
	}
	for class := range policy.Retryable {
		if !knownInfrastructureFailure(class) {
			return fmt.Errorf("retry policy contains an unknown infrastructure class")
		}
	}
	return nil
}

func (policy ExecutionPolicy) key() string {
	classes := make([]string, 0, len(policy.Retry.Retryable))
	for class := range policy.Retry.Retryable {
		classes = append(classes, string(class))
	}
	sort.Strings(classes)
	return strings.Join([]string{
		strconv.FormatInt(policy.Timeout.Milliseconds(), 10),
		strconv.FormatUint(uint64(policy.Retry.MaximumAttempts), 10),
		strings.Join(classes, ","),
		string(policy.Network),
		string(policy.Concurrency),
	}, "\x00")
}

func sameExecutionPolicy(left, right ExecutionPolicy) bool {
	return left.key() == right.key()
}

// ValidateFor rejects a policy detached from the closed program it was admitted for.
func (policy ExecutionPolicy) ValidateFor(program Program) error {
	if policy.Timeout <= 0 || policy.Timeout > 24*time.Hour {
		return fmt.Errorf("program %q has no bounded production timeout", program.Key())
	}
	if err := validateRetryPolicy(policy.Retry); err != nil {
		return fmt.Errorf("program %q: %w", program.Key(), err)
	}
	if policy.Concurrency != ConcurrencySharedReadOnly && policy.Concurrency != ConcurrencyIsolatedWorkspace && policy.Concurrency != ConcurrencyExclusiveMutation {
		return fmt.Errorf("program %q has unknown concurrency class %q", program.Key(), policy.Concurrency)
	}
	return validateProgramNetwork(program, policy.Network)
}

// validateProgramNetwork makes the closed program route the enforcement boundary. The adapter
// has no caller-controlled command or endpoint with which to widen these reviewed policies.
func validateProgramNetwork(program Program, policy NetworkPolicy) error {
	expected := NetworkEngineOnly
	switch {
	case program.Kind == ProgramStableConnector:
		expected = NetworkManifestAndEngine
	case program.Kind == ProgramStandaloneClient && program.Value == "pinned-remote-client":
		expected = NetworkImmutableRemote
	case program.Kind == ProgramStandaloneExample:
		expected = NetworkReadOnlyPublic
	}
	if policy != expected {
		return fmt.Errorf("program %q requires network policy %q, got %q", program.Key(), expected, policy)
	}
	return nil
}
