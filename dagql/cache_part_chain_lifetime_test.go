package dagql

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

type lifetimeChainSource struct{ provider content.InfoReaderProvider }

func (s lifetimeChainSource) Provider(context.Context, PersistedPartOffer, *PartDemandState) content.InfoReaderProvider {
	return s.provider
}

type lifetimeFailManager struct {
	snapshots.SnapshotManager
	fail atomic.Bool
}

var lifetimeSyncFailure = errors.New("owner sync acknowledgement failed")

func (m *lifetimeFailManager) AttachLease(ctx context.Context, id, ref string) error {
	if err := m.SnapshotManager.AttachLease(ctx, id, ref); err != nil {
		return err
	}
	if m.fail.Swap(false) {
		return lifetimeSyncFailure
	}
	return nil
}

func TestPartAdmittedChainLifetime(t *testing.T) {
	for _, mode := range []string{"donor-collected", "slot-replaced", "owner-backref-sync-failure", "output-backref-rejected"} {
		t.Run(mode, func(t *testing.T) {
			a, b := testutil.NewStore(t), testutil.NewStore(t)
			ref, _ := a.Build(t, nil, "payload", "admitted chain bytes")
			chain, err := ref.ExportChain(t.Context(), config.RefConfig{Compression: compression.New(compression.Uncompressed)})
			require.NoError(t, err)
			defer func() { require.NoError(t, chain.Release(context.Background())) }()
			ctx, c, srv := transferTestCache(t)
			manager := &lifetimeFailManager{SnapshotManager: b.Manager}
			c.snapshotManager = manager
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
			donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "pending"})
			dependency := persistedListTestResult(t, ctx, c, srv, "owner-dependency", String("owner only"))
			if mode == "owner-backref-sync-failure" || mode == "output-backref-rejected" {
				transferTestDependency(c, ctx, dependency, receiver)
			}
			partTestEquivalent(t, ctx, c, receiver, donor)
			address := PersistedPartAddress{Part: "snapshot"}
			record := PersistedPartOffer{Address: address, Value: SnapshotValue{Kind: "directory", Path: "/"}, Chain: OfferedChain{Layers: chain.Layers, RenewalKey: "in-process"}, Owner: PersistedOfferOwner{DependencyIDs: []uint64{uint64(dependency.cacheSharedResult().id)}}}
			if mode == "output-backref-rejected" {
				record.Value.Services = []TransferredServiceBinding{{ServiceResultID: uint64(dependency.cacheSharedResult().id)}}
			}
			c.egraphMu.Lock()
			owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
			require.NoError(t, err)
			require.NoError(t, c.attachPartOfferLocked(donor.cacheSharedResult(), address, &partOffer{record: record, owner: owner}))
			c.egraphMu.Unlock()
			captured, err := c.CapturePersistedRecord(ctx, receiver)
			require.NoError(t, err)
			row := receiver.cacheSharedResult()
			row.payloadMu.Lock()
			row.self = nil
			row.hasValue = false
			row.persistedEnvelope = &captured.Envelope
			row.payloadRevision++
			row.payloadMu.Unlock()
			entered, resume := make(chan struct{}), make(chan struct{})
			provider := &testutil.Provider{InfoReaderProvider: chain.Provider, BeforeRead: func(ctx context.Context, _ ocispec.Descriptor) error {
				close(entered)
				select {
				case <-resume:
					return nil
				case <-ctx.Done():
					return context.Cause(ctx)
				}
			}}
			c.SetPartContentSource(lifetimeChainSource{provider})
			demandCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "demand-client", SessionID: "demand"})
			done := make(chan error, 1)
			go func() {
				done <- c.RunLazyTask(demandCtx, receiver, "obtain:lifetime", LazyTaskSpec{Body: func(ctx context.Context) error {
					source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
					if err != nil {
						return err
					}
					if source == nil || source.source != nil {
						return errors.New("expected admitted owner without donor hold")
					}
					permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
					if err != nil {
						return err
					}
					return c.installChainPart(ctx, receiver, source, permit, &PartDemandState{})
				}})
			}()
			select {
			case <-entered:
			case err := <-done:
				require.NoError(t, err)
				t.Fatal("provider not reached")
			}
			if mode == "slot-replaced" {
				c.egraphMu.Lock()
				replacement, err := c.newOfferOwnerLocked(ctx, record.Owner)
				require.NoError(t, err)
				q, err := c.replacePartOfferLocked(ctx, donor.cacheSharedResult(), address, &partOffer{record: record, owner: replacement})
				require.NoError(t, err)
				callbacks, err := c.collectUnownedResultsLocked(ctx, q)
				c.egraphMu.Unlock()
				require.NoError(t, err)
				require.NoError(t, runOnReleaseFuncs(ctx, callbacks))
			} else if mode == "donor-collected" {
				require.NoError(t, c.ReleaseSession(ctx, "test-session"))
				_, err := c.removePersistedEdge(ctx, donor.cacheSharedResult().id)
				require.NoError(t, err)
				c.egraphMu.RLock()
				require.Nil(t, c.resultsByID[donor.cacheSharedResult().id])
				require.Same(t, owner, c.offerOwners[owner.id])
				c.egraphMu.RUnlock()
			}
			if mode == "owner-backref-sync-failure" {
				manager.fail.Store(true)
			}
			close(resume)
			err = <-done
			switch mode {
			case "output-backref-rejected":
				require.ErrorContains(t, err, "cycle")
				require.Empty(t, row.loadSnapshotOwnerLinks())
			case "owner-backref-sync-failure":
				require.ErrorIs(t, err, lifetimeSyncFailure)
			default:
				require.NoError(t, err)
			}
			c.egraphMu.RLock()
			require.Equal(t, owner.slots, owner.holds, "acquisition hold dropped before sync or on rejection")
			c.egraphMu.RUnlock()
			if mode != "output-backref-rejected" {
				links := row.loadSnapshotOwnerLinks()
				if mode == "owner-backref-sync-failure" {
					links = row.loadPayloadState().snapshotLinkIntent.Links
				}
				require.Len(t, links, 1)
				opened, err := b.Manager.GetBySnapshotID(ctx, links[0].RefKey)
				require.NoError(t, err)
				testutil.CheckFile(t, opened, "payload", "admitted chain bytes")
				require.NoError(t, opened.Release(ctx))
			}
			require.NoError(t, c.ReleaseSession(ctx, "test-session"))
			for _, res := range []AnyResult{donor, dependency, receiver} {
				_, err := c.removePersistedEdge(ctx, res.cacheSharedResult().id)
				require.NoError(t, err)
			}
			c.egraphMu.RLock()
			require.Nil(t, c.resultsByID[row.id], "no retained owner/pin self-cycle")
			require.Empty(t, c.offerOwners)
			c.egraphMu.RUnlock()
			require.EqualValues(t, 1, provider.Reads.Load())
		})
	}
}

func (lifetimeChainSource) Available(PersistedPartOffer, int64) bool { return true }
