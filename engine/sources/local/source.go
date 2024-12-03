package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	upstreamlocal "github.com/moby/buildkit/source/local"
	srctypes "github.com/moby/buildkit/source/types"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/locker"
	digest "github.com/opencontainers/go-digest"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
)

type Opt struct {
	CacheAccessor cache.Accessor
}

func NewSource(opt Opt) (source.Source, error) {
	ls := &localSource{
		cm:          opt.CacheAccessor,
		refs:        make(map[string]*filesyncCacheRef),
		perClientMu: locker.New(),
	}
	return ls, nil
}

type localSource struct {
	cm cache.Accessor

	refs        map[string]*filesyncCacheRef
	mu          sync.RWMutex
	perClientMu *locker.Locker
}

func (ls *localSource) Schemes() []string {
	return []string{srctypes.LocalScheme}
}

func (ls *localSource) Identifier(scheme, ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	id, err := upstreamlocal.NewLocalIdentifier(ref)
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

	return &localSourceHandler{
		src:         *localIdentifier,
		sm:          sm,
		localSource: ls,
	}, nil
}

type localSourceHandler struct {
	src upstreamlocal.LocalIdentifier
	sm  *session.Manager
	*localSource
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
		FollowPaths     []string
	}{SessionID: sessionID, IncludePatterns: ls.src.IncludePatterns, ExcludePatterns: ls.src.ExcludePatterns, FollowPaths: ls.src.FollowPaths})
	if err != nil {
		return "", "", nil, false, err
	}
	return "session:" + ls.src.Name + ":" + digest.FromBytes(dt).String(), digest.FromBytes(dt).String(), nil, true, nil
}

func (ls *localSourceHandler) Snapshot(ctx context.Context, g session.Group) (cache.ImmutableRef, error) {
	sessionID := ls.src.SessionID
	if sessionID == "" {
		// should only happen in Dockerfile cases
		return ls.snapshotWithAnySession(ctx, g)
	}

	timeoutCtx, cancel := context.WithCancelCause(ctx)
	timeoutCtx, _ = context.WithTimeoutCause(timeoutCtx, 5*time.Second, fmt.Errorf("timeout: %w", context.DeadlineExceeded))
	defer cancel(context.Canceled)

	caller, err := ls.sm.Get(timeoutCtx, sessionID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	ref, err := ls.snapshot(ctx, g, caller)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot: %w", err)
	}
	return ref, nil
}

func (ls *localSourceHandler) snapshotWithAnySession(ctx context.Context, g session.Group) (cache.ImmutableRef, error) {
	var ref cache.ImmutableRef
	err := ls.sm.Any(ctx, g, func(ctx context.Context, _ string, c session.Caller) error {
		r, err := ls.snapshot(ctx, g, c)
		if err != nil {
			return err
		}
		ref = r
		return nil
	})
	return ref, err
}

func curTracer(ctx context.Context) trace.Tracer {
	return trace.SpanFromContext(ctx).TracerProvider().Tracer("dagger.io/filesync")
}

func newSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	tr := curTracer(ctx)
	return tr.Start(ctx, name)
}

func (ls *localSourceHandler) snapshot(ctx context.Context, session session.Group, caller session.Caller) (_ cache.ImmutableRef, rerr error) {
	ctx, span := newSpan(ctx, "filesync")
	defer telemetry.End(span, func() error { return rerr })

	clientPath := ls.src.Name

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
	drive := client.GetDrive(clientPath)
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

	finalRef, err := ls.sync(ctx, ref, clientPath, drive, session, caller)
	if err != nil {
		return nil, fmt.Errorf("failed to sync: %w", err)
	}

	return finalRef, nil
}

