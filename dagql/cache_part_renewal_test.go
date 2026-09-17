package dagql

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func renewalTestLayers(values ...string) []snapshots.ExportLayer {
	layers := make([]snapshots.ExportLayer, len(values))
	for i, value := range values {
		layers[i] = snapshots.ExportLayer{Descriptor: ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer, Digest: digest.FromString(value), Size: int64(len(value))}}
	}
	return layers
}

func renewalTestRequest(t *testing.T, layers []snapshots.ExportLayer) RenewalRequest {
	t.Helper()
	chain, err := renewalChainFingerprint(layers)
	require.NoError(t, err)
	return RenewalRequest{Chain: chain, RenewalKey: "key", Layers: layers, NeededBlob: layers[len(layers)-1].Descriptor.Digest, Deadline: time.Now().Add(renewalDeadline)}
}

type renewalTestResult struct {
	addresses map[digest.Digest]BlobAddress
	err       error
}

func startRenewal(ctx context.Context, bridge *RemoteCacheBridge, request RenewalRequest) <-chan renewalTestResult {
	out := make(chan renewalTestResult, 1)
	go func() {
		addresses, err := bridge.request(ctx, request)
		out <- renewalTestResult{addresses, err}
	}()
	return out
}

func takeNow(t *testing.T, bridge *RemoteCacheBridge) *RenewalRequest {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	bridge.mu.Lock()
	queued := len(bridge.queue) > 0
	bridge.mu.Unlock()
	require.True(t, queued, "take requires a queued request")
	request, err := bridge.TakeRenewalRequest(ctx)
	require.NoError(t, err)
	return request
}

func liveExchanges(bridge *RemoteCacheBridge) (live, queued int) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return len(bridge.exchanges), len(bridge.queue)
}

