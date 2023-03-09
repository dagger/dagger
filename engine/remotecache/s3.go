package remotecache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/smithy-go"
	"github.com/containerd/containerd/content"
	"github.com/moby/buildkit/cache/remotecache"
	v1 "github.com/moby/buildkit/cache/remotecache/v1"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/compression"
	"github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const (
	blobsSubprefix     = "blobs/"
	manifestsSubprefix = "manifests/"

	experimentalDaggerS3CacheType = "experimental_dagger_s3"
)

type settings map[string]string

func (s settings) bucket() string {
	b := s["bucket"]
	if b == "" {
		b = os.Getenv("AWS_BUCKET")
	}
	return b
}

func (s settings) region() string {
	r := s["region"]
	if r == "" {
		r = os.Getenv("AWS_REGION")
	}
	return r
}

func (s settings) prefix() string {
	return s["prefix"]
}

func (s settings) name() string {
	return s["name"]
}

func (s settings) endpointURL() string {
	return s["endpoint_url"]
}

func (s settings) usePathStyle() bool {
	return s["use_path_style"] == "true"
}

func (s settings) accessKey() string {
	return s["access_key_id"]
}

func (s settings) secretKey() string {
	return s["secret_access_key"]
}

func (s settings) sessionToken() string {
	return s["session_token"]
}

type s3CacheManager struct {
	mu                sync.Mutex
	config            v1.CacheConfig
	descProviders     v1.DescriptorProvider
	exportRequested   chan struct{}
	settings          settings
	s3Client          *s3.Client
	s3UploadManager   *manager.Uploader
	s3DownloadManager *manager.Downloader
}

func newS3CacheManager(ctx context.Context, attrs map[string]string, doneCh chan<- struct{}) (*s3CacheManager, error) {
	m := &s3CacheManager{
		descProviders:   v1.DescriptorProvider{},
		exportRequested: make(chan struct{}, 1),
		settings:        settings(attrs),
	}

	cfg, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion(m.settings.region()))
	if err != nil {
		return nil, errors.Errorf("Unable to load AWS SDK config, %v", err)
	}
	m.s3Client = s3.NewFromConfig(cfg, func(options *s3.Options) {
		if m.settings.accessKey() != "" && m.settings.secretKey() != "" {
			options.Credentials = credentials.NewStaticCredentialsProvider(m.settings.accessKey(), m.settings.secretKey(), m.settings.sessionToken())
		}
		if m.settings.endpointURL() != "" {
			options.UsePathStyle = m.settings.usePathStyle()
			options.EndpointResolver = s3.EndpointResolverFromURL(m.settings.endpointURL())
		}
	})
	m.s3UploadManager = manager.NewUploader(m.s3Client)
	m.s3DownloadManager = manager.NewDownloader(m.s3Client)

	// loop for exporting asychronously as requested
	go func() {
		defer close(doneCh)
		var shutdown bool
		for {
			select {
			case <-m.exportRequested:
			case <-ctx.Done():
				shutdown = true
				// always run a final export before shutdown
			}
			exportCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute) // TODO:(sipsma) arbitrary number, should be configurable
			defer cancel()
			if err := m.export(exportCtx); err != nil {
				bklog.G(ctx).WithError(err).Error("failed to export s3 cache")
			}
			if shutdown {
				return
			}
		}
	}()

	// do an initial synchronous import from the pool
	if err := m.importFromPool(ctx); err != nil {
		return nil, err
	}
	// loop for periodic async imports
	go func() {
		for {
			select {
			case <-time.After(5 * time.Minute): // TODO:(sipsma) arbitrary number, should be configurable
			case <-ctx.Done():
				return
			}
			if err := m.importFromPool(ctx); err != nil {
				bklog.G(ctx).WithError(err).Error("failed to import s3 cache")
			}
		}
	}()

	return m, nil
}

func (m *s3CacheManager) mergeChains(ctx context.Context, chains *v1.CacheChains) error {
	// TODO:(sipsma) I don't think this results in a multiprovider for each desc provider,
	// only one wins. We should make sure that doesn't cause weird issues in e.g. pruning
	// cases where a provider may no longer be valid
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := v1.ParseConfig(m.config, m.descProviders, chains); err != nil {
		return err
	}
	newConfig, newProviders, err := chains.Marshal(ctx)
	if err != nil {
		return err
	}
	m.config = *newConfig
	m.descProviders = newProviders
	return nil
}

func (m *s3CacheManager) requestExport() {
	// put in a request, but if there's already one pending no need to send another
	select {
	case m.exportRequested <- struct{}{}:
	default:
	}
}

func (m *s3CacheManager) copyConfig() (v1.CacheConfig, v1.DescriptorProvider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// json serder is a cheap way of doing a deep copy for the config
	data, err := json.Marshal(m.config)
	if err != nil {
		return v1.CacheConfig{}, nil, err
	}
	var config v1.CacheConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return v1.CacheConfig{}, nil, err
	}

	// make a copy of the provider map
	descriptors := v1.DescriptorProvider{}
	for k, v := range m.descProviders {
		descriptors[k] = v
	}
	return config, descriptors, nil
}

