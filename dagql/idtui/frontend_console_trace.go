package idtui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
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
	credentials, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return nil, err
	}
	client, err := cloud.NewOTLPClient(ctx, credentials)
	if err != nil {
		return nil, err
	}
	db := dagui.NewDB()
	if err := client.FetchTrace(ctx, traceID, enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans: db, Logs: db.LogExporter(), Metrics: db.MetricExporter(),
	})); err != nil {
		// Never advertise a partially downloaded roster as complete. A later
		// request may retry; don't cache failed downloads.
		return nil, fmt.Errorf("fetch extraction payloads for trace %s: %w", traceID, err)
	}
	db.Agents() // Initialize the projection before publishing the immutable DB.
	i.db, i.traceID = db, traceID
	return db, nil
}

func (i *consoleTraceInspector) agents(w http.ResponseWriter, r *http.Request) {
	db, err := i.recordedDB(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if db == nil {
		i.frontend.consoleAgentsHandler(w, r)
		return
	}
	agents := consoleAgentsFromDB(db)
	if r.URL.Query().Get("format") == "json" {
		consoleJSON(w, struct {
			Agents          []consoleAgent `json:"agents"`
			LoadedSpansOnly bool           `json:"loadedSpansOnly"`
			Source          string         `json:"source"`
			Note            string         `json:"note"`
		}{agents, false, "recorded-trace", "States are historical observations, not live runtimes. No agents have been restored or started."})
		return
	}
	consoleText(w, renderConsoleAgents(agents, true, r.URL.Query().Get("all") == "true",
		"Recorded trace: states are historical observations, not live runtimes. No agents have been restored or started."))
}

func (i *consoleTraceInspector) transcript(w http.ResponseWriter, r *http.Request) {
	db, err := i.recordedDB(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if db == nil {
		i.frontend.consoleTranscriptHandler(w, r)
		return
	}
	agents := consoleAgentsFromDB(db)
	serveConsoleTranscript(w, r, agents, func(_ context.Context, handle, _ string) (consoleAgentSnapshot, error) {
		for _, agent := range agents {
			if agent.ID == handle {
				return decodeConsoleCheckpoint(db, agent)
			}
		}
		return consoleAgentSnapshot{}, fmt.Errorf("agent %q not in recorded trace", handle)
	})
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
