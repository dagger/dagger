// TLA+ model checking for the dagql cache specs (dagql/tla).
//
// Runs every TLC configuration of CacheLifecycle.tla, single-tier. Green
// configurations are regression gates: any violation fails the check. Red
// configurations are self-verifying reproduction artifacts: each must
// violate exactly its designated invariant — coming up green, or violating
// a different invariant, fails the check, because either means the model
// or a configuration drifted from the bug or finding it reproduces.
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
	// green: regression gates
	"fixed":           "",
	"asis":            "",
	"liveness":        "",
	"lazy_asis":       "",
	"lazy_liveness":   "",
	"persist_asis":    "",
	"persist_liveness": "",
	"flush_roundtrip": "",
	// red: seeded/known bugs
	"bug_canonical":   "NoResurrection",
	"bug_persistable": "PersistableHonored",
	"bug_once_gap":    "ReturnedOwned",
	"bug_lost_cancel": "NoLostCancels",
	// red: mechanism-removal checks
	"no_barrier":           "NoHalfAttachedRead",
	"lazy_no_singleflight": "LazyMutualExclusion",
	// red: findings (ledger IDs F1-F6)
	"release_inflight":          "ReturnedLive",
	"orphan_edges":              "NoOrphanEdgesAtQuiescence",
	"finding_rollback":          "OwnershipExact",
	"finding_rollback_decode":   "OwnershipExact",
	"finding_poisoned":          "NoRetainedPoisonedEntry",
	"finding_poisoned_restart":  "NoLaunderedServe",
	"finding_lazy_stale_cancel": "NoStaleCancelError",
	"finding_flush_inflight":    "FlushCleanCapture",
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
	)
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
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
			"TLA+ cache model check failed (%d of %d configurations):\n%s\n\nSee dagql/tla/README.md ('When this check fails') for what each outcome means and how to reproduce locally.",
			len(failures), len(names), strings.Join(failures, "\n"))
	}
	return nil
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
			violated = strings.TrimSuffix(strings.TrimSpace(rest), " is violated.")
			violated = strings.TrimSuffix(violated, " is violated")
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
