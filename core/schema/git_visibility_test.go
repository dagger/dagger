package schema

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/stretchr/testify/require"
)

func TestPublicRemoteAdvertisement(t *testing.T) {
	source := t.TempDir()
	run := func(args ...string) []byte {
		t.Helper()
		args = append([]string{"-C", source, "-c", "user.name=Dagger", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return out
	}
	run("init", "--quiet", "--initial-branch=main")
	run("commit", "--allow-empty", "-m", "initial")
	run("tag", "-a", "v1", "-m", "annotated")
	run("tag", "lightweight")
	run("branch", "other")
	advertisement := run("upload-pack", "--stateless-rpc", "--advertise-refs", ".")
	var requests atomic.Int64
	var private atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("public probe attached credentials")
		}
		if private.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/repo/info/refs" || r.URL.Query().Get("service") != "git-upload-pack" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		fmt.Fprint(w, "001e# service=git-upload-pack\n0000")
		_, _ = w.Write(advertisement)
	}))
	t.Cleanup(server.Close)
	remoteURL, err := gitutil.ParseURL(server.URL + "/repo")
	require.NoError(t, err)
	ctx := t.Context()
	want, err := gitutil.NewGitCLI().LsRemote(ctx, remoteURL.Remote())
	require.NoError(t, err)
	require.Len(t, want.Refs, 6) // HEAD, two branches, two tags, peeled annotated tag
	require.Equal(t, "refs/heads/main", want.Symrefs["HEAD"])
	requests.Store(0)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = engine.ContextWithClientMetadata(dagql.ContextWithCache(ctx, cache), &engine.ClientMetadata{ClientID: "cli", SessionID: "first"})

	// Independent clients in one session share the probe and the canonical
	// metadata cache. Remote must succeed without a Query/engine or another GET.
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			clientCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: fmt.Sprint(i), SessionID: "first"})
			probe, err := cachedPublicRemote(clientCtx, remoteURL, false)
			if !assertPublicProbe(t, probe, err) {
				return
			}
			backend := &core.RemoteGitRepository{URL: remoteURL}
			if err := backend.PrimePublicRemote(clientCtx, probe.metadata); err != nil {
				t.Error(err)
				return
			}
			got, err := backend.Remote(clientCtx)
			if err != nil {
				t.Error(err)
				return
			}
			if fmt.Sprint(got.Digest()) != fmt.Sprint(want.Digest()) {
				t.Errorf("different digest: %s != %s", got.Digest(), want.Digest())
			}
			// Callers may alter their own HEAD/refs without mutating cached data.
			got.Refs[0].SHA = "changed"
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, requests.Load())
	backend := &core.RemoteGitRepository{URL: remoteURL}
	got, err := backend.Remote(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)

	// A service binding bypasses the shared visibility result.
	_, err = cachedPublicRemote(ctx, remoteURL, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, requests.Load())

	private.Store(true)
	probe, err := cachedPublicRemote(ctx, remoteURL, false)
	require.NoError(t, err)
	require.True(t, probe.public, "existing session retains its original view")
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "cli", SessionID: "second"})
	probe, err = cachedPublicRemote(ctx, remoteURL, false)
	require.NoError(t, err)
	require.False(t, probe.public, "new session rechecks visibility")
	require.Nil(t, probe.metadata)
	require.EqualValues(t, 3, requests.Load())
}

func assertPublicProbe(t *testing.T, probe publicRemoteProbe, err error) bool {
	t.Helper()
	if err != nil || !probe.public || probe.metadata == nil {
		t.Errorf("invalid public probe: %+v, %v", probe, err)
		return false
	}
	return true
}

func TestRemoteAdvertisementDoesNotInventSymbolicHEAD(t *testing.T) {
	advertised := packp.NewAdvRefs()
	hash := plumbing.NewHash(strings.Repeat("a", 40))
	advertised.Head = &hash
	advertised.References["refs/heads/main"] = hash
	got := remoteFromAdvertisement(advertised)
	require.Empty(t, got.Symrefs)
	require.Equal(t, []*gitutil.Ref{{Name: "HEAD", SHA: hash.String()}, {Name: "refs/heads/main", SHA: hash.String()}}, got.Refs)
}
