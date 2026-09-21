package server

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// The fixture controller is the consumer of the real renewal mailbox. With a
// reply armed for a chain's fingerprint it answers that request itself and
// never hands it out; otherwise a fixture call takes the delivered request and
// replies through the real ReplyRenewal, once.
func TestRemoteCacheFixtureRenewal(t *testing.T) {
	t.Run("off-gate there is no controller", func(t *testing.T) {
		t.Setenv(core.RemoteCacheFixtureRootEnv, "")
		srv := &Server{engineCache: newGCTestCache(t), shutdownCtx: t.Context()}
		require.Nil(t, srv.remoteCacheFixtureIntegration(nil), "no integration, no consumer and no bridge")
		require.Nil(t, srv.remoteCacheFixture)
		_, err := srv.RemoteCacheFixtureGC(t.Context())
		require.ErrorIs(t, err, errRemoteCacheFixtureDisabled)
		require.ErrorIs(t, srv.RemoteCacheFixtureArmRenewalReply(core.RemoteCacheFixtureRenewalReply{}), errRemoteCacheFixtureDisabled)
	})

	t.Setenv(core.RemoteCacheFixtureRootEnv, t.TempDir())
	configured := &RemoteCacheIntegrationConfig{Run: func(context.Context, *RemoteCacheAdapter) error { return nil }}
	require.Same(t, configured, (&Server{}).remoteCacheFixtureIntegration(configured), "a configured integration is never replaced")

	cache := newGCTestCache(t)
	srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
	require.NoError(t, srv.startRemoteCacheIntegration(srv.remoteCacheFixtureIntegration(nil)))
	t.Cleanup(func() { _ = srv.stopRemoteCacheIntegration(boundedContext(t)) })
	require.True(t, bridgeAttached(cache))

	offer := renewalOnlyOffer()
	layer := offer.Chain.Layers[0].Descriptor
	// A request's only route to the mailbox: the real content provider asked
	// for a blob that has no address.
	request := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			provider := cache.PartContentSource().Provider(boundedContext(t), offer, &dagql.PartDemandState{})
			_, err := provider.ReaderAt(boundedContext(t), layer)
			done <- err
		}()
		return done
	}

	t.Run("a delivered request is taken and answered once", func(t *testing.T) {
		done := request()
		renewal, err := srv.RemoteCacheFixtureTakeRenewal(boundedContext(t))
		require.NoError(t, err)
		require.Equal(t, "key", renewal.RenewalKey)
		require.Equal(t, layer.Digest, renewal.NeededBlob)
		require.Positive(t, renewal.RemainingDeadline)
		want, err := dagql.RenewalChainFingerprint(offer.Chain.Layers)
		require.NoError(t, err)
		require.Equal(t, want, renewal.Chain)

		reply := core.RemoteCacheFixtureRenewalReply{Epoch: renewal.Epoch, Sequence: renewal.Sequence, Chain: renewal.Chain, Unavailable: true}
		disposition, err := srv.RemoteCacheFixtureReplyRenewal(reply)
		require.NoError(t, err)
		require.Equal(t, "accepted", disposition)
		disposition, err = srv.RemoteCacheFixtureReplyRenewal(reply)
		require.NoError(t, err)
		require.Equal(t, "discarded", disposition, "a duplicate reply has no effect")
		select {
		case err := <-done:
			require.ErrorIs(t, err, dagql.ErrRenewalUnavailable)
		case <-time.After(10 * time.Second):
			t.Fatal("the requester never saw its reply")
		}
		_, err = srv.RemoteCacheFixtureReplyRenewal(core.RemoteCacheFixtureRenewalReply{Epoch: "zz"})
		require.ErrorContains(t, err, "hex bytes")
	})

	t.Run("an armed reply is sent by the consumer loop itself", func(t *testing.T) {
		require.ErrorContains(t, srv.RemoteCacheFixtureArmRenewalReply(core.RemoteCacheFixtureRenewalReply{Sequence: 1, Layers: offer.Chain.Layers}), "names no exchange")
		require.Error(t, srv.RemoteCacheFixtureArmRenewalReply(core.RemoteCacheFixtureRenewalReply{}), "a template needs a chain or its layers")
		require.NoError(t, srv.RemoteCacheFixtureArmRenewalReply(core.RemoteCacheFixtureRenewalReply{Layers: offer.Chain.Layers, Unavailable: true}))
		select {
		case err := <-request():
			require.ErrorIs(t, err, dagql.ErrRenewalUnavailable, "the armed reply reached the requester through the real mailbox")
		case <-time.After(10 * time.Second):
			t.Fatal("the armed reply was never sent")
		}
		// Nothing was handed out: a take now waits until its context ends.
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		_, err := srv.RemoteCacheFixtureTakeRenewal(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

// A reply paused at renewalReplied is inside the consumer's own Run
// goroutine. Stopping the adapter detaches the bridge and joins Run before
// the cache closes and releases barriers, so the pause must end at the
// detachment; otherwise Stop waits out its whole deadline and the engine
// cannot shut down cleanly.
func TestRemoteCacheFixtureStopReleasesPausedReply(t *testing.T) {
	t.Setenv(core.RemoteCacheFixtureRootEnv, t.TempDir())
	cache := newGCTestCache(t)
	cache.EnableTransferFixtureParts()
	srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
	require.NoError(t, srv.startRemoteCacheIntegration(srv.remoteCacheFixtureIntegration(nil)))

	armed, err := cache.ArmTransferFixtureBarrier(dagql.FixtureBarrierRequest{Key: "replied", Point: dagql.FixtureRenewalReplied, Action: dagql.FixturePause})
	require.NoError(t, err)
	offer := renewalOnlyOffer()
	require.NoError(t, srv.RemoteCacheFixtureArmRenewalReply(core.RemoteCacheFixtureRenewalReply{Layers: offer.Chain.Layers, Unavailable: true}))
	done := make(chan error, 1)
	go func() {
		provider := cache.PartContentSource().Provider(boundedContext(t), offer, &dagql.PartDemandState{})
		_, err := provider.ReaderAt(boundedContext(t), offer.Chain.Layers[0].Descriptor)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, dagql.ErrRenewalUnavailable, "the reply was published before the pause")
	case <-time.After(10 * time.Second):
		t.Fatal("the armed reply was never sent")
	}
	reached, err := cache.WaitTransferFixtureBarrier(boundedContext(t), "replied", armed.Generation)
	require.NoError(t, err)
	require.False(t, reached.Released, "the consumer is paused inside ReplyRenewal")

	stopCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.stopRemoteCacheIntegration(stopCtx), "detaching the bridge ends a pause at its points")
	require.NoError(t, cache.Close(boundedContext(t)))
}

// A delivered request nobody takes is dropped when its own Done closes, so the
// controller's records never outnumber the bridge's live exchanges. 65
// sequential requests, each canceled after its real delivery and never taken,
// leave nothing behind.
func TestRemoteCacheFixtureRetiresUntakenDeliveries(t *testing.T) {
	t.Setenv(core.RemoteCacheFixtureRootEnv, t.TempDir())
	cache := newGCTestCache(t)
	srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
	require.NoError(t, srv.startRemoteCacheIntegration(srv.remoteCacheFixtureIntegration(nil)))
	t.Cleanup(func() {
		// The test's own context is already canceled here.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, srv.stopRemoteCacheIntegration(ctx), "Run joined its watchers")
		require.NoError(t, cache.Close(ctx))
	})
	ctx := boundedContext(t)
	offer := renewalOnlyOffer()
	controller := srv.remoteCacheFixture
	// await blocks on the controller's own change signal until cond holds.
	await := func(what string, cond func() bool) {
		t.Helper()
		for {
			controller.mu.Lock()
			ok, changed := cond(), controller.ready
			controller.mu.Unlock()
			if ok {
				return
			}
			select {
			case <-changed:
			case <-ctx.Done():
				t.Fatalf("%s: %d delivered records remain", what, len(controller.delivered))
			}
		}
	}
	for i := 0; i < 65; i++ {
		requestCtx, cancelRequest := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			provider := cache.PartContentSource().Provider(requestCtx, offer, &dagql.PartDemandState{})
			_, err := provider.ReaderAt(requestCtx, offer.Chain.Layers[0].Descriptor)
			done <- err
		}()
		await("the request was never delivered", func() bool { return len(controller.delivered) == 1 })
		cancelRequest()
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("the canceled requester never returned")
		}
		await("a request whose Done closed was not retired", func() bool { return len(controller.delivered) == 0 })
	}
}

