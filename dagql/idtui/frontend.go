package idtui

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/term"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type cmdContextKey struct{}
type cmdContext struct {
	printTraceLink bool
}

// WithPrintTraceLink is used for enabling printing the trace link
// for the selected commands.
func WithPrintTraceLink(ctx context.Context, printTraceLink bool) context.Context {
	return context.WithValue(ctx, cmdContextKey{}, &cmdContext{printTraceLink: printTraceLink})
}

func FromCmdContext(ctx context.Context) (*cmdContext, bool) {
	value, ok := ctx.Value(cmdContextKey{}).(*cmdContext)
	if ok {
		return value, true
	}

	return nil, false
}

// having a bit of fun with these. cc @vito @jedevc
var skipLoggedOutTraceMsgEnvs = []string{"NOTHANKS", "SHUTUP", "GOAWAY", "STOPIT"}

// Keep this to one line, and 80 characters max (longest env var name is NOTHANKS)
//
//nolint:gosec
var loggedOutTraceMsg = fmt.Sprintf("Setup tracing at %%s. To hide: export %s=1",
	skipLoggedOutTraceMsgEnvs[rand.Intn(len(skipLoggedOutTraceMsgEnvs))])

type Frontend interface {
	// Run starts a frontend, and runs the target function.
	Run(ctx context.Context, opts dagui.FrontendOpts, f func(context.Context) error) error

	// SetPrimary tells the frontend which span should be treated like the focal
	// point of the command. Its output will be displayed at the end, and its
	// children will be promoted to the "top-level" of the TUI.
	SetPrimary(spanID dagui.SpanID)
	Background(cmd tea.ExecCommand) error
	// RevealAllSpans tells the frontend to show all spans, not just
	// the spans beneath the primary span.
	RevealAllSpans()

	// Can consume otel spans and logs.
	SpanExporter() sdktrace.SpanExporter
	LogExporter() sdklog.Exporter

	// ConnectedToEngine is called when the CLI connects to an engine.
	ConnectedToEngine(ctx context.Context, name string, version string, clientID string)
	// SetCloudURL is called after the CLI checks auth and sets the cloud URL.
	SetCloudURL(ctx context.Context, url string, msg string, logged bool)
}

type Dump struct {
	Newline string
	Prefix  string
}

func (d *Dump) DumpID(out *termenv.Output, id *call.ID) error {
	if id.Receiver() != nil {
		if err := d.DumpID(out, id.Receiver()); err != nil {
			return err
		}
	}
	dag, err := id.ToProto()
	if err != nil {
		return err
	}

	db := dagui.NewDB()
	for dig, call := range dag.CallsByDigest {
		db.Calls[dig] = call
	}
	r := newRenderer(db, -1, dagui.FrontendOpts{})
	if d.Newline != "" {
		r.newline = d.Newline
	}
	err = r.renderCall(out, nil, id.Call(), d.Prefix, false, 0, false, false, false)
	fmt.Fprint(out, r.newline)
	return err
}

type renderer struct {
	dagui.FrontendOpts

	now           time.Time
	newline       string
	db            *dagui.DB
	maxLiteralLen int
	rendering     map[string]bool
}

func newRenderer(db *dagui.DB, maxLiteralLen int, fe dagui.FrontendOpts) *renderer {
	return &renderer{
		FrontendOpts:  fe,
		now:           time.Now(),
		db:            db,
		maxLiteralLen: maxLiteralLen,
		rendering:     map[string]bool{},
		newline:       "\n",
	}
}

const (
	kwColor     = termenv.ANSICyan
	faintColor  = termenv.ANSIBrightBlack
	moduleColor = termenv.ANSIMagenta
)

func (r *renderer) indent(out *termenv.Output, depth int) {
	fmt.Fprint(out, out.String(strings.Repeat(VertBar+" ", depth)).
		Foreground(termenv.ANSIBrightBlack).
		Faint())
}

func (r *renderer) renderIDBase(out *termenv.Output, call *callpbv1.Call) {
	typeName := call.Type.ToAST().Name()
	parent := out.String(typeName)
	if call.Module != nil {
		parent = parent.Foreground(moduleColor)
	}
	fmt.Fprint(out, parent.String())
	if r.Verbosity > dagui.ShowDigestsVerbosity && call.ReceiverDigest != "" {
		fmt.Fprint(out, out.String(fmt.Sprintf("@%s", call.ReceiverDigest)).Foreground(faintColor))
	}
}

