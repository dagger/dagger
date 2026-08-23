package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
)

// Trace-backed agent resume restores a past session's agents, conversations,
// lifecycle state, and TUI history into the new interactive session.
//
// Everything below the CLI is already built: internal/cloud fetches the trace,
// engine/telemetry imports it into the live frontend's own exporters,
// dagui.DB projects the restore plan, and Agent.rehydrate re-creates each
// instance's runtime entry from its committed conversation. This is the
// wiring, and the order it happens in — which is load-bearing rather than
// incidental (§3.1b, recommendation §6.2's seed race):
//
//  1. Fetch, under a span so the wait is visible, before the interactive loop
//     starts.
//  2. Rebuild EVERY entry's anchor. An anchor that will not rebuild fails the
//     command here, before anything has been re-hydrated, so a refused
//     restore leaves the engine untouched.
//  3. Re-hydrate every entry, before anything can dispatch a tool or bind an
//     LLM: the chief's recorded chain binds its workers by ID, and a dispatch
//     that resolves against a registry missing one is an error (§4.2) rather
//     than an amnesiac twin.
//  4. Then attach, adopting each restored instance as a conversation.
//  5. Then focus (§3.1c). No Replay: the imported spans ARE the scrollback
//     (§5.1.4).

// traceRestore describes one resume request. An empty traceID asks the source
// to select from its retained traces.
type traceRestore struct {
	traceID string
	timeout time.Duration
	agent   string
	source  traceRestoreSource
}

// traceRestoreSource is the transport boundary for trace selection and import.
// The engine archive client can implement this without coupling restore plan
// execution to its wire protocol. Until that client is wired, explicit IDs use
// the existing Cloud importer and picker selection fails clearly.
type traceRestoreSource interface {
	Select(context.Context) (string, error)
	Import(context.Context, string, time.Duration) (bool, error)
}

type cloudTraceRestoreSource struct{}

func (cloudTraceRestoreSource) Select(context.Context) (string, error) {
	return "", errors.New("engine archive picker unavailable; resume an explicit trace with dagger agent -r=<trace-id>")
}

func (cloudTraceRestoreSource) Import(ctx context.Context, traceID string, timeout time.Duration) (bool, error) {
	return fetchTraceForRestore(ctx, traceRestore{traceID: traceID, timeout: timeout}, fetchTraceIntoFrontend)
}

// agentRestoreSource is the frontend seam the plan is read through
// (idtui.AgentRestorer). Named here so the executor can be driven by a fake.
type agentRestoreSource interface {
	AgentRestorePlan() []dagui.AgentRestore
	EncodedIDForCallDigest(digest string) (string, error)
}

// restoreTarget is the session half of a restore: the three verbs the plan is
// executed with. It is an interface so §5.3's ORDER — every re-hydration
// before any attach, focus last — is testable without an engine, in the style
// of session_agent_test.go's fake runtime.
type restoreTarget interface {
	// Rehydrate re-creates the instance's runtime entry from the conversation
	// its anchor rebuilt to, returning the encoded handle on the restored
	// agent.
	Rehydrate(ctx context.Context, entry dagui.AgentRestore, snapshotID string) (string, error)
	// Adopt makes a re-hydrated instance a conversation of this session.
	Adopt(ctx context.Context, entry dagui.AgentRestore, agentID string) error
	// Focus points the prompt at one of the adopted conversations.
	Focus(ctx context.Context, entry dagui.AgentRestore, agentID string) error
}

