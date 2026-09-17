package schema

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type httpLazyServer struct{ *scratchTestServer }

func (*httpLazyServer) DNS() *oci.DNSConfig { return &oci.DNSConfig{} }

func TestHTTPResolvedCall(t *testing.T) {
	store := testutil.NewStore(t)
	ctx, cache, srv := scratchTestCache(t, store, "", "http-first")
	query, err := core.CurrentQuery(ctx)
	require.NoError(t, err)
	query.Server = &httpLazyServer{query.Server.(*scratchTestServer)}
	srv.InstallObject(dagql.NewClass[*core.File](srv))
	(&httpSchema{}).Install(srv)
	var requests atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("ETag", "saved")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		if r.Header.Get("If-None-Match") == "saved" {
			w.WriteHeader(304)
			return
		}
		fmt.Fprint(w, "saved")
	}))
	defer origin.Close()
	name := "data"
	selectFile := func(session string) dagql.ObjectResult[*core.File] {
		cctx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: session, SessionID: session})
		var result dagql.ObjectResult[*core.File]
		before := requests.Load()
		rowsBefore := cache.Size()
		require.NoError(t, srv.Select(cctx, srv.Root(), &result, dagql.Selector{Field: "http", Args: []dagql.NamedInput{{Name: "url", Value: dagql.String(origin.URL)}, {Name: "name", Value: dagql.Optional[dagql.String]{Valid: true, Value: dagql.String(name)}}, {Name: "permissions", Value: dagql.Optional[dagql.Int]{Valid: true, Value: 0644}}}}))
		require.EqualValues(t, 1, requests.Load()-before, "outer resolution in %s", session)
		t.Logf("HTTP selection %s: retained rows before=%d after=%d delta=%d", session, rowsBefore, cache.Size(), cache.Size()-rowsBefore)
		return result
	}
	first := selectFile("http-first")
	require.NotNil(t, first.Self().Lazy)
	require.False(t, first.Self().Lazy.IsEvaluated())
	_, set := first.Self().Snapshot.Peek()
	require.False(t, set)
	record, err := cache.CapturePersistedRecord(ctx, first)
	require.NoError(t, err)
	require.Equal(t, "__httpFile", record.Call.Field)
	require.Nil(t, record.Call.Receiver)
	require.Empty(t, record.Call.ImplicitInputs)
	names := []string{}
	for _, arg := range record.Call.Args {
		names = append(names, arg.Name)
	}
	require.ElementsMatch(t, []string{"url", "bodyDigest", "name", "permissions", "lastModified", "platform", "checksum"}, names)
	pending := selectFile("http-pending")
	same, err := cache.CapturePersistedRecord(ctx, pending)
	require.NoError(t, err)
	require.Equal(t, record.ResultID, same.ResultID)
	before := requests.Load()
	require.NoError(t, cache.Evaluate(ctx, pending))
	require.Equal(t, before, requests.Load(), "matching local body must avoid an operation request")
	require.True(t, pending.Self().Lazy.IsEvaluated())
	completed := selectFile("http-completed")
	same, err = cache.CapturePersistedRecord(ctx, completed)
	require.NoError(t, err)
	require.Equal(t, record.ResultID, same.ResultID)
	before = requests.Load()
	require.NoError(t, cache.Evaluate(ctx, completed))
	require.Equal(t, before, requests.Load())
	report, err := cache.TransferFixtureSnapshot(ctx, "http-first", nil)
	require.NoError(t, err)
	for _, row := range report.Rows {
		if row.ResultID == record.ResultID {
			require.Empty(t, row.DependencyIDs)
		}
	}
	name = "first-use"
	miss := selectFile("http-miss-first-use")
	require.False(t, miss.Self().Lazy.IsEvaluated())
	before = requests.Load()
	require.NoError(t, cache.Evaluate(ctx, miss))
	require.Equal(t, before, requests.Load())
	t.Log("HTTP requests: miss outer=1 operation=0; pending hit outer=1 operation=0; completed hit outer=1 operation=0")
}