func (r *renderer) renderCall(
	out *termenv.Output,
	span *dagui.Span,
	call *callpbv1.Call,
	prefix string,
	chained bool,
	depth int,
	inline bool,
	internal bool,
	focused bool,
) error {
	if r.rendering[call.Digest] {
		slog.Warn("cycle detected while rendering call", "span", span.Name, "call", call.String())
		return nil
	}
	r.rendering[call.Digest] = true
	defer func() { delete(r.rendering, call.Digest) }()

	if !inline {
		fmt.Fprint(out, prefix)
		r.indent(out, depth)
	}

	if span != nil {
		r.renderStatus(out, span, focused)
	}

	if call.ReceiverDigest != "" {
		if !chained {
			r.renderIDBase(out, r.db.MustCall(call.ReceiverDigest))
		}
		fmt.Fprint(out, ".")
	}

	fmt.Fprint(out, out.String(call.Field).Bold())

	if len(call.Args) > 0 {
		fmt.Fprint(out, "(")
		var needIndent bool
		for _, arg := range call.Args {
			if arg.GetValue().GetCallDigest() != "" {
				needIndent = true
				break
			}
		}
		if needIndent {
			fmt.Fprint(out, r.newline)
			depth++
			depth++
			for _, arg := range call.Args {
				fmt.Fprint(out, prefix)
				r.indent(out, depth)
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String(), arg.GetName())
				val := arg.GetValue()
				fmt.Fprint(out, " ")
				if argDig := val.GetCallDigest(); argDig != "" {
					forceSimplify := false
					internal := internal
					argSpan := r.db.MostInterestingSpan(argDig)
					if argSpan != nil {
						forceSimplify = argSpan.Internal && !internal // only for the first internal call (not it's children)
						internal = internal || argSpan.Internal
						if span == nil {
							argSpan = nil
						}
					}
					argCall := r.db.Simplify(r.db.MustCall(argDig), forceSimplify)
					if err := r.renderCall(out, argSpan, argCall, prefix, false, depth-1, true, internal, false); err != nil {
						return err
					}
				} else {
					r.renderLiteral(out, arg.GetValue())
				}
				fmt.Fprint(out, r.newline)
			}
			depth--
			fmt.Fprint(out, prefix)
			r.indent(out, depth)
			depth-- //nolint:ineffassign
		} else {
			for i, arg := range call.Args {
				if i > 0 {
					fmt.Fprint(out, ", ")
				}
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String()+" ", arg.GetName())
				r.renderLiteral(out, arg.GetValue())
			}
		}
		fmt.Fprint(out, ")")
	}

	if call.Type != nil {
		typeStr := out.String(": " + call.Type.ToAST().String()).Faint()
		fmt.Fprint(out, typeStr)
	}

	if r.Verbosity > dagui.ShowDigestsVerbosity {
		fmt.Fprint(out, out.String(fmt.Sprintf(" = %s", call.Digest)).Foreground(faintColor))
	}

	if span != nil {
		r.renderDuration(out, span)
		r.renderCached(out, span)
	}

	return nil
}

func (r *renderer) renderSpan(
	out *termenv.Output,
	span *dagui.Span,
	name string,
	prefix string,
	depth int,
	focused bool,
) error {
	fmt.Fprint(out, prefix)
	r.indent(out, depth)

	style := lipgloss.NewStyle()
	if span != nil {
		r.renderStatus(out, span, focused)
		if len(span.Links) > 0 {
			style = style.Italic(true)
		}
	}
	fmt.Fprint(out, style.Render(name))

	if span != nil {
		// TODO: when a span has child spans that have progress, do 2-d progress
		// fe.renderVertexTasks(out, span, depth)
		r.renderDuration(out, span)
		r.renderCached(out, span)
	}

	return nil
}

