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
			{SpanID: "s1", StartUnixNano: 1, EndUnixNano: 10, Kind: "create", ObjectID: "obj-1", Visible: true},
			{SpanID: "s2", StartUnixNano: 11, EndUnixNano: 20, Kind: "mutate", ObjectID: "obj-1", Visible: true},
			{SpanID: "s3", StartUnixNano: 12, EndUnixNano: 20, Kind: "mutate", ObjectID: "obj-1", Visible: true},
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

	var foundHidden bool
	for _, event := range proj.Events {
		if event.Name == "Module.source" && event.ObjectID != "" {
			foundHidden = true
			if event.Visible {
				t.Fatalf("expected Module.source fan-out object event to be hidden, got %+v", event)
			}
		}
	}
	if !foundHidden {
		t.Fatalf("expected Module.source call-only fan-out event to remain in event stream")
	}
}

func TestProjectTracePrunesNonTopLevelOnlyObjects(t *testing.T) {
	t.Parallel()

	spans := []store.SpanRecord{
		mustCallSpan(t, callSpanInput{
			traceID:      "trace4",
			spanID:       "s1",
			parentSpanID: "",
			name:         "Query.container",
			start:        1,
			end:          10,
			output:       "state-container",
			call: &callpbv1.Call{
				Digest: "call-container",
				Field:  "container",
				Type:   &callpbv1.Type{NamedType: "Container"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace4",
			spanID:       "s2",
			parentSpanID: "s1",
			name:         "Query.http",
			start:        11,
			end:          20,
			output:       "state-file",
			call: &callpbv1.Call{
				Digest: "call-file",
				Field:  "http",
				Type:   &callpbv1.Type{NamedType: "File"},
			},
		}),
	}

	proj, err := ProjectTrace("trace4", spans)
	if err != nil {
		t.Fatalf("project trace: %v", err)
	}

	if len(proj.Objects) != 1 {
		t.Fatalf("expected only top-level object to remain, got %d", len(proj.Objects))
	}
	if proj.Objects[0].TypeName != "Container" {
		t.Fatalf("expected kept object type Container, got %s", proj.Objects[0].TypeName)
	}

	var foundHidden bool
	for _, event := range proj.Events {
		if event.Name == "Query.http" && event.ObjectID != "" {
			foundHidden = true
			if event.Visible {
				t.Fatalf("expected non-top-level object event to be hidden: %+v", event)
			}
		}
	}
	if !foundHidden {
		t.Fatalf("expected non-top-level object event to remain in event stream")
	}
}

func TestProjectTraceSummaryAndCallDepth(t *testing.T) {
	t.Parallel()

	spans := []store.SpanRecord{
		mustCallSpan(t, callSpanInput{
			traceID:      "trace5",
			spanID:       "s1",
			parentSpanID: "",
			name:         "Query.container",
			start:        1,
			end:          10,
			output:       "state-a",
			resource: map[string]any{
				"service.name":         "dagger-cli",
				"process.command_args": []any{"dagger", "call", "module", "test"},
			},
			scope: map[string]any{
				"name": "dagger.io/cli",
			},
			call: &callpbv1.Call{
				Digest: "call-1",
				Field:  "container",
				Type:   &callpbv1.Type{NamedType: "Container"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace5",
			spanID:       "s2",
			parentSpanID: "s1",
			name:         "Container.withExec",
			start:        11,
			end:          20,
			output:       "state-b",
			call: &callpbv1.Call{
				Digest:         "call-2",
				ReceiverDigest: "state-a",
				Field:          "withExec",
				Type:           &callpbv1.Type{NamedType: "Container"},
			},
		}),
	}

	proj, err := ProjectTrace("trace5", spans)
	if err != nil {
		t.Fatalf("project trace: %v", err)
	}

	if proj.Summary.Title != "dagger call module test" {
		t.Fatalf("unexpected summary title: %q", proj.Summary.Title)
	}
	if len(proj.Summary.RootSpans) != 1 {
		t.Fatalf("expected 1 root span, got %d", len(proj.Summary.RootSpans))
	}
	if len(proj.Summary.CommandSpans) != 1 {
		t.Fatalf("expected 1 command span, got %d", len(proj.Summary.CommandSpans))
	}

	var nested MutationEvent
	for _, event := range proj.Events {
		if event.SpanID == "s2" {
			nested = event
		}
	}
	if nested.CallDepth != 1 || nested.ParentCallSpanID != "s1" || nested.ParentCallName != "Query.container" {
		t.Fatalf("unexpected nested call metadata: %+v", nested)
	}
}

func TestProjectTraceIncludesNonDagSpansInEventStream(t *testing.T) {
	t.Parallel()

	spans := []store.SpanRecord{
		{
			TraceID:       "trace6",
			SpanID:        "root",
			ParentSpanID:  "",
			Name:          "session",
			StartUnixNano: 1,
			EndUnixNano:   2,
			StatusCode:    "STATUS_CODE_OK",
			DataJSON:      `{"attributes":{"foo":"bar"}}`,
		},
		mustCallSpan(t, callSpanInput{
			traceID:      "trace6",
			spanID:       "s1",
			parentSpanID: "root",
			name:         "Query.container",
			start:        3,
			end:          4,
			output:       "state-a",
			call: &callpbv1.Call{
				Digest: "call-1",
				Field:  "container",
				Type:   &callpbv1.Type{NamedType: "Container"},
			},
		}),
	}

	proj, err := ProjectTrace("trace6", spans)
	if err != nil {
		t.Fatalf("project trace: %v", err)
	}

	if len(proj.Events) != 2 {
		t.Fatalf("expected non-dag span + dag.call event, got %d", len(proj.Events))
	}
	if proj.Events[0].SpanID != "root" || proj.Events[0].RawKind != "span" {
		t.Fatalf("expected first event to be raw span, got %+v", proj.Events[0])
	}
	if proj.Events[1].SpanID != "s1" || proj.Events[1].RawKind != "call" {
		t.Fatalf("expected second event to be dag.call span, got %+v", proj.Events[1])
	}
}

func TestProjectTraceRehydratesOutputStateFromDuplicateDigest(t *testing.T) {
	t.Parallel()

	outputState := map[string]any{
		"type": "Container",
		"fields": map[string]any{
			"name": map[string]any{
				"name":  "name",
				"type":  "String",
				"value": "demo",
			},
		},
	}

	spans := []store.SpanRecord{
		mustCallSpan(t, callSpanInput{
			traceID:      "trace7",
			spanID:       "s1",
			parentSpanID: "",
			name:         "Query.container",
			start:        1,
			end:          10,
			output:       "state-a",
			outputState:  outputState,
			call: &callpbv1.Call{
				Digest: "call-1",
				Field:  "container",
				Type:   &callpbv1.Type{NamedType: "Container"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace7",
			spanID:       "s2",
			parentSpanID: "",
			name:         "Query.container",
			start:        11,
			end:          20,
			output:       "state-a", // duplicate immutable state ID, payload omitted by emitter
			call: &callpbv1.Call{
				Digest: "call-2",
				Field:  "container",
				Type:   &callpbv1.Type{NamedType: "Container"},
			},
		}),
	}

	proj, err := ProjectTrace("trace7", spans)
	if err != nil {
		t.Fatalf("project trace: %v", err)
	}
	if len(proj.Objects) != 1 {
		t.Fatalf("expected one object, got %d", len(proj.Objects))
	}
	if proj.Objects[0].MissingState {
		t.Fatalf("expected missingState=false when duplicate digest payload is available elsewhere")
	}
	if len(proj.Objects[0].StateHistory) != 2 {
		t.Fatalf("expected 2 state entries, got %d", len(proj.Objects[0].StateHistory))
	}
	if proj.Objects[0].StateHistory[1].OutputStateJSON == nil {
		t.Fatalf("expected duplicate digest state entry to be rehydrated with payload")
	}
}

func TestProjectTraceBuildsObjectEdgesFromStateFieldRefs(t *testing.T) {
	t.Parallel()

	spans := []store.SpanRecord{
		mustCallSpan(t, callSpanInput{
			traceID:      "trace8",
			spanID:       "s1",
			parentSpanID: "",
			name:         "Query.directory",
			start:        1,
			end:          10,
			output:       "state-dir",
			outputState: map[string]any{
				"type": "Directory",
				"fields": map[string]any{
					"path": map[string]any{
						"name":  "path",
						"type":  "String",
						"value": "/work",
					},
				},
			},
			call: &callpbv1.Call{
				Digest: "call-dir",
				Field:  "directory",
				Type:   &callpbv1.Type{NamedType: "Directory"},
			},
		}),
		mustCallSpan(t, callSpanInput{
			traceID:      "trace8",
			spanID:       "s2",
			parentSpanID: "",
			name:         "Query.container",
			start:        11,
			end:          20,
			output:       "state-ctr",
			outputState: map[string]any{
				"type": "Container",
				"fields": map[string]any{
					"FS": map[string]any{
						"name":  "FS",
						"type":  "Directory!",
						"value": "state-dir",
					},
					"Mounts": map[string]any{
						"name": "Mounts",
						"type": "[]Mount",
						"value": []any{
							map[string]any{
								"Source": "state-dir",
							},
						},
					},
				},
			},
			call: &callpbv1.Call{
				Digest: "call-ctr",
				Field:  "container",
				Type:   &callpbv1.Type{NamedType: "Container"},
			},
		}),
	}

	proj, err := ProjectTrace("trace8", spans)
	if err != nil {
		t.Fatalf("project trace: %v", err)
	}

	if len(proj.Objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(proj.Objects))
	}
	if len(proj.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %#v", proj.Edges)
	}

	labels := map[string]ObjectEdge{}
	for _, edge := range proj.Edges {
		labels[edge.Label] = edge
		if edge.Kind != "field-ref" {
			t.Fatalf("unexpected edge kind: %#v", edge)
		}
	}
	fsEdge, ok := labels["FS"]
	if !ok {
		t.Fatalf("expected FS edge, got %#v", proj.Edges)
	}
	if fsEdge.EvidenceCount != 1 {
		t.Fatalf("expected FS evidence count=1, got %#v", fsEdge)
	}
	mountEdge, ok := labels["Mounts[0].Source"]
	if !ok {
		t.Fatalf("expected Mounts[0].Source edge, got %#v", proj.Edges)
	}
	if mountEdge.EvidenceCount != 1 {
		t.Fatalf("expected mount edge evidence count=1, got %#v", mountEdge)
	}
	if fsEdge.FromObjectID != mountEdge.FromObjectID || fsEdge.ToObjectID != mountEdge.ToObjectID {
		t.Fatalf("expected both edges to connect the same objects, got %#v %#v", fsEdge, mountEdge)
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
	outputState  map[string]any
	internal     bool
	resource     map[string]any
	scope        map[string]any
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
	if in.outputState != nil {
		raw, err := json.Marshal(in.outputState)
		if err != nil {
			t.Fatalf("marshal output state payload: %v", err)
		}
		attrs[telemetry.DagOutputStateAttr] = base64.StdEncoding.EncodeToString(raw)
		attrs[telemetry.DagOutputStateVersionAttr] = "v1"
	}
	if in.internal {
		attrs[telemetry.UIInternalAttr] = true
	}
	data := map[string]any{"attributes": attrs}
	if len(in.resource) > 0 {
		data["resource"] = in.resource
	}
	if len(in.scope) > 0 {
		data["scope"] = in.scope
	}
	payload, err := json.Marshal(data)
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
		DataJSON:        string(payload),
		UpdatedUnixNano: in.end,
	}
}
