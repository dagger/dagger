package dagql

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

// The progress rule records only refusals whose counters moved, never fires
// while they keep moving, and fires on the first repeat.
func TestPartProgressRule(t *testing.T) {
	row := &sharedResult{id: 7}
	address := PersistedPartAddress{Part: "fs"}
	at := func(payload uint64) partSourceFacts { return partSourceFacts{payload: payload} }

	t.Run("a refusal no counter explains is ordinary", func(t *testing.T) {
		demand := &PartDemandState{}
		for i := range 100 {
			require.NoError(t, demand.refused("loop", uint64(i), address, partRefused("commit: part store busy")))
			// Equal counters: the site refused for an uncounted cause.
			same := partChanged("commit: donor facts changed", row, at(3), at(3))
			require.False(t, same.(*partRefusal).changed)
			require.NoError(t, demand.refused("loop", uint64(i), address, same))
		}
		require.Empty(t, demand.progress)
	})

	t.Run("expiry is not a counter", func(t *testing.T) {
		refused := partChanged("commit: donor facts changed", row, partSourceFacts{offers: 2, expires: 100}, partSourceFacts{offers: 2, expires: 1})
		require.False(t, refused.(*partRefusal).changed)
	})

	t.Run("contention never repeats", func(t *testing.T) {
		demand := &PartDemandState{}
		// Two sites interleave while the counters they read keep growing, as
		// they do when other work really changes the rows between attempts.
		for i := uint64(1); i <= 1000; i++ {
			require.NoError(t, demand.refused("loop", i, address, partChanged("commit: receiver version", row, at(i), at(i+1))))
			require.NoError(t, demand.refused("loop", i, address, partChanged("commit: donor facts changed", row, partSourceFacts{gate: i, offers: 1}, partSourceFacts{gate: i + 1, offers: 1})))
		}
	})

	t.Run("a repeat is no progress", func(t *testing.T) {
		demand := &PartDemandState{}
		// The expectation is wrong, so the site refuses a row that has not moved.
		refusal := func() error { return partChanged("commit: receiver representation", row, at(0), at(5)) }
		require.NoError(t, demand.refused("publishEvaluatedParts", 1, address, refusal()))
		// A different row at the same counters is a different record.
		require.NoError(t, demand.refused("publishEvaluatedParts", 1, address, partChanged("commit: receiver representation", &sharedResult{id: 8}, at(0), at(5))))
		err := demand.refused("demandPart acquire", 2, address, errors.Join(fmt.Errorf("obtain: %w", refusal()), nil))
		var stuck *PartNoProgressError
		require.ErrorAs(t, err, &stuck)
		require.ErrorIs(t, err, ErrPartNoProgress)
		require.False(t, partCanReselect(err), "the hard error is outside the reselect class")
		require.Equal(t, PartNoProgressError{
			Loop: "demandPart acquire", Site: "commit: receiver representation", ResultID: 7, Address: address,
			Expected: PartCounters{Payload: 0}, Current: PartCounters{Payload: 5},
			FirstLoop: "publishEvaluatedParts", FirstIteration: 1, Iteration: 2,
		}, *stuck)
	})

	t.Run("no demand state, no rule", func(t *testing.T) {
		var demand *PartDemandState
		for range 3 {
			require.NoError(t, demand.refused("loop", 1, address, partChanged("commit: receiver representation", row, at(0), at(5))))
		}
	})
}

// A failed version check is a changed refusal only when the payload revision
// moved. A typed output's revision and a held core guard fail the same check
// and leave it the ordinary refusal.
func TestPartVersionRefusal(t *testing.T) {
	row := &sharedResult{id: 3}
	row.payloadRevision = 4
	version := capturedRowRevision{payload: sharedResultPayloadState{payloadRevision: 4}}
	require.False(t, version.changed("commit: receiver version", row).(*partRefusal).changed)
	row.payloadRevision = 6
	refusal := version.changed("commit: receiver version", row).(*partRefusal)
	require.True(t, refusal.changed)
	require.Equal(t, row.id, refusal.row)
	require.Equal(t, uint64(4), refusal.expected.payload)
	require.Equal(t, uint64(6), refusal.current.payload)
	require.True(t, partCanReselect(refusal))
}

