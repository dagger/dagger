package signoff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// ProgramKind is one closed exact-engine executor family mirrored from the Rust catalog.
type ProgramKind string

const (
	ProgramCommonHarness     ProgramKind = "common-harness"
	ProgramStableConnector   ProgramKind = "stable-connector"
	ProgramCoreShape         ProgramKind = "core-shape"
	ProgramEngineIntegration ProgramKind = "engine-integration"
	ProgramModuleAuthoring   ProgramKind = "module-authoring"
	ProgramStandaloneClient  ProgramKind = "standalone-client"
	ProgramDefinitiveGo      ProgramKind = "definitive-go-client"
	ProgramIntegration       ProgramKind = "integration-assertion"
)

// Program is one validated fixed route; Value is empty only for the stable connector.
type Program struct {
	Kind  ProgramKind
	Value string
}

// Key returns the collision-free stable registry coordinate.
func (program Program) Key() string {
	return string(program.Kind) + "/" + program.Value
}

type catalogWire struct {
	Cases []struct {
		Family  string          `json:"family"`
		Program json.RawMessage `json:"program"`
	} `json:"cases"`
}

// DecodeFixedPrograms accepts only the exact fixed inventory already admitted by Rust policy.
func DecodeFixedPrograms(data []byte) ([]Program, error) {
	var catalog catalogWire
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode sign-off case catalog: %w", err)
	}
	registry := FixedProgramRegistry()
	programs := make([]Program, 0, len(registry))
	seen := make(map[string]struct{}, len(registry))
	for _, item := range catalog.Cases {
		if item.Family == "integration-assertion" {
			continue
		}
		program, err := decodeProgram(item.Program)
		if err != nil {
			return nil, err
		}
		if _, ok := registry[program.Key()]; !ok {
			return nil, fmt.Errorf("fixed sign-off program %q is not registered", program.Key())
		}
		if _, duplicate := seen[program.Key()]; duplicate {
			return nil, fmt.Errorf("fixed sign-off program %q is duplicated", program.Key())
		}
		seen[program.Key()] = struct{}{}
		programs = append(programs, program)
	}
	if len(seen) != len(registry) {
		return nil, fmt.Errorf("fixed sign-off program inventory is incomplete")
	}
	sort.Slice(programs, func(left, right int) bool {
		return programs[left].Key() < programs[right].Key()
	})
	return programs, nil
}

func decodeProgram(data []byte) (Program, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire struct {
		Program   ProgramKind `json:"program"`
		Check     *string     `json:"check,omitempty"`
		Shape     *string     `json:"shape,omitempty"`
		Case      *string     `json:"case,omitempty"`
		Behaviour *string     `json:"behaviour,omitempty"`
	}
	if err := decoder.Decode(&wire); err != nil {
		return Program{}, fmt.Errorf("decode fixed sign-off program: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Program{}, err
	}
	var selected *string
	switch wire.Program {
	case ProgramCommonHarness:
		selected = wire.Check
	case ProgramStableConnector:
		if wire.Check != nil || wire.Shape != nil || wire.Case != nil || wire.Behaviour != nil {
			return Program{}, fmt.Errorf("stable connector carries an unexpected selector")
		}
		return Program{Kind: wire.Program}, nil
	case ProgramCoreShape:
		selected = wire.Shape
	case ProgramEngineIntegration, ProgramModuleAuthoring, ProgramStandaloneClient:
		selected = wire.Case
	case ProgramDefinitiveGo:
		selected = wire.Behaviour
	default:
		return Program{}, fmt.Errorf("unknown fixed sign-off program family %q", wire.Program)
	}
	if selected == nil || *selected == "" {
		return Program{}, fmt.Errorf("fixed sign-off program %q has no selector", wire.Program)
	}
	set := 0
	for _, value := range []*string{wire.Check, wire.Shape, wire.Case, wire.Behaviour} {
		if value != nil {
			set++
		}
	}
	if set != 1 {
		return Program{}, fmt.Errorf("fixed sign-off program %q has an ambiguous selector", wire.Program)
	}
	return Program{Kind: wire.Program, Value: *selected}, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode fixed sign-off program suffix: %w", err)
	}
	return fmt.Errorf("fixed sign-off program contains trailing JSON")
}
