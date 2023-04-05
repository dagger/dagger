package cache

import (
	"context"
	"fmt"
	"io"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	s3sdkmanager "github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/smithy-go"
	"github.com/containerd/containerd/content"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type S3LayerStoreConfig struct {
	Bucket             string
	Region             string
	EndpointURL        string
	UsePathStyle       bool
	BlobsPrefix        string
	CacheMountPrefixes []string
	// TODO: auth stuff?
}

type S3LayerStore struct {
	config            S3LayerStoreConfig
	s3Client          *s3.Client
	s3UploadManager   *s3sdkmanager.Uploader
	s3DownloadManager *s3sdkmanager.Downloader
}

var _ LayerStore = &S3LayerStore{}

func NewS3LayerStore(ctx context.Context, config S3LayerStoreConfig) (LayerStore, error) {
	c := &S3LayerStore{
		config: config,
	}

	awsSDKConfig, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion(config.Region))
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS SDK config: %w", err)
	}
	c.s3Client = s3.NewFromConfig(awsSDKConfig, func(options *s3.Options) {
		if config.EndpointURL != "" {
			options.UsePathStyle = config.UsePathStyle
			options.EndpointResolver = s3.EndpointResolverFromURL(config.EndpointURL)
		}
	})
	c.s3UploadManager = s3sdkmanager.NewUploader(c.s3Client)
	c.s3DownloadManager = s3sdkmanager.NewDownloader(c.s3Client)

	return c, nil
}

func (c *S3LayerStore) PushLayer(ctx context.Context, layer ocispecs.Descriptor, provider content.Provider) error {
	key := c.blobKey(layer.Digest)
	exists, err := c.s3KeyExists(ctx, key)
	if err != nil {
		return errors.Wrapf(err, "failed to check file presence in cache")
	}
	if !exists {
		bklog.G(ctx).Debugf("s3 exporter: uploading blob %s", layer.Digest)
		blobReader, err := provider.ReaderAt(ctx, layer)
		if err != nil {
			return err
		}
		if err := c.uploadToS3(ctx, key, content.NewReader(blobReader)); err != nil {
			return errors.Wrap(err, "error writing layer blob")
		}
	}
	return nil
}

func (c *S3LayerStore) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	return &S3ReaderAt{
		Ctx:      ctx,
		Client:   c.s3Client,
		Bucket:   c.config.Bucket,
		Key:      c.blobKey(desc.Digest),
		BlobSize: desc.Size,
	}, nil
}

func (c *S3LayerStore) blobKey(dgst digest.Digest) string {
	return c.config.BlobsPrefix + dgst.String()
}

func (c *S3LayerStore) s3KeyExists(ctx context.Context, key string) (bool, error) {
	_, err := c.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if IsS3NotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *S3LayerStore) uploadToS3(ctx context.Context, key string, contents io.Reader) error {
	_, err := c.s3UploadManager.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
		Body:   contents,
	})
	return err
}

// S3ReaderAt is optimized for reading a layer into the content store. Layers are read sequentially and in
// 1MB chunks by the underlying containerd content code. We therefore initialize the reader at the first
// offset and after that keep reading sequentially. If an attempt is made at a non-sequental read the reader
// is re-opened from the new offset, which is slow but not expected to happen often.
//
// The relevant code currently lives here:
// https://github.com/containerd/containerd/blob/7a77da2c26007fbf4b8526fd01d5ab06ac12d452/content/helpers.go#L150
type S3ReaderAt struct {
	Ctx      context.Context
	Client   *s3.Client
	Bucket   string
	Key      string
	BlobSize int64

	// internally set fields
	body   io.ReadCloser
	offset int64
}

func (r *S3ReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if r.body == nil || off != r.offset {
		// this is either the first read or a non-sequential one, so we need to (re-)open the reader
		resp, err := r.Client.GetObject(r.Ctx, &s3.GetObjectInput{
			Bucket: aws.String(r.Bucket),
			Key:    aws.String(r.Key),
			Range:  aws.String(fmt.Sprintf("bytes=%d-", off)),
		})
		if err != nil {
			return 0, err
		}
		if r.body != nil {
			// close previous body if we had to reset due to non-sequential read
			bklog.G(r.Ctx).Debugf("non-sequential read in S3ReaderAt for key %s, %d != %d", r.Key, off, r.offset)
			r.body.Close()
		}
		r.body = resp.Body
		r.offset = off
	}

	n, err := r.body.Read(p)
	r.offset += int64(n)
	return n, err
}

func (r *S3ReaderAt) Size() int64 {
	return r.BlobSize
}

func (r *S3ReaderAt) Close() error {
	if r.body != nil {
		return r.body.Close()
	}
	return nil
}

func IsS3NotFound(err error) bool {
	var errapi smithy.APIError
	return errors.As(err, &errapi) && (errapi.ErrorCode() == "NoSuchKey" || errapi.ErrorCode() == "NotFound")
}
