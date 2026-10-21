package idtui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLogBufferWithoutTempStorage: past the in-memory limit with no writable
// TMPDIR, the log stays in memory and readable.
func TestLogBufferWithoutTempStorage(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()+"/missing")
	var b logBuffer
	want := strings.Repeat("x", logBufferMemoryLimit) + "past the limit\n" + "more\n"
	for _, part := range []string{strings.Repeat("x", logBufferMemoryLimit), "past the limit\n", "more\n"} {
		_, err := b.WriteString(part)
		require.NoError(t, err)
	}
	require.Empty(t, b.path)
	require.Equal(t, len(want), b.Len())
	got, err := b.contents()
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestLogBufferSpillFailureKeepsTail: when an established spill file stops
// accepting writes, what it holds is kept and the rest is read from memory
// after it.
func TestLogBufferSpillFailureKeepsTail(t *testing.T) {
	var b logBuffer
	t.Cleanup(b.Reset)
	head := strings.Repeat("h", logBufferMemoryLimit+1)
	_, err := b.WriteString(head)
	require.NoError(t, err)
	require.NotEmpty(t, b.path)
	b.abandonSpill(errors.New("no space left on device"))
	_, err = b.WriteString("tail\n")
	require.NoError(t, err)
	got, err := b.contents()
	require.NoError(t, err)
	require.Equal(t, head+"tail\n", got)
	stat, err := os.Stat(b.path)
	require.NoError(t, err)
	require.EqualValues(t, len(head), stat.Size(), "the tail is not written to the file")
}
