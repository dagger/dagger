package local

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/source"
	upstreamlocal "github.com/dagger/dagger/internal/buildkit/source/local"
	srctypes "github.com/dagger/dagger/internal/buildkit/source/types"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/moby/locker"
	fstypes "github.com/tonistiigi/fsutil/types"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source/types"
)

type LocalSource struct {
	cacheManager bkcache.Accessor
	refs         map[string]*filesyncCacheRef
	mu           sync.RWMutex
	perClientMu  *locker.Locker
}

type LocalSourceOpt struct {
	CacheAccessor bkcache.Accessor
}

// TODO: Write a dummy local source to verify that we never use llb.Local anymore
func NewLocalSource(opt LocalSourceOpt) *LocalSource {
	return &LocalSource{
		cacheManager: opt.CacheAccessor,
		refs:         make(map[string]*filesyncCacheRef),
		perClientMu:  locker.New(),
	}
}

type SnapshotSyncOpts struct {
	IncludePatterns []string
	ExcludePatterns []string
	GitIgnore       bool
	CacheBuster     string
}

func (ls *LocalSource) Snapshot(ctx context.Context, session session.Group, sm *session.Manager, clientPath string, opts SnapshotSyncOpts) (bkcache.ImmutableRef, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	for k, v := range attrs {
		switch k {
		case pb.AttrLocalSessionID:
			id.SessionID = v
			if p := strings.SplitN(v, ":", 2); len(p) == 2 {
				id.Name = p[0] + "-" + id.Name
				id.SessionID = p[1]
			}
		case pb.AttrIncludePatterns:
			var patterns []string
			if err := json.Unmarshal([]byte(v), &patterns); err != nil {
				return nil, err
			}
			id.IncludePatterns = patterns
		case pb.AttrExcludePatterns:
			var patterns []string
			if err := json.Unmarshal([]byte(v), &patterns); err != nil {
				return nil, err
			}
			id.ExcludePatterns = patterns
		case pb.AttrFollowPaths:
			var paths []string
			if err := json.Unmarshal([]byte(v), &paths); err != nil {
				return nil, err
			}
			id.FollowPaths = paths
		case pb.AttrSharedKeyHint:
			id.SharedKeyHint = v
		case pb.AttrLocalDiffer:
			switch v {
			case pb.AttrLocalDifferMetadata, "":
				id.Differ = fsutil.DiffMetadata
			case pb.AttrLocalDifferNone:
				id.Differ = fsutil.DiffNone
			}
		}
	}

	return id, nil
}

func (ls *localSource) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager, _ solver.Vertex) (source.SourceInstance, error) {
	localIdentifier, ok := id.(*upstreamlocal.LocalIdentifier)
	if !ok {
		return nil, fmt.Errorf("invalid local identifier %v", id)
	}
	clone := *localIdentifier
	localIdentifier = &clone

	gitIgnore := false
	cachebuster := ""

	excludes := make([]string, 0, len(localIdentifier.ExcludePatterns))
	for _, pattern := range localIdentifier.ExcludePatterns {
		if pattern == "[dagger.gitignore]" {
			gitIgnore = true
		} else if strings.HasPrefix(pattern, "[dagger.cachebuster=") && strings.HasSuffix(pattern, "]") {
			cachebuster = strings.TrimSuffix(strings.TrimPrefix(pattern, "[dagger.cachebuster="), "]")
		} else {
			excludes = append(excludes, pattern)
		}
	}
	localIdentifier.ExcludePatterns = excludes
	return &localSourceHandler{
		src:         *localIdentifier,
		gitIgnore:   gitIgnore,
		cachebuster: cachebuster,
		sm:          sm,
		localSource: ls,
	}, nil
}

type localSourceHandler struct {
	src upstreamlocal.LocalIdentifier
	sm  *session.Manager
	*localSource
	gitIgnore   bool
	cachebuster string
}

