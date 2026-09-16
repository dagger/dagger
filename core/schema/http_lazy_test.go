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
		require.NoError(t, srv.Select(cctx, srv.Root(), &result, dagql.Selector{Field: "http", Args: []dagql.NamedInput{{Name: "url", Value: dagql.String(origin.URL)}, {Name: "name", Value: dagql.Optional[dagql.String]{Valid: true, Value: dagql.String(name)}}, {Name: "permissions", Value: dagql.Optional[dagql.Int]{Valid: true, Value: 0644}}}}))
		require.EqualValues(t, 1, requests.Load()-before, "outer resolution in %s", session)
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
