package dagql

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPartDelegationPath(t *testing.T) {
	root := &sharedResult{id: 1}
	parent := &sharedResult{id: 2}
	address := PersistedPartAddress{OutputPath: PersistedRefPath{}.Field("items").Index(2), Part: "fs"}
	ctx, err := enterPartDemand(context.Background(), root, address)
	require.NoError(t, err)
	// Every nested demand gets a fresh exhaustion state; cycle detection must
	// survive it and must happen before RunLazyTask can join its own ancestor.
	ctx = context.WithValue(ctx, partDemandContextKey{}, new(PartDemandState))
	nested, err := enterPartDemand(ctx, parent, address)
	require.NoError(t, err)
	_, err = enterPartDemand(nested, root, address)
	require.ErrorContains(t, err, "delegation cycle")
	_, err = enterPartDemand(nested, root, PersistedPartAddress{Part: "fs"})
	require.NoError(t, err, "the full output path is part of the cycle key")
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			path, err := enterPartDemand(ctx, parent, PersistedPartAddress{OutputPath: PersistedRefPath{}.Field("items").Index(i), Part: "execMeta"})
			require.NoError(t, err)
			require.Len(t, path.Value(partDelegationPathKey{}), 2)
		})
	}
	wg.Wait()
	require.Len(t, ctx.Value(partDelegationPathKey{}), 1)
}

func TestPartDelegationProofAdmission(t *testing.T) {
	ctx, cache, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, cache, srv, "child", &transferTestValue{Text: "pending"})
	parent := persistedListTestResult(t, ctx, cache, srv, "parent", &transferTestValue{Text: "ready"})
	transferTestDependency(cache, ctx, child, parent)
	row, input := child.cacheSharedResult(), parent.cacheSharedResult()
	frame := row.loadResultCall().clone()
	frame.Receiver = &ResultCallRef{ResultID: uint64(input.id)}
	row.storeResultCall(frame)
	session, err := partSession(ctx)
	require.NoError(t, err)
	address := PersistedPartAddress{Part: "snapshot"}
	proof := &partDelegationProof{child: row, parent: input, childFrame: row.loadResultCall(), parentFrame: input.loadResultCall(), target: address, source: address, mapping: PartDelegation{ParentResultID: uint64(input.id), Address: address}}
	source := &PartSourceLease{source: input, target: address, descriptor: PartDescriptor{Address: address}, sessionID: session}
	cache.egraphMu.Lock()
	initial := proof.currentLocked(cache, source, row)
	input.expiresAtUnix = 1
	expiredInput := proof.currentLocked(cache, source, row)
	delete(row.deps, input.id)
	missingDependency := proof.currentLocked(cache, source, row)
	row.deps[input.id] = struct{}{}
	source.sessionID = ""
	sessionless := proof.currentLocked(cache, source, row)
	source.sessionID = session
	input.storeResultCall(input.loadResultCall().clone())
	changedFrame := proof.currentLocked(cache, source, row)
	cache.egraphMu.Unlock()
	require.True(t, initial)
	require.True(t, expiredInput, "owned exact inputs do not use ordinary donor expiry filtering")
	require.False(t, missingDependency)
	require.False(t, sessionless, "delegation is never a sessionless demand fallback")
	require.False(t, changedFrame, "changed parent provenance requires a new proof")
}
