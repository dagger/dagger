package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/cache/contenthash"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	srctypes "github.com/moby/buildkit/source/types"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/progress"
	"github.com/moby/locker"
	digest "github.com/opencontainers/go-digest"
	"github.com/tonistiigi/fsutil"
	fscopy "github.com/tonistiigi/fsutil/copy"
	fstypes "github.com/tonistiigi/fsutil/types"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"

	"github.com/dagger/dagger/engine"
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
	id, err := NewLocalIdentifier(ref)
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
	localIdentifier, ok := id.(*LocalIdentifier)
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
	src LocalIdentifier
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
		return ls.snapshotWithAnySession(ctx, g)
	}

	timeoutCtx, cancel := context.WithCancelCause(ctx)
	timeoutCtx, _ = context.WithTimeoutCause(timeoutCtx, 5*time.Second, fmt.Errorf("timeout: %w", context.DeadlineExceeded))
	defer cancel(context.Canceled)

	caller, err := ls.sm.Get(timeoutCtx, sessionID, false)
	if err != nil {
		return ls.snapshotWithAnySession(ctx, g)
	}

	ref, err := ls.snapshot(ctx, g, caller)
	if err != nil {
		var serr filesync.InvalidSessionError
		if errors.As(err, &serr) {
			return ls.snapshotWithAnySession(ctx, g)
		}
		return nil, err
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

// TODO: cleanup trace/span stuff
// TODO: ^ including putting all the span ends in defers everywhere
func curTracer(ctx context.Context) trace.Tracer {
	return trace.SpanFromContext(ctx).TracerProvider().Tracer("dagger.io/filesync")
}

func newSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	tr := curTracer(ctx)
	return tr.Start(ctx, name)
}

/* TODO:
* Be sure to test case where Dir is modified, make sure that doesn't invalidate the whole cachecontext tree
 */