func TestRenewalMailbox(t *testing.T) {
	layers := renewalTestLayers("lower", "upper")
	t.Run("capacity and deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bridge, err := newRemoteCacheBridge(new(sync.Mutex))
			require.NoError(t, err)
			results := make([]<-chan renewalTestResult, renewalMailboxCapacity)
			for i := range results {
				results[i] = startRenewal(t.Context(), bridge, renewalTestRequest(t, layers))
			}
			synctest.Wait()
			for range renewalMailboxCapacity / 2 {
				takeNow(t, bridge)
			}
			live, queued := liveExchanges(bridge)
			require.Equal(t, renewalMailboxCapacity, live, "taking a request does not free capacity")
			require.Equal(t, renewalMailboxCapacity/2, queued)
			started := time.Now()
			_, err = bridge.request(t.Context(), renewalTestRequest(t, layers))
			require.ErrorIs(t, err, ErrRenewalUnavailable)
			require.ErrorContains(t, err, "mailbox full")
			require.Equal(t, started, time.Now(), "a full mailbox refuses without waiting")

			time.Sleep(renewalDeadline)
			synctest.Wait()
			for _, result := range results {
				got := <-result
				require.ErrorIs(t, got.err, ErrRenewalUnavailable)
				require.ErrorContains(t, got.err, "deadline")
			}
			live, queued = liveExchanges(bridge)
			require.Zero(t, live, "timeout frees queued and delivered exchanges")
			require.Zero(t, queued)

			// A request taken late keeps only its remaining time.
			late := startRenewal(t.Context(), bridge, renewalTestRequest(t, layers))
			synctest.Wait()
			time.Sleep(1800 * time.Millisecond)
			request := takeNow(t, bridge)
			require.Equal(t, 200*time.Millisecond, time.Until(request.Deadline))
			select {
			case <-request.Done:
				t.Fatal("live request reported done")
			default:
			}
			time.Sleep(200 * time.Millisecond)
			synctest.Wait()
			require.ErrorIs(t, (<-late).err, ErrRenewalUnavailable)
			<-request.Done
			require.Equal(t, RenewalReplyDiscarded, bridge.ReplyRenewal(RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{request.NeededBlob: {URL: "https://late.invalid/blob"}}}))
		})
	})
	t.Run("cancellation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bridge, err := newRemoteCacheBridge(new(sync.Mutex))
			require.NoError(t, err)
			queuedCtx, cancelQueued := context.WithCancel(t.Context())
			deliveredCtx, cancelDelivered := context.WithCancel(t.Context())
			delivered := startRenewal(deliveredCtx, bridge, renewalTestRequest(t, layers))
			synctest.Wait()
			request := takeNow(t, bridge)
			queued := startRenewal(queuedCtx, bridge, renewalTestRequest(t, layers))
			synctest.Wait()
			cancelQueued()
			cancelDelivered()
			synctest.Wait()
			require.ErrorIs(t, (<-queued).err, context.Canceled)
			require.ErrorIs(t, (<-delivered).err, context.Canceled)
			<-request.Done
			live, pending := liveExchanges(bridge)
			require.Zero(t, live)
			require.Zero(t, pending, "a canceled queued request is compacted, not left for Take")
			require.Equal(t, RenewalReplyDiscarded, bridge.ReplyRenewal(RenewalReply{ID: request.ID, Chain: request.Chain}))

			// Canceling a Take does not cancel a delivered exchange.
			result := startRenewal(t.Context(), bridge, renewalTestRequest(t, layers))
			synctest.Wait()
			request = takeNow(t, bridge)
			takeCtx, cancelTake := context.WithCancel(t.Context())
			taken := make(chan error, 1)
			go func() {
				_, err := bridge.TakeRenewalRequest(takeCtx)
				taken <- err
			}()
			synctest.Wait()
			cancelTake()
			require.ErrorIs(t, <-taken, context.Canceled)
			require.Equal(t, RenewalReplyAccepted, bridge.ReplyRenewal(RenewalReply{ID: request.ID, Chain: request.Chain, Unavailable: true}))
			got := <-result
			require.ErrorIs(t, got.err, ErrRenewalUnavailable)
			require.ErrorContains(t, got.err, "integration reported no address")
		})
	})
	t.Run("replies", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bridge, err := newRemoteCacheBridge(new(sync.Mutex))
			require.NoError(t, err)
			other, err := newRemoteCacheBridge(new(sync.Mutex))
			require.NoError(t, err)
			result := startRenewal(t.Context(), bridge, renewalTestRequest(t, layers))
			synctest.Wait()
			request := takeNow(t, bridge)
			require.Equal(t, layers, request.Layers)
			request.Layers[0].Descriptor.Size = -1
			fresh := map[digest.Digest]BlobAddress{request.NeededBlob: {URL: "https://renewed.invalid/upper", ExpiresAtUnix: 7}}
			otherLayers := renewalTestLayers("lower", "different")
			otherChain, err := renewalChainFingerprint(otherLayers)
			require.NoError(t, err)
			reordered, err := renewalChainFingerprint(renewalTestLayers("upper", "lower"))
			require.NoError(t, err)
			oldEpoch := request.ID
			oldEpoch.Epoch = other.epoch
			for name, reply := range map[string]RenewalReply{
				"wrong content":       {ID: request.ID, Chain: otherChain, Addresses: fresh},
				"reordered content":   {ID: request.ID, Chain: reordered, Addresses: fresh},
				"unknown digest":      {ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{otherLayers[1].Descriptor.Digest: {URL: "https://renewed.invalid/x"}}},
				"negative with value": {ID: request.ID, Chain: request.Chain, Unavailable: true, Addresses: fresh},
				"old epoch":           {ID: oldEpoch, Chain: request.Chain, Addresses: fresh},
				"unknown sequence":    {ID: RenewalRequestID{Epoch: request.ID.Epoch, Sequence: request.ID.Sequence + 1}, Chain: request.Chain, Addresses: fresh},
			} {
				require.Equal(t, RenewalReplyDiscarded, bridge.ReplyRenewal(reply), name)
			}
			synctest.Wait()
			select {
			case <-request.Done:
				t.Fatal("rejected replies must leave the exchange live")
			default:
			}
			time.Sleep(time.Second)
			require.Equal(t, RenewalReplyAccepted, bridge.ReplyRenewal(RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: fresh}))
			fresh[request.NeededBlob] = BlobAddress{URL: "https://caller.invalid/mutated"}
			require.Equal(t, RenewalReplyDiscarded, bridge.ReplyRenewal(RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: fresh}), "duplicate")
			got := <-result
			require.NoError(t, got.err)
			require.Equal(t, map[digest.Digest]BlobAddress{request.NeededBlob: {URL: "https://renewed.invalid/upper", ExpiresAtUnix: 7}}, got.addresses)
			<-request.Done
			live, _ := liveExchanges(bridge)
			require.Zero(t, live)
		})
	})
	t.Run("detach", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mu := new(sync.Mutex)
			bridge, err := newRemoteCacheBridge(mu)
			require.NoError(t, err)
			delivered := startRenewal(t.Context(), bridge, renewalTestRequest(t, layers))
			synctest.Wait()
			request := takeNow(t, bridge)
			queued := startRenewal(t.Context(), bridge, renewalTestRequest(t, layers))
			taken := make(chan error, 1)
			synctest.Wait()
			takeNow(t, bridge)
			go func() {
				_, err := bridge.TakeRenewalRequest(t.Context())
				taken <- err
			}()
			synctest.Wait()
			mu.Lock()
			bridge.closeLocked()
			bridge.closeLocked()
			mu.Unlock()
			require.ErrorIs(t, <-taken, ErrRemoteCacheBridgeClosed)
			require.ErrorContains(t, (<-delivered).err, "bridge detached")
			require.ErrorContains(t, (<-queued).err, "bridge detached")
			<-request.Done
			_, err = bridge.request(t.Context(), renewalTestRequest(t, layers))
			require.ErrorContains(t, err, "bridge detached")
			require.Equal(t, RenewalReplyDiscarded, bridge.ReplyRenewal(RenewalReply{ID: request.ID, Chain: request.Chain, Unavailable: true}))
		})
	})
}

func TestRenewalEpisodeSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		layers := renewalTestLayers("lower", "upper")
		demand := &PartDemandState{target: PersistedPartAddress{Part: "snapshot"}}
		key := renewalEpisodeKey{target: demand.targetKey(), content: renewalContentKey(layers)}
		annotated := renewalTestLayers("lower", "upper")
		annotated[1].Descriptor.Annotations = map[string]string{"changed": "description"}
		now := time.Now()
		annotated[1].CreatedAt = &now
		require.Equal(t, key.content, renewalContentKey(annotated), "descriptions and timestamps do not renew a content budget")
		annotatedChain, err := renewalChainFingerprint(annotated)
		require.NoError(t, err)
		plainChain, err := renewalChainFingerprint(layers)
		require.NoError(t, err)
		require.NotEqual(t, plainChain, annotatedChain, "replies correlate with the exact layer records")
		require.NotEqual(t, key.content, renewalContentKey(renewalTestLayers("lower", "other")))

		episode, claimed := demand.claimRenewal(key, false, time.Now().Add(renewalDeadline))
		require.Nil(t, episode, "an absent bridge or key cannot claim")
		require.False(t, claimed)
		episode, claimed = demand.claimRenewal(key, true, time.Now().Add(renewalDeadline))
		require.True(t, claimed)
		again, claimed := demand.claimRenewal(key, true, time.Now().Add(time.Hour))
		require.False(t, claimed)
		require.Same(t, episode, again)
		waited := make(chan error, 1)
		go func() {
			_, err := demand.awaitRenewal(t.Context(), again)
			waited <- err
		}()
		synctest.Wait()
		supplied := map[digest.Digest]BlobAddress{layers[1].Descriptor.Digest: {URL: "https://renewed.invalid/upper"}}
		demand.settleRenewal(episode, supplied, nil)
		demand.settleRenewal(episode, nil, errors.New("second terminal result is ignored"))
		require.NoError(t, <-waited)
		addresses, err := demand.awaitRenewal(t.Context(), episode)
		require.NoError(t, err)
		require.Equal(t, supplied, addresses)
		// The deadline bounds waiting for a reply, not a supplied result's use.
		// Consulting it after the deadline must return the result every time.
		time.Sleep(time.Hour)
		for range 100 {
			addresses, err = demand.awaitRenewal(t.Context(), again)
			require.NoError(t, err)
			require.Equal(t, supplied, addresses)
		}
		require.Zero(t, demand.revision, "episodes never invalidate a SourceCheck")

		// Chain failure records the renewed episode as exhausted once, through
		// the ordinary exhaustion revision.
		offer := &PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Chain: OfferedChain{Layers: annotated, RenewalKey: "rotated"}}
		demand.exhaust(&PartSourceLease{sourceID: 9, descriptor: PartDescriptor{Address: offer.Address}, offer: offer, offerRev: 3}, errors.New("digest mismatch"))
		require.EqualValues(t, 1, demand.revision)
		_, err = demand.awaitRenewal(t.Context(), episode)
		require.ErrorIs(t, err, ErrRenewalUnavailable)
		_, claimed = demand.claimRenewal(key, true, time.Now().Add(renewalDeadline))
		require.False(t, claimed, "a rotated key or changed record cannot claim again")

		// Another target has its own episode; a waiter stops at the original deadline.
		other := renewalEpisodeKey{target: (&PartDemandState{target: PersistedPartAddress{Part: "mount:/src"}}).targetKey(), content: key.content}
		pending, claimed := demand.claimRenewal(other, true, time.Now().Add(renewalDeadline))
		require.True(t, claimed)
		started := time.Now()
		_, err = demand.awaitRenewal(t.Context(), pending)
		require.ErrorIs(t, err, ErrRenewalUnavailable)
		require.Equal(t, renewalDeadline, time.Since(started))
		require.EqualValues(t, 1, demand.revision)
	})
}

