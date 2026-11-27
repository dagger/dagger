package idtui_test

import (
	"strings"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/dagql/dagui"
)

// Assertions for `dagger check` examples, on what a golden can't show: where
// a check's error, output and spans landed in the exported telemetry.
//
// A check's function runs under a hidden Type.__check_<fn> span no frontend
// renders. What the function does belongs to the check's span: an error of
// its own has the check as its origin, and its output and SDK spans are the
// check's (trace f63854c7: a Python check's traceback and origin were on
// DaggerverseCore.__check_allTs).

// checkAddress is the dag:// address of the check named name.
func checkAddress(name string) string {
	return "dag://?check=" + name
}

// checkSpans returns the spans of the check named name.
func checkSpans(db *dagui.DB, name string) []*dagui.Span {
	var spans []*dagui.Span
	for _, span := range db.Spans.Order {
		if span.CheckName == checkAddress(name) {
			spans = append(spans, span)
		}
	}
	return spans
}

// isCheckRun reports whether span is the hidden Type.__check_<fn> span a
// check's function runs under.
func isCheckRun(span *dagui.Span) bool {
	return span != nil && strings.Contains(span.Name, ".__check_")
}

// errorOriginLinks returns the spans span links as its error's origins.
func errorOriginLinks(db *dagui.DB, span *dagui.Span) []*dagui.Span {
	var origins []*dagui.Span
	for _, link := range span.Links {
		if link.Purpose != telemetry.LinkPurposeErrorOrigin {
			continue
		}
		if target := db.Spans.Map[link.SpanContext.SpanID]; target != nil {
			origins = append(origins, target)
		}
	}
	return origins
}

// assertCheckIsOwnOrigin asserts that the check named name failed with an
// error its function raised, and is that error's origin: it has no origin
// frontends show, the check's run span least of all.
func assertCheckIsOwnOrigin(t *testctx.T, db *dagui.DB, name string) {
	checks := checkSpans(db, name)
	require.NotEmpty(t, checks, "the check %s must have a span", name)
	for _, check := range checks {
		require.True(t, check.IsFailed(), "the check %s must fail", name)
		for _, origin := range errorOriginLinks(db, check) {
			require.False(t, isCheckRun(origin), "the check %s must not link its run span %s as its error's origin", name, origin.Name)
		}
		// One naming the check itself is not recorded.
		require.Empty(t, spanNames(check.ErrorOrigins.Spans()), "the check %s must be its error's origin", name)
	}
}

// assertCheckOriginIsExec asserts that the check named name failed on an
// exec of cmd, which stays its error's only origin.
func assertCheckOriginIsExec(t *testctx.T, db *dagui.DB, name, cmd string) {
	checks := checkSpans(db, name)
	require.NotEmpty(t, checks, "the check %s must have a span", name)
	for _, check := range checks {
		require.True(t, check.IsFailed(), "the check %s must fail", name)
		origins := check.ErrorOrigins.Spans()
		require.Len(t, origins, 1, "the check %s's origins: %v", name, spanNames(origins))
		require.True(t, spanHasStringArg(origins[0], cmd), "the check %s's origin must be its exec, not %s", name, origins[0].Name)
	}
}

// assertNotUnderCheckRun asserts that the spans named names, which a check's
// function emits, exist and have no check run span among their ancestors:
// the SDK parents them to the check's span.
func assertNotUnderCheckRun(t *testctx.T, db *dagui.DB, names ...string) {
	for _, name := range names {
		var found bool
		for _, span := range db.Spans.Order {
			if span.Name != name {
				continue
			}
			found = true
			for parent := span.ParentSpan; parent != nil; parent = parent.ParentSpan {
				require.False(t, isCheckRun(parent), "the span %s must not be under the check's run span %s", name, parent.Name)
			}
		}
		require.True(t, found, "the function must emit the span %s", name)
	}
}

// assertLoggedOnCheck asserts that the log records holding text are on the
// span of the check named name: the function's output is the check's.
func assertLoggedOnCheck(t *testctx.T, db *dagui.DB, logs []sdklog.Record, name, text string) {
	var found int
	for _, rec := range logs {
		body, _ := dagui.LogBodyString(rec)
		if !strings.Contains(body, text) {
			continue
		}
		span := db.Spans.Map[db.LogTargetSpanID(rec)]
		require.NotNil(t, span, "the log %q must be on a span", text)
		require.Equal(t, checkAddress(name), span.CheckName, "the log %q must be on the check %s, not on %s", text, name, span.Name)
		found++
	}
	require.NotZero(t, found, "the check %s must log %q", name, text)
}

func spanNames(spans []*dagui.Span) []string {
	var names []string
	for _, span := range spans {
		names = append(names, span.Name)
	}
	return names
}

func spanHasStringArg(span *dagui.Span, want string) bool {
	call := span.Call()
	if call == nil {
		return false
	}
	for _, arg := range call.Args {
		if arg.GetValue().GetString_() == want {
			return true
		}
		for _, value := range arg.GetValue().GetList().GetValues() {
			if value.GetString_() == want {
				return true
			}
		}
	}
	return false
}
