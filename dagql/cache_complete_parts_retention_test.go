package dagql

import (
	"runtime"
	"strings"
	"testing"
	"weak"

	"github.com/stretchr/testify/require"
)

// A merge reads the complete parts of each record's current entry ahead of
// time. When that entry is still an undecoded envelope, the cached parts must
// not keep the envelope alive once decoding drops it.
func TestCompletePartsDoNotPinADecodedEnvelope(t *testing.T) {
	t.Parallel()
	ctx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, a, asrv, "root", &transferTestValue{Text: strings.Repeat("x", 64<<10)})
	bundle := exportTestBundle(t, ctx, a, root)

	bctx, b, bsrv := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	id := reply.Imported()[0].ResultID
	b.egraphMu.RLock()
	row := b.resultsByID[sharedResultID(id)]
	b.egraphMu.RUnlock()
	envelope := weak.Make(row.loadPayloadState().persistedEnvelope)
	require.NotNil(t, envelope.Value(), "the merged entry is an undecoded envelope")

	// The second merge targets the same entry and reads its parts.
	_, err = b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.NotNil(t, row.completeParts.Load(), "the merge cached the entry's parts")

	// Loading by number decodes without a call span, so nothing reads the
	// entry's parts again.
	_, err = b.LoadResultByResultID(bctx, "reader", bsrv, id)
	require.NoError(t, err)
	require.Nil(t, row.loadPayloadState().persistedEnvelope, "decoding drops the envelope")

	runtime.GC()
	require.Nil(t, envelope.Value(), "the decoded entry's envelope is still reachable")
	require.NotNil(t, row.completeParts.Load(), "the entry still caches its parts")
	runtime.KeepAlive(row)
	require.NoError(t, b.ReleaseSession(bctx, "reader"))
}
