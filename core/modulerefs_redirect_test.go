package core

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestDaggerGetEligible(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{"www.marcosnils.com/go", true},
		{"https://www.marcosnils.com/go", true},
		{"github.com/dagger/dagger", true},
		{"www.marcosnils.com/go@v1.2.3", true},
		{"http://example.com/mod", false},
		{"ssh://git@github.com/foo/bar", false},
		{"git://example.com/foo", false},
		{"git@github.com:foo/bar", false},
		{"./local/path", false},
		{"/abs/path", false},
		{"nodot/path", false},
		{"", false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			require.Equal(t, tc.want, daggerGetEligible(tc.ref))
		})
	}
}

func TestDaggerGetProbe(t *testing.T) {
	// Server redirects only the exact dagger-get probe and echoes the flag back
	// in Location to verify it gets stripped.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get(daggerGetQueryParam) == "1" && r.URL.Path == "/go" {
			http.Redirect(w, r, "https://github.com/dagger/dagger?dagger-get=1", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	host := srv.Listener.Addr().String()

	t.Run("redirect strips dagger-get and preserves version", func(t *testing.T) {
		got := daggerGetProbe(t.Context(), "https://"+host+"/go@v1.2.3")
		require.Equal(t, "https://github.com/dagger/dagger@v1.2.3", got)
	})

	t.Run("redirect without version", func(t *testing.T) {
		got := daggerGetProbe(t.Context(), "https://"+host+"/go")
		require.Equal(t, "https://github.com/dagger/dagger", got)
	})

	t.Run("no redirect falls back to original", func(t *testing.T) {
		ref := "https://" + host + "/other"
		require.Equal(t, ref, daggerGetProbe(t.Context(), ref))
	})
}

func TestDaggerGetProbeRejectsNonHTTPSLocation(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://github.com/dagger/dagger", http.StatusFound)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	ref := "https://" + srv.Listener.Addr().String() + "/go"
	require.Equal(t, ref, daggerGetProbe(t.Context(), ref))
}

func TestDaggerGetProbeIgnoresCanonicalizationRedirects(t *testing.T) {
	// GitHub (and other hosts) 301 "repo.git" -> "repo", "www." -> apex, etc.
	// for the same repository. These must not be treated as dagger-get
	// redirects: the user's ref must stay untouched.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		switch {
		case strings.HasSuffix(r.URL.Path, ".git"):
			http.Redirect(w, r,
				"https://"+host+strings.TrimSuffix(r.URL.Path, ".git")+"?"+r.URL.RawQuery,
				http.StatusMovedPermanently)
		case strings.HasSuffix(r.URL.Path, "/slash/"):
			http.Redirect(w, r, "https://"+host+strings.TrimSuffix(r.URL.Path, "/"), http.StatusMovedPermanently)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	host := srv.Listener.Addr().String()

	t.Run("dot-git canonicalization is ignored", func(t *testing.T) {
		ref := "https://" + host + "/dagger/dagger.git"
		require.Equal(t, ref, daggerGetProbe(t.Context(), ref))
	})

	t.Run("dot-git canonicalization with version is ignored", func(t *testing.T) {
		ref := "https://" + host + "/dagger/dagger.git@v0.16.2"
		require.Equal(t, ref, daggerGetProbe(t.Context(), ref))
	})

	t.Run("trailing-slash canonicalization is ignored", func(t *testing.T) {
		ref := "https://" + host + "/foo/slash/"
		require.Equal(t, ref, daggerGetProbe(t.Context(), ref))
	})
}

func TestDaggerGetProbeIgnoresAuthWallRedirects(t *testing.T) {
	// GitLab 302s unauthenticated repo paths to /users/sign_in and drops the
	// query string. Without the dagger-get marker in Location, the redirect
	// must be ignored.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+"/users/sign_in", http.StatusFound)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	host := srv.Listener.Addr().String()
	ref := "https://" + host + "/dagger-modules/test/more/dagger-test-modules-public/top-level@d730fb3af8757e1ca293e01aa4fcfd510a6e40e5"
	require.Equal(t, ref, daggerGetProbe(t.Context(), ref))
}

func TestResolveDaggerGetRedirectCachesPerSession(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://github.com/dagger/dagger?dagger-get=1", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)

	ctxForSession := func(sessionID string) context.Context {
		ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{SessionID: sessionID})
		return dagql.ContextWithCache(ctx, cache)
	}

	ref := "https://" + srv.Listener.Addr().String() + "/go"
	ctxA := ctxForSession("session-a")
	ctxB := ctxForSession("session-b")

	resolved, err := ResolveDaggerGetRedirect(ctxA, ref)
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger", resolved)
	resolved, err = ResolveDaggerGetRedirect(ctxA, ref)
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger", resolved)
	require.EqualValues(t, 1, requests.Load(), "same-session lookup should hit the cache")

	resolved, err = ResolveDaggerGetRedirect(ctxB, ref)
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger", resolved)
	require.EqualValues(t, 2, requests.Load(), "different sessions should not share redirect results")
}

