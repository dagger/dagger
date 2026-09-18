package dagql

import (
	"errors"
	"testing"

	"github.com/dagger/dagger/engine"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// A lazy attempt that ends with a part reselect is retried by the
// evaluation loop, so its span ends without error status and records the
// reselect as an event; a real failure keeps the error status. Both the
// resume span (producer context captured) and the plain lazy op.
func TestLazyOpSpanReselectIsNotAnError(t *testing.T) {
	t.Parallel()
	const sessionID = "sess-reselect"
	for _, tc := range []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "reselect refusal", err: partRefused("source check: candidates or demand changed"), wantErr: false},
		{name: "not ready", err: partRefusedBy("demand", ErrPersistStateNotReady), wantErr: false},
		{name: "wrapped reselect", err: errors.Join(partRefused("scan"), partRefused("decision")), wantErr: false},
		{name: "real failure", err: errors.New("exec failed"), wantErr: true},
		{name: "reselect joined with a real failure", err: errors.Join(partRefused("scan"), errors.New("cleanup failed")), wantErr: true},
	} {
		for _, resume := range []bool{true, false} {
			name := tc.name + "/plain"
			if resume {
				name = tc.name + "/resume"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				const resultID = sharedResultID(7)
				sr, rootCtx, root := newLazyRecordingRoot("POST /query")
				c := &Cache{sessionLazySpansBySession: map[string]map[sharedResultID]trace.SpanContext{}}
				if resume {
					_, producer := Tracer(rootCtx).Start(rootCtx, "Query.directory")
					producer.End()
					c.sessionLazySpansBySession[sessionID] = map[sharedResultID]trace.SpanContext{resultID: producer.SpanContext()}
				}
				evalCtx := engine.ContextWithClientMetadata(rootCtx, &engine.ClientMetadata{SessionID: sessionID, ClientID: "client-1"})
				_, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, resultID, LazyGroupWhole, &ResultCall{Field: "withMountedDirectory"}, "")
				if isResume != resume {
					t.Fatalf("isResume = %v, want %v", isResume, resume)
				}
				err := tc.err
				endOTelLazyOp(lazySpan, isResume, resultID, false, false, "", &err)
				root.End()
				exported := spanBySpanID(t, sr.Ended(), lazySpan.SpanContext().SpanID())
				if tc.wantErr {
					if exported.Status().Code != codes.Error {
						t.Fatalf("a real failure keeps the error status; got %v", exported.Status().Code)
					}
					return
				}
				if exported.Status().Code == codes.Error {
					t.Fatalf("a retried reselect must not end the span with error status; got description %q", exported.Status().Description)
				}
				var event bool
				for _, ev := range exported.Events() {
					if ev.Name != lazyReselectEvent {
						continue
					}
					for _, attr := range ev.Attributes {
						if string(attr.Key) == lazyReselectReasonAttr && attr.Value.AsString() == tc.err.Error() {
							event = true
						}
					}
				}
				if !event {
					t.Fatalf("the reselect is recorded as the %q event with its message; events: %+v", lazyReselectEvent, exported.Events())
				}
			})
		}
	}
}
