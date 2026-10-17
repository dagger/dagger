package idtui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

// Allocated only by serveConsole. Ordinary trace rendering stays incremental;
// extraction downloads the full recorded payloads on its first explicit request.
// The private DB avoids duplicating logs in the rendered frontend. It never
// loads IDs in an engine, so unavailable modules, mounts and tools are irrelevant.
type consoleTraceInspector struct {
	frontend *frontendPretty
	mu       sync.Mutex
	db       *dagui.DB
	traceID  string
	// fetch downloads a trace's full payloads into db; nil fetches from Cloud.
	fetch func(ctx context.Context, traceID string, db *dagui.DB) error
}

func fetchCloudTrace(ctx context.Context, traceID string, db *dagui.DB) error {
	credentials, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return err
	}
	client, err := cloud.NewOTLPClient(ctx, credentials)
	if err != nil {
		return err
	}
	if err := client.FetchTrace(ctx, traceID, enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans: db, Logs: db.LogExporter(), Metrics: db.MetricExporter(),
	})); err != nil {
		return fmt.Errorf("fetch extraction payloads for trace %s: %w", traceID, err)
	}
	return nil
}

func (i *consoleTraceInspector) recordedDB(ctx context.Context) (*dagui.DB, error) {
	i.frontend.consoleMu.Lock()
	traceID := i.frontend.traceID
	i.frontend.consoleMu.Unlock()
	if traceID == "" {
		return nil, nil // An engine session, rather than `dagger trace`.
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db != nil && i.traceID == traceID {
		return i.db, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	fetch := i.fetch
	if fetch == nil {
		fetch = fetchCloudTrace
	}
	db := dagui.NewDB()
	if err := fetch(ctx, traceID, db); err != nil {
		// Never advertise a partially downloaded roster as complete. A later
		// request may retry; don't cache failed downloads.
		return nil, err
	}
	db.Agents() // Initialize the projection before publishing the immutable DB.
	i.db, i.traceID = db, traceID
	return db, nil
}

// consoleAgentSource is where agent rosters and transcripts are read from for
// one request: the recorded trace's full payloads, the engine's runtime
// snapshots, or (as a fallback) the telemetry the frontend has already
// ingested while rendering.
type consoleAgentSource struct {
	agents  []consoleAgent
	read    consoleSnapshotReader
	source  string // recorded-trace, loaded-telemetry, or "" for live rosters
	warning string
}

func (i *consoleTraceInspector) agentSource(ctx context.Context) consoleAgentSource {
	fe := i.frontend
	db, err := i.recordedDB(ctx)
	if err == nil && db != nil {
		agents := consoleAgentsFromDB(db)
		return consoleAgentSource{
			agents: agents,
			source: "recorded-trace",
			read: func(_ context.Context, handle, _ string) (consoleAgentSnapshot, error) {
				for _, agent := range agents {
					if agent.ID == handle {
						return decodeCheckpointWithFallback(db, agent)
					}
				}
				return consoleAgentSnapshot{}, fmt.Errorf("agent %q not in recorded trace", handle)
			},
		}
	}
	fe.consoleMu.Lock()
	if fe.tui != nil {
		fe.tui.Step()
	}
	agents := fe.consoleAgents()
	dag := fe.dag
	fe.consoleMu.Unlock()
	if err != nil {
		// The full download failed (e.g. one oversized payload the server
		// refuses to stream), but rendering already ingested the trace
		// incrementally. Serve what that has, marked as such.
		return consoleAgentSource{
			agents:  agents,
			source:  "loaded-telemetry",
			read:    fe.readLoadedSnapshot,
			warning: err.Error(),
		}
	}
	var runtime consoleSnapshotReader
	if dag != nil {
		runtime = func(ctx context.Context, handle, name string) (consoleAgentSnapshot, error) {
			return readConsoleAgentSnapshot(ctx, dag, handle, name)
		}
	}
	return consoleAgentSource{agents: agents, read: withLoadedFallback(runtime, fe.readLoadedSnapshot)}
}

func (i *consoleTraceInspector) agents(w http.ResponseWriter, r *http.Request) {
	src := i.agentSource(r.Context())
	if r.URL.Query().Get("format") != "json" && src.source != "" {
		header := "Recorded trace: states are historical observations, not live runtimes. No agents have been restored or started."
		if src.source == "loaded-telemetry" {
			header = "Loaded telemetry: the full trace download failed; transcripts are decoded best-effort. Warning: " + src.warning
		}
		consoleText(w, renderConsoleAgents(src.agents, src.source == "recorded-trace", r.URL.Query().Get("all") == "true", header))
		return
	}
	switch src.source {
	case "recorded-trace":
		consoleJSON(w, struct {
			Agents          []consoleAgent `json:"agents"`
			LoadedSpansOnly bool           `json:"loadedSpansOnly"`
			Source          string         `json:"source"`
			Note            string         `json:"note"`
		}{src.agents, false, src.source, "States are historical observations, not live runtimes. No agents have been restored or started."})
	case "loaded-telemetry":
		consoleJSON(w, struct {
			Agents          []consoleAgent `json:"agents"`
			LoadedSpansOnly bool           `json:"loadedSpansOnly"`
			Source          string         `json:"source"`
			Warning         string         `json:"warning"`
			Note            string         `json:"note"`
		}{src.agents, true, src.source, src.warning, "The full trace download failed, so this roster comes from the telemetry already loaded for rendering. Transcripts are decoded best-effort from it."})
	default:
		i.frontend.consoleAgentsHandler(w, r)
	}
}

func (i *consoleTraceInspector) transcript(w http.ResponseWriter, r *http.Request) {
	src := i.agentSource(r.Context())
	serveConsoleTranscript(w, r, src.agents, src.withWarning())
}

// withWarning attaches the source's warning to every snapshot it reads.
func (src consoleAgentSource) withWarning() consoleSnapshotReader {
	if src.warning == "" || src.read == nil {
		return src.read
	}
	return func(ctx context.Context, handle, name string) (consoleAgentSnapshot, error) {
		snap, err := src.read(ctx, handle, name)
		if err != nil {
			return snap, fmt.Errorf("%w (after: %s)", err, src.warning)
		}
		if snap.Warning == "" {
			snap.Warning = src.warning
		}
		return snap, nil
	}
}

// readLoadedSnapshot decodes an agent's checkpoint, best-effort, from the call
// payloads this frontend has already ingested while rendering.
func (fe *frontendPretty) readLoadedSnapshot(_ context.Context, handle, _ string) (consoleAgentSnapshot, error) {
	fe.consoleMu.Lock()
	defer fe.consoleMu.Unlock()
	for _, agent := range fe.consoleAgents() {
		if agent.ID == handle {
			return decodeLoadedCheckpoint(fe.db, agent)
		}
	}
	return consoleAgentSnapshot{}, fmt.Errorf("agent %q not in loaded telemetry", handle)
}

// withLoadedFallback reads the engine's runtime snapshot, falling back to the
// loaded telemetry when there is no engine, the read fails, or it comes back
// empty -- as it does for an agent whose restore failed. The fallback is only
// taken when it recovers messages; otherwise the runtime outcome stands.
func withLoadedFallback(runtime, loaded consoleSnapshotReader) consoleSnapshotReader {
	return func(ctx context.Context, handle, name string) (consoleAgentSnapshot, error) {
		if runtime == nil {
			snap, err := loaded(ctx, handle, name)
			if err != nil {
				return snap, fmt.Errorf("no engine session to read a runtime snapshot from, and %w", err)
			}
			return snap, nil
		}
		snap, err := runtime(ctx, handle, name)
		if err == nil && len(snap.Snapshot.Messages) > 0 {
			return snap, nil
		}
		fallback, fallbackErr := loaded(ctx, handle, name)
		if fallbackErr != nil {
			return snap, err
		}
		if err != nil {
			fallback.Warning = "engine snapshot unavailable (" + err.Error() + "); decoded from loaded telemetry instead"
		} else {
			fallback.Warning = "engine snapshot has no messages (e.g. a failed restore); decoded from loaded telemetry instead"
		}
		return fallback, nil
	}
}

// decodeCheckpointWithFallback decodes a recorded checkpoint with explicit gaps,
// falling back to inferred telemetry when no checkpoint was recorded.
func decodeCheckpointWithFallback(db *dagui.DB, agent consoleAgent) (consoleAgentSnapshot, error) {
	snap, err := decodeConsoleCheckpoint(db, agent)
	if err == nil {
		return snap, nil
	}
	partial, partialErr := decodeLoadedCheckpoint(db, agent)
	if partialErr != nil {
		return snap, err
	}
	partial.Source = snap.Source
	if partial.Source == "" {
		partial.Source = "recorded-checkpoint"
	}
	partial.Warning = "recorded checkpoint unavailable (" + err.Error() + "); decoded best-effort instead"
	return partial, nil
}

// decodeLoadedCheckpoint is the best-effort decode: it never refuses a
// transcript, but reports what it could not read as GAP messages, and fails
// only when it recovers no message at all.
func decodeLoadedCheckpoint(db *dagui.DB, agent consoleAgent) (consoleAgentSnapshot, error) {
	inferred := false
	if agent.SnapshotDigest == "" {
		agent.SnapshotDigest = inferCheckpointDigest(db, agent.ID)
		inferred = agent.SnapshotDigest != ""
	}
	snap, err := decodeConsoleCheckpoint(db, agent)
	if err != nil {
		return snap, err
	}
	for _, message := range snap.Snapshot.Messages {
		if message.Role != consoleGapRole {
			snap.Source = "loaded-telemetry"
			if inferred {
				// Messages after the newest traced call (e.g. a final reply)
				// may be missing, so this is a trailing gap.
				text := "no control record named the agent's checkpoint; inferred " + agent.SnapshotDigest +
					" from its longest traced conversation, so later messages may be missing"
				snap.Gaps = append(snap.Gaps, text)
				snap.Snapshot.Messages = append(snap.Snapshot.Messages, consoleTranscriptMessage{
					Index: len(snap.Snapshot.Messages), Role: consoleGapRole,
					Content: []consoleTranscriptBlock{{Kind: consoleGapRole, Text: text}},
				})
			}
			return snap, nil
		}
	}
	return snap, fmt.Errorf("no messages recoverable from loaded telemetry: %s", strings.Join(snap.Gaps, "; "))
}

// consoleCheckpointConfig names core LLM selectors that configure a
// conversation without adding messages to it. Their ID arguments (tools,
// workspaces, expertise, ...) are never followed.
var consoleCheckpointConfig = map[string]bool{
	"withoutDefaultSystemPrompt": true,
	"withMCPServer":              true,
	"withSkills":                 true,
	"withTools":                  true,
	"withWorkspace":              true,
	"withModel":                  true,
	"withSmallModel":             true,
	"withReasoningEffort":        true,
	"attempt":                    true,
	"__withExpertise":            true,
	"__withOwnerAgent":           true,
	// Legacy recorded checkpoints; no longer emitted or evaluated.
	"__withCompositionOwner": true,
}

// consoleSpineStep is one frame of a checkpoint's LLM receiver spine, or a
// gap where the spine could not be followed.
type consoleSpineStep struct {
	frame *callpbv1.Call
	gap   string
}

// decodeConsoleCheckpoint reads an agent's recorded conversation from its
// checkpoint: only the committed LLM receiver spine, not CallIDForDigest's
// full dependency closure, so a missing host/tool/module dependency does not
// prevent reading messages that are present. Nothing is evaluated.
//
// Decoding is best-effort, never silent: where message data is missing or a
// selector is not understood, the transcript carries a GAP message at that
// position (and the snapshot lists it in Gaps) instead of failing outright,
// so one unfamiliar selector does not make a whole conversation unreadable.
func decodeConsoleCheckpoint(db *dagui.DB, agent consoleAgent) (consoleAgentSnapshot, error) {
	var result consoleAgentSnapshot
	if agent.SnapshotDigest == "" {
		return result, fmt.Errorf("agent %q has no recorded checkpoint", agent.ID)
	}
	dec := &consoleCheckpointDecoder{}
	for _, step := range consoleCheckpointSpine(db, agent.SnapshotDigest) {
		if step.frame == nil {
			dec.gap(step.gap)
			continue
		}
		dec.apply(step.frame)
	}
	result.Source, result.Digest, result.State = "recorded-checkpoint", agent.SnapshotDigest, agent.State
	result.Gaps = dec.gaps
	result.Snapshot.Messages = make([]consoleTranscriptMessage, 0, len(dec.messages))
	for i, message := range dec.messages {
		message.message.Index = i
		result.Snapshot.Messages = append(result.Snapshot.Messages, message.message)
	}
	return result, nil
}

// consoleCheckpointSpine walks a checkpoint from its newest frame back to
// its root and returns the steps oldest first. A conversation returned by a
// module function (e.g. a tool that rebinds itself) is followed through that
// function's LLM argument.
func consoleCheckpointSpine(db *dagui.DB, digest string) []consoleSpineStep {
	var steps []consoleSpineStep
	seen := map[string]bool{}
	gap := func(format string, args ...any) {
		steps = append(steps, consoleSpineStep{gap: fmt.Sprintf(format, args...)})
	}
walk:
	for digest != "" {
		if seen[digest] {
			gap("cycle in the checkpoint spine at %s: earlier history is unavailable", digest)
			break
		}
		seen[digest] = true
		frame := db.Calls[digest]
		switch {
		case frame == nil:
			gap("checkpoint frame %s is missing from the trace: earlier history is unavailable", digest)
			break walk
		case frame.GetType().GetNamedType() != "LLM":
			gap("checkpoint frame %s (%s) does not return an LLM: earlier history is unavailable", digest, frame.Field)
			break walk
		case frame.Module != nil:
			name := consoleCallName(db, frame)
			next := consoleLLMArgument(db, frame)
			if next == "" {
				gap("the conversation was returned by module function %s, which takes no LLM argument: earlier history is unavailable", name)
				break walk
			}
			gap("the conversation was returned by module function %s: changes it made are not decoded", name)
			digest = next
		default:
			steps = append(steps, consoleSpineStep{frame: frame})
			digest = frame.ReceiverDigest
		}
	}
	slices.Reverse(steps)
	if len(steps) > 0 && steps[0].frame != nil && steps[0].frame.Field != "llm" {
		steps = append([]consoleSpineStep{{
			gap: fmt.Sprintf("the checkpoint is not rooted at llm (it starts at %s): earlier history is unavailable", steps[0].frame.Field),
		}}, steps...)
	}
	return steps
}

func consoleCallName(db *dagui.DB, frame *callpbv1.Call) string {
	if recv := db.Calls[frame.ReceiverDigest]; recv != nil && recv.GetType().GetNamedType() != "" {
		return recv.GetType().GetNamedType() + "." + frame.Field
	}
	if frame.Module != nil && frame.Module.Name != "" {
		return frame.Module.Name + "." + frame.Field
	}
	return frame.Field
}

// consoleLLMArgument returns the digest of a call's only LLM-valued argument.
func consoleLLMArgument(db *dagui.DB, frame *callpbv1.Call) string {
	var found []string
	for _, arg := range frame.Args {
		digest := arg.GetValue().GetCallDigest()
		if digest == "" {
			continue
		}
		if call := db.Calls[digest]; call != nil && call.GetType().GetNamedType() == "LLM" {
			found = append(found, digest)
		}
	}
	if len(found) != 1 {
		return ""
	}
	return found[0]
}

type consoleDecodedMessage struct {
	message consoleTranscriptMessage
	// owner is a system prompt's composition owner, so __withoutComposition
	// can remove exactly what the engine removed.
	owner string
}

type consoleCheckpointDecoder struct {
	messages []consoleDecodedMessage
	gaps     []string
}

func (d *consoleCheckpointDecoder) add(role string, origin *consoleTranscriptOrigin, owner string, blocks ...consoleTranscriptBlock) {
	d.messages = append(d.messages, consoleDecodedMessage{
		message: consoleTranscriptMessage{Role: role, Origin: origin, Content: blocks},
		owner:   owner,
	})
}

func (d *consoleCheckpointDecoder) gap(text string) {
	d.gaps = append(d.gaps, text)
	d.add("GAP", nil, "", consoleTranscriptBlock{Kind: "TEXT", Text: text})
}

func (d *consoleCheckpointDecoder) drop(remove func(consoleDecodedMessage) bool) {
	d.messages = slices.DeleteFunc(d.messages, remove)
}

// restamp sets the owner of the system prompt at index in the conversation,
// counting decoded messages but not gaps. It reports whether there was one.
func (d *consoleCheckpointDecoder) restamp(index int, owner string) bool {
	for i := range d.messages {
		if d.messages[i].message.Role == "GAP" {
			continue
		}
		if index == 0 {
			if d.messages[i].message.Role != "SYSTEM" {
				return false
			}
			d.messages[i].owner = owner
			return true
		}
		index--
	}
	return false
}

func (d *consoleCheckpointDecoder) apply(frame *callpbv1.Call) {
	if frame.Field == "llm" || consoleCheckpointConfig[frame.Field] {
		return
	}
	fail := func(format string, args ...any) {
		d.gap(fmt.Sprintf("%s: ", frame.Field) + fmt.Sprintf(format, args...))
	}
	switch frame.Field {
	case "compose":
		// Entries run inside compose; what they contributed is not on the
		// spine. Only system prompts and tools are expected there.
		d.add("SYSTEM", nil, "", consoleTranscriptBlock{Kind: "TEXT",
			Text: "[expertise composed here; the system prompts and tools it contributed are not recorded on the checkpoint spine]"})
		return
	case "__withContributionOwner":
		// Re-stamps system prompts by their position in the conversation,
		// so a later __withoutComposition removes them under the new owner.
		args, _ := consoleFrameArgs(frame, true)
		owner, _ := args["owner"].(string)
		indices, _ := args["messages"].([]any)
		for _, raw := range indices {
			index, ok := raw.(int64)
			if !ok || !d.restamp(int(index), owner) {
				fail("message %v is not in the decoded transcript; its owner was not updated", raw)
			}
		}
		return
	case "__withoutComposition":
		args, _ := consoleFrameArgs(frame, true)
		owner, _ := args["owner"].(string)
		if owner != "" {
			d.drop(func(m consoleDecodedMessage) bool { return m.message.Role == "SYSTEM" && m.owner == owner })
		}
		return
	case "withoutMessageHistory":
		d.drop(func(m consoleDecodedMessage) bool { return m.message.Role != "SYSTEM" })
		return
	case "withoutSystemPrompts":
		d.drop(func(m consoleDecodedMessage) bool { return m.message.Role == "SYSTEM" })
		return
	case "withPromptFile", "withContentFile":
		fail("a user message was read from a file whose contents are not recorded in the checkpoint")
		return
	case "withPrompt", "withSystemPrompt", "withContent", "withResponse", "withToolResult":
	default:
		fail("unrecognized checkpoint selector skipped; it may have changed the conversation")
		return
	}
	args, err := consoleFrameArgs(frame, true)
	if err != nil {
		fail("%v", err)
		return
	}
	origin := consoleOrigin(args["origin"])
	switch frame.Field {
	case "withPrompt", "withSystemPrompt":
		prompt, ok := args["prompt"].(string)
		if !ok {
			fail("%s is missing its prompt", frame.Field)
			return
		}
		if frame.Field == "withSystemPrompt" {
			owner, _ := args["owner"].(string)
			d.add("SYSTEM", nil, owner, consoleTranscriptBlock{Kind: "TEXT", Text: prompt})
			return
		}
		d.add("USER", origin, "", consoleTranscriptBlock{Kind: "TEXT", Text: prompt})
	case "withContent":
		blocks := consoleContentBlocks(args["content"])
		if len(blocks) == 0 {
			fail("invalid withContent content")
			return
		}
		d.add("USER", origin, "", blocks...)
	case "withResponse":
		blocks := consoleContentBlocks(args["content"])
		if len(blocks) == 0 {
			fail("invalid withResponse content")
			return
		}
		d.add("ASSISTANT", nil, "", blocks...)
	case "withToolResult":
		text, hasText := args["content"].(string)
		extra := consoleContentBlocks(args["blocks"])
		if !hasText && len(extra) == 0 {
			fail("invalid withToolResult content")
			return
		}
		callID, _ := args["callId"].(string)
		errored, _ := args["errored"].(bool)
		blocks := []consoleTranscriptBlock{{Kind: "TOOL_RESULT", CallID: callID, Text: text, Errored: errored}}
		d.add("USER", nil, "", append(blocks, extra...)...)
	}
}

func consoleFrameArgs(frame *callpbv1.Call, lenient bool) (map[string]any, error) {
	args := map[string]any{}
	for _, arg := range frame.Args {
		value, err := consoleLiteralValue(arg.Value, lenient)
		if err != nil {
			return nil, fmt.Errorf("argument %s: %w", arg.Name, err)
		}
		args[arg.Name] = value
	}
	return args, nil
}

func consoleOrigin(value any) *consoleTranscriptOrigin {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var origin consoleTranscriptOrigin
	if json.Unmarshal(encoded, &origin) != nil || origin.Kind == "" {
		return nil
	}
	return &origin
}

// consoleContentBlocks decodes recorded LLMContent blocks. Media and other
// non-text kinds keep their kind with a short placeholder: their bytes are
// not useful in a transcript.
func consoleContentBlocks(value any) []consoleTranscriptBlock {
	list, _ := value.([]any)
	blocks := make([]consoleTranscriptBlock, 0, len(list))
	for _, item := range list {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		str := func(name string) string {
			s, _ := fields[name].(string)
			return s
		}
		block := consoleTranscriptBlock{
			Kind:      str("kind"),
			Text:      str("text"),
			CallID:    str("callId"),
			ToolName:  str("toolName"),
			Arguments: str("arguments"),
		}
		block.Errored, _ = fields["errored"].(bool)
		switch block.Kind {
		case "TEXT", "THINKING", "TOOL_CALL", "TOOL_RESULT":
		case "":
			continue
		default:
			placeholder := "[" + block.Kind
			if mime := str("mimeType"); mime != "" {
				placeholder += " " + mime
			}
			placeholder += "]"
			if block.Text != "" {
				placeholder += " " + block.Text
			}
			block.Text = placeholder
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func consoleLiteralData(literal *callpbv1.Literal) (any, error) {
	return consoleLiteralValue(literal, false)
}

// consoleLiteralValue converts recorded literal data. Leniently, IDs and
// bytes become placeholders instead of errors, so a message that embeds a
// file or media still decodes.
func consoleLiteralValue(literal *callpbv1.Literal, lenient bool) (any, error) {
	switch value := literal.GetValue().(type) {
	case *callpbv1.Literal_String_:
		return value.String_, nil
	case *callpbv1.Literal_DigestedString:
		return value.DigestedString.GetValue(), nil
	case *callpbv1.Literal_Enum:
		return value.Enum, nil
	case *callpbv1.Literal_Bool:
		return value.Bool, nil
	case *callpbv1.Literal_Int:
		return value.Int, nil
	case *callpbv1.Literal_Float:
		return value.Float, nil
	case *callpbv1.Literal_Null:
		return nil, nil
	case *callpbv1.Literal_List:
		list := make([]any, 0, len(value.List.GetValues()))
		for _, item := range value.List.GetValues() {
			decoded, err := consoleLiteralValue(item, lenient)
			if err != nil {
				return nil, err
			}
			list = append(list, decoded)
		}
		return list, nil
	case *callpbv1.Literal_Object:
		object := map[string]any{}
		for _, item := range value.Object.GetValues() {
			decoded, err := consoleLiteralValue(item.Value, lenient)
			if err != nil {
				return nil, err
			}
			object[item.Name] = decoded
		}
		return object, nil
	case *callpbv1.Literal_CallDigest:
		if lenient {
			return "<" + value.CallDigest + ">", nil
		}
	case *callpbv1.Literal_Bytes:
		if lenient {
			return fmt.Sprintf("<%d bytes>", len(value.Bytes)), nil
		}
	}
	return nil, fmt.Errorf("expected recorded literal data, got %T", literal.GetValue())
}

const consoleGapRole = "GAP"

var consoleMessageSelectors = []string{"withPrompt", "withSystemPrompt", "withContent", "withResponse", "withToolResult"}

// isConsoleSpineSelector reports whether the decoder knows frame as a core
// LLM message or configuration selector.
func isConsoleSpineSelector(frame *callpbv1.Call) bool {
	if frame.Module != nil {
		return false
	}
	return frame.Field == "llm" ||
		slices.Contains(consoleMessageSelectors, frame.Field) ||
		consoleCheckpointConfig[frame.Field] ||
		frame.Field == "compose" || frame.Field == "__withContributionOwner" ||
		frame.Field == "__withoutComposition" || frame.Field == "withoutSystemPrompts"
}

// nextSpineDigest is the frame before frame in its conversation: its
// receiver, or, best-effort, the LLM-valued argument of a step that isn't
// message data and whose receiver isn't the conversation.
func nextSpineDigest(db *dagui.DB, frame *callpbv1.Call, partial bool) string {
	next := frame.ReceiverDigest
	if partial && !isConsoleSpineSelector(frame) && !isLLMFrame(db, next) {
		if llm := llmArgument(db, frame); llm != "" {
			next = llm
		}
	}
	return next
}

// inferCheckpointDigest finds an agent's conversation tip when no control
// record named it (the records ride the log stream, which a trace may not
// have loaded): among the core LLM message calls traced under the agent's own
// loop spans -- not its nested agents' -- the one with the longest spine.
// Length, not recency, so a side chain such as a session-title prompt never
// wins over the conversation it branched from.
func inferCheckpointDigest(db *dagui.DB, agentID string) string {
	var node *dagui.AgentNode
	for _, candidate := range db.Agents() {
		if candidate.ID == agentID {
			node = candidate
			break
		}
	}
	if node == nil {
		return ""
	}
	var best string
	bestLen := 0
	seen := map[string]bool{}
	var walk func(*dagui.Span)
	walk = func(span *dagui.Span) {
		for _, child := range span.ChildSpans.Order {
			if child.Agent && child.AgentID != "" && child.AgentID != agentID {
				continue
			}
			if dig := child.CallDigest; dig != "" && !seen[dig] {
				seen[dig] = true
				if frame := db.Calls[dig]; frame != nil && frame.Module == nil &&
					frame.GetType().GetNamedType() == "LLM" && slices.Contains(consoleMessageSelectors, frame.Field) {
					if n := spineLength(db, dig); n > bestLen {
						best, bestLen = dig, n
					}
				}
			}
			walk(child)
		}
	}
	for _, span := range node.Spans {
		walk(span)
	}
	if best == "" {
		return ""
	}
	return extendCheckpointDigest(db, best)
}

// continuesConversation reports whether frame is a step of the conversation
// its predecessor (nextSpineDigest) belongs to: a known message or
// configuration selector, or a step that returns the conversation from its
// LLM argument (as checkout does). Anything else on an LLM receiver -- e.g.
// withoutMessageHistory, which a session title branches off with -- starts a
// different conversation.
func continuesConversation(db *dagui.DB, frame *callpbv1.Call) bool {
	if frame.GetType().GetNamedType() != "LLM" {
		return false
	}
	if isConsoleSpineSelector(frame) {
		return true
	}
	return !isLLMFrame(db, frame.ReceiverDigest) && llmArgument(db, frame) != ""
}

// extendCheckpointDigest follows a conversation forward from tip through the
// ingested calls: the furthest call that continues it. Spans load lazily, but
// call payloads may already be in, so the newest traced span is not
// necessarily the newest step.
func extendCheckpointDigest(db *dagui.DB, tip string) string {
	// distance[d] is how many steps d is past tip, or -1 if it never reaches it.
	distance := map[string]int{tip: 0}
	var resolve func(string) int
	resolve = func(start string) int {
		var path []string
		dig := start
		result := -1
		for {
			if d, ok := distance[dig]; ok {
				result = d
				break
			}
			frame := db.Calls[dig]
			if frame == nil || !continuesConversation(db, frame) {
				break
			}
			distance[dig] = -1 // guards against cycles while walking
			path = append(path, dig)
			dig = nextSpineDigest(db, frame, true)
		}
		for i := len(path) - 1; i >= 0; i-- {
			if result >= 0 {
				result++
			}
			distance[path[i]] = result
		}
		return result
	}
	best, bestDistance := tip, 0
	digests := make([]string, 0, len(db.Calls))
	for dig := range db.Calls {
		digests = append(digests, dig)
	}
	slices.Sort(digests) // deterministic ties
	for _, dig := range digests {
		if d := resolve(dig); d > bestDistance {
			best, bestDistance = dig, d
		}
	}
	return best
}

// spineLength counts the steps of digest's conversation that are traced
// back from it, stopping where another conversation branched off.
func spineLength(db *dagui.DB, digest string) int {
	seen := map[string]bool{}
	for digest != "" && !seen[digest] {
		frame := db.Calls[digest]
		if frame == nil || !continuesConversation(db, frame) {
			break
		}
		seen[digest] = true
		digest = nextSpineDigest(db, frame, true)
	}
	return len(seen)
}

func isLLMFrame(db *dagui.DB, digest string) bool {
	frame := db.Calls[digest]
	return frame != nil && frame.GetType().GetNamedType() == "LLM"
}

// llmArgument returns the digest of frame's LLM-valued argument, preferring
// one named llm, when it is present in db.
func llmArgument(db *dagui.DB, frame *callpbv1.Call) string {
	var found string
	for _, arg := range frame.Args {
		dig := arg.GetValue().GetCallDigest()
		if dig == "" || !isLLMFrame(db, dig) {
			continue
		}
		if arg.Name == "llm" {
			return dig
		}
		if found == "" {
			found = dig
		}
	}
	return found
}