// restoreFromTrace runs the whole of §5.3 against the live session.
func restoreFromTrace(ctx context.Context, handler *shellCallHandler, req traceRestore) (rerr error) {
	// Both startup resume and .resume enter here. The interactive command may
	// resume only before any agent has been prompted, spawned, or restored.
	if !handler.llmSession.Pristine() {
		return errors.New("the interactive session has already started agent work; start a new session with dagger agent -r")
	}

	source := req.source
	if source == nil {
		source = cloudTraceRestoreSource{}
	}
	if req.traceID == "" {
		traceID, err := source.Select(ctx)
		if err != nil {
			return err
		}
		if traceID == "" {
			return nil // picker aborted
		}
		req.traceID = traceID
	}

	// The plan and anchor rebuilds are reads of the frontend's DB, which the
	// frontend owns single-threaded. A frontend with no span DB cannot restore.
	restorer, ok := Frontend.(idtui.AgentRestorer)
	if !ok {
		return fmt.Errorf("--resume needs a frontend that keeps the trace: %T cannot restore from one", Frontend)
	}

	ctx, span := Tracer().Start(ctx, "resuming trace "+req.traceID, telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)

	timedOut, err := source.Import(ctx, req.traceID, req.timeout)
	if err != nil {
		return err
	}
	if timedOut {
		restoreNotice(ctx, fmt.Sprintf(
			"trace streams were idle for %s; restoring strictly from data received so far", req.timeout))
	}

	// Every resume starts from a fresh unbound LLM. Restored snapshot recipes
	// carry their original workspace and composition; the destination checkout
	// must not become their reset base.
	baseID, err := freshAgentBase(ctx, handler.dag)
	if err != nil {
		return err
	}
	base := dagger.Ref[*dagger.LLM](handler.dag, dagger.ID(baseID))
	if err := handler.llmSession.Target().setInitialLLM(base); err != nil {
		return err
	}

	// Import failures leave the session pristine. Once plan execution begins it
	// may create runtimes, so reserve the session against another .resume first.
	if !handler.llmSession.BeginRestore() {
		return errors.New("the interactive session has already started agent work; start a new session with dagger agent -r")
	}
	target := &sessionRestore{
		dag:     handler.dag,
		session: handler.llmSession,
		base:    handler.llmSession.Target().initialLLM,
	}
	return executeRestorePlan(ctx, restorer, target, req)
}

// traceFetcher is the Cloud fetch seam used to test timeout policy without a
// frontend or credentials.
type traceFetcher func(context.Context, string, time.Duration) error

// fetchTraceForRestore classifies an opted-in idle timeout separately from
// transport failures. Plan execution remains strict over the records imported
// before the stall. Idle time is not total transfer time.
func fetchTraceForRestore(ctx context.Context, req traceRestore, fetch traceFetcher) (bool, error) {
	err := fetch(ctx, req.traceID, req.timeout)
	if err == nil {
		return false, nil
	}
	if req.timeout > 0 && errors.Is(err, cloud.ErrStreamStalled) {
		return true, nil
	}
	return false, err
}

// fetchTraceIntoFrontend streams the whole trace into the LIVE frontend's own
// exporters (§5.1): one DB then holds both sessions, which is what makes the
// restored session the old session's TUI plus a live prompt.
//
// Two things the reference trace client does and this must not: Seal (the
// fetch does it internally, once the span stream has drained) and SetPrimary
// (§5.1.1 — the live CLI's root stays the primary span, and repointing it
// would take the restore plan's live-vs-imported discriminator with it).
func fetchTraceIntoFrontend(ctx context.Context, traceID string, idleTimeout time.Duration) error {
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return fmt.Errorf("cloud auth: %w", err)
	}
	client, err := cloud.NewOTLPClient(ctx, cloudAuth)
	if err != nil {
		return fmt.Errorf("cloud client: %w", err)
	}
	if idleTimeout > 0 {
		client = client.WithStallTimeout(idleTimeout)
	}
	sink := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   Frontend.SpanExporter(),
		Logs:    Frontend.LogExporter(),
		Metrics: Frontend.MetricExporter(),
	})
	if err := client.FetchTrace(ctx, traceID, sink); err != nil {
		return fmt.Errorf("fetch trace %s: %w", traceID, err)
	}
	// The largest fetch in the product, on the path where the user is
	// waiting: --debug says how much came down. Through slog rather than
	// stderr, which the interactive frontend owns.
	slog.Debug("restored trace from cloud", "trace", traceID, "stats", client.StatsSummary())
	return nil
}

// restoredAgent is one entry of the plan, with the handle its anchor rebuilt
// to and (after re-hydration) the handle on its restored runtime.
type restoredAgent struct {
	entry      dagui.AgentRestore
	snapshotID string
	agentID    string
}