func (ls *localSourceHandler) sync(
	ctx context.Context,
	ref *filesyncCacheRef,
	clientPath string,
	drive string, // only set for windows clients, otherwise ""
	session session.Group,
	caller session.Caller,
) (_ cache.ImmutableRef, rerr error) {
	// first ensure that all the parent dirs under the client's rootfs (above the given clientPath) are synced in correctly
	if err := ls.syncParentDirs(ctx, ref, clientPath, drive, caller); err != nil {
		return nil, fmt.Errorf("failed to sync parent dirs: %w", err)
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(rerr)
	}()

	// now sync in the clientPath dir
	remote := newRemoteFS(caller, drive+clientPath, ls.src.IncludePatterns, ls.src.ExcludePatterns)
	local, err := newLocalFS(ref.sharedState, clientPath, ls.src.IncludePatterns, ls.src.ExcludePatterns)
	if err != nil {
		return nil, fmt.Errorf("failed to create local fs: %w", err)
	}
	return local.Sync(ctx, remote, ls.cm, session, false)
}

func (ls *localSourceHandler) syncParentDirs(
	ctx context.Context,
	ref *filesyncCacheRef,
	clientPath string,
	drive string, // only set for windows clients, otherwise ""
	caller session.Caller,
) (rerr error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(rerr)
	}()

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("parentSync", "y"),
	)

	// include *just* the parent dirs, nothing else
	// TODO: the client side implementation of all this isn't incredibly efficient, it stats every dirent under the
	// the root rather than just sending us the stats of the parent dirs. Not a huge bottleneck most likely.
	include := strings.TrimPrefix(strings.TrimSuffix(clientPath, "/"), "/")
	includes := []string{include}
	exclude := include + "/*"
	excludes := []string{exclude}

	root := "/"
	if drive != "" {
		root = drive + "/"
	}
	remote := newRemoteFS(caller, root, includes, excludes)

	local, err := newLocalFS(ref.sharedState, "/", includes, excludes)
	if err != nil {
		return fmt.Errorf("failed to create local fs: %w", err)
	}
	_, err = local.Sync(ctx, remote, ls.cm, nil, true)
	if err != nil {
		return fmt.Errorf("failed to sync to local fs: %w", err)
	}

	return nil
}

type filesyncCacheRef struct {
	mutRef cache.MutableRef

	mounter snapshot.Mounter
	mntPath string

	sharedState *localFSSharedState

	usageCount int
}

// get the cache ref for the client, loading it if not already
func (ls *localSourceHandler) getRef(
	ctx context.Context,
	session session.Group,
	drive string, // only set for windows clients, otherwise ""
) (_ *filesyncCacheRef, _ func(context.Context) error, rerr error) {
	clientKey := ls.src.SharedKeyHint // this is the clientMetadata.ClientStableID
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

		sis, err := searchSharedKey(ctx, ls.cm, clientKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to search shared key: %w", err)
		}
		for _, si := range sis {
			if m, err := ls.cm.GetMutable(ctx, si.ID()); err == nil {
				bklog.G(ctx).Debugf("reusing ref for local: %s (%q)", m.ID(), clientKey)
				ref.mutRef = m
				break
			} else {
				bklog.G(ctx).Debugf("not reusing ref %s for local: %v (%q)", si.ID(), err, clientKey)
			}
		}

		if ref.mutRef == nil {
			ref.mutRef, err = ls.cm.New(ctx, nil, nil,
				cache.CachePolicyRetain,
				cache.WithRecordType(bkclient.UsageRecordTypeLocalSource),
				cache.WithDescription(fmt.Sprintf("local source for %s", ls.src.SharedKeyHint)),
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
			rootPath: ref.mntPath,
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

const (
	keySharedKey   = "local.sharedKey"
	sharedKeyIndex = keySharedKey + ":"
)

func searchSharedKey(ctx context.Context, store cache.MetadataStore, k string) ([]CacheRefMetadata, error) {
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
	cache.RefMetadata
}

func (md CacheRefMetadata) setSharedKey(key string) error {
	return md.SetString(keySharedKey, key, sharedKeyIndex+key)
}