// Commit's counted sites say what they compared, through the real Prepare and
// Commit over an ordinary Ready donor.
func TestCommitReadyPartChangedRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		between func(c *Cache, p *PreparedReadyPart, receiver, donor *sharedResult)
		site    string
		changed bool
		row     func(receiver, donor *sharedResult) *sharedResult
	}{
		{"receiver moved", func(_ *Cache, _ *PreparedReadyPart, receiver, _ *sharedResult) {
			receiver.storeSnapshotOwnerLinks(receiver.loadSnapshotOwnerLinks())
		}, "commit: receiver version", true, func(receiver, _ *sharedResult) *sharedResult { return receiver }},
		{"donor moved", func(_ *Cache, _ *PreparedReadyPart, _, donor *sharedResult) {
			donor.storeSnapshotOwnerLinks(donor.loadSnapshotOwnerLinks())
		}, "commit: donor version", true, func(_, donor *sharedResult) *sharedResult { return donor }},
		{"donor offers moved", func(c *Cache, _ *PreparedReadyPart, _, donor *sharedResult) {
			c.egraphMu.Lock()
			donor.transferRevision++
			c.egraphMu.Unlock()
		}, "commit: donor facts changed", true, func(_, donor *sharedResult) *sharedResult { return donor }},
		{"donor expiry alone", func(c *Cache, _ *PreparedReadyPart, _, donor *sharedResult) {
			c.egraphMu.Lock()
			donor.expiresAtUnix = time.Now().Add(time.Hour).Unix()
			c.egraphMu.Unlock()
		}, "commit: donor facts changed", false, nil},
		{"wrong expectation", func(_ *Cache, p *PreparedReadyPart, _, _ *sharedResult) {
			p.expectedRepresentation.payloadRevision += 100
		}, "commit: receiver representation", true, func(receiver, _ *sharedResult) *sharedResult { return receiver }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, c, srv, _ := shareTestCache(t)
			donor, receiver := shareTestPair(t, ctx, c, srv,
				map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
				map[string]sharePartState{"fs": {}},
			)
			address := PersistedPartAddress{Part: "fs"}
			var refused error
			require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:changed", LazyTaskSpec{Body: func(ctx context.Context) error {
				source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
				if err != nil {
					return err
				}
				permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
				if err != nil {
					return errors.Join(err, source.Release(ctx))
				}
				p, err := c.PrepareReadyPart(ctx, receiver, source, permit)
				if err != nil {
					return err
				}
				tc.between(c, p, receiver.cacheSharedResult(), donor.cacheSharedResult())
				_, outcome, err := c.CommitReadyPart(ctx, p)
				if outcome != PartInstallRefused {
					return fmt.Errorf("commit outcome %d", outcome)
				}
				refused = err
				return nil
			}}))
			var refusal *partRefusal
			require.ErrorAs(t, refused, &refusal)
			require.True(t, partCanReselect(refused))
			require.Equal(t, tc.site, refusal.site)
			require.Equal(t, tc.changed, refusal.changed)
			if tc.changed {
				require.Equal(t, tc.row(receiver.cacheSharedResult(), donor.cacheSharedResult()).id, refusal.row)
				require.NotEqual(t, refusal.expected, refusal.current)
			}
		})
	}
}

// publishLoopFixture runs publishEvaluatedParts as its real owner does: inside
// the Lazy evaluation task, after the final source check, over an encoded
// receiver.
func publishLoopFixture(t *testing.T, c *Cache, srv *Server, ctx context.Context, demand *PartDemandState) (*sharedResult, error) {
	t.Helper()
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	receiver := persistedListTestResult(t, ctx, c, srv, "progress-receiver", newShareTestValue("receiver", map[string]sharePartState{"fs": {}}))
	shareTestEncodedReceiver(t, ctx, c, receiver)
	evaluated := persistedListTestResult(t, ctx, c, srv, "progress-evaluated", newShareTestValue("evaluated", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}}))
	address := PersistedPartAddress{Part: "fs"}
	// A loop that never ends is ended by this deadline, not by the go test
	// timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := c.RunLazyTask(ctx, receiver, "lazy:progress", LazyTaskSpec{Body: func(ctx context.Context) error {
		drain, _, err := c.PrepareOriginal(ctx, receiver, LazyGroupAddress{Group: LazyGroupWhole}, []PersistedPartAddress{address}, PartTaskFromContext(ctx))
		if err != nil {
			return err
		}
		if err = drain.Wait(ctx); err != nil {
			return err
		}
		scan, err := c.CheckPartSources(ctx, receiver, address, drain, demand)
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
		return c.publishEvaluatedParts(ctx, receiver, address, produced, original, &partCleanup{fn: func(context.Context) error { return nil }}, demand)
	}})
	return receiver.cacheSharedResult(), err
}

