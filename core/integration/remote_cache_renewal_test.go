package core

import (
	"context"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// renewalScenario is one built Directory, which has a saved producer, exported
// by A with its selected chain. Before B imports it the harness gives the
// offer a renewal key and no address, as an integration whose addresses have
// expired would: B's first content read must then ask the real renewal
// mailbox, and the blobs are fetched over the real HTTP content path from the
// fixture's content host.
type renewalScenario struct {
	a, b    *fixtureEngine
	payload string
	layers  []any
	digests []string
	rHandle string
	rID     uint64
}

func newRenewalScenario(ctx context.Context, t *testctx.T, name string, expired bool) *renewalScenario {
	outer := connect(ctx, t)
	s := &renewalScenario{
		a:       newFixtureEngine(ctx, t, outer, name+"-a", true),
		b:       newFixtureEngine(ctx, t, outer, name+"-b", true),
		payload: "renewed payload " + identity.NewID(),
	}
	dir, err := s.a.client.Directory().WithNewFile("payload.txt", s.payload).Sync(ctx)
	require.NoError(t, err)
	id, err := dir.ID(ctx)
	require.NoError(t, err)
	var exported fixtureExportSelectedResult
	require.NoError(t, s.a.fixture("exportSelected", s.a.control("export.json", map[string]any{"bundle": "renewal.json", "outputs": []map[string]any{{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), []string{string(id)}, &exported))
	require.Len(t, exported.Outputs, 1)
	require.NotEmpty(t, exported.Outputs[0].Layers)
	for _, layer := range exported.Outputs[0].Layers {
		s.digests = append(s.digests, layer.Digest)
	}
	s.a.editBundle("renewal.json", func(bundle map[string]any) {
		outputs := bundle["outputs"].([]any)
		require.Len(t, outputs, 1)
		chain := outputs[0].(map[string]any)["chain"].(map[string]any)
		chain["renewalKey"] = "b7-renewal-key"
		delete(chain, "addresses")
		if expired {
			// Addresses that were valid once: every blob has one, all expired.
			addresses := map[string]any{}
			for _, blob := range s.digests {
				addresses[blob] = map[string]any{"url": s.contentURL(blob) + "?expired", "expiresAtUnix": time.Now().Add(-time.Hour).Unix()}
			}
			chain["addresses"] = addresses
		}
		s.layers = chain["layers"].([]any)
	})
	s.a.copyFixtureTo(s.b, "renewal.json")
	return s
}

func (s *renewalScenario) contentURL(blob string) string {
	return "https://" + fixturetransport.ContentHost + "/" + blob
}

// scriptContent maps each layer's content URL to B's copied blob file.
func (s *renewalScenario) scriptContent(t *testctx.T) {
	var responses []fixturetransport.Response
	for _, blob := range s.digests {
		alg, hex, ok := strings.Cut(blob, ":")
		require.True(t, ok)
		responses = append(responses, fixturetransport.Response{URL: s.contentURL(blob), BodyFile: "blobs/" + alg + "/" + hex, Rangeable: true})
	}
	scriptOrigin(t, s.b, responses...)
}

func (s *renewalScenario) importOnB(t *testctx.T) {
	var imported []transferFixtureMapping
	require.NoError(t, s.b.fixture("import", "renewal.json", nil, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Directory", imported[0].Type.NamedType)
	s.rHandle, s.rID = imported[0].Handle, imported[0].ResultID
}

// TestRenewal is the native renewal row (design §5 as amended by B1, F5): the
// mailbox's own rules are proven in process; here a real demand, the real
// mailbox, the real HTTP content path and a real fallback meet.
func (RemoteCacheTransferSuite) TestRenewal(ctx context.Context, t *testctx.T) {
	// Success. The reply is staged before the demand (F5), so the fixture's
	// consumer loop answers inside the two second window with no harness
	// round trip. The reply must be accepted, the bytes must come from the
	// content host, and the saved producer must never be entered.
	t.Run("ArmedReplySucceeds", func(ctx context.Context, t *testctx.T) {
		s := newRenewalScenario(ctx, t, "renewal-ok", false)
		s.scriptContent(t)
		addresses := map[string]any{}
		for _, blob := range s.digests {
			addresses[blob] = map[string]any{"url": s.contentURL(blob), "expiresAtUnix": time.Now().Add(time.Hour).Unix()}
		}
		require.NoError(t, s.b.fixture("armRenewalReply", s.b.control("reply.json", map[string]any{"layers": s.layers, "addresses": addresses}), nil, nil))
		s.importOnB(t)

		contents, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(s.rHandle)).File("payload.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, s.payload, contents)

		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		t.Logf("R=%d events: %v", s.rID, partKindsOf(report.transferFixtureReport, s.rID))
		require.NotNil(t, report.Renewal)
		require.Len(t, report.Renewal.ArmedReplies, 1)
		require.Equal(t, "accepted", report.Renewal.ArmedReplies[0].Disposition, "a discarded reply fails the success case even when the bytes arrive")
		require.Zero(t, report.Renewal.ArmedPending)
		require.Zero(t, report.Renewal.Delivered, "the staged reply answered; nothing was left for the harness")
		require.Len(t, report.reachedAt(dagql.FixtureRenewalEnqueued), 1, "one exchange for the one demand")
		require.Len(t, report.reachedAt(dagql.FixtureRenewalReplied), 1)
		require.Empty(t, partEventsOf(report.transferFixtureReport, s.rID, dagql.PartEventLazyEnter), "the saved producer never ran")
		require.Len(t, partEventsOf(report.transferFixtureReport, s.rID, dagql.PartEventInstalledChain), 1)
		var fetched int
		for _, request := range report.Transport.Requests {
			if strings.HasPrefix(request.URL, "https://"+fixturetransport.ContentHost+"/") {
				fetched++
				require.Contains(t, []int{200, 206}, request.Status)
				require.True(t, request.Closed)
			}
		}
		require.Positive(t, fetched, "the layers came over the real HTTP content path")
	})

	// The same success from an offer whose addresses have all expired, the
	// state a long-saved bundle is in. The expired URLs are never requested.
	t.Run("ExpiredAddressesRenewed", func(ctx context.Context, t *testctx.T) {
		s := newRenewalScenario(ctx, t, "renewal-expired", true)
		s.scriptContent(t)
		addresses := map[string]any{}
		for _, blob := range s.digests {
			addresses[blob] = map[string]any{"url": s.contentURL(blob), "expiresAtUnix": time.Now().Add(time.Hour).Unix()}
		}
		require.NoError(t, s.b.fixture("armRenewalReply", s.b.control("reply.json", map[string]any{"layers": s.layers, "addresses": addresses}), nil, nil))
		s.importOnB(t)
		contents, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(s.rHandle)).File("payload.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, s.payload, contents)
		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		t.Logf("R=%d events: %v", s.rID, partKindsOf(report.transferFixtureReport, s.rID))
		require.Len(t, report.Renewal.ArmedReplies, 1)
		require.Equal(t, "accepted", report.Renewal.ArmedReplies[0].Disposition)
		require.Len(t, report.reachedAt(dagql.FixtureRenewalEnqueued), 1)
		require.Empty(t, partEventsOf(report.transferFixtureReport, s.rID, dagql.PartEventLazyEnter))
		require.Len(t, partEventsOf(report.transferFixtureReport, s.rID, dagql.PartEventInstalledChain), 1)
		for _, request := range report.Transport.Requests {
			require.NotContains(t, request.URL, "?expired", "an expired address is never requested")
		}
	})

	// Timeout, then fallback. Nobody replies: the exchange ends at its own
	// deadline, the undelivered-to-harness record is retired by its Done, and
	// the same demand falls back to the saved producer once.
	t.Run("TimeoutThenFallback", func(ctx context.Context, t *testctx.T) {
		s := newRenewalScenario(ctx, t, "renewal-timeout", false)
		s.importOnB(t)
		started := time.Now()
		contents, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(s.rHandle)).File("payload.txt").Contents(ctx)
		require.NoError(t, err, "the saved producer restores the Directory")
		require.Equal(t, s.payload, contents)
		t.Logf("demand with an unanswered renewal took %s", time.Since(started))

		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		t.Logf("R=%d events: %v; renewal=%+v", s.rID, partKindsOf(report.transferFixtureReport, s.rID), *report.Renewal)
		require.Len(t, partEventsOf(report.transferFixtureReport, s.rID, dagql.PartEventLazyEnter), 1, "one fallback")
		require.Empty(t, partEventsOf(report.transferFixtureReport, s.rID, dagql.PartEventInstalledChain))
		require.Len(t, report.reachedAt(dagql.FixtureRenewalEnqueued), 1, "one episode for the demand, not one per retry")
		require.Empty(t, report.reachedAt(dagql.FixtureRenewalReplied))
		require.EqualValues(t, 1, report.Renewal.Delivered)
		require.Zero(t, report.Renewal.Taken)
		require.EqualValues(t, 1, report.Renewal.Retired, "the unanswered record went with its exchange")
		require.Zero(t, report.Renewal.Live)
		require.Zero(t, report.Transport.FixtureHosts, "no address was ever known, so nothing was fetched")
	})
}
