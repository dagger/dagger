package core

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/clientdb"
)

// Agent views for ReadTrace: the roster of agents in the session and in
// imported traces, and one agent's conversation, decoded from its checkpoint
// with the TUI console's transcript code (idtui.DecodeTraceTranscript).
//
// Loading follows the other inspection readers' discipline: scoped, into a
// throwaway dagui.DB that dies with the call. A roster is the stores' agent
// loop spans (from the span index), their ancestor chains, and each agent's
// newest control record (state, parent, checkpoint digest). A transcript adds
// only the call frames of the conversation's LLM receiver spine -- not its
// tools', workspaces' or modules' recipes -- so its cost is linear in the
// conversation, not the session.

// traceViewAgents and traceViewTranscript are the agent views of ReadTrace.
const (
	traceViewAgents     = "agents"
	traceViewTranscript = "transcript"
)

const (
	// traceTimingsDefaultLimit is the timings view's default row cap.
	traceTimingsDefaultLimit = 200
	// transcriptDefaultLimit and transcriptMaxLimit bound a transcript page,
	// in messages; the page also ends at about 40KiB.
	transcriptDefaultLimit = 20
	transcriptMaxLimit     = 100
	// transcriptDefaultClip bounds each tool call's arguments and result.
	transcriptDefaultClip = idtui.TraceTranscriptDefaultClip
	// transcriptInferMaxSpans bounds the spans loaded to infer a
	// conversation that no checkpoint names.
	transcriptInferMaxSpans = 20000
)

// transcriptOpts are ReadTrace's transcript view arguments.
type transcriptOpts struct {
	Span, Agent, Role, Tool, Grep string
	Offset, Limit, Clip           int
}

func (opts transcriptOpts) validate() (*regexp.Regexp, error) {
	switch strings.ToUpper(opts.Role) {
	case "", "USER", "ASSISTANT", "TOOL", "SYSTEM", "GAP":
	default:
		return nil, fmt.Errorf("invalid role %q: want user, assistant, tool, system or gap", opts.Role)
	}
	if opts.Offset < 0 {
		return nil, fmt.Errorf("invalid offset %d: want a non-negative integer", opts.Offset)
	}
	if opts.Limit < 1 || opts.Limit > transcriptMaxLimit {
		return nil, fmt.Errorf("invalid limit %d for the transcript view: want 1 to %d", opts.Limit, transcriptMaxLimit)
	}
	if opts.Clip < 0 {
		return nil, fmt.Errorf("invalid clip %d: want a non-negative integer (0 = unclipped)", opts.Clip)
	}
	if opts.Grep == "" {
		return nil, nil
	}
	re, err := regexp.Compile(opts.Grep)
	if err != nil {
		return nil, fmt.Errorf("invalid grep pattern %q: %w", opts.Grep, err)
	}
	return re, nil
}

// agentRoster is the throwaway DB a roster or transcript is read from.
type agentRoster struct {
	client *clientdb.DB
	db     *dagui.DB
	agents []idtui.TraceAgent
}

// loadAgentRoster loads every inspection store's agents, plus extra spans
// (with their ancestor chains) to place them relative to the agents.
func loadAgentRoster(ctx context.Context, client *clientdb.DB, extra ...string) (*agentRoster, error) {
	db := dagui.NewDB()
	for _, store := range client.InspectionStores() {
		ids := store.AgentSpanIDs()
		for _, id := range extra {
			if store.HasSpan(id) {
				ids[id] = struct{}{}
			}
		}
		if len(ids) > 0 {
			if err := ingestInspectionSpanScope(ctx, client, store, db, store.AncestorClosure(ids)); err != nil {
				return nil, err
			}
		}
		rows, err := store.AgentControlRows(ctx)
		if err != nil {
			return nil, fmt.Errorf("select agent control records: %w", err)
		}
		records := make([]sdklog.Record, 0, len(rows))
		for _, row := range rows {
			// A record that does not decode is left out; the agent still
			// shows from its loop spans, without a state.
			if rec, err := clientdb.DecodeLogRecord(row); err == nil {
				records = append(records, rec)
			}
		}
		db.IngestLogs(records)
	}
	roster := &agentRoster{client: client, db: db, agents: idtui.TraceAgents(db)}
	// An agent's own control record names its parent even where the spans
	// that would (the chief's) were not traced; spans win when they do.
	for i, node := range db.Agents() {
		if roster.agents[i].ParentID == "" && node.Control != nil && node.Control.Parent != node.ID {
			roster.agents[i].ParentID = node.Control.Parent
		}
	}
	return roster, nil
}

