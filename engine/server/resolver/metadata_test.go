package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	localcontentstore "github.com/containerd/containerd/v2/plugins/content/local"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestResolveImageConfigFallsBackWhenLocalMetadataIncomplete(t *testing.T) {
	ctx := context.Background()
	image := newTestOCIIndexImage(t)

	store, err := localcontentstore.NewLabeledStore(t.TempDir(), newTestContentLabelStore())
	require.NoError(t, err)

	require.NoError(t, content.WriteBlob(ctx, store, "seed-root-index", bytes.NewReader(image.indexBytes), image.rootDesc))

	registry := newTestOCIRegistry(t, image)
	t.Cleanup(registry.Close)
	registryHost := strings.TrimPrefix(registry.URL, "http://")

	rslvr := New(Opts{
		Hosts: func(domain string) ([]docker.RegistryHost, error) {
			require.Equal(t, registryHost, domain)
			return []docker.RegistryHost{
				{
					Scheme: "http",
					Host:   registryHost,
					Path:   "/v2",
					Capabilities: docker.HostCapabilityPull |
						docker.HostCapabilityResolve,
				},
			}, nil
		},
		ContentStore: store,
		LeaseManager: newTestLeaseManager(),
	})
	t.Cleanup(func() {
		require.NoError(t, rslvr.Close())
	})

	ref := fmt.Sprintf("%s/dagger/partial:latest@%s", registryHost, image.rootDesc.Digest)
	platform := ocispecs.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	_, resolvedDigest, configBytes, err := rslvr.ResolveImageConfig(ctx, ref, ResolveImageConfigOpts{
		Platform:    &platform,
		ResolveMode: ResolveModeDefault,
	})
	require.NoError(t, err)
	require.Equal(t, image.rootDesc.Digest, resolvedDigest)
	require.JSONEq(t, string(image.configBytes), string(configBytes))
}

