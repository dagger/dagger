package daggercmd

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// terminalOwnerFrontend is a frontend that draws on the terminal while owns
// is set, like the pretty TUI.
type terminalOwnerFrontend struct {
	*idtui.FrontendMock
	owns atomic.Bool
}

func (fe *terminalOwnerFrontend) OwnsTerminal() bool { return fe.owns.Load() }

type bodyLogExporter struct {
	bodies    []string
	shutdowns int
}

func (e *bodyLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	for _, record := range records {
		e.bodies = append(e.bodies, record.Body().AsString())
	}
	return nil
}

func (e *bodyLogExporter) Shutdown(context.Context) error   { e.shutdowns++; return nil }
func (e *bodyLogExporter) ForceFlush(context.Context) error { return nil }

func diagnosticRecords(body string) []sdklog.Record {
	var record sdklog.Record
	record.SetBody(log.StringValue(body))
	return []sdklog.Record{record}
}

// Diagnostics go to stderr, except while a TUI draws on the terminal, where a
// raw write would scramble it: then they go to the frontend.
func TestCLIDiagnosticsAvoidTUITerminal(t *testing.T) {
	ctx := t.Context()
	frontendLogs := new(bodyLogExporter)
	fe := &terminalOwnerFrontend{FrontendMock: &idtui.FrontendMock{
		LogExporterFunc: func() sdklog.Exporter { return frontendLogs },
	}}
	var stderr bytes.Buffer
	exporter := newCLIDiagnosticExporter(&stderr, true, fe)

	require.NoError(t, exporter.Export(ctx, diagnosticRecords("before the TUI\n")))
	fe.owns.Store(true)
	require.NoError(t, exporter.Export(ctx, diagnosticRecords("during the TUI\n")))
	fe.owns.Store(false)
	require.NoError(t, exporter.Export(ctx, diagnosticRecords("after the TUI\n")))
	require.NoError(t, exporter.Shutdown(ctx))

	require.Equal(t, "before the TUI\nafter the TUI\n", stderr.String())
	require.Equal(t, []string{"during the TUI\n"}, frontendLogs.bodies)
	require.Zero(t, frontendLogs.shutdowns, "shutting the frontend's exporter down would end its stream")

	t.Run("frontend that never owns the terminal", func(t *testing.T) {
		frontendLogs := new(bodyLogExporter)
		var stderr bytes.Buffer
		exporter := newCLIDiagnosticExporter(&stderr, true, &idtui.FrontendMock{
			LogExporterFunc: func() sdklog.Exporter { return frontendLogs },
		})
		require.NoError(t, exporter.Export(ctx, diagnosticRecords("diagnostic\n")))
		require.Equal(t, "diagnostic\n", stderr.String())
		require.Empty(t, frontendLogs.bodies)
	})

	// With stderr redirected, the TUI draws on stdout, and diagnostics keep
	// going to stderr as they happen.
	t.Run("stderr is not the terminal", func(t *testing.T) {
		frontendLogs := new(bodyLogExporter)
		fe := &terminalOwnerFrontend{FrontendMock: &idtui.FrontendMock{
			LogExporterFunc: func() sdklog.Exporter { return frontendLogs },
		}}
		fe.owns.Store(true)
		var stderr bytes.Buffer
		exporter := newCLIDiagnosticExporter(&stderr, false, fe)
		require.NoError(t, exporter.Export(ctx, diagnosticRecords("diagnostic\n")))
		require.Equal(t, "diagnostic\n", stderr.String())
		require.Empty(t, frontendLogs.bodies)
	})
}