func (ls *localSourceHandler) CacheKey(ctx context.Context, g session.Group, index int) (string, string, solver.CacheOpts, bool, error) {
	sessionID := ls.src.SessionID

	if sessionID == "" {
		id := g.SessionIterator().NextSession()
		if id == "" {
			return "", "", nil, false, errors.New("could not access local files without session")
		}
		sessionID = id
	}
	dt, err := json.Marshal(struct {
		SessionID       string
		IncludePatterns []string
		ExcludePatterns []string
		Gitignore       bool
		FollowPaths     []string
		Cachebuster     string
	}{
		SessionID:       sessionID,
		IncludePatterns: ls.src.IncludePatterns,
		ExcludePatterns: ls.src.ExcludePatterns,
		Gitignore:       ls.gitIgnore,
		FollowPaths:     ls.src.FollowPaths,
		Cachebuster:     ls.cachebuster,
	})
	if err != nil {
		return "", "", nil, false, err
	}
	digestString := digest.FromBytes(dt).String()
	return ls.src.Name + ":" + digestString, digestString, nil, true, nil
}

func (ls *localSourceHandler) Snapshot(ctx context.Context, g session.Group) (bkcache.ImmutableRef, error) {
	sessionID := ls.src.SessionID
	if sessionID == "" {
		// should only happen in Dockerfile cases
		return ls.snapshotWithAnySession(ctx, g)
	}

	timeoutCtx, cancel := context.WithCancelCause(ctx)
	timeoutCtx, _ = context.WithTimeoutCause(timeoutCtx, 5*time.Second, fmt.Errorf("timeout: %w", context.DeadlineExceeded))
	defer cancel(context.Canceled)

	caller, err := sm.Get(timeoutCtx, clientMetadata.ClientID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	ref, err := ls.snapshot(ctx, session, caller, clientPath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to snapshot: %w", err)
	}

	return ref, nil
}

func (ls *LocalSource) snapshot(ctx context.Context, session session.Group, caller session.Caller, clientPath string, opts SnapshotSyncOpts) (_ bkcache.ImmutableRef, rerr error) {
	ctx, span := newSpan(ctx, "filesync")
	defer telemetry.End(span, func() error { return rerr })

	// We need the full abs path since the cache ref we sync into holds every dir from this client's root.
	// We also need to evaluate all symlinks so we only create the actual parent dirs and not any symlinks as dirs.
	// Additionally, we need to see if this is a Windows client and thus needs drive handling
	statCtx := engine.LocalImportOpts{
		Path:              clientPath,
		StatPathOnly:      true,
		StatReturnAbsPath: true,
		StatResolvePath:   true,
	}.AppendToOutgoingContext(ctx)
	diffCopyClient, err := filesync.NewFileSyncClient(caller.Conn()).DiffCopy(statCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create diff copy client: %w", err)
	}

	var statMsg fstypes.Stat
	if err := diffCopyClient.RecvMsg(&statMsg); err != nil {
		diffCopyClient.CloseSend()
		return nil, fmt.Errorf("failed to receive stat message: %w", err)
	}
	diffCopyClient.CloseSend()

	clientPath = filepath.Clean(statMsg.Path)
	drive := pathutil.GetDrive(clientPath)
	if drive != "" {
		clientPath = clientPath[len(drive):]
	}

	ref, release, err := ls.getRef(ctx, session, drive)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := release(ctx); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to release ref: %w", err))
		}
	}()

	finalRef, err := ls.sync(ctx, ref, session, caller, drive, clientPath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to sync: %w", err)
	}

	return finalRef, nil
}

