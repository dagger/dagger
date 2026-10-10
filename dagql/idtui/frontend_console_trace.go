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

// decodeCheckpointWithFallback decodes a recorded checkpoint strictly, and
// only when that fails, best-effort with explicit gaps.
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
	partial.Warning = "strict decode failed (" + err.Error() + "); decoded best-effort instead"
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
	snap, err := decodeCheckpoint(db, agent, true)
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

// Read only the committed LLM receiver spine, not
// CallIDForDigest's full dependency closure: a missing host/tool/module dependency
// must not prevent reading messages that are present. Unknown or missing spine
// frames are errors, never silently omitted messages. Nothing is evaluated.
func decodeConsoleCheckpoint(db *dagui.DB, agent consoleAgent) (consoleAgentSnapshot, error) {
	return decodeCheckpoint(db, agent, false)
}

// consoleGapRole marks a best-effort transcript's explicit hole: a frame that
// never reached this client, or a step that isn't message data.
const consoleGapRole = "GAP"

// consoleMessageSelectors carry message data; consoleConfigSelectors only
// configure the LLM. Anything else on a spine is unknown to the decoder.
var (
	consoleMessageSelectors = []string{"withPrompt", "withSystemPrompt", "withResponse", "withToolResult"}
	consoleConfigSelectors  = []string{
		"withoutDefaultSystemPrompt", "withMCPServer", "withSkills", "withTools", "withWorkspace", "withModel", "withReasoningEffort",
		"__withCompositionOwner", // Legacy recorded checkpoints; no longer emitted or evaluated.
	}
)

type checkpointSpineItem struct {
	frame *callpbv1.Call
	gap   string
}

// decodeCheckpoint walks the spine from the snapshot digest back to its llm
// root. Strictly, any missing, cyclic, foreign or unknown frame is an error.
// With partial, each becomes a GAP message instead, and the walk continues
// where it can: through a step that isn't message data (e.g. a tool that
// returns the conversation in a new workspace, as checkout does), it follows
// the step's LLM-valued argument when its receiver isn't the conversation.
func decodeCheckpoint(db *dagui.DB, agent consoleAgent, partial bool) (consoleAgentSnapshot, error) {
	var result consoleAgentSnapshot
	if agent.SnapshotDigest == "" {
		return result, fmt.Errorf("agent %q has no recorded checkpoint", agent.ID)
	}
	var spine []checkpointSpineItem
	gap := func(format string, args ...any) {
		spine = append(spine, checkpointSpineItem{gap: fmt.Sprintf(format, args...)})
	}
	seen := map[string]bool{}
	for digest := agent.SnapshotDigest; digest != ""; {
		if seen[digest] {
			if !partial {
				return result, fmt.Errorf("cycle in checkpoint spine at %s", digest)
			}
			gap("cycle in checkpoint spine at %s; earlier messages are unavailable", digest)
			break
		}
		seen[digest] = true
		frame := db.Calls[digest]
		if frame == nil {
			if !partial {
				return result, fmt.Errorf("checkpoint spine frame %s is missing from the trace", digest)
			}
			gap("frame %s never reached this client; earlier messages are unavailable", digest)
			break
		}
		if frame.GetType().GetNamedType() != "LLM" {
			if !partial {
				return result, fmt.Errorf("checkpoint frame %s is not a core LLM selector", digest)
			}
			gap("frame %s (%s) is a %s, not the conversation; earlier messages are unavailable",
				digest, frame.Field, frame.GetType().GetNamedType())
			break
		}
		if frame.Module != nil && !partial {
			return result, fmt.Errorf("checkpoint frame %s is not a core LLM selector", digest)
		}
		spine = append(spine, checkpointSpineItem{frame: frame})
		digest = nextSpineDigest(db, frame, partial)
	}
	slices.Reverse(spine)
	if !partial && (len(spine) == 0 || spine[0].frame.Field != "llm") {
		return result, fmt.Errorf("checkpoint is not rooted at llm")
	}
	messages := make([]consoleTranscriptMessage, 0)
	addGap := func(text string) {
		result.Gaps = append(result.Gaps, text)
		messages = append(messages, consoleTranscriptMessage{
			Index: len(messages), Role: consoleGapRole,
			Content: []consoleTranscriptBlock{{Kind: consoleGapRole, Text: text}},
		})
	}
	for index, item := range spine {
		if item.frame == nil {
			addGap(item.gap)
			continue
		}
		frame := item.frame
		if !isConsoleSpineSelector(frame) {
			if !partial {
				return result, fmt.Errorf("unsupported checkpoint selector %q: refusing an incomplete transcript", frame.Field)
			}
			addGap(fmt.Sprintf("skipped %s (%s): not message data", consoleFrameName(frame), frame.Digest))
			continue
		}
		switch {
		case frame.Field == "llm":
			if index != 0 {
				if !partial {
					return result, fmt.Errorf("unexpected llm selector within checkpoint")
				}
				addGap(fmt.Sprintf("unexpected llm selector %s within the checkpoint", frame.Digest))
			}
			continue
		case slices.Contains(consoleConfigSelectors, frame.Field):
			continue // Binding/configuration, not message data. Do not follow IDs.
		}
		message, err := decodeCheckpointMessage(frame)
		if err != nil {
			if !partial {
				return result, err
			}
			addGap(fmt.Sprintf("could not decode %s (%s): %v", frame.Field, frame.Digest, err))
			continue
		}
		message.Index = len(messages)
		messages = append(messages, message)
	}
	result.Source, result.Digest, result.State = "recorded-checkpoint", agent.SnapshotDigest, agent.State
	result.Snapshot.Messages = messages
	return result, nil
}

