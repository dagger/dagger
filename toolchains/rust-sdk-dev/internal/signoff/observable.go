package signoff

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const observableProgramCount = 612

// ObservableProgramCatalog binds the Rust-owned integration routes to their admitted catalogs.
type ObservableProgramCatalog struct {
	TargetDigest           string
	AssertionCatalogDigest string
	FixtureRegistryDigest  string
	CaseCatalogDigest      string
	ProgramRegistryDigest  string
	Programs               map[string]ProgramSpec
}

type observableProgramArtifactWire struct {
	FormatVersion          string `json:"format_version"`
	TargetDigest           string `json:"target_digest"`
	AssertionCatalogDigest string `json:"assertion_catalog_digest"`
	FixtureRegistryDigest  string `json:"fixture_registry_digest"`
	CaseCatalogDigest      string `json:"case_catalog_digest"`
	ProgramRegistryDigest  string `json:"program_registry_digest"`
	Programs               []struct {
		FixtureID string          `json:"fixture_id"`
		CaseID    string          `json:"case_id"`
		Boundary  ProgramBoundary `json:"boundary"`
	} `json:"programs"`
}

// DecodeObservablePrograms accepts only the checked, closed Rust-owned integration registry.
func DecodeObservablePrograms(data []byte) (ObservableProgramCatalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire observableProgramArtifactWire
	if err := decoder.Decode(&wire); err != nil {
		return ObservableProgramCatalog{}, fmt.Errorf("decode observable program registry: %w", err)
	}
	if err := requireObservableEOF(decoder); err != nil {
		return ObservableProgramCatalog{}, err
	}
	if wire.FormatVersion != "1.0.0" ||
		!validSHA256(wire.TargetDigest) ||
		!validSHA256(wire.AssertionCatalogDigest) ||
		!validSHA256(wire.FixtureRegistryDigest) ||
		!validSHA256(wire.CaseCatalogDigest) ||
		!validSHA256(wire.ProgramRegistryDigest) ||
		len(wire.Programs) != observableProgramCount {
		return ObservableProgramCatalog{}, fmt.Errorf("observable program registry identity or count is invalid")
	}
	programs := make(map[string]ProgramSpec, len(wire.Programs))
	previous := ""
	for _, route := range wire.Programs {
		fixtureTail, fixtureOK := strings.CutPrefix(route.FixtureID, "fixture/integration/")
		caseTail, caseOK := strings.CutPrefix(route.CaseID, "case/integration/")
		if !fixtureOK || !caseOK || fixtureTail == "" || fixtureTail != caseTail || route.FixtureID <= previous ||
			!knownObservableBoundary(route.Boundary) {
			return ObservableProgramCatalog{}, fmt.Errorf("observable program route is malformed or non-canonical")
		}
		program := Program{Kind: ProgramIntegration, Value: route.FixtureID}
		spec := ProgramSpec{Program: program, Boundary: route.Boundary, Workspace: WorkspaceBaselineBranch}
		if _, duplicate := programs[program.Key()]; duplicate {
			return ObservableProgramCatalog{}, fmt.Errorf("observable program route %q is duplicated", program.Key())
		}
		programs[program.Key()] = spec
		previous = route.FixtureID
	}
	return ObservableProgramCatalog{
		TargetDigest:           wire.TargetDigest,
		AssertionCatalogDigest: wire.AssertionCatalogDigest,
		FixtureRegistryDigest:  wire.FixtureRegistryDigest,
		CaseCatalogDigest:      wire.CaseCatalogDigest,
		ProgramRegistryDigest:  wire.ProgramRegistryDigest,
		Programs:               programs,
	}, nil
}

// CompleteProgramRegistry joins fixed programs with the checked integration routes.
func CompleteProgramRegistry(observable ObservableProgramCatalog) (map[string]ProgramSpec, error) {
	if len(observable.Programs) != observableProgramCount {
		return nil, fmt.Errorf("observable program inventory is incomplete")
	}
	registry := FixedProgramRegistry()
	for key, spec := range observable.Programs {
		if _, duplicate := registry[key]; duplicate {
			return nil, fmt.Errorf("program route %q collides with the fixed registry", key)
		}
		registry[key] = spec
	}
	if len(registry) != observableProgramCount+60 {
		return nil, fmt.Errorf("complete program inventory has an unexpected cardinality")
	}
	return registry, nil
}

// OrderedPrograms returns stable registry order independently from JSON declaration order.
func OrderedPrograms(registry map[string]ProgramSpec) []Program {
	programs := make([]Program, 0, len(registry))
	for _, spec := range registry {
		programs = append(programs, spec.Program)
	}
	sort.Slice(programs, func(left, right int) bool {
		return programs[left].Key() < programs[right].Key()
	})
	return programs
}

func knownObservableBoundary(boundary ProgramBoundary) bool {
	return boundary == BoundarySharedCLI ||
		boundary == BoundaryGeneratedCore ||
		boundary == BoundaryModuleRuntime ||
		boundary == BoundaryPackagedRuntime
}

func validSHA256(value string) bool {
	hexValue, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(hexValue) != 64 {
		return false
	}
	_, err := hex.DecodeString(hexValue)
	return err == nil && strings.ToLower(hexValue) == hexValue
}

func requireObservableEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode observable program registry trailer: %w", err)
	}
	return fmt.Errorf("observable program registry contains trailing JSON")
}
