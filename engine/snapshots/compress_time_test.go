package snapshots

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// slowWriter is a compressor's writer that takes each write and its close
// some time, as compressing does.
type slowWriter struct {
	dest  io.Writer
	delay time.Duration
}

func (w *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	return w.dest.Write(p)
}

func (w *slowWriter) Close() error {
	time.Sleep(w.delay)
	return nil
}

// A layer's time in its compressor counts every write of the archive into
// the compressor's writer and its close, and nothing of what the differ does
// between them: so a diff's time less it is reading the layer and archiving
// it. The blob is the compressor's, unchanged.
func TestTimedCompressorCountsItsWritersTime(t *testing.T) {
	const delay = 20 * time.Millisecond
	var compress compression.Compressor = func(dest io.Writer, _ string) (io.WriteCloser, error) {
		return &slowWriter{dest: dest, delay: delay}, nil
	}
	var spent atomic.Int64
	var blob bytes.Buffer
	start := time.Now()
	w, err := timedCompressor(compress, &spent)(&blob, "")
	require.NoError(t, err)
	for _, chunk := range []string{"a", "b", "c"} {
		_, err := w.Write([]byte(chunk))
		require.NoError(t, err)
	}
	between := time.Now()
	time.Sleep(3 * delay)
	outside := time.Since(between)
	require.NoError(t, w.Close())
	total := time.Since(start)
	require.Equal(t, "abc", blob.String())
	got := time.Duration(spent.Load())
	require.GreaterOrEqual(t, got, 4*delay, "three writes and the close")
	// However long a loaded machine takes, what is counted lies inside the
	// whole and outside the gap between the writes and the close.
	require.LessOrEqual(t, got, total-outside, "not the time between them")
}
