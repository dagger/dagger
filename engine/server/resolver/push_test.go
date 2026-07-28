package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	localcontentstore "github.com/containerd/containerd/v2/plugins/content/local"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestPushImageRetriesThrottledUpload(t *testing.T) {
	ctx := context.Background()
	img, store, expectedBlobs := newTestPushImage(t, ctx, 10)
	registry := newThrottledPushRegistry(t, expectedBlobs)
	t.Cleanup(registry.Close)
	defer registry.releaseUploads()
	registryHost := strings.TrimPrefix(registry.URL, "http://")

	rslvr := New(Opts{
		Hosts: func(domain string) ([]docker.RegistryHost, error) {
			require.Equal(t, registryHost, domain)
			return []docker.RegistryHost{
				{
					Client: registry.Client(),
					Scheme: "http",
					Host:   registryHost,
					Path:   "/v2",
					Capabilities: docker.HostCapabilityPull |
						docker.HostCapabilityResolve |
						docker.HostCapabilityPush,
				},
			}, nil
		},
		ContentStore: store,
		LeaseManager: newTestLeaseManager(),
	})
	t.Cleanup(func() {
		require.NoError(t, rslvr.Close())
	})

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- rslvr.PushImage(
			ctx,
			img,
			registryHost+"/dagger/test:latest",
			PushOpts{
				RegistryTransport: RegistryTransport{
					Protocol: RegistryProtocolHTTP,
				},
			},
		)
	}()

	select {
	case <-registry.uploadLimitReached:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for concurrent uploads")
	}
	if rslvr.pushLimiter.TryAcquire(1) {
		rslvr.pushLimiter.Release(1)
		t.Fatal("registry uploads did not hold all limiter slots")
	}
	registry.releaseUploads()

	select {
	case err := <-pushDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for image push")
	}
	require.NoError(t, registry.err())
	require.Equal(t, 2, registry.throttledAttempts())
	require.Equal(t, len(expectedBlobs), registry.firstUploadCount())
	require.LessOrEqual(t, registry.maxConcurrentUploads(), maxConcurrentRegistryUploads)
}

func newTestPushImage(t *testing.T, ctx context.Context, layerCount int) (*PushedImage, content.Store, map[digest.Digest][]byte) {
	t.Helper()

	store, err := localcontentstore.NewLabeledStore(t.TempDir(), newTestContentLabelStore())
	require.NoError(t, err)

	expectedBlobs := make(map[digest.Digest][]byte, layerCount+1)
	layers := make([]ocispecs.Descriptor, 0, layerCount)
	for i := range layerCount {
		payload := bytes.Repeat([]byte(fmt.Sprintf("layer-%02d-", i)), 16)
		desc := testDescriptor(ocispecs.MediaTypeImageLayerGzip, payload)
		layers = append(layers, desc)
		expectedBlobs[desc.Digest] = payload
		require.NoError(t, content.WriteBlob(ctx, store, fmt.Sprintf("seed-layer-%d", i), bytes.NewReader(payload), desc))
	}

	configBytes := []byte(`{"architecture":"amd64","os":"linux","config":{}}`)
	configDesc := testDescriptor(ocispecs.MediaTypeImageConfig, configBytes)
	expectedBlobs[configDesc.Digest] = configBytes
	require.NoError(t, content.WriteBlob(ctx, store, "seed-config", bytes.NewReader(configBytes), configDesc))

	manifestBytes, err := json.Marshal(ocispecs.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecs.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    layers,
	})
	require.NoError(t, err)
	manifestDesc := testDescriptor(ocispecs.MediaTypeImageManifest, manifestBytes)
	manifestDesc.Platform = &ocispecs.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	require.NoError(t, content.WriteBlob(ctx, store, "seed-manifest", bytes.NewReader(manifestBytes), manifestDesc))

	indexBytes, err := json.Marshal(ocispecs.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecs.MediaTypeImageIndex,
		Manifests: []ocispecs.Descriptor{manifestDesc},
	})
	require.NoError(t, err)
	rootDesc := testDescriptor(ocispecs.MediaTypeImageIndex, indexBytes)
	require.NoError(t, content.WriteBlob(ctx, store, "seed-index", bytes.NewReader(indexBytes), rootDesc))

	return &PushedImage{
		RootDesc: rootDesc,
		Provider: store,
	}, store, expectedBlobs
}

type throttledPushRegistry struct {
	*httptest.Server

	expectedBlobs map[digest.Digest][]byte

	uploadLimitReached chan struct{}
	uploadsReleased    chan struct{}
	releaseOnce        sync.Once
	limitReachedOnce   sync.Once

	mu                sync.Mutex
	firstErr          error
	activeUploads     int
	maxActiveUploads  int
	firstUploads      int
	throttledDigest   digest.Digest
	attempts          map[digest.Digest]int
	nextUploadSession int
}

