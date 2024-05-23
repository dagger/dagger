package idtui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock/ui"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/telemetry"
	"github.com/dagger/dagger/telemetry/sdklog"
)

type FrontendOpts struct {
	// Debug tells the frontend to show everything and do one big final render.
	Debug bool

	// Silent tells the frontend to not display progress at all.
	Silent bool

	// Verbosity is the level of detail to show in the TUI.
	Verbosity int
}

type Frontend interface {
	// Run starts a frontend, and runs the target function.
	Run(ctx context.Context, opts FrontendOpts, f func(context.Context) error) error

	// SetPrimary tells the frontend which span should be treated like the focal
	// point of the command. Its output will be displayed at the end, and its
	// children will be promoted to the "top-level" of the TUI.
	SetPrimary(spanID trace.SpanID)
	Background(cmd tea.ExecCommand) error

	// Can consume otel spans and logs.
	sdktrace.SpanExporter
	sdklog.LogExporter

	// ConnectedToEngine is called when the CLI connects to an engine.
	ConnectedToEngine(name string, version string)
	// ConnectedToCloud is called when the CLI has started emitting events to The Cloud.
	ConnectedToCloud(url string)
}

// DumpID is exposed for troubleshooting.
func DumpID(out *termenv.Output, id *call.ID) error {
	if id.Base() != nil {
		if err := DumpID(out, id.Base()); err != nil {
			return err
		}
	}
	dag, err := id.ToProto()
	if err != nil {
		return err
	}

	db := NewDB()
	for dig, call := range dag.CallsByDigest {
		db.Calls[dig] = call
	}
	r := renderer{
		db:    db,
		width: -1,
	}
	return r.renderCall(out, nil, id.Call(), "", 0, false)
}

type renderer struct {
	db *DB

	width int
}

const (
	kwColor     = termenv.ANSICyan
	parentColor = termenv.ANSIWhite
	moduleColor = termenv.ANSIMagenta
)

func (r renderer) indent(out io.Writer, depth int) {
	fmt.Fprint(out, strings.Repeat("  ", depth))
}

func (r renderer) renderIDBase(out *termenv.Output, call *callpbv1.Call) error {
	typeName := call.Type.ToAST().Name()
	parent := out.String(typeName)
	if call.Module != nil {
		parent = parent.Foreground(moduleColor)
	}
	fmt.Fprint(out, parent.String())
	return nil
}

func (r renderer) renderCall(out *termenv.Output, span *Span, id *callpbv1.Call, prefix string, depth int, inline bool) error {
	if !inline {
		fmt.Fprint(out, prefix)
		r.indent(out, depth)
	}

	if span != nil {
		r.renderStatus(out, span)
	}

	if id.ReceiverDigest != "" {
		if err := r.renderIDBase(out, r.db.MustCall(id.ReceiverDigest)); err != nil {
			return err
		}
		fmt.Fprint(out, ".")
	}

	fmt.Fprint(out, out.String(id.Field).Bold())

	if len(id.Args) > 0 {
		fmt.Fprint(out, "(")
		var needIndent bool
		for _, arg := range id.Args {
			if arg.GetValue().GetCallDigest() != "" {
				needIndent = true
				break
			}
		}
		if needIndent {
			fmt.Fprintln(out)
			depth++
			depth++
			for _, arg := range id.Args {
				fmt.Fprint(out, prefix)
				r.indent(out, depth)
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String(), arg.GetName())
				val := arg.GetValue()
				fmt.Fprint(out, " ")
				if argDig := val.GetCallDigest(); argDig != "" {
					argCall := r.db.Simplify(r.db.MustCall(argDig))
					var argSpan *Span
					if span != nil {
						argSpan = r.db.MostInterestingSpan(argDig)
					}
					if err := r.renderCall(out, argSpan, argCall, prefix, depth-1, true); err != nil {
						return err
					}
				} else {
					r.renderLiteral(out, arg.GetValue())
				}
				fmt.Fprintln(out)
			}
			depth--
			fmt.Fprint(out, prefix)
			r.indent(out, depth)
			depth-- //nolint:ineffassign
		} else {
			for i, arg := range id.Args {
				if i > 0 {
					fmt.Fprint(out, ", ")
				}
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String()+" ", arg.GetName())
				r.renderLiteral(out, arg.GetValue())
			}
		}
		fmt.Fprint(out, ")")
	}

	typeStr := out.String(": " + id.Type.ToAST().String()).Faint()
	fmt.Fprint(out, typeStr)

	if span != nil {
		r.renderDuration(out, span)
	}

	return nil
}

