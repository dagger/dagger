package idtui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"

	callpbv1 "github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
)

func widthTestCall(db *dagui.DB, digest, field string, args ...*callpbv1.Argument) *callpbv1.Call {
	call := &callpbv1.Call{
		Digest: digest, Field: field, Args: args,
		Type: &callpbv1.Type{NamedType: "Directory"},
	}
	db.Calls[digest] = call
	return call
}

func widthTestString(name, value string) *callpbv1.Argument {
	return &callpbv1.Argument{Name: name, Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: value}}}
}

func widthTestRef(name string, call *callpbv1.Call) *callpbv1.Argument {
	return &callpbv1.Argument{Name: name, Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: call.Digest}}}
}

// Use ordinary inline rendering as the oracle, without a width probe or a
// viewport. Comparing colored output also ensures escape sequences add no cells.
func widthTestInline(t *testing.T, r *renderer, span *dagui.Span, call *callpbv1.Call, chained, abridged bool, profile termenv.Profile) string {
	t.Helper()
	inline := newRenderer(r.db, -1, r.FrontendOpts, r.final)
	inline.compactIDs = true
	inline.omitNulls = r.omitNulls
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(profile))
	require.NoError(t, inline.renderCall(out, span, call, "", chained, 0, false, nil, abridged))
	return buf.String()
}

func TestCallWidthMatchesInline(t *testing.T) {
	db := dagui.NewDB()
	base := widthTestCall(db, "base", "directory")
	base.Type.NamedType = "Base"
	otherBase := widthTestCall(db, "other-base", "directory")
	otherBase.Type.NamedType = "MuchLongerBase"
	spanCall := widthTestCall(db, "span-call", "custom")
	spanCall.ReceiverDigest = otherBase.Digest
	db.ImportSnapshots([]dagui.SpanSnapshot{{ID: prettyTestSpanID(1), CallDigest: spanCall.Digest}})
	span := db.Spans.Map[prettyTestSpanID(1)]
	generic := widthTestCall(db, "generic", "custom界",
		widthTestString("unicode", "界 e\u0301 👩‍💻 🇫🇷"),
		&callpbv1.Argument{Name: "unset", Value: &callpbv1.Literal{Value: &callpbv1.Literal_Null{Null: true}}},
	)
	generic.ReceiverDigest = base.Digest
	custom := widthTestCall(db, "custom", "withWorkdir", widthTestString("path", "界/e\u0301/👩‍💻"))
	for _, verbosity := range []int{0, dagui.ShowDigestsVerbosity, dagui.ShowDigestsVerbosity + 1} {
		for _, omitNulls := range []bool{false, true} {
			for _, limit := range []int{1, 40, 10000} {
				t.Run(fmt.Sprintf("verbosity=%d/omitNulls=%t/limit=%d", verbosity, omitNulls, limit), func(t *testing.T) {
					r := newRenderer(db, -1, dagui.FrontendOpts{Verbosity: verbosity}, true)
					r.compactIDs, r.omitNulls, r.maxWidth = true, omitNulls, limit
					// Reuse the same probe across all key variants and revisit them:
					// span, chained, and abridged must not alias in the memo table.
					for range 2 {
						for _, call := range []*callpbv1.Call{generic, custom} {
							for _, span := range []*dagui.Span{nil, {}, span} {
								for _, chained := range []bool{false, true} {
									for _, abridged := range []bool{false, true} {
										plain := widthTestInline(t, r, span, call, chained, abridged, termenv.Ascii)
										colored := widthTestInline(t, r, span, call, chained, abridged, termenv.ANSI)
										require.Equal(t, plain, ansi.Strip(colored))
										want := min(limit+1, ansi.StringWidth(plain))
										got := r.compactRenderedLen(span, call, "unused prefix", chained, 7, true, &dagui.TraceRow{}, abridged)
										require.Equal(t, want, got, "call=%s span=%p chained=%t abridged=%t inline=%q", call.Field, span, chained, abridged, plain)
									}
								}
							}
						}
					}
				})
			}
		}
	}
}

