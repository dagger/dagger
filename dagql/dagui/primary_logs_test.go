package dagui

import (
	"errors"
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

func TestPrimaryLogsSpillFailureIsVisible(t *testing.T) {
	db := NewDB()
	db.PrimarySpan = spanID(1)
	t.Setenv("TMPDIR", t.TempDir()+"/missing")
	db.IngestLogs([]sdklog.Record{newTestLogRecord(trace.TraceID{1}, db.PrimarySpan.SpanID, strings.Repeat("x", primaryLogMemoryLimit+1))})
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
