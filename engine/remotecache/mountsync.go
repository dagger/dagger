package remotecache

import (
	"context"
	"os"
	"path"
	"strconv"
	"time"

	"dagger.io/dagger"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

func startS3CacheMountSync(ctx context.Context, attrs map[string]string, daggerClient *dagger.Client) (func(ctx context.Context) error, error) {
	stop := func(ctx context.Context) error { return nil } // default to no-op
	settings := settings(attrs)

	if len(settings.synchronizedCacheMounts()) == 0 {
		return stop, nil
	}

	bklog.G(ctx).Debugf("syncing cache mounts under prefix %q: %+v", settings.prefix(), settings.synchronizedCacheMounts())

	cacheMountsExportPrefix := settings.prefix() + cacheMountsSubprefix + settings.name() + "/"

	s3Client, _, err := getS3Client(ctx, settings)
	if err != nil {
		return nil, err
	}

	existingCacheMountKeys, err := getExistingCacheMountKeys(ctx, s3Client, settings)
	if err != nil {
		return nil, err
	}

	otherEngineNames, err := getOtherEngineNames(ctx, s3Client, settings)
	if err != nil {
		return nil, err
	}

	var eg errgroup.Group
	for _, cacheMountName := range settings.synchronizedCacheMounts() {
		cacheMountPrefix := settings.prefix() + cacheMountsSubprefix + settings.name() + "/" + cacheMountName + "/"
		cacheMountName := cacheMountName
		eg.Go(func() error {
			if _, ok := existingCacheMountKeys[cacheMountName]; !ok {
				// no existing cache mount backup to import from, check to see if there's any others we could use
				for _, otherEngineName := range otherEngineNames {
					otherEngineName := otherEngineName
					otherCacheMountPrefix := settings.prefix() + cacheMountsSubprefix + otherEngineName + "/" + cacheMountName
					// check if this prefix exists
					resp, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
						Bucket:  aws.String(settings.bucket()),
						MaxKeys: 1, // only need to check if there's any objects in this prefix
						Prefix:  aws.String(otherCacheMountPrefix),
					})
					if err != nil {
						if !isS3NotFound(err) {
							return errors.Wrapf(err, "error checking for existing cache mount backup")
						}
						continue
					}
					if len(resp.Contents) == 0 {
						continue
					}
					// use the other engine's cache mount backup
					cacheMountPrefix = otherCacheMountPrefix
				}
			}
			bklog.G(ctx).Debugf("importing cache mount %q", cacheMountPrefix)
			err := execRclone(ctx, daggerClient, rcloneDownloadArgs(cacheMountPrefix, settings), cacheMountName)
			if err != nil {
				bklog.G(ctx).Debugf("failed to sync cache mount locally %s: %v", cacheMountName, err)
				return err
			}
			bklog.G(ctx).Debugf("synced cache mount locally %s", cacheMountName)
			return nil
		})
	}
	err = eg.Wait()
	if err != nil {
		return nil, err
	}

	stop = func(ctx context.Context) error {
		var eg errgroup.Group
		for _, cacheMountName := range settings.synchronizedCacheMounts() {
			cacheMountName := cacheMountName
			eg.Go(func() error {
				bklog.G(ctx).Debugf("syncing cache mount remotely %s", cacheMountName)

				cacheMountPrefix := cacheMountsExportPrefix + cacheMountName
				err := execRclone(ctx, daggerClient, rcloneUploadArgs(cacheMountPrefix, settings), cacheMountName)
				if err != nil {
					bklog.G(ctx).Errorf("failed to sync cache mount remotely %s: %v", cacheMountName, err)
					return err
				}
				bklog.G(ctx).Debugf("synced cache mount remotely %s", cacheMountName)
				return nil
			})
		}
		return eg.Wait()
	}

	return stop, nil
}

func getS3Client(ctx context.Context, s settings) (_ *s3.Client, bucket string, _ error) {
	cfg, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion(s.region()))
	if err != nil {
		return nil, "", errors.Errorf("Unable to load AWS SDK config, %v", err)
	}
	s3Client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		if s.accessKey() != "" && s.secretKey() != "" {
			options.Credentials = credentials.NewStaticCredentialsProvider(s.accessKey(), s.secretKey(), s.sessionToken())
		}
		if s.endpointURL() != "" {
			options.UsePathStyle = s.usePathStyle()
			options.EndpointResolver = s3.EndpointResolverFromURL(s.endpointURL())
		}
	})

	return s3Client, s.bucket(), nil
}

func getExistingCacheMountKeys(ctx context.Context, s3Client *s3.Client, settings settings) (map[string]struct{}, error) {
	existingCacheMountKeys := map[string]struct{}{}
	listObjectsPages := s3.NewListObjectsV2Paginator(s3Client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(settings.bucket()),
		Prefix:    aws.String(settings.prefix() + cacheMountsSubprefix + settings.name()),
		Delimiter: aws.String("/"),
	})
	for listObjectsPages.HasMorePages() {
		listResp, err := listObjectsPages.NextPage(ctx)
		if err != nil {
			if !isS3NotFound(err) {
				return nil, errors.Wrapf(err, "error listing s3 objects")
			}
		}
		for _, name := range listResp.CommonPrefixes {
			existingCacheMountKeys[path.Base(*name.Prefix)] = struct{}{}
		}
	}
	return existingCacheMountKeys, nil
}

