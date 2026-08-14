package signoff

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const scenarioCandidateDigestDomain = "dagger-rust-sdk-conformance-scenario-candidates-v1\x00"

// ScenarioRealizationKind is one closed Rust implementation strategy.
type ScenarioRealizationKind string

const (
	RealizationGeneratedCore   ScenarioRealizationKind = "generated-core"
	RealizationReviewedFixture ScenarioRealizationKind = "reviewed-rust-fixture"
)

// ScenarioRealization binds one authority scenario to one checked Rust selector.
type ScenarioRealization struct {
	ScenarioID       string
	ContractDigest   string
	ProofID          string
	Kind             ScenarioRealizationKind
	RealizationID    string
	SchemaCoordinate string
	FixtureID        string
}

// ScenarioRealizationCatalog is the source-bound, potentially partial review registry.
type ScenarioRealizationCatalog struct {
	TargetDigest            string
	ScenarioCandidateDigest string
	RunnerSourceDigest      string
	Registrations           map[string]ScenarioRealization
}

type scenarioRealizationRegistryWire struct {
	FormatVersion           string `json:"format_version"`
	TargetDigest            string `json:"target_digest"`
	ScenarioCandidateDigest string `json:"scenario_candidate_digest"`
	RunnerSourceDigest      string `json:"runner_source_digest"`
	Registrations           []struct {
		ScenarioID     string `json:"scenario_id"`
		ContractDigest string `json:"contract_digest"`
		ProofID        string `json:"proof_id"`
		Realization    struct {
			Kind             ScenarioRealizationKind `json:"kind"`
			RealizationID    string                  `json:"realization_id"`
			SchemaCoordinate *string                 `json:"schema_coordinate,omitempty"`
			FixtureID        *string                 `json:"fixture_id,omitempty"`
		} `json:"realization"`
	} `json:"registrations"`
}

