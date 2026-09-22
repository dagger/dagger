package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	localcontentstore "github.com/containerd/containerd/v2/plugins/content/local"
	cerrdefs "github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestPullReusesPinnedMetadataOnlyWhenEligible(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, s *pullScenario)
		reuse  bool
	}{
		{name: "reuse", reuse: true},
		{name: "force", mutate: func(_ *testing.T, s *pullScenario) { s.opts.ResolveMode = ResolveModeForcePull }},
		{name: "tag", mutate: func(_ *testing.T, s *pullScenario) { s.ref = s.taggedRef }},
		{name: "service-bound", mutate: func(_ *testing.T, s *pullScenario) {
			s.opts.Network.HostAliases = map[string]string{"unrelated-service": "127.0.0.1"}
		}},
		{name: "missing-root", mutate: func(t *testing.T, s *pullScenario) { s.deleteBlob(t, s.image.root) }},
		{name: "missing-manifest", mutate: func(t *testing.T, s *pullScenario) { s.deleteBlob(t, s.image.manifest) }},
		{name: "missing-config", mutate: func(t *testing.T, s *pullScenario) { s.deleteBlob(t, s.image.config) }},
		{name: "wrong-root-source", mutate: func(t *testing.T, s *pullScenario) { s.relabelSource(t, s.image.root) }},
		{name: "wrong-manifest-source", mutate: func(t *testing.T, s *pullScenario) { s.relabelSource(t, s.image.manifest) }},
		{name: "wrong-config-source", mutate: func(t *testing.T, s *pullScenario) { s.relabelSource(t, s.image.config) }},
	}
	for _, indexed := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("index=%t/%s", indexed, tc.name), func(t *testing.T) {
				ctx := t.Context()
				s := seedPinnedMetadata(t, indexed)
				before := s.registry.manifestHEADs.Load()
				if tc.mutate != nil {
					tc.mutate(t, s)
				}

				pulled, err := s.resolver.Pull(ctx, s.ref, s.opts)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, pulled.Release(context.WithoutCancel(ctx))) })

				if tc.reuse {
					require.Equal(t, before, s.registry.manifestHEADs.Load(), "metadata reuse must avoid the second HEAD")
				} else {
					require.Greater(t, s.registry.manifestHEADs.Load(), before, "ineligible metadata must use remote resolution")
				}
				s.requireLayerDownloaded(t, pulled)
			})
		}
	}
}

// pullScenario is a registry, a content store and a resolver where
// ResolveImageConfig has already run for a pinned ref, so the image's
// metadata is local but its layer is not. Cases mutate it before Pull.
type pullScenario struct {
	image     *testPullImage
	registry  *testPullRegistry
	store     content.Store
	resolver  *Resolver
	ref       string // pinned: host/test/image:latest@sha256:...
	taggedRef string // host/test/image:latest
	opts      PullOpts
}

func seedPinnedMetadata(t *testing.T, indexed bool) *pullScenario {
	t.Helper()
	ctx := t.Context()
	platform := ocispecs.Platform{OS: "linux", Architecture: "amd64"}

	image := newTestPullImage(t, indexed, platform)
	registry := newTestPullRegistry(t, image)
	store, err := localcontentstore.NewLabeledStore(t.TempDir(), newTestContentLabelStore())
	require.NoError(t, err)
	s := &pullScenario{
		image:     image,
		registry:  registry,
		store:     store,
		resolver:  newTestResolver(t, registry.host, store),
		taggedRef: registry.host + "/test/image:latest",
		opts:      PullOpts{Platform: platform},
	}
	s.ref = s.taggedRef + "@" + image.root.Digest.String()

	resolvedRef, resolvedDigest, configBytes, err := s.resolver.ResolveImageConfig(ctx, s.ref, ResolveImageConfigOpts{Platform: &platform})
	require.NoError(t, err)
	require.Equal(t, s.ref, resolvedRef)
	require.Equal(t, image.root.Digest, resolvedDigest)
	require.JSONEq(t, string(image.configBytes), string(configBytes))

	// Metadata is local, the layer is not, and resolving cost at least one HEAD.
	_, err = store.Info(ctx, image.layer.Digest)
	require.True(t, cerrdefs.IsNotFound(err))
	require.Zero(t, registry.layerGETs.Load())
	require.Positive(t, registry.manifestHEADs.Load())
	return s
}

func (s *pullScenario) deleteBlob(t *testing.T, desc ocispecs.Descriptor) {
	t.Helper()
	require.NoError(t, s.store.Delete(t.Context(), desc.Digest))
}

// relabelSource marks the blob as coming from another repository on the same
// registry, so its source no longer matches the ref being pulled.
func (s *pullScenario) relabelSource(t *testing.T, desc ocispecs.Descriptor) {
	t.Helper()
	_, err := s.store.Update(t.Context(), content.Info{
		Digest: desc.Digest,
		Labels: map[string]string{"containerd.io/distribution.source." + s.registry.host: "another/repository"},
	}, "labels")
	require.NoError(t, err)
}

