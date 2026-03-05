package transform

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/internal/odag/store"
	"google.golang.org/protobuf/proto"
)

func TestProjectTraceBuildsMutableObjects(t *testing.T) {
	t.Parallel()

	spans := []store.SpanRecord{
		mustCallSpan(t, callSpanInput{
			traceID:      "trace1",
			spanID:       "s1",
			parentSpanID: "",
			name:         "Query.container",
			start:        1,
			end:          10,
			output:       "state-a",
			call: &callpbv1.Call{
				Digest: "call-1",
				Field:  "container",
				Type:   &callpbv1.Type{NamedType: "Container"},
				Args: []*callpbv1.Argument{
					{
						Name: "src",
						Value: &callpbv1.Literal{
							Value: &callpbv1.Literal_CallDigest{CallDigest: "seed-state"},
						},
					},
				},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace1",
			spanID:       "s2",
			parentSpanID: "s1",
			name:         "Container.from",
			start:        11,
			end:          20,
			output:       "state-b",
			call: &callpbv1.Call{
				Digest:         "call-2",
				ReceiverDigest: "state-a",
				Field:          "from",
				Type:           &callpbv1.Type{NamedType: "Container"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace1",
			spanID:       "s3",
			parentSpanID: "",
			name:         "Query.directory",
			start:        5,
			end:          15,
			output:       "state-c",
			call: &callpbv1.Call{
				Digest: "call-3",
				Field:  "directory",
				Type:   &callpbv1.Type{NamedType: "Directory"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace1",
			spanID:       "s4",
			parentSpanID: "s2",
			name:         "Container.withExec",
			start:        25,
			end:          30,
			output:       "state-d",
			call: &callpbv1.Call{
				Digest:         "call-4",
				ReceiverDigest: "state-b",
				Field:          "withExec",
				Type:           &callpbv1.Type{NamedType: "Container"},
			},
		}),
	}

	proj, err := ProjectTrace("trace1", spans)
	if err != nil {
		t.Fatalf("project trace: %v", err)
	}

	if len(proj.Objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(proj.Objects))
	}

	var container ObjectNode
	var directory ObjectNode
	for _, obj := range proj.Objects {
		switch obj.TypeName {
		case "Container":
			container = obj
		case "Directory":
			directory = obj
		}
	}

	if len(container.StateHistory) != 3 {
		t.Fatalf("expected container state history length 3, got %d", len(container.StateHistory))
	}
	if container.StateHistory[0].StateDigest != "state-a" ||
		container.StateHistory[1].StateDigest != "state-b" ||
		container.StateHistory[2].StateDigest != "state-d" {
		t.Fatalf("unexpected container state history: %#v", container.StateHistory)
	}
	if !container.ReferencedByTop {
		t.Fatalf("expected container to be top-level referenced")
	}
	if len(directory.StateHistory) != 1 || directory.StateHistory[0].StateDigest != "state-c" {
		t.Fatalf("unexpected directory state history: %#v", directory.StateHistory)
	}

	if len(proj.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(proj.Events))
	}
	if proj.Events[0].Kind != "create" || proj.Events[0].SpanID != "s1" || !proj.Events[0].TopLevel {
		t.Fatalf("unexpected first event: %#v", proj.Events[0])
	}
	if proj.Events[0].Inputs[0].StateDigest != "seed-state" {
		t.Fatalf("expected first event to contain arg ref, got %#v", proj.Events[0].Inputs)
	}
	if proj.Events[1].Kind != "create" || proj.Events[1].SpanID != "s3" || !proj.Events[1].TopLevel {
		t.Fatalf("unexpected second event: %#v", proj.Events[1])
	}
	if proj.Events[2].Kind != "mutate" || proj.Events[2].SpanID != "s2" || proj.Events[2].TopLevel {
		t.Fatalf("unexpected third event: %#v", proj.Events[2])
	}
	if proj.Events[3].Kind != "mutate" || proj.Events[3].SpanID != "s4" || proj.Events[3].TopLevel {
		t.Fatalf("unexpected fourth event: %#v", proj.Events[3])
	}
}

func TestSnapshotAt(t *testing.T) {
	t.Parallel()

	proj := &TraceProjection{
		TraceID: "trace1",
		Objects: []ObjectNode{
			{
				ID:       "obj-1",
				TypeName: "Container",
				StateHistory: []ObjectState{
					{StateDigest: "a", StartUnixNano: 1, EndUnixNano: 10},
					{StateDigest: "b", StartUnixNano: 11, EndUnixNano: 20},
					{StateDigest: "c", StartUnixNano: 25, EndUnixNano: 30},
				},
			},
		},
		Events: []MutationEvent{
			{SpanID: "s1", StartUnixNano: 1, EndUnixNano: 10},
			{SpanID: "s2", StartUnixNano: 25, EndUnixNano: 30},
		},
		EndUnixNano: 30,
	}

	snap := SnapshotAt(proj, 26)
	if len(snap.Objects) != 1 {
		t.Fatalf("expected 1 object in snapshot, got %d", len(snap.Objects))
	}
	if len(snap.Objects[0].StateHistory) != 2 {
		t.Fatalf("expected 2 visible states, got %d", len(snap.Objects[0].StateHistory))
	}
	if len(snap.ActiveEventIDs) != 1 || snap.ActiveEventIDs[0] != "s2" {
		t.Fatalf("unexpected active events: %#v", snap.ActiveEventIDs)
	}
}

func TestSnapshotAtStep(t *testing.T) {
	t.Parallel()

	proj := &TraceProjection{
		TraceID: "trace-step",
		Objects: []ObjectNode{
			{
				ID:       "obj-1",
				TypeName: "Container",
				StateHistory: []ObjectState{
					{StateDigest: "a", SpanID: "s1", StartUnixNano: 1, EndUnixNano: 10},
					{StateDigest: "b", SpanID: "s2", StartUnixNano: 11, EndUnixNano: 20},
					{StateDigest: "c", SpanID: "s3", StartUnixNano: 12, EndUnixNano: 20},
				},
			},
		},
		Events: []MutationEvent{
			{SpanID: "sx", StartUnixNano: 1, EndUnixNano: 9, Kind: "call"},
			{SpanID: "s1", StartUnixNano: 1, EndUnixNano: 10, Kind: "create", ObjectID: "obj-1"},
			{SpanID: "s2", StartUnixNano: 11, EndUnixNano: 20, Kind: "mutate", ObjectID: "obj-1"},
			{SpanID: "s3", StartUnixNano: 12, EndUnixNano: 20, Kind: "mutate", ObjectID: "obj-1"},
		},
		EndUnixNano: 20,
	}

	snap0 := SnapshotAtStep(proj, 0)
	if len(snap0.Events) != 2 {
		t.Fatalf("expected first step to include 2 events, got %d", len(snap0.Events))
	}
	if len(snap0.Objects) != 1 || len(snap0.Objects[0].StateHistory) != 1 {
		t.Fatalf("expected first step to include one state, got %#v", snap0.Objects)
	}

	snap1 := SnapshotAtStep(proj, 1)
	if len(snap1.Events) != 3 {
		t.Fatalf("expected second step to include 3 events, got %d", len(snap1.Events))
	}
	if len(snap1.Objects) != 1 || len(snap1.Objects[0].StateHistory) != 2 {
		t.Fatalf("expected second step to include two states, got %#v", snap1.Objects)
	}

	snap2 := SnapshotAtStep(proj, 2)
	if len(snap2.Events) != 4 {
		t.Fatalf("expected third step to include 4 events, got %d", len(snap2.Events))
	}
	if len(snap2.Objects) != 1 || len(snap2.Objects[0].StateHistory) != 3 {
		t.Fatalf("expected third step to include three states, got %#v", snap2.Objects)
	}
}

func TestProjectTraceFiltersInternalSeedsAndScalarOutputs(t *testing.T) {
	t.Parallel()

	spans := []store.SpanRecord{
		mustCallSpan(t, callSpanInput{
			traceID:      "trace2",
			spanID:       "s1",
			parentSpanID: "",
			name:         "Query.moduleSource",
			start:        1,
			end:          2,
			output:       "state-internal",
			internal:     true,
			call: &callpbv1.Call{
				Digest: "call-internal",
				Field:  "moduleSource",
				Type:   &callpbv1.Type{NamedType: "ModuleSource"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace2",
			spanID:       "s2",
			parentSpanID: "",
			name:         "Query.moduleSource",
			start:        10,
			end:          20,
			output:       "state-a",
			call: &callpbv1.Call{
				Digest: "call-a",
				Field:  "moduleSource",
				Type:   &callpbv1.Type{NamedType: "ModuleSource"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace2",
			spanID:       "s3",
			parentSpanID: "s2",
			name:         "ModuleSource.withName",
			start:        21,
			end:          30,
			output:       "state-b",
			call: &callpbv1.Call{
				Digest:         "call-b",
				ReceiverDigest: "state-a",
				Field:          "withName",
				Type:           &callpbv1.Type{NamedType: "mymod.ModuleSource"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace2",
			spanID:       "s4",
			parentSpanID: "",
			name:         "Query.name",
			start:        31,
			end:          35,
			output:       "state-string",
			call: &callpbv1.Call{
				Digest: "call-scalar",
				Field:  "name",
				Type:   &callpbv1.Type{NamedType: "String"},
			},
		}),
	}

	proj, err := ProjectTrace("trace2", spans)
	if err != nil {
		t.Fatalf("project trace: %v", err)
	}

	if len(proj.Objects) != 1 {
		t.Fatalf("expected 1 rendered object, got %d", len(proj.Objects))
	}
	obj := proj.Objects[0]
	if obj.TypeName != "ModuleSource" {
		t.Fatalf("expected ModuleSource, got %s", obj.TypeName)
	}
	if len(obj.StateHistory) != 2 {
		t.Fatalf("expected collapsed history of 2 states, got %d", len(obj.StateHistory))
	}
	if obj.StateHistory[0].StateDigest != "state-a" || obj.StateHistory[1].StateDigest != "state-b" {
		t.Fatalf("unexpected state history: %#v", obj.StateHistory)
	}

	for _, event := range proj.Events {
		if event.Internal {
			t.Fatalf("internal events should be filtered, found %+v", event)
		}
	}

	if len(proj.Events) != 3 {
		t.Fatalf("expected 3 non-internal events, got %d", len(proj.Events))
	}
	if proj.Events[0].SpanID != "s2" || proj.Events[0].Kind != "create" {
		t.Fatalf("unexpected first event: %+v", proj.Events[0])
	}
	if proj.Events[1].SpanID != "s3" || proj.Events[1].Kind != "mutate" {
		t.Fatalf("unexpected second event: %+v", proj.Events[1])
	}
	if proj.Events[2].SpanID != "s4" || proj.Events[2].ObjectID != "" {
		t.Fatalf("expected scalar output to stay as call event, got %+v", proj.Events[2])
	}
}

func TestProjectTraceDropsTopLevelCallOnlyFanoutObjects(t *testing.T) {
	t.Parallel()

	spans := []store.SpanRecord{
		mustCallSpan(t, callSpanInput{
			traceID:      "trace3",
			spanID:       "s1",
			parentSpanID: "",
			name:         "Query.moduleSource",
			start:        1,
			end:          5,
			output:       "state-root",
			call: &callpbv1.Call{
				Digest: "call-root",
				Field:  "moduleSource",
				Type:   &callpbv1.Type{NamedType: "ModuleSource"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace3",
			spanID:       "s2",
			parentSpanID: "",
			name:         "ModuleSource.withName",
			start:        6,
			end:          8,
			output:       "state-noise",
			internal:     true,
			call: &callpbv1.Call{
				Digest:         "call-noise",
				ReceiverDigest: "state-unrelated",
				Field:          "withName",
				Type:           &callpbv1.Type{NamedType: "ModuleSource"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace3",
			spanID:       "s3",
			parentSpanID: "",
			name:         "Module.source",
			start:        9,
			end:          12,
			output:       "state-noise",
			call: &callpbv1.Call{
				Digest:         "call-module-source",
				ReceiverDigest: "state-module",
				Field:          "source",
				Type:           &callpbv1.Type{NamedType: "ModuleSource"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace3",
			spanID:       "s4",
			parentSpanID: "",
			name:         "Query.container",
			start:        13,
			end:          20,
			output:       "state-c1",
			call: &callpbv1.Call{
				Digest: "call-c1",
				Field:  "container",
				Type:   &callpbv1.Type{NamedType: "Container"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace3",
			spanID:       "s5",
			parentSpanID: "s4",
			name:         "Container.withExec",
			start:        21,
			end:          30,
			output:       "state-c2",
			call: &callpbv1.Call{
				Digest:         "call-c2",
				ReceiverDigest: "state-c1",
				Field:          "withExec",
				Type:           &callpbv1.Type{NamedType: "Container"},
			},
		}),
	}

	proj, err := ProjectTrace("trace3", spans)
	if err != nil {
		t.Fatalf("project trace: %v", err)
	}

	if len(proj.Objects) != 2 {
		t.Fatalf("expected 2 rendered objects after fan-out filtering, got %d", len(proj.Objects))
	}

	types := map[string]int{}
	for _, obj := range proj.Objects {
		types[obj.TypeName]++
	}
	if types["Container"] != 1 || types["ModuleSource"] != 1 {
		t.Fatalf("unexpected object types: %#v", types)
	}

	for _, event := range proj.Events {
		if event.Name == "Module.source" && event.ObjectID != "" {
			t.Fatalf("expected Module.source call-only fan-out object to be pruned from events, got %+v", event)
		}
	}
}

type callSpanInput struct {
	traceID      string
	spanID       string
	parentSpanID string
	name         string
	start        int64
	end          int64
	output       string
	internal     bool
	call         *callpbv1.Call
}

func mustCallSpan(t *testing.T, in callSpanInput) store.SpanRecord {
	t.Helper()

	callBytes, err := proto.Marshal(in.call)
	if err != nil {
		t.Fatalf("marshal call payload: %v", err)
	}
	attrs := map[string]any{
		telemetry.DagCallAttr:   base64.StdEncoding.EncodeToString(callBytes),
		telemetry.DagDigestAttr: in.call.GetDigest(),
	}
	if in.output != "" {
		attrs[telemetry.DagOutputAttr] = in.output
	}
	if in.internal {
		attrs[telemetry.UIInternalAttr] = true
	}
	data, err := json.Marshal(map[string]any{"attributes": attrs})
	if err != nil {
		t.Fatalf("marshal data_json: %v", err)
	}
	return store.SpanRecord{
		TraceID:         in.traceID,
		SpanID:          in.spanID,
		ParentSpanID:    in.parentSpanID,
		Name:            in.name,
		StartUnixNano:   in.start,
		EndUnixNano:     in.end,
		StatusCode:      "STATUS_CODE_OK",
		StatusMessage:   "",
		DataJSON:        string(data),
		UpdatedUnixNano: in.end,
	}
}
