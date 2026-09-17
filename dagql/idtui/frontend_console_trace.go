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
	consoleJSON(w, struct {
		Agents          []consoleAgent `json:"agents"`
		LoadedSpansOnly bool           `json:"loadedSpansOnly"`
		Source          string         `json:"source"`
		Note            string         `json:"note"`
	}{consoleAgentsFromDB(db), false, "recorded-trace", "States are historical observations, not live runtimes. No agents have been restored or started."})
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

// PortableRecipe emits a flat data-only LLM spine. Read just that spine, not
// CallIDForDigest's full dependency closure: a missing host/tool/module dependency
// must not prevent reading messages that are present. Unknown or missing spine
// frames are errors, never silently omitted messages. Nothing is evaluated.
func decodeConsoleCheckpoint(db *dagui.DB, agent consoleAgent) (consoleAgentSnapshot, error) {
	var result consoleAgentSnapshot
	if agent.SnapshotDigest == "" {
		return result, fmt.Errorf("agent %q has no recorded checkpoint", agent.ID)
	}
	var spine []*callpbv1.Call
	seen := map[string]bool{}
	for digest := agent.SnapshotDigest; digest != ""; {
		if seen[digest] {
			return result, fmt.Errorf("cycle in checkpoint spine at %s", digest)
		}
		seen[digest] = true
		frame := db.Calls[digest]
		if frame == nil {
			return result, fmt.Errorf("checkpoint spine frame %s is missing from the trace", digest)
		}
		if frame.GetType().GetNamedType() != "LLM" || frame.Module != nil {
			return result, fmt.Errorf("checkpoint frame %s is not a core LLM selector", digest)
		}
		spine = append(spine, frame)
		digest = frame.ReceiverDigest
	}
	slices.Reverse(spine)
	if len(spine) == 0 || spine[0].Field != "llm" {
		return result, fmt.Errorf("checkpoint is not rooted at llm")
	}
	messages := make([]consoleTranscriptMessage, 0)
	for index, frame := range spine {
		switch frame.Field {
		case "llm":
			if index != 0 {
				return result, fmt.Errorf("unexpected llm selector within checkpoint")
			}
			continue
		case "withoutDefaultSystemPrompt", "withMCPServer", "withSkills", "withTools", "withWorkspace", "withModel", "withReasoningEffort":
			continue // Binding/configuration, not message data. Do not follow IDs.
		case "withPrompt", "withSystemPrompt", "withResponse", "withToolResult":
		default:
			return result, fmt.Errorf("unsupported checkpoint selector %q: refusing an incomplete transcript", frame.Field)
		}
		args := map[string]any{}
		for _, arg := range frame.Args {
			value, err := consoleLiteralData(arg.Value)
			if err != nil {
				return result, fmt.Errorf("%s argument %s: %w", frame.Field, arg.Name, err)
			}
			args[arg.Name] = value
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			return result, err
		}
		var data struct {
			Prompt  *string
			Origin  *consoleTranscriptOrigin
			Content json.RawMessage
			CallID  string `json:"callId"`
			Errored bool
		}
		if err := json.Unmarshal(encoded, &data); err != nil {
			return result, fmt.Errorf("decode %s: %w", frame.Field, err)
		}
		message := consoleTranscriptMessage{Index: len(messages)}
		switch frame.Field {
		case "withPrompt", "withSystemPrompt":
			if data.Prompt == nil {
				return result, fmt.Errorf("%s is missing its prompt", frame.Field)
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
				return result, fmt.Errorf("invalid withResponse content")
			}
			for _, block := range message.Content {
				switch block.Kind {
				case "TEXT", "THINKING", "TOOL_CALL", "TOOL_RESULT":
				default:
					return result, fmt.Errorf("unsupported response content kind %q", block.Kind)
				}
			}
		case "withToolResult":
			var text *string
			if err := json.Unmarshal(data.Content, &text); err != nil || text == nil {
				return result, fmt.Errorf("invalid withToolResult content")
			}
			message.Role = "USER"
			message.Content = []consoleTranscriptBlock{{Kind: "TOOL_RESULT", CallID: data.CallID, Text: *text, Errored: data.Errored}}
		}
		messages = append(messages, message)
	}
	result.Source, result.Digest, result.State = "recorded-checkpoint", agent.SnapshotDigest, agent.State
	result.Snapshot.Messages = messages
	return result, nil
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
