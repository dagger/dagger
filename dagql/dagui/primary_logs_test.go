package dagui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func TestPrimaryLogsSpillAndReplay(t *testing.T) {
	db := NewDB()
	db.PrimarySpan = spanID(1)
	t.Cleanup(func() { require.NoError(t, db.ClosePrimaryLogs()) })
	bodies := []string{"prefix\n", strings.Repeat("out\x00\x1b[31m", primaryLogMemoryLimit), "last stderr"}
	streams := []int{2, 1, 2}
	for i, body := range bodies {
		rec := newTestLogRecord(trace.TraceID{1}, db.PrimarySpan.SpanID, body)
		rec.AddAttributes(otellog.Int(telemetry.StdioStreamAttr, streams[i]))
		db.IngestLogs([]sdklog.Record{rec})
	}
	buf := db.primaryLogs[db.PrimarySpan]
	require.NotNil(t, buf.file)
	require.Empty(t, buf.chunks, "spilled output must not retain SDK bodies")
	require.Zero(t, buf.bytes)
	name := buf.file.Name()
	stat, err := os.Stat(name)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), stat.Mode().Perm())
	for range 2 {
		var got strings.Builder
		counts := map[int64]int{}
		require.NoError(t, db.WalkPrimaryLogs(db.PrimarySpan, func(stream int64, data []byte) error {
			require.LessOrEqual(t, len(data), 32<<10)
			counts[stream] += len(data)
			got.Write(data)
			return nil
		}))
		require.Equal(t, strings.Join(bodies, ""), got.String())
		require.Equal(t, len(bodies[1]), counts[1])
		require.Equal(t, len(bodies[0])+len(bodies[2]), counts[2])
	}
	require.NoError(t, db.ClosePrimaryLogs())
	_, err = os.Stat(name)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestPrimaryLogsWithoutTempStorage: spilling is an optimization. Without a
// writable TMPDIR, command output, small or large, is still printed in full
// and in order, from memory.
func TestPrimaryLogsWithoutTempStorage(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()+"/missing")
	for _, tc := range []struct {
		name string
		body func(i int) string
		n    int
	}{
		{"many small records", func(i int) string { return fmt.Sprintf("line %d\n", i) }, 300},
		{"past the memory limit", func(i int) string { return fmt.Sprintf("%d:%s\n", i, strings.Repeat("x", 1<<10)) }, 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := NewDB()
			db.PrimarySpan = spanID(1)
			var want strings.Builder
			for i := range tc.n {
				body := tc.body(i)
				want.WriteString(body)
				rec := newTestLogRecord(trace.TraceID{1}, db.PrimarySpan.SpanID, body)
				rec.AddAttributes(otellog.Int(telemetry.StdioStreamAttr, 1+i%2))
				db.IngestLogs([]sdklog.Record{rec})
			}
			var got strings.Builder
			require.NoError(t, db.WalkPrimaryLogs(db.PrimarySpan, func(_ int64, data []byte) error {
				got.Write(data)
				return nil
			}))
			require.Equal(t, want.String(), got.String())
			require.NoError(t, db.ClosePrimaryLogs())
		})
	}
}

// TestPrimaryLogsCoalesceRecords: consecutive records on one stream share a
// chunk, so many small records do not force a spill.
func TestPrimaryLogsCoalesceRecords(t *testing.T) {
	db := NewDB()
	db.PrimarySpan = spanID(1)
	t.Cleanup(func() { require.NoError(t, db.ClosePrimaryLogs()) })
	for i := range 1000 {
		db.IngestLogs([]sdklog.Record{newTestLogRecord(trace.TraceID{1}, db.PrimarySpan.SpanID, fmt.Sprintf("%d\n", i))})
	}
	buf := db.primaryLogs[db.PrimarySpan]
	require.Nil(t, buf.file)
	require.Len(t, buf.chunks, 1)
}

// TestPrimaryLogsSpillWriteFailureFallsBack: when an established spill file
// stops accepting writes (a full disk), a partially written record is cut off,
// output already in the file is kept, and the rest stays in memory, replayed
// after it.
func TestPrimaryLogsSpillWriteFailureFallsBack(t *testing.T) {
	db := NewDB()
	db.PrimarySpan = spanID(1)
	t.Cleanup(func() { require.NoError(t, db.ClosePrimaryLogs()) })
	ingest := func(body string) {
		db.IngestLogs([]sdklog.Record{newTestLogRecord(trace.TraceID{1}, db.PrimarySpan.SpanID, body)})
	}
	first := strings.Repeat("a", primaryLogMemoryLimit+1)
	ingest(first)
	buf := db.primaryLogs[db.PrimarySpan]
	require.NotNil(t, buf.file)
	// A write that failed partway through its record.
	_, err := buf.file.Write([]byte{1, 2, 3})
	require.NoError(t, err)
	buf.abandonSpill(errors.New("no space left on device"))
	require.NoError(t, buf.err)
	ingest("b\n")
	ingest("c\n")
	var got strings.Builder
	require.NoError(t, db.WalkPrimaryLogs(db.PrimarySpan, func(_ int64, data []byte) error {
		got.Write(data)
		return nil
	}))
	require.Equal(t, first+"b\nc\n", got.String())
}

// TestPrimaryLogsUnrecoverableSpillIsVisible: a spill file that can neither be
// written nor cut back to its last complete record cannot be replayed
// faithfully, so the failure is reported rather than printing corrupt output.
func TestPrimaryLogsUnrecoverableSpillIsVisible(t *testing.T) {
	db := NewDB()
	db.PrimarySpan = spanID(1)
	db.IngestLogs([]sdklog.Record{newTestLogRecord(trace.TraceID{1}, db.PrimarySpan.SpanID, strings.Repeat("x", primaryLogMemoryLimit+1))})
	buf := db.primaryLogs[db.PrimarySpan]
	require.NotNil(t, buf.file)
	require.NoError(t, buf.file.Close())
	db.IngestLogs([]sdklog.Record{newTestLogRecord(trace.TraceID{1}, db.PrimarySpan.SpanID, "more")})
	require.Error(t, db.WalkPrimaryLogs(db.PrimarySpan, func(int64, []byte) error {
		t.Fatal("must not report incomplete output as successful")
		return nil
	}))
	require.Error(t, db.ClosePrimaryLogs())
}

func TestPrimaryLogsReaderFailures(t *testing.T) {
	for _, spilled := range []bool{false, true} {
		db := NewDB()
		db.PrimarySpan = spanID(1)
		size := 10
		if spilled {
			size = primaryLogMemoryLimit + 1
		}
		db.IngestLogs([]sdklog.Record{newTestLogRecord(trace.TraceID{1}, db.PrimarySpan.SpanID, strings.Repeat("x", size))})
		errStop := errors.New("stop output")
		require.ErrorIs(t, db.WalkPrimaryLogs(db.PrimarySpan, func(int64, []byte) error { return errStop }), errStop)
		if spilled {
			b := db.primaryLogs[db.PrimarySpan]
			require.NoError(t, b.file.Truncate(17))
			require.ErrorIs(t, db.WalkPrimaryLogs(db.PrimarySpan, func(int64, []byte) error { return nil }), io.ErrUnexpectedEOF)
		}
		require.NoError(t, db.ClosePrimaryLogs())
	}
}