func (r renderer) renderVertex(out *termenv.Output, span *Span, name string, prefix string, depth int) error {
	fmt.Fprint(out, prefix)
	r.indent(out, depth)

	if span != nil {
		r.renderStatus(out, span)
	}

	fmt.Fprint(out, name)

	if span != nil {
		// TODO: when a span has child spans that have progress, do 2-d progress
		// fe.renderVertexTasks(out, span, depth)
		r.renderDuration(out, span)
	}

	return nil
}

func (r renderer) renderLiteral(out *termenv.Output, lit *callpbv1.Literal) {
	switch val := lit.GetValue().(type) {
	case *callpbv1.Literal_Bool:
		fmt.Fprint(out, out.String(fmt.Sprintf("%v", val.Bool)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_Int:
		fmt.Fprint(out, out.String(fmt.Sprintf("%d", val.Int)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_Float:
		fmt.Fprint(out, out.String(fmt.Sprintf("%f", val.Float)).Foreground(termenv.ANSIRed))
	case *callpbv1.Literal_String_:
		if r.width != -1 && len(val.Value()) > r.width {
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

func (r renderer) renderStatus(out *termenv.Output, span *Span) {
	var symbol string
	var color termenv.Color
	switch {
	case span.IsRunning():
		symbol = ui.DotFilled
		color = termenv.ANSIYellow
	case span.Canceled:
		symbol = ui.IconSkipped
		color = termenv.ANSIBrightBlack
	case span.Status().Code == codes.Error:
		symbol = ui.IconFailure
		color = termenv.ANSIRed
	default:
		symbol = ui.IconSuccess
		color = termenv.ANSIGreen
	}

	symbol = out.String(symbol).Foreground(color).String()

	fmt.Fprintf(out, "%s ", symbol)
}

func (r renderer) renderDuration(out *termenv.Output, span *Span) {
	fmt.Fprint(out, " ")
	duration := out.String(fmtDuration(span.Duration()))
	if span.IsRunning() {
		duration = duration.Foreground(termenv.ANSIYellow)
	} else {
		duration = duration.Faint()
	}
	fmt.Fprint(out, duration)
}

// var (
// 	progChars = []string{"⠀", "⡀", "⣀", "⣄", "⣤", "⣦", "⣶", "⣷", "⣿"}
// )

// func (r renderer) renderVertexTasks(out *termenv.Output, span *Span, depth int) error {
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
// 			// sym = out.String(ui.IconSuccess)
// 			// } else if t.Started != nil {
// 			// sym = out.String(ui.DotFilled)
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

type spanFilter struct {
	gcThreshold      time.Duration
	tooFastThreshold time.Duration
}

func (sf spanFilter) shouldShow(opts FrontendOpts, row *TraceRow) bool {
	span := row.Span
	if span.IsInternal() && opts.Verbosity < 2 {
		// internal steps are hidden by default
		return false
	}
	if row.Parent != nil && (span.Encapsulated || row.Parent.Span.Encapsulate) && row.Parent.Span.Err() == nil && opts.Verbosity < 2 {
		// encapsulated steps are hidden (even on error) unless their parent errors
		return false
	}
	if span.Err() != nil {
		// show errors
		return true
	}
	if sf.tooFastThreshold > 0 && span.Duration() < sf.tooFastThreshold && opts.Verbosity < 3 {
		// ignore fast steps; signal:noise is too poor
		return false
	}
	if row.IsRunning {
		// show running steps
		return true
	}
	if sf.gcThreshold > 0 && time.Since(span.EndTime()) > sf.gcThreshold && opts.Verbosity < 1 {
		// stop showing steps that ended after a given threshold
		return false
	}
	return true
}

func renderPrimaryOutput(db *DB) error {
	logs := db.PrimaryLogs[db.PrimarySpan]
	if len(logs) == 0 {
		return nil
	}

	for _, l := range logs {
		data := l.Body().AsString()
		var stream int
		l.WalkAttributes(func(attr log.KeyValue) bool {
			if attr.Key == telemetry.LogStreamAttr {
				stream = int(attr.Value.AsInt64())
				return false
			}
			return true
		})
		switch stream {
		case 1:
			if _, err := fmt.Fprint(os.Stdout, data); err != nil {
				return err
			}
		case 2:
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
