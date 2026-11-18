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
}
