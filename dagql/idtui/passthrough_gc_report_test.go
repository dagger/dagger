package idtui

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
)

// TestReportNamesSourceAfterAgedPassthrough covers `dagger mod install` of a
// missing remote ref, reported more than GCThreshold after the engine's
// "POST /query" span ended, from a CLI running under a propagated trace (CI,
// a test, an SDK). Its primary span has a parent the CLI never receives, so
// there is no root span and no trace header naming the command. The final
// output must still name the requested source: either the failed
// Workspace.withModule row carries it, or the CLI prints the command's error.
// Before the fix the aged passthrough span took the row with it, the leaf
// origin alone was rendered, and that claim suppressed the error -- leaving
// only "repository does not contain ref".
func TestReportNamesSourceAfterAgedPassthrough(t *testing.T) {
	const source = "https://github.com/dagger/dagger#this-version-does-not-exist:modules/wolfi"
	const leafErr = `repository does not contain ref "this-version-does-not-exist"`
	wrapped := `load module source: resolve remote module "` + source + `": failed to resolve git src: ` + leafErr

	id := prettyTestSpanID
	traceID := prettyTestTraceID()
	primaryID, queryID, withModuleID, refID := id(1), id(2), id(3), id(4)
	// The span the CLI's TRACEPARENT points at; never exported to the CLI.
	callerID := id(9)

	// Well past the 1s threshold, so the age rule applies without sleeping.
	start := time.Now().Add(-time.Hour)
	end := start.Add(time.Second)
	failed := func(msg string) sdktrace.Status {
		return sdktrace.Status{Code: codes.Error, Description: msg}
	}
	origin := []dagui.SpanLink{{
		SpanContext: dagui.SpanContext{TraceID: traceID, SpanID: refID},
		Purpose:     telemetry.LinkPurposeErrorOrigin,
	}}

	db := dagui.NewDB()
	db.Calls["xxh3:currentWorkspace"] = &callpbv1.Call{
		Field: "currentWorkspace",
		Type:  &callpbv1.Type{NamedType: "Workspace"},
	}
	db.Calls["xxh3:withModule"] = &callpbv1.Call{
		ReceiverDigest: "xxh3:currentWorkspace",
		Field:          "withModule",
		Type:           &callpbv1.Type{NamedType: "Workspace"},
		Args: []*callpbv1.Argument{{
			Name:  "ref",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: source}},
		}},
	}
	db.Calls["xxh3:ref"] = &callpbv1.Call{
		Field: "ref",
		Type:  &callpbv1.Type{NamedType: "GitRef"},
		Args: []*callpbv1.Argument{{
			Name:  "name",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "this-version-does-not-exist"}},
		}},
	}
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID: primaryID, TraceID: traceID, ParentID: callerID, Name: "dagger mod install " + source,
			StartTime: start, EndTime: end.Add(3 * time.Second),
			Status: failed(wrapped), Links: origin,
		},
		{
			ID: queryID, TraceID: traceID, ParentID: primaryID, Name: "POST /query",
			StartTime: start, EndTime: end,
			Status: sdktrace.Status{Code: codes.Ok}, Passthrough: true,
		},
		{
			ID: withModuleID, TraceID: traceID, ParentID: queryID, Name: "Workspace.withModule",
			StartTime: start, EndTime: end, CallDigest: "xxh3:withModule",
			Status: failed(wrapped), Links: origin,
		},
		{
			ID: refID, TraceID: traceID, ParentID: withModuleID, Name: "GitRepository.ref",
			StartTime: start, EndTime: end, CallDigest: "xxh3:ref",
			Status: failed(leafErr),
		},
	})
	db.SetPrimarySpan(primaryID)

	fe := NewASCIIReporterWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.HideCompletedVerbosity
	// The CLI's default, which Run would set.
	fe.FrontendOpts.GCThreshold = time.Second
	// The error the CLI got back, with the engine's origin marker.
	fe.err = telemetry.TrackOrigin(errors.New(wrapped), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID.TraceID,
		SpanID:  refID.SpanID,
	}))

	var out bytes.Buffer
	renderErr := fe.FinalRender(&out)
	seen := out.String()
	var exit ExitError
	if !errors.As(renderErr, &exit) {
		require.NoError(t, renderErr)
		// FinalRender didn't claim the error, so the CLI prints it.
		seen += fe.err.Error()
	}
	require.Contains(t, seen, source, "final output does not name the requested source")
}
