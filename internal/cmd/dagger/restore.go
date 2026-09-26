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
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

// Trace restoration uses verified canonical control facts and their payload
// closure. Bootstrap application, anchor resolution, runtime creation, graph
// installation, and prompt attachment are distinct ordered phases. Original
// historical telemetry arrives after focus; it is never re-emitted by an LLM.

// traceRestore names the trace to restore and how to reach its archive.
type traceRestore struct {
	traceID     string
	agent       string
	source      archiveRestoreSource
	cloudSource cloudRestoreSource
}

// agentRestoreSource is the frontend seam the plan is read through
// (idtui.AgentRestorer). Named here so the executor can be driven by a fake.
type agentRestoreSource interface {
	AgentRestorePlan() []dagui.AgentRestore
	EncodedIDForCallDigest(digest string) (string, error)
}

// restoreTarget is the session half of a restore: the verbs the plan is
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
	// Subscribe reinstalls a recorded filter through notify. The watched agent
	// is restored and not yet activated, so this announces nothing.
	Subscribe(ctx context.Context, watchedID, subscriberID string, states []string) error
}

// restoreFromTrace imports only verified bootstrap state before activating the
// prompt. Its cleanup joins asynchronous original-history import on CLI exit.
func restoreFromTrace(ctx context.Context, handler *shellCallHandler, req traceRestore) (_ func(), rerr error) {
	fe, ok := Frontend.(archiveFrontend)
	if !ok {
		return nil, fmt.Errorf("-r/--resume needs a frontend that keeps the trace: %T cannot restore from one", Frontend)
	}
	if req.source == nil {
		return nil, errors.New("-r/--resume requires an authenticated archive source")
	}
	ctx, span := Tracer().Start(ctx, "restoring trace "+req.traceID, telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)
	return restoreTraceSources(ctx, fe, &sessionRestore{dag: handler.dag, session: handler.llmSession}, req)
}

// restoredAgent is one entry of the plan, with the handle its anchor rebuilt
// to and (after re-hydration) the handle on its restored runtime.
type restoredAgent struct {
	entry      dagui.AgentRestore
	snapshotID string
	agentID    string
}

func executeRestorePlan(ctx context.Context, src agentRestoreSource, dst restoreTarget, req traceRestore) error {
	return executeRestoreGraph(ctx, src, dst, req, nil)
}

