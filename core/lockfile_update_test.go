package core

import (
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

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	localcontentstore "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/core/workspace"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestUpdateWorkspaceLockEntry(t *testing.T) {
	t.Parallel()

	_, err := updateWorkspaceLockEntry(context.Background(), nil, workspace.LookupEntry{
		Namespace: "acme",
		Operation: "resolve",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `unsupported lock entry "acme" "resolve"`)
}

func TestUpdateWorkspaceLockRefreshesLatestReleaseContainerEntry(t *testing.T) {
	t.Parallel()

	registry := newLockfileUpdateRegistry(t, []string{"1.0.0", "1.2.0", "1.3.0-rc.1", "latest", "edge"})
	query := newLockfileUpdateQuery(t, registry.host)

	const platform = "linux/amd64"
	inputs := []any{registry.repositoryRef(), platform, ContainerLatestReleaseLockInput, false}
	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup("", "container.from", inputs, workspace.LookupResult{
		Value:  registry.taggedRef("1.0.0"),
		Policy: workspace.PolicyPin,
	}))

	require.NoError(t, UpdateWorkspaceLock(context.Background(), query, lock))

	result, ok, err := lock.GetLookup("", "container.from", inputs)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, workspace.PolicyPin, result.Policy)
	require.Equal(t, registry.taggedRef("1.2.0"), result.Value)
}

func TestUpdateWorkspaceLockRefreshesLatestReleaseContainerEntryWithSubreleases(t *testing.T) {
	t.Parallel()

	registry := newLockfileUpdateRegistry(t, []string{"1.0.0", "1.2.0", "1.3.0-rc.1", "latest", "edge"})
	query := newLockfileUpdateQuery(t, registry.host)

	const platform = "linux/amd64"
	inputs := []any{registry.repositoryRef(), platform, ContainerLatestReleaseLockInput, true}
	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup("", "container.from", inputs, workspace.LookupResult{
		Value:  registry.taggedRef("1.0.0"),
		Policy: workspace.PolicyPin,
	}))

	require.NoError(t, UpdateWorkspaceLock(context.Background(), query, lock))

	result, ok, err := lock.GetLookup("", "container.from", inputs)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, workspace.PolicyPin, result.Policy)
	require.Equal(t, registry.taggedRef("1.3.0-rc.1"), result.Value)
}

func TestUpdateWorkspaceLockFallsBackLatestReleaseContainerEntryToLatestTag(t *testing.T) {
	t.Parallel()

	registry := newLockfileUpdateRegistry(t, []string{"latest", "edge", "3.20"})
	query := newLockfileUpdateQuery(t, registry.host)

	const platform = "linux/amd64"
	inputs := []any{registry.repositoryRef(), platform, ContainerLatestReleaseLockInput, false}
	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup("", "container.from", inputs, workspace.LookupResult{
		Value:  registry.taggedRef("edge"),
		Policy: workspace.PolicyPin,
	}))

	require.NoError(t, UpdateWorkspaceLock(context.Background(), query, lock))

	result, ok, err := lock.GetLookup("", "container.from", inputs)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, workspace.PolicyPin, result.Policy)
	require.Equal(t, registry.taggedRef("latest"), result.Value)
}

func TestUpdateWorkspaceLockKeepsContainerExactRefSemantics(t *testing.T) {
	t.Parallel()

	registry := newLockfileUpdateRegistry(t, []string{"1.0.0", "1.2.0", "latest", "edge"})
	query := newLockfileUpdateQuery(t, registry.host)

	const platform = "linux/amd64"

	t.Run("tag", func(t *testing.T) {
		t.Parallel()

		inputs := []any{registry.repositoryRef() + ":latest", platform}
		lock := workspace.NewLock()
		require.NoError(t, lock.SetLookup("", "container.from", inputs, workspace.LookupResult{
			Value:  registry.taggedRef("1.0.0"),
			Policy: workspace.PolicyPin,
		}))

		require.NoError(t, UpdateWorkspaceLock(context.Background(), query, lock))

		result, ok, err := lock.GetLookup("", "container.from", inputs)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, registry.taggedRef("latest"), result.Value)
	})

	t.Run("digest", func(t *testing.T) {
		t.Parallel()

		inputs := []any{registry.digestRef("1.0.0"), platform}
		lock := workspace.NewLock()
		require.NoError(t, lock.SetLookup("", "container.from", inputs, workspace.LookupResult{
			Value:  registry.taggedRef("latest"),
			Policy: workspace.PolicyPin,
		}))

		require.NoError(t, UpdateWorkspaceLock(context.Background(), query, lock))

		result, ok, err := lock.GetLookup("", "container.from", inputs)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, registry.digestRef("1.0.0"), result.Value)
	})
}