func TestResolveDaggerGetRedirectRequiresSessionInfrastructure(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://github.com/dagger/dagger", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)

	ref := "https://" + srv.Listener.Addr().String() + "/go"
	for name, ctx := range map[string]context.Context{
		"no session infrastructure": t.Context(),
		"cache without metadata":    dagql.ContextWithCache(t.Context(), cache),
		"metadata without cache": engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
			SessionID: "session-a",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			resolved, err := ResolveDaggerGetRedirect(ctx, ref)
			require.NoError(t, err)
			require.Equal(t, ref, resolved)
		})
	}
	require.Zero(t, requests.Load())
}

func TestResolveDaggerGetRedirectUsesWorkspaceLock(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://github.com/dagger/dagger?dagger-get=1", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	lock := workspace.NewLock()
	ref := "https://127.0.0.1/go"
	require.NoError(t, lock.SetLookup(
		workspace.CoreLockNamespace,
		workspace.LockOperationVanityURL,
		[]any{ref},
		"https://github.com/dagger/dagger",
	))
	ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{workspaceLock: lock}})

	for _, input := range []string{ref + "@main", strings.TrimPrefix(ref, "https://") + "@main"} {
		resolved, err := ResolveDaggerGetRedirect(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger@main", resolved)
	}
	require.Zero(t, requests.Load(), "lock hit should avoid the HTTP probe")
}

func TestResolveDaggerGetRedirectWritesWorkspaceLock(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://github.com/dagger/dagger?dagger-get=1", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	// Use a port-free vanity URL; schemeless host:port refs are SSH-like.
	daggerGetClient.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	lock := workspace.NewLock()
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{workspaceLock: lock, lockWritable: true}})
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: "session-a"})
	ctx = dagql.ContextWithCache(ctx, cache)
	ref := "https://127.0.0.1/go"

	resolved, err := ResolveDaggerGetRedirect(ctx, strings.TrimPrefix(ref, "https://")+"@main")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger@main", resolved)
	require.EqualValues(t, 1, requests.Load())
	locked, ok := lock.GetLookup(
		workspace.CoreLockNamespace,
		workspace.LockOperationVanityURL,
		[]any{ref},
	)
	require.True(t, ok)
	require.Equal(t, "https://github.com/dagger/dagger", locked)
	_, ok = lock.GetLookup(
		workspace.CoreLockNamespace,
		workspace.LockOperationVanityURL,
		[]any{strings.TrimPrefix(ref, "https://")},
	)
	require.False(t, ok, "must not write a schemeless lockfile key")

	// A different spelling must reuse the lock, not the session cache.
	resolved, err = ResolveDaggerGetRedirect(ctx, ref+"@main")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger@main", resolved)
	require.EqualValues(t, 1, requests.Load(), "HTTPS lookup should reuse the schemeless lookup's lock entries")

	// The host can rewrite each version, so a version that is not locked needs
	// one probe. The locked URL stays.
	resolved, err = ResolveDaggerGetRedirect(ctx, ref+"@other")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger@other", resolved)
	require.EqualValues(t, 2, requests.Load())
	resolved, err = ResolveDaggerGetRedirect(ctx, ref+"@other")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger@other", resolved)
	require.EqualValues(t, 2, requests.Load(), "a locked version must not probe again")
}