func TestHTTPPendingInternalHits(t *testing.T) {
	for _, mode := range []string{"absent", "advanced", "changed-body"} {
		t.Run(mode, func(t *testing.T) {
			store := testutil.NewStore(t)
			ctx, cache, srv := scratchTestCache(t, store, "", "http-pending-hit")
			query, err := core.CurrentQuery(ctx)
			require.NoError(t, err)
			query.Server = &httpLazyServer{query.Server.(*scratchTestServer)}
			srv.InstallObject(dagql.NewClass[*core.File](srv))
			(&httpSchema{}).Install(srv)
			var requests atomic.Int64
			var body atomic.Value
			body.Store("saved")
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				require.Empty(t, r.Header.Get("If-None-Match"))
				require.Empty(t, r.Header.Get("If-Modified-Since"))
				w.Header().Set("ETag", body.Load().(string))
				fmt.Fprint(w, body.Load().(string))
			}))
			defer origin.Close()
			selectSaved := func() dagql.ObjectResult[*core.File] {
				var result dagql.ObjectResult[*core.File]
				require.NoError(t, srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "__httpFile", Args: []dagql.NamedInput{
					{Name: "url", Value: dagql.String(origin.URL)}, {Name: "bodyDigest", Value: dagql.String(digest.FromString("saved"))},
					{Name: "name", Value: dagql.String("data")}, {Name: "permissions", Value: dagql.Int(0644)},
					{Name: "lastModified", Value: dagql.String("")}, {Name: "platform", Value: core.Platform{OS: "linux", Architecture: "amd64"}},
				}}))
				return result
			}
			first := selectSaved()
			require.False(t, first.Self().Lazy.IsEvaluated())
			pending := selectSaved()
			require.Same(t, first.Self(), pending.Self())
			require.Zero(t, requests.Load(), "constructing or hitting the internal call performs no resolution")
			outerRequests := int64(0)
			if mode != "absent" {
				body.Store("advanced")
				var advanced dagql.ObjectResult[*core.File]
				require.NoError(t, srv.Select(ctx, srv.Root(), &advanced, dagql.Selector{Field: "http", Args: []dagql.NamedInput{{Name: "url", Value: dagql.String(origin.URL)}}}))
				outerRequests = requests.Load()
				require.EqualValues(t, 1, outerRequests)
				if mode == "advanced" {
					body.Store("saved")
				}
			}
			var state dagql.ObjectResult[*core.HTTPState]
			require.NoError(t, srv.Select(ctx, srv.Root(), &state, dagql.Selector{Field: "_httpState", Args: []dagql.NamedInput{{Name: "url", Value: dagql.String(origin.URL)}}}))
			before, err := cache.CapturePersistedRecord(ctx, state)
			require.NoError(t, err)
			err = cache.Evaluate(ctx, pending)
			if mode == "changed-body" {
				var mismatch *core.HTTPBodyDigestMismatchError
				require.ErrorAs(t, err, &mismatch)
				require.False(t, pending.Self().Lazy.IsEvaluated())
				_, installed := pending.Self().Snapshot.Peek()
				require.False(t, installed)
			} else {
				require.NoError(t, err)
				bytes := demandedFileContents(t, ctx, pending)
				require.Equal(t, "saved", string(bytes))
				completed := selectSaved()
				require.Same(t, pending.Self(), completed.Self())
				require.NoError(t, cache.Evaluate(ctx, completed))
			}
			require.EqualValues(t, 1, requests.Load()-outerRequests)
			after, err := cache.CapturePersistedRecord(ctx, state)
			require.NoError(t, err)
			require.Equal(t, before.Envelope, after.Envelope, "the operation cannot change state validators or identity")
			t.Logf("HTTP pending hit %s: outer=%d operation=1; completed usable hit operation=0 when evaluation succeeds", mode, outerRequests)
		})
	}
}