func TestUpdateWorkspaceLockRejectsMalformedLatestReleaseContainerEntry(t *testing.T) {
	t.Parallel()

	registry := newLockfileUpdateRegistry(t, []string{"1.0.0", "latest"})
	query := newLockfileUpdateQuery(t, registry.host)

	const platform = "linux/amd64"
	tests := []struct {
		name    string
		inputs  []any
		wantErr string
	}{
		{
			name:    "missing includeSubreleases input",
			inputs:  []any{registry.repositoryRef(), platform, ContainerLatestReleaseLockInput},
			wantErr: "missing container.from latestIncludeSubreleases input",
		},
		{
			name:    "non-bool includeSubreleases input",
			inputs:  []any{registry.repositoryRef(), platform, ContainerLatestReleaseLockInput, "false"},
			wantErr: "invalid container.from latestIncludeSubreleases",
		},
		{
			name:    "tagged address marked latest release",
			inputs:  []any{registry.repositoryRef() + ":1.0.0", platform, ContainerLatestReleaseLockInput, false},
			wantErr: "must not include a tag",
		},
		{
			name:    "digest address marked latest release",
			inputs:  []any{registry.digestRef("1.0.0"), platform, ContainerLatestReleaseLockInput, false},
			wantErr: "must not include a digest",
		},
		{
			name:    "invalid transport input after latest-release marker",
			inputs:  []any{registry.repositoryRef(), platform, ContainerLatestReleaseLockInput, false, "ftp"},
			wantErr: `invalid container.from transport input "ftp"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := updateWorkspaceLockEntry(context.Background(), query, workspace.LookupEntry{
				Operation: "container.from",
				Inputs:    tt.inputs,
				Result: workspace.LookupResult{
					Value:  registry.taggedRef("1.0.0"),
					Policy: workspace.PolicyPin,
				},
			})
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestParseGitLatestLockPin(t *testing.T) {
	t.Parallel()

	t.Run("accepts release tag", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseGitLatestLockPin("refs/tags/v1.2.3@0123456789abcdef0123456789abcdef01234567", false)
		require.NoError(t, err)
		require.Equal(t, "refs/tags/v1.2.3", ref.Name)
		require.Equal(t, "0123456789abcdef0123456789abcdef01234567", ref.SHA)
	})

	t.Run("accepts head fallback", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/heads/main@0123456789abcdef0123456789abcdef01234567", false)
		require.NoError(t, err)

		_, err = ParseGitLatestLockPin("HEAD@0123456789abcdef0123456789abcdef01234567", false)
		require.NoError(t, err)
	})

	t.Run("rejects non-release tag", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/tags/latest@0123456789abcdef0123456789abcdef01234567", false)
		require.ErrorContains(t, err, "not a release tag")
	})

	t.Run("honors prerelease option", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/tags/v1.2.3-rc.1@0123456789abcdef0123456789abcdef01234567", false)
		require.ErrorContains(t, err, "not a release tag")

		_, err = ParseGitLatestLockPin("refs/tags/v1.2.3-rc.1@0123456789abcdef0123456789abcdef01234567", true)
		require.NoError(t, err)
	})

	t.Run("rejects unrelated refs", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/pull/1/head@0123456789abcdef0123456789abcdef01234567", false)
		require.ErrorContains(t, err, "must be a release tag or HEAD branch")
	})

	t.Run("rejects invalid pin", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("refs/tags/v1.2.3@not-a-sha", false)
		require.ErrorContains(t, err, "invalid commit sha")
	})

	t.Run("rejects commit-only pin", func(t *testing.T) {
		t.Parallel()

		_, err := ParseGitLatestLockPin("0123456789abcdef0123456789abcdef01234567", false)
		require.ErrorContains(t, err, "invalid git ref lock pin")
	})
}

type lockfileUpdateTestServer struct {
	*mockServer
	resolver *serverresolver.Resolver
}

func (s *lockfileUpdateTestServer) RegistryResolver(context.Context) (*serverresolver.Resolver, error) {
	return s.resolver, nil
}

func newLockfileUpdateQuery(t *testing.T, registryHost string) *Query {
	t.Helper()

	store, err := localcontentstore.NewLabeledStore(t.TempDir(), newLockfileUpdateContentLabelStore())
	require.NoError(t, err)

	rslvr := serverresolver.New(serverresolver.Opts{
		Hosts: func(domain string) ([]docker.RegistryHost, error) {
			if domain != registryHost {
				return nil, fmt.Errorf("unexpected registry host %q, want %q", domain, registryHost)
			}
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
		LeaseManager: newLockfileUpdateLeaseManager(),
	})
	t.Cleanup(func() {
		require.NoError(t, rslvr.Close())
	})

	return NewRoot(&lockfileUpdateTestServer{
		mockServer: &mockServer{},
		resolver:   rslvr,
	})
}

type lockfileUpdateRegistry struct {
	server *httptest.Server
	host   string
	tags   []string
	images map[string]lockfileUpdateRegistryImage
}

type lockfileUpdateRegistryImage struct {
	manifestDesc  ocispecs.Descriptor
	configDesc    ocispecs.Descriptor
	manifestBytes []byte
	configBytes   []byte
}

func newLockfileUpdateRegistry(t *testing.T, tags []string) *lockfileUpdateRegistry {
	t.Helper()

	registry := &lockfileUpdateRegistry{
		tags:   tags,
		images: map[string]lockfileUpdateRegistryImage{},
	}
	for _, tag := range tags {
		registry.images[tag] = newLockfileUpdateRegistryImage(t, tag)
	}

	registry.server = httptest.NewServer(http.HandlerFunc(registry.serveHTTP))
	t.Cleanup(registry.server.Close)
	registry.host = strings.TrimPrefix(registry.server.URL, "http://")
	return registry
}

func newLockfileUpdateRegistryImage(t *testing.T, tag string) lockfileUpdateRegistryImage {
	t.Helper()

	configBytes := []byte(fmt.Sprintf(`{"architecture":"amd64","os":"linux","config":{"Labels":{"tag":%q}}}`, tag))
	configDesc := lockfileUpdateDescriptor(ocispecs.MediaTypeImageConfig, configBytes)

	manifest := ocispecs.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecs.MediaTypeImageManifest,
		Config:    configDesc,
	}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestDesc := lockfileUpdateDescriptor(ocispecs.MediaTypeImageManifest, manifestBytes)

	return lockfileUpdateRegistryImage{
		manifestDesc:  manifestDesc,
		configDesc:    configDesc,
		manifestBytes: manifestBytes,
		configBytes:   configBytes,
	}
}

func lockfileUpdateDescriptor(mediaType string, payload []byte) ocispecs.Descriptor {
	return ocispecs.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(payload),
		Size:      int64(len(payload)),
	}
}

func (r *lockfileUpdateRegistry) repositoryRef() string {
	return r.host + "/dagger/test"
}

func (r *lockfileUpdateRegistry) taggedRef(tag string) string {
	image := r.images[tag]
	return fmt.Sprintf("%s:%s@%s", r.repositoryRef(), tag, image.manifestDesc.Digest)
}

func (r *lockfileUpdateRegistry) digestRef(tag string) string {
	image := r.images[tag]
	return fmt.Sprintf("%s@%s", r.repositoryRef(), image.manifestDesc.Digest)
}

func (r *lockfileUpdateRegistry) serveHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.URL.Path == "/v2/":
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
	case req.URL.Path == "/v2/dagger/test/tags/list":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "dagger/test",
			"tags": r.tags,
		})
	case strings.HasPrefix(req.URL.Path, "/v2/dagger/test/manifests/"):
		ref := strings.TrimPrefix(req.URL.Path, "/v2/dagger/test/manifests/")
		if image, ok := r.imageByManifestRef(ref); ok {
			serveLockfileUpdateRegistryDescriptor(w, req, image.manifestDesc, image.manifestBytes)
			return
		}
		http.NotFound(w, req)
	case strings.HasPrefix(req.URL.Path, "/v2/dagger/test/blobs/"):
		ref := strings.TrimPrefix(req.URL.Path, "/v2/dagger/test/blobs/")
		if image, ok := r.imageByConfigDigest(ref); ok {
			serveLockfileUpdateRegistryDescriptor(w, req, image.configDesc, image.configBytes)
			return
		}
		http.NotFound(w, req)
	default:
		http.NotFound(w, req)
	}
}

func (r *lockfileUpdateRegistry) imageByManifestRef(ref string) (lockfileUpdateRegistryImage, bool) {
	if image, ok := r.images[ref]; ok {
		return image, true
	}
	for _, image := range r.images {
		if ref == image.manifestDesc.Digest.String() {
			return image, true
		}
	}
	return lockfileUpdateRegistryImage{}, false
}

func (r *lockfileUpdateRegistry) imageByConfigDigest(ref string) (lockfileUpdateRegistryImage, bool) {
	for _, image := range r.images {
		if ref == image.configDesc.Digest.String() {
			return image, true
		}
	}
	return lockfileUpdateRegistryImage{}, false
}

func serveLockfileUpdateRegistryDescriptor(w http.ResponseWriter, req *http.Request, desc ocispecs.Descriptor, payload []byte) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Docker-Content-Digest", desc.Digest.String())
	w.Header().Set("Content-Type", desc.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.WriteHeader(http.StatusOK)
	if req.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(payload)
}

type lockfileUpdateContentLabelStore struct {
	mu     sync.Mutex
	labels map[digest.Digest]map[string]string
}

func newLockfileUpdateContentLabelStore() *lockfileUpdateContentLabelStore {
	return &lockfileUpdateContentLabelStore{
		labels: map[digest.Digest]map[string]string{},
	}
}

func (s *lockfileUpdateContentLabelStore) Get(dgst digest.Digest) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneLockfileUpdateLabels(s.labels[dgst]), nil
}

func (s *lockfileUpdateContentLabelStore) Set(dgst digest.Digest, labels map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(labels) == 0 {
		delete(s.labels, dgst)
		return nil
	}
	s.labels[dgst] = cloneLockfileUpdateLabels(labels)
	return nil
}

func (s *lockfileUpdateContentLabelStore) Update(dgst digest.Digest, labels map[string]string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated := cloneLockfileUpdateLabels(s.labels[dgst])
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
	return cloneLockfileUpdateLabels(updated), nil
}

func cloneLockfileUpdateLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type lockfileUpdateLeaseManager struct {
	mu        sync.Mutex
	next      int
	leases    map[string]leases.Lease
	resources map[string][]leases.Resource
}

func newLockfileUpdateLeaseManager() *lockfileUpdateLeaseManager {
	return &lockfileUpdateLeaseManager{
		leases:    map[string]leases.Lease{},
		resources: map[string][]leases.Resource{},
	}
}

func (m *lockfileUpdateLeaseManager) Create(_ context.Context, opts ...leases.Opt) (leases.Lease, error) {
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

func (m *lockfileUpdateLeaseManager) Delete(_ context.Context, lease leases.Lease, _ ...leases.DeleteOpt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.leases, lease.ID)
	delete(m.resources, lease.ID)
	return nil
}

func (m *lockfileUpdateLeaseManager) List(_ context.Context, _ ...string) ([]leases.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]leases.Lease, 0, len(m.leases))
	for _, lease := range m.leases {
		out = append(out, lease)
	}
	return out, nil
}

func (m *lockfileUpdateLeaseManager) AddResource(_ context.Context, lease leases.Lease, resource leases.Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[lease.ID] = append(m.resources[lease.ID], resource)
	return nil
}

func (m *lockfileUpdateLeaseManager) DeleteResource(_ context.Context, lease leases.Lease, resource leases.Resource) error {
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

func (m *lockfileUpdateLeaseManager) ListResources(_ context.Context, lease leases.Lease) ([]leases.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]leases.Resource(nil), m.resources[lease.ID]...), nil
}