func (ls *localSourceHandler) snapshot(ctx context.Context, session session.Group, caller session.Caller) (_ cache.ImmutableRef, rerr error) {
	ctx, span := newSpan(ctx, "filesync")
	defer span.End()

	getRefCtx, getRefSpan := newSpan(ctx, "getRef")
	ref, release, err := ls.getRef(getRefCtx, session, caller)
	if err != nil {
		getRefSpan.End()
		return nil, err
	}
	getRefSpan.End()
	defer func() {
		if err := release(ctx); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to release ref: %w", err))
		}
	}()

	clientPath := ls.src.Name
	// TODO: IsAbs is probably wrong for windows clients
	if !filepath.IsAbs(clientPath) {
		statCtx := engine.LocalImportOpts{
			Path:              clientPath,
			StatPathOnly:      true,
			StatReturnAbsPath: true,
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
		clientPath = statMsg.Path
	}

	if err := ls.sync(ctx, ref, clientPath, caller); err != nil {
		return nil, fmt.Errorf("failed to sync: %w", err)
	}

	checksumCtx, checksumSpan := newSpan(ctx, "checksum")
	dgst, err := ref.cacheCtx.Checksum(
		checksumCtx,
		ref.mutRef, // TODO: think through this again, should be fine?
		clientPath,
		contenthash.ChecksumOpts{
			IncludePatterns: ls.src.IncludePatterns,
			ExcludePatterns: ls.src.ExcludePatterns,
		},
		session,
	)
	if err != nil {
		checksumSpan.End()
		return nil, fmt.Errorf("failed to checksum: %w", err)
	}
	checksumSpan.End()

	// TODO:
	// TODO:
	// TODO:
	bklog.G(ctx).Debugf("CONTENT HASH: %s", dgst)

	// TODO: dedupe concurrent requests for same dgst

	searchCtx, searchSpan := newSpan(ctx, "searchContentHash")
	sis, err := SearchContentHash(searchCtx, ls.cm, dgst)
	if err != nil {
		searchSpan.End()
		return nil, fmt.Errorf("failed to search content hash: %w", err)
	}
	for _, si := range sis {
		finalRef, err := ls.cm.Get(searchCtx, si.ID(), nil)
		if err == nil {
			// TODO:
			// TODO:
			// TODO:
			bklog.G(ctx).Debugf("REUSING COPY REF: %s", finalRef.ID())

			searchSpan.End()
			return finalRef, nil
		} else {
			bklog.G(searchCtx).Debugf("failed to get cache ref: %v", err)
		}
	}
	searchSpan.End()

	copyCtx, copySpan := newSpan(ctx, "copy")
	defer copySpan.End()
	copyRef, err := ls.cm.New(copyCtx, nil, session)
	if err != nil {
		return nil, fmt.Errorf("failed to create new cache ref: %w", err)
	}
	// TODO: RELEASE IN ERROR CASES
	// TODO: RELEASE IN ERROR CASES
	// TODO: RELEASE IN ERROR CASES

	copyRefMntable, err := copyRef.Mount(copyCtx, false, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get mountable: %w", err)
	}
	copyRefMnter := snapshot.LocalMounter(copyRefMntable)
	copyRefMntPath, err := copyRefMnter.Mount()
	if err != nil {
		return nil, fmt.Errorf("failed to mount: %w", err)
	}
	// TODO: UNMOUNT IN ERROR CASES
	// TODO: UNMOUNT IN ERROR CASES
	// TODO: UNMOUNT IN ERROR CASES

	copyOpts := []fscopy.Opt{
		func(ci *fscopy.CopyInfo) {
			ci.IncludePatterns = ls.src.IncludePatterns
			ci.ExcludePatterns = ls.src.ExcludePatterns

			ci.CopyDirContents = true
		},
		fscopy.WithXAttrErrorHandler(func(dst, src, key string, err error) error {
			bklog.G(copyCtx).Debugf("xattr error: %v", err)
			return nil
		}),
	}

	/* TODO: equivalent of this
	defer func() {
		var osErr *os.PathError
		if errors.As(err, &osErr) {
			// remove system root from error path if present
			osErr.Path = strings.TrimPrefix(osErr.Path, src)
			osErr.Path = strings.TrimPrefix(osErr.Path, dest)
		}
	}()
	*/

	if err := fscopy.Copy(copyCtx, ref.mntPath, clientPath, copyRefMntPath, "/", copyOpts...); err != nil {
		return nil, fmt.Errorf("failed to copy %q: %w", clientPath, err)
	}

	if err := copyRefMnter.Unmount(); err != nil {
		return nil, fmt.Errorf("failed to unmount: %w", err)
	}

	finalRef, err := copyRef.Commit(copyCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	if err := finalRef.Finalize(copyCtx); err != nil {
		return nil, fmt.Errorf("failed to finalize: %w", err)
	}

	// TODO: Put buildkit's cachecontext md on this ref so solver reuses it
	// TODO: Put buildkit's cachecontext md on this ref so solver reuses it
	// TODO: Put buildkit's cachecontext md on this ref so solver reuses it
	// TODO: Put buildkit's cachecontext md on this ref so solver reuses it
	if err := (CacheRefMetadata{finalRef}).SetContentHashKey(dgst); err != nil {
		return nil, fmt.Errorf("failed to set content hash key: %w", err)
	}

	return finalRef, nil
}

func (ls *localSourceHandler) sync(ctx context.Context, ref *filesyncCacheRef, clientPath string, caller session.Caller) (rerr error) {
	ctx, syncSpan := newSpan(ctx, "sync")
	defer syncSpan.End()

	if err := ls.syncParentDirs(ctx, ref, clientPath, caller); err != nil {
		return fmt.Errorf("failed to sync parent dirs: %w", err)
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(rerr)
	}()

	remote, err := newRemoteFS(ctx, caller, clientPath, ls.src.IncludePatterns, ls.src.ExcludePatterns)
	if err != nil {
		return fmt.Errorf("failed to create remote fs: %w", err)
	}
	defer func() {
		if err := remote.Close(); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to close remote fs: %w", err))
		}
	}()

	local, err := NewLocalFS(ref.sharedState, clientPath, ls.src.IncludePatterns, ls.src.ExcludePatterns)
	if err != nil {
		return fmt.Errorf("failed to create local fs: %w", err)
	}
	err = local.Sync(ctx, remote)
	if err != nil {
		return fmt.Errorf("failed to sync to local fs: %w", err)
	}

	return nil
}