func TestListTags(t *testing.T) {
	ctx := context.Background()

	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v2/dagger/test/tags/list", r.URL.Path)
		require.Equal(t, "1000", r.URL.Query().Get("n"))

		switch r.URL.Query().Get("last") {
		case "":
			w.Header().Set("Link", `</v2/dagger/test/tags/list?n=1000&last=0.9.0>; rel="next"`)
			_, _ = w.Write([]byte(`{"name":"dagger/test","tags":["0.9.0"]}`))
		case "0.9.0":
			_, _ = w.Write([]byte(`{"name":"dagger/test","tags":["1.0.0"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(registry.Close)
	registryHost := strings.TrimPrefix(registry.URL, "http://")

	rslvr := New(Opts{
		Hosts: func(domain string) ([]docker.RegistryHost, error) {
			require.Equal(t, registryHost, domain)
			return []docker.RegistryHost{
				{
					Scheme:       "http",
					Host:         registryHost,
					Path:         "/v2",
					Capabilities: docker.HostCapabilityResolve,
				},
			}, nil
		},
	})
	t.Cleanup(func() {
		require.NoError(t, rslvr.Close())
	})

	tags, err := rslvr.ListTags(ctx, registryHost+"/dagger/test", ListTagsOpts{})
	require.NoError(t, err)
	require.Equal(t, []string{"0.9.0", "1.0.0"}, tags)
}

type testOCIIndexImage struct {
	rootDesc     ocispecs.Descriptor
	manifestDesc ocispecs.Descriptor
	configDesc   ocispecs.Descriptor

	indexBytes    []byte
	manifestBytes []byte
	configBytes   []byte
}

func newTestOCIIndexImage(t *testing.T) *testOCIIndexImage {
	t.Helper()

	configBytes := []byte(`{"architecture":"amd64","os":"linux","config":{}}`)
	configDesc := testDescriptor(ocispecs.MediaTypeImageConfig, configBytes)

	manifest := ocispecs.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecs.MediaTypeImageManifest,
		Config:    configDesc,
	}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestDesc := testDescriptor(ocispecs.MediaTypeImageManifest, manifestBytes)
	manifestDesc.Platform = &ocispecs.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}

	index := ocispecs.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecs.MediaTypeImageIndex,
		Manifests: []ocispecs.Descriptor{manifestDesc},
	}
	indexBytes, err := json.Marshal(index)
	require.NoError(t, err)

	return &testOCIIndexImage{
		rootDesc:      testDescriptor(ocispecs.MediaTypeImageIndex, indexBytes),
		manifestDesc:  manifestDesc,
		configDesc:    configDesc,
		indexBytes:    indexBytes,
		manifestBytes: manifestBytes,
		configBytes:   configBytes,
	}
}

func testDescriptor(mediaType string, payload []byte) ocispecs.Descriptor {
	return ocispecs.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(payload),
		Size:      int64(len(payload)),
	}
}

func newTestOCIRegistry(t *testing.T, image *testOCIIndexImage) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/v2/dagger/partial/manifests/"):
			ref := strings.TrimPrefix(r.URL.Path, "/v2/dagger/partial/manifests/")
			switch ref {
			case "latest", "latest@" + image.rootDesc.Digest.String(), image.rootDesc.Digest.String():
				serveTestRegistryDescriptor(w, r, image.rootDesc, image.indexBytes)
			case image.manifestDesc.Digest.String():
				serveTestRegistryDescriptor(w, r, image.manifestDesc, image.manifestBytes)
			default:
				http.NotFound(w, r)
			}
		case strings.HasPrefix(r.URL.Path, "/v2/dagger/partial/blobs/"):
			ref := strings.TrimPrefix(r.URL.Path, "/v2/dagger/partial/blobs/")
			switch ref {
			case image.configDesc.Digest.String():
				serveTestRegistryDescriptor(w, r, image.configDesc, image.configBytes)
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
}

func serveTestRegistryDescriptor(w http.ResponseWriter, r *http.Request, desc ocispecs.Descriptor, payload []byte) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Docker-Content-Digest", desc.Digest.String())
	w.Header().Set("Content-Type", desc.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(payload)
}

type testContentLabelStore struct {
	mu     sync.Mutex
	labels map[digest.Digest]map[string]string
}

func newTestContentLabelStore() *testContentLabelStore {
	return &testContentLabelStore{
		labels: map[digest.Digest]map[string]string{},
	}
}

func (s *testContentLabelStore) Get(dgst digest.Digest) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStringMap(s.labels[dgst]), nil
}

func (s *testContentLabelStore) Set(dgst digest.Digest, labels map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(labels) == 0 {
		delete(s.labels, dgst)
		return nil
	}
	s.labels[dgst] = cloneStringMap(labels)
	return nil
}

func (s *testContentLabelStore) Update(dgst digest.Digest, labels map[string]string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated := cloneStringMap(s.labels[dgst])
	for k, v := range labels {
		if v == "" {
			delete(updated, k)
			continue
		}
		if updated == nil {
			updated = map[string]string{}
		}
		updated[k] = v
	}
	if len(updated) == 0 {
		delete(s.labels, dgst)
		return nil, nil
	}
	s.labels[dgst] = updated
	return cloneStringMap(updated), nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type testLeaseManager struct {
	mu        sync.Mutex
	next      int
	leases    map[string]leases.Lease
	resources map[string][]leases.Resource
}

func newTestLeaseManager() *testLeaseManager {
	return &testLeaseManager{
		leases:    map[string]leases.Lease{},
		resources: map[string][]leases.Resource{},
	}
}

func (m *testLeaseManager) Create(_ context.Context, opts ...leases.Opt) (leases.Lease, error) {
	lease := leases.Lease{CreatedAt: time.Now()}
	for _, opt := range opts {
		if err := opt(&lease); err != nil {
			return leases.Lease{}, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if lease.ID == "" {
		m.next++
		lease.ID = fmt.Sprintf("test-lease-%d", m.next)
	}
	m.leases[lease.ID] = lease
	return lease, nil
}

func (m *testLeaseManager) Delete(_ context.Context, lease leases.Lease, _ ...leases.DeleteOpt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.leases, lease.ID)
	delete(m.resources, lease.ID)
	return nil
}

func (m *testLeaseManager) List(_ context.Context, _ ...string) ([]leases.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]leases.Lease, 0, len(m.leases))
	for _, lease := range m.leases {
		out = append(out, lease)
	}
	return out, nil
}

func (m *testLeaseManager) AddResource(_ context.Context, lease leases.Lease, resource leases.Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[lease.ID] = append(m.resources[lease.ID], resource)
	return nil
}

func (m *testLeaseManager) DeleteResource(_ context.Context, lease leases.Lease, resource leases.Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	resources := m.resources[lease.ID]
	for i, candidate := range resources {
		if candidate == resource {
			m.resources[lease.ID] = append(resources[:i], resources[i+1:]...)
			break
		}
	}
	return nil
}

func (m *testLeaseManager) ListResources(_ context.Context, lease leases.Lease) ([]leases.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]leases.Resource(nil), m.resources[lease.ID]...), nil
}
