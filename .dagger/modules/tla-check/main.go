// TLA+ model checking for the dagql cache spec (dagql/tla).
//
// Runs every TLC configuration of CacheLifecycle.tla. Green configurations
// are regression checks: any violation fails the check. A configuration may
// name an expected invariant only when it deliberately mutates behavior to
// prove that the check detects the bug, or tracks an accepted model finding.
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
	"snapshot_import": "",
	"snapshot_export": "",
	// RemoteParts.tla and RemoteOwners.tla: remote-cache part acquisition and
	// offer ownership, separate modules. A fault or witness configuration
	// names the one invariant it must violate; a witness is a reachability
	// probe, the negation of what it shows. None is in the quick set.
	"remote_parts":                                  "",
	"remote_parts_fault_certify_sibling":            "ServedOutputIsComplete",
	"remote_parts_fault_accept_after_seal":          "OfferAfterSealCannotPublish",
	"remote_parts_fault_wrong_expectation":          "NoProgressIsUnreachable",
	"remote_parts_fault_wrong_expectation_round":    "",
	"remote_parts_witness_downloaded_fs":            "WitnessDownloadedFsPendingMeta",
	"remote_parts_witness_late_offer":               "WitnessLateOfferWinsDuringPreparing",
	"remote_parts_witness_fs_beside_meta":           "WitnessFsAcquiredBesideProducedMeta",
	"remote_owners":                                 "",
	"remote_owners_fault_release_on_replace":        "OwnerLivesWhileHeld",
	"remote_owners_fault_offer_resources_in_lookup": "OrdinaryHitNotGatedByOffers",
	"remote_owners_fault_retain_owner_in_retry":     "OwnerLivesWhileHeld",
	"remote_owners_witness_old_acquisition":         "WitnessOldAcquisitionSurvivesReplacement",
	"remote_owners_witness_unauthorized_hit":        "WitnessUnauthorizedHitOfferSkipped",
	"remote_owners_witness_offer_row_outlives":      "WitnessOfferRowOutlivesItsRetention",
	// RemoteSharing.tla and RemoteCheckpoint.tla, on the same convention.
	"remote_sharing":                                  "",
	"remote_sharing_decoded":                          "",
	"remote_sharing_fault_finish_before_release":      "MembersReleasedBeforeFinish",
	"remote_sharing_fault_stale_role_map":             "DesiredCoversInstalled",
	"remote_sharing_fault_decrement_before_successor": "MembersAreLive",
	"remote_sharing_fault_typed_two_slots":            "DecodedReceiverOneSlotPerPass",
	"remote_sharing_fault_retry_repins":               "PinsBalanced",
	"remote_sharing_witness_donor_collected":          "WitnessDonorCollectedBeforeReceiverRead",
	"remote_sharing_witness_decoded_while_finish":     "WitnessDecodedWhileFinishPaused",
	"remote_sharing_witness_two_parts_one_pass":       "WitnessTwoPartsInOnePass",
	"remote_sharing_witness_successor_fills":          "WitnessSuccessorFillsDecodedReceiver",
	"remote_sharing_witness_stale_revision":           "WitnessStaleRevisionRefusesSlot",
	"remote_checkpoint":                               "",
	"remote_checkpoint_fault_unpin_before_attach":     "DesiredRolesStayProtected",
	"remote_checkpoint_fault_repeat_producer":         "ProducerRunsOnce",
	"remote_checkpoint_fault_restore_applied_only":    "CheckpointHasCompleteDesiredRoles",
	"remote_checkpoint_fault_drop_operation":          "OperationRetained",
	"remote_checkpoint_witness_desired_survives":      "WitnessInstalledDesiredSurvivesEpoch",
	"remote_checkpoint_witness_last_owner":            "WitnessFailedFinishLastOwnerCollected",
	"remote_checkpoint_witness_producer_preserved":    "WitnessProducerPreservedAcrossRestart",
	"remote_checkpoint_witness_pending_offer":         "WitnessPendingOfferRestored",
	// green: regression checks over the modeled cache behavior. (The
	// former core configuration is folded into resources: same bounds,
	// every core invariant, and strictly more behavior.)
	"release_prune":         "",
	"liveness":              "",
	"lazy":                  "",
	"lazy_liveness":         "",
	"lazy_stale_cancel":     "",
	"lazy_import":           "",
	"persist":               "",
	"persist_liveness":      "",
	"flush_roundtrip":       "",
	"orphan_edges":          "",
	"release_claim_race":    "",
	"drain_orphan":          "",
	"rollback":              "",
	"rollback_decode":       "",
	"lost_cancel":           "",
	"attach_error":          "",
	"attach_error_adoption": "",
	"attach_error_restart":  "",
	"flush_closure":         "",
	"release_inflight":      "",
	"drain_nested_call":     "",
	"flush_inflight":        "",
	"flush_drained":         "",
	"lazy_release":          "",
	"release_wait":          "",
	// mutation: the last canceling waiter must release a completed fn's leases
	"orphaned_lease": "SharedLeaseReleasedWhenRetired",

	// green: per-part evaluation (stage 2). Attempts are per
	// (result, group); parts map to groups; the metadata-first ordering is
	// enforced before a group's attempt exists; delegation bodies demand
	// dependency parts from inside a running body. See the config headers
	// for the recorded probe and re-break evidence.
	"lazy_parts":          "",
	"lazy_parts_prereq":   "",
	"lazy_parts_liveness": "",
	"lazy_parts_delegate": "",
	"lazy_parts_release":  "",

	// Container completion capture and independent local snapshot opening.
	"container_part_restart":  "",
	"container_sweep_restart": "",
	"container_joint_restore": "",

	// green: reader cancellation inside the persisted-decode singleflight.
	// A joiner that wakes on a departed leader's cancellation retries
	// instead of failing (persistDecodeRetry), and a post-install failure
	// leaves persistLeaseSyncPending set so the next demand retries the
	// lease sync; see the config headers.
	"decode_cancel":          "",
	"decode_cancel_liveness": "",

	// green: session-resource validation (the filter in
	// LookupHit/CanonicalPick/FnComplete, PubIndexFresh/PubAttachAddDep
	// maintenance, BindResource, RequiredExact and ReturnedResourcesBound).
	// resources_restart additionally checks the import-time accounting:
	// the dependency-first required recompute at import and the decode
	// installs leaving the stored set alone; see the config headers.
	"resources":         "",
	"resources_restart": "",
	// green: explicit retention edges on already-published results
	// (AddExplicitDependency) accept requirement-carrying deps; the
	// grown stored set cascades to the parent's ancestors and
	// RequiredExact holds the accounting exact. See the config header.
	"resources_latedep": "",

	// green: requirement growth after the lookup filter. The stored set
	// can grow after a hit was selected (an attached dep while
	// attachment is in flight, or a requirement-carrying retention edge
	// after settling); the serve re-validates by the requirement
	// generation captured at selection and converts a stale hit to a
	// miss. resources_requirement_growth covers the attachment window,
	// resources_latedep_recheck the retention-edge window and
	// resources_latedep_cascade the ancestor cascade, each from an
	// imported starting graph; see the config headers.
	"resources_requirement_growth": "",
	"resources_latedep_recheck":    "",
	"resources_latedep_cascade":    "",

	// green: a session's release can no longer manufacture a failure for
	// a live, innocent caller through the attachment machinery. The
	// publisher's own release still fails the publisher, but its barrier
	// error is classified so parked cross-session readers convert to a
	// miss and execute the call themselves; and attachment targets are
	// always pinned for the session (the claim-at-acquisition invariant,
	// with the claim running before the unlocked refresh), so no other
	// session's release can collect a target out from under its claim.
	// See the config header.
	"attach_release_reader": "",
}

