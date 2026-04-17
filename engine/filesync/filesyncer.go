package filesync

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	remotefilesync "github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	fstypes "github.com/dagger/dagger/internal/fsutil/types"
	"github.com/opencontainers/go-digest"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/pathutil"
	telemetry "github.com/dagger/otel-go"
)

type FileSyncer struct {
	cacheManager bkcache.Accessor
}

type FileSyncerOpt struct {
	CacheAccessor bkcache.Accessor
}

func NewFileSyncer(opt FileSyncerOpt) *FileSyncer {
	return &FileSyncer{cacheManager: opt.CacheAccessor}
}

type SnapshotOpts struct {
	IncludePatterns []string
	ExcludePatterns []string
	FollowPaths     []string
	GitIgnore       bool
	CacheBuster     string
	RelativePath    string
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
	ctx, span := Tracer(ctx).Start(ctx, "filesync")
	defer telemetry.EndWithCause(span, &rerr)

	statCtx := engine.LocalImportOpts{
		Path:              clientPath,
		StatPathOnly:      true,
		StatReturnAbsPath: true,
		StatResolvePath:   true,
	}.AppendToOutgoingContext(ctx)
	diffCopyClient, err := remotefilesync.NewFileSyncClient(callerConn).DiffCopy(statCtx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create diff copy client: %w", err)
	}

	var statMsg fstypes.Stat
	if err := diffCopyClient.RecvMsg(&statMsg); err != nil {
		diffCopyClient.CloseSend()
		return nil, "", fmt.Errorf("failed to receive stat message: %w", err)
	}
	diffCopyClient.CloseSend()

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
	local, err := newLocalFS(sharedState, clientPath, opts.IncludePatterns, opts.ExcludePatterns, opts.FollowPaths, opts.RelativePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create local fs: %w", err)
	}
	return local.Sync(ctx, remote, ls.cacheManager, false)
}

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

	include := strings.TrimPrefix(strings.TrimSuffix(clientPath, "/"), "/")
	includes := []string{include}
	excludes := []string{include + "/*"}
	root := "/"
	if drive != "" {
		root = drive + "/"
	}

	remote := newRemoteFS(callerConn, root, includes, excludes, nil, false)
	local, err := newLocalFS(sharedState, "/", includes, excludes, nil, opts.RelativePath)
	if err != nil {
		return fmt.Errorf("failed to create local fs: %w", err)
	}
	_, _, err = local.Sync(ctx, remote, ls.cacheManager, true)
	if err != nil {
		return fmt.Errorf("failed to sync to local fs: %w", err)
	}
	return nil
}
