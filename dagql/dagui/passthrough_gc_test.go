package dagui

import (
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// TestAgedPassthroughKeepsFailedChildren covers a CLI command whose failing
// call is served under the engine's "POST /query" span. That span is
// passthrough and ends OK (GraphQL errors travel in the response body), so
// once it has ended longer ago than GCThreshold the age rule hides it. Hiding
// a span drops everything beneath it, so the failed call -- the row that
// names the requested module source -- vanished from the final report when
// it was rendered more than a second after the query span ended.
func TestAgedPassthroughKeepsFailedChildren(t *testing.T) {
	const source = "https://github.com/dagger/dagger#this-version-does-not-exist:modules/wolfi"

	traceID := TraceID{TraceID: trace.TraceID{1}}
	primaryID := testID(1)
	queryID := testID(2)
	withModuleID := testID(3)
	refID := testID(4)
	configFileID := testID(5)
	otherParentID := testID(6)
	otherChildID := testID(7)

	// Well past the 1s threshold, so the age rule applies without sleeping.
	start := time.Now().Add(-time.Hour)
	end := start.Add(time.Second)
	ok := sdktrace.Status{Code: codes.Ok}
	failed := func(msg string) sdktrace.Status {
		return sdktrace.Status{Code: codes.Error, Description: msg}
	}
	origin := []SpanLink{{
		SpanContext: SpanContext{TraceID: traceID, SpanID: refID},
		Purpose:     telemetry.LinkPurposeErrorOrigin,
	}}
	wrapped := `load module source: resolve remote module "` + source + `": failed to resolve git src: repository does not contain ref "this-version-does-not-exist"`

	db := NewDB()
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
	db.Calls["xxh3:configFile"] = &callpbv1.Call{
		ReceiverDigest: "xxh3:currentWorkspace",
		Field:          "configFile",
		Type:           &callpbv1.Type{NamedType: "String"},
	}
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID: primaryID, TraceID: traceID, Name: "dagger mod install " + source,
			StartTime: start, EndTime: end.Add(3 * time.Second),
			Status: failed(wrapped), Links: origin,
		},
		{
			ID: queryID, TraceID: traceID, ParentID: primaryID, Name: "POST /query",
			StartTime: start, EndTime: end, Status: ok, Passthrough: true,
		},
		{
			ID: withModuleID, TraceID: traceID, ParentID: queryID, Name: "Workspace.withModule",
			StartTime: start, EndTime: end, CallDigest: "xxh3:withModule",
			Status: failed(wrapped), Links: origin,
		},
		{
			ID: refID, TraceID: traceID, ParentID: withModuleID, Name: "GitRepository.ref",
			StartTime: start, EndTime: end,
			Status: failed(`repository does not contain ref "this-version-does-not-exist"`),
		},
		{
			// A successful call under the same passthrough span.
			ID: configFileID, TraceID: traceID, ParentID: queryID, Name: "Workspace.configFile",
			StartTime: start, EndTime: end, CallDigest: "xxh3:configFile", Status: ok,
		},
		{
			// An ordinary (not passthrough) successful parent and child.
			ID: otherParentID, TraceID: traceID, ParentID: primaryID, Name: "connect",
			StartTime: start, EndTime: end, Status: ok,
		},
		{
			ID: otherChildID, TraceID: traceID, ParentID: otherParentID, Name: "starting engine",
			StartTime: start, EndTime: end, Status: ok,
		},
	})
	db.SetPrimarySpan(primaryID)

	view := db.RowsView(FrontendOpts{
		ZoomedSpan:  primaryID,
		Verbosity:   HideCompletedVerbosity,
		GCThreshold: time.Second,
	})
	if view.BySpan[withModuleID] == nil {
		t.Fatal("failed Workspace.withModule beneath an aged passthrough span was dropped from the view")
	}
	if view.BySpan[queryID] != nil {
		t.Error("passthrough span rendered as its own row")
	}
	if view.BySpan[configFileID] != nil {
		t.Error("aged successful child of the passthrough span is shown; it should still age out")
	}
	if view.BySpan[otherParentID] != nil || view.BySpan[otherChildID] != nil {
		t.Error("aged ordinary successful parent or its child is shown; it should still age out")
	}
}