// DecodeScenarioRealizations verifies that reviewed selectors remain bound to the exact
// generated queue and Rust runner bytes admitted by sign-off.
func DecodeScenarioRealizations(data, scenarioCandidates, runnerSource []byte) (ScenarioRealizationCatalog, error) {
	canonical, err := canonicalScenarioJSON(data)
	if err != nil || !bytes.Equal(data, canonical) {
		return ScenarioRealizationCatalog{}, fmt.Errorf("rust scenario realization registry is not canonical")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire scenarioRealizationRegistryWire
	if err := decoder.Decode(&wire); err != nil {
		return ScenarioRealizationCatalog{}, fmt.Errorf("decode Rust scenario realization registry: %w", err)
	}
	if err := requireScenarioEOF(decoder); err != nil {
		return ScenarioRealizationCatalog{}, err
	}
	if wire.FormatVersion != "1.0.0" || !validSHA256(wire.TargetDigest) ||
		wire.ScenarioCandidateDigest != domainSeparatedSHA256(scenarioCandidateDigestDomain, scenarioCandidates) ||
		wire.RunnerSourceDigest != rawSHA256(runnerSource) {
		return ScenarioRealizationCatalog{}, fmt.Errorf("rust scenario realization source identity is stale")
	}

	registrations := make(map[string]ScenarioRealization, len(wire.Registrations))
	previousScenarioID := ""
	for _, registration := range wire.Registrations {
		realization := registration.Realization
		if !validScenarioIdentifier(registration.ScenarioID) || registration.ScenarioID <= previousScenarioID ||
			!validSHA256(registration.ContractDigest) ||
			!validScenarioIdentifier(registration.ProofID) || !strings.HasPrefix(registration.ProofID, "probe/") ||
			!validScenarioIdentifier(realization.RealizationID) {
			return ScenarioRealizationCatalog{}, fmt.Errorf("rust scenario realization identities are malformed or non-canonical")
		}
		decoded := ScenarioRealization{
			ScenarioID: registration.ScenarioID, ContractDigest: registration.ContractDigest,
			ProofID: registration.ProofID, Kind: realization.Kind, RealizationID: realization.RealizationID,
		}
		switch realization.Kind {
		case RealizationGeneratedCore:
			if realization.SchemaCoordinate == nil || *realization.SchemaCoordinate == "" || realization.FixtureID != nil {
				return ScenarioRealizationCatalog{}, fmt.Errorf("generated Core realization %q is ambiguous", registration.ScenarioID)
			}
			decoded.SchemaCoordinate = *realization.SchemaCoordinate
		case RealizationReviewedFixture:
			if realization.FixtureID == nil || !validScenarioIdentifier(*realization.FixtureID) || realization.SchemaCoordinate != nil {
				return ScenarioRealizationCatalog{}, fmt.Errorf("reviewed Rust realization %q is ambiguous", registration.ScenarioID)
			}
			decoded.FixtureID = *realization.FixtureID
		default:
			return ScenarioRealizationCatalog{}, fmt.Errorf("rust scenario %q has no executable realization", registration.ScenarioID)
		}
		if _, duplicate := registrations[registration.ScenarioID]; duplicate {
			return ScenarioRealizationCatalog{}, fmt.Errorf("rust scenario %q is registered twice", registration.ScenarioID)
		}
		registrations[registration.ScenarioID] = decoded
		previousScenarioID = registration.ScenarioID
	}
	return ScenarioRealizationCatalog{
		TargetDigest: wire.TargetDigest, ScenarioCandidateDigest: wire.ScenarioCandidateDigest,
		RunnerSourceDigest: wire.RunnerSourceDigest, Registrations: registrations,
	}, nil
}

// ApplyScenarioRealizations attaches only exact, boundary-compatible Rust selectors to the
// closed case registry. Partial input remains useful for review, while the later totality gate
// continues to reject every unregistered route.
func ApplyScenarioRealizations(
	registry map[string]ProgramSpec,
	observable ObservableProgramCatalog,
	realizations ScenarioRealizationCatalog,
) (map[string]ProgramSpec, error) {
	if realizations.TargetDigest != observable.TargetDigest {
		return nil, fmt.Errorf("rust scenario realizations and observable routes name different targets")
	}
	result := make(map[string]ProgramSpec, len(registry))
	for key, spec := range registry {
		result[key] = spec
	}
	scenarioIDs := make([]string, 0, len(realizations.Registrations))
	for scenarioID := range realizations.Registrations {
		scenarioIDs = append(scenarioIDs, scenarioID)
	}
	sort.Strings(scenarioIDs)
	for _, scenarioID := range scenarioIDs {
		realization := realizations.Registrations[scenarioID]
		if realization.ScenarioID != scenarioID {
			return nil, fmt.Errorf("rust realization key differs from scenario %q", scenarioID)
		}
		programKey, selected := observable.CasePrograms[scenarioID]
		if !selected {
			return nil, fmt.Errorf("rust realization names unselected scenario %q", scenarioID)
		}
		spec, exists := result[programKey]
		if !exists || spec.Program.Kind != ProgramIntegration || spec.Executor != nil {
			return nil, fmt.Errorf("rust realization for %q collides with its closed route", scenarioID)
		}
		switch realization.Kind {
		case RealizationGeneratedCore:
			if spec.Boundary != BoundaryGeneratedCore || realization.SchemaCoordinate == "" {
				return nil, fmt.Errorf("generated Core realization for %q widens its reviewed boundary", scenarioID)
			}
		case RealizationReviewedFixture:
			if spec.Boundary == BoundaryGeneratedCore || realization.FixtureID != spec.Program.Value {
				return nil, fmt.Errorf("reviewed Rust realization for %q differs from its fixture route", scenarioID)
			}
		default:
			return nil, fmt.Errorf("rust realization for %q uses an unknown implementation kind", scenarioID)
		}
		spec.Executor = &ExecutorDefinition{
			Kind: ExecutorScenarioConformance, Selector: realization.RealizationID,
			ContractDigest: realization.ContractDigest, ProofID: realization.ProofID,
			Expected: ObservationExpectation{Category: string(realization.Kind), Operation: realization.RealizationID},
		}
		result[programKey] = spec
	}
	return result, nil
}

func domainSeparatedSHA256(domain string, data []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(domain))
	_, _ = digest.Write(data)
	return fmt.Sprintf("sha256:%x", digest.Sum(nil))
}

func rawSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", digest)
}

func validScenarioIdentifier(value string) bool {
	if value == "" || len(value) > 192 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, "//") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	for _, char := range []byte(value) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == '.' || char == '/' {
			continue
		}
		return false
	}
	return true
}

func requireScenarioEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode rust scenario realization registry trailer: %w", err)
	}
	return fmt.Errorf("rust scenario realization registry contains trailing JSON")
}

func canonicalScenarioJSON(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := requireScenarioEOF(decoder); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