// Run's contract is to return once its lifetime context is canceled. A reply
// paused at renewalReplied is inside Run's own goroutine and only a
// detachment ends that pause, while the server's wrapper detaches only after
// Run returns. The fixture's Run therefore detaches its adapter itself when
// its lifetime ends; cancellation alone, with no Stop, must let Run return.
func TestRemoteCacheFixtureLifetimeCancelReleasesPausedReply(t *testing.T) {
	t.Setenv(core.RemoteCacheFixtureRootEnv, t.TempDir())
	cache := newGCTestCache(t)
	cache.EnableTransferFixtureParts()
	lifetime, cancelLifetime := context.WithCancel(t.Context())
	defer cancelLifetime()
	srv := &Server{engineCache: cache, shutdownCtx: lifetime}
	require.NoError(t, srv.startRemoteCacheIntegration(srv.remoteCacheFixtureIntegration(nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = cache.ReleaseTransferFixtureBarrier("replied", 0)
		require.NoError(t, srv.stopRemoteCacheIntegration(ctx))
		require.NoError(t, cache.Close(ctx))
	})

	armed, err := cache.ArmTransferFixtureBarrier(dagql.FixtureBarrierRequest{Key: "replied", Point: dagql.FixtureRenewalReplied, Action: dagql.FixturePause})
	require.NoError(t, err)
	offer := renewalOnlyOffer()
	require.NoError(t, srv.RemoteCacheFixtureArmRenewalReply(core.RemoteCacheFixtureRenewalReply{Layers: offer.Chain.Layers, Unavailable: true}))
	done := make(chan error, 1)
	go func() {
		provider := cache.PartContentSource().Provider(boundedContext(t), offer, &dagql.PartDemandState{})
		_, err := provider.ReaderAt(boundedContext(t), offer.Chain.Layers[0].Descriptor)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, dagql.ErrRenewalUnavailable)
	case <-time.After(10 * time.Second):
		t.Fatal("the armed reply was never sent")
	}
	_, err = cache.WaitTransferFixtureBarrier(boundedContext(t), "replied", armed.Generation)
	require.NoError(t, err)

	cancelLifetime()
	select {
	case <-srv.remoteCacheAdapter.runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its lifetime was canceled")
	}
	renewals, err := srv.RemoteCacheFixtureRenewals()
	require.NoError(t, err)
	require.Len(t, renewals.ArmedReplies, 1)
	require.Equal(t, "accepted", renewals.ArmedReplies[0].Disposition, "the published reply keeps its disposition")
}

