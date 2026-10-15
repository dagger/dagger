package idtui

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// Recovery endpoints: read what an agent did from telemetry alone, without
// restoring it or evaluating anything. /call returns one ingested call with
// its arguments in full; /salvage packs an agent's transcript and the tool
// calls that changed its workspace into a tar archive, so work can be
// recovered even when the agent itself can't be.

type consoleCall struct {
	Digest   string           `json:"digest"`
	Field    string           `json:"field"`
	Type     string           `json:"type,omitempty"`
	Receiver string           `json:"receiver,omitempty"`
	Module   string           `json:"module,omitempty"`
	Nth      int64            `json:"nth,omitempty"`
	View     string           `json:"view,omitempty"`
	Args     []consoleCallArg `json:"args"`
}

type consoleCallArg struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// consoleCallHandler serves GET /call?dig=<digest>[&arg=<name>]: the call as
// JSON with every argument in full, or with arg, that one argument alone --
// a string as raw text/plain, anything else as JSON. ID-valued arguments are
// {"call": "<digest>"}, ready for the next /call.
func (fe *frontendPretty) consoleCallHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dig := q.Get("dig")
	if dig == "" {
		http.Error(w, "want ?dig=<call digest, e.g. xxh3:...>[&arg=<name>]", http.StatusBadRequest)
		return
	}
	fe.consoleMu.Lock()
	frame := fe.db.Call(dig)
	var call consoleCall
	if frame != nil {
		call = newConsoleCall(frame)
	}
	fe.consoleMu.Unlock()
	if frame == nil {
		http.Error(w, fmt.Sprintf("call %s never reached this client", dig), http.StatusNotFound)
		return
	}
	name := q.Get("arg")
	if name == "" {
		consoleJSON(w, call)
		return
	}
	for _, arg := range call.Args {
		if arg.Name != name {
			continue
		}
		if s, ok := arg.Value.(string); ok {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(s)) //nolint:gosec // G705: argument text as text/plain, never rendered as HTML.
			return
		}
		consoleJSON(w, arg.Value)
		return
	}
	names := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		names = append(names, arg.Name)
	}
	http.Error(w, fmt.Sprintf("call %s has no argument %q (has: %s)", dig, name, strings.Join(names, ", ")), http.StatusNotFound)
}

func newConsoleCall(frame *callpbv1.Call) consoleCall {
	call := consoleCall{
		Digest:   frame.GetDigest(),
		Field:    frame.GetField(),
		Type:     consoleTypeString(frame.GetType()),
		Receiver: frame.GetReceiverDigest(),
		Module:   frame.GetModule().GetName(),
		Nth:      frame.GetNth(),
		View:     frame.GetView(),
		Args:     make([]consoleCallArg, 0, len(frame.GetArgs())),
	}
	for _, arg := range frame.GetArgs() {
		call.Args = append(call.Args, consoleCallArg{Name: arg.GetName(), Value: consoleLiteralJSON(arg.GetValue())})
	}
	return call
}

func consoleTypeString(t *callpbv1.Type) string {
	if t == nil {
		return ""
	}
	s := t.GetNamedType()
	if t.GetElem() != nil {
		s = "[" + consoleTypeString(t.GetElem()) + "]"
	}
	if t.GetNonNull() {
		s += "!"
	}
	return s
}

// consoleLiteralJSON is consoleLiteralData for any literal: ID references
// become {"call": digest} rather than errors, and bytes stay bytes.
func consoleLiteralJSON(literal *callpbv1.Literal) any {
	switch value := literal.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		return map[string]string{"call": value.CallDigest}
	case *callpbv1.Literal_Bytes:
		return value.Bytes
	case *callpbv1.Literal_List:
		list := make([]any, 0, len(value.List.GetValues()))
		for _, item := range value.List.GetValues() {
			list = append(list, consoleLiteralJSON(item))
		}
		return list
	case *callpbv1.Literal_Object:
		object := make(map[string]any, len(value.Object.GetValues()))
		for _, item := range value.Object.GetValues() {
			object[item.GetName()] = consoleLiteralJSON(item.GetValue())
		}
		return object
	default:
		data, err := consoleLiteralData(literal)
		if err != nil {
			return fmt.Sprintf("<%T>", literal.GetValue())
		}
		return data
	}
}

