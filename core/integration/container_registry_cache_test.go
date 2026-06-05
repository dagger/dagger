package core

import (
	"context"
	"encoding/json"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const (
	countingRegistryHost = "counting-registry"
	countingRegistryRef  = countingRegistryHost + ":5000/dagger/manifest-cache:latest"
	countingRegistryFile = "hello from fake registry\n"
)

func (ContainerSuite) TestFromTagCachesManifestMetadataAcrossSessions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	registrySvc := countingOCIRegistryService(c)
	devEngine := devEngineContainer(c,
		func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithServiceBinding(countingRegistryHost, registrySvc)
		},
		engineWithConfig(ctx, t, func(ctx context.Context, t *testctx.T, cfg config.Config) config.Config {
			cfg.Registries = map[string]config.RegistryConfig{
				countingRegistryHost + ":5000": {PlainHTTP: ptr(true)},
			}
			return cfg
		}),
	)

	engineSvc, err := c.Host().Tunnel(devEngineContainerAsService(devEngine)).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engineSvc.Stop(ctx) })

	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	resetCountingOCIRegistry(ctx, t, c, registrySvc)

	runSession := func() {
		nestedClient, err := dagger.Connect(ctx,
			dagger.WithRunnerHost(endpoint),
			dagger.WithLogOutput(testutil.NewTWriter(t)),
		)
		require.NoError(t, err)
		defer nestedClient.Close()

		contents, err := nestedClient.Container().
			From(countingRegistryRef).
			File("/marker.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, countingRegistryFile, contents)
	}

	runSession()
	runSession()

	counts := readCountingOCIRegistryCounts(ctx, t, c, registrySvc)
	t.Logf("counting OCI registry requests: %+v", counts)
	require.GreaterOrEqual(t, counts.ManifestHEADs, int64(2), "tag refs should still be revalidated once per session")
	require.Equal(t, int64(1), counts.ManifestGETs, "manifest should be downloaded once and reused from the engine content store across canonical resolution, lazy pull, and the next session")
	require.Equal(t, int64(1), counts.ConfigGETs, "config blob should be downloaded once with the manifest metadata")
}

type countingOCIRegistryCounts struct {
	ManifestGETs  int64 `json:"manifest_gets"`
	ManifestHEADs int64 `json:"manifest_heads"`
	ConfigGETs    int64 `json:"config_gets"`
	LayerGETs     int64 `json:"layer_gets"`
}

func countingOCIRegistryService(c *dagger.Client) *dagger.Service {
	return c.Container().
		From(golangImage).
		WithNewFile("/src/main.go", countingOCIRegistrySource).
		WithMountedCache("/tmp/go-cache", c.CacheVolume("counting-oci-registry-go-cache")).
		WithEnvVariable("GOCACHE", "/tmp/go-cache").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithDefaultArgs([]string{"go", "run", "/src/main.go"}).
		AsService()
}

func resetCountingOCIRegistry(ctx context.Context, t *testctx.T, c *dagger.Client, registrySvc *dagger.Service) {
	t.Helper()
	_, err := c.Container().
		From(alpineImage).
		WithServiceBinding(countingRegistryHost, registrySvc).
		WithExec([]string{"wget", "-qO-", "http://" + countingRegistryHost + ":5000/_reset"}).
		Sync(ctx)
	require.NoError(t, err)
}

func readCountingOCIRegistryCounts(ctx context.Context, t *testctx.T, c *dagger.Client, registrySvc *dagger.Service) countingOCIRegistryCounts {
	t.Helper()
	out, err := c.Container().
		From(alpineImage).
		WithServiceBinding(countingRegistryHost, registrySvc).
		WithExec([]string{"wget", "-qO-", "http://" + countingRegistryHost + ":5000/_counts"}).
		Stdout(ctx)
	require.NoError(t, err)

	var counts countingOCIRegistryCounts
	require.NoError(t, json.Unmarshal([]byte(out), &counts))
	return counts
}