// The armed-reply history is an observation like any other: bounded by the
// scenario's cap, an overflow is reported instead of dropping a record
// silently, and a new observation scope starts it empty.
func TestRemoteCacheFixtureArmedReplyHistoryIsBounded(t *testing.T) {
	t.Setenv(core.RemoteCacheFixtureRootEnv, t.TempDir())
	cache := newGCTestCache(t)
	srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
	require.NoError(t, srv.startRemoteCacheIntegration(srv.remoteCacheFixtureIntegration(nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, srv.stopRemoteCacheIntegration(ctx))
		require.NoError(t, cache.Close(ctx))
	})
	offer := renewalOnlyOffer()
	reply := func() {
		t.Helper()
		require.NoError(t, srv.RemoteCacheFixtureArmRenewalReply(core.RemoteCacheFixtureRenewalReply{Layers: offer.Chain.Layers, Unavailable: true}))
		provider := cache.PartContentSource().Provider(boundedContext(t), offer, &dagql.PartDemandState{})
		_, err := provider.ReaderAt(boundedContext(t), offer.Chain.Layers[0].Descriptor)
		require.ErrorIs(t, err, dagql.ErrRenewalUnavailable)
	}

	require.NoError(t, srv.RemoteCacheFixtureObserve(1))
	reply()
	renewals, err := srv.RemoteCacheFixtureRenewals()
	require.NoError(t, err)
	require.Len(t, renewals.ArmedReplies, 1)
	require.False(t, renewals.Overflowed)
	reply()
	reply()
	renewals, err = srv.RemoteCacheFixtureRenewals()
	require.NoError(t, err)
	require.Len(t, renewals.ArmedReplies, 1, "the history never grows past the scenario's bound")
	require.True(t, renewals.Overflowed, "and says so, instead of dropping records silently")

	require.NoError(t, srv.RemoteCacheFixtureObserve(8))
	renewals, err = srv.RemoteCacheFixtureRenewals()
	require.NoError(t, err)
	require.Empty(t, renewals.ArmedReplies, "a new observation scope starts empty")
	require.False(t, renewals.Overflowed)
	require.Zero(t, renewals.Delivered+renewals.Taken+renewals.Retired)
}