// salvage serves GET /salvage?agent=<handle|name>: a tar archive with
//
//	index.md          where the transcript came from, its gaps, and every
//	                  workspace-changing tool call in order
//	transcript.md     the whole transcript, untruncated
//	steps/NNNN-<tool>.json   each such call: full arguments and result
//	steps/NNNN-<tool>.patch  the diff its result reported, if any
//
// It reads transcripts the same way /transcript does, falling back to the
// loaded telemetry, so it works on a trace whose agent can't be restored.
func (i *consoleTraceInspector) salvage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("agent") == "" {
		http.Error(w, "agent is required: use /agents, then select a handle or unique name", http.StatusBadRequest)
		return
	}
	src := i.agentSource(r.Context())
	agent, snapshot, ok := readConsoleTranscript(w, r, src.agents, src.withWarning())
	if !ok {
		return
	}
	archive, err := buildConsoleSalvage(agent, snapshot)
	if err != nil {
		http.Error(w, "build salvage archive: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(archive) //nolint:gosec // G705: a tar archive, never rendered as HTML.
}

type consoleSalvageStep struct {
	Seq       int             `json:"seq"`
	Message   int             `json:"message"`
	Tool      string          `json:"tool"`
	CallID    string          `json:"callId,omitempty"`
	Arguments json.RawMessage `json:"arguments"`
	// Result is nil when no result was recorded for the call, e.g. the
	// last call of an interrupted turn.
	Result  *string `json:"result"`
	Errored bool    `json:"errored,omitempty"`

	patch string
}

// Tools that change a workspace or its history, by the name after any
// "<toolset>_" prefix. Any call whose result carries a git diff counts too.
var consoleSalvageTools = []string{
	"write", "edit", "rm", "mv", "cp", "commit", "restore", "reset",
	"rebase", "rebaseContinue", "rebaseSkip", "rebaseAbort", "cherryPick",
	"checkout", "push", "pushBranch", "salvagePending", "generate",
}

var consoleDiffStart = regexp.MustCompile(`(?m)^diff --git `)

func isConsoleSalvageTool(name, result string) bool {
	base := name
	if i := strings.LastIndex(name, "_"); i >= 0 {
		base = name[i+1:]
	}
	for _, tool := range consoleSalvageTools {
		if strings.EqualFold(base, tool) {
			return true
		}
	}
	return consoleDiffStart.MatchString(result)
}

// consoleResultPatch extracts the git diff a tool result reports: from its
// first "diff --git" line up to a bare "---" separator, if any.
func consoleResultPatch(result string) string {
	loc := consoleDiffStart.FindStringIndex(result)
	if loc == nil {
		return ""
	}
	patch := result[loc[0]:]
	if end := strings.Index(patch, "\n---\n"); end >= 0 {
		patch = patch[:end+1]
	}
	return strings.TrimRight(patch, "\n") + "\n"
}

func consoleSalvageSteps(messages []consoleTranscriptMessage) []consoleSalvageStep {
	type result struct {
		text    string
		errored bool
	}
	results := map[string]result{}
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Kind == "TOOL_RESULT" && block.CallID != "" {
				results[block.CallID] = result{block.Text, block.Errored}
			}
		}
	}
	var steps []consoleSalvageStep
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Kind != "TOOL_CALL" {
				continue
			}
			res, hasResult := results[block.CallID]
			if !isConsoleSalvageTool(block.ToolName, res.text) {
				continue
			}
			args := json.RawMessage(block.Arguments)
			if !json.Valid(args) {
				args, _ = json.Marshal(block.Arguments)
			}
			step := consoleSalvageStep{
				Seq: len(steps) + 1, Message: message.Index, Tool: block.ToolName,
				CallID: block.CallID, Arguments: args,
			}
			if hasResult {
				text := res.text
				step.Result = &text
				step.Errored = res.errored
				step.patch = consoleResultPatch(text)
			}
			steps = append(steps, step)
		}
	}
	return steps
}

// consoleStepTarget summarizes what a step acts on, from its arguments.
func consoleStepTarget(args json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(args, &fields) != nil {
		return ""
	}
	str := func(key string) string {
		s, _ := fields[key].(string)
		return s
	}
	switch {
	case str("filePath") != "":
		return str("filePath")
	case str("oldPath") != "" || str("newPath") != "":
		return str("oldPath") + " -> " + str("newPath")
	case str("sourcePath") != "" || str("destPath") != "":
		return str("sourcePath") + " -> " + str("destPath")
	case str("path") != "":
		return str("path")
	case str("message") != "":
		subject, _, _ := strings.Cut(str("message"), "\n")
		return fmt.Sprintf("%q", subject)
	case str("repo") != "":
		return str("repo")
	case str("commit") != "":
		return str("commit")
	case str("ref") != "":
		return str("ref")
	}
	return ""
}