func TestSourceURLWithVersionOverridesDestinationVersion(t *testing.T) {
	for _, destination := range []string{
		"https://github.com/dagger/dagger@v1.2.3?other=1",
		"https://github.com/dagger/dagger?other=1#v1.2.3",
	} {
		require.Equal(t, destination, sourceURLWithVersion(destination, ""))
		require.Equal(t, "https://github.com/dagger/dagger@main?other=1", sourceURLWithVersion(destination, "main"))
	}
}

func TestResolveDaggerGetRedirectPreservesDestinationVersion(t *testing.T) {
	for _, destinationSuffix := range []string{"", "@v1.2.3", "#v1.2.3"} {
		for _, inputSuffix := range []string{"", "@main"} {
			t.Run(destinationSuffix+"/input="+inputSuffix, func(t *testing.T) {
				const repo = "https://github.com/dagger/dagger"
				destination := repo + destinationSuffix
				var requests atomic.Int32
				srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					require.Equal(t, "/go", r.URL.Path)
					location := repo + "?dagger-get=1"
					if strings.HasPrefix(destinationSuffix, "#") {
						location += destinationSuffix
					} else {
						location = destination + "?dagger-get=1"
					}
					http.Redirect(w, r, location, http.StatusTemporaryRedirect)
				}))
				defer srv.Close()
				oldClient := daggerGetClient
				daggerGetClient = srv.Client()
				daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				}
				defer func() { daggerGetClient = oldClient }()

				lock := workspace.NewLock()
				ctx := ContextWithVanityURLLookupLock(t.Context(), lock)
				cache, err := dagql.NewCache(t.Context(), "", nil, nil)
				require.NoError(t, err)
				probeCtx := dagql.ContextWithCache(ctx, cache)
				probeCtx = engine.ContextWithClientMetadata(probeCtx, &engine.ClientMetadata{SessionID: "first"})
				ref := srv.URL + "/go"
				resolved, err := ResolveDaggerGetRedirect(probeCtx, ref+inputSuffix)
				require.NoError(t, err)
				want := destination
				if inputSuffix != "" {
					want = repo + inputSuffix
				}
				require.Equal(t, want, resolved)
				locked, ok := lock.GetLookup(workspace.CoreLockNamespace, workspace.LockOperationVanityURL, []any{ref})
				require.True(t, ok)
				require.Equal(t, destination, locked)

				// Replay without session infrastructure: only the lock can resolve it.
				resolved, err = ResolveDaggerGetRedirect(ctx, ref)
				require.NoError(t, err)
				require.Equal(t, destination, resolved)
				resolved, err = ResolveDaggerGetRedirect(ctx, ref+"@other")
				require.NoError(t, err)
				require.Equal(t, repo+"@other", resolved)
				require.EqualValues(t, 1, requests.Load())

				require.NoError(t, UpdateWorkspaceLock(ctx, nil, lock))
				locked, ok = lock.GetLookup(workspace.CoreLockNamespace, workspace.LockOperationVanityURL, []any{ref})
				require.True(t, ok)
				require.Equal(t, destination, locked, "refresh must use the same destination format as creation")
				require.EqualValues(t, 2, requests.Load())
			})
		}
	}
}

