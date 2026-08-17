package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
)

// `dagger agent --trace <TRACE_ID>` — restoring a past session's agents, their
// conversations and its whole TUI into the session in front of you
// (hack/designs/resume-from-trace.md §5.3, §5.4).
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

// traceRestore is what `--trace` asks for.
type traceRestore struct {
	// traceID is the Cloud trace to restore from.
	traceID string
	// agent names the conversation to focus, by instance ID or display name,
	// overriding §3.1c's automatic choice.
	agent string
	// partial opts into a best-effort restore: entries the trace does not
	// carry enough to restore are skipped instead of failing the command.
	partial bool
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
	// The plan and the anchor rebuilds are reads of the frontend's DB, which
	// the frontend owns single-threaded (§5.1, "Reading the DB back"). A
	// frontend with no span DB cannot restore at all, and says so rather than
	// restoring nothing.
	restorer, ok := Frontend.(idtui.AgentRestorer)
	if !ok {
		return fmt.Errorf("--trace needs a frontend that keeps the trace: %T cannot restore from one", Frontend)
	}

	ctx, span := Tracer().Start(ctx, "restoring trace "+req.traceID, telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)

	if err := fetchTraceIntoFrontend(ctx, req.traceID); err != nil {
		return err
	}

	target := &sessionRestore{
		dag:     handler.dag,
		session: handler.llmSession,
		base:    handler.llmSession.Target().initialLLM,
	}
	return executeRestorePlan(ctx, restorer, target, req)
}

// fetchTraceIntoFrontend streams the whole trace into the LIVE frontend's own
// exporters (§5.1): one DB then holds both sessions, which is what makes the
// restored session the old session's TUI plus a live prompt.
//
// Two things the reference trace client does and this must not: Seal (the
// fetch does it internally, once the span stream has drained) and SetPrimary
// (§5.1.1 — the live CLI's root stays the primary span, and repointing it
// would take the restore plan's live-vs-imported discriminator with it).
func fetchTraceIntoFrontend(ctx context.Context, traceID string) error {
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return fmt.Errorf("cloud auth: %w", err)
	}
	client, err := cloud.NewOTLPClient(ctx, cloudAuth)
	if err != nil {
		return fmt.Errorf("cloud client: %w", err)
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
	//
	// Both refusals are the same kind and are reported the same way: the
	// projection's (a stop with no reason, no anchor at all — §5.2) and the
	// rebuild's (a frame whose payload never reached this client — §9's first
	// row). Neither degrades to a partial restore unless asked, because a
	// missing worker is exactly the hole a later tool dispatch falls into,
	// and that error would arrive minutes later with none of this context.
	var (
		restoring []restoredAgent
		skipped   []string
	)
	for _, entry := range plan {
		snapshotID, err := resolveAnchor(src, entry)
		if err != nil {
			if !req.partial {
				return fmt.Errorf("%w\n\npass --partial to restore the rest of the trace without it", err)
			}
			skipped = append(skipped, fmt.Sprintf("%s (%s): %v", entry.Name, entry.ID, err))
			continue
		}
		restoring = append(restoring, restoredAgent{entry: entry, snapshotID: snapshotID})
	}
	if len(restoring) == 0 {
		return fmt.Errorf("no agent in trace %s could be restored:\n  %s",
			req.traceID, strings.Join(skipped, "\n  "))
	}
	for _, skip := range skipped {
		restoreNotice(ctx, "skipped unrestorable agent "+skip)
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
	notice := ""
	if len(toplevel) == 0 {
		// Only reachable under --partial, where the chief an entry names as
		// its parent may be one of the skipped ones. Focus among what there
		// is rather than refusing to focus at all.
		toplevel = restored
		notice = "no top-level agent was restored; focusing "
	}
	if len(toplevel) == 1 {
		return toplevel[0], notice + focusLabel(toplevel[0]), nil
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

// validateAgentTraceFlags rejects the combinations §5.4 rules out, before any
// engine work happens.
func validateAgentTraceFlags(traceID string, resume bool, args []string) error {
	if traceID == "" {
		return nil
	}
	if resume {
		// Two stores, one conversation: a saved session and a trace both
		// claim to say what the conversation is, and nothing decides between
		// them. (The direction §5.4 sketches — the save file as a pointer AT
		// a trace — makes this one flag later, not two.)
		return errors.New("--trace cannot be combined with -r/--resume: " +
			"a saved session and a trace are two stores for one conversation")
	}
	if len(args) > 0 {
		// Composition comes from the trace: the restored agents are the ones
		// the source session actually had, not the ones currentWorkspace
		// offers today.
		return fmt.Errorf("--trace cannot be combined with agent names (%s): "+
			"a restored session's agents come from the trace, not from the workspace",
			strings.Join(args, ", "))
	}
	return nil
}
