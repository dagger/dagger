package cache

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	remotecache "github.com/moby/buildkit/cache/remotecache/v1"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

/*
The process on export is as follows:
  - Engine gathers metadata for current state of its local cache and sends it to the cache service
    via UpdateCacheRecords
  - The cache service responds with a list of cache refs that should be exported, if any
  - The engine compresses those them into layers, pushes them and then updates the cache service on
    what the digests of the layers ended up being via UpdateCacheLayers

The process on import is as follows:
  - The engine asks for a cache config from the cache service via ImportCache. This cache config
    is the same format used by buildkit to create cache managers from remote caches.
  - The cache service responds with that cache config
  - The engine creates a cache manager from the cache config and plugs it into the combined cache
    manager with the actual local cache

For cache mounts, the process is:
  - At engine startup, GetCacheMountConfig is called and any cache mounts returned are synced locally
    to the corresponding cache mount. This happens before any clients can connect to ensure consistency.
    The cache mount is a compressed tarball of the cache mount contents.
  - At engine shutdown, those cache mounts are synced back to the cache service. GetCacheMountUploadURL
    is called to get a URL to upload to, which may or may not be the same as the original download URL.
*/
type Service interface {
	// GetConfig returns configuration needed for the engine to push layer blobs
	GetConfig(context.Context, GetConfigRequest) (*Config, error)

	// UpdateCacheRecords informs the cache service of the current state of the cache metadata.
	// It returns a list of cache refs that should be prepared for export and pushed.
	UpdateCacheRecords(context.Context, UpdateCacheRecordsRequest) (*UpdateCacheRecordsResponse, error)

	// UpdateCacheLayers tells the cache service that layers for the given records have been
	// uploaded with the given digests.
	UpdateCacheLayers(context.Context, UpdateCacheLayersRequest) error

	// ImportCache returns a cache config that the engine can turn into cache manager.
	ImportCache(context.Context) (*remotecache.CacheConfig, error)

	// GetLayerDownloadURL returns a URL that the engine can use to download the layer blob. The URL
	// is only valid for a limited time so this API should only be called right as the layer is needed.
	GetLayerDownloadURL(context.Context, GetLayerDownloadURLRequest) (*GetLayerDownloadURLResponse, error)

	// GetLayerUploadURL returns a URL that the engine can use to upload the layer blob. The URL is only
	// valid for a limited time so this API should only be called right as the layer is to be uploaded.
	GetLayerUploadURL(context.Context, GetLayerUploadURLRequest) (*GetLayerUploadURLResponse, error)

	// GetCacheMountConfig returns a list of cache mounts that the engine should sync locally. It contains
	// metadata like digest+size plus a time-limited URL that the engine can use to download the cache mounts.
	GetCacheMountConfig(context.Context, GetCacheMountConfigRequest) (*GetCacheMountConfigResponse, error)

	// GetCacheMountUploadURL returns a URL that the engine can use to upload the cache mount blob. The URL is only
	// valid for a limited time so this API should only be called right as the cache mount is to be uploaded.
	GetCacheMountUploadURL(context.Context, GetCacheMountUploadURLRequest) (*GetCacheMountUploadURLResponse, error)
}

type GetConfigRequest struct {
	EngineID string
}

func (r GetConfigRequest) String() string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

type Config struct {
	// ImportPeriod is the frequency to attempt imports from the cache service.
	ImportPeriod time.Duration
	// ExportPeriod is the frequency to attempt exporting cache to the cache service.
	ExportPeriod time.Duration

	// ExportTimeout is the maximum duration to allow exports to last (after
	// this, they are cancelled)
	ExportTimeout time.Duration

	// TODO: reload config period
}

