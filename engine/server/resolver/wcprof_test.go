package resolver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/remotes/docker"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// profiledEvent is a dumped event with its strings resolved.
type profiledEvent struct {
	wcprof.DumpEvent
	class, ident string
}

func profiledRoot(ctx context.Context) (context.Context, *wcprof.Op) {
	wcprof.EnsureRecorder()
	return wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindCall, "test.registry", wcprof.OpOpts{})
}

// profiledOps returns the ops recorded under the op root, by class.
func profiledOps(t *testing.T, root uint64) map[string][]profiledEvent {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	parents := map[uint64]uint64{}
	for _, ev := range events {
		if ev.Type == "op" {
			parents[ev.OpID] = ev.ParentID
		}
	}
	out := map[string][]profiledEvent{}
	for _, ev := range events {
		if ev.Type != "op" || ev.OpID == root {
			continue
		}
		id := ev.OpID
		for ; id != 0 && id != root; id = parents[id] {
		}
		if id == root {
			class := header.Strings[ev.ClassID]
			out[class] = append(out[class], profiledEvent{DumpEvent: ev, class: class, ident: header.Strings[ev.IdentID]})
		}
	}
	return out
}

// A profiled pull is an io op naming the ref, external work. Each blob it
// downloads is an op under it with the blob's digest and the bytes read; a
// blob already in the store isn't downloaded, and has none.
func TestPullProfiled(t *testing.T) {
	s := seedPinnedMetadata(t, false)
	ctx, root := profiledRoot(t.Context())
	pulled, err := s.resolver.Pull(ctx, s.ref, s.opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pulled.Release(context.WithoutCancel(ctx))) })
	root.End(wcprof.OutcomeOK)

	ops := profiledOps(t, root.ID())
	require.Len(t, ops["image.pull"], 1)
	pull := ops["image.pull"][0]
	require.Equal(t, "io", pull.OpKind)
	require.Equal(t, "external", pull.WorkType)
	require.Equal(t, "ok", pull.Outcome)
	require.Equal(t, s.ref, pull.ident)
	require.Len(t, ops["image.fetch"], 1, "only the layer is downloaded; the metadata is local")
	fetch := ops["image.fetch"][0]
	require.Equal(t, pull.OpID, fetch.ParentID)
	require.Equal(t, s.image.layer.Digest.String(), fetch.ident)
	require.EqualValues(t, len(s.image.layerBytes), fetch.Bytes)
	require.Equal(t, "external", fetch.WorkType)
	require.Equal(t, "ok", fetch.Outcome)
	require.GreaterOrEqual(t, fetch.StartNS, pull.StartNS)
	require.LessOrEqual(t, fetch.EndNS, pull.EndNS)
}

// A profiled resolve is an io op naming the ref. Its fetches aren't
// recorded: they run in a flight other resolves of the ref may share, which
// can outlive the caller whose op they would be under.
func TestResolveImageConfigProfiled(t *testing.T) {
	s := seedPinnedMetadata(t, false)
	// The resolve has to download the config again.
	s.deleteBlob(t, s.image.config)
	ctx, root := profiledRoot(t.Context())
	_, _, config, err := s.resolver.ResolveImageConfig(ctx, s.taggedRef, ResolveImageConfigOpts{Platform: &s.opts.Platform})
	require.NoError(t, err)
	require.NotEmpty(t, config)
	root.End(wcprof.OutcomeOK)
	_, err = s.store.Info(t.Context(), s.image.config.Digest)
	require.NoError(t, err, "the resolve fetched the config")

	ops := profiledOps(t, root.ID())
	require.Len(t, ops["image.resolve"], 1)
	require.Equal(t, s.taggedRef, ops["image.resolve"][0].ident)
	require.Equal(t, "external", ops["image.resolve"][0].WorkType)
	require.Empty(t, ops["image.fetch"])

	ctx, root = profiledRoot(t.Context())
	_, _, err = s.resolver.ResolveImageDigest(ctx, s.taggedRef, ResolveImageDigestOpts{})
	require.NoError(t, err)
	root.End(wcprof.OutcomeOK)
	ops = profiledOps(t, root.ID())
	require.Len(t, ops["image.resolve"], 1, "resolving a digest is a resolve too")
	require.Equal(t, s.taggedRef, ops["image.resolve"][0].ident)
}

// A profiled push is an io op naming the ref, and each blob's upload an op
// under it with the blob's digest and size.
func TestPushImageProfiled(t *testing.T) {
	img, store, blobs := newTestPushImage(t, t.Context(), 2)
	registry := newThrottledPushRegistry(t, blobs)
	t.Cleanup(registry.Close)
	registry.releaseUploads()
	host := strings.TrimPrefix(registry.URL, "http://")
	rslvr := New(Opts{
		Hosts: func(string) ([]docker.RegistryHost, error) {
			return []docker.RegistryHost{{
				Client: registry.Client(), Scheme: "http", Host: host, Path: "/v2",
				Capabilities: docker.HostCapabilityPull | docker.HostCapabilityResolve | docker.HostCapabilityPush,
			}}, nil
		},
		ContentStore: store,
		LeaseManager: newTestLeaseManager(),
	})
	t.Cleanup(func() { require.NoError(t, rslvr.Close()) })

	ctx, root := profiledRoot(t.Context())
	ref := host + "/dagger/test:latest"
	require.NoError(t, rslvr.PushImage(ctx, img, ref, PushOpts{RegistryTransport: RegistryTransport{Protocol: RegistryProtocolHTTP}}))
	root.End(wcprof.OutcomeOK)

	ops := profiledOps(t, root.ID())
	require.Len(t, ops["image.push"], 1)
	push := ops["image.push"][0]
	require.Equal(t, ref, push.ident)
	require.Equal(t, "external", push.WorkType)
	uploaded := map[string]int64{}
	for _, op := range ops["image.push.blob"] {
		require.Equal(t, push.OpID, op.ParentID)
		if op.Outcome == "ok" {
			uploaded[op.ident] = op.Bytes
		}
	}
	for dgst, payload := range blobs {
		require.EqualValues(t, len(payload), uploaded[dgst.String()], "blob %s", dgst)
	}
	require.Contains(t, uploaded, img.RootDesc.Digest.String(), "the index is pushed too")
}

