package daggercmd

import (
	"context"
	"slices"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql/idtui"
)

// BenchmarkLiveLogsImport imports a follow's batch of 1000 records of 50
// spans the frontend placed, all claimed -- or all but 10 records, of 5 spans
// it has not received yet, imported unclaimed, which the replay check then
// remembers.
func BenchmarkLiveLogsImport(b *testing.B) {
	for _, bc := range []struct {
		name    string
		unknown int
	}{
		{"claimed", 0},
		{"unknown", 10},
	} {
		b.Run(bc.name, func(b *testing.B) {
			const records, spans = 1000, 50
			rows := make([]liveLogRow, records)
			routes := make([]idtui.LogRoute, records)
			for i := range rows {
				span := uint16(1 + i%spans)
				route := idtui.LogRoute{Coverage: idtui.LogCovered, Span: liveTestSpan(span)}
				if every := records / max(bc.unknown, 1); bc.unknown > 0 && i%every == 0 {
					span = uint16(1000 + (i/every)%5)
					route = idtui.LogRoute{Coverage: idtui.LogUnknown}
				}
				rows[i] = liveLogRow{span: span, body: "line of output\n"}
				routes[i] = route
			}
			req := liveLogRequest(rows)
			sl := req.GetResourceLogs()[0].GetScopeLogs()[0]
			recs := slices.Clone(sl.LogRecords)
			l := newLiveLogs(nil, "trace", exporterFunc(func(context.Context, []sdklog.Record) error { return nil }), true)
			l.coverage = func([]idtui.LogTarget) []idtui.LogRoute { return routes }
			l.diag = newLiveLogsDiag(func(string, ...any) {})
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				// importLogs filters the batch in place.
				sl.LogRecords = append(sl.LogRecords[:0], recs...)
				if err := l.importLogs(ctx, req, 0, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
