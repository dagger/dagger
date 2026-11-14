package protocol

import (
	"encoding/json"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// allocatedBy returns the bytes the heap allocated running f, on average
// over n runs, each after two collections: encoding/json's buffer pools are
// empty for every run, as a collection empties them, so that a run's count
// doesn't depend on what an earlier run left pooled.
func allocatedBy(n int, f func()) float64 {
	var total uint64
	for range n {
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		total += after.TotalAlloc - before.TotalAlloc
	}
	return float64(total) / float64(n)
}

// EncodeEnvelope gives the bytes json.Marshal gives for NewEnvelope's
// envelope, for a merge and for other bodies, its error as NewEnvelope's.
func TestEncodeEnvelopeIsTheEnvelopesJSON(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		re   uint64
		typ  Type
		body any
	}{
		"merge":  {0, TypeMerge, sharedLayersMerge()},
		"lookup": {0, TypeMerge, Merge{Bundle: dagql.ValueBundle{Version: 8}, Lookup: 7}},
		"reply":  {41, TypeError, Error{Message: "<no & such>"}},
		"hello":  {0, TypeHello, Hello{CacheID: "cache-a", Generation: 2}},
	} {
		env, err := NewEnvelope(9, c.re, c.typ, c.body)
		require.NoError(t, err, name)
		want, err := json.Marshal(env)
		require.NoError(t, err, name)
		got, err := EncodeEnvelope(9, c.re, c.typ, c.body)
		require.NoError(t, err, name)
		require.Equal(t, string(want), string(got), name)
	}
	_, want := NewEnvelope(1, 0, TypeMerge, func() {})
	_, got := EncodeEnvelope(1, 0, TypeMerge, func() {})
	require.Error(t, got)
	require.Equal(t, want.Error(), got.Error())
}
