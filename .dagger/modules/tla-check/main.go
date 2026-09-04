// TLA+ model checking for the dagql cache spec (dagql/tla).
//
// Runs every TLC configuration of CacheLifecycle.tla. Green configurations
// are regression gates: any violation fails the check. A configuration may
// name an expected invariant only when it deliberately mutates behavior to
// prove that the gate detects the bug, or tracks an accepted model finding.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"dagger/tla-check/internal/dagger"
)

const (
	// Pinned TLC release. Same jar and same invocation as documented in
	// dagql/tla/README.md, so local and CI runs are identical.
	tla2toolsURL    = "https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar"
	tla2toolsSHA256 = "936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"
	javaBaseImage   = "eclipse-temurin:21-jre"
)

// temporalOutcome is the expected outcome of a configuration that must
// violate a temporal property. TLC does not name the violated property, so
// a liveness mutation can only be gated on the class of failure.
const temporalOutcome = "temporal"

// expectedOutcome maps every configuration to what TLC must report:
// "" means the run must complete with no error found; temporalOutcome means
// a temporal property must be violated; any other value names the one
// invariant that must be violated.
var expectedOutcome = map[string]string{
	// green: regression gates over the modeled cache behavior
	"core":              "",
	"release_prune":     "",
	"liveness":          "",
	"lazy":              "",
	"lazy_liveness":     "",
	"lazy_stale_cancel": "",
	"lazy_import":       "",
	"persist":           "",
	"persist_liveness":  "",
	"flush_roundtrip":   "",
	"orphan_edges":      "",
	"release_steal":     "",
	"drain_orphan":      "",
	"rollback":          "",
	"rollback_decode":   "",
	"lost_cancel":       "",
	"poisoned":          "",
	"poisoned_adoption": "",
	"poisoned_restart":  "",
	"flush_closure":     "",
	"release_inflight":  "",
	"release_wait":      "",
	"drain_escape":      "",
	"flush_inflight":    "",
	"flush_drained":     "",
	"lazy_release":      "",
}

var clientExpectedOutcome = map[string]string{
	"core":        "",
	"shared_work": "",
	"child":       "",
	"children":    "",
	"grandchild":  "",
	"teardown":    "",
}

var nestedClientProxyExpectedOutcome = map[string]string{
	"core":               "",
	"close_all":          "IndependentClose",
	"cross_carrier":      "CarrierBindingExact",
	"malformed_fallback": "MalformedRejected",
	"metadata_parent":    "ParentUsesScope",
	"rebind":             "NoClosedIDRebind",
	"retry_closed":       "ClosedIsTerminal",
	"substitute":         "ExactRouting",
}

type TlaCheck struct {
	// The dagql/tla spec directory.
	Source *dagger.Directory

	// The engine/server/tla spec directory.
	ClientSource *dagger.Directory
}

func New(ws *dagger.Workspace) *TlaCheck {
	return &TlaCheck{
		Source:       ws.Directory("/dagql/tla", dagger.WorkspaceDirectoryOpts{}),
		ClientSource: ws.Directory("/engine/server/tla", dagger.WorkspaceDirectoryOpts{}),
	}
}

// base returns a container with Java, the checksum-verified TLC jar, and
// the spec directory mounted.
func (m *TlaCheck) base(source *dagger.Directory) *dagger.Container {
	jar := dag.HTTP(tla2toolsURL)
	return dag.Container().
		From(javaBaseImage).
		WithFile("/tla2tools.jar", jar).
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("echo '%s  /tla2tools.jar' | sha256sum -c -", tla2toolsSHA256)}).
		WithDirectory("/spec", source).
		WithWorkdir("/spec")
}

// CacheLifecycle model-checks every configuration of the dagql cache spec
// and verifies each outcome against its expectation.
// +check
func (m *TlaCheck) CacheLifecycle(ctx context.Context) error {
	base := m.base(m.Source)

	names := make([]string, 0, len(expectedOutcome))
	for name := range expectedOutcome {
		names = append(names, name)
	}
	sort.Strings(names)

	var (
		mu       sync.Mutex
		failures []runFailure
		wg       sync.WaitGroup
	)
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if failure := runOne(ctx, base, "CacheLifecycle", "CacheLifecycle_", name, expectedOutcome[name]); failure != nil {
				mu.Lock()
				failures = append(failures, *failure)
				mu.Unlock()
			}
		}(name)
	}
	wg.Wait()

	return reportFailures("cache", failures, len(names))
}