func executeRestorePlan(ctx context.Context, src agentRestoreSource, dst restoreTarget, req traceRestore) error {
	plan := src.AgentRestorePlan()
	if len(plan) == 0 {
		return fmt.Errorf("trace %s carries no agents to restore: "+
			"either nothing in it published an agent loop, or its agents are already restored in this session",
			req.traceID)
	}

	// Phase 1: resolve every anchor, refusing before anything is created.
	restoring := make([]restoredAgent, 0, len(plan))
	for _, entry := range plan {
		snapshotID, err := resolveAnchor(src, entry)
		if err != nil {
			return err
		}
		restoring = append(restoring, restoredAgent{entry: entry, snapshotID: snapshotID})
	}

	// Phase 2: re-hydrate everything, before anything can address any of it.
	for i, restored := range restoring {
		agentID, err := dst.Rehydrate(ctx, restored.entry, restored.snapshotID)
		if err != nil {
			return fmt.Errorf("re-hydrate agent %q (%s): %w", restored.entry.Name, restored.entry.ID, err)
		}
		restoring[i].agentID = agentID
	}

	// Phase 3: adopt them as this session's conversations.
	for _, restored := range restoring {
		if err := dst.Adopt(ctx, restored.entry, restored.agentID); err != nil {
			return fmt.Errorf("attach to restored agent %q (%s): %w",
				restored.entry.Name, restored.entry.ID, err)
		}
	}

	// Phase 4: point the prompt at one of them.
	focus, notice, err := selectFocus(restoring, req.agent)
	if err != nil {
		return err
	}
	if notice != "" {
		restoreNotice(ctx, notice)
	}
	return dst.Focus(ctx, focus.entry, focus.agentID)
}

// resolveAnchor turns an entry's snapshot digest into the encoded ID of the
// conversation to re-hydrate it from.
func resolveAnchor(src agentRestoreSource, entry dagui.AgentRestore) (string, error) {
	if !entry.Restorable() {
		return "", entry.Err
	}
	snapshotID, err := src.EncodedIDForCallDigest(entry.SnapshotDigest)
	if err != nil {
		return "", fmt.Errorf("agent %q (%s) cannot be restored from anchor %s: %w",
			entry.Name, entry.ID, entry.SnapshotDigest, err)
	}
	return snapshotID, nil
}

// selectFocus applies §3.1c: focus the agent with no agent above it. Several
// top-level agents means the most recently active one, and saying so;
// --agent <name|id> overrides the whole rule.
func selectFocus(restored []restoredAgent, want string) (restoredAgent, string, error) {
	if want != "" {
		return focusByName(restored, want)
	}

	// A worker's loop span is started under its chief's tool-call span, so
	// "no agent above it" is a fact the projection already carries.
	toplevel := slices.DeleteFunc(slices.Clone(restored), func(r restoredAgent) bool {
		return r.entry.ParentAgentID != ""
	})
	if len(toplevel) == 0 {
		return restoredAgent{}, "", errors.New("restored trace has no top-level agent")
	}
	if len(toplevel) == 1 {
		return toplevel[0], focusLabel(toplevel[0]), nil
	}

	// Most recently active, which is NOT the plan's order: the plan is
	// ordered by when each agent first appeared, and a session's own
	// conversation is usually the first to appear and the last to speak.
	sort.SliceStable(toplevel, func(i, j int) bool {
		return toplevel[j].entry.LastActivity.Before(toplevel[i].entry.LastActivity)
	})
	focus := toplevel[0]
	return focus, fmt.Sprintf(
		"%d top-level agents restored; focusing %s, which was active most recently — "+
			"pass --agent <name|id> to focus another",
		len(toplevel), focusLabel(focus)), nil
}

