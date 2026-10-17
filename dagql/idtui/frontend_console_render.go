package idtui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// consoleDefaultClip bounds each tool call's arguments and each tool
	// result in a rendered transcript. Prose (prompts, replies, thinking) is
	// never clipped.
	consoleDefaultClip = 4000
	// consoleTextPageBytes ends a transcript page early so a page fits an LLM
	// tool result (48KiB in core/mcp.go) without the middle being elided.
	consoleTextPageBytes = 40 * 1024
	// consoleWrapBytes keeps rendered lines under LLM tool results' per-line
	// clamp (2000 bytes), which would otherwise cut long paragraphs and
	// single-line JSON.
	consoleWrapBytes = 1000
)

func consoleText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(text))
}

// renderConsoleAgents renders the roster as an indented tree, one agent per
// line. Recorded traces hide agents without a checkpoint (typically agents
// run by tests inside the trace), since their transcripts can't be read.
func renderConsoleAgents(agents []consoleAgent, recorded, all bool, header string) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	names := map[string]int{}
	for _, agent := range agents {
		names[agent.Name]++
	}
	visible := map[string]bool{}
	hidden := 0
	for _, agent := range agents {
		if recorded && !all && agent.SnapshotDigest == "" {
			hidden++
			continue
		}
		visible[agent.ID] = true
	}
	children := map[string][]consoleAgent{}
	var roots []consoleAgent
	for _, agent := range agents {
		if !visible[agent.ID] {
			continue
		}
		if agent.ParentID != "" && visible[agent.ParentID] && agent.ParentID != agent.ID {
			children[agent.ParentID] = append(children[agent.ParentID], agent)
		} else {
			roots = append(roots, agent)
		}
	}
	fmt.Fprintf(&b, "%d agent(s); read one with transcript(agent: <handle or unique name>).\n\n", len(visible))
	printed := map[string]bool{}
	var walk func(agent consoleAgent, depth int)
	walk = func(agent consoleAgent, depth int) {
		if printed[agent.ID] {
			return
		}
		printed[agent.ID] = true
		state := agent.State
		if state == "" {
			state = "-"
		}
		var notes []string
		if names[agent.Name] > 1 {
			notes = append(notes, "name shared: use the handle")
		}
		if recorded && agent.SnapshotDigest == "" {
			notes = append(notes, "no checkpoint")
		}
		note := ""
		if len(notes) > 0 {
			note = "  (" + strings.Join(notes, "; ") + ")"
		}
		fmt.Fprintf(&b, "%s%s  %s  %s%s\n", strings.Repeat("  ", depth), agent.ID, agent.Name, state, note)
		for _, child := range children[agent.ID] {
			walk(child, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "\n%d agent(s) without a recorded checkpoint hidden (e.g. agents run by tests inside this trace); /agents?all=true lists them.\n", hidden)
	}
	return b.String()
}

type consoleTranscriptPage struct {
	Agent    consoleAgent
	Snapshot consoleAgentSnapshot
	// Messages are the messages matching the filters; Offset indexes them.
	Messages []consoleTranscriptMessage
	Offset   int
	Limit    int
	Clip     int
	Filters  string
}

func consoleTranscriptFilters(q url.Values) string {
	var filters []string
	for _, name := range []string{"role", "tool", "grep"} {
		if value := q.Get(name); value != "" {
			filters = append(filters, fmt.Sprintf("%s=%q", name, value))
		}
	}
	if len(filters) == 0 {
		return "all non-system messages"
	}
	return strings.Join(filters, ", ")
}

// renderConsoleTranscript renders a page of a transcript as plain text:
// a header per message, prose verbatim, tool calls as name + arguments and
// tool results labeled with the tool that produced them. The page stops at
// Limit messages or consoleTextPageBytes, whichever comes first, and always
// says where the next page starts.
func renderConsoleTranscript(page consoleTranscriptPage) string {
	toolNames := map[string]string{}
	for _, message := range page.Snapshot.Snapshot.Messages {
		for _, block := range message.Content {
			if block.Kind == "TOOL_CALL" && block.CallID != "" {
				toolNames[block.CallID] = block.ToolName
			}
		}
	}
	total := len(page.Messages)
	var body strings.Builder
	shown := 0
	for i := page.Offset; i < total && shown < page.Limit; i++ {
		rendered := wrapConsoleLines(renderConsoleMessage(page.Messages[i], toolNames, page.Clip))
		if shown > 0 && body.Len()+len(rendered) > consoleTextPageBytes {
			break
		}
		body.WriteString(rendered)
		shown++
	}
	next := page.Offset + shown

	var b strings.Builder
	id := page.Agent.ID
	if page.Agent.Name != "" && page.Agent.Name != id {
		id = page.Agent.Name + " (" + page.Agent.ID + ")"
	}
	state := page.Agent.State
	if state == "" {
		state = "-"
	}
	source := page.Snapshot.Source
	if page.Snapshot.Digest != "" {
		source += " " + page.Snapshot.Digest
	} else if page.Snapshot.Snapshot.ID != "" {
		source += " " + page.Snapshot.Snapshot.ID
	}
	fmt.Fprintf(&b, "agent %s · %s · %s\n", id, state, source)
	if shown == 0 {
		fmt.Fprintf(&b, "no messages at offset %d: %d match (%s)\n", page.Offset, total, page.Filters)
	} else {
		fmt.Fprintf(&b, "messages %d-%d of %d matching (%s)\n", page.Offset+1, next, total, page.Filters)
	}
	if page.Snapshot.Warning != "" {
		fmt.Fprintf(&b, "WARNING: %s\n", page.Snapshot.Warning)
	}
	if n := len(page.Snapshot.Gaps); n > 0 {
		fmt.Fprintf(&b, "PARTIAL: %d gap(s) where the checkpoint could not be fully decoded; each appears in place as a GAP message.\n", n)
	}
	nextHint := ""
	if next < total {
		nextHint = fmt.Sprintf("next page: offset=%d (%d more)\n", next, total-next)
		b.WriteString(nextHint)
	}
	b.WriteString("\n")
	b.WriteString(body.String())
	if nextHint != "" {
		b.WriteString("\n")
		b.WriteString(nextHint)
	}
	return b.String()
}