func (m *s3CacheManager) export(ctx context.Context) error {
	// make a copy so we work with a snapshot rather than something mutating during export
	cacheConfig, descs, err := m.copyConfig()
	if err != nil {
		return err
	}

	// TODO:(sipsma) optional parallelization
	for i, l := range cacheConfig.Layers {
		dgstPair, ok := descs[l.Blob]
		if !ok {
			return errors.Errorf("missing blob %s", l.Blob)
		}
		if dgstPair.Descriptor.Annotations == nil {
			return errors.Errorf("invalid descriptor without annotations")
		}
		v, ok := dgstPair.Descriptor.Annotations["containerd.io/uncompressed"]
		if !ok {
			return errors.Errorf("invalid descriptor without uncompressed annotation")
		}
		diffID, err := digest.Parse(v)
		if err != nil {
			return errors.Wrapf(err, "failed to parse uncompressed annotation")
		}

		key := m.blobKey(dgstPair.Descriptor.Digest)
		exists, err := m.s3KeyExists(ctx, key)
		if err != nil {
			return errors.Wrapf(err, "failed to check file presence in cache")
		}
		if !exists {
			bklog.G(ctx).Debugf("s3 exporter: uploading blob %s", l.Blob)
			blobReader, err := dgstPair.Provider.ReaderAt(ctx, dgstPair.Descriptor)
			if err != nil {
				return err
			}
			if err := m.uploadToS3(ctx, key, content.NewReader(blobReader)); err != nil {
				return errors.Wrap(err, "error writing layer blob")
			}
		}

		la := &v1.LayerAnnotations{
			DiffID:    diffID,
			Size:      dgstPair.Descriptor.Size,
			MediaType: dgstPair.Descriptor.MediaType,
		}
		if v, ok := dgstPair.Descriptor.Annotations["buildkit/createdat"]; ok {
			var t time.Time
			if err := (&t).UnmarshalText([]byte(v)); err != nil {
				return err
			}
			la.CreatedAt = t.UTC()
		}
		cacheConfig.Layers[i].Annotations = la
	}

	configBytes, err := json.Marshal(cacheConfig)
	if err != nil {
		return err
	}

	if err := m.uploadToS3(ctx, m.manifestKey(), bytes.NewReader(configBytes)); err != nil {
		return errors.Wrapf(err, "error writing manifest: %s", m.manifestKey())
	}

	return nil
}

func (m *s3CacheManager) importFromPool(ctx context.Context) error {
	var manifestKeys []string
	listObjectsPages := s3.NewListObjectsV2Paginator(m.s3Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(m.settings.bucket()),
		Prefix: aws.String(m.manifestsPrefix()),
	})
	for listObjectsPages.HasMorePages() {
		listResp, err := listObjectsPages.NextPage(ctx)
		if err != nil {
			if !isS3NotFound(err) {
				return errors.Wrapf(err, "error listing s3 objects")
			}
		}
		for _, obj := range listResp.Contents {
			manifestKeys = append(manifestKeys, *obj.Key)
		}
	}
	configs := make([]v1.CacheConfig, 0, len(manifestKeys))
	descProvider := v1.DescriptorProvider{}
	// TODO:(sipsma) skip own manifest after initial import
	for _, manifestKey := range manifestKeys {
		configBuffer := manager.NewWriteAtBuffer([]byte{})
		if err := m.downloadFromS3(ctx, manifestKey, configBuffer); err != nil {
			return errors.Wrapf(err, "error reading manifest: %s", manifestKey)
		}
		var config v1.CacheConfig
		if err := json.Unmarshal(configBuffer.Bytes(), &config); err != nil {
			return err
		}
		configs = append(configs, config)
		for _, l := range config.Layers {
			providerPair, err := m.descriptorProviderPair(l)
			if err != nil {
				return err
			}
			descProvider[l.Blob] = *providerPair
		}
	}

	for _, config := range configs {
		chain := v1.NewCacheChains()
		if err := v1.ParseConfig(config, descProvider, chain); err != nil {
			return err
		}
		if err := m.mergeChains(ctx, chain); err != nil {
			return err
		}
	}
	return nil
}

func (m *s3CacheManager) Resolve(ctx context.Context, _ ocispecs.Descriptor, id string, w worker.Worker) (solver.CacheManager, error) {
	// make a snapshot of the current cache config as the existing cache key
	// storage and manager don't handle the chains mutating underneath them
	// TODO:(sipsma) a cache key storage + manager that did handle mutating
	// config would be better as it would allow cache imports from other
	// engines to take affect in the middle of any solves happening in this
	// engine.
	config, providers, err := m.copyConfig()
	if err != nil {
		return nil, err
	}
	chains := v1.NewCacheChains()
	if err := v1.ParseConfig(config, providers, chains); err != nil {
		return nil, err
	}
	keyStore, resultStore, err := v1.NewCacheKeyStorage(chains, w)
	if err != nil {
		return nil, err
	}
	return solver.NewCacheManager(ctx, id, keyStore, resultStore), nil
}

func (m *s3CacheManager) blobKey(dgst digest.Digest) string {
	return m.settings.prefix() + blobsSubprefix + dgst.String()
}

