package telemetry

import (
	"testing"

	daggertelemetry "dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
)

func TestExecutionScopeConnectAttributes(t *testing.T) {
	t.Parallel()

	attrs := ExecutionScopeConnectAttributes(&engine.ClientMetadata{
		SessionID:      "session-1",
		ClientID:       "client-1",
		ParentClientID: "client-0",
		ClientKind:     ClientKindNested,
	})

	got := map[string]string{}
	for _, attr := range attrs {
		got[string(attr.Key)] = attr.Value.AsString()
	}

	if got[daggertelemetry.EngineSessionIDAttr] != "session-1" {
		t.Fatalf("expected session attr, got %#v", got)
	}
	if got[daggertelemetry.EngineClientIDAttr] != "client-1" {
		t.Fatalf("expected client attr, got %#v", got)
	}
	if got[daggertelemetry.EngineParentClientIDAttr] != "client-0" {
		t.Fatalf("expected parent client attr, got %#v", got)
	}
	if got[daggertelemetry.EngineClientKindAttr] != ClientKindNested {
		t.Fatalf("expected client kind attr, got %#v", got)
	}
}