func renderConsoleMessage(message consoleTranscriptMessage, toolNames map[string]string, clip int) string {
	var b strings.Builder
	role := message.Role
	isToolResult := len(message.Content) > 0 && message.Content[0].Kind == "TOOL_RESULT"
	if isToolResult {
		role = "TOOL RESULT"
	}
	fmt.Fprintf(&b, "── #%d %s", message.Index, role)
	if origin := message.Origin; origin != nil {
		switch {
		case origin.AgentName != "":
			fmt.Fprintf(&b, " · from %s %q", strings.ToLower(origin.Kind), origin.AgentName)
		case origin.Kind != "":
			fmt.Fprintf(&b, " · from %s", strings.ToLower(origin.Kind))
		}
		if origin.Ref != "" {
			fmt.Fprintf(&b, " %s", origin.Ref)
		}
		if origin.ReplyTo != "" {
			fmt.Fprintf(&b, " (reply to %s)", origin.ReplyTo)
		}
	}
	b.WriteString(" ──\n")
	for _, block := range message.Content {
		switch block.Kind {
		case "TEXT":
			b.WriteString(strings.TrimRight(block.Text, "\n"))
			b.WriteString("\n")
		case "THINKING":
			b.WriteString("[thinking]\n")
			b.WriteString(strings.TrimRight(block.Text, "\n"))
			b.WriteString("\n[/thinking]\n")
		case "TOOL_CALL":
			fmt.Fprintf(&b, "→ %s", block.ToolName)
			if block.CallID != "" {
				fmt.Fprintf(&b, "  [%s]", block.CallID)
			}
			b.WriteString("\n")
			b.WriteString(renderConsoleToolArguments(block.Arguments, clip))
		case "TOOL_RESULT":
			name := toolNames[block.CallID]
			if name == "" {
				name = "unknown tool"
			}
			fmt.Fprintf(&b, "← %s", name)
			if block.CallID != "" {
				fmt.Fprintf(&b, "  [%s]", block.CallID)
			}
			if block.Errored {
				b.WriteString("  ERROR")
			}
			b.WriteString("\n")
			if block.Text != "" {
				b.WriteString(clipConsoleText(strings.TrimRight(block.Text, "\n"), clip))
				b.WriteString("\n")
			} else if len(message.Content) == 1 {
				b.WriteString("(empty result)\n")
			}
		default:
			fmt.Fprintf(&b, "[%s] %s\n", block.Kind, strings.TrimRight(block.Text, "\n"))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// renderConsoleToolArguments prints a JSON object's fields one per line,
// strings raw so edits and file contents read as written.
func renderConsoleToolArguments(arguments string, clip int) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" || arguments == "{}" || arguments == "null" {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &fields); err != nil {
		return "  " + clipConsoleText(arguments, clip) + "\n"
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		raw := fields[key]
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			value = string(raw)
		}
		value = clipConsoleText(value, clip)
		if strings.Contains(value, "\n") {
			fmt.Fprintf(&b, "  %s:\n", key)
			for _, line := range strings.Split(strings.TrimRight(value, "\n"), "\n") {
				b.WriteString("    ")
				b.WriteString(line)
				b.WriteString("\n")
			}
			continue
		}
		fmt.Fprintf(&b, "  %s: %s\n", key, value)
	}
	return b.String()
}

// clipConsoleText keeps the head of text within clip bytes (0 = unclipped),
// on a rune boundary, and says how much went.
func clipConsoleText(text string, clip int) string {
	if clip <= 0 || len(text) <= clip {
		return text
	}
	cut := clip
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + fmt.Sprintf("\n[... %d more bytes clipped; pass clip=0 for all of it]", len(text)-cut)
}

// wrapConsoleLines breaks lines longer than consoleWrapBytes at the last
// space before the limit (or a rune boundary when there is none).
func wrapConsoleLines(text string) string {
	if !anyConsoleLineLonger(text, consoleWrapBytes) {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		for len(line) > consoleWrapBytes {
			cut := strings.LastIndexByte(line[:consoleWrapBytes], ' ')
			if cut <= 0 {
				cut = consoleWrapBytes
				for cut > 0 && !utf8.RuneStart(line[cut]) {
					cut--
				}
				out = append(out, line[:cut])
				line = line[cut:]
				continue
			}
			out = append(out, line[:cut])
			line = line[cut+1:]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func anyConsoleLineLonger(text string, limit int) bool {
	return slices.ContainsFunc(strings.Split(text, "\n"), func(line string) bool { return len(line) > limit })
}
