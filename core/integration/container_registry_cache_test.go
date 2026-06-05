package core

import (
	"context"
	"encoding/json"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const (
	countingRegistryHost        = "counting-registry"
	countingRegistryRef         = countingRegistryHost + ":5000/dagger/manifest-cache:latest"
	countingRegistryInitialFile = "hello from fake registry\n"
	countingRegistryUpdatedFile = "hello from updated fake registry\n"
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

	runSession := func(expected string) {
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
		require.Equal(t, expected, contents)
	}

	runSession(countingRegistryInitialFile)
	runSession(countingRegistryInitialFile)

	counts := readCountingOCIRegistryCounts(ctx, t, c, registrySvc)
	t.Logf("counting OCI registry requests: %+v", counts)
	require.GreaterOrEqual(t, counts.ManifestHEADs, int64(2), "tag refs should still be revalidated once per session")
	require.Equal(t, int64(1), counts.ManifestGETs, "manifest should be downloaded once and reused from the engine content store across canonical resolution, lazy pull, and the next session")
	require.Equal(t, int64(1), counts.ConfigGETs, "config blob should be downloaded once with the manifest metadata")
	require.Equal(t, int64(1), counts.LayerGETs, "layer should be downloaded once for the initial image")

	pushCountingOCIRegistryImage(ctx, t, c, registrySvc, countingRegistryUpdatedFile)
	runSession(countingRegistryUpdatedFile)

	counts = readCountingOCIRegistryCounts(ctx, t, c, registrySvc)
	t.Logf("counting OCI registry requests after tag update: %+v", counts)
	require.GreaterOrEqual(t, counts.ManifestHEADs, int64(3), "tag refs should still be revalidated after the tag is updated")
	require.Equal(t, int64(2), counts.ManifestGETs, "updated tag digest should trigger one more manifest download")
	require.Equal(t, int64(2), counts.ConfigGETs, "updated manifest should trigger one more config blob download")
	require.Equal(t, int64(2), counts.LayerGETs, "updated image should run from the newly pushed layer")
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
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"wget", "-qO-", "http://" + countingRegistryHost + ":5000/_reset"}).
		Sync(ctx)
	require.NoError(t, err)
}

func pushCountingOCIRegistryImage(ctx context.Context, t *testctx.T, c *dagger.Client, registrySvc *dagger.Service, marker string) {
	t.Helper()
	_, err := c.Container().
		From(golangImage).
		WithServiceBinding(countingRegistryHost, registrySvc).
		WithNewFile("/src/main.go", countingOCIRegistryPusherSource).
		WithMountedCache("/tmp/go-cache", c.CacheVolume("counting-oci-registry-pusher-go-cache")).
		WithEnvVariable("GOCACHE", "/tmp/go-cache").
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"go", "run", "/src/main.go", marker}).
		Sync(ctx)
	require.NoError(t, err)
}

func readCountingOCIRegistryCounts(ctx context.Context, t *testctx.T, c *dagger.Client, registrySvc *dagger.Service) countingOCIRegistryCounts {
	t.Helper()
	out, err := c.Container().
		From(alpineImage).
		WithServiceBinding(countingRegistryHost, registrySvc).
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
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
	"io"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	manifestMediaType    = "application/vnd.oci.image.manifest.v1+json"
	configMediaType      = "application/vnd.oci.image.config.v1+json"
	layerMediaType       = "application/vnd.oci.image.layer.v1.tar+gzip"
	initialMarkerContent = "hello from fake registry\n"
)

type descriptorData struct {
	mediaType string
	payload   []byte
}

type imageData struct {
	manifest       []byte
	manifestDigest string
	config         []byte
	configDigest   string
	layer          []byte
	layerDigest    string
}

type registryServer struct {
	mu        sync.RWMutex
	manifests map[string]descriptorData
	blobs     map[string][]byte
	tags      map[string]string
	uploads   map[string][]byte

	nextUploadID atomic.Int64

	manifestGETs atomic.Int64
	manifestHEADs atomic.Int64
	configGETs   atomic.Int64
	layerGETs    atomic.Int64
}

func main() {
	srv, err := newRegistryServer()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving counting registry")
	log.Fatal(http.ListenAndServe(":5000", srv))
}

