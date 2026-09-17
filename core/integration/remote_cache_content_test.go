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

// contentScenario is one built Directory, which has a saved producer, exported
// by A with its selected chain. The harness gives every layer a live address
// on the fixture's content host before B imports it, so B fetches the chain
// over the real HTTP content path and the transport script decides what that
// path sees.
type contentScenario struct {
	a, b    *fixtureEngine
	payload string
	digests []string
	sizes   []int64
	rHandle string
	rID     uint64
}

func newContentScenario(ctx context.Context, t *testctx.T, name string) *contentScenario {
	outer := connect(ctx, t)
	s := &contentScenario{
		a:       newFixtureEngine(ctx, t, outer, name+"-a", true),
		b:       newFixtureEngine(ctx, t, outer, name+"-b", true),
		payload: "addressed payload " + identity.NewID(),
	}
	dir, err := s.a.client.Directory().WithNewFile("payload.txt", s.payload).Sync(ctx)
	require.NoError(t, err)
	id, err := dir.ID(ctx)
	require.NoError(t, err)
	var exported fixtureExportSelectedResult
	require.NoError(t, s.a.fixture("exportSelected", s.a.control("export.json", map[string]any{"bundle": "content.json", "outputs": []map[string]any{{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), []string{string(id)}, &exported))
	require.Len(t, exported.Outputs, 1)
	for _, layer := range exported.Outputs[0].Layers {
		s.digests = append(s.digests, layer.Digest)
		s.sizes = append(s.sizes, layer.Size)
	}
	require.NotEmpty(t, s.digests)
	s.a.editBundle("content.json", func(bundle map[string]any) {
		chain := bundle["outputs"].([]any)[0].(map[string]any)["chain"].(map[string]any)
		addresses := map[string]any{}
		for _, blob := range s.digests {
			addresses[blob] = map[string]any{"url": s.url(blob), "expiresAtUnix": time.Now().Add(time.Hour).Unix()}
		}
		chain["addresses"] = addresses
	})
	s.a.copyFixtureTo(s.b, "content.json")
	return s
}

func (s *contentScenario) url(blob string) string {
	return "https://" + fixturetransport.ContentHost + "/" + blob
}

func (s *contentScenario) blobFile(t *testctx.T, blob string) string {
	alg, hex, ok := strings.Cut(blob, ":")
	require.True(t, ok)
	return "blobs/" + alg + "/" + hex
}

// script answers every layer's URL with the given response shape.
func (s *contentScenario) script(t *testctx.T, shape func(blob string, response *fixturetransport.Response)) {
	var responses []fixturetransport.Response
	for _, blob := range s.digests {
		response := fixturetransport.Response{URL: s.url(blob), BodyFile: s.blobFile(t, blob), Rangeable: true}
		if shape != nil {
			shape(blob, &response)
		}
		responses = append(responses, response)
	}
	scriptOrigin(t, s.b, responses...)
}

func (s *contentScenario) importAndRead(ctx context.Context, t *testctx.T) (string, error) {
	var imported []transferFixtureMapping
	require.NoError(t, s.b.fixture("import", "content.json", nil, &imported))
	require.NotEmpty(t, imported)
	s.rHandle, s.rID = imported[0].Handle, imported[0].ResultID
	return dagger.Ref[*dagger.Directory](s.b.client, dagger.ID(s.rHandle)).File("payload.txt").Contents(ctx)
}

func (s *contentScenario) requests(report fixtureControlsReport, blob string) []*fixturetransport.Observation {
	var out []*fixturetransport.Observation
	for _, request := range report.Transport.Requests {
		if request.URL == s.url(blob) {
			out = append(out, request)
		}
	}
	return out
}

// contentClassificationCases are the native content and error classification
// row (design §4 "HTTP and content" and §5 as amended by B1), which B1 places
// under TestPipeline/FailedChain: what the real HTTP content path does with
// real responses. Every failure ends the offered chain for that demand and
// the saved producer restores the Directory once; which errors are permanent
// is proven in process with the controlled clock.
func contentClassificationCases(t *testctx.T) {
	t.Run("Served", func(ctx context.Context, t *testctx.T) {
		s := newContentScenario(ctx, t, "content-served")
		s.script(t, nil)
		contents, err := s.importAndRead(ctx, t)
		require.NoError(t, err)
		require.Equal(t, s.payload, contents)
		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		require.Len(t, partEventsOf(report.transferFixtureReport, s.rID, "installed-chain"), 1)
		require.Empty(t, partEventsOf(report.transferFixtureReport, s.rID, "lazy-enter"))
		require.Empty(t, report.reachedAt(dagql.FixtureRenewalEnqueued), "a live address needs no renewal")
		for i, blob := range s.digests {
			requests := s.requests(report, blob)
			require.NotEmpty(t, requests, "layer %s was fetched from its address", blob)
			var read int64
			for _, request := range requests {
				require.Contains(t, []int{200, 206}, request.Status)
				require.False(t, request.Authorization)
				require.True(t, request.Closed, "every response body is closed")
				read += request.BodyBytesRead
			}
			require.GreaterOrEqual(t, read, s.sizes[i], "the whole blob was read and verified")
		}
	})

	for _, tc := range []struct {
		name  string
		shape func(response *fixturetransport.Response)
	}{
		{"Status401", func(r *fixturetransport.Response) { r.Status, r.BodyFile = 401, "" }},
		{"Status403", func(r *fixturetransport.Response) { r.Status, r.BodyFile = 403, "" }},
		{"Status404", func(r *fixturetransport.Response) { r.Status, r.BodyFile = 404, "" }},
		{"Status410", func(r *fixturetransport.Response) { r.Status, r.BodyFile = 410, "" }},
		{"Status500", func(r *fixturetransport.Response) { r.Status, r.BodyFile = 500, "" }},
		{"TransportFault", func(r *fixturetransport.Response) { r.Fault = "transport" }},
		{"TruncatedBody", func(r *fixturetransport.Response) { r.Fault, r.TruncateAt = "truncate", 7 }},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			s := newContentScenario(ctx, t, "content-"+strings.ToLower(tc.name))
			s.script(t, func(_ string, response *fixturetransport.Response) { tc.shape(response) })
			contents, err := s.importAndRead(ctx, t)
			require.NoError(t, err, "the saved producer restores the Directory")
			require.Equal(t, s.payload, contents)
			var report fixtureControlsReport
			require.NoError(t, s.b.fixture("report", "", nil, &report))
			t.Logf("%s: R=%d events: %v", tc.name, s.rID, partKindsOf(report.transferFixtureReport, s.rID))
			for _, request := range report.Transport.Requests {
				t.Logf("  %s %s range=%q status=%d err=%q read=%d closed=%t", request.Method, request.URL, request.Range, request.Status, request.Error, request.BodyBytesRead, request.Closed)
			}
			require.Len(t, partEventsOf(report.transferFixtureReport, s.rID, "lazy-enter"), 1, "one fallback")
			require.Empty(t, partEventsOf(report.transferFixtureReport, s.rID, "installed-chain"))
			if strings.HasPrefix(tc.name, "Status4") {
				require.Len(t, s.requests(report, s.digests[0]), 1, "a permanent status is requested once")
			}
			for _, request := range report.Transport.Requests {
				if request.Status != 0 {
					require.True(t, request.Closed, "every response body is closed")
				}
			}
		})
	}

	// The address answers 200 with bytes of the right length and the wrong
	// digest: the real verifier refuses them and nothing is installed from
	// the chain.
	t.Run("DigestMismatch", func(ctx context.Context, t *testctx.T) {
		s := newContentScenario(ctx, t, "content-digest")
		blob := s.digests[len(s.digests)-1]
		s.b.volumeExec(`mkdir -p /fixture/corrupt; size=$(stat -c %s "/fixture/$BLOB"); head -c "$size" /dev/zero | tr '\0' 'x' > /fixture/corrupt/blob`, map[string]string{"BLOB": s.blobFile(t, blob)}, nil)
		s.script(t, func(current string, response *fixturetransport.Response) {
			if current == blob {
				response.BodyFile = "corrupt/blob"
			}
		})
		contents, err := s.importAndRead(ctx, t)
		require.NoError(t, err, "the saved producer restores the Directory")
		require.Equal(t, s.payload, contents)
		var report fixtureControlsReport
		require.NoError(t, s.b.fixture("report", "", nil, &report))
		t.Logf("R=%d events: %v", s.rID, partKindsOf(report.transferFixtureReport, s.rID))
		require.Len(t, partEventsOf(report.transferFixtureReport, s.rID, "lazy-enter"), 1)
		require.Empty(t, partEventsOf(report.transferFixtureReport, s.rID, "installed-chain"), "corrupt bytes are never installed")
	})
}
