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
	c.addResultDigestPostingLocked(first.cacheSharedResult().id, extra.String())
	c.addResultDigestPostingLocked(second.cacheSharedResult().id, extra.String())
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
			released := armLazyAttemptReleased(c)
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
			waitLazyAttemptReleased(t, released)
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
		return c.publishEvaluatedParts(ctx, receiver, address, produced, original, &partCleanup{fn: func(context.Context) error { return nil }}, nil)
	}})
	require.ErrorContains(t, err, "left required output snapshot unset")
	require.Equal(t, 1, calls)
}

// A lazy operation's publication is prepared by prepareEvaluatedParts, not by
// PrepareReadyPart. Whichever constructor built it, a preparation must reach
// Commit with the representation it observed and, for an encoded receiver, the
// envelope Commit installs. A constructor that skips either once made Commit
// answer reselect forever, which publishEvaluatedParts retries without a
// bound, and then would have installed a nil envelope.
func TestPartLazyOperationPublishesOverObservedRepresentation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		encoded bool
	}{{"encoded receiver", true}, {"typed receiver", false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, c, srv, _ := partTestCache(t)
			srv.InstallObject(NewClass(srv, ClassOpts[*partTestValue]{}))
			receiver := persistedListTestResult(t, ctx, c, srv, "lazy-receiver", newPartTestValue("receiver", map[string]partTestState{"fs": {}}))
			row := receiver.cacheSharedResult()
			if tc.encoded {
				partTestEncodedReceiver(t, ctx, c, receiver)
			} else {
				c.egraphMu.Lock()
				row.imported = true
				c.egraphMu.Unlock()
			}
			evaluated := persistedListTestResult(t, ctx, c, srv, "lazy-evaluated", newPartTestValue("evaluated", map[string]partTestState{"fs": {Snapshot: "fs-snap"}}))
			address := PersistedPartAddress{Part: "fs"}
			before := row.loadPayloadState()
			require.Equal(t, !tc.encoded, before.hasValue)

			// Bounded: a Commit that can never succeed ends the publication
			// loop with this deadline instead of spinning.
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			var published sharedResultPayloadState
			err := c.RunLazyTask(ctx, receiver, "lazy:publishes", LazyTaskSpec{Body: func(ctx context.Context) error {
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
				var version capturedRowRevision
				produced, err := c.capturePartRecord(ctx, evaluated.cacheSharedResult(), false, nil, &version)
				if err != nil {
					return err
				}
				err = c.publishEvaluatedParts(ctx, receiver, address, produced, original, &partCleanup{fn: func(context.Context) error { return nil }}, nil)
				// Observed before the owner synchronization that follows the
				// Body applies the link and advances the revision again.
				published = row.loadPayloadState()
				return err
			}})
			require.NoError(t, err)
			require.Equal(t, before.payloadRevision+1, published.payloadRevision, "one Commit advances the payload revision once")
			require.Equal(t, before.hasValue, published.hasValue)
			if tc.encoded {
				require.NotNil(t, published.persistedEnvelope, "an encoded Commit installs an envelope")
				require.NotSame(t, before.persistedEnvelope, published.persistedEnvelope)
			} else {
				value, ok := receiver.Unwrap().(*partTestValue)
				require.True(t, ok)
				require.Equal(t, "fs-snap", value.Parts["fs"].Snapshot, "the typed store published the evaluated part")
			}
			_, _, probe, err := c.probePart(context.WithoutCancel(ctx), row, address)
			require.NoError(t, err)
			require.NotNil(t, probe)
			require.True(t, probe.LocalComplete, "the published representation contains the evaluated part")
			require.Equal(t, "fs-snap", probe.Descriptor.SnapshotID)
			require.True(t, partTestHasLink(receiver, "fs-snap"), "and the row owns its snapshot")
		})
	}
}

// A preparation that reaches Commit without what its constructor's seal
// establishes is a construction defect. Commit names it in an error; it never
// answers reselect, which a caller's retry loop would take for a changed
// source and retry forever.
func TestCommitReadyPartRejectsBrokenPreparation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		breaks func(*PreparedReadyPart)
		want   string
	}{
		{"unsealed", func(p *PreparedReadyPart) { p.sealed = false }, "not sealed"},
		{"encoded without envelope", func(p *PreparedReadyPart) { p.published = nil }, "no envelope to install"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, c, srv, manager := partTestCache(t)
			srv.InstallObject(NewClass(srv, ClassOpts[*partTestValue]{}))
			receiver := persistedListTestResult(t, ctx, c, srv, "broken-receiver", newPartTestValue("receiver", map[string]partTestState{"fs": {}}))
			partTestEncodedReceiver(t, ctx, c, receiver)
			evaluated := persistedListTestResult(t, ctx, c, srv, "broken-evaluated", newPartTestValue("evaluated", map[string]partTestState{"fs": {Snapshot: "fs-snap"}}))
			address := PersistedPartAddress{Part: "fs"}
			row := receiver.cacheSharedResult()
			before := row.loadPayloadState()

			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			var outcome PartInstallOutcome
			err := c.RunLazyTask(ctx, receiver, "lazy:broken", LazyTaskSpec{Body: func(ctx context.Context) error {
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
				var version capturedRowRevision
				produced, err := c.capturePartRecord(ctx, evaluated.cacheSharedResult(), false, nil, &version)
				if err != nil {
					return err
				}
				prepared, err := c.prepareEvaluatedParts(ctx, receiver, address, produced, original, &partCleanup{fn: func(context.Context) error { return nil }})
				if err != nil {
					return err
				}
				tc.breaks(prepared)
				_, outcome, err = c.CommitReadyPart(ctx, prepared)
				return err
			}})
			require.ErrorContains(t, err, tc.want)
			require.False(t, partCanReselect(err), "a construction defect is never a reselect")
			require.Equal(t, PartInstallRefused, outcome)
			after := row.loadPayloadState()
			require.Equal(t, before.payloadRevision, after.payloadRevision, "nothing was published")
			require.Same(t, before.persistedEnvelope, after.persistedEnvelope)
			require.Equal(t, manager.pins.Load(), manager.released.Load(), "the refused preparation released its pin")
		})
	}
}