// stubFetcher returns body for every fetch.
type stubFetcher struct{ body func() io.ReadCloser }

func (f stubFetcher) Fetch(context.Context, ocispecs.Descriptor) (io.ReadCloser, error) {
	return f.body(), nil
}

// seekBody reads and seeks, and doesn't read at offsets.
type seekBody struct{ r *bytes.Reader }

func (b seekBody) Read(p []byte) (int, error)                { return b.r.Read(p) }
func (b seekBody) Seek(off int64, whence int) (int64, error) { return b.r.Seek(off, whence) }
func (seekBody) Close() error                                { return nil }

// readerAtBody also reads at offsets.
type readerAtBody struct{ *bytes.Reader }

func (readerAtBody) Close() error { return nil }

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (failingBody) Close() error             { return nil }

// A profiled fetch's body seeks when the registry's does, so a resumed
// download still skips what it has; and a fetch whose body fails to read is
// recorded as failed.
func TestProfiledFetcherKeepsSeekingAndRecordsFailures(t *testing.T) {
	desc := ocispecs.Descriptor{Digest: digest.FromString("blob"), Size: 4}
	ctx, root := profiledRoot(t.Context())

	seeking := profiledFetcher{Fetcher: stubFetcher{body: func() io.ReadCloser { return seekBody{bytes.NewReader([]byte("blob"))} }}}
	body, err := seeking.Fetch(ctx, desc)
	require.NoError(t, err)
	seeker, ok := body.(io.Seeker)
	require.True(t, ok, "the body still seeks")
	_, err = seeker.Seek(2, io.SeekStart)
	require.NoError(t, err)
	rest, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "ob", string(rest))
	require.NoError(t, body.Close())

	plain := profiledFetcher{Fetcher: stubFetcher{body: func() io.ReadCloser { return io.NopCloser(strings.NewReader("blob")) }}}
	body, err = plain.Fetch(ctx, desc)
	require.NoError(t, err)
	_, ok = body.(io.Seeker)
	require.False(t, ok, "a body that didn't seek doesn't claim to")
	require.NoError(t, body.Close())

	// A body that reads at offsets is the fetcher's own: its reads bypass Read.
	readerAt := profiledFetcher{Fetcher: stubFetcher{body: func() io.ReadCloser { return readerAtBody{bytes.NewReader([]byte("blob"))} }}}
	body, err = readerAt.Fetch(ctx, desc)
	require.NoError(t, err)
	require.IsType(t, readerAtBody{}, body)
	require.NoError(t, body.Close())

	failing := profiledFetcher{Fetcher: stubFetcher{body: func() io.ReadCloser { return failingBody{} }}}
	body, err = failing.Fetch(ctx, desc)
	require.NoError(t, err)
	_, err = io.ReadAll(body)
	require.Error(t, err)
	require.NoError(t, body.Close())
	root.End(wcprof.OutcomeOK)

	var outcomes []string
	var read []int64
	for _, op := range profiledOps(t, root.ID())["image.fetch"] {
		outcomes = append(outcomes, op.Outcome)
		read = append(read, op.Bytes)
	}
	require.ElementsMatch(t, []string{"ok", "ok", "ok", "error"}, outcomes)
	require.ElementsMatch(t, []int64{2, 0, 0, 0}, read, "the bytes actually read")
}

// A registry that fails a blob's download is a failed fetch: the docker
// fetcher sends its request lazily, on the body's first read.
func TestFetchProfiledRegistryFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	resolver := docker.NewResolver(docker.ResolverOptions{Hosts: func(string) ([]docker.RegistryHost, error) {
		return []docker.RegistryHost{{Client: server.Client(), Host: host, Scheme: "http", Path: "/v2", Capabilities: docker.HostCapabilityPull}}, nil
	}})
	ctx, root := profiledRoot(t.Context())
	fetcher, err := profFetcher(ctx, resolver, host+"/test/image")
	require.NoError(t, err)
	body, err := fetcher.Fetch(ctx, ocispecs.Descriptor{Digest: digest.FromString("missing"), Size: 7, MediaType: ocispecs.MediaTypeImageLayer})
	if err == nil {
		_, err = io.ReadAll(body)
		_ = body.Close()
	}
	require.Error(t, err)
	root.End(wcprof.OutcomeOK)

	fetches := profiledOps(t, root.ID())["image.fetch"]
	require.Len(t, fetches, 1)
	require.Equal(t, "error", fetches[0].Outcome)
}
