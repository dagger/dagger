package dagql

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// This adapter gives the sharing fixture the same acquisition entry point as a
// core Directory, without changing the other sharing tests' value family.
type fixtureDemandTestValue struct {
	*shareTestValue
	host *PartHost
}

func (*fixtureDemandTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "FixtureDemandTestValue", NonNull: true}
}

func (v *fixtureDemandTestValue) BindPartHost(host *PartHost) { v.host = host }

func (*fixtureDemandTestValue) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value, err := (*shareTestValue)(nil).DecodePersistedObject(ctx, dec, raw)
	if err != nil {
		return nil, err
	}
	return &fixtureDemandTestValue{shareTestValue: value.(*shareTestValue)}, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.FixtureDemand", Typed: (*fixtureDemandTestValue)(nil),
		Visitor: shareTestCodec{}, Transfer: shareTestCodec{}, BackgroundDecode: true,
	})
}

func TestTransferFixtureEvaluatesExactReceiver(t *testing.T) {
	t.Parallel()
	ctx, c, srv, _ := shareTestCache(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c.EnableTransferFixtureParts()
	srv.InstallObject(NewClass(srv, ClassOpts[*fixtureDemandTestValue]{}))
	barrier := newSharePassBarrier(c)
	held := make(chan struct{})
	release := sync.OnceFunc(func() { close(held) })
	barrier.hold.Store(&held)
	finished := false
	t.Cleanup(func() {
		release()
		if !finished {
			select {
			case <-barrier.passes:
			case <-time.After(5 * time.Second):
				t.Error("sharing pass did not end after cleanup released it")
			}
		}
	})
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &fixtureDemandTestValue{shareTestValue: newShareTestValue("donor", map[string]sharePartState{"snapshot": {Snapshot: "local-snapshot"}})})
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &fixtureDemandTestValue{shareTestValue: newShareTestValue("receiver", map[string]sharePartState{"snapshot": {}})})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	receiverID, err := receiver.ID()
	require.NoError(t, err)
	shareTestUnite(t, ctx, c, "handle-read-before-share", donor, receiver)
	require.Equal(t, 1, barrier.awaitEntered(t))

	loaded, err := c.LoadResultByResultID(ctx, "test-session", srv, uint64(receiver.cacheSharedResult().id))
	require.NoError(t, err)
	require.Same(t, donor.cacheSharedResult(), loaded.cacheSharedResult(), "a session handle load may canonicalize to the earlier donor")
	require.NoError(t, c.Evaluate(ctx, loaded))
	value, ok := loaded.Unwrap().(*fixtureDemandTestValue)
	require.True(t, ok)
	value.mu.RLock()
	snapshot := value.Parts["snapshot"].Snapshot
	value.mu.RUnlock()
	require.Equal(t, "local-snapshot", snapshot)

	counts := func(report TransferFixtureReport) map[string]int {
		out := map[string]int{}
		for _, event := range report.Parts {
			if event.ResultID == uint64(receiver.cacheSharedResult().id) && event.Address.Part == "snapshot" {
				out[event.Kind]++
			}
		}
		return out
	}
	before, err := c.TransferFixtureSnapshot(ctx, "test-session", nil)
	require.NoError(t, err)
	require.Empty(t, counts(before), "the successful canonical read did not wait for the imported row's sharing pass")
	require.Empty(t, receiver.cacheSharedResult().loadSnapshotOwnerLinks())
	t.Logf("requested receiver=%d, loaded donor=%d, before=%v", receiver.cacheSharedResult().id, loaded.cacheSharedResult().id, counts(before))

	// The sharing pass is still parked. Demand must decode and fill this row,
	// even though the ordinary load above chose its completed equivalent.
	require.NoError(t, c.EvaluateTransferFixtureRoots(ctx, "test-session", srv, []*call.ID{receiverID}))
	afterDemand, err := c.TransferFixtureSnapshot(ctx, "test-session", nil)
	require.NoError(t, err)
	require.Equal(t, 1, counts(afterDemand)["installed-ready"])
	require.Equal(t, 1, counts(afterDemand)["settled"])
	require.Zero(t, counts(afterDemand)["installed-chain"])
	require.Zero(t, counts(afterDemand)["provider-read"])
	require.Zero(t, counts(afterDemand)["lazy-enter"])
	require.True(t, shareTestHasLink(receiver, "local-snapshot"))
	t.Logf("exact demand=%v", counts(afterDemand))

	release()
	require.Equal(t, 1, barrier.awaitPass(t))
	finished = true
	// An already owned receiver can be demanded again without another install.
	require.NoError(t, c.EvaluateTransferFixtureRoots(ctx, "test-session", srv, []*call.ID{receiverID}))
	after, err := c.TransferFixtureSnapshot(ctx, "test-session", nil)
	require.NoError(t, err)
	require.Equal(t, 1, counts(after)["installed-ready"])
	require.Equal(t, 1, counts(after)["settled"])
	require.True(t, shareTestHasLink(receiver, "local-snapshot"))
	t.Logf("after=%v", counts(after))
}