func focusByName(restored []restoredAgent, want string) (restoredAgent, string, error) {
	// Instance IDs first: a name is a display label that two agents may
	// legitimately share, and an ID never is.
	for _, r := range restored {
		if r.entry.ID == want {
			return r, "", nil
		}
	}
	var matched []restoredAgent
	for _, r := range restored {
		if r.entry.Name == want {
			matched = append(matched, r)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], "", nil
	case 0:
		names := make([]string, 0, len(restored))
		for _, r := range restored {
			names = append(names, focusLabel(r))
		}
		return restoredAgent{}, "", fmt.Errorf("no restored agent named %q; the trace restored: %s",
			want, strings.Join(names, ", "))
	default:
		ids := make([]string, 0, len(matched))
		for _, r := range matched {
			ids = append(ids, r.entry.ID)
		}
		return restoredAgent{}, "", fmt.Errorf(
			"%d restored agents are named %q; name one by instance ID instead: %s",
			len(matched), want, strings.Join(ids, ", "))
	}
}

func focusLabel(r restoredAgent) string {
	return fmt.Sprintf("%s (%s)", r.entry.Name, r.entry.ID)
}

// restoreNotice surfaces a line about the restore in the TUI. Revealed rather
// than logged: it describes a decision the user may want to override, and it
// has to survive the restore span it is emitted under.
func restoreNotice(ctx context.Context, msg string) {
	_, span := Tracer().Start(ctx, msg, telemetry.Reveal())
	span.End()
}

// sessionRestore executes a plan against the interactive session.
type sessionRestore struct {
	dag     *dagger.Client
	session *LLMSession
	// base is the composed agent group `dagger agent` started with, kept as
	// each restored conversation's reset target so .clear returns to the
	// selected agents rather than a blank workspace-bound LLM.
	base *dagger.LLM
}

var _ restoreTarget = (*sessionRestore)(nil)

// rehydrateQuery is design §3.2's restore chain: load the committed
// conversation, address the instance it belonged to, and re-create its
// runtime entry from it. Written out rather than driven through the generated
// client because the value needed is the ENCODED handle rehydrate returns —
// the ID LLMSession.Attach adopts the agent by — and the client would
// re-select it as a fresh chain.
const rehydrateQuery = `query Rehydrate($llm: ID!, $id: String!, $name: String!, $state: AgentState!, $error: String!) {
  node(id: $llm) {
    ... on LLM {
      agent(id: $id, name: $name) { rehydrate(state: $state, error: $error) }
    }
  }
}`

func (r *sessionRestore) Rehydrate(ctx context.Context, entry dagui.AgentRestore, snapshotID string) (string, error) {
	var res struct {
		Node struct {
			Agent struct {
				Rehydrate string
			}
		}
	}
	if err := r.dag.Do(ctx, &dagger.Request{
		Query:  rehydrateQuery,
		OpName: "Rehydrate",
		Variables: map[string]any{
			"llm":   snapshotID,
			"id":    entry.ID,
			"name":  entry.Name,
			"state": entry.State,
			"error": entry.Error,
		},
	}, &dagger.Response{Data: &res}); err != nil {
		return "", err
	}
	if res.Node.Agent.Rehydrate == "" {
		return "", errors.New("the engine returned no handle on the restored agent")
	}
	return res.Node.Agent.Rehydrate, nil
}

func (r *sessionRestore) Adopt(ctx context.Context, entry dagui.AgentRestore, agentID string) error {
	conv, err := r.session.AttachRestored(ctx, entry.ID, entry.Name, agentID)
	if err != nil {
		return err
	}
	conv.initialLLM = r.base
	return nil
}

func (r *sessionRestore) Focus(ctx context.Context, entry dagui.AgentRestore, agentID string) error {
	return r.session.Focus(ctx, entry.ID, entry.Name, agentID)
}

// validateAgentResumeFlags rejects resume combinations that have no meaning
// before any engine work begins.
func validateAgentResumeFlags(
	resume bool,
	timeout time.Duration,
	timeoutSet bool,
	agentSet bool,
	args []string,
) error {
	if timeout < 0 {
		return errors.New("--resume-timeout cannot be negative")
	}
	if !resume {
		if timeoutSet {
			return errors.New("--resume-timeout requires -r/--resume")
		}
		if agentSet {
			return errors.New("--agent requires -r/--resume")
		}
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("-r/--resume cannot be combined with agent names (%s): "+
			"a restored session's agents come from the trace, not from the workspace",
			strings.Join(args, ", "))
	}
	return nil
}
