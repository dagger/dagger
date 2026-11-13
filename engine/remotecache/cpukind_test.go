package remotecache

import (
	"context"
	"io"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// kindOf is the kind of remote cache work ctx is labeled with.
func kindOf(ctx context.Context) string {
	kind, _ := pprof.Label(ctx, wcprof.CPUKindLabel)
	return kind
}

// The remote cache's work runs labeled by kind, so the engine's CPU samples
// can be split by it: a merge as a merge, an export's capture and its upload,
// and the lookups.
func TestWorkIsLabeledByKind(t *testing.T) {
	adapter := newFakeAdapter()
	kinds := make(chan string, 8)
	adapter.merge = func(ctx context.Context, _ protocol.Merge) (protocol.Merged, error) {
		kinds <- "merge=" + kindOf(ctx)
		return protocol.Merged{}, nil
	}
	d := digest.FromString("blob")
	adapter.export = func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
		kinds <- "capture=" + kindOf(ctx)
		return consume(ctx, Export{Bundle: testBundle(), Blobs: blobs{d: "blob"}})
	}
	s, conn := testSession(t, adapter)
	s.cfg.put = func(ctx context.Context, _ string, body io.Reader, _ int64) error {
		kinds <- "upload=" + kindOf(ctx)
		_, err := io.Copy(io.Discard, body)
		return err
	}

	s.dispatch(request(t, 7, protocol.TypeMerge, protocol.Merge{Bundle: *testBundle()}))
	conn.next(t)
	require.Equal(t, "merge="+wcprof.CPUKindMerge, <-kinds)

	s.dispatch(request(t, 8, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
	exported := decode[protocol.Exported](t, conn.next(t))
	require.Equal(t, "capture="+wcprof.CPUKindExportCapture, <-kinds)
	s.dispatch(request(t, 9, protocol.TypeUpload, protocol.Upload{ExportID: exported.ExportID, URLs: map[digest.Digest]string{d: "put-1"}}))
	conn.next(t)
	require.Equal(t, "upload="+wcprof.CPUKindExportSend, <-kinds)
}

// The lookups take the engine's misses labeled as lookups.
func TestLookupsAreLabeled(t *testing.T) {
	adapter := newFakeAdapter()
	l := &labelingAdapter{fakeAdapter: adapter, kinds: make(chan string, 4)}
	s, _ := testSession(t, l)
	s.cfg.Lookups = true
	go s.lookups()
	select {
	case kind := <-l.kinds:
		require.Equal(t, wcprof.CPUKindLookup, kind)
	case <-time.After(10 * time.Second):
		t.Fatal("the lookups never took misses")
	}
}

// labelingAdapter reports the kind its lookups' takes run under.
type labelingAdapter struct {
	*fakeAdapter
	kinds chan string
}

func (a *labelingAdapter) TakeLookupMisses(ctx context.Context, limit int, linger time.Duration) ([]dagql.RemoteMiss, uint64, error) {
	select {
	case a.kinds <- kindOf(ctx):
	default:
	}
	return a.fakeAdapter.TakeLookupMisses(ctx, limit, linger)
}
