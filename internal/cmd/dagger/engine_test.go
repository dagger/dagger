package daggercmd

import (
	"context"
	"io"
	"testing"

	"github.com/dagger/dagger/dagql/idtui"
)

// TestEngineTelemetryConfigSkipsSharedExporters guards the fix for the noisy
// "HTTP exporter is shutdown" / "context canceled" telemetry warnings emitted by
// the second engine session that `dagger module init` opens. Internal plumbing
// sessions must not wire up (and later tear down) the process-wide OTLP exporter
// singletons, otherwise the real command that runs next in the same process
// re-exports into already-shut-down exporters. See engineTelemetryConfig.
func TestEngineTelemetryConfigSkipsSharedExporters(t *testing.T) {
	oldFrontend := Frontend
	oldSkip := skipSharedTelemetryExporters
	Frontend = idtui.NewPlain(io.Discard)
	t.Cleanup(func() {
		Frontend = oldFrontend
		skipSharedTelemetryExporters = oldSkip
	})

	ctx := context.Background()

	skipSharedTelemetryExporters = false
	if cfg := engineTelemetryConfig(ctx); !cfg.Detect {
		t.Fatal("expected Detect to be enabled for a normal session")
	}

	skipSharedTelemetryExporters = true
	if cfg := engineTelemetryConfig(ctx); cfg.Detect {
		t.Fatal("expected Detect to be disabled for an internal silent session")
	}
}