// A provider without a key or bridge uses an episode another source settled,
// even when it consults the episode after that episode's deadline.
func TestRenewalSettledEpisodeAfterDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const data = "renewed bytes"
		transport, source, offer, descriptor := contentReaderFixture(t, data)
		offer.Chain.Addresses[descriptor.Digest] = BlobAddress{URL: "https://expired.invalid/blob", ExpiresAtUnix: 1}
		demand := &PartDemandState{target: PersistedPartAddress{Part: "snapshot"}}
		key := renewalEpisodeKey{target: demand.targetKey(), content: renewalContentKey(offer.Chain.Layers)}
		episode, claimed := demand.claimRenewal(key, true, time.Now().Add(renewalDeadline))
		require.True(t, claimed)
		demand.settleRenewal(episode, map[digest.Digest]BlobAddress{descriptor.Digest: {URL: "https://blobs.invalid/blob"}}, nil)
		time.Sleep(2 * renewalDeadline)
		for range 20 {
			reader, err := source.Provider(t.Context(), offer, demand).ReaderAt(t.Context(), descriptor)
			require.NoError(t, err)
			buf := make([]byte, len(data))
			n, err := reader.ReadAt(buf, 0)
			require.NoError(t, err)
			require.Equal(t, data, string(buf[:n]))
			require.NoError(t, reader.Close())
		}
		require.Equal(t, 20, transport.count("https://blobs.invalid/blob"))
		require.Zero(t, transport.count("https://expired.invalid/blob"))
	})
}

func TestRenewalClaimKeepsSourceCheck(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	address := testLiveOffer().Address
	require.NoError(t, c.RunLazyTask(ctx, r, "lazy:renewal-claim", LazyTaskSpec{Body: func(ctx context.Context) error {
		drain, outcome, err := c.PrepareOriginal(ctx, r, LazyGroupAddress{Group: "exec"}, []PersistedPartAddress{address}, PartTaskFromContext(ctx))
		require.NoError(t, err)
		require.Equal(t, GateGranted, outcome)
		require.NoError(t, drain.Wait(ctx))
		demand := &PartDemandState{target: address}
		scan, err := c.CheckPartSources(ctx, r, address, drain, demand)
		require.NoError(t, err)
		require.NotNil(t, scan.NoSource)
		episode, claimed := demand.claimRenewal(renewalEpisodeKey{target: demand.targetKey(), content: renewalContentKey(renewalTestLayers("blob"))}, true, time.Now().Add(renewalDeadline))
		require.True(t, claimed)
		demand.settleRenewal(episode, nil, renewalUnavailable("test"))
		_, outcome, err = c.BeginOriginal(ctx, scan.NoSource)
		require.NoError(t, err)
		require.Equal(t, GateGranted, outcome)
		return nil
	}}))
}

