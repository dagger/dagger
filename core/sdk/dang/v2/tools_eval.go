package dangv2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"

	"github.com/dagger/dagger/core"
)

// This file is the Dang side of the DangEval builtin (see
// core/llm_dang_tools.go).

const (
	// dangEvalName names an evaluated snippet in positions and messages.
	dangEvalName = "<eval>"
	// dangOutputMax caps the printed output a DangEval report holds, and
	// the description of its result, only to bound their memory: the
	// engine bounds what the model sees of the report as it bounds a
	// module tool's output, and the tool call's logs have the report. The
	// tail of printed output is kept, the head of a result.
	dangOutputMax = 1 << 20
	// dangMemberNamesMax caps the member names listed after a result.
	dangMemberNamesMax = 80
	// dangBlockStringMin is how long a multi-line string result gets
	// before it is shown as a block of lines rather than one quoted line:
	// the width Describe lays values out in.
	dangBlockStringMin = 80
	// dangLineMax is the line clamp of the tool result and of ReadLogs
	// (core's llmLogsMaxLineLen): a longer line keeps only its head.
	dangLineMax = 2000
	// dangLineStringMax is how many characters a string shown quoted, on
	// one line, keeps, so the line fits dangLineMax: Describe keeps its
	// head and its tail, where errors tend to be. Characters count, not
	// bytes; a result with a line still too long (wide characters, many
	// escapes) is described again keeping a quarter as many.
	dangLineStringMax = 1500
	// dangItemsMax caps the elements described of a flat list, the rest
	// counted. Describe lays out every item it shows before its byte cap
	// applies, at every level, so its work grows as items^depth: anything
	// else nested keeps Describe's default (32).
	dangItemsMax = 512
)

// dangDescribeOptions describe a DangEval result whole, but for what no
// reader could see, or would want to: a string on one line is cut to fit a
// line, long collections and deep nesting are summarized, and the whole
// description is capped at dangOutputMax.
var dangDescribeOptions = dang.DescribeOptions{MaxString: dangLineStringMax, MaxItems: dangItemsMax, MaxBytes: dangOutputMax}

// dangWrite is a bound value a snippet reassigned, with the ID of its final
// value (see core.DangWrite).
type dangWrite struct {
	name, typeName, id string
}

// dangWriter applies a successful snippet's writes and summarizes them.
type dangWriter func(context.Context, []dangWrite) (string, error)