var clientExpectedOutcome = map[string]string{
	"core":                  "",
	"shared_work":           "",
	"child":                 "",
	"children":              "",
	"grandchild":            "",
	"teardown":              "",
	"blocking_registration": temporalOutcome,
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

// quickConfigs is the curated cheap subset for Quick: every configuration
// that finishes in seconds (roughly 100k distinct states or fewer). It
// catches a spec that stops parsing and registration drift without paying
// for the big state spaces. Keep it in sync when configurations are added
// or their costs change materially.
var quickConfigs = []string{
	"drain_nested_call",
	"drain_orphan",
	"flush_closure",
	"flush_drained",
	"flush_inflight",
	"flush_roundtrip",
	"lazy_liveness",
	"lazy_parts_liveness",
	"lazy_parts_release",
	"lazy_release",
	"lazy_stale_cancel",
	"liveness",
	"lost_cancel",
	"orphan_edges",
	"attach_error",
	"attach_error_restart",
	"release_inflight",
	"release_claim_race",
}

// CacheLifecycle model-checks every configuration of the dagql cache spec
// and verifies each outcome against its expectation.
//
// WARNING: the full run is expensive - well over an hour wall with four
// TLC JVMs, and the largest configurations reach more than 110 million
// distinct states each. It is not a check, so CI never schedules it: run it
// by hand, `dagger call tla-check cache-lifecycle`, before pushing changes
// under dagql/tla. For iteration prefer Quick (seconds, the check CI runs),
// Some (chosen configurations with their expectations enforced), or One (a
// single configuration, raw output, optional probe injection).
func (m *TlaCheck) CacheLifecycle(ctx context.Context) error {
	names := make([]string, 0, len(expectedOutcome))
	for name := range expectedOutcome {
		names = append(names, name)
	}
	return m.runConfigs(ctx, names)
}

// Quick model-checks only the cheap configurations (quickConfigs), with
// their expectations enforced. It finishes in about a minute, is the check
// CI runs, and is the right default while iterating; it does not replace
// the full CacheLifecycle run before a push.
// +check
func (m *TlaCheck) Quick(ctx context.Context) error {
	return m.runConfigs(ctx, quickConfigs)
}

// Some model-checks the named configurations (without the CacheLifecycle_
// prefix), with their expectations enforced - the middle ground between
// the full check and One, which enforces nothing.
func (m *TlaCheck) Some(
	ctx context.Context,
	// configuration names without the CacheLifecycle_ prefix, e.g.
	// "resources,resources_latedep"
	configs []string,
) error {
	if len(configs) == 0 {
		return fmt.Errorf("some: no configurations named")
	}
	var unknown []string
	for _, name := range configs {
		if _, ok := expectedOutcome[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("some: unknown configurations %s (see expectedOutcome in this module)", strings.Join(unknown, ", "))
	}
	return m.runConfigs(ctx, configs)
}

func (m *TlaCheck) runConfigs(ctx context.Context, names []string) error {
	base := m.base(m.Source)
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	var (
		mu       sync.Mutex
		failures []runFailure
		wg       sync.WaitGroup
		// Each configuration is a TLC JVM of several GiB; unbounded fan-out
		// over 30 configurations exhausted a 64 GiB host.
		sem = make(chan struct{}, 4)
	)
	for _, name := range sorted {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Snapshot configurations live in SnapshotChain.tla; every other
			// name is a CacheLifecycle configuration (see modelFiles).
			model, cfgPath := modelFiles(name)
			specName := strings.TrimSuffix(model, ".tla")
			configPrefix := specName + "_"
			cfgName := strings.TrimSuffix(strings.TrimPrefix(cfgPath, configPrefix), ".cfg")
			if failure := runOne(ctx, base, specName, configPrefix, cfgName, expectedOutcome[name]); failure != nil {
				failure.name = name
				mu.Lock()
				failures = append(failures, *failure)
				mu.Unlock()
			}
		}(name)
	}
	wg.Wait()

	return reportFailures("cache", failures, len(sorted))
}

// ClientLifecycle model-checks client runtime reclamation, typed leases,
// nested-client ownership, authoritative session teardown, and the final
// telemetry barrier. It is not a check, so CI never schedules it: run it
// by hand, `dagger call tla-check client-lifecycle`.
func (m *TlaCheck) ClientLifecycle(ctx context.Context) error {
	base := m.base(m.ClientSource)

	var (
		mu       sync.Mutex
		failures []runFailure
		wg       sync.WaitGroup
		// The same JVM fan-out bound as CacheLifecycle.
		sem = make(chan struct{}, 4)
	)
	run := func(group, specName, configPrefix, name, expect string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
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

	wg.Wait()

	return reportFailures("client lifecycle", failures, len(clientNames))
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
		model, _ := modelFiles(config)
		spec, err := m.Source.File(model).Contents(ctx)
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
		ctr = ctr.WithNewFile("/spec/"+model, spec)
	}

	model, cfgPath := modelFiles(config)
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

	// -Xmx8g: the JVM's default heap is a quarter of host memory, so four
	// concurrent configurations could still overcommit a 64 GiB host.
	cmd := fmt.Sprintf(
		"java -Xmx8g -XX:+UseParallelGC -cp /tla2tools.jar tlc2.TLC -workers auto -deadlock -config %s %s 2>&1; true",
		cfgPath, model)
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
	// -Xmx8g: the JVM's default heap is a quarter of host memory, so four
	// concurrent configurations could still overcommit a 64 GiB host.
	cmd := fmt.Sprintf(
		"java -Xmx8g -XX:+UseParallelGC -cp /tla2tools.jar tlc2.TLC -workers auto -deadlock -config %s%s.cfg %s.tla 2>&1; true",
		configPrefix, name, specName)
	out, err := base.WithExec([]string{"sh", "-c", cmd}).Stdout(ctx)
	if err != nil {
		return &runFailure{name: name, summary: "could not run TLC", detail: err.Error()}
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

// modelFiles preserves the existing short names and adds the snapshot component.
func modelFiles(name string) (string, string) {
	switch name {
	case "snapshot_import":
		return "SnapshotChain.tla", "SnapshotChain_import.cfg"
	case "snapshot_export":
		return "SnapshotChain.tla", "SnapshotChain_export.cfg"
	}
	switch {
	case strings.HasPrefix(name, "remote_parts"):
		return "RemoteParts.tla", fmt.Sprintf("RemoteParts_%s.cfg", name)
	case strings.HasPrefix(name, "remote_owners"):
		return "RemoteOwners.tla", fmt.Sprintf("RemoteOwners_%s.cfg", name)
	case strings.HasPrefix(name, "remote_sharing"):
		return "RemoteSharing.tla", fmt.Sprintf("RemoteSharing_%s.cfg", name)
	case strings.HasPrefix(name, "remote_checkpoint"):
		return "RemoteCheckpoint.tla", fmt.Sprintf("RemoteCheckpoint_%s.cfg", name)
	default:
		return "CacheLifecycle.tla", fmt.Sprintf("CacheLifecycle_%s.cfg", name)
	}
}