// vanityVersionServer redirects /go to dagger/dagger. It rewrites the version
// "my feature" to a pull request ref, gives "v1" as the default version, and
// echoes any other version unchanged, as a passthrough host does.
func vanityVersionServer(t *testing.T, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			requests.Add(1)
		}
		q := r.URL.Query()
		if q.Get(daggerGetQueryParam) != "1" || r.URL.Path != "/go" || !q.Has(daggerVersionQueryParam) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		version := q.Get(daggerVersionQueryParam)
		switch version {
		case "my feature":
			version = "pull/7/head"
		case "":
			version = "v1"
		}
		loc := url.URL{Scheme: "https", Host: "github.com", Path: "/dagger/dagger"}
		loc.RawQuery = url.Values{
			daggerGetQueryParam:     {"1"},
			daggerVersionQueryParam: {version},
		}.Encode()
		http.Redirect(w, r, loc.String(), http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	// Use a port-free vanity URL; schemeless host:port refs are SSH-like.
	daggerGetClient.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	t.Cleanup(func() { daggerGetClient = oldClient })
	return srv
}

func TestDaggerGetProbeVersion(t *testing.T) {
	vanityVersionServer(t, nil)
	source := "https://127.0.0.1/go"

	t.Run("host rewrites the version", func(t *testing.T) {
		got := daggerGetProbeVersion(t.Context(), source, "my feature")
		require.Equal(t, daggerGetResult{SourceURL: "https://github.com/dagger/dagger", Version: "pull/7/head"}, got)
		require.Equal(t, "https://github.com/dagger/dagger@pull/7/head", daggerGetProbe(t.Context(), source+"@my feature"))
	})

	t.Run("echoed version is not a rewrite", func(t *testing.T) {
		got := daggerGetProbeVersion(t.Context(), source, "main")
		require.Equal(t, daggerGetResult{SourceURL: "https://github.com/dagger/dagger"}, got)
		require.Equal(t, "https://github.com/dagger/dagger@main", daggerGetProbe(t.Context(), source+"@main"))
	})

	t.Run("host gives a default version", func(t *testing.T) {
		got := daggerGetProbeVersion(t.Context(), source, "")
		require.Equal(t, daggerGetResult{SourceURL: "https://github.com/dagger/dagger", Version: "v1"}, got)
		require.Equal(t, "https://github.com/dagger/dagger@v1", daggerGetProbe(t.Context(), source))
	})
}

func TestResolveDaggerGetRedirectLocksVanityVersion(t *testing.T) {
	var requests atomic.Int32
	vanityVersionServer(t, &requests)

	lock := workspace.NewLock()
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	newCtx := func(sessionID string) context.Context {
		ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{workspaceLock: lock, lockWritable: true}})
		ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: sessionID})
		return dagql.ContextWithCache(ctx, cache)
	}
	source := "https://127.0.0.1/go"

	resolved, err := ResolveDaggerGetRedirect(newCtx("session-a"), "127.0.0.1/go@my feature")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger@pull/7/head", resolved)
	require.EqualValues(t, 1, requests.Load())

	lockedURL, ok := lock.GetLookup(workspace.CoreLockNamespace, workspace.LockOperationVanityURL, []any{source})
	require.True(t, ok)
	require.Equal(t, "https://github.com/dagger/dagger", lockedURL)
	lockedVersion, ok := lock.GetLookup(workspace.CoreLockNamespace, workspace.LockOperationVanityVersion, []any{source, "my feature"})
	require.True(t, ok)
	require.Equal(t, "pull/7/head", lockedVersion)

	// A new session has no cached probe. The lock alone must resolve the ref.
	resolved, err = ResolveDaggerGetRedirect(newCtx("session-b"), source+"@my feature")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger@pull/7/head", resolved)
	require.EqualValues(t, 1, requests.Load(), "a locked version must not probe again")
}

func TestUpdateVanityVersionLockEntry(t *testing.T) {
	vanityVersionServer(t, nil)
	source := "https://127.0.0.1/go"

	for requested, expected := range map[string]string{
		"my feature": "pull/7/head",
		"main":       "main",
		"":           "v1",
	} {
		actual, err := updateVanityVersionLockEntry(t.Context(), workspace.LookupEntry{
			Operation: workspace.LockOperationVanityVersion,
			Inputs:    []any{source, requested},
		})
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
}