// evalDang is DangEval: it checks source as one more file of the scope, and
// only if that succeeds loads the scope (a module's top level runs) and runs
// source in it, with the Dagger import's queries sent to client. A type error
// or a failure is reported like a result, as DangCheck reports diagnostics:
// the error return is for what keeps it from running at all.
//
// The whole report goes to output too, as a module function's prints go to
// its call's logs: printed output as it is printed, under the report's
// heading for it, and the rest of the report once it is known, so the logs
// read line for line as the report does. Secret plaintexts are scrubbed
// from both.
//
// Assignment is the write: when source succeeds, the scope's bound values
// (`ws`, `chat`, the bound tool objects) it reassigned are handed to write
// with their final values. A snippet that fails writes nothing, and one that
// reassigns `me`, or `chat` beside another value, doesn't run.
func evalDang(ctx context.Context, sc *dangScope, source string, client graphql.Client, output io.Writer, write dangWriter) (core.DangReport, error) {
	start := time.Now()
	var r dangReport
	calls := &countingClient{inner: client}
	if output == nil {
		output = io.Discard
	}
	logs := &lineEndWriter{w: output, heading: dangOutputHeading}
	// printedEnd is where the report's printed output ends: the rest is
	// logged when the report is done.
	printedEnd := 0
	done := func(rejected bool) core.DangReport {
		report := r.String()
		logs.WriteRest(calls.secrets.scrub(report[printedEnd:]))
		return core.DangReport{Text: calls.secrets.scrub(report), Rejected: rejected}
	}

	// Check first: a type error runs nothing.
	snippet := sc.snippetPath()
	a, err := analysis.Analyze(ctx, sc.source(map[string]string{snippet: source}), sc.dir, analysis.Options{
		Focus:             snippet,
		ImportConfigs:     sc.imports(),
		DiagnosticContext: true,
	})
	if err != nil {
		return core.DangReport{}, err
	}
	// The bound values the snippet reassigns, by resolution: what it writes,
	// unless it reassigns one it can't (see bindingWriteDiagnostics).
	assigned := boundAssignments(a, sc, snippet)
	if diags := slices.Concat(a.Diagnostics(), bindingWriteDiagnostics(assigned)); hasErrors(diags) {
		r.diagnostics(sc, diags)
		r.linef("%s · %s · nothing ran", r.counts(), sc.label)
		return done(true), nil
	}

	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
		Name: "Dagger", Client: calls, Schema: sc.schema, AutoImport: true,
	})
	var printed tailBuffer
	sink := io.Writer(&scrubWriter{w: io.MultiWriter(&printed, logs), secrets: &calls.secrets})
	ctx = ioctx.StdoutToContext(ctx, sink)
	ctx = ioctx.StderrToContext(ctx, sink)

	footer := func(status string) string {
		return strings.Join([]string{
			status,
			formatDuration(time.Since(start)),
			plural(int(calls.count.Load()), "engine call"),
			sc.label,
		}, " · ")
	}
	writeOutput := func() {
		if out := printed.String(); out != "" {
			r.WriteString(dangOutputHeading)
			r.WriteString(out)
			if !strings.HasSuffix(out, "\n") {
				r.WriteByte('\n')
			}
		}
		printedEnd = r.Len()
	}
	// fail reports err; rejected says it is the snippet failing to check.
	fail := func(err error, rejected bool) core.DangReport {
		writeOutput()
		diags := errorDiagnostics(err)
		if len(diags) == 0 {
			diags = []analysis.Diagnostic{{Severity: analysis.SeverityError, Message: err.Error()}}
		}
		sc.attributeBindingFailures(diags)
		r.diagnostics(sc, diags)
		r.linef("%s", footer("failed"))
		return done(rejected)
	}

	mod, err := dang.LoadDir(ctx, sc.dir, dang.LoadOptions{FS: sc.source(nil), Eager: sc.module})
	if err != nil {
		return fail(err, false), nil
	}
	defer mod.Close()
	session, err := mod.NewSession(ctx)
	if err != nil {
		return fail(err, false), nil
	}
	ran, err := session.Check(ctx, dangEvalName, source)
	if err != nil {
		return fail(err, true), nil
	}
	val, err := ran.Eval(ctx)
	if err != nil {
		return fail(err, false), nil
	}
	writes, err := finalWrites(ctx, session, sc.bound, assignedNames(assigned))
	if err != nil {
		return fail(err, false), nil
	}

	writeOutput()
	wroteWS := slices.ContainsFunc(writes, func(w dangWrite) bool { return w.name == core.DangWorkspaceBinding })
	unwritten := reportEvalResult(ctx, &r, calls, val, ran.Type, wroteWS, endsWithWrite(a, snippet, assigned))
	r.linef("%s", footer("ok"))
	switch {
	case len(writes) > 0 && write != nil:
		summary, err := write(ctx, writes)
		if err != nil {
			r.linef("write failed: %s", err)
		} else if summary != "" {
			r.linef("%s", summary)
		}
	case unwritten:
		// Only where it matters: a result that looks like a write.
		r.linef("0 writes")
	}
	return done(false), nil
}