func (r *agentRoster) span(id string) *dagui.Span {
	sid, err := trace.SpanIDFromHex(id)
	if err != nil {
		return nil
	}
	span := r.db.Spans.Map[dagui.SpanID{SpanID: sid}]
	if span == nil || !span.Received {
		return nil
	}
	return span
}

// beneath reports whether agent has a loop span at or beneath span.
func (r *agentRoster) beneath(agent idtui.TraceAgent, span *dagui.Span) bool {
	for _, id := range agent.SpanIDs {
		seen := map[*dagui.Span]bool{}
		for s := r.span(id); s != nil && !seen[s]; s = s.ParentSpan {
			seen[s] = true
			if s == span {
				return true
			}
		}
	}
	return false
}

// enclosing returns the agent whose loop span most closely encloses span
// (span itself included).
func (r *agentRoster) enclosing(span *dagui.Span) (idtui.TraceAgent, bool) {
	seen := map[*dagui.Span]bool{}
	for s := span; s != nil && !seen[s]; s = s.ParentSpan {
		seen[s] = true
		if !s.Agent || s.AgentID == "" {
			continue
		}
		for _, agent := range r.agents {
			if agent.ID == s.AgentID {
				return agent, true
			}
		}
	}
	return idtui.TraceAgent{}, false
}

// topBeneath returns the outermost agents at or beneath span.
func (r *agentRoster) topBeneath(span *dagui.Span) []idtui.TraceAgent {
	inside := map[string]bool{}
	var found []idtui.TraceAgent
	for _, agent := range r.agents {
		if r.beneath(agent, span) {
			inside[agent.ID] = true
			found = append(found, agent)
		}
	}
	return slices.DeleteFunc(found, func(agent idtui.TraceAgent) bool {
		return agent.ParentID != "" && inside[agent.ParentID]
	})
}

func (r *agentRoster) byName(name string) (idtui.TraceAgent, error) {
	var matches []idtui.TraceAgent
	for _, agent := range r.agents {
		if agent.ID == name {
			return agent, nil
		}
		if agent.Name == name {
			matches = append(matches, agent)
		}
	}
	switch len(matches) {
	case 0:
		return idtui.TraceAgent{}, fmt.Errorf("no agent %q in this session or loaded traces; ReadTrace(view: %q) lists them", name, traceViewAgents)
	case 1:
		return matches[0], nil
	}
	handles := make([]string, len(matches))
	for i, agent := range matches {
		handles[i] = agent.ID
	}
	return idtui.TraceAgent{}, fmt.Errorf("agent name %q is shared by %d agents; pass a handle: %s", name, len(matches), strings.Join(handles, ", "))
}

// readTraceAgents renders the roster, scoped to the agents at or beneath
// span when one is given.
func readTraceAgents(ctx context.Context, spanArg string) (string, error) {
	client, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return readTraceAgentsIn(ctx, client, spanArg)
}

