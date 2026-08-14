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
	ProgramStandaloneExample ProgramKind = "standalone-example"
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
		ID            string          `json:"id"`
		Family        string          `json:"family"`
		FixtureDigest string          `json:"fixture_digest"`
		Program       json.RawMessage `json:"program"`
		executionPolicyWire
	} `json:"cases"`
}

// CaseRoute binds one canonical case identity to its closed production program.
type CaseRoute struct {
	CaseID        string
	FixtureDigest string
	Program       Program
	Policy        ExecutionPolicy
}

// CaseExecutionGroup is one concrete Rust invocation and every catalog route proved by it.
// Authority aliases may share an invocation only when the reviewed registry selected the same
// concrete Rust executor and production policy for all of them.
type CaseExecutionGroup struct {
	Representative CaseRoute
	Members        []CaseRoute
}

type facadeAdmissionRouteWire struct {
	CaseID            string          `json:"case_id"`
	Program           json.RawMessage `json:"program"`
	FixtureDigest     string          `json:"fixture_digest"`
	Boundary          ProgramBoundary `json:"boundary"`
	ExecutionSelector string          `json:"execution_selector"`
	Executed          bool            `json:"executed"`
	executionPolicyWire
}

// GroupCaseExecutions collapses source-identity aliases without collapsing their verdict rows.
func GroupCaseExecutions(routes []CaseRoute, registry map[string]ProgramSpec) ([]CaseExecutionGroup, error) {
	groups := make([]CaseExecutionGroup, 0, len(routes))
	indices := make(map[string]int, len(routes))
	for _, route := range routes {
		spec, ok := registry[route.Program.Key()]
		if !ok || spec.Program != route.Program || spec.Executor == nil {
			return nil, fmt.Errorf("case route %q has no concrete registered executor", route.CaseID)
		}
		key := route.Program.Key()
		if spec.Executor.Kind == ExecutorScenarioConformance {
			// A shared selector is one physical execution only when all runtime policy is
			// identical. In particular, the immutable-remote standalone client must not
			// borrow the engine-only policy of its source-identity peers.
			key = string(spec.Executor.Kind) + "/" + spec.Executor.Selector + "\x00" + route.Policy.key()
		}
		if index, exists := indices[key]; exists {
			representativeSpec := registry[groups[index].Representative.Program.Key()]
			if representativeSpec.Boundary != spec.Boundary || representativeSpec.Workspace != spec.Workspace ||
				!sameExecution(*representativeSpec.Executor, *spec.Executor) ||
				!sameExecutionPolicy(groups[index].Representative.Policy, route.Policy) {
				return nil, fmt.Errorf("shared Rust executor %q crosses a reviewed production policy", spec.Executor.Selector)
			}
			groups[index].Members = append(groups[index].Members, route)
			continue
		}
		indices[key] = len(groups)
		groups = append(groups, CaseExecutionGroup{Representative: route, Members: []CaseRoute{route}})
	}
	return groups, nil
}

