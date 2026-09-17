package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// offersScenario is one built Directory, which has a saved producer. A exports
// it twice: as metadata only, which B imports as an ordinary pending receiver
// R with no offer, and with its selected chain, which the harness later
// presents to the real OfferParts as an integration would. R is created and
// held by the import before any offer; an offer never creates or demands it.
type offersScenario struct {
	a, b    *fixtureEngine
	payload string
	offers  []any
	rHandle string
	rID     uint64
}

type fixtureOfferResult struct {
	Dispositions []struct {
		Address  dagql.PersistedPartAddress `json:"address"`
		Outcome  string                     `json:"outcome"`
		Replaced bool                       `json:"replaced"`
		OfferRev uint64                     `json:"offerRev"`
		Error    string                     `json:"error"`
	} `json:"dispositions"`
	Error string `json:"error"`
}

func newOffersScenario(ctx context.Context, t *testctx.T, name string) *offersScenario {
	outer := connect(ctx, t)
	s := &offersScenario{
		a:       newFixtureEngine(ctx, t, outer, name+"-a", true),
		b:       newFixtureEngine(ctx, t, outer, name+"-b", true),
		payload: "offered payload " + identity.NewID(),
	}
	dir, err := s.a.client.Directory().WithNewFile("payload.txt", s.payload).Sync(ctx)
	require.NoError(t, err)
	id, err := dir.ID(ctx)
	require.NoError(t, err)
	require.NoError(t, s.a.fixture("export", "pending.json", []string{string(id)}, nil))
	require.NoError(t, s.a.fixture("exportSelected", s.a.control("export.json", map[string]any{"bundle": "chain.json", "outputs": []map[string]any{{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), []string{string(id)}, nil))
	s.offers = s.a.bundleOffers("chain.json")
	s.a.copyFixtureTo(s.b, "pending.json")

	var imported []transferFixtureMapping
	require.NoError(t, s.b.fixture("import", "pending.json", nil, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Directory", imported[0].Type.NamedType)
	s.rHandle, s.rID = imported[0].Handle, imported[0].ResultID
	return s
}

func (s *offersScenario) offer(t *testctx.T) fixtureOfferResult {
	t.Helper()
	var result fixtureOfferResult
	require.NoError(t, s.b.fixture("offer", s.b.control("offer-"+identity.NewID()+".json", map[string]any{"offers": s.offers}), []string{s.rHandle}, &result))
	require.Len(t, result.Dispositions, 1)
	require.Equal(t, dagql.PartKey("snapshot"), result.Dispositions[0].Address.Part, "a disposition names its full address")
	return result
}

func (s *offersScenario) report(t *testctx.T) (fixtureControlsReport, dagql.TransferFixtureRow) {
	t.Helper()
	var all, row fixtureControlsReport
	require.NoError(t, s.b.fixture("report", "", nil, &all))
	require.NoError(t, s.b.fixture("report", "", []string{s.rHandle}, &row))
	require.Len(t, row.Rows, 1)
	return all, row.Rows[0]
}

// read demands R on its own goroutine, so a barrier can hold it.
func (s *offersScenario) read(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		contents, err := dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(s.rHandle)).File("payload.txt").Contents(ctx)
		if err == nil && contents != s.payload {
			err = fmt.Errorf("read %q, want %q", contents, s.payload)
		}
		done <- err
	}()
	return done
}

func (s *offersScenario) pauseAt(t *testctx.T, point dagql.FixtureBarrierPoint) dagql.FixtureBarrierArmed {
	t.Helper()
	var armed dagql.FixtureBarrierArmed
	require.NoError(t, s.b.fixture("barrierArm", s.b.control("pause.json", dagql.FixtureBarrierRequest{Key: "pause", Point: point, Selector: dagql.FixtureBarrierSelector{ResultID: s.rID}, Action: dagql.FixturePause}), nil, &armed))
	s.releaseOnExit(t, armed)
	return armed
}

// releaseOnExit releases the scenario's pause on every exit, so a failed
// assertion between arming and releasing ends the held demand locally rather
// than at engine shutdown. Releasing twice is harmless. It is registered
// after the engine's own cleanup and so runs before it.
func (s *offersScenario) releaseOnExit(t *testctx.T, armed dagql.FixtureBarrierArmed) {
	record := s.b.control("pause-exit.json", map[string]any{"key": "pause", "generation": armed.Generation})
	t.Cleanup(func() {
		if s.b.client == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.b.ctx), 30*time.Second)
		defer cancel()
		_ = transferFixture(ctx, s.b.client, "barrierRelease", record, []string{}, new(json.RawMessage))
	})
}

func (s *offersScenario) await(ctx context.Context, t *testctx.T, armed dagql.FixtureBarrierArmed) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var reached dagql.FixtureBarrierReached
	require.NoError(t, transferFixture(waitCtx, s.b.client, "barrierWait", s.b.control("pause-wait.json", map[string]any{"key": "pause", "generation": armed.Generation}), []string{}, &reached))
	require.Equal(t, s.rID, reached.Event.ResultID)
}