func readTraceAgentsIn(ctx context.Context, client *clientdb.DB, spanArg string) (string, error) {
	spanID, err := optionalSpanArg(client, spanArg)
	if err != nil {
		return "", err
	}
	var extra []string
	if spanID != "" {
		extra = append(extra, spanID)
	}
	roster, err := loadAgentRoster(ctx, client, extra...)
	if err != nil {
		return "", err
	}
	agents := roster.agents
	header := "Agents in this session and in traces loaded with LoadTrace, as a tree (workers under the agent that spawned them). States in loaded traces are historical observations, not live runtimes."
	if spanID != "" {
		span := roster.span(spanID)
		in := map[string]bool{}
		for _, agent := range agents {
			if span != nil && roster.beneath(agent, span) {
				in[agent.ID] = true
			}
		}
		// An agent seen only through its control record (its loop spans
		// were not traced) is placed by its recorded parent.
		for grew := true; grew; {
			grew = false
			for _, agent := range agents {
				if !in[agent.ID] && len(agent.SpanIDs) == 0 && in[agent.ParentID] {
					in[agent.ID], grew = true, true
				}
			}
		}
		agents = slices.DeleteFunc(slices.Clone(agents), func(agent idtui.TraceAgent) bool {
			return !in[agent.ID]
		})
		header = fmt.Sprintf("Agents at or beneath span %s.", spanID)
		if span != nil {
			if owner, ok := roster.enclosing(span); ok && !slices.ContainsFunc(agents, func(a idtui.TraceAgent) bool { return a.ID == owner.ID }) {
				header += fmt.Sprintf(" The span itself is inside agent %s (%s), span=%s.", owner.ID, owner.Name, lastSpan(owner))
			}
		}
	}
	if len(agents) == 0 {
		if spanID != "" {
			return header + "\n(no agents)\n", nil
		}
		return "(no agents in this session or in loaded traces)\n", nil
	}
	return idtui.RenderTraceAgents(agents, idtui.AgentRosterOpts{
		Recorded: true, All: true, Header: header, ShowSpans: true,
		ReadHint:       fmt.Sprintf("ReadTrace(view: %q, agent: <handle or unique name>), or ReadTrace(span: <its span>) for its trace", traceViewTranscript),
		CheckpointNote: "no checkpoint; transcript inferred best-effort",
	}), nil
}

func lastSpan(agent idtui.TraceAgent) string {
	if len(agent.SpanIDs) == 0 {
		return ""
	}
	return agent.SpanIDs[len(agent.SpanIDs)-1]
}

// optionalSpanArg normalizes an optional span argument and checks it exists.
func optionalSpanArg(client *clientdb.DB, arg string) (string, error) {
	if strings.TrimSpace(arg) == "" {
		return "", nil
	}
	span := normalizeSpanArg(arg)
	if !isHexID(span, 16) {
		return "", fmt.Errorf("invalid span ID %q: want a hex span ID", arg)
	}
	for _, store := range client.InspectionStores() {
		if store.HasSpan(span) {
			return span, nil
		}
	}
	return "", fmt.Errorf("no span %q in this trace; use FindSpans to find a span ID", span)
}

// conversation is a resolved transcript target: an agent's, or (with no
// agent) the LLM conversation traced beneath a span.
type conversation struct {
	agent idtui.TraceAgent
	// span is the span a non-agent conversation was found beneath.
	span string
	// via says how a span argument resolved, for the page header.
	via string
}