func (ls *localSourceHandler) syncParentDirs(ctx context.Context, ref *filesyncCacheRef, clientPath string, caller session.Caller) (rerr error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(rerr)
	}()

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("parentSync", "y"),
	)

	include := strings.TrimPrefix(strings.TrimSuffix(clientPath, "/"), "/")
	includes := []string{include}
	exclude := include + "/*"
	excludes := []string{exclude}

	remote, err := newRemoteFS(ctx, caller, "/", includes, excludes)
	if err != nil {
		return fmt.Errorf("failed to create remote fs: %w", err)
	}
	defer func() {
		if err := remote.Close(); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to close remote fs: %w", err))
		}
	}()

	local, err := NewLocalFS(ref.sharedState, "/", includes, excludes)
	if err != nil {
		return fmt.Errorf("failed to create local fs: %w", err)
	}
	err = local.Sync(ctx, remote)
	if err != nil {
		return fmt.Errorf("failed to sync to local fs: %w", err)
	}

	return nil
}

type filesyncCacheRef struct {
	mutRef cache.MutableRef

	mounter snapshot.Mounter
	mntPath string
	idmap   *idtools.IdentityMapping

	sharedState *localFSSharedState

	cacheCtx contenthash.CacheContext

	usageCount int
}

func (ls *localSourceHandler) getRef(
	ctx context.Context,
	session session.Group,
	caller session.Caller,
) (_ *filesyncCacheRef, _ func(context.Context) error, rerr error) {
	clientKey := ls.src.SharedKeyHint + ":" + caller.SharedKey()
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
				cache.WithRecordType(client.UsageRecordTypeLocalSource),
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

		ref.idmap = mntable.IdentityMapping()

		ref.cacheCtx, err = contenthash.GetCacheContext(ctx, ref.mutRef)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get cache context: %w", err)
		}

		ref.sharedState = &localFSSharedState{
			rootPath: ref.mntPath,
			contentHasher: func(kind ChangeKind, path string, fi os.FileInfo, err error) error {
				/*
					// TODO:
					// TODO:
					// TODO:
					// TODO:
					bklog.G(ctx).Debugf("CONTENT HASH CB: %s %s %v %v", kind, path, fi, err)
				*/

				return ref.cacheCtx.HandleChange(fsutil.ChangeKind(kind), path, fi, err)
			},
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
		// TODO: think through error cases where cache context should be cleared
		// TODO: think through error cases where cache context should be cleared
		// TODO: think through error cases where cache context should be cleared
		if err := contenthash.SetCacheContext(ctx, ref.mutRef, ref.cacheCtx); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to set cache context: %w", err))
		}
		if err := ref.mounter.Unmount(); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to unmount: %w", err))
		}
		if err := ref.mutRef.Release(ctx); err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("failed to release ref: %w", err))
		}
		return rerr
	}, nil
}

func newProgressHandler(ctx context.Context, id string) func(int, bool) {
	limiter := rate.NewLimiter(rate.Every(100*time.Millisecond), 1)
	pw, _, _ := progress.NewFromContext(ctx)
	now := time.Now()
	st := progress.Status{
		Started: &now,
		Action:  "transferring",
	}
	pw.Write(id, st)
	return func(s int, last bool) {
		if last || limiter.Allow() {
			st.Current = s
			if last {
				now := time.Now()
				st.Completed = &now
			}
			pw.Write(id, st)
			if last {
				pw.Close()
			}
		}
	}
}

const (
	keySharedKey   = "local.sharedKey"
	sharedKeyIndex = keySharedKey + ":"

	keyContentHashKey = "local.contentHashKey"
	contentHashIndex  = keyContentHashKey + ":"
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

func SearchContentHash(ctx context.Context, store cache.MetadataStore, dgst digest.Digest) ([]CacheRefMetadata, error) {
	var results []CacheRefMetadata
	mds, err := store.Search(ctx, contentHashIndex+dgst.Encoded(), false)
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

func (md CacheRefMetadata) getSharedKey() string {
	return md.GetString(keySharedKey)
}

func (md CacheRefMetadata) setSharedKey(key string) error {
	return md.SetString(keySharedKey, key, sharedKeyIndex+key)
}

func (md CacheRefMetadata) GetContentHashKey() (digest.Digest, bool) {
	dgstStr := md.GetString(keyContentHashKey)
	if dgstStr == "" {
		return "", false
	}
	return digest.Digest(string(digest.Canonical) + ":" + dgstStr), true
}

func (md CacheRefMetadata) SetContentHashKey(dgst digest.Digest) error {
	return md.SetString(keyContentHashKey, dgst.Encoded(), contentHashIndex+dgst.Encoded())
}
