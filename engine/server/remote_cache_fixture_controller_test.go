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
