package filesync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	remotefilesync "github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	fstypes "github.com/dagger/dagger/internal/fsutil/types"
	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/pathutil"
	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
)

type FileSyncer struct {
	cacheManager bkcache.SnapshotManager
}

type FileSyncerOpt struct {
	CacheAccessor bkcache.SnapshotManager
}

func NewFileSyncer(opt FileSyncerOpt) *FileSyncer {
	return &FileSyncer{cacheManager: opt.CacheAccessor}
}

type SnapshotOpts struct {
	IncludePatterns    []string
	ExcludePatterns    []string
	FollowPaths        []string
	GitIgnore          bool
	GitIgnoreOverrides map[string]string
	CacheBuster        string
	RelativePath       string
}

func (ls *FileSyncer) Snapshot(
	ctx context.Context,
	sharedState *MirrorSharedState,
	callerConn *grpc.ClientConn,
	clientPath string,
	opts SnapshotOpts,
) (bkcache.ImmutableRef, digest.Digest, error) {
	if sharedState == nil {
		return nil, "", fmt.Errorf("filesync mirror shared state is nil")
	}
	if callerConn == nil {
		return nil, "", fmt.Errorf("filesync caller conn is nil")
	}
	if opts.RelativePath == "." {
		opts.RelativePath = ""
	}
	ref, dgst, err := ls.snapshot(ctx, sharedState, callerConn, clientPath, opts)
	if err != nil {
		return nil, "", fmt.Errorf("failed to snapshot: %w", err)
	}
	return ref, dgst, nil
}

func (ls *FileSyncer) snapshot(
	ctx context.Context,
	sharedState *MirrorSharedState,
	callerConn *grpc.ClientConn,
	clientPath string,
	opts SnapshotOpts,
) (_ bkcache.ImmutableRef, _ digest.Digest, rerr error) {
	// Encapsulated like the resolver's "pulling" span: hidden unless it
	// fails, surfacing as a labeled progress row only when bytes actually
	// move (an unchanged directory syncs nothing).
	ctx, span := Tracer(ctx).Start(ctx, "uploading "+clientPath,
		telemetry.Encapsulated(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)
	ctx, err := enginetelemetry.WithNetworkRecording(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("create filesync network recorders: %w", err)
	}

	statMsg, err := statClientPath(ctx, callerConn, engine.LocalImportOpts{
		Path:              clientPath,
		StatPathOnly:      true,
		StatReturnAbsPath: true,
		StatResolvePath:   true,
	})
	if err != nil {
		return nil, "", err
	}

	clientPath = filepath.Clean(statMsg.Path)
	drive := pathutil.GetDrive(clientPath)
	if drive != "" {
		clientPath = clientPath[len(drive):]
	}

	finalRef, dgst, err := ls.sync(ctx, sharedState, callerConn, drive, clientPath, opts)
	if err != nil {
		return nil, "", fmt.Errorf("failed to sync: %w", err)
	}
	return finalRef, dgst, nil
}

func (ls *FileSyncer) sync(
	ctx context.Context,
	sharedState *MirrorSharedState,
	callerConn *grpc.ClientConn,
	drive string,
	clientPath string,
	opts SnapshotOpts,
) (_ bkcache.ImmutableRef, _ digest.Digest, rerr error) {
	if err := ls.syncParentDirs(ctx, sharedState, callerConn, clientPath, drive, opts); err != nil {
		return nil, "", fmt.Errorf("failed to sync parent dirs: %w", err)
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(rerr)
	}()

	remote := newRemoteFS(callerConn, drive+clientPath, opts.IncludePatterns, opts.ExcludePatterns, opts.FollowPaths, opts.GitIgnore)
	remote.gitIgnoreOverrides = opts.GitIgnoreOverrides
	local, err := newLocalFS(sharedState, clientPath, opts.IncludePatterns, opts.ExcludePatterns, opts.FollowPaths, opts.RelativePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create local fs: %w", err)
	}
	return local.Sync(ctx, remote, ls.cacheManager, false)
}

// syncParentDirs creates the client path's parent directories in the mirror,
// and the client path itself, matching the client's, so that the client
// path's own sync has somewhere to write. It reads the client's filesystem
// only along that chain of directories, statting each one by its literal
// path. A walk filtered by include patterns would treat a pattern character
// in a parent's name, such as the ^ or [ ] of "a^b/[x]", as a pattern, and
// walk far beyond the chain.
func (ls *FileSyncer) syncParentDirs(
	ctx context.Context,
	sharedState *MirrorSharedState,
	callerConn *grpc.ClientConn,
	clientPath string,
	drive string,
	opts SnapshotOpts,
) (rerr error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(rerr)
	}()

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("parentSync", "y"))

	var chain []string
	for dir := strings.Trim(clientPath, "/"); dir != "" && dir != "."; dir = filepath.Dir(dir) {
		chain = append(chain, dir)
	}
	slices.Reverse(chain)

	stats := make(parentChainFS, len(chain))
	eg, egCtx := errgroup.WithContext(ctx)
	for i, dir := range chain {
		eg.Go(func() error {
			stat, err := statClientPath(egCtx, callerConn, engine.LocalImportOpts{
				Path:         drive + "/" + dir,
				StatPathOnly: true,
			})
			if err != nil {
				return fmt.Errorf("stat %s: %w", dir, err)
			}
			stat.Path = dir
			// as the client does for the stats a directory sync sends
			stat.Uid = 0
			stat.Gid = 0
			stat.Xattrs = nil
			stats[i] = stat
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	local := newParentLocalFS(sharedState, chain, opts.RelativePath)
	if _, _, err := local.Sync(ctx, stats, ls.cacheManager, true); err != nil {
		return fmt.Errorf("failed to sync to local fs: %w", err)
	}
	return nil
}

// parentChainFS is the client's chain of directories down to a client path,
// as syncParentDirs statted them, in walk order.
type parentChainFS []*fstypes.Stat

func (chain parentChainFS) Walk(_ context.Context, _ string, walkFn fs.WalkDirFunc) error {
	for _, stat := range chain {
		if err := walkFn(stat.Path, &StatInfo{stat}, nil); err != nil {
			return err
		}
		if !os.FileMode(stat.Mode).IsDir() {
			// as mirrorChainFS does
			return nil
		}
	}
	return nil
}

func (parentChainFS) ReadFile(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("the parent directory sync reads no files")
}

// statClientPath sends the client a stat request for opts.Path.
func statClientPath(ctx context.Context, callerConn *grpc.ClientConn, opts engine.LocalImportOpts) (*fstypes.Stat, error) {
	diffCopyClient, err := remotefilesync.NewFileSyncClient(callerConn).DiffCopy(opts.AppendToOutgoingContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to create diff copy client: %w", err)
	}
	defer diffCopyClient.CloseSend()
	var stat fstypes.Stat
	if err := diffCopyClient.RecvMsg(&stat); err != nil {
		return nil, fmt.Errorf("failed to receive stat message: %w", err)
	}
	return &stat, nil
}
