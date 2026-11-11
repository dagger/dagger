package idtui

import (
	"regexp"
	"strings"

	"github.com/dagger/dagger/dagql/dagui"
)

// Exported entry points to the console's agent roster and transcript code,
// for readers outside the TUI console -- the engine's ReadTrace builtin --
// that build their own dagui.DB from a telemetry store. They are thin
// wrappers: the console's behavior is unchanged.

// TraceAgent is one roster entry: an agent's handle, name, state, parent
// agent, loop spans (oldest first) and checkpoint digest.
type TraceAgent = consoleAgent

// TraceTranscript is a decoded agent conversation and where it came from.
type TraceTranscript = consoleAgentSnapshot

// TraceTranscriptMessage is one message of a TraceTranscript.
type TraceTranscriptMessage = consoleTranscriptMessage

// TraceTranscriptDefaultClip is the console's per-tool-argument and
// per-tool-result clip.
const TraceTranscriptDefaultClip = consoleDefaultClip

// TraceAgents returns db's agent roster, as the console serves it.
func TraceAgents(db *dagui.DB) []TraceAgent {
	return consoleAgentsFromDB(db)
}

// RenderTraceAgents renders a roster as an indented tree.
func RenderTraceAgents(agents []TraceAgent, opts AgentRosterOpts) string {
	return renderAgentRoster(agents, opts)
}

// DecodeTraceTranscript decodes agent's conversation from the call payloads
// in db: its recorded checkpoint, or best-effort an inferred one, with GAP
// messages where it can't. Nothing is evaluated.
func DecodeTraceTranscript(db *dagui.DB, agent TraceAgent) (TraceTranscript, error) {
	return decodeCheckpointWithFallback(db, agent)
}

// DecodeTraceConversation decodes the conversation recorded at digest, with
// GAP messages where it can't.
func DecodeTraceConversation(db *dagui.DB, agent TraceAgent, digest string) (TraceTranscript, error) {
	agent.SnapshotDigest = digest
	return decodeConsoleCheckpoint(db, agent)
}

// InferConversationDigest finds the tip of the longest LLM conversation
// traced beneath spans, skipping nested agents other than agentID ("" skips
// them all). Empty when there is none.
func InferConversationDigest(db *dagui.DB, spans []*dagui.Span, agentID string) string {
	return inferConversationDigest(db, spans, agentID)
}

// IsTraceConversationFrame reports whether digest is a core LLM message call
// in db -- a candidate conversation tip.
func IsTraceConversationFrame(db *dagui.DB, digest string) bool {
	frame := db.Calls[digest]
	return frame != nil && frame.Module == nil && isLLMFrame(db, digest) && isConsoleSpineSelector(frame)
}

// SelectTraceTranscript filters messages by role (USER, ASSISTANT, TOOL,
// SYSTEM, GAP; "" = all but system), tool name and grep, keeping each
// message's index in the whole transcript.
func SelectTraceTranscript(messages []TraceTranscriptMessage, role, tool string, grep *regexp.Regexp) []TraceTranscriptMessage {
	return selectConsoleTranscript(messages, strings.ToUpper(role), tool, grep)
}

// TraceTranscriptPage is one page of a rendered transcript.
type TraceTranscriptPage struct {
	Agent      TraceAgent
	Transcript TraceTranscript
	// Messages are the messages matching the filters; Offset indexes them.
	Messages []TraceTranscriptMessage
	Offset   int
	Limit    int
	// Clip bounds each tool call's arguments and tool result (0 = all).
	Clip int
	// Filters describes the filters, for the page header.
	Filters string
	// NextPage formats the pointer to the page starting at offset.
	NextPage func(offset int) string
}

// RenderTraceTranscript renders a page of a transcript as the console does:
// at most Limit messages or about 40KiB, with the next page's offset.
func RenderTraceTranscript(page TraceTranscriptPage) string {
	return renderConsoleTranscript(consoleTranscriptPage{
		Agent: page.Agent, Snapshot: page.Transcript, Messages: page.Messages,
		Offset: page.Offset, Limit: page.Limit, Clip: page.Clip,
		Filters: page.Filters, NextPage: page.NextPage,
	})
}