func (ls *LocalSource) sync(
	ctx context.Context,
	ref *filesyncCacheRef,
	session session.Group,
	caller session.Caller,
	drive string,
	clientPath string,
	opts SnapshotSyncOpts,
) (_ bkcache.ImmutableRef, rerr error) {
	// first ensure that all the parent dirs under the client's rootfs (above the given clientPath) are synced in correctly
	if err := ls.syncParentDirs(ctx, ref, caller, clientPath, drive); err != nil {
		return nil, fmt.Errorf("failed to sync parent dirs: %w", err)
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(rerr)
	}()

	// now sync in the clientPath dir
	remote := newRemoteFS(caller, drive+clientPath, opts.IncludePatterns, opts.ExcludePatterns, opts.GitIgnore)
	local, err := newLocalFS(ref.sharedState, clientPath, opts.IncludePatterns, opts.ExcludePatterns, opts.GitIgnore)
	if err != nil {
		return nil, fmt.Errorf("failed to create local fs: %w", err)
	}

	return local.Sync(ctx, remote, ls.cacheManager, session, false)
}

func (ls *LocalSource) syncParentDirs(
	ctx context.Context,
	ref *filesyncCacheRef,
	caller session.Caller,
	clientPath string,
	drive string,
) (rerr error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(rerr)
	}()

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("parentSync", "y"),
	)

	// include the parent dirs, and all the gitignores
	// the gitignores are needed to ensure that the local gitignore state matches the remote gitignore state
	// TODO: the client side implementation of all this isn't incredibly efficient, it stats every dirent under the
	// the root rather than just sending us the stats of the parent dirs. Not a huge bottleneck most likely.
	include := strings.TrimPrefix(strings.TrimSuffix(clientPath, "/"), "/")
	includes := []string{include}
	excludes := []string{include + "/*"}
	if ls.gitIgnore {
		excludes = append(excludes, "!"+include+"/**/.gitignore", include+"/**/.git")
	}

	root := "/"
	if drive != "" {
		root = drive + "/"
	}

	remote := newRemoteFS(caller, root, includes, excludes, false)
	local, err := newLocalFS(ref.sharedState, "/", includes, excludes, false)
	if err != nil {
		return fmt.Errorf("failed to create local fs: %w", err)
	}
	_, err = local.Sync(ctx, remote, ls.cacheManager, nil, true)
	if err != nil {
		return fmt.Errorf("failed to sync to local fs: %w", err)
	}

	return nil
}

type filesyncCacheRef struct {
	mutRef bkcache.MutableRef

	mounter snapshot.Mounter
	mntPath string

	sharedState *localFSSharedState

	usageCount int
}