func (s *pullScenario) requireLayerDownloaded(t *testing.T, pulled *PulledImage) {
	t.Helper()
	require.Len(t, pulled.Layers, 1)
	require.Equal(t, s.image.layer.Digest, pulled.Layers[0].Digest)
	require.Equal(t, int32(1), s.registry.layerGETs.Load())
	actual, err := content.ReadBlob(t.Context(), s.store, s.image.layer)
	require.NoError(t, err)
	require.Equal(t, s.image.layerBytes, actual)
}

// testPullImage is a single-platform image with one layer. Its root is either
// the manifest itself or an index pointing at it.
type testPullImage struct {
	root, manifest, config, layer                     ocispecs.Descriptor
	rootBytes, manifestBytes, configBytes, layerBytes []byte
}

func newTestPullImage(t *testing.T, indexed bool, platform ocispecs.Platform) *testPullImage {
	t.Helper()

	// The layer never gets unpacked; its bytes only have to survive transfer verification.
	layerBytes := []byte("layer content for metadata reuse testing")
	layer := testDescriptor(ocispecs.MediaTypeImageLayer, layerBytes)

	configBytes, err := json.Marshal(ocispecs.Image{
		Platform: platform,
		RootFS:   ocispecs.RootFS{Type: "layers", DiffIDs: []digest.Digest{layer.Digest}},
	})
	require.NoError(t, err)
	config := testDescriptor(ocispecs.MediaTypeImageConfig, configBytes)

	manifestBytes, err := json.Marshal(ocispecs.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecs.MediaTypeImageManifest,
		Config:    config,
		Layers:    []ocispecs.Descriptor{layer},
	})
	require.NoError(t, err)
	manifest := testDescriptor(ocispecs.MediaTypeImageManifest, manifestBytes)

	image := &testPullImage{
		root: manifest, manifest: manifest, config: config, layer: layer,
		rootBytes: manifestBytes, manifestBytes: manifestBytes, configBytes: configBytes, layerBytes: layerBytes,
	}
	if indexed {
		child := manifest
		child.Platform = &platform
		image.rootBytes, err = json.Marshal(ocispecs.Index{
			Versioned: specs.Versioned{SchemaVersion: 2},
			MediaType: ocispecs.MediaTypeImageIndex,
			Manifests: []ocispecs.Descriptor{child},
		})
		require.NoError(t, err)
		image.root = testDescriptor(ocispecs.MediaTypeImageIndex, image.rootBytes)
	}
	return image
}

// testPullRegistry serves one image at test/image and counts the requests
// the assertions care about.
type testPullRegistry struct {
	host          string
	manifestHEADs atomic.Int32
	layerGETs     atomic.Int32
}

func newTestPullRegistry(t *testing.T, image *testPullImage) *testPullRegistry {
	t.Helper()
	registry := &testPullRegistry{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/v2/test/image/manifests/"):
			if r.Method == http.MethodHead {
				registry.manifestHEADs.Add(1)
			}
			switch strings.TrimPrefix(r.URL.Path, "/v2/test/image/manifests/") {
			case "latest", image.root.Digest.String():
				serveTestRegistryDescriptor(w, r, image.root, image.rootBytes)
			case image.manifest.Digest.String():
				serveTestRegistryDescriptor(w, r, image.manifest, image.manifestBytes)
			default:
				http.NotFound(w, r)
			}
		case r.URL.Path == "/v2/test/image/blobs/"+image.config.Digest.String():
			serveTestRegistryDescriptor(w, r, image.config, image.configBytes)
		case r.URL.Path == "/v2/test/image/blobs/"+image.layer.Digest.String():
			if r.Method == http.MethodGet {
				registry.layerGETs.Add(1)
			}
			serveTestRegistryDescriptor(w, r, image.layer, image.layerBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	registry.host = strings.TrimPrefix(server.URL, "http://")
	return registry
}

func newTestResolver(t *testing.T, host string, store content.Store) *Resolver {
	t.Helper()
	rslvr := New(Opts{
		Hosts: func(domain string) ([]docker.RegistryHost, error) {
			if domain != host {
				return nil, fmt.Errorf("unexpected registry: %s", domain)
			}
			return []docker.RegistryHost{{
				Scheme:       "http",
				Host:         domain,
				Path:         "/v2",
				Capabilities: docker.HostCapabilityPull | docker.HostCapabilityResolve,
			}}, nil
		},
		ContentStore: store,
		LeaseManager: newTestLeaseManager(),
	})
	t.Cleanup(func() { require.NoError(t, rslvr.Close()) })
	return rslvr
}