func newRegistryServer() (*registryServer, error) {
	s := &registryServer{
		manifests: map[string]descriptorData{},
		blobs:     map[string][]byte{},
		tags:      map[string]string{},
		uploads:   map[string][]byte{},
	}
	if err := s.resetImageLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *registryServer) resetImageLocked() error {
	img, err := newImageData(initialMarkerContent)
	if err != nil {
		return err
	}
	s.manifests = map[string]descriptorData{}
	s.blobs = map[string][]byte{}
	s.tags = map[string]string{}
	s.uploads = map[string][]byte{}
	s.storeImageLocked("latest", img)
	return nil
}

func (s *registryServer) storeImageLocked(tag string, img *imageData) {
	s.blobs[img.configDigest] = img.config
	s.blobs[img.layerDigest] = img.layer
	s.manifests[img.manifestDigest] = descriptorData{
		mediaType: manifestMediaType,
		payload:   img.manifest,
	}
	s.tags[tag] = img.manifestDigest
}

func newImageData(markerContents string) (*imageData, error) {
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
		s.mu.Lock()
		err := s.resetImageLocked()
		s.manifestGETs.Store(0)
		s.manifestHEADs.Store(0)
		s.configGETs.Store(0)
		s.layerGETs.Store(0)
		s.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
		w.WriteHeader(http.StatusOK)
	case strings.Contains(r.URL.Path, "/blobs/uploads"):
		s.handleBlobUpload(w, r)
	case strings.Contains(r.URL.Path, "/manifests/"):
		s.handleManifest(w, r)
	case strings.Contains(r.URL.Path, "/blobs/"):
		s.handleBlob(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *registryServer) handleManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		s.putManifest(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ref := r.URL.Path[strings.LastIndex(r.URL.Path, "/manifests/")+len("/manifests/"):]
	s.mu.RLock()
	dgst := ref
	if tagDgst, ok := s.tags[ref]; ok {
		dgst = tagDgst
	}
	manifest, ok := s.manifests[dgst]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodHead {
		s.manifestHEADs.Add(1)
	} else {
		s.manifestGETs.Add(1)
	}
	serveDescriptor(w, r, manifest.mediaType, dgst, manifest.payload)
}

func (s *registryServer) putManifest(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Path[strings.LastIndex(r.URL.Path, "/manifests/")+len("/manifests/"):]
	p, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mediaType := r.Header.Get("content-type")
	if mediaType == "" {
		mediaType = manifestMediaType
	}
	dgst := digestBytes(p)

	s.mu.Lock()
	s.manifests[dgst] = descriptorData{mediaType: mediaType, payload: p}
	if !strings.HasPrefix(ref, "sha256:") {
		s.tags[ref] = dgst
	}
	s.mu.Unlock()

	w.Header().Set("docker-content-digest", dgst)
	w.Header().Set("location", "/v2/dagger/manifest-cache/manifests/"+dgst)
	w.WriteHeader(http.StatusCreated)
}

func (s *registryServer) handleBlob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	dgst := r.URL.Path[strings.LastIndex(r.URL.Path, "/blobs/")+len("/blobs/"):]
	s.mu.RLock()
	p, ok := s.blobs[dgst]
	isConfig := s.currentConfigDigestLocked() == dgst
	isLayer := s.currentLayerDigestsLocked()[dgst]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if isConfig {
		if r.Method == http.MethodGet {
			s.configGETs.Add(1)
		}
	} else if isLayer {
		if r.Method == http.MethodGet {
			s.layerGETs.Add(1)
		}
	}
	serveDescriptor(w, r, "application/octet-stream", dgst, p)
}