// resolveConversation picks the conversation agent or span names. An agent
// handle or unique name is exact. A span resolves, in order, to: the agent
// whose loop span it is; the one outermost agent beneath it (a tool call
// that spawned a worker); an LLM conversation run beneath it outside any
// agent (a module function's own LLM loop) when it is a tool call or in no
// agent; else its nearest enclosing agent.
func (r *agentRoster) resolveConversation(ctx context.Context, agentArg, spanID string) (conversation, error) {
	if agentArg != "" {
		agent, err := r.byName(agentArg)
		return conversation{agent: agent}, err
	}
	span := r.span(spanID)
	if span == nil {
		return conversation{}, fmt.Errorf("no span %q in this trace", spanID)
	}
	if span.Agent && span.AgentID != "" {
		if agent, ok := r.enclosing(span); ok {
			return conversation{agent: agent}, nil
		}
	}
	switch top := r.topBeneath(span); len(top) {
	case 0:
	case 1:
		return conversation{agent: top[0], via: fmt.Sprintf("the agent beneath span %s", spanID)}, nil
	default:
		var names []string
		for _, agent := range top {
			names = append(names, fmt.Sprintf("%s (%s)", agent.ID, agent.Name))
		}
		return conversation{}, fmt.Errorf("%d agents run beneath span %s: %s; pass agent: <handle> to pick one", len(top), spanID, strings.Join(names, ", "))
	}
	owner, inAgent := r.enclosing(span)
	if !inAgent || span.LLMTool != "" {
		found, err := r.loadSpanConversation(ctx, spanID)
		if err != nil {
			return conversation{}, err
		}
		if found {
			return conversation{span: spanID, via: fmt.Sprintf("the LLM conversation beneath span %s (not an agent)", spanID)}, nil
		}
	}
	if inAgent {
		return conversation{agent: owner, via: fmt.Sprintf("the agent enclosing span %s", spanID)}, nil
	}
	return conversation{}, fmt.Errorf("no agent or LLM conversation at or beneath span %s; ReadTrace(view: %q) lists the agents", spanID, traceViewAgents)
}

// transcript decodes c's conversation, loading what the decode needs.
func (r *agentRoster) transcript(ctx context.Context, c conversation) (idtui.TraceAgent, idtui.TraceTranscript, error) {
	if c.span != "" {
		agent := idtui.TraceAgent{ID: "span " + c.span, Name: "conversation", SpanIDs: []string{c.span}}
		span := r.span(c.span)
		digest := idtui.InferConversationDigest(r.db, []*dagui.Span{span}, "")
		if digest == "" {
			return agent, idtui.TraceTranscript{}, fmt.Errorf("no LLM conversation beneath span %s", c.span)
		}
		snap, err := idtui.DecodeTraceConversation(r.db, agent, digest)
		snap.Source = "inferred from traced calls"
		snap.Warning = "no checkpoint names this conversation; it was inferred from its longest traced call chain, so its last messages may be missing"
		return agent, snap, err
	}
	agent := c.agent
	if agent.SnapshotDigest != "" {
		if err := r.loadSpine(ctx, agent.SnapshotDigest); err != nil {
			return agent, idtui.TraceTranscript{}, err
		}
	} else {
		// No control record named a checkpoint: the decode infers one from
		// the calls traced under the agent's own loop spans.
		if _, err := r.loadSubtreeConversation(ctx, agent.SpanIDs, agent.ID); err != nil {
			return agent, idtui.TraceTranscript{}, err
		}
	}
	snap, err := idtui.DecodeTraceTranscript(r.db, agent)
	if err != nil {
		return agent, snap, fmt.Errorf("decode agent %s (%s) conversation: %w", agent.ID, agent.Name, err)
	}
	if agent.SnapshotDigest == "" {
		// The decode's own fallback labels say "recorded checkpoint", which
		// this is not; its GAP message says what may be missing.
		snap.Source = "inferred from traced calls"
		snap.Warning = "no control record names this agent's checkpoint"
	}
	return agent, snap, nil
}

// loadSpine loads the call frames of the conversation ending at digest: its
// LLM receiver chain back to its root, following a module function that
// returned the conversation through its LLM argument, as the decode does.
func (r *agentRoster) loadSpine(ctx context.Context, digest string) error {
	seen := map[string]bool{}
	for digest != "" && !seen[digest] {
		seen[digest] = true
		if err := r.loadCalls(ctx, digest); err != nil {
			return err
		}
		frame := r.db.Calls[digest]
		if frame == nil || frame.GetType().GetNamedType() != "LLM" {
			return nil
		}
		if frame.Module == nil {
			digest = frame.ReceiverDigest
			continue
		}
		// The receiver names the function; the arguments hold the LLM.
		refs := []string{frame.ReceiverDigest}
		for _, arg := range frame.Args {
			refs = append(refs, arg.GetValue().GetCallDigest())
		}
		if err := r.loadCalls(ctx, refs...); err != nil {
			return err
		}
		digest = ""
		for _, arg := range frame.Args {
			dig := arg.GetValue().GetCallDigest()
			if call := r.db.Calls[dig]; call != nil && call.GetType().GetNamedType() == "LLM" {
				if digest == "" || arg.Name == "llm" {
					digest = dig
				}
			}
		}
	}
	return nil
}