func getOtherEngineNames(ctx context.Context, s3Client *s3.Client, settings settings) ([]string, error) {
	var otherEngineNames []string
	listEnginesPages := s3.NewListObjectsV2Paginator(s3Client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(settings.bucket()),
		Prefix:    aws.String(settings.prefix() + cacheMountsSubprefix),
		Delimiter: aws.String("/"),
	})
	for listEnginesPages.HasMorePages() {
		listResp, err := listEnginesPages.NextPage(ctx)
		if err != nil {
			if !isS3NotFound(err) {
				return nil, errors.Wrapf(err, "error listing s3 objects")
			}
		}
		for _, obj := range listResp.CommonPrefixes {
			otherEngineName := path.Base(*obj.Prefix)
			if otherEngineName == settings.name() {
				continue
			}
			otherEngineNames = append(otherEngineNames, otherEngineName)
		}
	}
	return otherEngineNames, nil
}

func rcloneCommonArgs(s settings) []string {
	// https://rclone.org/docs/
	// https://rclone.org/s3/
	commonArgs := []string{
		"--s3-provider", s.serverImplementation(),
		"--s3-region", s.region(),
		"--s3-acl=private",
		"--s3-no-check-bucket=true", // don't try to auto-create bucket
		"--s3-env-auth=true",        // use AWS_* env vars for auth
		"--metadata",                // preserve file metadata (note: this hurts performance a bit)
		"-v",

		// performance knobs
		// "--s3-memory-pool-use-mmap=true",
		// "--s3-upload-concurrency", strconv.Itoa(runtime.NumCPU()),
		// --s3-list-chunk
		// --s3-chunk-size
		// --s3-copy-cutoff
		// --s3-disable-checksum
		// --s3-use-accelerate-endpoint
		// --s3-no-head
		// --s3-no-head-object
		// --s3-encoding
		// --s3-memory-pool-flush-time
		// --size-only
		// --checksum
		// --update --use-server-modtime
		// --buffer-size=SIZE
		// --checkers=N
		// --max-backlog=N
		// --max-depth=N
		// --multi-thread-cutoff
		// --multi-thread-streams
		// --track-renames
		// --fast-list
		// --transfers
		// --use-mmap

		// misc options
		// --s3-leave-parts-on-error
		// --retries
		// --retries-sleep
	}

	if s.endpointURL() != "" {
		commonArgs = append(commonArgs, "--s3-endpoint", s.endpointURL())
	}
	if s.usePathStyle() {
		commonArgs = append(commonArgs, "--s3-force-path-style=true")
	} else {
		commonArgs = append(commonArgs, "--s3-force-path-style=false")
	}

	// TODO:(sipsma) use secrets instead, these currently are insecure anyways since they come from an env
	// to the engine though and are only needed for our integ tests.
	if s.accessKey() != "" {
		commonArgs = append(commonArgs, "--s3-access-key-id", s.accessKey())
	}
	if s.secretKey() != "" {
		commonArgs = append(commonArgs, "--s3-secret-access-key", s.secretKey())
	}
	if s.sessionToken() != "" {
		commonArgs = append(commonArgs, "--s3-session-token", s.sessionToken())
	}
	return commonArgs
}

func rcloneDownloadArgs(cacheMountPrefix string, s settings) []string {
	downloadArgs := []string{
		"copy",
		":s3:" + s.bucket() + "/" + cacheMountPrefix,
		"/mnt",
	}
	return append(downloadArgs, rcloneCommonArgs(s)...)
}

func rcloneUploadArgs(cacheMountPrefix string, s settings) []string {
	uploadArgs := []string{
		"sync", // unlike copy, sync results in deletions taking place in remote
		"/mnt",
		":s3:" + s.bucket() + "/" + cacheMountPrefix,
	}
	return append(uploadArgs, rcloneCommonArgs(s)...)
}

func execRclone(ctx context.Context, c *dagger.Client, args []string, cacheMountName string) error {
	ctr := c.Container().
		From("rclone/rclone:1.61").
		WithEnvVariable("CACHEBUST", strconv.Itoa(int(time.Now().UnixNano()))).
		WithMountedCache("/mnt", c.CacheVolume(cacheMountName))

	if v, ok := os.LookupEnv("AWS_STS_REGIONAL_ENDPOINTS"); ok {
		ctr = ctr.WithEnvVariable("AWS_STS_REGIONAL_ENDPOINTS", v)
	}
	if v, ok := os.LookupEnv("AWS_DEFAULT_REGION"); ok {
		ctr = ctr.WithEnvVariable("AWS_DEFAULT_REGION", v)
	}
	if v, ok := os.LookupEnv("AWS_REGION"); ok {
		ctr = ctr.WithEnvVariable("AWS_REGION", v)
	}
	if v, ok := os.LookupEnv("AWS_ROLE_ARN"); ok {
		ctr = ctr.WithEnvVariable("AWS_ROLE_ARN", v)
	}
	if v, ok := os.LookupEnv("AWS_WEB_IDENTITY_TOKEN_FILE"); ok {
		ctr = ctr.WithEnvVariable("AWS_WEB_IDENTITY_TOKEN_FILE", v)
		contents, err := os.ReadFile(v)
		if err != nil {
			return err
		}
		// TODO:(sipsma) use dynamic secrets API for this once available
		ctr = ctr.WithMountedSecret(v, c.Directory().WithNewFile("token", string(contents)).File("token").Secret())
	}

	_, err := ctr.WithExec(args).ExitCode(ctx)
	return err
}
