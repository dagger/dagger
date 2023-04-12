package cache

import (
	"context"
	"os"
	"path"
	"strconv"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/util/bklog"
	"golang.org/x/sync/errgroup"
)

func startS3CacheMountSync(ctx context.Context, s3Config *S3LayerStoreConfig, daggerClient dagger.Client) (func(ctx context.Context) error, error) {
	stop := func(ctx context.Context) error { return nil } // default to no-op

	cacheMountPrefixes := s3Config.CacheMountPrefixes
	if len(cacheMountPrefixes) == 0 {
		return stop, nil
	}
	bklog.G(ctx).Debugf("syncing cache mounts %+v", cacheMountPrefixes)

	var eg errgroup.Group
	for _, cacheMountPrefix := range cacheMountPrefixes {
		cacheMountName := path.Base(cacheMountPrefix)
		cacheMountPrefix := cacheMountPrefix
		eg.Go(func() error {
			bklog.G(ctx).Debugf("importing cache mount %q", cacheMountPrefix)
			err := execRclone(ctx, daggerClient, rcloneDownloadArgs(cacheMountPrefix, s3Config), cacheMountName)
			if err != nil {
				bklog.G(ctx).Debugf("failed to sync cache mount locally %s: %v", cacheMountName, err)
				return err
			}
			bklog.G(ctx).Debugf("synced cache mount locally %s", cacheMountName)
			return nil
		})
	}
	err := eg.Wait()
	if err != nil {
		return nil, err
	}

	stop = func(ctx context.Context) error {
		var eg errgroup.Group
		for _, cacheMountPrefix := range cacheMountPrefixes {
			cacheMountName := path.Base(cacheMountPrefix)
			cacheMountPrefix := cacheMountPrefix
			eg.Go(func() error {
				bklog.G(ctx).Debugf("syncing cache mount remotely %s", cacheMountName)
				err := execRclone(ctx, daggerClient, rcloneUploadArgs(cacheMountPrefix, s3Config), cacheMountName)
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

func rcloneCommonArgs(cfg *S3LayerStoreConfig) []string {
	// https://rclone.org/docs/
	// https://rclone.org/s3/
	commonArgs := []string{
		"--s3-region", cfg.Region,
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

	if cfg.EndpointURL != "" {
		commonArgs = append(commonArgs, "--s3-endpoint", cfg.EndpointURL)
	}
	if cfg.UsePathStyle {
		commonArgs = append(commonArgs, "--s3-force-path-style=true")
	} else {
		commonArgs = append(commonArgs, "--s3-force-path-style=false")
	}

	return commonArgs
}

func rcloneDownloadArgs(cacheMountPrefix string, s3Config *S3LayerStoreConfig) []string {
	downloadArgs := []string{
		"copy",
		":s3:" + s3Config.Bucket + "/" + cacheMountPrefix,
		"/mnt",
	}
	return append(downloadArgs, rcloneCommonArgs(s3Config)...)
}

func rcloneUploadArgs(cacheMountPrefix string, s3Config *S3LayerStoreConfig) []string {
	uploadArgs := []string{
		"sync", // unlike copy, sync results in deletions taking place in remote
		"/mnt",
		":s3:" + s3Config.Bucket + "/" + cacheMountPrefix,
	}
	return append(uploadArgs, rcloneCommonArgs(s3Config)...)
}

func execRclone(ctx context.Context, c dagger.Client, args []string, cacheMountName string) error {
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
		secret := c.SetSecret("AWS_WEB_IDENTITY_TOKEN_FILE", string(contents))
		ctr = ctr.WithMountedSecret(v, secret)
	}
	if v, ok := os.LookupEnv("AWS_ACCESS_KEY_ID"); ok {
		ctr = ctr.WithSecretVariable("AWS_ACCESS_KEY_ID", c.SetSecret("AWS_ACCESS_KEY_ID", v))
	}
	if v, ok := os.LookupEnv("AWS_SECRET_ACCESS_KEY"); ok {
		ctr = ctr.WithSecretVariable("AWS_SECRET_ACCESS_KEY", c.SetSecret("AWS_SECRET_ACCESS_KEY", v))
	}
	if v, ok := os.LookupEnv("AWS_SESSION_TOKEN"); ok {
		ctr = ctr.WithSecretVariable("AWS_SESSION_TOKEN", c.SetSecret("AWS_SESSION_TOKEN", v))
	}

	_, err := ctr.WithExec(args).ExitCode(ctx)
	return err
}
