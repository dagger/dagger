package core

import (
	"context"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// httpRestoreScenario is one ordinary http File resolved on A from the
// fixture's origin host and exported with its selected chain, and a fresh B
// that imported it. Distinct Files cannot hide one another's route, so each
// outcome class gets its own B.
type httpRestoreScenario struct {
	a, b    *fixtureEngine
	url     string
	body    string
	rHandle string
	rID     uint64
}

func scriptOrigin(t *testctx.T, e *fixtureEngine, responses ...fixturetransport.Response) {
	t.Helper()
	require.NoError(t, e.fixture("transport", e.control("script-"+identity.NewID()+".json", fixturetransport.Script{Responses: responses}), nil, nil))
}

func newHTTPRestoreScenario(ctx context.Context, t *testctx.T, name string) *httpRestoreScenario {
	outer := connect(ctx, t)
	s := &httpRestoreScenario{
		a:    newFixtureEngine(ctx, t, outer, name+"-a", true),
		b:    newFixtureEngine(ctx, t, outer, name+"-b", true),
		body: "recorded origin body " + identity.NewID(),
	}
	s.url = "https://" + fixturetransport.OriginHost + "/" + identity.NewID() + "/data.txt"
	s.a.writeFile("origins/data.txt", s.body)
	scriptOrigin(t, s.a, fixturetransport.Response{URL: s.url, BodyFile: "origins/data.txt", Headers: map[string]string{"ETag": `"v1"`, "Last-Modified": "Mon, 02 Jan 2006 15:04:05 GMT"}})
	file, err := s.a.client.HTTP(s.url).Sync(ctx)
	require.NoError(t, err)
	id, err := file.ID(ctx)
	require.NoError(t, err)
	var exported fixtureExportSelectedResult
	require.NoError(t, s.a.fixture("exportSelected", s.a.control("export.json", map[string]any{"bundle": "http.json", "outputs": []map[string]any{{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), []string{string(id)}, &exported))
	require.Len(t, exported.Outputs, 1)
	require.NotEmpty(t, exported.Outputs[0].Layers)
	s.a.copyFixtureTo(s.b, "http.json")
	var imported []transferFixtureMapping
	require.NoError(t, s.b.fixture("import", "http.json", nil, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "File", imported[0].Type.NamedType)
	s.rHandle, s.rID = imported[0].Handle, imported[0].ResultID
	return s
}

// failChain arms the one-shot content fault on R's offered chain.
func (s *httpRestoreScenario) failChain(t *testctx.T) {
	t.Helper()
	require.NoError(t, s.b.fixture("barrierArm", s.b.control("chain.json", dagql.FixtureBarrierRequest{Key: "chain", Point: dagql.FixtureChainReaderOpen, Selector: dagql.FixtureBarrierSelector{ResultID: s.rID}, Action: dagql.FixtureFailChainOpen}), nil, nil))
}

func (s *httpRestoreScenario) report(t *testctx.T) (fixtureControlsReport, dagql.TransferFixtureRow) {
	t.Helper()
	var all, row fixtureControlsReport
	require.NoError(t, s.b.fixture("report", "", nil, &all))
	require.NoError(t, s.b.fixture("report", "", []string{s.rHandle}, &row))
	require.Len(t, row.Rows, 1)
	return all, row.Rows[0]
}

// originRequests returns what B's dispatcher saw for the scenario's URL.
func (s *httpRestoreScenario) originRequests(report fixtureControlsReport) []*fixturetransport.Observation {
	var out []*fixturetransport.Observation
	if report.Transport == nil {
		return nil
	}
	for _, request := range report.Transport.Requests {
		if request.URL == s.url {
			out = append(out, request)
		}
	}
	return out
}

// TestHTTPRestore is the native HTTP outcomes row (design §4 "HTTP and
// content" and §5 as amended by B1): everything that writes a file. The
// in-process half is classification only.
func (RemoteCacheTransferSuite) TestHTTPRestore(ctx context.Context, t *testctx.T) {
	// The offered chain succeeds: correct bytes, no origin request, and the
	// saved HTTP producer is never entered.
	t.Run("ChainSucceeds", func(ctx context.Context, t *testctx.T) {
		s := newHTTPRestoreScenario(ctx, t, "http-chain")
		// B has no origin script at all: any origin request would fail.
		contents, err := dagger.Ref[*dagger.File](s.b.client, dagger.ID(s.rHandle)).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, s.body, contents)
		name, err := dagger.Ref[*dagger.File](s.b.client, dagger.ID(s.rHandle)).Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "data.txt", name)
		all, row := s.report(t)
		require.True(t, row.Imported)
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"), 1)
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"), "the HTTP producer is never entered")
		require.Empty(t, s.originRequests(all), "zero origin requests")
	})

	// The chain fails and the origin still returns the recorded body: the
	// real stateless producer runs and its equal body is accepted, with a
	// changed Last-Modified, no ETag, and no conditional validators sent.
	t.Run("ChainFailsOriginSame", func(ctx context.Context, t *testctx.T) {
		s := newHTTPRestoreScenario(ctx, t, "http-origin")
		s.b.writeFile("origins/data.txt", s.body)
		scriptOrigin(t, s.b, fixturetransport.Response{URL: s.url, BodyFile: "origins/data.txt", Headers: map[string]string{"Last-Modified": time.Now().UTC().Format(time.RFC1123)}})
		s.failChain(t)
		contents, err := dagger.Ref[*dagger.File](s.b.client, dagger.ID(s.rHandle)).Contents(ctx)
		require.NoError(t, err, "the saved producer restores the File from its origin")
		require.Equal(t, s.body, contents)
		all, row := s.report(t)
		t.Logf("R=%d events: %v", s.rID, partKindsOf(all.transferFixtureReport, s.rID))
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, "lazy-enter"), 1, "the producer ran once")
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"))
		require.Len(t, row.SnapshotLinks, 1, "the restored File is owned under the saved identity")
		requests := s.originRequests(all)
		require.Len(t, requests, 1, "one origin request")
		require.Equal(t, "GET", requests[0].Method)
		require.Empty(t, requests[0].IfNoneMatch, "a stateless restoration sends no conditional validator")
		require.Empty(t, requests[0].IfModifiedSince)
		require.False(t, requests[0].Authorization)
		require.Equal(t, int64(len(s.body)), requests[0].BodyBytesRead)
		require.True(t, requests[0].Closed)
	})

	// The chain fails and the origin no longer returns the recorded body: an
	// ordinary restoration error, nothing installed under the saved identity,
	// and a separately executed new http call still produces a new File.
	t.Run("ChainFailsOriginDiffers", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct {
			name     string
			response fixturetransport.Response
		}{
			{"changed body", fixturetransport.Response{BodyFile: "origins/changed.txt"}},
			{"server error", fixturetransport.Response{Status: 500}},
			{"not found", fixturetransport.Response{Status: 404}},
			{"truncated body", fixturetransport.Response{BodyFile: "origins/same.txt", Fault: "truncate", TruncateAt: 5}},
			{"no response", fixturetransport.Response{Fault: "transport"}},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				s := newHTTPRestoreScenario(ctx, t, "http-differs")
				s.b.writeFile("origins/changed.txt", "a different body "+identity.NewID())
				s.b.writeFile("origins/same.txt", s.body)
				response := tc.response
				response.URL = s.url
				scriptOrigin(t, s.b, response)
				s.failChain(t)
				_, err := dagger.Ref[*dagger.File](s.b.client, dagger.ID(s.rHandle)).Contents(ctx)
				require.Error(t, err, "the saved File cannot be restored")
				t.Logf("%s: restoration error: %v", tc.name, err)
				all, row := s.report(t)
				t.Logf("R=%d events: %v", s.rID, partKindsOf(all.transferFixtureReport, s.rID))
				require.Empty(t, row.SnapshotLinks, "no output is installed under the saved File identity")
				require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-lazy"))
				require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, "installed-chain"))

				if tc.name == "changed body" {
					// A new ordinary call is its own File with the new bytes.
					fresh, err := s.b.client.HTTP(s.url).Contents(ctx)
					require.NoError(t, err)
					require.NotEqual(t, s.body, fresh)
					_, after := s.report(t)
					require.Empty(t, after.SnapshotLinks, "the new File did not fill the saved one")
				}
			})
		}
	})
}