func (c Config) String() string {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

// UpdateCacheRecordsRequest is essentially similar to the internal
// representation for a cachechains response. A few key differences:
//   - No reliance on array-indexed values. Instead we use CacheKeyID pairs in
//     Links to join them together.
//   - No normalization. The links are just the links between records, there
//     may be loops, duplicates, etc.
type UpdateCacheRecordsRequest struct {
	CacheKeys []CacheKey
	Links     []Link
}

func (r UpdateCacheRecordsRequest) String() string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

// CacheKeyID is the ID of a cache record.
//
// We use these to connect records together, instead of using the array-indexed
// records format that buildkit uses.
type CacheKeyID string

// CacheKey is the part of a CacheRecord that contains the results.
//
// TODO: rename this at some point - a cache key should just be the
// ID/digest/etc, and shouldn't have associated results.
type CacheKey struct {
	// ID is the unique identifier for a record.
	ID CacheKeyID
	// Results are all the results that are connected to this record.
	Results []Result
}

// Link is connects two CacheKeys together.
//
// It's similar to upstream's CacheInfoLink. And CacheChain's link struct.
type Link struct {
	// ID is the unique identifier for a record.
	ID CacheKeyID
	// LinkedID is the unique identifier for a parent dependency record.
	LinkedID CacheKeyID

	// Digest is the actual cachekey - this *roughly* corresponds to a cache
	// result for the input edge. This is what buildkit computes and matches
	// against to determine a cache match.
	Digest digest.Digest

	// Input is the input-index for the vertex pointed at by this record.
	Input int
	// Selector is the selector for this link (it helps nake this link unique).
	Selector digest.Digest
}

// CacheResultID is the worker ID of a cache result.
type CacheResultID string

type Result struct {
	// ID is the cache result ID
	ID CacheResultID

	CreatedAt   time.Time
	Description string
}

type UpdateCacheRecordsResponse struct {
	// ExportRecords are records that the engine should prepare layers for and push
	ExportRecords []ExportRecord
}

func (r UpdateCacheRecordsResponse) String() string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

type ExportRecord struct {
	// Digest is the target digest to associate a layer with
	Digest digest.Digest

	// CacheRefID corresponds to Result.ID
	CacheRefID CacheResultID
}

type UpdateCacheLayersRequest struct {
	UpdatedRecords []RecordLayers
}

func (r UpdateCacheLayersRequest) String() string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

type RecordLayers struct {
	// RecordDigest corresponds to ExportRecord.Digest
	RecordDigest digest.Digest
	// Layers are the layers that are part of this record's result
	Layers []ocispecs.Descriptor
}

type GetLayerDownloadURLRequest struct {
	Digest digest.Digest
}

type GetLayerDownloadURLResponse struct {
	URL string
}

type GetLayerUploadURLRequest struct {
	Digest digest.Digest
}

type GetLayerUploadURLResponse struct {
	URL     string
	Headers map[string]string
}

type GetCacheMountConfigRequest struct{}

type GetCacheMountConfigResponse struct {
	SyncedCacheMounts []SyncedCacheMountConfig
}

type SyncedCacheMountConfig struct {
	Name      string
	Digest    digest.Digest
	Size      int64
	MediaType string
	URL       string
}

type GetCacheMountUploadURLRequest struct {
	CacheName string
	Digest    digest.Digest
	Size      int64
}

type GetCacheMountUploadURLResponse struct {
	URL     string
	Headers map[string]string
}

type client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

var _ Service = &client{}

func newClient(urlString, token string) (Service, error) {
	c := &client{}

	u, err := url.Parse(urlString)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "tcp":
		c.baseURL = "http://" + u.Host
		c.httpClient = &http.Client{}
	case "unix":
		c.baseURL = "http://local"
		c.httpClient = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", u.Path)
				},
			},
		}
	default:
		c.baseURL = urlString
		c.httpClient = &http.Client{}
	}

	c.token = token
	return c, nil
}