func (ls *LocalSource) getRef(
	ctx context.Context,
	session session.Group,
	drive string, // only set for windows clients, otherwise ""
) (_ *filesyncCacheRef, _ func(context.Context) error, rerr error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get requester session ID: %w", err)
	}

	clientKey := clientMetadata.ClientStableID
	if clientKey == "" {
		slog.WarnContext(ctx, "client stable ID not set, using random value")
		clientKey = identity.NewID()
	}
	if drive != "" {
		clientKey = drive + clientKey
	}

	ls.perClientMu.Lock(clientKey)
	defer ls.perClientMu.Unlock(clientKey)

	ls.mu.RLock()
	ref, ok := ls.refs[clientKey]
	ls.mu.RUnlock()
	if !ok {
		ref = &filesyncCacheRef{}
		defer func() {
			if rerr != nil {
				if ref.mounter != nil && ref.mntPath != "" {
					ref.mounter.Unmount()
				}
				if ref.mutRef != nil {
					ref.mutRef.Release(context.WithoutCancel(ctx))
				}
			}
		}()

		sis, err := searchSharedKey(ctx, ls.cacheManager, clientKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to search shared key: %w", err)
		}
		for _, si := range sis {
			if m, err := ls.cacheManager.GetMutable(ctx, si.ID()); err == nil {
				bklog.G(ctx).Debugf("reusing ref for local: %s (%q)", m.ID(), clientKey)
				ref.mutRef = m
				break
			} else {
				bklog.G(ctx).Debugf("not reusing ref %s for local: %v (%q)", si.ID(), err, clientKey)
			}
		}

		if ref.mutRef == nil {
			ref.mutRef, err = ls.cacheManager.New(ctx, nil, nil,
				bkcache.CachePolicyRetain,
				bkcache.WithRecordType(bkclient.UsageRecordTypeLocalSource),
				bkcache.WithDescription(fmt.Sprintf("local source for %s", clientKey)),
			)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create new mutable ref: %w", err)
			}
			if err := (CacheRefMetadata{ref.mutRef}).setSharedKey(clientKey); err != nil {
				return nil, nil, fmt.Errorf("failed to set shared key: %w", err)
			}
			bklog.G(ctx).Debugf("new ref for local: %s (%q)", ref.mutRef.ID(), clientKey)
		}

		mntable, err := ref.mutRef.Mount(ctx, false, session)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get mountable: %w", err)
		}
		ref.mounter = snapshot.LocalMounter(mntable)

		ref.mntPath, err = ref.mounter.Mount()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to mount: %w", err)
		}

		ref.sharedState = &localFSSharedState{
			rootPath:    ref.mntPath,
			changeCache: cache.NewCache[string, *ChangeWithStat](),
		}

		ls.mu.Lock()
		ls.refs[clientKey] = ref
		ls.mu.Unlock()
	}
	ref.usageCount++

	return ref, func(ctx context.Context) (rerr error) {
		ls.perClientMu.Lock(clientKey)
		defer ls.perClientMu.Unlock(clientKey)
		ref.usageCount--
		if ref.usageCount > 0 {
			return nil
		}

		ls.mu.Lock()
		delete(ls.refs, clientKey)
		ls.mu.Unlock()

		ctx = context.WithoutCancel(ctx)
		if err := ref.mounter.Unmount(); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to unmount: %w", err))
		}
		if err := ref.mutRef.Release(ctx); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to release ref: %w", err))
		}
		return rerr
	}, nil
}

func curTracer(ctx context.Context) trace.Tracer {
	return trace.SpanFromContext(ctx).TracerProvider().Tracer("dagger.io/filesync")
}

func newSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	tr := curTracer(ctx)
	return tr.Start(ctx, name)
}

const (
	keySharedKey   = "local.sharedKey"
	sharedKeyIndex = keySharedKey + ":"
)

func searchSharedKey(ctx context.Context, store bkcache.MetadataStore, k string) ([]CacheRefMetadata, error) {
	var results []CacheRefMetadata
	mds, err := store.Search(ctx, sharedKeyIndex+k, false)
	if err != nil {
		return nil, err
	}
	for _, md := range mds {
		results = append(results, CacheRefMetadata{md})
	}
	return results, nil
}

type CacheRefMetadata struct {
	bkcache.RefMetadata
}

func (md CacheRefMetadata) setSharedKey(key string) error {
	return md.SetString(keySharedKey, key, sharedKeyIndex+key)
}

type DummySource struct {}

func NewDummySource() (source.Source) {
	return &DummySource{}
}

func (*DummySource) Schemes() []string{
	return []string{srctypes.LocalScheme}
}

func (*DummySource) Identifier(scheme, ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	panic("dummy local source has been called")
}

func (*DummySource) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager, _ solver.Vertex) (source.SourceInstance, error) {
	panic("dummy local source has been called")
}