// Batch 6's second defect, in its shape: every preparation reaches Commit
// expecting a representation the row never has. The row does not move, so the
// second refusal repeats the first and the publication fails instead of
// retrying until its caller gives up.
func TestPublishEvaluatedPartsStopsWithoutProgress(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	var commits int
	c.testBeforePartCommit = func(p *PreparedReadyPart) {
		commits++
		p.expectedRepresentation.payloadRevision += 100
	}
	row, err := publishLoopFixture(t, c, srv, ctx, &PartDemandState{})
	var stuck *PartNoProgressError
	require.ErrorAs(t, err, &stuck)
	require.Equal(t, 2, commits, "the second refusal is the last")
	require.Equal(t, "publishEvaluatedParts", stuck.Loop)
	require.Equal(t, "commit: receiver representation", stuck.Site)
	require.Equal(t, uint64(row.id), stuck.ResultID)
	require.Equal(t, PartKey("fs"), stuck.Address.Part)
	require.Equal(t, stuck.Expected.Payload, stuck.Current.Payload+100)
	require.Equal(t, uint64(1), stuck.FirstIteration)
	require.Equal(t, uint64(2), stuck.Iteration)
}

// Ordinary contention: the receiver really changes between Prepare and Commit,
// many times. Every refusal reads a greater revision than the one before, so
// the rule stays silent and the publication succeeds once the row is still.
func TestPublishEvaluatedPartsSurvivesContention(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	const rounds = 200
	var commits int
	c.testBeforePartCommit = func(p *PreparedReadyPart) {
		if commits++; commits <= rounds {
			p.receiver.storeSnapshotOwnerLinks(p.receiver.loadSnapshotOwnerLinks())
		}
	}
	demand := &PartDemandState{}
	_, err := publishLoopFixture(t, c, srv, ctx, demand)
	require.NoError(t, err)
	require.Equal(t, rounds+1, commits)
	require.Len(t, demand.progress, rounds, "each refusal was recorded, and none repeated")
}

// The same wrong expectation through a whole demand: selection, the obtain
// task, InstallReadyPart and demandPart's two loops. The demand fails with the
// hard error instead of spinning.
func TestDemandPartStopsWithoutProgress(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	_, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	var commits int
	c.testBeforePartCommit = func(p *PreparedReadyPart) {
		commits++
		p.expectedRepresentation.payloadRevision += 100
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := c.demandPart(ctx, receiver, PersistedPartAddress{Part: "fs"})
	var stuck *PartNoProgressError
	require.ErrorAs(t, err, &stuck)
	require.Equal(t, 2, commits)
	require.Equal(t, "demandPart acquire", stuck.Loop)
	require.Equal(t, "commit: receiver representation", stuck.Site)
	require.Equal(t, uint64(receiver.cacheSharedResult().id), stuck.ResultID)
}

// chainLoopFixture runs installChainPart as its real owner does, inside an
// obtain Body over a real store, for an encoded receiver whose equivalent
// donor row carries the offer. It returns the donor's row for a test that
// wants to move it.
func chainLoopFixture(t *testing.T, demand *PartDemandState, arm func(c *Cache, receiver, donor *sharedResult)) (*sharedResult, error) {
	t.Helper()
	a, b := testutil.NewStore(t), testutil.NewStore(t)
	ref, _ := a.Build(t, nil, "payload", "chain bytes")
	chain, err := ref.ExportChain(t.Context(), config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
	ctx, c, srv := transferTestCache(t)
	c.snapshotManager = b.Manager
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "pending"})
	dependency := persistedListTestResult(t, ctx, c, srv, "owner-dependency", String("owner only"))
	partTestEquivalent(t, c, receiver, donor)
	address := PersistedPartAddress{Part: "snapshot"}
	record := PersistedPartOffer{Address: address, Value: SnapshotValue{Kind: "directory", Path: "/"}, Chain: OfferedChain{Layers: chain.Layers, RenewalKey: "in-process"},
		Owner: PersistedOfferOwner{DependencyIDs: []uint64{uint64(dependency.cacheSharedResult().id)}}}
	c.egraphMu.Lock()
	owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
	if err == nil {
		err = c.attachPartOfferLocked(donor.cacheSharedResult(), address, &partOffer{record: record, owner: owner})
	}
	c.egraphMu.Unlock()
	require.NoError(t, err)
	captured, err := c.CapturePersistedRecord(ctx, receiver)
	require.NoError(t, err)
	row := receiver.cacheSharedResult()
	row.payloadMu.Lock()
	row.self, row.hasValue, row.persistedEnvelope = nil, false, &captured.Envelope
	row.payloadRevision++
	row.payloadMu.Unlock()
	c.SetPartContentSource(lifetimeChainSource{&testutil.Provider{InfoReaderProvider: chain.Provider}})
	arm(c, row, donor.cacheSharedResult())
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return row, c.RunLazyTask(ctx, receiver, "obtain:chain-loop", LazyTaskSpec{Body: func(ctx context.Context) error {
		source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
		if err != nil {
			return err
		}
		permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
		if err != nil {
			return errors.Join(err, source.Release(ctx))
		}
		return c.installChainPart(ctx, receiver, source, permit, demand)
	}})
}

