package core

import (
	"context"
	"fmt"
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
	s.b.armBarrier(dagql.FixtureBarrierRequest{Key: "chain", Point: dagql.FixtureChainReaderOpen, Selector: dagql.FixtureBarrierSelector{ResultID: s.rID}, Action: dagql.FixtureFailChainOpen})
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
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, dagql.PartEventInstalledChain), 1)
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, dagql.PartEventLazyEnter), "the HTTP producer is never entered")
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
		require.Len(t, partEventsOf(all.transferFixtureReport, s.rID, dagql.PartEventLazyEnter), 1, "the producer ran once")
		require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, dagql.PartEventInstalledChain))
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
				require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, dagql.PartEventInstalledLazy))
				require.Empty(t, partEventsOf(all.transferFixtureReport, s.rID, dagql.PartEventInstalledChain))

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
	// The writer table, under the named StateResolveLayout control: the
	// internal _httpState._resolve field takes the file name and permissions
	// as given, so A's real HTTPState.fileResult is the reference output.
	// After transfer and a failed chain the saved producer restores the File
	// with the same layout: equal recorded name, actual mode and bytes, or the
	// matching error where A had no output either. One A and one B serve the
	// whole table; every case has its own URL, so no File hides another's
	// route, and each chain fault is selected by its own row.
	t.Run("StateResolveLayout", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		a := newFixtureEngine(ctx, t, outer, "http-layout-a", true)
		b := newFixtureEngine(ctx, t, outer, "http-layout-b", true)
		type layoutCase struct {
			name        string
			permissions int
			url, body   string
		}
		var cases []layoutCase
		for _, name := range []string{"data.txt", "/data.txt", "../data.txt", "a/../data.txt", "./data.txt"} {
			cases = append(cases, layoutCase{name: name, permissions: 0o644})
		}
		for _, permissions := range []int{0, 0o600, 0o755} {
			cases = append(cases, layoutCase{name: "data.txt", permissions: permissions})
		}
		var responses []fixturetransport.Response
		for i := range cases {
			cases[i].url = "https://" + fixturetransport.OriginHost + "/" + identity.NewID() + "/data.txt"
			cases[i].body = fmt.Sprintf("layout case %d %s", i, identity.NewID())
			file := fmt.Sprintf("origins/layout-%d", i)
			a.writeFile(file, cases[i].body)
			b.writeFile(file, cases[i].body)
			responses = append(responses, fixturetransport.Response{URL: cases[i].url, BodyFile: file})
		}
		absent := "https://" + fixturetransport.OriginHost + "/" + identity.NewID() + "/data.txt"
		a.writeFile("origins/layout-absent", "absent parent")
		responses = append(responses, fixturetransport.Response{URL: absent, BodyFile: "origins/layout-absent"})
		scriptOrigin(t, a, responses...)
		scriptOrigin(t, b, responses...)

		resolve := func(e *fixtureEngine, url, name string, permissions int) (string, error) {
			var data struct {
				State struct {
					Resolve struct{ ID string } `json:"_resolve"`
				} `json:"_httpState"`
			}
			err := e.client.Do(ctx, &dagger.Request{Query: `query($url:String!,$name:String!,$permissions:Int!){_httpState(url:$url){_resolve(name:$name,permissions:$permissions){id}}}`, Variables: map[string]any{"url": url, "name": name, "permissions": permissions}}, &dagger.Response{Data: &data})
			return data.State.Resolve.ID, err
		}
		// facts are what identifies the written file: its recorded name, its
		// actual mode on a real mount, and its bytes.
		facts := func(e *fixtureEngine, handle string) string {
			file := dagger.Ref[*dagger.File](e.client, dagger.ID(handle))
			name, err := file.Name(ctx)
			require.NoError(t, err)
			out, err := e.client.Container().From(alpineImage).WithMountedFile("/probe/file", file).
				WithExec([]string{"sh", "-ec", "stat -c '%a' /probe/file; cat /probe/file"}).Stdout(ctx)
			require.NoError(t, err)
			return name + " " + out
		}

		_, err := resolve(a, absent, "sub/data.txt", 0o644)
		require.Error(t, err, "an absent parent is the ordinary writer error on A")
		t.Logf("absent parent on A: %v", err)

		handles := make([]string, len(cases))
		outputs := make([]map[string]any, len(cases))
		want := make([]string, len(cases))
		for i, tc := range cases {
			id, err := resolve(a, tc.url, tc.name, tc.permissions)
			require.NoError(t, err, "%q %o", tc.name, tc.permissions)
			want[i] = facts(a, id)
			t.Logf("A %q %o -> %q", tc.name, tc.permissions, want[i])
			handles[i] = id
			outputs[i] = map[string]any{"handle": id, "address": dagql.PersistedPartAddress{Part: "snapshot"}}
		}
		var exported fixtureExportSelectedResult
		require.NoError(t, a.fixture("exportSelected", a.control("export.json", map[string]any{"bundle": "layout.json", "outputs": outputs}), handles, &exported))
		require.Len(t, exported.Outputs, len(cases))
		a.copyFixtureTo(b, "layout.json")
		var imported []transferFixtureMapping
		require.NoError(t, b.fixture("import", "layout.json", nil, &imported))
		require.GreaterOrEqual(t, len(imported), len(cases))

		for i, tc := range cases {
			row := imported[i]
			require.Equal(t, "File", row.Type.NamedType)
			b.armBarrier(dagql.FixtureBarrierRequest{Key: fmt.Sprintf("chain-%d", i), Point: dagql.FixtureChainReaderOpen, Selector: dagql.FixtureBarrierSelector{ResultID: row.ResultID}, Action: dagql.FixtureFailChainOpen})
			got := facts(b, row.Handle)
			require.Equal(t, want[i], got, "%q %o: the restored File has A's recorded name, actual mode and bytes", tc.name, tc.permissions)
			var report fixtureControlsReport
			require.NoError(t, b.fixture("report", "", nil, &report))
			require.Len(t, partEventsOf(report.transferFixtureReport, row.ResultID, dagql.PartEventLazyEnter), 1, "%q %o: restored by the saved producer", tc.name, tc.permissions)
			require.Empty(t, partEventsOf(report.transferFixtureReport, row.ResultID, dagql.PartEventInstalledChain))
		}
	})
	// Status codes the stateless restoration can meet, asserted as the
	// existing writer behaves rather than by a new 200-only rule: each case
	// names whether the File is restored with exactly A's bytes or the demand
	// is a restoration error of a named kind with nothing installed under the
	// saved identity. One A and one B serve the table; every case has its own
	// URL and row.
	t.Run("StatusTable", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		a := newFixtureEngine(ctx, t, outer, "http-status-a", true)
		b := newFixtureEngine(ctx, t, outer, "http-status-b", true)
		type statusCase struct {
			name     string
			saved    string
			response fixturetransport.Response
			// failure is empty when the writer restores the File, else the
			// restoration error it gives.
			failure   string
			url, file string
		}
		cases := []statusCase{
			{name: "204 empty saved body", saved: "", response: fixturetransport.Response{Status: 204}},
			{name: "204 nonempty saved body", saved: "kept", response: fixturetransport.Response{Status: 204}, failure: "HTTP File operation body mismatch"},
			{name: "206 whole body", saved: "partial", response: fixturetransport.Response{Status: 206, BodyFile: "saved"}},
			// One empty saved body only: two empty Files are one row, and a
			// bundle cannot name a root twice.
			{name: "304 nonempty saved body", saved: "kept", response: fixturetransport.Response{Status: 304}, failure: "HTTP File operation body mismatch"},
			{name: "403", saved: "kept", response: fixturetransport.Response{Status: 403}, failure: "invalid response status 403"},
			{name: "503", saved: "kept", response: fixturetransport.Response{Status: 503}, failure: "invalid response status 503"},
		}
		var onA, onB []fixturetransport.Response
		for i := range cases {
			tc := &cases[i]
			tc.url = "https://" + fixturetransport.OriginHost + "/" + identity.NewID() + "/data.txt"
			tc.file = fmt.Sprintf("origins/status-%d", i)
			if tc.saved != "" {
				tc.saved += " " + identity.NewID()
			}
			a.writeFile(tc.file, tc.saved)
			b.writeFile(tc.file, tc.saved)
			onA = append(onA, fixturetransport.Response{URL: tc.url, BodyFile: tc.file})
			response := tc.response
			response.URL = tc.url
			if response.BodyFile == "saved" {
				response.BodyFile = tc.file
			}
			onB = append(onB, response)
		}
		scriptOrigin(t, a, onA...)
		scriptOrigin(t, b, onB...)

		handles := make([]string, len(cases))
		outputs := make([]map[string]any, len(cases))
		for i, tc := range cases {
			file, err := a.client.HTTP(tc.url).Sync(ctx)
			require.NoError(t, err, tc.name)
			id, err := file.ID(ctx)
			require.NoError(t, err)
			handles[i] = string(id)
			outputs[i] = map[string]any{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}
		}
		require.NoError(t, a.fixture("exportSelected", a.control("export.json", map[string]any{"bundle": "status.json", "outputs": outputs}), handles, nil))
		a.copyFixtureTo(b, "status.json")
		var imported []transferFixtureMapping
		require.NoError(t, b.fixture("import", "status.json", nil, &imported))
		require.GreaterOrEqual(t, len(imported), len(cases))

		for i, tc := range cases {
			row := imported[i]
			require.Equal(t, "File", row.Type.NamedType)
			b.armBarrier(dagql.FixtureBarrierRequest{Key: fmt.Sprintf("chain-%d", i), Point: dagql.FixtureChainReaderOpen, Selector: dagql.FixtureBarrierSelector{ResultID: row.ResultID}, Action: dagql.FixtureFailChainOpen})
			contents, err := dagger.Ref[*dagger.File](b.client, dagger.ID(row.Handle)).Contents(ctx)
			var report, single fixtureControlsReport
			require.NoError(t, b.fixture("report", "", nil, &report))
			require.NoError(t, b.fixture("report", "", []string{row.Handle}, &single))
			require.Len(t, single.Rows, 1)
			require.Len(t, partEventsOf(report.transferFixtureReport, row.ResultID, dagql.PartEventLazyEnter), 1, "%s: the saved producer ran once", tc.name)
			if tc.failure == "" {
				require.NoError(t, err, "%s: the writer restores the File", tc.name)
				require.Equal(t, tc.saved, contents, "%s: a restored File has exactly A's bytes", tc.name)
				require.Len(t, single.Rows[0].SnapshotLinks, 1, tc.name)
			} else {
				require.ErrorContains(t, err, tc.failure, tc.name)
				require.Empty(t, single.Rows[0].SnapshotLinks, "%s: nothing is installed under the saved identity", tc.name)
			}
			asked := 0
			for _, request := range report.Transport.Requests {
				if request.URL == tc.url {
					asked++
					require.Equal(t, tc.response.Status, request.Status, tc.name)
					require.Empty(t, request.IfNoneMatch, "%s: no conditional validator", tc.name)
					require.Empty(t, request.IfModifiedSince, tc.name)
					require.True(t, request.Closed, "%s: the response body is closed", tc.name)
				}
			}
			require.Equal(t, 1, asked, "%s: the scripted origin was asked exactly once", tc.name)
		}
	})
}
