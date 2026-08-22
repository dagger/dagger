// TLA+ model checking for the dagql cache spec (dagql/tla).
//
// Runs every TLC configuration of CacheLifecycle.tla. Green configurations
// are regression gates: any violation fails the check. A configuration may
// name an expected invariant only while it deliberately tracks an accepted
// model finding.
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

// expectedOutcome maps every configuration to what TLC must report:
// "" means the run must complete with no error found; a non-empty value
// names the one invariant that must be violated.
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
	"drain_escape":      "",
	"flush_inflight":    "",
	"flush_drained":     "",
	"lazy_release":      "",

	// green: reader cancellation inside the persisted-decode singleflight.
	// A joiner that wakes on a departed leader's cancellation retries
	// instead of failing (persistDecodeRetry), and a post-install failure
	// leaves persistLeaseSyncPending set so the next demand retries the
	// lease sync; see the config headers.
	"decode_cancel":          "",
	"decode_cancel_liveness": "",

	// green: session-resource gating with no persistence (the filter in
	// LookupHit/CanonicalPick/FnComplete, PubIndexFresh/PubAttachAddDep
	// maintenance, BindResource, RequiredExact and ReturnedGated).
	"resources": "",

	// accepted finding: the STORED requiredSessionResources set drifts
	// from the true transitive requirement. importPersistedState
	// recomputes it over c.resultsByID in Go map order, so a parent
	// visited before its dependency ends with an empty set
	// (cache_persistence_import.go:360, recompute at cache.go:556); and
	// ensurePersistedHitValueLoaded overwrites it from the decoded value,
	// which knows only its own handle, dropping a non-leaf's
	// dependency-derived requirement (cache_persistence_import.go:736-740).
	// Both exist unchanged on upstream/main; see the config header.
	"resources_restart": "RequiredExact",
}

type TlaCheck struct {
	// The dagql/tla spec directory.
	Source *dagger.Directory
}

func New(ws *dagger.Workspace) *TlaCheck {
	return &TlaCheck{Source: ws.Directory("/dagql/tla", dagger.WorkspaceDirectoryOpts{})}
}

// base returns a container with Java, the checksum-verified TLC jar, and
// the spec directory mounted.
func (m *TlaCheck) base() *dagger.Container {
	jar := dag.HTTP(tla2toolsURL)
	return dag.Container().
		From(javaBaseImage).
		WithFile("/tla2tools.jar", jar).
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("echo '%s  /tla2tools.jar' | sha256sum -c -", tla2toolsSHA256)}).
		WithDirectory("/spec", m.Source).
		WithWorkdir("/spec")
}

// CacheLifecycle model-checks every configuration of the dagql cache spec
// and verifies each outcome against its expectation.
// +check
func (m *TlaCheck) CacheLifecycle(ctx context.Context) error {
	base := m.base()

	names := make([]string, 0, len(expectedOutcome))
	for name := range expectedOutcome {
		names = append(names, name)
	}
	sort.Strings(names)

	var (
		mu       sync.Mutex
		failures []string
		wg       sync.WaitGroup
		// Each configuration is a TLC JVM of several GiB; unbounded fan-out
		// over 30 configurations exhausted a 64 GiB host.
		sem = make(chan struct{}, 4)
	)
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if msg := runOne(ctx, base, name, expectedOutcome[name]); msg != "" {
				mu.Lock()
				failures = append(failures, msg)
				mu.Unlock()
			}
		}(name)
	}
	wg.Wait()

	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf(
			"TLA+ cache model check failed (%d of %d configurations):\n%s\n\nEach configuration's comment in dagql/tla/ describes its scenario and expected outcome.",
			len(failures), len(names), strings.Join(failures, "\n"))
	}
	return nil
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
	ctr := m.base()

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

// runOne executes one TLC configuration and returns "" on the expected
// outcome, or a human-readable failure line. TLC exits nonzero on
// violations, so the exec swallows the exit code and the output is parsed
// instead.
func runOne(ctx context.Context, base *dagger.Container, name, expect string) string {
	cmd := fmt.Sprintf(
		"java -XX:+UseParallelGC -cp /tla2tools.jar tlc2.TLC -workers auto -deadlock -config CacheLifecycle_%s.cfg CacheLifecycle.tla 2>&1 | tee /tmp/out.txt; true",
		name)
	out, err := base.WithExec([]string{"sh", "-c", cmd}).Stdout(ctx)
	if err != nil {
		return fmt.Sprintf("- %s: could not run TLC: %v", name, err)
	}

	clean := strings.Contains(out, "No error has been found")
	violated := ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "Error: Invariant "); ok {
			// The invariant name is the first word; the rest is either
			// " is violated." or " is violated by the initial state:".
			violated = strings.Fields(rest)[0]
			break
		}
	}

	switch {
	case expect == "" && clean:
		return ""
	case expect == "" && violated != "":
		return fmt.Sprintf("- %s: expected a clean pass, but invariant %s was violated — a regression in the modeled cache behavior or the spec", name, violated)
	case expect != "" && violated == expect:
		return ""
	case expect != "" && clean:
		return fmt.Sprintf("- %s: expected invariant %s to be violated (this configuration reproduces a known bug or finding), but the run came up clean — the model or config no longer reproduces it", name, expect)
	case expect != "" && violated != "":
		return fmt.Sprintf("- %s: expected invariant %s to be violated, but %s was violated instead — the configuration drifted", name, expect, violated)
	default:
		tail := out
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return fmt.Sprintf("- %s: unrecognized TLC outcome (no clean pass, no invariant violation); output tail:\n%s", name, tail)
	}
}