// installChainPart's re-preparation loop records a counted Commit refusal
// itself and never hands it on: it retries, and what it returns to the obtain
// Body's caller is success, a hard error or its own uncounted refusal. So the
// refusal is recorded once, not once per loop it passes through.
//
// The loop rebuilds its source from what it knew before the download. That
// rebuilt source is downloadable: it names no source row, version or facts,
// and Commit's two source-row sites apply only to a Ready source. The only
// counted sites the loop can reach compare the receiver's payload revision,
// captured afresh by each round's preparation. So a donor whose counters moved
// once, here together with the receiver's, costs one reselect and no more.
func TestInstallChainPartRecordsOnce(t *testing.T) {
	demand := &PartDemandState{}
	var commits int
	row, err := chainLoopFixture(t, demand, func(c *Cache, receiver, donor *sharedResult) {
		c.testBeforePartCommit = func(*PreparedReadyPart) {
			if commits++; commits > 1 {
				return
			}
			receiver.storeSnapshotOwnerLinks(receiver.loadSnapshotOwnerLinks())
			donor.storeSnapshotOwnerLinks(donor.loadSnapshotOwnerLinks())
			c.egraphMu.Lock()
			donor.transferRevision++
			donor.dependencyOwnershipRevision++
			c.egraphMu.Unlock()
		}
	})
	require.NoError(t, err)
	require.Equal(t, 2, commits, "one refusal, then the install")
	require.Len(t, demand.progress, 1)
	for key := range demand.progress {
		require.Equal(t, "commit: receiver version", key.site)
		require.Equal(t, row.id, key.row, "the counters recorded are the receiver's")
	}
	require.Len(t, row.loadSnapshotOwnerLinks(), 1, "the chain was installed")
}

// The same loop with an expectation that is wrong every round: the second
// refusal repeats the first, and the hard error leaves the obtain Body.
func TestInstallChainPartStopsWithoutProgress(t *testing.T) {
	demand := &PartDemandState{}
	var commits int
	row, err := chainLoopFixture(t, demand, func(c *Cache, _, _ *sharedResult) {
		c.testBeforePartCommit = func(p *PreparedReadyPart) {
			commits++
			p.expectedRepresentation.payloadRevision += 100
		}
	})
	var stuck *PartNoProgressError
	require.ErrorAs(t, err, &stuck)
	require.Equal(t, 2, commits)
	require.Equal(t, "installChainPart", stuck.Loop)
	require.Equal(t, "commit: receiver representation", stuck.Site)
	require.Equal(t, uint64(row.id), stuck.ResultID)
	require.Len(t, demand.progress, 1)
}
