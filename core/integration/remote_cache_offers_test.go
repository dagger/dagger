package core

import (
	"context"
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
			err = context.DeadlineExceeded
		}
		done <- err
	}()
	return done
}

func (s *offersScenario) pauseAt(t *testctx.T, point dagql.FixtureBarrierPoint) dagql.FixtureBarrierArmed {
	t.Helper()
	var armed dagql.FixtureBarrierArmed
	require.NoError(t, s.b.fixture("barrierArm", s.b.control("pause.json", dagql.FixtureBarrierRequest{Key: "pause", Point: point, Selector: dagql.FixtureBarrierSelector{ResultID: s.rID}, Action: dagql.FixturePause}), nil, &armed))
	return armed
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

		require.NoError(t, <-s.read(ctx))
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
		require.NoError(t, <-done)
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
		require.NoError(t, <-done, "the body was not interrupted")
		all, row := s.report(t)
		t.Logf("R=%d events: %v", s.rID, partKindsOf(all.transferFixtureReport, s.rID))
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"), 1)
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-lazy"), 1)
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"))
		require.Empty(t, all.reachedAt(dagql.FixtureChainReaderOpen), "a refused offer reads nothing")
		require.Empty(t, row.Offers)
	})
}