func sameExecution(left, right ExecutorDefinition) bool {
	return left.Kind == right.Kind && left.Selector == right.Selector && left.Expected == right.Expected
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

// DecodeCaseRoutes joins the complete checked case catalog to the closed executor registry.
func DecodeCaseRoutes(data []byte, registry map[string]ProgramSpec) ([]CaseRoute, error) {
	var catalog catalogWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode complete sign-off case catalog: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if len(catalog.Cases) != len(registry) {
		return nil, fmt.Errorf("complete sign-off case catalog cardinality differs from its registry")
	}
	routes := make([]CaseRoute, 0, len(catalog.Cases))
	seenCases := make(map[string]struct{}, len(catalog.Cases))
	seenPrograms := make(map[string]struct{}, len(catalog.Cases))
	previous := ""
	for _, item := range catalog.Cases {
		if item.ID == "" || item.ID <= previous {
			return nil, fmt.Errorf("complete sign-off case identities are empty or non-canonical")
		}
		program, err := decodeCompleteProgram(item.Program)
		if err != nil {
			return nil, err
		}
		spec, ok := registry[program.Key()]
		if !ok || spec.Program != program {
			return nil, fmt.Errorf("complete sign-off program %q is not registered", program.Key())
		}
		if _, duplicate := seenCases[item.ID]; duplicate {
			return nil, fmt.Errorf("complete sign-off case %q is duplicated", item.ID)
		}
		if _, duplicate := seenPrograms[program.Key()]; duplicate {
			return nil, fmt.Errorf("complete sign-off program %q is duplicated", program.Key())
		}
		seenCases[item.ID] = struct{}{}
		seenPrograms[program.Key()] = struct{}{}
		policy, err := decodeExecutionPolicy(item.executionPolicyWire)
		if err != nil {
			return nil, fmt.Errorf("case route %q has invalid production policy: %w", item.ID, err)
		}
		if err := policy.ValidateFor(program); err != nil {
			return nil, fmt.Errorf("case route %q: %w", item.ID, err)
		}
		if !validSHA256(item.FixtureDigest) {
			return nil, fmt.Errorf("case route %q has a malformed fixture identity", item.ID)
		}
		routes = append(routes, CaseRoute{CaseID: item.ID, FixtureDigest: item.FixtureDigest, Program: program, Policy: policy})
		previous = item.ID
	}
	return routes, nil
}

// DecodeFacadeAdmissionRoutes consumes only the Rust-produced pre-target projection. The Go
// adapter rechecks that every projected route still selects its closed local executor, but it
// does not reinterpret the caller's authored catalog or invent execution policy.
func DecodeFacadeAdmissionRoutes(data []byte, registry map[string]ProgramSpec) ([]CaseRoute, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var projected []facadeAdmissionRouteWire
	if err := decoder.Decode(&projected); err != nil {
		return nil, fmt.Errorf("decode Rust facade admission routes: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if len(projected) != len(registry) {
		return nil, fmt.Errorf("Rust facade route projection cardinality differs from the executor registry")
	}
	routes := make([]CaseRoute, 0, len(projected))
	executed := make(map[string]bool, len(projected))
	seenPrograms := make(map[string]struct{}, len(projected))
	previous := ""
	for _, item := range projected {
		if item.CaseID == "" || item.CaseID <= previous {
			return nil, fmt.Errorf("Rust facade route identities are empty or non-canonical")
		}
		program, err := decodeCompleteProgram(item.Program)
		if err != nil {
			return nil, err
		}
		spec, ok := registry[program.Key()]
		if !ok || spec.Program != program || spec.Executor == nil ||
			item.Boundary != spec.Boundary || item.ExecutionSelector != spec.Executor.Selector {
			return nil, fmt.Errorf("Rust facade route %q differs from its closed executor", item.CaseID)
		}
		if _, duplicate := seenPrograms[program.Key()]; duplicate {
			return nil, fmt.Errorf("Rust facade program %q is duplicated", program.Key())
		}
		policy, err := decodeExecutionPolicy(item.executionPolicyWire)
		if err != nil {
			return nil, fmt.Errorf("Rust facade route %q has invalid production policy: %w", item.CaseID, err)
		}
		if err := policy.ValidateFor(program); err != nil {
			return nil, fmt.Errorf("Rust facade route %q: %w", item.CaseID, err)
		}
		if !validSHA256(item.FixtureDigest) {
			return nil, fmt.Errorf("Rust facade route %q has a malformed fixture identity", item.CaseID)
		}
		routes = append(routes, CaseRoute{CaseID: item.CaseID, FixtureDigest: item.FixtureDigest, Program: program, Policy: policy})
		executed[item.CaseID] = item.Executed
		seenPrograms[program.Key()] = struct{}{}
		previous = item.CaseID
	}
	groups, err := GroupCaseExecutions(routes, registry)
	if err != nil {
		return nil, err
	}
	expectedExecutions := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		expectedExecutions[group.Representative.CaseID] = struct{}{}
	}
	for caseID, selected := range executed {
		_, expected := expectedExecutions[caseID]
		if selected != expected {
			return nil, fmt.Errorf("Rust facade route %q has a stale physical-execution marker", caseID)
		}
	}
	return routes, nil
}

func decodeCompleteProgram(data []byte) (Program, error) {
	var discriminator struct {
		Program ProgramKind `json:"program"`
		Fixture *string     `json:"fixture,omitempty"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return Program{}, fmt.Errorf("decode complete sign-off program: %w", err)
	}
	if discriminator.Program != ProgramIntegration {
		return decodeProgram(data)
	}
	if discriminator.Fixture == nil || *discriminator.Fixture == "" {
		return Program{}, fmt.Errorf("integration sign-off program has no reviewed fixture")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&discriminator); err != nil {
		return Program{}, fmt.Errorf("decode integration sign-off program: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Program{}, err
	}
	return Program{Kind: ProgramIntegration, Value: *discriminator.Fixture}, nil
}

func decodeProgram(data []byte) (Program, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire struct {
		Program   ProgramKind `json:"program"`
		Check     *string     `json:"check,omitempty"`
		Shape     *string     `json:"shape,omitempty"`
		Case      *string     `json:"case,omitempty"`
		Example   *string     `json:"example,omitempty"`
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
		if wire.Check != nil || wire.Shape != nil || wire.Case != nil || wire.Example != nil || wire.Behaviour != nil {
			return Program{}, fmt.Errorf("stable connector carries an unexpected selector")
		}
		return Program{Kind: wire.Program}, nil
	case ProgramCoreShape:
		selected = wire.Shape
	case ProgramEngineIntegration, ProgramModuleAuthoring, ProgramStandaloneClient:
		selected = wire.Case
	case ProgramStandaloneExample:
		selected = wire.Example
	case ProgramDefinitiveGo:
		selected = wire.Behaviour
	default:
		return Program{}, fmt.Errorf("unknown fixed sign-off program family %q", wire.Program)
	}
	if selected == nil || *selected == "" {
		return Program{}, fmt.Errorf("fixed sign-off program %q has no selector", wire.Program)
	}
	set := 0
	for _, value := range []*string{wire.Check, wire.Shape, wire.Case, wire.Example, wire.Behaviour} {
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