// loadCalls loads the frames of digests the DB lacks, from every store.
func (r *agentRoster) loadCalls(ctx context.Context, digests ...string) error {
	pending := map[string]struct{}{}
	for _, digest := range digests {
		if digest != "" && r.db.Calls[digest] == nil {
			pending[digest] = struct{}{}
		}
	}
	for _, store := range r.client.InspectionStores() {
		if len(pending) == 0 {
			return nil
		}
		frames, err := store.SelectCallFrames(ctx, pending)
		if err != nil {
			return fmt.Errorf("select call frames: %w", err)
		}
		if err := ingestCallFrames(ctx, r.db, frames); err != nil {
			return err
		}
		for digest := range pending {
			if r.db.Calls[digest] != nil {
				delete(pending, digest)
			}
		}
	}
	return nil
}

// loadSpanConversation loads the conversation traced beneath span outside
// any agent, reporting whether there is one.
func (r *agentRoster) loadSpanConversation(ctx context.Context, spanID string) (bool, error) {
	found, err := r.loadSubtreeConversation(ctx, []string{spanID}, "")
	if err != nil || !found {
		return false, err
	}
	return idtui.InferConversationDigest(r.db, []*dagui.Span{r.span(spanID)}, "") != "", nil
}

// loadSubtreeConversation loads the spans beneath roots -- not descending
// into other agents' loop spans, and bounded by transcriptInferMaxSpans --
// and the spines of the LLM message calls they traced, which is what
// inferring a conversation tip needs. It reports whether any were found.
func (r *agentRoster) loadSubtreeConversation(ctx context.Context, roots []string, agentID string) (bool, error) {
	if len(roots) == 0 {
		return false, nil
	}
	store := inspectionStoreForSpan(r.client, roots[0])
	agents := store.AgentSpanIDs()
	own := map[string]bool{}
	for _, root := range roots {
		own[root] = true
	}
	scope := map[string]struct{}{}
	queue := slices.Clone(roots)
	for len(queue) > 0 && len(scope) < transcriptInferMaxSpans {
		id := queue[0]
		queue = queue[1:]
		if _, seen := scope[id]; seen {
			continue
		}
		if _, isAgent := agents[id]; isAgent && !own[id] {
			continue
		}
		scope[id] = struct{}{}
		for child := range store.ChildSpanIDs(id) {
			queue = append(queue, child)
		}
	}
	if err := ingestInspectionSpanScope(ctx, r.client, store, r.db, scope); err != nil {
		return false, err
	}
	var digests []string
	for id := range scope {
		if span := r.span(id); span != nil && span.CallDigest != "" {
			digests = append(digests, span.CallDigest)
		}
	}
	if err := r.loadCalls(ctx, digests...); err != nil {
		return false, err
	}
	found := false
	for _, digest := range digests {
		if !idtui.IsTraceConversationFrame(r.db, digest) {
			continue
		}
		found = true
		if err := r.loadSpine(ctx, digest); err != nil {
			return false, err
		}
	}
	return found, nil
}

// readTraceTranscript renders a page of one conversation.
func readTraceTranscript(ctx context.Context, opts transcriptOpts) (string, error) {
	client, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return readTraceTranscriptIn(ctx, client, opts)
}

