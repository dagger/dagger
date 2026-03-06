package derive

import (
	"encoding/json"
	"testing"

	daggertelemetry "dagger.io/dagger/telemetry"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

func TestBuildScopeIndexDerivesNestedClients(t *testing.T) {
	t.Parallel()

	traceID := "trace-nested"
	rootConnect := "root-connect"
	rootWork := "root-work"
	childConnect := "child-connect"
	rootCall := "root-call"
	childCall := "child-call"

	spans := []store.SpanRecord{
		mustSpanRecord(t, spanRecordInput{
			traceID:   traceID,
			spanID:    rootConnect,
			name:      engineClientConnect,
			start:     1,
			end:       2,
			scopeName: engineClientScopeName,
			resource: map[string]any{
				"service.name":         "dagger-cli",
				"process.command_args": []any{"dagger", "call", "test"},
			},
		}),
		mustSpanRecord(t, spanRecordInput{
			traceID: traceID,
			spanID:  rootWork,
			name:    "POST /query",
			start:   5,
			end:     30,
		}),
		mustSpanRecord(t, spanRecordInput{
			traceID:      traceID,
			spanID:       childConnect,
			parentSpanID: rootWork,
			name:         engineClientConnect,
			start:        12,
			end:          13,
			scopeName:    engineClientScopeName,
			resource: map[string]any{
				"service.name": "module-client",
			},
		}),
		mustSpanRecord(t, spanRecordInput{
			traceID: traceID,
			spanID:  rootCall,
			name:    "Query.container",
			start:   10,
			end:     11,
		}),
		mustSpanRecord(t, spanRecordInput{
			traceID: traceID,
			spanID:  childCall,
			name:    "Container.withExec",
			start:   15,
			end:     18,
		}),
	}

	proj := &transform.TraceProjection{
		TraceID:       traceID,
		StartUnixNano: 1,
		EndUnixNano:   30,
		Events: []transform.MutationEvent{
			{SpanID: rootCall, RawKind: "call"},
			{SpanID: childCall, RawKind: "call"},
		},
	}

	idx := BuildScopeIndex(traceID, spans, proj)
	if len(idx.Clients) != 2 {
		t.Fatalf("expected 2 clients, got %#v", idx.Clients)
	}
	if len(idx.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %#v", idx.Sessions)
	}

	rootClientID := ClientID(traceID, rootConnect)
	childClientID := ClientID(traceID, childConnect)
	sessionID := SessionID(rootClientID)

	rootClient := idx.ClientByID[rootClientID]
	childClient := idx.ClientByID[childClientID]
	if rootClient.SessionID != sessionID || rootClient.ParentClientID != "" {
		t.Fatalf("unexpected root client: %+v", rootClient)
	}
	if childClient.ParentClientID != rootClientID || childClient.SessionID != sessionID {
		t.Fatalf("unexpected child client: %+v", childClient)
	}
	if got := idx.ClientIDForSpan(rootCall); got != rootClientID {
		t.Fatalf("expected root call owner %q, got %q", rootClientID, got)
	}
	if got := idx.ClientIDForSpan(childCall); got != childClientID {
		t.Fatalf("expected child call owner %q, got %q", childClientID, got)
	}
	if got := idx.SessionIDForSpan(childCall); got != sessionID {
		t.Fatalf("expected child call session %q, got %q", sessionID, got)
	}
}

func TestBuildScopeIndexSynthesizesFallbackSession(t *testing.T) {
	t.Parallel()

	traceID := "trace-fallback"
	callSpan := "call-only"

	spans := []store.SpanRecord{
		mustSpanRecord(t, spanRecordInput{
			traceID: traceID,
			spanID:  callSpan,
			name:    "Query.container",
			start:   10,
			end:     20,
		}),
	}

	proj := &transform.TraceProjection{
		TraceID:       traceID,
		StartUnixNano: 10,
		EndUnixNano:   20,
		Events: []transform.MutationEvent{
			{SpanID: callSpan, RawKind: "call"},
		},
	}

	idx := BuildScopeIndex(traceID, spans, proj)
	if len(idx.Clients) != 0 {
		t.Fatalf("expected no derived clients, got %#v", idx.Clients)
	}
	if len(idx.Sessions) != 1 {
		t.Fatalf("expected one fallback session, got %#v", idx.Sessions)
	}
	wantSession := FallbackSessionID(traceID)
	if idx.Sessions[0].ID != wantSession || !idx.Sessions[0].Fallback {
		t.Fatalf("unexpected fallback session: %+v", idx.Sessions[0])
	}
	if got := idx.SessionIDForSpan(callSpan); got != wantSession {
		t.Fatalf("expected fallback session %q for call span, got %q", wantSession, got)
	}
}

func TestBuildScopeIndexUsesExplicitExecutionScopeTelemetry(t *testing.T) {
	t.Parallel()

	traceID := "trace-explicit"
	sessionID := "session-explicit"
	rootClientID := "client-root"
	childClientID := "client-child"

	rootConnect := "root-connect"
	rootWork := "root-work"
	childConnect := "child-connect"
	childCall := "child-call"
	childAux := "child-aux"

	spans := []store.SpanRecord{
		mustSpanRecord(t, spanRecordInput{
			traceID:   traceID,
			spanID:    rootConnect,
			name:      engineClientConnect,
			start:     1,
			end:       2,
			scopeName: engineClientScopeName,
			resource: map[string]any{
				"service.name":         "dagger-cli",
				"process.command_args": []any{"dagger", "call"},
			},
			attributes: map[string]any{
				daggertelemetry.EngineClientIDAttr:   rootClientID,
				daggertelemetry.EngineSessionIDAttr:  sessionID,
				daggertelemetry.EngineClientKindAttr: enginetel.ClientKindRoot,
			},
		}),
		mustSpanRecord(t, spanRecordInput{
			traceID: traceID,
			spanID:  rootWork,
			name:    "POST /query",
			start:   5,
			end:     30,
			attributes: map[string]any{
				daggertelemetry.EngineClientIDAttr:  rootClientID,
				daggertelemetry.EngineSessionIDAttr: sessionID,
			},
		}),
		mustSpanRecord(t, spanRecordInput{
			traceID:      traceID,
			spanID:       childConnect,
			parentSpanID: rootWork,
			name:         engineClientConnect,
			start:        10,
			end:          11,
			scopeName:    engineClientScopeName,
			resource: map[string]any{
				"service.name": "module-client",
			},
			attributes: map[string]any{
				daggertelemetry.EngineClientIDAttr:       childClientID,
				daggertelemetry.EngineSessionIDAttr:      sessionID,
				daggertelemetry.EngineParentClientIDAttr: rootClientID,
				daggertelemetry.EngineClientKindAttr:     enginetel.ClientKindNested,
			},
		}),
		mustSpanRecord(t, spanRecordInput{
			traceID:      traceID,
			spanID:       childCall,
			parentSpanID: childConnect,
			name:         "Container.withExec",
			start:        12,
			end:          18,
			attributes: map[string]any{
				daggertelemetry.EngineClientIDAttr:  childClientID,
				daggertelemetry.EngineSessionIDAttr: sessionID,
			},
		}),
		mustSpanRecord(t, spanRecordInput{
			traceID:      traceID,
			spanID:       childAux,
			parentSpanID: childCall,
			name:         "sub-operation",
			start:        13,
			end:          14,
		}),
	}

	proj := &transform.TraceProjection{
		TraceID:       traceID,
		StartUnixNano: 1,
		EndUnixNano:   30,
		Events: []transform.MutationEvent{
			{SpanID: childCall, RawKind: "call"},
		},
	}

	idx := BuildScopeIndex(traceID, spans, proj)
	if len(idx.Clients) != 2 {
		t.Fatalf("expected 2 clients, got %#v", idx.Clients)
	}
	if len(idx.Sessions) != 1 || idx.Sessions[0].ID != sessionID {
		t.Fatalf("expected explicit session %q, got %#v", sessionID, idx.Sessions)
	}

	rootClient := idx.ClientByID[rootClientID]
	childClient := idx.ClientByID[childClientID]
	if rootClient.SessionID != sessionID || rootClient.RootClientID != rootClientID || rootClient.ClientKind != enginetel.ClientKindRoot {
		t.Fatalf("unexpected explicit root client: %+v", rootClient)
	}
	if childClient.SessionID != sessionID || childClient.ParentClientID != rootClientID || childClient.RootClientID != rootClientID || childClient.ClientKind != enginetel.ClientKindNested {
		t.Fatalf("unexpected explicit child client: %+v", childClient)
	}
	if got := idx.ClientIDForSpan(childAux); got != childClientID {
		t.Fatalf("expected child aux owner %q, got %q", childClientID, got)
	}
	if got := idx.SessionIDForSpan(childAux); got != sessionID {
		t.Fatalf("expected child aux session %q, got %q", sessionID, got)
	}
}

type spanRecordInput struct {
	traceID      string
	spanID       string
	parentSpanID string
	name         string
	start        int64
	end          int64
	resource     map[string]any
	scopeName    string
	attributes   map[string]any
}

func mustSpanRecord(t *testing.T, input spanRecordInput) store.SpanRecord {
	t.Helper()
	env := map[string]any{
		"resource":   input.resource,
		"attributes": input.attributes,
	}
	if input.scopeName != "" {
		env["scope"] = map[string]any{"name": input.scopeName}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal span env: %v", err)
	}
	return store.SpanRecord{
		TraceID:       input.traceID,
		SpanID:        input.spanID,
		ParentSpanID:  input.parentSpanID,
		Name:          input.name,
		StartUnixNano: input.start,
		EndUnixNano:   input.end,
		DataJSON:      string(raw),
	}
}
