package core

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

func liveHeapBytes() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// TestModuleFunctionReceiverRetention measures what a session keeps of the
// receivers it converted for module function calls. An object's state never
// changes in place: a call that changes it returns a new object, so every call
// on an evolving object, such as an agent's tool object, has a new receiver.
func TestModuleFunctionReceiverRetention(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	const session = "receiver-retention-session"
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "receiver-retention-client", SessionID: session})
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		require.NoError(t, cache.ReleaseSession(ctx, session))
		require.NoError(t, cache.Close(ctx))
	})

	holderType := newTypeDefAttachedResult(t, ctx, cache, srv, "receiver-retention-type", NewObjectTypeDef("Holder", "", nil)).Self()
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{TypeDef: holderType}}))
	receivers := make([]*CallOpts, 0, 256)
	for i := range cap(receivers) {
		// An object whose notes grew by one per call, the i-th holding i.
		notes := make([]any, i)
		for n := range notes {
			notes[n] = map[string]any{"id": fmt.Sprintf("notes/%d", n), "text": strings.Repeat("x", 1024)}
		}
		holder := &ModuleObject{TypeDef: holderType, Fields: map[string]any{"notes": notes}}
		receiver, err := dagql.NewObjectResultForCall(holder, srv, moduleObjectTestSyntheticCall(fmt.Sprintf("receiver-%d", i), holder))
		require.NoError(t, err)
		attached, err := cache.AttachResult(ctx, session, srv, receiver)
		require.NoError(t, err)
		attachedObj, ok := dagql.UnwrapAs[*ModuleObject](attached)
		require.True(t, ok)
		receivers = append(receivers, &CallOpts{ParentTyped: attached, ParentFields: attachedObj.Fields})
	}
	baseEntries := cache.EntryStats().CompletedArbitrary

	var jsonBytes int
	before := liveHeapBytes()
	for _, opts := range receivers {
		parentJSON, err := moduleFunctionReceiverJSON(ctx, opts)
		require.NoError(t, err)
		jsonBytes += len(parentJSON)
	}
	retained := int64(liveHeapBytes()) - int64(before)
	entries := cache.EntryStats().CompletedArbitrary - baseEntries
	t.Logf("%d calls on new receivers: %d entries for %.2f MiB of receiver JSON, %.2f MiB retained",
		len(receivers), entries, float64(jsonBytes)/(1<<20), float64(retained)/(1<<20))
	require.Equal(t, 1, entries, "a session keeps one entry for its converted receivers")
	res, err := cache.GetOrInitArbitrary(ctx, session, fmt.Sprintf("module-receiver-sdk-input:%q", session), dagql.ArbitraryValueFunc(nil))
	require.NoError(t, err)
	kept := res.Value().(*convertedReceivers)
	kept.mu.Lock()
	defer kept.mu.Unlock()
	require.LessOrEqual(t, kept.weight, maxConvertedReceiversWeight)
	require.Less(t, kept.lru.Len(), len(receivers), "the least recently used conversions were dropped")
}

// TestConvertedReceiversBounded shows that what a session keeps stops growing
// at its bound, with a bound small enough for a quick test.
func TestConvertedReceiversBounded(t *testing.T) {
	const maxWeight = 1 << 20
	receivers := newConvertedReceivers(maxWeight)
	var jsonBytes int
	before := liveHeapBytes()
	for i := range 1000 {
		data := []byte(fmt.Sprintf(`{"state":%q}`, strings.Repeat("x", 1024*(i%64))))
		got, err := receivers.get(t.Context(), convertedReceiverKey{resultID: uint64(i + 1)}, func(context.Context) ([]byte, error) {
			return data, nil
		})
		require.NoError(t, err)
		jsonBytes += len(got)
	}
	retained := int64(liveHeapBytes()) - int64(before)
	receivers.mu.Lock()
	weight, kept := receivers.weight, receivers.lru.Len()
	receivers.mu.Unlock()
	t.Logf("1000 calls: %.2f MiB of receiver JSON, %d kept weighing %.2f MiB, %.2f MiB retained",
		float64(jsonBytes)/(1<<20), kept, float64(weight)/(1<<20), float64(retained)/(1<<20))
	require.LessOrEqual(t, weight, maxWeight)
}