func readTraceTranscriptIn(ctx context.Context, client *clientdb.DB, opts transcriptOpts) (string, error) {
	grep, err := opts.validate()
	if err != nil {
		return "", err
	}
	if opts.Agent == "" && strings.TrimSpace(opts.Span) == "" {
		return "", fmt.Errorf("the %s view needs agent (a handle or unique name from ReadTrace(view: %q)) or a span inside or above the agent", traceViewTranscript, traceViewAgents)
	}
	spanID := ""
	if opts.Agent == "" {
		if spanID, err = optionalSpanArg(client, opts.Span); err != nil {
			return "", err
		}
	}
	var extra []string
	if spanID != "" {
		extra = append(extra, spanID)
	}
	roster, err := loadAgentRoster(ctx, client, extra...)
	if err != nil {
		return "", err
	}
	target, err := roster.resolveConversation(ctx, opts.Agent, spanID)
	if err != nil {
		return "", err
	}
	agent, snap, err := roster.transcript(ctx, target)
	if err != nil {
		return "", err
	}
	messages := idtui.SelectTraceTranscript(snap.Snapshot.Messages, opts.Role, opts.Tool, grep)
	page := idtui.RenderTraceTranscript(idtui.TraceTranscriptPage{
		Agent: agent, Transcript: snap, Messages: messages,
		Offset: min(opts.Offset, len(messages)), Limit: opts.Limit, Clip: opts.Clip,
		Filters:  transcriptFilters(opts),
		NextPage: func(offset int) string { return transcriptCall(target, opts, offset) },
	})
	var head []string
	if total := len(snap.Snapshot.Messages); len(messages) == 0 && total > 0 && opts.Role == "" && opts.Tool == "" && opts.Grep == "" {
		page += fmt.Sprintf("The transcript holds only %s; pass role: \"system\" to read them.\n", countNoun(total, "system prompt", "system prompts"))
	}
	if target.via != "" {
		head = append(head, "resolved to "+target.via)
	}
	if target.span == "" {
		if span := lastSpan(agent); span != "" {
			head = append(head, fmt.Sprintf("agent span=%s: ReadTrace(span: %q) for its trace", span, span))
		}
	}
	if len(head) == 0 {
		return page, nil
	}
	return strings.Join(head, "\n") + "\n" + page, nil
}

func transcriptFilters(opts transcriptOpts) string {
	var filters []string
	for _, f := range [][2]string{{"role", opts.Role}, {"tool", opts.Tool}, {"grep", opts.Grep}} {
		if f[1] != "" {
			filters = append(filters, fmt.Sprintf("%s=%q", f[0], f[1]))
		}
	}
	if len(filters) == 0 {
		return "all non-system messages"
	}
	return strings.Join(filters, ", ")
}

// transcriptCall spells the ReadTrace call that reads c from offset, keeping
// the caller's filters and page shape.
func transcriptCall(c conversation, opts transcriptOpts, offset int) string {
	args := []string{fmt.Sprintf("view: %q", traceViewTranscript)}
	if c.span != "" {
		args = append(args, fmt.Sprintf("span: %q", c.span))
	} else {
		args = append(args, fmt.Sprintf("agent: %q", c.agent.ID))
	}
	for _, f := range [][2]string{{"role", opts.Role}, {"tool", opts.Tool}, {"grep", opts.Grep}} {
		if f[1] != "" {
			args = append(args, fmt.Sprintf("%s: %q", f[0], f[1]))
		}
	}
	args = append(args, fmt.Sprintf("offset: %d", offset))
	if opts.Limit != transcriptDefaultLimit {
		args = append(args, fmt.Sprintf("limit: %d", opts.Limit))
	}
	if opts.Clip != transcriptDefaultClip {
		args = append(args, fmt.Sprintf("clip: %d", opts.Clip))
	}
	return "ReadTrace(" + strings.Join(args, ", ") + ")"
}

