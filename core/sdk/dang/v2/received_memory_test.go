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
			require.Equal(t, 1, entries, "a session keeps one entry for its received values")
		})
	}
}

// TestReceivedRetentionBounded shows that what a session keeps stops growing
// at its bound, with a bound small enough for a quick test.
func TestReceivedRetentionBounded(t *testing.T) {
	const maxWeight = 2 << 20
	values := newReceivedValues(maxWeight)
	var inputBytes int
	before := liveHeap()
	for i := range 400 {
		parent, args := receivedToolCall(t, i, i%64)
		for _, data := range append([][]byte{parent}, args...) {
			_, err := values.decode(t.Context(), data)
			require.NoError(t, err)
			inputBytes += len(data)
		}
	}
	retained := int64(liveHeap()) - int64(before)
	values.mu.Lock()
	weight, kept := values.weight, values.lru.Len()
	values.mu.Unlock()
	t.Logf("400 calls: %.2f MiB of input JSON, %d values kept from %.2f MiB of it, %.2f MiB retained",
		float64(inputBytes)/(1<<20), kept, float64(weight)/(1<<20), float64(retained)/(1<<20))
	require.LessOrEqual(t, weight, maxWeight)
}
