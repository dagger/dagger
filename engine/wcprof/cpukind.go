package wcprof

import (
	"context"
	"runtime/pprof"
)

// CPUKindLabel is the pprof label that names the kind of remote cache work a
// goroutine does. A goroutine a labeled one starts carries its label, so the
// work it hands off is counted with it. While wcprof records, the engine's
// CPU samples are split by this label (engine/server's engine CPU windows).
const CPUKindLabel = "rc.kind"

// The kinds of remote cache work an engine's CPU is split into.
const (
	// CPUKindRemoteCache is the remote cache's work that no other kind
	// names: its connection, its reads of the service's messages, settles,
	// renewals and offers.
	CPUKindRemoteCache = "remotecache"
	// CPUKindExportCapture is an export's capture of its records.
	CPUKindExportCapture = "export.capture"
	// CPUKindExportLayers is an export's layer builds: each layer's diff
	// against its parent, compressed.
	CPUKindExportLayers = "export.layers"
	// CPUKindExportSend is an export's records and blobs sent to the
	// service.
	CPUKindExportSend = "export.send"
	// CPUKindMerge is a merge of the service's records into the cache.
	CPUKindMerge = "merge"
	// CPUKindLookup is lookups of the engine's misses.
	CPUKindLookup = "lookup"
	// CPUKindFetch is the fetches and imports of the parts the service's
	// records offer.
	CPUKindFetch = "fetch"
)

// WithCPUKind labels the calling goroutine with kind, as pprof.Do does, and
// returns a context that carries the label, for what the goroutine starts
// and calls, and a func that gives the goroutine back ctx's labels. Labels
// cost a context value and a goroutine field: they're always on, whether or
// not anything samples.
func WithCPUKind(ctx context.Context, kind string) (context.Context, func()) {
	labeled := pprof.WithLabels(ctx, pprof.Labels(CPUKindLabel, kind))
	pprof.SetGoroutineLabels(labeled)
	return labeled, func() { pprof.SetGoroutineLabels(ctx) }
}
