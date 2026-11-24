package tracesource

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
)

var _ LiveLogClient = (*client.Client)(nil)

type fakeLiveLogs struct {
	got    archive.LogQuery
	cursor int64
}

func (f *fakeLiveLogs) QueryLogs(_ context.Context, q archive.LogQuery, cursor int64, consume func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	f.got, f.cursor = q, cursor
	for _, next := range []int64{cursor + 2, cursor + 5} {
		if err := consume(next, &collogspb.ExportLogsServiceRequest{}); err != nil {
			return cursor, err
		}
		cursor = next
	}
	return cursor, nil
}

func TestLiveReadLogs(t *testing.T) {
	fake := &fakeLiveLogs{}
	spans := []archive.LogSpan{{SpanID: "00000000000000a1", Descendants: true}}
	var cursors []string
	err := Live{Client: fake}.ReadLogs(t.Context(), "ignored", LogQuery{Spans: spans, Follow: true, Cursor: "10"}, func(_ context.Context, _ *collogspb.ExportLogsServiceRequest, cursor string) error {
		cursors = append(cursors, cursor)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, archive.LogQuery{Spans: spans, Follow: true}, fake.got)
	require.EqualValues(t, 10, fake.cursor)
	require.Equal(t, []string{"12", "15"}, cursors)

	err = Live{Client: fake}.ReadLogs(t.Context(), "", LogQuery{Spans: spans, Cursor: "{}"}, nil)
	require.ErrorContains(t, err, "invalid log cursor")

	// A bounded one-shot passes its bound on; one with nothing between its
	// cursor and its bound asks nothing.
	err = Live{Client: fake}.ReadLogs(t.Context(), "", LogQuery{Spans: spans, Through: "7"}, func(context.Context, *collogspb.ExportLogsServiceRequest, string) error { return nil })
	require.NoError(t, err)
	require.Equal(t, archive.LogQuery{Spans: spans, Through: 7}, fake.got)
	fake.got = archive.LogQuery{}
	err = Live{Client: fake}.ReadLogs(t.Context(), "", LogQuery{Spans: spans, Cursor: "7", Through: "7"}, nil)
	require.NoError(t, err)
	require.Empty(t, fake.got.Spans)
}

type fakeStoreLogQuery struct {
	archiveReader
	opts archive.StreamOptions
	q    archive.LogQuery
}

func (f *fakeStoreLogQuery) LogQuery(_ context.Context, _ string, opts archive.StreamOptions, q archive.LogQuery, consume func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	f.opts, f.q = opts, q
	return opts.HighWater, consume(opts.HighWater, &collogspb.ExportLogsServiceRequest{})
}

func TestStoreReadLogs(t *testing.T) {
	reader := &fakeStoreLogQuery{}
	store := NewStore(reader, archive.Manifest{TraceID: "trace", HighWater: archive.HighWater{Logs: 9}})
	spans := []archive.LogSpan{{SpanID: "00000000000000a1", Digests: []string{"xxh3:x"}}}
	var cursors []string
	err := store.ReadLogs(t.Context(), "trace", LogQuery{Spans: spans, Follow: true, Cursor: "3"}, func(_ context.Context, _ *collogspb.ExportLogsServiceRequest, cursor string) error {
		cursors = append(cursors, cursor)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, archive.StreamOptions{Cursor: 3, HighWater: 9}, reader.opts)
	require.Equal(t, archive.LogQuery{Spans: spans}, reader.q, "an archive is finite: no follow")
	require.Equal(t, []string{"9"}, cursors)

	require.ErrorContains(t, store.ReadLogs(t.Context(), "other", LogQuery{Spans: spans}, nil), "pinned")
	require.ErrorContains(t, store.ReadLogs(t.Context(), "trace", LogQuery{Spans: spans, Cursor: "10"}, nil), "past the archive's cut")
}

type fakeCloudLogs struct {
	mu      sync.Mutex
	active  atomic.Int32
	maxSeen atomic.Int32
	sels    map[string]cloud.LogSelection
}

func (f *fakeCloudLogs) FetchLogs(ctx context.Context, traceID string, sel cloud.LogSelection, cb func(context.Context, *collogspb.ExportLogsServiceRequest) error) error {
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		seen := f.maxSeen.Load()
		if n <= seen || f.maxSeen.CompareAndSwap(seen, n) {
			break
		}
	}
	f.mu.Lock()
	f.sels[sel.SpanID] = sel
	f.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	return cb(ctx, &collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{
		ScopeLogs: []*otlplogsv1.ScopeLogs{{LogRecords: []*otlplogsv1.LogRecord{{TimeUnixNano: 1000}}}},
	}}})
}

func TestCloudLogsFanOutIsBounded(t *testing.T) {
	fake := &fakeCloudLogs{sels: map[string]cloud.LogSelection{}}
	var spans []archive.LogSpan
	for i := range 20 {
		spans = append(spans, archive.LogSpan{SpanID: fmt.Sprintf("%016x", i+1), Descendants: i == 0})
	}
	resumeAt := time.Unix(0, 500).UTC()
	cursor, err := json.Marshal(map[string]time.Time{spans[1].SpanID: resumeAt})
	require.NoError(t, err)
	var batches int
	var last string
	var inCallback atomic.Bool
	err = CloudLogs{Client: fake, Concurrency: 3}.ReadLogs(t.Context(), "trace", LogQuery{Spans: spans, Cursor: string(cursor)}, func(_ context.Context, _ *collogspb.ExportLogsServiceRequest, c string) error {
		require.False(t, inCallback.Swap(true), "callbacks are serial")
		defer inCallback.Store(false)
		batches++
		last = c
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 20, batches)
	require.LessOrEqual(t, fake.maxSeen.Load(), int32(3))
	require.True(t, fake.sels[spans[0].SpanID].Descendants)
	require.Equal(t, cloud.LogRecordsAll, fake.sels[spans[0].SpanID].Records)
	require.Nil(t, fake.sels[spans[0].SpanID].After)
	require.Equal(t, resumeAt, *fake.sels[spans[1].SpanID].After, "a cursor resumes each span after its last record")

	var resumed map[string]time.Time
	require.NoError(t, json.Unmarshal([]byte(last), &resumed))
	require.Len(t, resumed, 20)
	require.Equal(t, time.Unix(0, 1000).UTC(), resumed[spans[5].SpanID])
}
