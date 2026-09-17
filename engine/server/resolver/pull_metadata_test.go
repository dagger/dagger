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
	for _, indexed := range []bool{false, true} {
		for _, mode := range []string{
			"reuse", "force", "tag", "service-bound",
			"missing-root", "missing-manifest", "missing-config",
			"wrong-root-source", "wrong-manifest-source", "wrong-config-source",
		} {
			t.Run(fmt.Sprintf("index=%t/%s", indexed, mode), func(t *testing.T) {
				ctx := t.Context()
				platform := ocispecs.Platform{OS: "linux", Architecture: "amd64"}
				// A missing layer ensures Pull cannot take the existing complete-image
				// cache path. These bytes exercise transfer verification, not unpacking.
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
				root, rootBytes := manifest, manifestBytes
				if indexed {
					child := manifest
					child.Platform = &platform
					rootBytes, err = json.Marshal(ocispecs.Index{
						Versioned: specs.Versioned{SchemaVersion: 2},
						MediaType: ocispecs.MediaTypeImageIndex,
						Manifests: []ocispecs.Descriptor{child},
					})
					require.NoError(t, err)
					root = testDescriptor(ocispecs.MediaTypeImageIndex, rootBytes)
				}

				var manifestHEADs, layerGETs atomic.Int32
				registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.URL.Path == "/v2/":
						w.WriteHeader(http.StatusOK)
					case strings.HasPrefix(r.URL.Path, "/v2/test/image/manifests/"):
						if r.Method == http.MethodHead {
							manifestHEADs.Add(1)
						}
						switch strings.TrimPrefix(r.URL.Path, "/v2/test/image/manifests/") {
						case "latest", root.Digest.String():
							serveTestRegistryDescriptor(w, r, root, rootBytes)
						case manifest.Digest.String():
							serveTestRegistryDescriptor(w, r, manifest, manifestBytes)
						default:
							http.NotFound(w, r)
						}
					case r.URL.Path == "/v2/test/image/blobs/"+config.Digest.String():
						serveTestRegistryDescriptor(w, r, config, configBytes)
					case r.URL.Path == "/v2/test/image/blobs/"+layer.Digest.String():
						if r.Method == http.MethodGet {
							layerGETs.Add(1)
						}
						serveTestRegistryDescriptor(w, r, layer, layerBytes)
					default:
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(registry.Close)
				host := strings.TrimPrefix(registry.URL, "http://")
				store, err := localcontentstore.NewLabeledStore(t.TempDir(), newTestContentLabelStore())
				require.NoError(t, err)
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
				ref := fmt.Sprintf("%s/test/image:latest@%s", host, root.Digest)
				_, _, _, err = rslvr.ResolveImageConfig(ctx, ref, ResolveImageConfigOpts{Platform: &platform})
				require.NoError(t, err)
				_, err = store.Info(ctx, layer.Digest)
				require.True(t, cerrdefs.IsNotFound(err))
				require.Zero(t, layerGETs.Load())
				before := manifestHEADs.Load()
				require.Positive(t, before)

				opts := PullOpts{Platform: platform}
				switch mode {
				case "force":
					opts.ResolveMode = ResolveModeForcePull
				case "tag":
					ref = strings.Split(ref, "@")[0]
				case "service-bound":
					opts.Network.HostAliases = map[string]string{"unrelated-service": "127.0.0.1"}
				case "missing-root", "missing-manifest", "missing-config":
					desc := map[string]ocispecs.Descriptor{
						"missing-root": root, "missing-manifest": manifest, "missing-config": config,
					}[mode]
					require.NoError(t, store.Delete(ctx, desc.Digest))
				case "wrong-root-source", "wrong-manifest-source", "wrong-config-source":
					desc := map[string]ocispecs.Descriptor{
						"wrong-root-source": root, "wrong-manifest-source": manifest, "wrong-config-source": config,
					}[mode]
					_, err := store.Update(ctx, content.Info{
						Digest: desc.Digest,
						Labels: map[string]string{"containerd.io/distribution.source." + host: "another/repository"},
					}, "labels")
					require.NoError(t, err)
				}
				pulled, err := rslvr.Pull(ctx, ref, opts)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, pulled.Release(context.WithoutCancel(ctx))) })
				if mode == "reuse" {
					require.Equal(t, before, manifestHEADs.Load(), "metadata reuse must avoid the second HEAD")
				} else {
					require.Greater(t, manifestHEADs.Load(), before, "ineligible metadata must use remote resolution")
				}
				require.Len(t, pulled.Layers, 1)
				require.Equal(t, layer.Digest, pulled.Layers[0].Digest)
				require.Equal(t, int32(1), layerGETs.Load())
				actual, err := content.ReadBlob(ctx, store, layer)
				require.NoError(t, err)
				require.Equal(t, layerBytes, actual)
			})
		}
	}
}