func TestCallWidthWrapBoundary(t *testing.T) {
	db := dagui.NewDB()
	leaf := widthTestCall(db, "leaf", "leaf", widthTestString("text", "界 e\u0301 👩‍💻"))
	root := widthTestCall(db, "root", "root", widthTestRef("source", leaf))
	for _, profile := range []termenv.Profile{termenv.Ascii, termenv.ANSI} {
		for _, offset := range []int{0, 11} {
			for _, delta := range []int{-1, 0, 1} {
				r := newRenderer(db, -1, dagui.FrontendOpts{}, true)
				r.enableCallSimplification(10000)
				inline := widthTestInline(t, r, nil, root, false, false, profile)
				r.maxWidth = offset + ansi.StringWidth(inline) + delta
				r.widthOffset = offset
				var buf strings.Builder
				out := termenv.NewOutput(&buf, termenv.WithProfile(profile))
				require.NoError(t, r.renderCall(out, nil, root, "", false, 0, false, nil, false))
				if delta < 0 {
					require.Contains(t, buf.String(), "\n")
				} else {
					require.Equal(t, inline, buf.String())
				}
				require.Nil(t, r.widthProbe, "root render must release measurements")
			}
		}
	}
}

func TestCallWidthSharedAndCyclicGraphs(t *testing.T) {
	db := dagui.NewDB()
	leaf := widthTestCall(db, "leaf", "leaf", widthTestString("value", "shared"))
	shared := widthTestCall(db, "shared", "shared", widthTestRef("a", leaf), widthTestRef("b", leaf))
	root := widthTestCall(db, "root", "root", widthTestRef("a", shared), widthTestRef("b", shared))
	self := widthTestCall(db, "self", "self")
	self.Args = []*callpbv1.Argument{widthTestRef("self", self)}
	a := widthTestCall(db, "cycle-a", "a")
	b := widthTestCall(db, "cycle-b", "longerB", widthTestRef("a", a), widthTestRef("shared", shared))
	a.Args = []*callpbv1.Argument{widthTestRef("b", b)}
	cycleRoot := widthTestCall(db, "cycle-root", "cycleRoot", widthTestRef("a", a), widthTestRef("b", b))
	for _, limit := range []int{12, 10000} {
		r := newRenderer(db, -1, dagui.FrontendOpts{}, true)
		r.enableCallSimplification(limit)
		// Cyclic calls are visited as both ancestors and descendants, and then
		// measured on their own with the same memo table. Cycle marker widths
		// depend on the ancestors and must never leak into another context.
		for _, call := range []*callpbv1.Call{root, shared, self, cycleRoot, b, a, cycleRoot, root} {
			inline := widthTestInline(t, r, nil, call, false, false, termenv.ANSI)
			want := min(limit+1, ansi.StringWidth(inline))
			require.Equal(t, want, r.compactRenderedLen(nil, call, "", false, 0, false, nil, false), "call=%s inline=%q", call.Digest, inline)
		}
	}
}

func TestCallWidthRefreshesBetweenRootRenders(t *testing.T) {
	for _, change := range []string{"call contents", "simplification"} {
		t.Run(change, func(t *testing.T) {
			db := dagui.NewDB()
			leaf := widthTestCall(db, "leaf", "leaf", widthTestString("value", "short"))
			root := widthTestCall(db, "root", "root", widthTestRef("source", leaf))
			r := newRenderer(db, -1, dagui.FrontendOpts{}, true)
			r.enableCallSimplification(80)
			render := func() string {
				var buf strings.Builder
				out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
				require.NoError(t, r.renderCall(out, nil, root, "", false, 0, false, nil, false))
				require.Nil(t, r.widthProbe)
				return buf.String()
			}
			require.NotContains(t, render(), "\n")
			if change == "call contents" {
				leaf.Args[0] = widthTestString("value", strings.Repeat("long", 50))
			} else {
				other := widthTestCall(db, "replacement", "replacement", widthTestString("value", strings.Repeat("long", 50)))
				db.CreatorSpans[leaf.Digest] = dagui.NewSpanSet()
				db.CreatorSpans[leaf.Digest].Add(&dagui.Span{SpanSnapshot: dagui.SpanSnapshot{CallDigest: other.Digest}})
			}
			require.Contains(t, render(), "\n", "updated telemetry must invalidate the earlier short width")
			leaf.Args[0] = widthTestString("value", "short")
			delete(db.CreatorSpans, leaf.Digest)
			require.NotContains(t, render(), "\n", "a saturated measurement must not survive the root render")
		})
	}
}