// type Opt struct {
// 	CacheAccessor bkcache.Accessor
// }
//
// // check how we get global cache and do the same plumbing for local
// // core.LocalSource().Snaptshot(xxxx)
// func NewSource(opt Opt) (source.Source, error) {
// 	ls := &localSource{
// 		cm:          opt.CacheAccessor,
// 		refs:        make(map[string]*filesyncCacheRef),
// 		perClientMu: locker.New(),
// 	}
// 	return ls, nil
// }
//
// type localSource struct {
// 	cm bkcache.Accessor
//
// 	refs        map[string]*filesyncCacheRef
// 	mu          sync.RWMutex
// 	perClientMu *locker.Locker // Very important -> global for multi-client sync
// }
//
// func (ls *localSource) Schemes() []string {
// 	return []string{srctypes.LocalScheme}
// }
//
// // Note: The LLB we put in schema/host.directory in retrieved there:
// // attrs contains all the llb.SessionID, sharedKeyInt, Include, Exclude...
// //
// // Question: how to instead of calling LLB.Local, I can create a localSource?
// // This seems like it can then be passed to snapshot in the
// // and then in sync newRemoteFs??
// func (ls *localSource) Identifier(scheme, ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
// 	id, err := upstreamlocal.NewLocalIdentifier(ref)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	for k, v := range attrs {
// 		switch k {
// 		case pb.AttrLocalSessionID:
// 			id.SessionID = v
// 			if p := strings.SplitN(v, ":", 2); len(p) == 2 {
// 				id.Name = p[0] + "-" + id.Name
// 				id.SessionID = p[1]
// 			}
// 		case pb.AttrIncludePatterns:
// 			var patterns []string
// 			if err := json.Unmarshal([]byte(v), &patterns); err != nil {
// 				return nil, err
// 			}
// 			id.IncludePatterns = patterns
// 		case pb.AttrExcludePatterns:
// 			var patterns []string
// 			if err := json.Unmarshal([]byte(v), &patterns); err != nil {
// 				return nil, err
// 			}
// 			id.ExcludePatterns = patterns
// 		case pb.AttrFollowPaths:
// 			var paths []string
// 			if err := json.Unmarshal([]byte(v), &paths); err != nil {
// 				return nil, err
// 			}
// 			id.FollowPaths = paths
// 		case pb.AttrSharedKeyHint:
// 			id.SharedKeyHint = v
// 		case pb.AttrLocalDiffer:
// 			switch v {
// 			case pb.AttrLocalDifferMetadata, "":
// 				id.Differ = fsutil.DiffMetadata
// 			case pb.AttrLocalDifferNone:
// 				id.Differ = fsutil.DiffNone
// 			}
// 		}
// 	}
//
// 	return id, nil
// }
//
// func (ls *localSource) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager, _ solver.Vertex) (source.SourceInstance, error) {
// 	localIdentifier, ok := id.(*upstreamlocal.LocalIdentifier)
// 	if !ok {
// 		return nil, fmt.Errorf("invalid local identifier %v", id)
// 	}
// 	clone := *localIdentifier
// 	localIdentifier = &clone
//
// 	gitIgnore := false
// 	cachebuster := ""
//
// 	excludes := make([]string, 0, len(localIdentifier.ExcludePatterns))
// 	for _, pattern := range localIdentifier.ExcludePatterns {
// 		if pattern == "[dagger.gitignore]" {
// 			gitIgnore = true
// 		} else if strings.HasPrefix(pattern, "[dagger.cachebuster=") && strings.HasSuffix(pattern, "]") {
// 			cachebuster = strings.TrimSuffix(strings.TrimPrefix(pattern, "[dagger.cachebuster="), "]")
// 		} else {
// 			excludes = append(excludes, pattern)
// 		}
// 	}
// 	localIdentifier.ExcludePatterns = excludes
// 	return &localSourceHandler{
// 		src:         *localIdentifier,
// 		gitIgnore:   gitIgnore,
// 		cachebuster: cachebuster,
// 		sm:          sm,
// 		localSource: ls,
// 	}, nil
// }
//
// type localSourceHandler struct {
// 	src upstreamlocal.LocalIdentifier
// 	sm  *session.Manager
// 	*localSource
// 	gitIgnore   bool
// 	cachebuster string
// }
//
// // Can ignore it for now
// func (ls *localSourceHandler) CacheKey(ctx context.Context, g session.Group, index int) (string, string, solver.CacheOpts, bool, error) {
// 	sessionID := ls.src.SessionID
//
// 	if sessionID == "" {
// 		id := g.SessionIterator().NextSession()
// 		if id == "" {
// 			return "", "", nil, false, errors.New("could not access local files without session")
// 		}
// 		sessionID = id
// 	}
// 	dt, err := json.Marshal(struct {
// 		SessionID       string
// 		IncludePatterns []string
// 		ExcludePatterns []string
// 		Gitignore       bool
// 		FollowPaths     []string
// 		Cachebuster     string
// 	}{
// 		SessionID:       sessionID,
// 		IncludePatterns: ls.src.IncludePatterns,
// 		ExcludePatterns: ls.src.ExcludePatterns,
// 		Gitignore:       ls.gitIgnore,
// 		FollowPaths:     ls.src.FollowPaths,
// 		Cachebuster:     ls.cachebuster,
// 	})
// 	if err != nil {
// 		return "", "", nil, false, err
// 	}
// 	digestString := digest.FromBytes(dt).String()
// 	return "session:" + ls.src.Name + ":" + digestString, digestString, nil, true, nil
// }
//
// // Note: I can rearrange all that part the way I feel like it.
// // Snapshot has now SnapshotOpts with path, filter and whatever
// func (ls *localSourceHandler) Snapshot(ctx context.Context, g session.Group) (bkcache.ImmutableRef, error) {
// 	sessionID := ls.src.SessionID
// 	if sessionID == "" {
// 		// should only happen in Dockerfile cases
// 		return ls.snapshotWithAnySession(ctx, g)
// 	}
//
// 	timeoutCtx, cancel := context.WithCancelCause(ctx)
// 	timeoutCtx, _ = context.WithTimeoutCause(timeoutCtx, 5*time.Second, fmt.Errorf("timeout: %w", context.DeadlineExceeded))
// 	defer cancel(context.Canceled)
//
// 	caller, err := ls.sm.Get(timeoutCtx, sessionID, false)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get session: %w", err)
// 	}
//
// 	ref, err := ls.snapshot(ctx, g, caller)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get snapshot: %w", err)
// 	}
// 	return ref, nil
// }
//
// func (ls *localSourceHandler) snapshotWithAnySession(ctx context.Context, g session.Group) (bkcache.ImmutableRef, error) {
// 	var ref bkcache.ImmutableRef
// 	err := ls.sm.Any(ctx, g, func(ctx context.Context, _ string, c session.Caller) error {
// 		r, err := ls.snapshot(ctx, g, c)
// 		if err != nil {
// 			return err
// 		}
// 		ref = r
// 		return nil
// 	})
// 	return ref, err
// }
//

