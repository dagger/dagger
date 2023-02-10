package remotecache

import (
	"context"
	"os"
	"strconv"
	"time"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/moby/buildkit/cache/remotecache"
	s3remotecache "github.com/moby/buildkit/cache/remotecache/s3"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const (
	bucketAttr       = "bucket"
	regionAttr       = "region"
	prefixAttr       = "prefix"
	endpointURLAttr  = "endpoint_url"
	usePathStyleAttr = "use_path_style"
	accessKeyAttr    = "access_key_id"
	secretKeyAttr    = "secret_access_key"
	sessionTokenAttr = "session_token"

	blobsSubprefix     = "blobs/"
	manifestsSubprefix = "manifests/"
)

func s3Exporter(ctx context.Context, g session.Group, attrs map[string]string) (remotecache.Exporter, error) {
	attrs["blob_prefix"] = blobsSubprefix
	attrs["manifests_prefix"] = manifestsSubprefix
	attrs["name"] = strconv.Itoa(int(time.Now().UnixNano())) + ".json"
	attrs["ignore-error"] = "true"
	return s3remotecache.ResolveCacheExporterFunc()(ctx, g, attrs)
}

func s3Importer(ctx context.Context, g session.Group, attrs map[string]string) (remotecache.Importer, ocispecs.Descriptor, error) {
	prefix := attrs[prefixAttr]
	bklog.G(ctx).Debugf("importing all manifests under prefix %q", prefix)

	region := attrs[regionAttr]
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	bucket := attrs[bucketAttr]
	if bucket == "" {
		bucket = os.Getenv("AWS_BUCKET")
	}

	accessKey := attrs[accessKeyAttr]
	secretKey := attrs[secretKeyAttr]
	sessionToken := attrs[sessionTokenAttr]
	endpointURL := attrs[endpointURLAttr]
	usePathStyle := attrs[usePathStyleAttr] == "true"

	cfg, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion(region))
	if err != nil {
		return nil, ocispecs.Descriptor{}, errors.Errorf("Unable to load AWS SDK config, %v", err)
	}
	s3Client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		if accessKey != "" && secretKey != "" {
			options.Credentials = credentials.NewStaticCredentialsProvider(accessKey, secretKey, sessionToken)
		}
		if endpointURL != "" {
			options.UsePathStyle = usePathStyle
			options.EndpointResolver = s3.EndpointResolverFromURL(endpointURL)
		}
	})

	manifestsPrefix := prefix + manifestsSubprefix

	listObjectsPages := s3.NewListObjectsV2Paginator(s3Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(manifestsPrefix),
	})
	var manifestKeys []string
	for listObjectsPages.HasMorePages() {
		listResp, err := listObjectsPages.NextPage(ctx)
		if err != nil {
			// ignore not exists
			// TODO: does this err check work? it's what copilot suggested
			if awsErr, ok := err.(awserr.Error); !ok || awsErr.Code() != "NotFound" {
				return nil, ocispecs.Descriptor{}, err
			}
			bklog.G(ctx).Debugf("not found error under prefix %s", prefix)
		}
		for _, obj := range listResp.Contents {
			manifestKeys = append(manifestKeys, *obj.Key)
		}
	}
	bklog.G(ctx).Debugf("found manifests under prefix %s: %+v", prefix, manifestKeys)

	importers := make([]remotecache.Importer, len(manifestKeys))
	for i, manifestKey := range manifestKeys {
		theseAttrs := map[string]string{}
		for k, v := range attrs {
			theseAttrs[k] = v
		}
		theseAttrs["prefix"] = ""
		theseAttrs["manifests_prefix"] = ""
		theseAttrs["blobs_prefix"] = prefix + blobsSubprefix
		theseAttrs["name"] = manifestKey
		importer, _, err := s3remotecache.ResolveCacheImporterFunc()(ctx, g, theseAttrs)
		if err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
		importers[i] = importer
	}
	return &combinedImporter{importers: importers}, ocispecs.Descriptor{}, nil
}