// ClientLifecycle model-checks client runtime reclamation, typed leases,
// authoritative session teardown, the final telemetry barrier, and exact
// nested process-proxy routing.
// +check
func (m *TlaCheck) ClientLifecycle(ctx context.Context) error {
	base := m.base(m.ClientSource)

	var (
		mu       sync.Mutex
		failures []runFailure
		wg       sync.WaitGroup
	)
	run := func(group, specName, configPrefix, name, expect string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if failure := runOne(ctx, base, specName, configPrefix, name, expect); failure != nil {
				failure.name = group + "/" + failure.name
				mu.Lock()
				failures = append(failures, *failure)
				mu.Unlock()
			}
		}()
	}

	clientNames := make([]string, 0, len(clientExpectedOutcome))
	for name := range clientExpectedOutcome {
		clientNames = append(clientNames, name)
	}
	sort.Strings(clientNames)
	for _, name := range clientNames {
		run("lifecycle", "ClientLifecycle", "ClientLifecycle_", name, clientExpectedOutcome[name])
	}

	proxyNames := make([]string, 0, len(nestedClientProxyExpectedOutcome))
	for name := range nestedClientProxyExpectedOutcome {
		proxyNames = append(proxyNames, name)
	}
	sort.Strings(proxyNames)
	for _, name := range proxyNames {
		run("proxy", "NestedClientProxy", "NestedClientProxy_", name, nestedClientProxyExpectedOutcome[name])
	}
	wg.Wait()

	return reportFailures("client lifecycle", failures, len(clientNames)+len(proxyNames))
}

// One runs a single TLC configuration and returns the raw TLC output,
// using the same pinned jar and invocation as the CacheLifecycle check.
// Unlike the check, it applies no expectation: violations come back in
// the output for the caller to read.
//
// With invariant set, the configuration's INVARIANTS line is replaced by
// that single invariant, the specification is forced to the safety-only
// Spec, and PROPERTY lines are dropped, so one question runs in
// isolation. With define also set, the given TLA+ operator definition is
// appended to the spec first; that runs a scratch probe invariant (for
// example a reachability probe expected to violate) without editing the
// repository.
func (m *TlaCheck) One(
	ctx context.Context,
	// configuration name without the CacheLifecycle_ prefix, e.g. "lazy"
	config string,
	// +optional
	// invariant to check instead of the configuration's INVARIANTS line
	invariant string,
	// +optional
	// TLA+ operator definition to append to the spec, e.g. "ProbeX == ..."
	define string,
) (string, error) {
	if define != "" && invariant == "" {
		return "", fmt.Errorf("define requires invariant: name which invariant to check")
	}
	ctr := m.base(m.Source)

	if define != "" {
		spec, err := m.Source.File("CacheLifecycle.tla").Contents(ctx)
		if err != nil {
			return "", fmt.Errorf("read spec: %w", err)
		}
		// The module body ends at the last ==== line; definitions must sit
		// above it.
		term := strings.LastIndex(spec, "\n====")
		if term < 0 {
			return "", fmt.Errorf("spec terminator not found")
		}
		spec = spec[:term] + "\n" + define + "\n" + spec[term:]
		ctr = ctr.WithNewFile("/spec/CacheLifecycle.tla", spec)
	}

	cfgPath := fmt.Sprintf("CacheLifecycle_%s.cfg", config)
	if invariant != "" {
		cfg, err := m.Source.File(cfgPath).Contents(ctx)
		if err != nil {
			return "", fmt.Errorf("read config %s: %w", cfgPath, err)
		}
		lines := strings.Split(cfg, "\n")
		kept := lines[:0]
		for _, line := range lines {
			switch {
			case strings.HasPrefix(line, "SPECIFICATION"):
				kept = append(kept, "SPECIFICATION Spec")
			case strings.HasPrefix(line, "INVARIANTS"):
				kept = append(kept, "INVARIANTS "+invariant)
			case strings.HasPrefix(line, "PROPERTY"):
				// dropped: a lone invariant is a safety question
			default:
				kept = append(kept, line)
			}
		}
		ctr = ctr.WithNewFile("/spec/"+cfgPath, strings.Join(kept, "\n"))
	}

	cmd := fmt.Sprintf(
		"java -XX:+UseParallelGC -cp /tla2tools.jar tlc2.TLC -workers auto -deadlock -config %s CacheLifecycle.tla 2>&1; true",
		cfgPath)
	return ctr.WithExec([]string{"sh", "-c", cmd}).Stdout(ctx)
}