// within bounds a wait outside a synctest bubble.
func within[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting")
		panic("unreachable")
	}
}

// waitQueued blocks on the mailbox's ready signal until live exchanges exist.
func waitQueued(t *testing.T, bridge *RemoteCacheBridge, live int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		bridge.mu.Lock()
		n, ready := len(bridge.exchanges), bridge.ready
		bridge.mu.Unlock()
		if n >= live {
			return
		}
		select {
		case <-ready:
		case <-deadline:
			t.Fatalf("timed out waiting for %d live exchanges, have %d", live, n)
		}
	}
}

func TestRemoteCacheBridgeAttachment(t *testing.T) {
	layers := renewalTestLayers("lower", "upper")
	ctx, c, _ := transferTestCache(t)
	source := c.PartContentSource()
	require.Nil(t, source.bridge.Load())
	first, created, err := c.AttachRemoteCacheBridge()
	require.NoError(t, err)
	require.True(t, created)
	again, created, err := c.AttachRemoteCacheBridge()
	require.NoError(t, err)
	require.False(t, created)
	require.Same(t, first, again, "a live attachment is returned, not replaced")
	require.Same(t, first, source.bridge.Load())

	pending := startRenewal(ctx, first, renewalTestRequest(t, layers))
	waitQueued(t, first, 1)
	oldRequest := takeNow(t, first)
	require.False(t, c.DetachRemoteCacheBridge(nil))
	require.True(t, c.DetachRemoteCacheBridge(first))
	require.False(t, c.DetachRemoteCacheBridge(first), "repeated detach")
	require.ErrorContains(t, within(t, pending).err, "bridge detached")
	within(t, oldRequest.Done)
	require.Nil(t, source.bridge.Load())

	second, created, err := c.AttachRemoteCacheBridge()
	require.NoError(t, err)
	require.True(t, created)
	require.NotSame(t, first, second)
	require.NotEqual(t, first.epoch, second.epoch, "each attachment has a fresh epoch")
	require.False(t, c.DetachRemoteCacheBridge(first), "a stale detach cannot close its successor")
	require.Same(t, second, source.bridge.Load())
	result := startRenewal(ctx, second, renewalTestRequest(t, layers))
	waitQueued(t, second, 1)
	request := takeNow(t, second)
	require.Equal(t, oldRequest.ID.Sequence, request.ID.Sequence)
	stale := RenewalReply{ID: oldRequest.ID, Chain: request.Chain, Unavailable: true}
	require.Equal(t, RenewalReplyDiscarded, second.ReplyRenewal(stale), "an old attachment's reply cannot match")
	require.Equal(t, RenewalReplyAccepted, second.ReplyRenewal(RenewalReply{ID: request.ID, Chain: request.Chain, Unavailable: true}))
	require.ErrorIs(t, within(t, result).err, ErrRenewalUnavailable)

	// Cache close detaches before waiting for operations and refuses a later
	// attachment; an idle consumer cannot hold close open.
	op, err := c.beginCacheOperation()
	require.NoError(t, err)
	finished := false
	finish := func() {
		if !finished {
			finished = true
			op.finish(false)
		}
	}
	defer finish()
	pending = startRenewal(ctx, second, renewalTestRequest(t, layers))
	waitQueued(t, second, 1)
	delivered := takeNow(t, second)
	taken := make(chan error, 1)
	go func() {
		_, err := second.TakeRenewalRequest(ctx)
		taken <- err
	}()
	// A failed check cancels close before the operation ends, so cleanup
	// cannot wait on this close.
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelClose()
	closed := make(chan error, 1)
	go func() { closed <- c.Close(closeCtx) }()
	require.ErrorContains(t, within(t, pending).err, "bridge detached")
	within(t, delivered.Done)
	require.ErrorIs(t, within(t, taken), ErrRemoteCacheBridgeClosed)
	_, _, err = c.AttachRemoteCacheBridge()
	require.ErrorIs(t, err, ErrCacheClosed)
	select {
	case err := <-closed:
		t.Fatalf("close finished before the active operation: %v", err)
	default:
	}
	finish()
	require.NoError(t, within(t, closed))
	require.False(t, c.DetachRemoteCacheBridge(second))
}
