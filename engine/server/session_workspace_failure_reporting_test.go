package server

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCachedMetadataModuleFailureReportsWhenSelected(t *testing.T) {
	for _, mode := range []core.ModuleLoadMode{core.ModuleLoadBestEffort, core.ModuleLoadRepairing} {
		t.Run(map[core.ModuleLoadMode]string{core.ModuleLoadBestEffort: "check", core.ModuleLoadRepairing: "generate"}[mode], func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

			ctx, parent := provider.Tracer("test").Start(context.Background(), "command")
			defer parent.End()
			mod := pendingModule{Name: "broken", WorkspaceDir: "modules/broken"}
			loadErr := errors.New("compiler failed")
			client := &clientRuntime{pendingModules: []pendingModule{mod}}
			client.modulesMu.Lock()
			client.recordFailedModule(mod, loadErr)
			client.reportFailedModuleLocked(ctx, mod, loadErr, core.ModuleLoadMetadata)
			client.modulesMu.Unlock()
			require.Empty(t, recorder.Ended())

			srv := &Server{}
			for _, step := range []struct {
				mode    core.ModuleLoadMode
				reports int
			}{
				{core.ModuleLoadMetadata, 0},
				{mode, 1},
				{mode, 1},
				{core.ModuleLoadMetadata, 1},
			} {
				failures, err := srv.ensureModulesLoadedMode(ctx, client, nil, step.mode)
				require.NoError(t, err)
				require.Len(t, failures, 1)
				require.Equal(t, "broken", failures[0].Name)
				require.Len(t, recorder.Ended(), step.reports)
			}
			span := recorder.Ended()[0]
			require.Equal(t, "broken", span.Name())
			require.Equal(t, parent.SpanContext().SpanID(), span.Parent().SpanID())
			require.Equal(t, codes.Error, span.Status().Code)
			reported := false
			for _, attr := range span.Attributes() {
				if string(attr.Key) == telemetryattrs.GenerateSkippedAttr {
					reported = attr.Value.AsBool()
				}
			}
			require.True(t, reported)
		})
	}
}
