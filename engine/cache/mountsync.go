package cache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/klauspost/compress/zstd"
	"github.com/moby/buildkit/solver/llbsolver/mounts"
	solverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/leaseutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
)

func (m *manager) StartCacheMountSynchronization(ctx context.Context) error {
	getCacheMountConfigResp, err := m.cacheClient.GetCacheMountConfig(ctx, GetCacheMountConfigRequest{})
	if err != nil {
		return fmt.Errorf("failed to get cache mount config: %w", err)
	}
	syncedCacheMounts := getCacheMountConfigResp.SyncedCacheMounts

	var eg errgroup.Group
	for _, syncedCacheMount := range syncedCacheMounts {
		syncedCacheMount := syncedCacheMount
		if syncedCacheMount.URL == "" {
			// nothing to download, have to start fresh, skip it until we sync back to cloud at shutdown
			continue
		}
		eg.Go(func() error {
			bklog.G(ctx).Debugf("syncing cache mount locally %s", syncedCacheMount.Name)
			cacheKey := cacheKeyFromMountName(syncedCacheMount.Name)
			return withCacheMount(ctx, m.MountManager, cacheKey, func(ctx context.Context, mnt mount.Mount) error {
				cacheMountDir := mnt.Source // relies on our check that this is a bind mount in withCacheMount

				// if there's any existing data in the cache mount, we'll just leave it alone
				// NOTE: there's cases in which this heuristic isn't ideal, such as when a
				// remote cache mount has "better" contents than this one, but this will suffice
				// for now
				dirents, err := os.ReadDir(cacheMountDir)
				if err != nil {
					return fmt.Errorf("failed to read cache mount dir: %w", err)
				}
				if len(dirents) > 0 {
					bklog.G(ctx).Debugf("cache mount %q already has data, skipping", syncedCacheMount.Name)
					return nil
				}

				fsApplier := apply.NewFileSystemApplier(&cacheMountProvider{
					httpClient: m.httpClient,
					url:        syncedCacheMount.URL,
				})
				_, err = fsApplier.Apply(ctx, ocispecs.Descriptor{
					Digest:    syncedCacheMount.Digest,
					Size:      syncedCacheMount.Size,
					MediaType: syncedCacheMount.MediaType,
				}, []mount.Mount{mnt})
				if err != nil {
					if removeErr := removeAllUnderDir(cacheMountDir); removeErr != nil {
						err = errors.Join(err, fmt.Errorf("failed to empty out cache mount dir after failure %q: %w", cacheMountDir, removeErr))
					}
					return fmt.Errorf("failed to apply cache mount: %w", err)
				}

				bklog.G(ctx).Debugf("synced cache mount locally %s", syncedCacheMount.Name)
				return nil
			})
		})
	}
	err = eg.Wait()
	if err != nil {
		return err
	}

	m.stopCacheMountSync = func(ctx context.Context) error {
		var eg errgroup.Group

		seenCacheMounts := map[string]struct{}{}
		core.SeenCacheKeys.Range(func(k any, v any) bool {
			seenCacheMounts[k.(string)] = struct{}{}
			return true
		})

		for cacheMountName := range seenCacheMounts {
			cacheMountName := cacheMountName
			eg.Go(func() error {
				bklog.G(ctx).Debugf("syncing cache mount remotely %s", cacheMountName)
				cacheKey := cacheKeyFromMountName(cacheMountName)

				return withCacheMount(ctx, m.MountManager, cacheKey, func(ctx context.Context, mnt mount.Mount) error {
					// First compress the mount into the content store. We can't stream direct to S3 because we want
					// to tell S3 the checksum of the whole thing when we open the request there. Apparently there
					// is a way to include the checksum as a trailer, but it is poorly documented and seems to require
					// a different streaming request type, which is giving me a headache right now. Can optimize in future.

					// add a temporary lease so our content doesn't get pruned immediately from the store
					ctx, done, err := leaseutil.WithLease(ctx, m.Worker.LeaseManager(), leaseutil.MakeTemporary)
					if err != nil {
						return fmt.Errorf("failed to create lease: %w", err)
					}
					defer done(ctx)

					// compress the mount to a tar.zstd and write to the content store
					contentRef := "dagger-cachemount-" + cacheMountName
					contentWriter, err := m.Worker.ContentStore().Writer(ctx, content.WithRef(contentRef))
					if err != nil {
						return fmt.Errorf("failed to create content writer: %w", err)
					}
					defer contentWriter.Close()
					writeBuffer := bufio.NewWriterSize(contentWriter, 1024*1024)
					compressor, err := zstd.NewWriter(writeBuffer, zstd.WithEncoderLevel(zstd.SpeedDefault))
					if err != nil {
						return fmt.Errorf("failed to create compressor: %w", err)
					}
					defer compressor.Close()
					// mnt.Source relies on our check that this is a bind mount in withCacheMount
					err = archive.WriteDiff(ctx, compressor, "", mnt.Source)
					if err != nil {
						return fmt.Errorf("failed to write diff: %w", err)
					}
					if err := compressor.Close(); err != nil {
						return fmt.Errorf("failed to close compressor: %w", err)
					}
					writeBuffer.Flush()
					if err := contentWriter.Commit(ctx, 0, ""); err != nil {
						if errors.Is(err, errdefs.ErrAlreadyExists) {
							// we should be releasing these, but if it was already there, that's weird but fine
							bklog.G(ctx).Debugf("cache mount %q already committed", cacheMountName)
						} else {
							return fmt.Errorf("failed to commit content: %w", err)
						}
					}
					contentDigest := contentWriter.Digest()

					// now that we have the digest we can upload from the content store to the url
					contentReaderAt, err := m.Worker.ContentStore().ReaderAt(ctx, ocispecs.Descriptor{
						Digest: contentDigest,
					})
					if err != nil {
						return fmt.Errorf("failed to create content reader: %w", err)
					}
					defer contentReaderAt.Close()
					contentLength := contentReaderAt.Size()
					getURLResp, err := m.cacheClient.GetCacheMountUploadURL(ctx, GetCacheMountUploadURLRequest{
						CacheName: cacheMountName,
						Digest:    contentDigest,
						Size:      contentLength,
					})
					if err != nil {
						return fmt.Errorf("failed to get cache mount upload url: %w", err)
					}

					if getURLResp.Skip {
						bklog.G(ctx).Debugf("skipped pushing cache mount %s", cacheMountName)
						return nil
					}

					contentReader := io.NewSectionReader(contentReaderAt, 0, contentLength)
					httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, getURLResp.URL, contentReader)
					if err != nil {
						return fmt.Errorf("failed to create http request: %w", err)
					}
					httpReq.ContentLength = contentLength // set it here, go stdlib will ignore if set on Header (??!!)
					for k, v := range getURLResp.Headers {
						httpReq.Header.Set(k, v)
					}
					resp, err := m.httpClient.Do(httpReq)
					if err != nil {
						return fmt.Errorf("failed to upload cache mount: %w", err)
					}
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						return fmt.Errorf("failed to upload cache mount: %s", resp.Status)
					}

					bklog.G(ctx).Debugf("synced cache mount remotely %s", cacheMountName)
					return nil
				})
			})
		}
		return eg.Wait()
	}

	return nil
}