const countingOCIRegistrySource = `
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	manifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	configMediaType   = "application/vnd.oci.image.config.v1+json"
	layerMediaType    = "application/vnd.oci.image.layer.v1.tar+gzip"
	markerContents    = "hello from fake registry\n"
)

type imageData struct {
	manifest       []byte
	manifestDigest string
	config         []byte
	configDigest   string
	layer          []byte
	layerDigest    string
}

type registryServer struct {
	img           *imageData
	manifestGETs atomic.Int64
	manifestHEADs atomic.Int64
	configGETs   atomic.Int64
	layerGETs    atomic.Int64
}

func main() {
	img, err := newImageData()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving counting registry manifest %s", img.manifestDigest)
	log.Fatal(http.ListenAndServe(":5000", &registryServer{img: img}))
}

func newImageData() (*imageData, error) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{
		Name:    "marker.txt",
		Mode:    0644,
		Size:    int64(len(markerContents)),
		ModTime: time.Unix(0, 0),
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(markerContents)); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	rawLayer := tarBuf.Bytes()
	diffID := digestBytes(rawLayer)
	var gzBuf bytes.Buffer
	zw := gzip.NewWriter(&gzBuf)
	if _, err := zw.Write(rawLayer); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	layer := gzBuf.Bytes()
	layerDigest := digestBytes(layer)
	config, err := json.Marshal(map[string]any{
		"architecture": runtime.GOARCH,
		"os":           "linux",
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": []string{diffID},
		},
		"config": map[string]any{},
	})
	if err != nil {
		return nil, err
	}
	configDigest := digestBytes(config)

	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     manifestMediaType,
		"config": map[string]any{
			"mediaType": configMediaType,
			"digest":    configDigest,
			"size":      len(config),
		},
		"layers": []map[string]any{
			{
				"mediaType": layerMediaType,
				"digest":    layerDigest,
				"size":      len(layer),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return &imageData{
		manifest:       manifest,
		manifestDigest: digestBytes(manifest),
		config:         config,
		configDigest:   configDigest,
		layer:          layer,
		layerDigest:    layerDigest,
	}, nil
}

func digestBytes(p []byte) string {
	sum := sha256.Sum256(p)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s *registryServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/_counts":
		w.Header().Set("content-type", "application/json")
		fmt.Fprintf(w, "{\"manifest_gets\":%d,\"manifest_heads\":%d,\"config_gets\":%d,\"layer_gets\":%d}\n",
			s.manifestGETs.Load(),
			s.manifestHEADs.Load(),
			s.configGETs.Load(),
			s.layerGETs.Load(),
		)
	case r.URL.Path == "/_reset":
		s.manifestGETs.Store(0)
		s.manifestHEADs.Store(0)
		s.configGETs.Store(0)
		s.layerGETs.Store(0)
		_, _ = w.Write([]byte("ok\n"))
	case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
		w.WriteHeader(http.StatusOK)
	case strings.Contains(r.URL.Path, "/manifests/"):
		s.handleManifest(w, r)
	case strings.Contains(r.URL.Path, "/blobs/"):
		s.handleBlob(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *registryServer) handleManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ref := r.URL.Path[strings.LastIndex(r.URL.Path, "/manifests/")+len("/manifests/"):]
	if ref != "latest" && ref != s.img.manifestDigest {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodHead {
		s.manifestHEADs.Add(1)
	} else {
		s.manifestGETs.Add(1)
	}
	serveDescriptor(w, r, manifestMediaType, s.img.manifestDigest, s.img.manifest)
}

func (s *registryServer) handleBlob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	dgst := r.URL.Path[strings.LastIndex(r.URL.Path, "/blobs/")+len("/blobs/"):]
	switch dgst {
	case s.img.configDigest:
		if r.Method == http.MethodGet {
			s.configGETs.Add(1)
		}
		serveDescriptor(w, r, configMediaType, s.img.configDigest, s.img.config)
	case s.img.layerDigest:
		if r.Method == http.MethodGet {
			s.layerGETs.Add(1)
		}
		serveDescriptor(w, r, layerMediaType, s.img.layerDigest, s.img.layer)
	default:
		http.NotFound(w, r)
	}
}

func serveDescriptor(w http.ResponseWriter, r *http.Request, mediaType, dgst string, p []byte) {
	w.Header().Set("content-type", mediaType)
	w.Header().Set("docker-content-digest", dgst)
	w.Header().Set("etag", fmt.Sprintf("%q", dgst))
	w.Header().Set("content-length", strconv.Itoa(len(p)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(p)
	}
}
`