// TestOffers is the native offers-on-existing-work row (design §5 as amended
// by B1). The dispositions and the hold arithmetic are proven in process; here
// a real OfferParts meets a real demand at three moments.
func (RemoteCacheTransferSuite) TestOffers(ctx context.Context, t *testctx.T) {
	// Before any demand: the offer is accepted and acquires nothing; the
	// first demand then installs the chain and the producer never runs.
	t.Run("BeforeStart", func(ctx context.Context, t *testctx.T) {
		s := newOffersScenario(ctx, t, "offers-before")
		_, pending := s.report(t)
		require.Empty(t, pending.Offers)
		result := s.offer(t)
		require.Equal(t, "accepted", result.Dispositions[0].Outcome)
		require.False(t, result.Dispositions[0].Replaced)
		all, row := s.report(t)
		require.Len(t, row.Offers, 1)
		require.Empty(t, partKindsOf(all.transferFixtureReport, s.rID), "acceptance acquires nothing")
		require.Empty(t, all.reachedAt(dagql.FixtureChainReaderOpen))

		require.NoError(t, joinBounded(t, s.read(ctx), "the demand"))
		all, row = s.report(t)
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"), 1)
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"))
		require.Empty(t, row.Offers, "the installed offer is retired")
	})

	// While the demand is about to begin the original operation: the offer
	// is accepted and invalidates the demand's final source check, so the
	// chain is installed and the producer never runs.
	t.Run("Preparing", func(ctx context.Context, t *testctx.T) {
		s := newOffersScenario(ctx, t, "offers-preparing")
		armed := s.pauseAt(t, dagql.FixtureBeforeBeginOriginal)
		done := s.read(ctx)
		s.await(ctx, t, armed)
		result := s.offer(t)
		require.Equal(t, "accepted", result.Dispositions[0].Outcome)
		require.NoError(t, s.b.fixture("barrierRelease", "pause-wait.json", nil, nil))
		require.NoError(t, joinBounded(t, done, "the held demand"))
		all, _ := s.report(t)
		t.Logf("R=%d events: %v", s.rID, partKindsOf(all.transferFixtureReport, s.rID))
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"), "the accepted offer invalidated the final source check")
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"), 1)
	})

	// After the original operation is sealed: the offer is answered
	// executionStarted, the running body is not interrupted, and its output
	// is the one installed.
	t.Run("Running", func(ctx context.Context, t *testctx.T) {
		s := newOffersScenario(ctx, t, "offers-running")
		armed := s.pauseAt(t, dagql.FixtureOriginalSealed)
		done := s.read(ctx)
		s.await(ctx, t, armed)
		result := s.offer(t)
		require.Equal(t, "executionStarted", result.Dispositions[0].Outcome)
		require.NoError(t, s.b.fixture("barrierRelease", "pause-wait.json", nil, nil))
		require.NoError(t, joinBounded(t, done, "the held demand"), "the body was not interrupted")
		all, row := s.report(t)
		t.Logf("R=%d events: %v", s.rID, partKindsOf(all.transferFixtureReport, s.rID))
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"), 1)
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-lazy"), 1)
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"))
		require.Empty(t, all.reachedAt(dagql.FixtureChainReaderOpen), "a refused offer reads nothing")
		require.Empty(t, row.Offers)
	})
	// Two offer owners. R's first offer O1 is being read, its reader held at
	// the first chainRead, when a second offer O2 for the same address
	// replaces it and the engine's real collection runs. O1's acquisition
	// keeps what it holds: the read completes from O1, nothing is read twice,
	// the producer never runs, and the final settlement retires the current
	// replacement too, so no offer and no fixture hold is left.
	t.Run("Replacement", func(ctx context.Context, t *testctx.T) {
		s := newOffersScenario(ctx, t, "offers-replacement")
		first := s.offer(t)
		require.Equal(t, "accepted", first.Dispositions[0].Outcome)
		require.False(t, first.Dispositions[0].Replaced)

		var armed dagql.FixtureBarrierArmed
		require.NoError(t, s.b.fixture("barrierArm", s.b.control("pause.json", dagql.FixtureBarrierRequest{Key: "pause", Point: dagql.FixtureChainRead, Selector: dagql.FixtureBarrierSelector{ResultID: s.rID}, Action: dagql.FixturePause}), nil, &armed))
		s.releaseOnExit(t, armed)
		done := s.read(ctx)
		s.await(ctx, t, armed)

		// O2 is a different record for the same chain: it names addresses.
		chain := s.offers[0].(map[string]any)["chain"].(map[string]any)
		addresses := map[string]any{}
		for _, layer := range chain["layers"].([]any) {
			blob := layer.(map[string]any)["Descriptor"].(map[string]any)["digest"].(string)
			addresses[blob] = map[string]any{"url": "https://content.remote-cache.invalid/" + blob, "expiresAtUnix": time.Now().Add(time.Hour).Unix()}
		}
		chain["addresses"] = addresses
		second := s.offer(t)
		t.Logf("first %+v; second %+v", first.Dispositions[0], second.Dispositions[0])
		require.Equal(t, "accepted", second.Dispositions[0].Outcome, "an offer for an address whose output is still pending is accepted")
		require.True(t, second.Dispositions[0].Replaced, "and replaces the current offer")
		require.Greater(t, second.Dispositions[0].OfferRev, first.Dispositions[0].OfferRev)
		require.NoError(t, s.b.fixture("gc", "", nil, nil), "a collection while O1's reader is held")

		require.NoError(t, s.b.fixture("barrierRelease", "pause-wait.json", nil, nil))
		require.NoError(t, joinBounded(t, done, "the held demand"), "O1's acquisition survived the swap and the collection")
		all, row := s.report(t)
		t.Logf("R=%d events: %v", s.rID, partKindsOf(all.transferFixtureReport, s.rID))
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"), 1)
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"))
		require.Empty(t, row.Offers, "the final settlement retired the replacement as well")
		require.Len(t, row.SnapshotLinks, 1)
		require.Equal(t, dagql.TransferFixtureControls{}, all.Controls)
		opens := 0
		for _, o := range all.reachedAt(dagql.FixtureChainReaderOpen) {
			if o.ResultID == s.rID {
				opens++
			}
		}
		require.Equal(t, len(s.offers[0].(map[string]any)["chain"].(map[string]any)["layers"].([]any)), opens, "each layer was opened once, by O1's acquisition only")
	})
	// Offer-only resources. The offer's owner depends on a row that needs a
	// session resource, a Secret only the offering session holds. A session
	// without it still gets the ordinary hit on R: lookup is not gated by an
	// offer's requirements. Only that offer is skipped for it, so its demand
	// is served by the saved producer. The session that holds the resource
	// may install from the offer.
	t.Run("Resources", func(ctx context.Context, t *testctx.T) {
		for _, authorized := range []bool{false, true} {
			name := map[bool]string{false: "SessionWithoutResource", true: "SessionWithResource"}[authorized]
			t.Run(name, func(ctx context.Context, t *testctx.T) {
				s := newOffersScenario(ctx, t, "offers-resources")
				secret := s.b.client.SetSecret("b7-offer-resource", "value "+identity.NewID())
				secretID, err := secret.ID(ctx)
				require.NoError(t, err)
				resource := rowOf(t, s.b, string(secretID))
				offer := s.offers[0].(map[string]any)
				offer["owner"] = map[string]any{"dependencyIDs": []uint64{resource.ResultID}}
				result := s.offer(t)
				t.Logf("offer with a resource-bearing owner: %+v", result.Dispositions[0])
				require.Equal(t, "accepted", result.Dispositions[0].Outcome)

				reader := s.b.client
				if !authorized {
					reader = s.b.connect()
					defer func() { require.NoError(t, closeClientBounded(ctx, reader)) }()
				}
				contents, err := dagger.Ref[*dagger.Directory](reader, dagger.ID(s.rHandle)).File("payload.txt").Contents(ctx)
				require.NoError(t, err, "the ordinary hit is never gated by an offer's requirements")
				require.Equal(t, s.payload, contents)
				all, _ := s.report(t)
				t.Logf("%s: R=%d events: %v", name, s.rID, partKindsOf(all.transferFixtureReport, s.rID))
				if authorized {
					require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"), 1, "the session that holds the resource installs from the offer")
					require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"))
				} else {
					require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"), "the offer is skipped for a session that lacks its resource")
					require.Empty(t, all.reachedAt(dagql.FixtureChainReaderOpen), "and none of its content is read")
					require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"), 1, "the saved producer serves it instead")
				}
			})
		}
	})
}