//nolint:dupl
func (c *client) GetConfig(ctx context.Context, req GetConfigRequest) (*Config, error) {
	bodyR, bodyW := io.Pipe()
	encoder := json.NewEncoder(bodyW)
	go func() {
		defer bodyW.Close()
		if err := encoder.Encode(req); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to encode request")
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/config", bodyR)
	if err != nil {
		return nil, err
	}
	if len(c.token) > 0 {
		httpReq.SetBasicAuth(c.token, "")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if err := checkResponse(httpResp); err != nil {
		return nil, err
	}

	config := &Config{}
	if err := json.NewDecoder(httpResp.Body).Decode(config); err != nil {
		return nil, err
	}
	return config, nil
}

//nolint:dupl
func (c *client) UpdateCacheRecords(
	ctx context.Context,
	req UpdateCacheRecordsRequest,
) (*UpdateCacheRecordsResponse, error) {
	bodyR, bodyW := io.Pipe()
	encoder := json.NewEncoder(bodyW)
	go func() {
		defer bodyW.Close()
		if err := encoder.Encode(req); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to encode request")
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/records", bodyR)
	if err != nil {
		return nil, err
	}
	if len(c.token) > 0 {
		httpReq.SetBasicAuth(c.token, "")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if err := checkResponse(httpResp); err != nil {
		return nil, err
	}

	resp := &UpdateCacheRecordsResponse{}
	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

//nolint:dupl
func (c *client) UpdateCacheLayers(
	ctx context.Context,
	req UpdateCacheLayersRequest,
) error {
	bodyR, bodyW := io.Pipe()
	encoder := json.NewEncoder(bodyW)
	go func() {
		defer bodyW.Close()
		if err := encoder.Encode(req); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to encode request")
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/layers", bodyR)
	if err != nil {
		return err
	}
	if len(c.token) > 0 {
		httpReq.SetBasicAuth(c.token, "")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if err := checkResponse(httpResp); err != nil {
		return err
	}

	return nil
}

//nolint:dupl
func (c *client) ImportCache(ctx context.Context) (*remotecache.CacheConfig, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/import", nil)
	if err != nil {
		return nil, err
	}
	if len(c.token) > 0 {
		httpReq.SetBasicAuth(c.token, "")
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if err := checkResponse(httpResp); err != nil {
		return nil, err
	}

	config := &remotecache.CacheConfig{}
	if err := json.NewDecoder(httpResp.Body).Decode(config); err != nil {
		return nil, err
	}
	return config, nil
}

//nolint:dupl
func (c *client) GetLayerDownloadURL(ctx context.Context, req GetLayerDownloadURLRequest) (*GetLayerDownloadURLResponse, error) {
	bodyR, bodyW := io.Pipe()
	encoder := json.NewEncoder(bodyW)
	go func() {
		defer bodyW.Close()
		if err := encoder.Encode(req); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to encode request")
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/layerDownloadURL", bodyR)
	if err != nil {
		return nil, err
	}
	if len(c.token) > 0 {
		httpReq.SetBasicAuth(c.token, "")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if err := checkResponse(httpResp); err != nil {
		return nil, err
	}

	resp := &GetLayerDownloadURLResponse{}
	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

//nolint:dupl
func (c *client) GetLayerUploadURL(ctx context.Context, req GetLayerUploadURLRequest) (*GetLayerUploadURLResponse, error) {
	bodyR, bodyW := io.Pipe()
	encoder := json.NewEncoder(bodyW)
	go func() {
		defer bodyW.Close()
		if err := encoder.Encode(req); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to encode request")
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/layerUploadURL", bodyR)
	if err != nil {
		return nil, err
	}
	if len(c.token) > 0 {
		httpReq.SetBasicAuth(c.token, "")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if err := checkResponse(httpResp); err != nil {
		return nil, err
	}

	resp := &GetLayerUploadURLResponse{}
	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

//nolint:dupl
func (c *client) GetCacheMountConfig(ctx context.Context, req GetCacheMountConfigRequest) (*GetCacheMountConfigResponse, error) {
	bodyR, bodyW := io.Pipe()
	encoder := json.NewEncoder(bodyW)
	go func() {
		defer bodyW.Close()
		if err := encoder.Encode(req); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to encode request")
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/cacheMountConfig", bodyR)
	if err != nil {
		return nil, err
	}
	if len(c.token) > 0 {
		httpReq.SetBasicAuth(c.token, "")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if err := checkResponse(httpResp); err != nil {
		return nil, err
	}

	resp := &GetCacheMountConfigResponse{}
	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

//nolint:dupl
func (c *client) GetCacheMountUploadURL(ctx context.Context, req GetCacheMountUploadURLRequest) (*GetCacheMountUploadURLResponse, error) {
	bodyR, bodyW := io.Pipe()
	encoder := json.NewEncoder(bodyW)
	go func() {
		defer bodyW.Close()
		if err := encoder.Encode(req); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to encode request")
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/cacheMountUploadURL", bodyR)
	if err != nil {
		return nil, err
	}
	if len(c.token) > 0 {
		httpReq.SetBasicAuth(c.token, "")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if err := checkResponse(httpResp); err != nil {
		return nil, err
	}

	resp := &GetCacheMountUploadURLResponse{}
	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return nil, err
	}
	return resp, nil
}