type runFailure struct {
	name    string
	summary string
	detail  string
}

// reportFailures prints the detailed diagnostics and returns a short error
// suitable for propagation through the check API.
func reportFailures(model string, failures []runFailure, total int) error {
	if len(failures) == 0 {
		return nil
	}

	sort.Slice(failures, func(i, j int) bool {
		return failures[i].name < failures[j].name
	})

	fmt.Printf("TLA+ %s model check failures:\n", model)
	for _, failure := range failures {
		fmt.Printf("- %s: %s\n", failure.name, failure.summary)
	}
	for _, failure := range failures {
		if failure.detail == "" {
			continue
		}
		fmt.Printf("\n--- details for %s ---\n%s", failure.name, failure.detail)
		if !strings.HasSuffix(failure.detail, "\n") {
			fmt.Println()
		}
		fmt.Printf("--- end details for %s ---\n\n", failure.name)
	}

	return fmt.Errorf("TLA+ %s model check failed: %d of %d configurations failed", model, len(failures), total)
}

// runOne executes one TLC configuration and returns nil on the expected
// outcome, or detailed diagnostics for reportFailures to print. TLC exits
// nonzero on violations, so the exec swallows the exit code and the output is
// parsed instead.
func runOne(
	ctx context.Context,
	base *dagger.Container,
	specName,
	configPrefix,
	name,
	expect string,
) *runFailure {
	cmd := fmt.Sprintf(
		"java -XX:+UseParallelGC -cp /tla2tools.jar tlc2.TLC -workers auto -deadlock -config %s%s.cfg %s.tla 2>&1; true",
		configPrefix, name, specName)
	out, err := base.WithExec([]string{"sh", "-c", cmd}).Stdout(ctx)
	if err != nil {
		return &runFailure{name: name, summary: "could not run TLC", detail: err.Error()}
	}

	clean := strings.Contains(out, "No error has been found")
	violated := ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "Error: Invariant "); ok {
			violated = strings.TrimSuffix(strings.TrimSpace(rest), " is violated.")
			violated = strings.TrimSuffix(violated, " is violated")
			break
		}
		if strings.HasPrefix(line, "Error: Temporal properties were violated") {
			violated = temporalOutcome
			break
		}
	}

	describe := func(outcome string) string {
		if outcome == temporalOutcome {
			return "a temporal property"
		}
		return "invariant " + outcome
	}

	switch {
	case expect == "" && clean:
		return nil
	case expect == "" && violated != "":
		return &runFailure{
			name:    name,
			summary: fmt.Sprintf("expected a clean pass, but %s was violated — a regression in the modeled behavior or the spec", describe(violated)),
			detail:  out,
		}
	case expect != "" && violated == expect:
		return nil
	case expect != "" && clean:
		return &runFailure{
			name:    name,
			summary: fmt.Sprintf("expected %s to be violated, but the run came up clean — the model or config no longer reproduces it", describe(expect)),
			detail:  out,
		}
	case expect != "" && violated != "":
		return &runFailure{
			name:    name,
			summary: fmt.Sprintf("expected %s to be violated, but %s was violated instead — the configuration drifted", describe(expect), describe(violated)),
			detail:  out,
		}
	default:
		return &runFailure{
			name:    name,
			summary: "unrecognized TLC outcome (no clean pass, invariant violation, or temporal violation)",
			detail:  out,
		}
	}
}
