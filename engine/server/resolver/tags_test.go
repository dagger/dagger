package resolver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/stretchr/testify/require"
)

func TestListImageTagsPaginatesAndAuthenticates(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/v2/acme/widget/tags/list", r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("last") == "v1.0.0" {
			fmt.Fprint(w, `{"name":"acme/widget","tags":["v2.0.0"]}`)
			return
		}
		w.Header().Set("Link", `</v2/acme/widget/tags/list?n=1000&last=v1.0.0>; rel="next"`)
		fmt.Fprint(w, `{"name":"acme/widget","tags":["v1.0.0"]}`)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	authorizer := newTestRegistryAuthorizer()
	host := docker.RegistryHost{
		Client:       server.Client(),
		Authorizer:   authorizer,
		Host:         serverURL.Host,
		Scheme:       serverURL.Scheme,
		Path:         "/v2",
		Capabilities: docker.HostCapabilityPull | docker.HostCapabilityResolve,
	}

	tags, err := listImageTagsFromHost(context.Background(), host, "example.com", "acme/widget")
	require.NoError(t, err)
	require.Equal(t, []string{"v1.0.0", "v2.0.0"}, tags)
	require.Equal(t, 3, requests)
}

func TestListImageTagsRejectsPaginationToAnotherHost(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<https://evil.example/tags/list>; rel="next"`)
		fmt.Fprint(w, `{"tags":[]}`)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	host := docker.RegistryHost{
		Client:       server.Client(),
		Host:         serverURL.Host,
		Scheme:       serverURL.Scheme,
		Path:         "/v2",
		Capabilities: docker.HostCapabilityResolve,
	}

	_, err = listImageTagsFromHost(context.Background(), host, "example.com", "acme/widget")
	require.ErrorContains(t, err, "pagination escaped host")
}

type testRegistryAuthorizer struct {
	mu         sync.Mutex
	authorized bool
}

func newTestRegistryAuthorizer() *testRegistryAuthorizer {
	return &testRegistryAuthorizer{}
}

func (a *testRegistryAuthorizer) Authorize(_ context.Context, req *http.Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.authorized {
		req.Header.Set("Authorization", "Bearer token")
	}
	return nil
}

func (a *testRegistryAuthorizer) AddResponses(_ context.Context, responses []*http.Response) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(responses) == 0 || !strings.Contains(responses[len(responses)-1].Header.Get("WWW-Authenticate"), "Bearer") {
		return fmt.Errorf("missing bearer challenge")
	}
	a.authorized = true
	return nil
}