func buildConsoleSalvage(agent consoleAgent, snapshot consoleAgentSnapshot) ([]byte, error) {
	messages := snapshot.Snapshot.Messages
	steps := consoleSalvageSteps(messages)

	var index strings.Builder
	fmt.Fprintf(&index, "# Salvage: %s (%s)\n\n", agent.Name, agent.ID)
	fmt.Fprintf(&index, "- source: %s\n", snapshot.Source)
	if snapshot.Digest != "" {
		fmt.Fprintf(&index, "- snapshot digest: %s\n", snapshot.Digest)
	}
	if snapshot.Snapshot.ID != "" {
		fmt.Fprintf(&index, "- snapshot id: %s\n", snapshot.Snapshot.ID)
	}
	fmt.Fprintf(&index, "- state: %s\n- messages: %d\n", snapshot.State, len(messages))
	if snapshot.Warning != "" {
		fmt.Fprintf(&index, "- warning: %s\n", snapshot.Warning)
	}
	if len(snapshot.Gaps) > 0 {
		fmt.Fprintf(&index, "\n## Gaps (%d)\n\nThe transcript is partial. Each gap also appears in place in transcript.md.\n\n", len(snapshot.Gaps))
		for _, gap := range snapshot.Gaps {
			fmt.Fprintf(&index, "- %s\n", gap)
		}
	}
	fmt.Fprintf(&index, "\n## Workspace changes (%d)\n\n", len(steps))
	if len(steps) == 0 {
		index.WriteString("No tool call in the transcript changed a workspace.\n")
	} else {
		index.WriteString("Tool calls that changed the agent's workspace or its history, in order. " +
			"To replay them, apply each step's change in sequence and skip the errored ones: " +
			"steps/NNNN-<tool>.json has the full arguments (e.g. an edit's oldText/newText, " +
			"a write's contents) and the result; a .patch next to it has the diff the tool " +
			"reported. Check transcript.md for anything the agent changed back later, such " +
			"as a temporary edit made to test a regression.\n\n")
		for _, step := range steps {
			status := ""
			switch {
			case step.Errored:
				status = " ERRORED"
			case step.Result == nil:
				status = " NO RESULT"
			}
			fmt.Fprintf(&index, "- %04d [message %d] %s %s%s", step.Seq, step.Message, step.Tool, consoleStepTarget(step.Arguments), status)
			if step.patch != "" {
				index.WriteString(" (patch)")
			}
			index.WriteString("\n")
		}
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name, contents string) error {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(contents)),
			ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		_, err := tw.Write([]byte(contents))
		return err
	}
	if err := add("index.md", index.String()); err != nil {
		return nil, err
	}
	if err := add("transcript.md", renderConsoleTranscriptMarkdown(agent, messages)); err != nil {
		return nil, err
	}
	for _, step := range steps {
		base := fmt.Sprintf("steps/%04d-%s", step.Seq, consoleSafeFileName(step.Tool))
		encoded, err := json.MarshalIndent(step, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := add(base+".json", string(encoded)+"\n"); err != nil {
			return nil, err
		}
		if step.patch != "" {
			if err := add(base+".patch", step.patch); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var consoleUnsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func consoleSafeFileName(s string) string {
	s = consoleUnsafeFileChars.ReplaceAllString(s, "_")
	if s == "" {
		return "tool"
	}
	return s
}

// renderConsoleTranscriptMarkdown renders every message in full. Fences grow
// past any backtick run in their content, so code in messages can't end them.
func renderConsoleTranscriptMarkdown(agent consoleAgent, messages []consoleTranscriptMessage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Transcript: %s (%s)\n", agent.Name, agent.ID)
	for _, message := range messages {
		heading := message.Role
		if message.Origin != nil && message.Origin.AgentName != "" {
			heading += " from " + message.Origin.AgentName
		}
		fmt.Fprintf(&b, "\n## [%d] %s\n", message.Index, heading)
		for _, block := range message.Content {
			switch block.Kind {
			case "TEXT", consoleGapRole:
				b.WriteString("\n" + strings.TrimRight(block.Text, "\n") + "\n")
			case "THINKING":
				b.WriteString("\n> thinking: " + strings.ReplaceAll(strings.TrimRight(block.Text, "\n"), "\n", "\n> ") + "\n")
			case "TOOL_CALL":
				args := block.Arguments
				var pretty bytes.Buffer
				if json.Indent(&pretty, []byte(args), "", "  ") == nil {
					args = pretty.String()
				}
				fmt.Fprintf(&b, "\n### tool call: %s (%s)\n\n%s", block.ToolName, block.CallID, consoleFenced("json", args))
			case "TOOL_RESULT":
				status := ""
				if block.Errored {
					status = " ERRORED"
				}
				fmt.Fprintf(&b, "\n### tool result (%s)%s\n\n%s", block.CallID, status, consoleFenced("", block.Text))
			default:
				fmt.Fprintf(&b, "\n### %s\n\n%s", block.Kind, consoleFenced("", block.Text))
			}
		}
	}
	return b.String()
}

func consoleFenced(lang, text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + lang + "\n" + strings.TrimRight(text, "\n") + "\n" + fence + "\n"
}