func (m *s3CacheManager) manifestsPrefix() string {
	return m.settings.prefix() + manifestsSubprefix
}

func (m *s3CacheManager) manifestKey() string {
	return m.manifestsPrefix() + m.settings.name()
}

func (m *s3CacheManager) s3KeyExists(ctx context.Context, key string) (bool, error) {
	_, err := m.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(m.settings.bucket()),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (m *s3CacheManager) uploadToS3(ctx context.Context, key string, contents io.Reader) error {
	_, err := m.s3UploadManager.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(m.settings.bucket()),
		Key:    aws.String(key),
		Body:   contents,
	})
	return err
}

func (m *s3CacheManager) downloadFromS3(ctx context.Context, key string, dest io.WriterAt) error {
	_, err := m.s3DownloadManager.Download(ctx, dest, &s3.GetObjectInput{
		Bucket: aws.String(m.settings.bucket()),
		Key:    aws.String(key),
	})
	return err
}

func (m *s3CacheManager) descriptorProviderPair(layer v1.CacheLayer) (*v1.DescriptorProviderPair, error) {
	if layer.Annotations == nil {
		return nil, errors.Errorf("missing annotations for layer %s", layer.Blob)
	}

	annotations := map[string]string{}
	if layer.Annotations.DiffID == "" {
		return nil, errors.Errorf("missing diffID for layer %s", layer.Blob)
	}
	annotations["containerd.io/uncompressed"] = layer.Annotations.DiffID.String()
	if !layer.Annotations.CreatedAt.IsZero() {
		createdAt, err := layer.Annotations.CreatedAt.MarshalText()
		if err != nil {
			return nil, err
		}
		annotations["buildkit/createdat"] = string(createdAt)
	}
	return &v1.DescriptorProviderPair{
		Provider: m,
		Descriptor: ocispecs.Descriptor{
			MediaType:   layer.Annotations.MediaType,
			Digest:      layer.Blob,
			Size:        layer.Annotations.Size,
			Annotations: annotations,
		},
	}, nil
}

func (m *s3CacheManager) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	return &s3ReaderAt{
		ctx:    ctx,
		client: m.s3Client,
		bucket: m.settings.bucket(),
		key:    m.blobKey(desc.Digest),
		size:   desc.Size,
	}, nil
}

type s3CacheExporter struct {
	*v1.CacheChains
	manager *s3CacheManager
}

var _ remotecache.Exporter = &s3CacheExporter{}

func newS3CacheExporter(manager *s3CacheManager) *s3CacheExporter {
	return &s3CacheExporter{
		CacheChains: v1.NewCacheChains(),
		manager:     manager,
	}
}

func (e *s3CacheExporter) Name() string {
	return "dagger-s3-exporter"
}

func (e *s3CacheExporter) Config() remotecache.Config {
	return remotecache.Config{
		// TODO: support for faster compression types like zstd
		Compression: compression.New(compression.Default),
	}
}

func (e *s3CacheExporter) Finalize(ctx context.Context) (map[string]string, error) {
	err := e.manager.mergeChains(ctx, e.CacheChains)
	if err != nil {
		return nil, err
	}
	e.manager.requestExport()
	return nil, nil
}

func isS3NotFound(err error) bool {
	var errapi smithy.APIError
	return errors.As(err, &errapi) && (errapi.ErrorCode() == "NoSuchKey" || errapi.ErrorCode() == "NotFound")
}

// s3ReaderAt is optimized for reading a layer into the content store. Layers are read sequentially and in
// 1MB chunks by the underlying containerd content code. We therefore initialize the reader at the first
// offset and after that keep reading sequentially. If an attempt is made at a non-sequental read the reader
// is re-opened from the new offset, which is slow but not expected to happen often.
//
// The relevant code currently lives here:
// https://github.com/containerd/containerd/blob/7a77da2c26007fbf4b8526fd01d5ab06ac12d452/content/helpers.go#L150
type s3ReaderAt struct {
	ctx    context.Context
	client *s3.Client
	bucket string
	key    string
	size   int64

	// internally set fields
	body   io.ReadCloser
	offset int64
}

func (r *s3ReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if r.body == nil || off != r.offset {
		// this is either the first read or a non-sequential one, so we need to (re-)open the reader
		resp, err := r.client.GetObject(r.ctx, &s3.GetObjectInput{
			Bucket: aws.String(r.bucket),
			Key:    aws.String(r.key),
			Range:  aws.String(fmt.Sprintf("bytes=%d-", off)),
		})
		if err != nil {
			return 0, err
		}
		if r.body != nil {
			// close previous body if we had to reset due to non-sequential read
			bklog.G(r.ctx).Debugf("non-sequential read in s3ReaderAt for key %s, %d != %d", r.key, off, r.offset)
			r.body.Close()
		}
		r.body = resp.Body
		r.offset = off
	}

	n, err := r.body.Read(p)
	r.offset += int64(n)
	return n, err
}

func (r *s3ReaderAt) Size() int64 {
	return r.size
}

func (r *s3ReaderAt) Close() error {
	if r.body != nil {
		return r.body.Close()
	}
	return nil
}
