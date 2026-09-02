package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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

	require.Equal(t, "https://github.com/dagger/dagger", resolveDaggerGetRedirect(ctxA, ref))
	require.Equal(t, "https://github.com/dagger/dagger", resolveDaggerGetRedirect(ctxA, ref))
	require.EqualValues(t, 1, requests.Load(), "same-session lookup should hit the cache")

	require.Equal(t, "https://github.com/dagger/dagger", resolveDaggerGetRedirect(ctxB, ref))
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
			require.Equal(t, ref, resolveDaggerGetRedirect(ctx, ref))
		})
	}
	require.Zero(t, requests.Load())
}