func cacheKeyFromMountName(name string) string {
	// Turn the human-readable name into the key we use internally
	// NOTE: this will be problematic if backwards incompatible changes are made
	// to the key format and client<->server are out of sync. That's a general
	// problem though too, so just accepting it for now.
	return core.NewCache(name).Sum()
}

func withCacheMount(ctx context.Context, mountManager *mounts.MountManager, cacheKey string, cb func(ctx context.Context, mnt mount.Mount) error) error {
	// this should never block in theory since we have exclusive access at
	// engine startup, but put a timeout on this out of an abundance of caution
	getRefCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ref, err := mountManager.MountableCache(getRefCtx, &solverpb.Mount{
		CacheOpt: &solverpb.CacheOpt{
			ID:      cacheKey,
			Sharing: solverpb.CacheSharingOpt_SHARED,
		},
	}, nil, nil)
	defer func() {
		if ref != nil {
			ref.Release(context.Background())
		}
	}()
	if err != nil {
		return fmt.Errorf("failed to get cache mount ref: %w", err)
	}

	mountable, err := ref.Mount(ctx, false, nil)
	if err != nil {
		return fmt.Errorf("failed to get cache mount: %w", err)
	}
	mounts, releaseMounts, err := mountable.Mount()
	if err != nil {
		return fmt.Errorf("failed to get cache mount mounts: %w", err)
	}
	defer releaseMounts()
	if len(mounts) != 1 {
		return fmt.Errorf("expected 1 mount, got %d", len(mounts))
	}
	mnt := mounts[0]
	if mnt.Type != "bind" && mnt.Type != "rbind" {
		// TODO: we could support overlay (when there's a parent ref to the cache mount)
		// by just mounting to a tempdir
		return fmt.Errorf("expected bind mount, got %s", mnt.Type)
	}
	return cb(ctx, mnt)
}

func removeAllUnderDir(dir string) error {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read dir %s: %w", dir, err)
	}
	for _, dirent := range dirents {
		if err := os.RemoveAll(filepath.Join(dir, dirent.Name())); err != nil {
			return fmt.Errorf("failed to remove %s: %w", dirent.Name(), err)
		}
	}
	return nil
}