// isConsoleSpineSelector reports whether the decoder knows frame as a core
// LLM message or configuration selector.
func isConsoleSpineSelector(frame *callpbv1.Call) bool {
	if frame.Module != nil {
		return false
	}
	return frame.Field == "llm" ||
		slices.Contains(consoleMessageSelectors, frame.Field) ||
		slices.Contains(consoleConfigSelectors, frame.Field)
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

func consoleFrameName(frame *callpbv1.Call) string {
	if name := frame.GetModule().GetName(); name != "" {
		return name + "." + frame.Field
	}
	return frame.Field
}

func decodeCheckpointMessage(frame *callpbv1.Call) (consoleTranscriptMessage, error) {
	var message consoleTranscriptMessage
	args := map[string]any{}
	for _, arg := range frame.Args {
		value, err := consoleLiteralData(arg.Value)
		if err != nil {
			return message, fmt.Errorf("%s argument %s: %w", frame.Field, arg.Name, err)
		}
		args[arg.Name] = value
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return message, err
	}
	var data struct {
		Prompt  *string
		Origin  *consoleTranscriptOrigin
		Content json.RawMessage
		CallID  string `json:"callId"`
		Errored bool
	}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return message, fmt.Errorf("decode %s: %w", frame.Field, err)
	}
	switch frame.Field {
	case "withPrompt", "withSystemPrompt":
		if data.Prompt == nil {
			return message, fmt.Errorf("%s is missing its prompt", frame.Field)
		}
		message.Role = "USER"
		if frame.Field == "withSystemPrompt" {
			message.Role = "SYSTEM"
		}
		message.Origin = data.Origin
		message.Content = []consoleTranscriptBlock{{Kind: "TEXT", Text: *data.Prompt}}
	case "withResponse":
		message.Role = "ASSISTANT"
		if err := json.Unmarshal(data.Content, &message.Content); err != nil || message.Content == nil {
			return message, fmt.Errorf("invalid withResponse content")
		}
		for _, block := range message.Content {
			switch block.Kind {
			case "TEXT", "THINKING", "TOOL_CALL", "TOOL_RESULT":
			default:
				return message, fmt.Errorf("unsupported response content kind %q", block.Kind)
			}
		}
	case "withToolResult":
		var text *string
		if err := json.Unmarshal(data.Content, &text); err != nil || text == nil {
			return message, fmt.Errorf("invalid withToolResult content")
		}
		message.Role = "USER"
		message.Content = []consoleTranscriptBlock{{Kind: "TOOL_RESULT", CallID: data.CallID, Text: *text, Errored: data.Errored}}
	}
	return message, nil
}

func consoleLiteralData(literal *callpbv1.Literal) (any, error) {
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
			decoded, err := consoleLiteralData(item)
			if err != nil {
				return nil, err
			}
			list = append(list, decoded)
		}
		return list, nil
	case *callpbv1.Literal_Object:
		object := map[string]any{}
		for _, item := range value.Object.GetValues() {
			decoded, err := consoleLiteralData(item.Value)
			if err != nil {
				return nil, err
			}
			object[item.Name] = decoded
		}
		return object, nil
	default:
		return nil, fmt.Errorf("expected recorded literal data, got %T", literal.GetValue())
	}
}