func executeRestoreGraph(ctx context.Context, src agentRestoreSource, dst restoreTarget, req traceRestore, subscriptions []agentcontrol.Subscription) error {
	plan := src.AgentRestorePlan()
	if len(plan) == 0 {
		return fmt.Errorf("trace %s carries no agents to restore", req.traceID)
	}
	plan, err := parentFirst(plan)
	if err != nil {
		return err
	}

	// Resolve every anchor, validate every edge and check focus before creating
	// any runtime. Restore is best-effort: an agent the trace does not carry
	// enough to restore is skipped with a warning naming it and why, rather
	// than costing the rest of the session. A kept agent may still reference a
	// skipped worker; a tool call addressing it fails when dispatched.
	roster := make(map[string]dagui.AgentRestore, len(plan))
	var (
		restoring []restoredAgent
		skipped   []string
	)
	for _, entry := range plan {
		roster[entry.ID] = entry
		snapshotID, err := resolveAnchor(src, entry)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", restoreLabel(entry), err))
			warnAgentNotRestored(entry, err)
			continue
		}
		restoring = append(restoring, restoredAgent{entry: entry, snapshotID: snapshotID})
	}
	for _, edge := range subscriptions {
		if err := edge.Validate(); err != nil {
			return fmt.Errorf("invalid restored subscription: %w", err)
		}
		for _, handle := range []string{edge.Watched, edge.Subscriber} {
			entry, ok := roster[handle]
			if !ok {
				return fmt.Errorf("subscription endpoint %q is outside restore roster", handle)
			}
			if entry.Source.Namespace != edge.Namespace {
				return fmt.Errorf("subscription endpoint %q belongs to another source namespace", handle)
			}
		}
	}
	if len(restoring) == 0 {
		return fmt.Errorf("no agent in trace %s could be restored:\n  %s",
			req.traceID, strings.Join(skipped, "\n  "))
	}
	if _, _, err := selectFocus(restoring, req.agent); err != nil {
		return err
	}

	// Runtimes are created inert: nothing runs until the prompt does, and
	// nothing can address them until they are adopted. An agent the engine
	// refuses is skipped like one whose anchor did not resolve.
	restored := make([]restoredAgent, 0, len(restoring))
	agentIDs := make(map[string]string, len(restoring))
	for _, r := range restoring {
		agentID, err := dst.Rehydrate(ctx, r.entry, r.snapshotID)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", restoreLabel(r.entry), err))
			warnAgentNotRestored(r.entry, err)
			continue
		}
		r.agentID = agentID
		restored = append(restored, r)
		agentIDs[r.entry.ID] = agentID
	}
	if len(restored) == 0 {
		return fmt.Errorf("no agent in trace %s could be restored:\n  %s",
			req.traceID, strings.Join(skipped, "\n  "))
	}

	// Install the recorded graph before attaching the prompt. The watched
	// agents have not been activated, so notify does not announce their
	// restored states; only transitions after the restore notify.
	for _, edge := range subscriptions {
		if len(edge.States) == 0 {
			continue // removal tombstone, not a historical edge to resurrect
		}
		watched, watchedOK := agentIDs[edge.Watched]
		subscriber, subscriberOK := agentIDs[edge.Subscriber]
		if !watchedOK || !subscriberOK {
			slog.Warn("dropped subscription to an agent that was not restored",
				"watched", edge.Watched, "subscriber", edge.Subscriber)
			continue
		}
		if err := dst.Subscribe(ctx, watched, subscriber, edge.States); err != nil {
			slog.Warn("dropped subscription that could not be restored",
				"watched", edge.Watched, "subscriber", edge.Subscriber, "reason", err)
		}
	}
	for _, r := range restored {
		if err := dst.Adopt(ctx, r.entry, r.agentID); err != nil {
			return fmt.Errorf("attach to restored agent %q (%s): %w", r.entry.Name, r.entry.ID, err)
		}
	}
	focus, notice, err := selectFocus(restored, req.agent)
	if err != nil {
		return err
	}
	if notice != "" {
		restoreNotice(ctx, notice)
	}
	return dst.Focus(ctx, focus.entry, focus.agentID)
}

// warnAgentNotRestored reports an agent a best-effort restore skipped.
func warnAgentNotRestored(entry dagui.AgentRestore, reason error) {
	slog.Warn("agent not restored", "agent", restoreLabel(entry), "reason", reason)
}

func restoreLabel(entry dagui.AgentRestore) string {
	return fmt.Sprintf("%s (%s)", entry.Name, entry.ID)
}