//
// func (ls *localSourceHandler) snapshot(ctx context.Context, session session.Group, caller session.Caller) (_ bkcache.ImmutableRef, rerr error) {
// 	ctx, span := newSpan(ctx, "filesync")
// 	defer telemetry.End(span, func() error { return rerr })
//
// 	clientPath := ls.src.Name
//
// 	// We need the full abs path since the cache ref we sync into holds every dir from this client's root.
// 	// We also need to evaluate all symlinks so we only create the actual parent dirs and not any symlinks as dirs.
// 	// Additionally, we need to see if this is a Windows client and thus needs drive handling
// 	statCtx := engine.LocalImportOpts{
// 		Path:              clientPath,
// 		StatPathOnly:      true,
// 		StatReturnAbsPath: true,
// 		StatResolvePath:   true,
// 	}.AppendToOutgoingContext(ctx)
// 	diffCopyClient, err := filesync.NewFileSyncClient(caller.Conn()).DiffCopy(statCtx)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create diff copy client: %w", err)
// 	}
// 	var statMsg fstypes.Stat
// 	if err := diffCopyClient.RecvMsg(&statMsg); err != nil {
// 		diffCopyClient.CloseSend()
// 		return nil, fmt.Errorf("failed to receive stat message: %w", err)
// 	}
// 	diffCopyClient.CloseSend()
// 	clientPath = filepath.Clean(statMsg.Path)
// 	drive := pathutil.GetDrive(clientPath)
// 	if drive != "" {
// 		clientPath = clientPath[len(drive):]
// 	}
//
// 	ref, release, err := ls.getRef(ctx, session, drive)
// 	if err != nil {
// 		return nil, err
// 	}
// 	defer func() {
// 		if err := release(ctx); err != nil {
// 			rerr = errors.Join(rerr, fmt.Errorf("failed to release ref: %w", err))
// 		}
// 	}()
//
// 	finalRef, err := ls.sync(ctx, ref, clientPath, drive, session, caller)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to sync: %w", err)
// 	}
//
// 	return finalRef, nil
// }
//
// func (ls *localSourceHandler) sync(
// 	ctx context.Context,
// 	ref *filesyncCacheRef,
// 	clientPath string,
// 	drive string, // only set for windows clients, otherwise ""
// 	session session.Group,
// 	caller session.Caller,
// ) (_ bkcache.ImmutableRef, rerr error) {
// 	// first ensure that all the parent dirs under the client's rootfs (above the given clientPath) are synced in correctly
// 	if err := ls.syncParentDirs(ctx, ref, clientPath, drive, caller); err != nil {
// 		return nil, fmt.Errorf("failed to sync parent dirs: %w", err)
// 	}
//
// 	ctx, cancel := context.WithCancelCause(ctx)
// 	defer func() {
// 		cancel(rerr)
// 	}()
//
// 	// now sync in the clientPath dir
// 	remote := newRemoteFS(caller, drive+clientPath, ls.src.IncludePatterns, ls.src.ExcludePatterns, ls.gitIgnore)
// 	local, err := newLocalFS(ref.sharedState, clientPath, ls.src.IncludePatterns, ls.src.ExcludePatterns, ls.gitIgnore)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create local fs: %w", err)
// 	}
// 	return local.Sync(ctx, remote, ls.cm, session, false)
// }
//
// func (ls *localSourceHandler) syncParentDirs(
// 	ctx context.Context,
// 	ref *filesyncCacheRef,
// 	clientPath string,
// 	drive string, // only set for windows clients, otherwise ""
// 	caller session.Caller,
// ) (rerr error) {
// 	ctx, cancel := context.WithCancelCause(ctx)
// 	defer func() {
// 		cancel(rerr)
// 	}()
//
// 	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
// 		WithField("parentSync", "y"),
// 	)
//
// 	// include *just* the parent dirs, nothing else
// 	// TODO: the client side implementation of all this isn't incredibly efficient, it stats every dirent under the
// 	// the root rather than just sending us the stats of the parent dirs. Not a huge bottleneck most likely.
// 	include := strings.TrimPrefix(strings.TrimSuffix(clientPath, "/"), "/")
// 	includes := []string{include}
// 	exclude := include + "/*"
// 	excludes := []string{exclude}
//
// 	root := "/"
// 	if drive != "" {
// 		root = drive + "/"
// 	}
// 	remote := newRemoteFS(caller, root, includes, excludes, false)
//
// 	local, err := newLocalFS(ref.sharedState, "/", includes, excludes, false)
// 	if err != nil {
// 		return fmt.Errorf("failed to create local fs: %w", err)
// 	}
// 	_, err = local.Sync(ctx, remote, ls.cm, nil, true)
// 	if err != nil {
// 		return fmt.Errorf("failed to sync to local fs: %w", err)
// 	}
//
// 	return nil
// }

