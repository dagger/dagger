package dangv2

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// receivedToolCall returns the parent and argument JSON of the i-th call of
// an agent's tool object: a handle-bearing parent whose state holds blocks
// notes, the i-th call having blocks of them, and per-call arguments, a path
// and a 2 KiB edit.
func receivedToolCall(t testing.TB, i, blocks int) (parent []byte, args [][]byte) {
	t.Helper()
	notes := make([]map[string]any, blocks)
	for n := range notes {
		notes[n] = map[string]any{"id": fmt.Sprintf("notes/%d", n), "kind": "FACT", "text": strings.Repeat("x", 1024)}
	}
	parent, err := json.Marshal(map[string]any{
		"workdir":   "/src",
		"workspace": strings.Repeat("W", 300),
		"notes":     notes,
	})
	require.NoError(t, err)
	for _, arg := range []any{fmt.Sprintf("src/file-%d.go", i), fmt.Sprintf("%d:%s", i, strings.Repeat("e", 2048))} {
		data, err := json.Marshal(arg)
		require.NoError(t, err)
		args = append(args, data)
	}
	return parent, args
}

// TestReceivedRetention measures what a session retains of the parents and
// arguments its Dang function calls received.
func TestReceivedRetention(t *testing.T) {
	for _, tc := range []struct {
		name string
		// blocks is the number of notes the parent holds on call i.
		blocks func(i int) int
	}{
		{"fixed state", func(int) int { return 4 }},
		{"growing state", func(i int) int { return i }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := schemaCacheContext(t)
			cache, err := dagql.EngineCache(ctx)
			require.NoError(t, err)
			const calls = 200
			var inputBytes int
			before := liveHeap()
			for i := range calls {
				parent, args := receivedToolCall(t, i, tc.blocks(i))
				_, err := decodeReceivedParent(ctx, parent)
				require.NoError(t, err)
				inputBytes += len(parent)
				for _, arg := range args {
					_, err := decodeReceived(ctx, arg)
					require.NoError(t, err)
					inputBytes += len(arg)
				}
			}
			retained := int64(liveHeap()) - int64(before)
			entries := cache.EntryStats().CompletedArbitrary
			t.Logf("%d calls: %d received entries for %.2f MiB of input JSON, %.2f MiB retained (%.1f KiB per call)",
				calls, entries, float64(inputBytes)/(1<<20), float64(retained)/(1<<20), float64(retained)/1024/calls)
		})
	}
}