// parentFirst rejects ambiguous handles, missing parents and lineage cycles.
// A stable DFS retains archive order among independent agents.
func parentFirst(plan []dagui.AgentRestore) ([]dagui.AgentRestore, error) {
	byID := make(map[string]dagui.AgentRestore, len(plan))
	for _, entry := range plan {
		if _, ok := byID[entry.ID]; ok || entry.ID == "" {
			return nil, fmt.Errorf("duplicate or empty agent handle %q in restore roster", entry.ID)
		}
		byID[entry.ID] = entry
	}
	var ordered []dagui.AgentRestore
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		entry, ok := byID[id]
		if !ok {
			return fmt.Errorf("parent %q is outside restore roster", id)
		}
		if visiting[id] {
			return fmt.Errorf("cycle in restored lineage at agent %q", id)
		}
		visiting[id] = true
		if entry.ParentAgentID != "" {
			if err := visit(entry.ParentAgentID); err != nil {
				return err
			}
		}
		visited[id] = true
		ordered = append(ordered, entry)
		return nil
	}
	for _, entry := range plan {
		if err := visit(entry.ID); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// resolveAnchor turns an entry's snapshot digest into the encoded ID of the
// conversation to re-hydrate it from. Errors describe why without naming the
// agent: callers already label them.
func resolveAnchor(src agentRestoreSource, entry dagui.AgentRestore) (string, error) {
	if !entry.Restorable() {
		return "", entry.Err
	}
	snapshotID, err := src.EncodedIDForCallDigest(entry.SnapshotDigest)
	if err != nil {
		return "", fmt.Errorf("snapshot %s does not rebuild: %w", entry.SnapshotDigest, err)
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
		// The chief an entry names as its parent was skipped. Focus among
		// what there is rather than refusing to focus at all.
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
			"%d restored agents are named %q; name one by runtime handle instead: %s",
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
}

var _ restoreTarget = (*sessionRestore)(nil)

// rehydrateQuery is design §3.2's restore chain: load the committed
// conversation and spawn it under the handle of the instance it belonged
// to, which re-creates that instance's runtime entry from it. Written out
// rather than driven through the generated client because the value needed
// is the ENCODED handle spawn returns — the ID LLMSession.Attach adopts the
// agent by — and the client would re-select it as a fresh chain.
const rehydrateQuery = `query Rehydrate($llm: ID!, $id: String!, $name: String!, $state: AgentState!, $error: String!, $parent: String!) {
  node(id: $llm) {
    ... on LLM {
      spawn(handle: $id, name: $name, state: $state, error: $error, parentHandle: $parent)
    }
  }
}`

func (r *sessionRestore) Rehydrate(ctx context.Context, entry dagui.AgentRestore, snapshotID string) (string, error) {
	var res struct {
		Node struct {
			Spawn string
		}
	}
	if err := r.dag.Do(ctx, &dagger.Request{
		Query:  rehydrateQuery,
		OpName: "Rehydrate",
		Variables: map[string]any{
			"llm":    snapshotID,
			"id":     entry.ID,
			"name":   entry.Name,
			"state":  entry.State,
			"error":  entry.Error,
			"parent": entry.ParentAgentID,
		},
	}, &dagger.Response{Data: &res}); err != nil {
		return "", err
	}
	if res.Node.Spawn == "" {
		return "", errors.New("the engine returned no handle on the restored agent")
	}
	return res.Node.Spawn, nil
}

func (r *sessionRestore) Subscribe(ctx context.Context, watchedID, subscriberID string, states []string) error {
	return r.dag.Do(ctx, &dagger.Request{
		Query: `query RestoreSubscription($watched: ID!, $subscriber: ID!, $states: [AgentState!]!) {
  node(id: $watched) { ... on Agent { notify(subscriber: $subscriber, on: $states) } }
}`,
		Variables: map[string]any{"watched": watchedID, "subscriber": subscriberID, "states": states},
	}, &dagger.Response{})
}

func (r *sessionRestore) Adopt(ctx context.Context, entry dagui.AgentRestore, agentID string) error {
	_, err := r.session.AttachRestored(ctx, entry.ID, entry.Name, agentID)
	return err
}

func (r *sessionRestore) Focus(ctx context.Context, entry dagui.AgentRestore, agentID string) error {
	return r.session.Focus(ctx, entry.ID, entry.Name, agentID)
}

// validateAgentTraceFlags rejects the combinations §5.4 rules out, before any
// engine work happens.
func validateAgentTraceFlags(traceID string, args []string) error {
	if traceID == "" {
		return nil
	}
	if len(args) > 0 {
		// Composition comes from the trace: the restored agents are the ones
		// the source session actually had, not the ones currentWorkspace
		// offers today.
		return fmt.Errorf("-r/--resume cannot be combined with agent names (%s): "+
			"a restored session's agents come from the trace, not from the workspace",
			strings.Join(args, ", "))
	}
	return nil
}