// finalWrites returns the bound values assigned names, with their values as
// the session holds them after it ran. A value is written by assigning it
// (`ws = ws.withChanges(…)`, `editor = editor.todoWrite(…)`); calling a
// method on it, or returning a new one, writes nothing. A read-only value is
// never written.
func finalWrites(ctx context.Context, session *dang.Session, bound []dangValue, assigned map[string]bool) ([]dangWrite, error) {
	var writes []dangWrite
	for _, v := range bound {
		if !assigned[v.name] || v.readOnly {
			continue
		}
		read, err := session.Check(ctx, "<"+v.name+">", v.name)
		if err != nil {
			return nil, err
		}
		final, err := read.Eval(ctx)
		if err != nil {
			return nil, err
		}
		g, ok := final.(dang.GraphQLValue)
		if !ok {
			return nil, fmt.Errorf("%s is a %T, not an API object", v.name, final)
		}
		id, err := g.ID(ctx)
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", v.name, err)
		}
		writes = append(writes, dangWrite{name: v.name, typeName: v.typeName, id: id})
	}
	return writes, nil
}

// reassignedNames are the names source assigns with `=`, anywhere in it, by
// its syntax alone: a local shadowing a bound value isn't told apart (see
// boundAssignments for what a snippet writes).
func reassignedNames(source string, version dang.Version) map[string]bool {
	names := map[string]bool{}
	parsed, err := dang.Parse(dangEvalName, []byte(source), dang.WithVersion(version))
	if err != nil {
		return names
	}
	block, ok := parsed.(*dang.FileBlock)
	if !ok {
		return names
	}
	block.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}
		if re, ok := n.(*dang.Reassignment); ok {
			if sym, ok := re.Target.(*dang.Symbol); ok {
				names[sym.Name] = true
			}
		}
		return true
	})
	return names
}

// endsWithWrite reports whether the snippet at snippet ends by assigning one
// of the bound values it writes, so its result is the value written.
func endsWithWrite(a *analysis.Analysis, snippet string, assigned []boundAssignment) bool {
	f := a.File(snippet)
	if f == nil || f.Block == nil || len(f.Block.Forms) == 0 {
		return false
	}
	re, ok := f.Block.Forms[len(f.Block.Forms)-1].(*dang.Reassignment)
	if !ok {
		return false
	}
	sym := assignedSymbol(re.Target)
	if sym == nil || sym.Loc == nil {
		return false
	}
	return slices.ContainsFunc(assigned, func(as boundAssignment) bool {
		return as.path == snippet && as.pos == analysis.Pos{Line: sym.Loc.Line, Column: sym.Loc.Column} && !as.value.readOnly
	})
}

// reportEvalResult reports a snippet's result: its value and, for an object,
// its members. A result is described, never applied: writing is assigning (see
// finalWrites), so a Workspace or Changeset the snippet only returns says
// how to write it; reportEvalResult reports whether it was one. A result that
// is the value the snippet just wrote (wroteResult) lists no members: the
// write's summary says what came of it.
func reportEvalResult(ctx context.Context, r *dangReport, calls *countingClient, val dang.Value, typ hm.Type, wroteWS, wroteResult bool) (unwritten bool) {
	// Dang leaves out its "(not evaluated…)" hint for a snippet ending in an
	// assignment to any binding from before it. Only a bound value's write
	// does something, though: reassigning a module's own binding ran nothing
	// and persists nothing, so keep the hint there.
	if g, ok := val.(dang.GraphQLValue); ok && g.Assigned && !wroteResult {
		g.Assigned = false
		val = g
	}
	r.linef("%s", calls.secrets.scrub(describeResult(ctx, val, typ)))
	if typ == nil {
		return false
	}
	switch strings.TrimPrefix(strings.TrimSuffix(dang.FormatType(typ), "!"), "Dagger.") {
	case "Changeset":
		r.linef("(not applied; to write it, assign it: ws = ws.withChanges(…))")
		unwritten = true
	case "Workspace":
		if !wroteWS {
			r.linef("(not applied; to write it, assign it: ws = …)")
			unwritten = true
		}
	}
	// Only an object's members are worth listing: what to do with it next.
	if _, isNull := val.(dang.NullValue); isNull || val == nil || wroteResult || !hasMemberListing(typ) {
		return unwritten
	}
	members := analysis.TypeMembers(typ)
	if len(members) == 0 {
		return unwritten
	}
	names := make([]string, 0, len(members))
	for i, m := range members {
		if i == dangMemberNamesMax {
			names = append(names, fmt.Sprintf("… (%d more)", len(members)-i))
			break
		}
		names = append(names, m.Name)
	}
	r.linef("members: %s", strings.Join(names, " "))
	return unwritten
}

