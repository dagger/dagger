package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// streamSaveTestValue is a value whose saved encoding is large and made
// afresh at each encode, as a container's or a directory's is, so the heap
// during a save shows how many rows' encodings the save holds at once.
type streamSaveTestValue struct {
	size     int
	onEncode func()
}

func (*streamSaveTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "StreamSaveTestValue", NonNull: true}
}

func (v *streamSaveTestValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	if v.onEncode != nil {
		v.onEncode()
	}
	return PersistedObjectEncoding{JSON: json.RawMessage(`"` + strings.Repeat("s", v.size) + `"`)}, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.StreamSaveTestValue", Typed: (*streamSaveTestValue)(nil), Visitor: PersistedNoReferences{},
	})
}

// A save writes each row as it encodes it, so while it encodes one row it
// holds no earlier row's encoding: its live heap rises by about one row, not
// by every row it has written so far.
//
// Not parallel: the live heap is the whole process's, and Go starts parallel
// tests only once every sequential one has finished, so nothing else
// allocates while this measures.
func TestCachePersistenceSaveHoldsOneRowEncodingAtATime(t *testing.T) {
	const rows = 32
	const rowSize = 2 << 20

	ctx, cache, srv := persistedListTestCache(t, filepath.Join(t.TempDir(), "cache.db"))
	values := make([]*streamSaveTestValue, rows)
	for i := range values {
		values[i] = &streamSaveTestValue{size: rowSize}
		persistedListTestResult(t, ctx, cache, srv, fmt.Sprintf("stream-save-%d", i), values[i])
	}

	liveHeap := func() int64 {
		runtime.GC()
		sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
		metrics.Read(sample)
		return int64(sample[0].Value.Uint64())
	}
	var encodes int
	var peak int64
	for _, value := range values {
		value.onEncode = func() {
			encodes++
			peak = max(peak, liveHeap())
		}
	}

	before := liveHeap()
	require.NoError(t, cache.persistCurrentState(ctx))
	require.Equal(t, rows, encodes, "the save encodes each row once")
	rise := peak - before
	t.Logf("live heap %d MiB before the save, rising at most %d MiB while it encoded %d rows of %d MiB", before>>20, rise>>20, rows, rowSize>>20)
	require.Less(t, rise, int64(rows*rowSize/4), "the save held earlier rows' encodings while encoding a later one")

	saved, err := cache.pdb.ListMirrorResults(ctx)
	require.NoError(t, err)
	var full int
	for _, row := range saved {
		if len(row.SelfPayload) > rowSize {
			full++
		}
	}
	require.Equal(t, rows, full, "every row is saved whole")
}