// conversationSummary is the bounded stand-in for a CONVERSATION section too
// long to render inline: the conversation's size, its opening prompt and
// final reply, and the transcript call that reads it. Best-effort: when the
// transcript can't be decoded, only the size and the pointer remain.
func conversationSummary(ctx context.Context, client *clientdb.DB, root string, surfaced int) string {
	pointer := fmt.Sprintf("ReadTrace(span: %q, view: %q) reads it in pages; filter with role, tool or grep.", root, traceViewTranscript)
	var b strings.Builder
	b.WriteString("== CONVERSATION ==\n")
	fmt.Fprintf(&b, "%d messages surfaced beneath this span, nested agents' included; omitted here.\n", surfaced)
	summary, err := summarizeConversation(ctx, client, root)
	if err == nil {
		b.WriteString(summary)
	}
	b.WriteString(pointer)
	return b.String()
}

const (
	conversationSummaryPromptBytes = 1000
	conversationSummaryReplyBytes  = 2000
)

func summarizeConversation(ctx context.Context, client *clientdb.DB, root string) (string, error) {
	roster, err := loadAgentRoster(ctx, client, root)
	if err != nil {
		return "", err
	}
	target, err := roster.resolveConversation(ctx, "", root)
	if err != nil {
		return "", err
	}
	agent, snap, err := roster.transcript(ctx, target)
	if err != nil {
		return "", err
	}
	messages := snap.Snapshot.Messages
	var prompt, reply *idtui.TraceTranscriptMessage
	counts := map[string]int{}
	for i := range messages {
		message := &messages[i]
		kind := message.Role
		if len(message.Content) > 0 && message.Content[0].Kind == "TOOL_RESULT" {
			kind = "TOOL"
		}
		counts[kind]++
		if kind == "USER" && prompt == nil {
			prompt = message
		}
		if kind == "ASSISTANT" && messageText(*message) != "" {
			reply = message
		}
	}
	var b strings.Builder
	who := "conversation beneath span " + target.span
	if target.span == "" {
		who = fmt.Sprintf("agent %s (%s)", agent.Name, agent.ID)
		if agent.State != "" {
			who += " " + agent.State
		}
	}
	fmt.Fprintf(&b, "Transcript of %s: %d messages (%s, %s, %s).\n",
		who, len(messages), countNoun(counts["USER"], "prompt", "prompts"),
		countNoun(counts["ASSISTANT"], "reply", "replies"), countNoun(counts["TOOL"], "tool result", "tool results"))
	if len(snap.Gaps) > 0 {
		fmt.Fprintf(&b, "PARTIAL: %d gap(s) where the checkpoint could not be fully decoded.\n", len(snap.Gaps))
	}
	if prompt != nil {
		fmt.Fprintf(&b, "Opening prompt (#%d):\n%s\n", prompt.Index, indentText(clipText(messageText(*prompt), conversationSummaryPromptBytes)))
	}
	if reply != nil {
		fmt.Fprintf(&b, "Final reply (#%d):\n%s\n", reply.Index, indentText(clipText(messageText(*reply), conversationSummaryReplyBytes)))
	}
	if n := len(messages); n > 0 && (reply == nil || reply.Index != messages[n-1].Index) {
		last := messages[n-1]
		kind := strings.ToLower(last.Role)
		if len(last.Content) > 0 && last.Content[0].Kind == "TOOL_RESULT" {
			kind = "tool result"
		}
		fmt.Fprintf(&b, "The conversation ends with #%d, a %s message: it may still have been running.\n", last.Index, kind)
	}
	return b.String(), nil
}

func countNoun(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func messageText(message idtui.TraceTranscriptMessage) string {
	var parts []string
	for _, block := range message.Content {
		if block.Kind == "TEXT" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func clipText(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimRight(text[:cut], " \n") + fmt.Sprintf(" [... %d more bytes]", len(text)-cut)
}

func indentText(text string) string {
	return "  " + strings.ReplaceAll(text, "\n", "\n  ")
}