// describeResult describes a snippet's result as `TYPE = value` (see
// dangDescribeOptions). A long multi-line string, such as a file's
// contents, is shown as a block of its own lines between triple single
// quotes rather than as one quoted line, so it reads as the text it is, and
// a reader that bounds lines (the tool result, ReadLogs) cuts it by lines.
// A triple-single-quoted string is Dang's verbatim string at every version:
// `${` and `\` in it are text, where from Dang 2.5 a `"""` string would
// interpolate and escape them. The block is the string verbatim, not a Dang
// literal (which would drop common indentation and trailing blank lines): a
// string without a final newline says so. Text holding triple single quotes
// (a Dang module's shell script, a Python docstring) is still shown
// verbatim, so an agent can copy it into an edit, with a note that the block
// ends at its last line, as it always does.
//
// (The quotes aren't spelled out here: gofmt rewrites two single quotes in a
// doc comment as a typographic closing quote.)
func describeResult(ctx context.Context, val dang.Value, typ hm.Type) string {
	s, ok := val.(dang.StringValue)
	if !ok || !isBlockString(s.Val) {
		opts := dangDescribeOptions
		if !isFlatList(val) {
			// Describe's work grows as items^depth: only a flat list
			// shows more than Describe's default.
			opts.MaxItems = 0
		}
		desc := dang.Describe(ctx, val, typ, opts)
		if longestLine(desc) > dangLineMax {
			opts.MaxString /= 4
			desc = dang.Describe(ctx, val, typ, opts)
		}
		return desc
	}
	if typ == nil {
		typ = s.Type()
	}
	text, closing := s.Val, ""
	if over := len(text) - dangOutputMax; over > 0 {
		cut := dangOutputMax
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
		closing += fmt.Sprintf(" (%d more bytes not shown; slice the string to read them)", len(s.Val)-cut)
	} else if !strings.HasSuffix(text, "\n") {
		closing += " (no final newline)"
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if strings.Contains(text, "'''") {
		closing += " (the text holds '''; the block ends at its last line)"
	}
	if longestLine(text) > dangLineMax {
		text, closing = cutLongLines(text), closing+" (long lines cut in the middle)"
	}
	return fmt.Sprintf("%s = '''\n%s'''%s", typ, text, closing)
}

// isFlatList reports whether v is a list of values that hold no others.
func isFlatList(v dang.Value) bool {
	list, ok := v.(dang.ListValue)
	if !ok {
		return false
	}
	for _, e := range list.Elements {
		switch e.(type) {
		case dang.ListValue, dang.MapValue, *dang.Object:
			return false
		}
	}
	return true
}

// cutLongLines cuts each line of text longer than dangLineMax to its head
// and its tail around a count of what was left out, as Describe cuts a long
// string, so the line clamp of a reader doesn't drop its end.
func cutLongLines(text string) string {
	var b strings.Builder
	for line := range strings.Lines(text) {
		body := strings.TrimSuffix(line, "\n")
		if len(body) <= dangLineMax {
			b.WriteString(line)
			continue
		}
		head, tail := dangLineMax*3/4, len(body)-dangLineMax/5
		for head > 0 && !utf8.RuneStart(body[head]) {
			head--
		}
		for tail < len(body) && !utf8.RuneStart(body[tail]) {
			tail++
		}
		fmt.Fprintf(&b, "%s…(%d bytes omitted)…%s%s", body[:head], tail-head, body[tail:], line[len(body):])
	}
	return b.String()
}

// isBlockString reports whether a string result is shown as a block: it
// spans lines, and quoted on one it would not fit dangBlockStringMin.
func isBlockString(s string) bool {
	body := strings.TrimSuffix(s, "\n")
	return strings.Contains(body, "\n") && len(s)+strings.Count(s, "\n")+2 > dangBlockStringMin
}

// longestLine is the length in bytes of the longest line of s.
func longestLine(s string) int {
	longest := 0
	for line := range strings.Lines(s) {
		longest = max(longest, len(strings.TrimSuffix(line, "\n")))
	}
	return longest
}

func hasErrors(diags []analysis.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == analysis.SeverityError {
			return true
		}
	}
	return false
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return d.Round(100 * time.Millisecond).String()
	}
}