func (r *renderer) renderLiteral(out *termenv.Output, lit *callpbv1.Literal) {
	switch val := lit.GetValue().(type) {
	case *callpbv1.Literal_Bool:
		fmt.Fprint(out, out.String(fmt.Sprintf("%v", val.Bool)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_Int:
		fmt.Fprint(out, out.String(fmt.Sprintf("%d", val.Int)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_Float:
		fmt.Fprint(out, out.String(fmt.Sprintf("%f", val.Float)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_String_:
		if r.maxLiteralLen != -1 && len(val.Value()) > r.maxLiteralLen {
			display := string(digest.FromString(val.Value()))
			fmt.Fprint(out, out.String("ETOOBIG:"+display).Foreground(termenv.ANSIYellow))
			return
		}
		fmt.Fprint(out, out.String(fmt.Sprintf("%q", val.String_)).Foreground(termenv.ANSIYellow))
	case *callpbv1.Literal_CallDigest:
		fmt.Fprint(out, out.String(val.CallDigest).Foreground(termenv.ANSIMagenta))
	case *callpbv1.Literal_Enum:
		fmt.Fprint(out, out.String(val.Enum).Foreground(termenv.ANSIYellow))
	case *callpbv1.Literal_Null:
		fmt.Fprint(out, out.String("null").Foreground(termenv.ANSIBrightBlack))
	case *callpbv1.Literal_List:
		fmt.Fprint(out, "[")
		for i, item := range val.List.GetValues() {
			if i > 0 {
				fmt.Fprint(out, ", ")
			}
			r.renderLiteral(out, item)
		}
		fmt.Fprint(out, "]")
	case *callpbv1.Literal_Object:
		fmt.Fprint(out, "{")
		for i, item := range val.Object.GetValues() {
			if i > 0 {
				fmt.Fprint(out, ", ")
			}
			fmt.Fprintf(out, "%s: ", item.GetName())
			r.renderLiteral(out, item.GetValue())
		}
		fmt.Fprint(out, "}")
	}
}

func (r *renderer) renderStatus(out *termenv.Output, span *dagui.Span, focused bool) {
	var symbol string
	var color termenv.Color
	switch {
	case span.IsRunningOrLinksRunning():
		symbol = DotFilled
		color = termenv.ANSIYellow
	case span.IsCached():
		symbol = IconCached
		color = termenv.ANSIBlue
	case span.Canceled:
		symbol = IconSkipped
		color = termenv.ANSIBrightBlack
	case span.IsFailed():
		symbol = IconFailure
		color = termenv.ANSIRed
	case span.IsPending():
		symbol = DotEmpty
		color = termenv.ANSIBrightBlack
	default:
		symbol = IconSuccess
		color = termenv.ANSIGreen
	}

	style := out.String(symbol).Foreground(color)
	if focused {
		style = style.Reverse()
	}
	symbol = style.String()

	fmt.Fprintf(out, "%s ", symbol)

	if r.Debug {
		fmt.Fprintf(out, "%s ", out.String(span.ID.String()).Foreground(termenv.ANSIBrightBlack))
	}
}

func (r *renderer) renderDuration(out *termenv.Output, span *dagui.Span) {
	fmt.Fprint(out, " ")
	duration := out.String(dagui.FormatDuration(span.Activity.Duration(r.now)))
	if span.IsRunningOrLinksRunning() {
		duration = duration.Foreground(termenv.ANSIYellow)
	} else {
		duration = duration.Faint()
	}
	fmt.Fprint(out, duration)
}

func (r *renderer) renderCached(out *termenv.Output, span *dagui.Span) {
	if !span.IsRunningOrLinksRunning() && span.IsCached() {
		fmt.Fprintf(out, " %s", out.String("CACHED").
			Foreground(termenv.ANSIBlue))
	}
}

// var (
// 	progChars = []string{"⠀", "⡀", "⣀", "⣄", "⣤", "⣦", "⣶", "⣷", "⣿"}
// )

// func (r *renderer) renderVertexTasks(out *termenv.Output, span *Span, depth int) error {
// 	tasks := r.db.Tasks[span.SpanContext().SpanID()]
// 	if len(tasks) == 0 {
// 		return nil
// 	}
// 	var spaced bool
// 	for _, t := range tasks {
// 		var sym termenv.Style
// 		if t.Total != 0 {
// 			percent := int(100 * (float64(t.Current) / float64(t.Total)))
// 			idx := (len(progChars) - 1) * percent / 100
// 			chr := progChars[idx]
// 			sym = out.String(chr)
// 		} else {
// 			// TODO: don't bother printing non-progress-bar tasks for now
// 			// else if t.Completed != nil {
// 			// sym = out.String(IconSuccess)
// 			// } else if t.Started != nil {
// 			// sym = out.String(DotFilled)
// 			// }
// 			continue
// 		}
// 		if t.Completed.IsZero() {
// 			sym = sym.Foreground(termenv.ANSIYellow)
// 		} else {
// 			sym = sym.Foreground(termenv.ANSIGreen)
// 		}
// 		if !spaced {
// 			fmt.Fprint(out, " ")
// 			spaced = true
// 		}
// 		fmt.Fprint(out, sym)
// 	}
// 	return nil
// }

func renderPrimaryOutput(db *dagui.DB) error {
	logs := db.PrimaryLogs[db.PrimarySpan]
	if len(logs) == 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr)

	for _, l := range logs {
		data := l.Body().AsString()
		var stream int
		l.WalkAttributes(func(attr log.KeyValue) bool {
			if attr.Key == telemetry.StdioStreamAttr {
				stream = int(attr.Value.AsInt64())
				return false
			}
			return true
		})
		switch stream {
		case 1: // stdout
			if _, err := fmt.Fprint(os.Stdout, data); err != nil {
				return err
			}
		case 2: // stderr
			fallthrough
		default:
			if _, err := fmt.Fprint(os.Stderr, data); err != nil {
				return err
			}
		}
	}

	trailingLn := false
	if len(logs) > 0 {
		trailingLn = strings.HasSuffix(logs[len(logs)-1].Body().AsString(), "\n")
	}
	if !trailingLn && term.IsTerminal(int(os.Stdout.Fd())) {
		// NB: ensure there's a trailing newline if stdout is a TTY, so we don't
		// encourage module authors to add one of their own
		fmt.Fprintln(os.Stdout)
	}
	return nil
}

func skipLoggedOutTraceMsg() bool {
	for _, env := range skipLoggedOutTraceMsgEnvs {
		if os.Getenv(env) != "" {
			return true
		}
	}
	return false
}
