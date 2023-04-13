package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/containerd/containerd/content"
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
	ImportCache(ctx context.Context) (*remotecache.CacheConfig, error)
}

type GetConfigRequest struct {
	CacheMountIDs []string
}

func (r GetConfigRequest) String() string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

type Config struct {
	S3            *S3LayerStoreConfig
	ImportPeriod  time.Duration
	ExportPeriod  time.Duration
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

//nolint:revive
type CacheKey struct {
	ID      string
	Results []Result
}

type Link struct {
	ID       string
	LinkedID string
	Input    int
	Digest   digest.Digest
	Selector digest.Digest
}

type Result struct {
	ID          string
	CreatedAt   time.Time
	Description string
}

type UpdateCacheRecordsResponse struct {
	// cache records that the engine should prepare layers for and push
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
	Digest     digest.Digest // record digest
	CacheRefID string        // worker cache id
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
	RecordDigest digest.Digest
	Layers       []ocispecs.Descriptor
}

type LayerStore interface {
	content.Provider
	PushLayer(ctx context.Context, layer ocispecs.Descriptor, provider content.Provider) error
}

type client struct {
	httpClient *http.Client
	host       string
}

var _ Service = &client{}

func newClient(urlString string) (Service, error) {
	c := &client{}

	u, err := url.Parse(urlString)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "tcp":
		c.host = u.Host
		c.httpClient = &http.Client{}
	case "unix":
		c.host = "local"
		c.httpClient = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", u.Path)
				},
			},
		}
	}

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

	httpReq, err := http.NewRequestWithContext(ctx, "GET", "http://"+c.host+"/config", bodyR)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", httpResp.StatusCode)
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

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://"+c.host+"/records", bodyR)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", httpResp.StatusCode)
	}

	resp := &UpdateCacheRecordsResponse{}
	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

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

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://"+c.host+"/layers", bodyR)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", httpResp.StatusCode)
	}

	return nil
}

func (c *client) ImportCache(ctx context.Context) (*remotecache.CacheConfig, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", "http://"+c.host+"/import", nil)
	if err != nil {
		return nil, err
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", httpResp.StatusCode)
	}

	config := &remotecache.CacheConfig{}
	if err := json.NewDecoder(httpResp.Body).Decode(config); err != nil {
		return nil, err
	}
	return config, nil
}
