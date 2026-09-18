package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestPartSourceSelectionLaterRoute(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "request", &transferTestValue{Text: "pending"})
	first := persistedListTestResult(t, ctx, c, srv, "later-first", &transferTestValue{Text: "ready"})
	second := persistedListTestResult(t, ctx, c, srv, "later-second", &transferTestValue{Text: "ready"})
	frame := receiver.cacheSharedResult().loadResultCall().clone()
	extra := digest.FromString("later ready equivalent")
	frame.ExtraDigests = append(frame.ExtraDigests, call.ExtraDigest{Digest: extra})
	receiver.cacheSharedResult().storeResultCall(frame)
	c.egraphMu.Lock()
	c.addResultDigestPostingLocked(first.cacheSharedResult().id, extra.String(), resultDigestPostingExact)
	c.addResultDigestPostingLocked(second.cacheSharedResult().id, extra.String(), resultDigestPostingExact)
	c.egraphMu.Unlock()
	lookup, err := c.partLookupFor(receiver.cacheSharedResult())
	require.NoError(t, err)
	session, err := partSession(ctx)
	require.NoError(t, err)
	ordinary := func() (*sharedResult, CacheHitRoute) {
		c.egraphMu.Lock()
		defer c.egraphMu.Unlock()
		match := c.lookupMatchForCallLocked(frame, lookup.recipe, lookup.self, lookup.inputs, time.Now().Unix())
		return c.selectLookupCandidateForSessionLocked(session, match.candidates), match.route
	}
	before, route := ordinary()
	require.Same(t, receiver.cacheSharedResult(), before)
	require.Equal(t, CacheHitRouteRecipe, route)
	for range 2 {
		source, err := c.AcquireEquivalentPartSource(ctx, receiver, PersistedPartAddress{Part: "snapshot"})
		require.NoError(t, err)
		require.Same(t, first.cacheSharedResult(), source.source)
		require.Equal(t, CacheHitRouteDigest, source.route)
		require.NoError(t, source.Release(ctx))
	}
	after, afterRoute := ordinary()
	require.Same(t, before, after)
	require.Equal(t, route, afterRoute)
}

func TestPartSessionlessOwnSubset(t *testing.T) {
	for _, mode := range []string{"offer-only", "own", "revoked-before-commit", "native-receiver"} {
		t.Run(mode, func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
			donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "ready"})
			resource := persistedListTestResult(t, ctx, c, srv, "socket", String("owned resource"))
			c.egraphMu.Lock()
			resource.cacheSharedResult().sessionResourceHandle = "socket"
			_, err := c.recomputeRequiredSessionResourcesLocked(resource.cacheSharedResult())
			receiver.cacheSharedResult().imported = mode != "native-receiver"
			c.egraphMu.Unlock()
			require.NoError(t, err)
			transferTestDependency(c, ctx, donor, resource)
			transferTestOffer(t, c, ctx, receiver, resource)
			if mode != "offer-only" {
				transferTestDependency(c, ctx, receiver, resource)
			}
			partTestEquivalent(t, c, receiver, donor)
			c.egraphMu.Lock()
			_, err = c.recomputeRequiredSessionResourcesLocked(donor.cacheSharedResult())
			require.NoError(t, err)
			_, err = c.recomputeRequiredSessionResourcesLocked(receiver.cacheSharedResult())
			require.NoError(t, err)
			c.egraphMu.Unlock()
			address := PersistedPartAddress{Part: "snapshot"}
			_, _, probe, err := c.probePart(ctx, donor.cacheSharedResult(), address)
			require.NoError(t, err)
			before := ownershipCounts(c, donor.cacheSharedResult())[0]
			source, err := c.newSessionlessPartSourceLease(ctx, receiver.cacheSharedResult(), donor.cacheSharedResult(), address, address, *probe)
			if mode == "offer-only" || mode == "native-receiver" {
				require.Error(t, err)
				require.Nil(t, source)
				require.Equal(t, before, ownershipCounts(c, donor.cacheSharedResult())[0])
				return
			}
			require.NoError(t, err)
			require.Equal(t, before+1, ownershipCounts(c, donor.cacheSharedResult())[0])
			require.True(t, source.sessionlessShare)
			require.Empty(t, source.sessionID)
			// Encoded receiver keeps the test independent of a test-only typed store.
			record, err := c.CapturePersistedRecord(ctx, receiver)
			require.NoError(t, err)
			row := receiver.cacheSharedResult()
			row.payloadMu.Lock()
			row.hasValue = false
			row.self = nil
			row.persistedEnvelope = &record.Envelope
			row.payloadRevision++
			row.payloadMu.Unlock()
			session, err := partSession(ctx)
			require.NoError(t, err)
			require.NoError(t, c.BindSessionResource(ctx, session, "client", "socket", new(int)))
			require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:subset", LazyTaskSpec{Body: func(ctx context.Context) error {
				permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
				require.NoError(t, err)
				p, err := c.PrepareReadyPart(ctx, receiver, source, permit)
				require.NoError(t, err)
				if mode == "revoked-before-commit" {
					c.egraphMu.Lock()
					row.requiredSessionResources = nil
					row.requiredSessionResourcesGen.Add(1)
					c.egraphMu.Unlock()
				}
				receipt, outcome, err := c.CommitReadyPart(ctx, p)
				if mode == "revoked-before-commit" {
					require.ErrorIs(t, err, ErrPartReselect)
					require.Equal(t, PartInstallRefused, outcome)
					require.Nil(t, receipt)
					return nil
				}
				require.NoError(t, err)
				require.Equal(t, PartInstalled, outcome)
				return c.finishReadyPartInline(ctx, receipt)
			}}))
			waitCacheQuiescent(t, c)
			require.Equal(t, before, ownershipCounts(c, donor.cacheSharedResult())[0])
		})
	}
}