// countingClient counts the queries a snippet sends, and remembers the
// secret plaintexts they return, to scrub them from what is shown.
type countingClient struct {
	inner   graphql.Client
	count   atomic.Int64
	secrets secretSet
}

func (c *countingClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.count.Add(1)
	err := c.inner.MakeRequest(ctx, req, resp)
	if err == nil && req != nil && resp != nil && strings.Contains(req.Query, "plaintext") {
		// Secret.plaintext is the one way a snippet reads a secret: every
		// string such a query returns is treated as one.
		if data, err := json.Marshal(resp.Data); err == nil {
			var decoded any
			if json.Unmarshal(data, &decoded) == nil {
				c.secrets.addStrings(decoded)
			}
		}
	}
	return err
}

// secretSet holds the secret plaintexts seen, to scrub from output.
type secretSet struct {
	mu     sync.Mutex
	values []string
}

func (s *secretSet) addStrings(v any) {
	switch v := v.(type) {
	case string:
		if v != "" {
			s.mu.Lock()
			s.values = append(s.values, v)
			s.mu.Unlock()
		}
	case []any:
		for _, e := range v {
			s.addStrings(e)
		}
	case map[string]any:
		for _, e := range v {
			s.addStrings(e)
		}
	}
}

func (s *secretSet) scrub(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, secret := range s.values {
		text = strings.ReplaceAll(text, secret, "***")
	}
	return text
}

// scrubWriter scrubs secrets from what it writes.
type scrubWriter struct {
	w       io.Writer
	secrets *secretSet
}

func (s *scrubWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(s.w, s.secrets.scrub(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// tailBuffer keeps the last dangOutputMax bytes written to it.
type tailBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	dropped int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
	if over := b.buf.Len() - dangOutputMax; over > 0 {
		b.dropped += over
		b.buf.Next(over)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dropped > 0 {
		return fmt.Sprintf("… (%d earlier bytes cut)\n%s", b.dropped, b.buf.String())
	}
	return b.buf.String()
}

// dangOutputHeading heads a DangEval report's printed output.
const dangOutputHeading = "output:\n"

// lineEndWriter writes printed output to w under a heading, written before
// the first of it, and remembers whether what it wrote last ended a line,
// so the rest of a report logged after the output starts on a line of its
// own, as it does in the report.
type lineEndWriter struct {
	mu      sync.Mutex
	w       io.Writer
	heading string
	wrote   bool
	midLine bool
}

func (l *lineEndWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if !l.wrote {
		l.wrote = true
		if _, err := io.WriteString(l.w, l.heading); err != nil {
			return 0, err
		}
	}
	l.midLine = p[len(p)-1] != '\n'
	return l.w.Write(p)
}

// WriteRest writes the rest of a report, on a line of its own. Logging is
// best effort: a failure to log doesn't fail the call.
func (l *lineEndWriter) WriteRest(rest string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rest == "" {
		return
	}
	if l.midLine {
		rest = "\n" + rest
	}
	_, _ = io.WriteString(l.w, rest)
	l.midLine = !strings.HasSuffix(rest, "\n")
}