func newThrottledPushRegistry(t *testing.T, expectedBlobs map[digest.Digest][]byte) *throttledPushRegistry {
	t.Helper()

	registry := &throttledPushRegistry{
		expectedBlobs:      expectedBlobs,
		uploadLimitReached: make(chan struct{}),
		uploadsReleased:    make(chan struct{}),
		attempts:           map[digest.Digest]int{},
	}
	registry.Server = httptest.NewServer(http.HandlerFunc(registry.serveHTTP))
	return registry
}

func (r *throttledPushRegistry) serveHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.URL.Path == "/v2/":
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)

	case req.Method == http.MethodHead &&
		(strings.Contains(req.URL.Path, "/blobs/") || strings.Contains(req.URL.Path, "/manifests/")):
		http.NotFound(w, req)

	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/blobs/uploads/"):
		r.mu.Lock()
		r.nextUploadSession++
		uploadID := r.nextUploadSession
		r.mu.Unlock()
		w.Header().Set("Docker-Upload-UUID", fmt.Sprintf("upload-%d", uploadID))
		w.Header().Set("Location", fmt.Sprintf("/v2/dagger/test/blobs/uploads/upload-%d", uploadID))
		w.Header().Set("Range", "0-0")
		w.WriteHeader(http.StatusAccepted)

	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/blobs/uploads/"):
		r.serveBlobUpload(w, req)

	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/manifests/"):
		payload, err := io.ReadAll(req.Body)
		if err != nil {
			r.recordError(fmt.Errorf("read manifest body: %w", err))
			http.Error(w, "read manifest", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(payload).String())
		w.Header().Set("Location", req.URL.Path)
		w.WriteHeader(http.StatusCreated)

	default:
		http.NotFound(w, req)
	}
}

func (r *throttledPushRegistry) serveBlobUpload(w http.ResponseWriter, req *http.Request) {
	dgst, err := digest.Parse(req.URL.Query().Get("digest"))
	if err != nil {
		r.recordError(fmt.Errorf("parse upload digest: %w", err))
		http.Error(w, "invalid digest", http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	r.activeUploads++
	if r.activeUploads > r.maxActiveUploads {
		r.maxActiveUploads = r.activeUploads
	}
	if r.activeUploads == maxConcurrentRegistryUploads {
		r.limitReachedOnce.Do(func() {
			close(r.uploadLimitReached)
		})
	}
	r.attempts[dgst]++
	attempt := r.attempts[dgst]
	if attempt == 1 {
		r.firstUploads++
	}
	if r.throttledDigest == "" {
		r.throttledDigest = dgst
	}
	throttled := r.throttledDigest == dgst
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.activeUploads--
		r.mu.Unlock()
	}()

	<-r.uploadsReleased

	payload, err := io.ReadAll(req.Body)
	if err != nil {
		r.recordError(fmt.Errorf("read blob %s attempt %d: %w", dgst, attempt, err))
		http.Error(w, "read blob", http.StatusInternalServerError)
		return
	}
	expected, ok := r.expectedBlobs[dgst]
	if !ok {
		r.recordError(fmt.Errorf("unexpected blob %s", dgst))
		http.Error(w, "unexpected blob", http.StatusBadRequest)
		return
	}
	if !bytes.Equal(expected, payload) {
		r.recordError(fmt.Errorf("blob %s attempt %d: expected %d bytes, got %d", dgst, attempt, len(expected), len(payload)))
		http.Error(w, "invalid blob", http.StatusBadRequest)
		return
	}

	if throttled && attempt == 1 {
		w.Header().Set("Retry-After", "0")
		http.Error(w, "retry upload", http.StatusTooManyRequests)
		return
	}

	w.Header().Set("Docker-Content-Digest", dgst.String())
	w.Header().Set("Location", req.URL.Path)
	w.WriteHeader(http.StatusCreated)
}

func (r *throttledPushRegistry) releaseUploads() {
	r.releaseOnce.Do(func() {
		close(r.uploadsReleased)
	})
}

func (r *throttledPushRegistry) recordError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.firstErr == nil {
		r.firstErr = err
	}
}

func (r *throttledPushRegistry) err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.firstErr
}

func (r *throttledPushRegistry) throttledAttempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts[r.throttledDigest]
}

func (r *throttledPushRegistry) firstUploadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.firstUploads
}

func (r *throttledPushRegistry) maxConcurrentUploads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxActiveUploads
}