// type filesyncCacheRef struct {
// 	mutRef bkcache.MutableRef
//
// 	mounter snapshot.Mounter
// 	mntPath string
//
// 	sharedState *localFSSharedState
//
// 	usageCount int
// }

// get the cache ref for the client, loading it if not already
// func (ls *localSourceHandler) getRef(
// 	ctx context.Context,
// 	session session.Group,
// 	drive string, // only set for windows clients, otherwise ""
// ) (_ *filesyncCacheRef, _ func(context.Context) error, rerr error) {
// 	clientKey := ls.src.SharedKeyHint // this is the clientMetadata.ClientStableID
// 	if drive != "" {
// 		clientKey = drive + clientKey
// 	}
// 	ls.perClientMu.Lock(clientKey)
// 	defer ls.perClientMu.Unlock(clientKey)
//
// 	ls.mu.RLock()
// 	ref, ok := ls.refs[clientKey]
// 	ls.mu.RUnlock()
// 	if !ok {
// 		ref = &filesyncCacheRef{}
// 		defer func() {
// 			if rerr != nil {
// 				if ref.mounter != nil && ref.mntPath != "" {
// 					ref.mounter.Unmount()
// 				}
// 				if ref.mutRef != nil {
// 					ref.mutRef.Release(context.WithoutCancel(ctx))
// 				}
// 			}
// 		}()
//
// 		sis, err := searchSharedKey(ctx, ls.cm, clientKey)
// 		if err != nil {
// 			return nil, nil, fmt.Errorf("failed to search shared key: %w", err)
// 		}
// 		for _, si := range sis {
// 			if m, err := ls.cm.GetMutable(ctx, si.ID()); err == nil {
// 				bklog.G(ctx).Debugf("reusing ref for local: %s (%q)", m.ID(), clientKey)
// 				ref.mutRef = m
// 				break
// 			} else {
// 				bklog.G(ctx).Debugf("not reusing ref %s for local: %v (%q)", si.ID(), err, clientKey)
// 			}
// 		}
//
// 		if ref.mutRef == nil {
// 			ref.mutRef, err = ls.cm.New(ctx, nil, nil,
// 				bkcache.CachePolicyRetain,
// 				bkcache.WithRecordType(bkclient.UsageRecordTypeLocalSource),
// 				bkcache.WithDescription(fmt.Sprintf("local source for %s", ls.src.SharedKeyHint)),
// 			)
// 			if err != nil {
// 				return nil, nil, fmt.Errorf("failed to create new mutable ref: %w", err)
// 			}
// 			if err := (CacheRefMetadata{ref.mutRef}).setSharedKey(clientKey); err != nil {
// 				return nil, nil, fmt.Errorf("failed to set shared key: %w", err)
// 			}
// 			bklog.G(ctx).Debugf("new ref for local: %s (%q)", ref.mutRef.ID(), clientKey)
// 		}
//
// 		mntable, err := ref.mutRef.Mount(ctx, false, session)
// 		if err != nil {
// 			return nil, nil, fmt.Errorf("failed to get mountable: %w", err)
// 		}
// 		ref.mounter = snapshot.LocalMounter(mntable)
//
// 		ref.mntPath, err = ref.mounter.Mount()
// 		if err != nil {
// 			return nil, nil, fmt.Errorf("failed to mount: %w", err)
// 		}
//
// 		ref.sharedState = &localFSSharedState{
// 			rootPath:    ref.mntPath,
// 			changeCache: cache.NewCache[string, *ChangeWithStat](),
// 		}
//
// 		ls.mu.Lock()
// 		ls.refs[clientKey] = ref
// 		ls.mu.Unlock()
// 	}
// 	ref.usageCount++
//
// 	return ref, func(ctx context.Context) (rerr error) {
// 		ls.perClientMu.Lock(clientKey)
// 		defer ls.perClientMu.Unlock(clientKey)
// 		ref.usageCount--
// 		if ref.usageCount > 0 {
// 			return nil
// 		}
//
// 		ls.mu.Lock()
// 		delete(ls.refs, clientKey)
// 		ls.mu.Unlock()
//
// 		ctx = context.WithoutCancel(ctx)
// 		if err := ref.mounter.Unmount(); err != nil {
// 			rerr = errors.Join(rerr, fmt.Errorf("failed to unmount: %w", err))
// 		}
// 		if err := ref.mutRef.Release(ctx); err != nil {
// 			rerr = errors.Join(rerr, fmt.Errorf("failed to release ref: %w", err))
// 		}
// 		return rerr
// 	}, nil
// }