func (s *registryServer) handleBlobUpload(w http.ResponseWriter, r *http.Request) {
	uploadPrefix := r.URL.Path[:strings.LastIndex(r.URL.Path, "/blobs/uploads")+len("/blobs/uploads")]
	switch r.Method {
	case http.MethodPost:
		mount := r.URL.Query().Get("mount")
		if mount != "" {
			s.mu.RLock()
			_, ok := s.blobs[mount]
			s.mu.RUnlock()
			if ok {
				w.Header().Set("location", "/v2/dagger/manifest-cache/blobs/"+mount)
				w.Header().Set("docker-content-digest", mount)
				w.WriteHeader(http.StatusCreated)
				return
			}
		}

		id := strconv.FormatInt(s.nextUploadID.Add(1), 10)
		location := uploadPrefix + "/" + id
		s.mu.Lock()
		s.uploads[id] = nil
		s.mu.Unlock()
		w.Header().Set("location", location)
		w.Header().Set("docker-upload-uuid", id)
		w.Header().Set("range", "0-0")
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPatch, http.MethodPut:
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		p, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.mu.Lock()
		upload, ok := s.uploads[id]
		if !ok {
			s.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		upload = append(upload, p...)
		if r.Method == http.MethodPatch {
			s.uploads[id] = upload
			s.mu.Unlock()
			w.Header().Set("location", r.URL.Path)
			w.Header().Set("range", fmt.Sprintf("0-%d", len(upload)))
			w.WriteHeader(http.StatusAccepted)
			return
		}

		dgst := r.URL.Query().Get("digest")
		if dgst == "" {
			s.mu.Unlock()
			http.Error(w, "missing digest", http.StatusBadRequest)
			return
		}
		if got := digestBytes(upload); got != dgst {
			s.mu.Unlock()
			http.Error(w, "digest mismatch: got "+got+", expected "+dgst, http.StatusBadRequest)
			return
		}
		s.blobs[dgst] = upload
		delete(s.uploads, id)
		s.mu.Unlock()

		w.Header().Set("location", "/v2/dagger/manifest-cache/blobs/"+dgst)
		w.Header().Set("docker-content-digest", dgst)
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *registryServer) currentConfigDigestLocked() string {
	manifest := s.currentManifestLocked()
	if len(manifest) == 0 {
		return ""
	}
	var parsed struct {
		Config struct {
			Digest string
		}
	}
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		return ""
	}
	return parsed.Config.Digest
}

func (s *registryServer) currentLayerDigestsLocked() map[string]bool {
	manifest := s.currentManifestLocked()
	if len(manifest) == 0 {
		return nil
	}
	var parsed struct {
		Layers []struct {
			Digest string
		}
	}
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		return nil
	}
	digests := map[string]bool{}
	for _, layer := range parsed.Layers {
		digests[layer.Digest] = true
	}
	return digests
}

func (s *registryServer) currentManifestLocked() []byte {
	dgst := s.tags["latest"]
	if dgst == "" {
		return nil
	}
	manifest, ok := s.manifests[dgst]
	if !ok {
		return nil
	}
	return manifest.payload
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

const countingOCIRegistryPusherSource = `
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"time"
)

const (
	registryURL       = "http://counting-registry:5000"
	repositoryPath    = "/v2/dagger/manifest-cache"
	manifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	configMediaType   = "application/vnd.oci.image.config.v1+json"
	layerMediaType    = "application/vnd.oci.image.layer.v1.tar+gzip"
)

type imageData struct {
	manifest       []byte
	manifestDigest string
	config         []byte
	configDigest   string
	layer          []byte
	layerDigest    string
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: push <marker>")
	}
	img, err := newImageData(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if err := pushBlob(img.configDigest, img.config); err != nil {
		log.Fatal(err)
	}
	if err := pushBlob(img.layerDigest, img.layer); err != nil {
		log.Fatal(err)
	}
	if err := putManifest(img.manifest); err != nil {
		log.Fatal(err)
	}
	log.Printf("pushed updated manifest %s", img.manifestDigest)
}

func newImageData(markerContents string) (*imageData, error) {
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

func pushBlob(dgst string, p []byte) error {
	resp, err := http.Post(registryURL+repositoryPath+"/blobs/uploads/", "application/octet-stream", nil)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("start upload: %s", resp.Status)
	}
	location := resp.Header.Get("location")
	if location == "" {
		return fmt.Errorf("start upload: missing location")
	}
	uploadURL, err := url.Parse(location)
	if err != nil {
		return err
	}
	if !uploadURL.IsAbs() {
		uploadURL, err = url.Parse(registryURL + location)
		if err != nil {
			return err
		}
	}
	q := uploadURL.Query()
	q.Set("digest", dgst)
	uploadURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodPut, uploadURL.String(), bytes.NewReader(p))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/octet-stream")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("finish upload: %s: %s", resp.Status, string(body))
	}
	return nil
}

func putManifest(manifest []byte) error {
	req, err := http.NewRequest(http.MethodPut, registryURL+repositoryPath+"/manifests/latest", bytes.NewReader(manifest))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", manifestMediaType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("put manifest: %s: %s", resp.Status, string(body))
	}
	return nil
}

func digestBytes(p []byte) string {
	sum := sha256.Sum256(p)
	return "sha256:" + hex.EncodeToString(sum[:])
}
`