func TestPartReadyRevalidationAndCanceledFinish(t *testing.T) {
	for _, mode := range []string{"stale-donor", "cancel-commit", "cancel-finish"} {
		t.Run(mode, func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
			donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "ready"})
			partTestEquivalent(t, c, receiver, donor)
			record, err := c.CapturePersistedRecord(ctx, receiver)
			require.NoError(t, err)
			row := receiver.cacheSharedResult()
			row.payloadMu.Lock()
			row.hasValue = false
			row.self = nil
			row.persistedEnvelope = &record.Envelope
			row.payloadRevision++
			row.payloadMu.Unlock()
			before := ownershipCounts(c, row)[0]
			externalCtx := ctx
			require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:boundary", LazyTaskSpec{Body: func(ctx context.Context) error {
				address := PersistedPartAddress{Part: "snapshot"}
				source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
				require.NoError(t, err)
				permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
				require.NoError(t, err)
				p, err := c.PrepareReadyPart(ctx, receiver, source, permit)
				require.NoError(t, err)
				commitCtx := ctx
				if mode == "stale-donor" {
					c.egraphMu.Lock()
					donor.cacheSharedResult().expiresAtUnix = 1
					c.egraphMu.Unlock()
				}
				if mode == "cancel-commit" {
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					commitCtx = canceled
				}
				receipt, outcome, err := c.CommitReadyPart(commitCtx, p)
				if mode != "cancel-finish" {
					require.Error(t, err)
					require.Equal(t, PartInstallRefused, outcome)
					return nil
				}
				require.NoError(t, err)
				require.Equal(t, PartInstalled, outcome)
				// An external canceled Finish consumes its independent hold even when
				// the body has not returned. The owning body can still finish syncing.
				canceled, cancel := context.WithCancel(externalCtx)
				cancel()
				require.ErrorIs(t, c.FinishReadyPart(canceled, receipt), context.Canceled)
				return nil
			}}))
			waitCacheQuiescent(t, c)
			require.Equal(t, before, ownershipCounts(c, row)[0])
		})
	}
}

func TestPartLazyOperationMissingOutputStopsOnce(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "missing-output", &transferTestValue{Text: "pending"})
	address := PersistedPartAddress{Part: "snapshot"}
	calls := 0
	err := c.RunLazyTask(ctx, receiver, "lazy:missing-output", LazyTaskSpec{Body: func(ctx context.Context) error {
		drain, _, err := c.PrepareOriginal(ctx, receiver, LazyGroupAddress{Group: LazyGroupWhole}, []PersistedPartAddress{address}, PartTaskFromContext(ctx))
		if err != nil {
			return err
		}
		if err = drain.Wait(ctx); err != nil {
			return err
		}
		scan, err := c.CheckPartSources(ctx, receiver, address, drain, &PartDemandState{})
		if err != nil {
			return err
		}
		original, _, err := c.BeginOriginal(ctx, scan.NoSource)
		if err != nil {
			return err
		}
		calls++
		var version capturedRowRevision
		produced, err := c.capturePartRecord(ctx, receiver.cacheSharedResult(), false, nil, &version)
		if err != nil {
			return err
		}
		return c.publishEvaluatedParts(ctx, receiver, address, produced, original, &partCleanup{fn: func(context.Context) error { return nil }})
	}})
	require.ErrorContains(t, err, "left required output snapshot unset")
	require.Equal(t, 1, calls)
}

// CheckSessionlessRestoredDirectoryForTest is test-only so the external test
// can use core's actual Directory codec without adding a dagql -> core import.
func CheckSessionlessRestoredDirectoryForTest(t *testing.T, ctx context.Context, c *Cache, receiverID, donorID uint64) {
	t.Helper()
	receiver, donor := c.resultsByID[sharedResultID(receiverID)], c.resultsByID[sharedResultID(donorID)]
	require.NotNil(t, receiver)
	require.NotNil(t, donor)
	require.True(t, receiver.imported)
	frame := receiver.loadResultCall()
	require.NotNil(t, frame.Receiver)
	require.NotZero(t, frame.Receiver.ResultID)
	require.Nil(t, frame.Receiver.shared)
	address := PersistedPartAddress{Part: "snapshot"}
	_, _, probe, err := c.probePart(ctx, donor, address)
	require.NoError(t, err)
	require.NotNil(t, probe)
	before := donor.incomingOwnershipCount
	source, err := c.newSessionlessPartSourceLease(ctx, receiver, donor, address, address, *probe)
	require.NoError(t, err)
	require.Equal(t, before+1, donor.incomingOwnershipCount)
	require.NoError(t, source.Release(ctx))
	require.Equal(t, before, donor.incomingOwnershipCount)
	// Preparation never grants authority over a replacement call frame.
	lookup, err := c.partLookupFor(receiver)
	require.NoError(t, err)
	receiver.storeResultCall(frame.clone())
	c.egraphMu.Lock()
	source, err = c.newSessionlessPartSourceLeaseLocked(ctx, receiver, donor, address, address, *probe, lookup)
	c.egraphMu.Unlock()
	require.ErrorIs(t, err, ErrPartReselect)
	require.Nil(t, source)
	require.Equal(t, before, donor.incomingOwnershipCount)
}
