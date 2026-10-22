package snapshots

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	cerrdefs "github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
)

// fetchAheadEnv turns fetching chain layers' blobs ahead, and joining
// downloads, off when set to 0: each import then downloads its own blobs
// under its own lease, one layer at a time.
const fetchAheadEnv = "_EXPERIMENTAL_DAGGER_FETCH_AHEAD"

const (
	// chainAheadWindow is how many layers past the one importing a chain
	// import downloads ahead. A chain with no more missing layers than that
	// pays about one round of request latency instead of one per layer, and
	// one long chain can't take every slot.
	chainAheadWindow = 16
	// maxAheadDownloads bounds the downloads started ahead across the
	// engine, and so the load on the blob store. It matches the part
	// transport's idle connections per blob host (dagql
	// maxPartIdleConnsPerHost), so the connections these requests open can
	// all stay for the next ones. A layer's own download, when it imports,
	// and a joined download take no slot, so the bound never holds an
	// import up.
	maxAheadDownloads = 32
)

var fetchAheadEnabled = sync.OnceValue(func() bool {
	return os.Getenv(fetchAheadEnv) != "0"
})

// errFetchAbandoned cancels a chain-content download no import waits for
// anymore.
var errFetchAbandoned = errors.New("no import waits for the content anymore")

// contentFetches runs chain-content downloads one per blob: an import that
// needs a blob another import is downloading joins that download instead of
// starting its own. A download runs under its own temporary lease, detached
// from the import that started it, until no import holds it; each import
// links the content to its own lease before letting go, so the blob is never
// unprotected.
type contentFetches struct {
	mu       sync.Mutex
	inflight map[digest.Digest]*contentFetch
	// ahead holds the slots of downloads started ahead, maxAheadDownloads.
	ahead *semaphore.Weighted
}

// contentFetch is one blob's download.
type contentFetch struct {
	desc ocispecs.Descriptor
	// owner is the chain import the download started for. An import never
	// retries its own failed download.
	owner *ExportChain
	// done is closed once err is set and, if no import held the download by
	// then, its lease is released.
	done chan struct{}
	err  error
	// read is set with err: whether the download read the provider. One
	// that found the blob stored already read nothing.
	read   bool
	cancel context.CancelCauseFunc
	pin    *resourcePin
	// ended, when set, runs once the download ends: it frees an ahead slot.
	ended func()

	// refs counts the imports holding the download; finished is set once it
	// ended. Both under contentFetches.mu.
	refs     int
	finished bool
}

// joinFetch holds desc's download for owner, starting it with provider when
// none runs. A retry names the download that failed: it starts a new one in
// its place, unless another import already did, which it then joins. ended,
// when set, runs once a download this call starts ends, and right away when
// it starts none. It reports whether this call started the download.
func (cm *snapshotManager) joinFetch(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider, owner *ExportChain, failed *contentFetch, ended func()) (_ *contentFetch, started bool, rerr error) {
	defer func() {
		if !started && ended != nil {
			ended()
		}
	}()
	f := &cm.fetches
	f.mu.Lock()
	if f.inflight == nil {
		f.inflight = map[digest.Digest]*contentFetch{}
	}
	if current := f.inflight[desc.Digest]; current != nil && current != failed {
		current.refs++
		f.mu.Unlock()
		return current, false, nil
	}
	f.mu.Unlock()
	// The download runs under its own lease, so that it survives the
	// cancellation of the import that started it while another waits.
	pin, dctx, err := cm.newResourcePin(context.WithoutCancel(ctx))
	if err != nil {
		return nil, false, err
	}
	dctx, cancel := context.WithCancelCause(dctx)
	download := &contentFetch{desc: desc, owner: owner, done: make(chan struct{}), cancel: cancel, pin: pin, ended: ended, refs: 1}
	f.mu.Lock()
	if current := f.inflight[desc.Digest]; current != nil && current != failed {
		// Another import started the download meanwhile.
		current.refs++
		f.mu.Unlock()
		cancel(nil)
		_ = pin.release(context.WithoutCancel(ctx))
		return current, false, nil
	}
	f.inflight[desc.Digest] = download
	f.mu.Unlock()
	go cm.runFetch(dctx, download, provider)
	return download, true, nil
}

func (cm *snapshotManager) runFetch(ctx context.Context, f *contentFetch, provider content.Provider) {
	read, err := cm.downloadChainContent(ctx, f.desc, provider)
	if f.ended != nil {
		f.ended()
	}
	cm.fetches.mu.Lock()
	f.err = err
	f.read = read
	f.finished = true
	last := f.refs == 0
	cm.fetches.mu.Unlock()
	if last {
		_ = f.pin.release(context.WithoutCancel(ctx))
	}
	f.cancel(nil)
	close(f.done)
}

// leave drops one hold of f. The last hold of a running download cancels it;
// the last hold of a finished one releases its lease. It reports whether the
// download was abandoned while running, which its done then follows.
func (cm *snapshotManager) leaveFetch(ctx context.Context, f *contentFetch) bool {
	if f == nil {
		return false
	}
	fs := &cm.fetches
	fs.mu.Lock()
	f.refs--
	last := f.refs == 0
	if last && fs.inflight[f.desc.Digest] == f {
		delete(fs.inflight, f.desc.Digest)
	}
	finished := f.finished
	fs.mu.Unlock()
	switch {
	case !last:
		return false
	case finished:
		_ = f.pin.release(context.WithoutCancel(ctx))
		return false
	default:
		f.cancel(errFetchAbandoned)
		return true
	}
}

// dropFetch drops one hold of f and, when that abandons the download, waits
// for it to stop and release its lease and partial content.
func (cm *snapshotManager) dropFetch(ctx context.Context, f *contentFetch) {
	if cm.leaveFetch(ctx, f) {
		<-f.done
	}
}

// fetchChainContent puts desc's blob in the content store, linked to ctx's
// lease: by waiting for held, this import's own download of it started
// ahead, by joining a download of it in progress, or by downloading it from
// provider. A download that failed for other imports leaves this import its
// own attempt with its provider, unless it already made one: an import never
// retries its own failed download, and imports retrying the same failed
// download join one retry. The caller keeps its hold of held. It reports
// whether the blob came from another import's download, and whether the
// download it came from read the provider.
func (cm *snapshotManager) fetchChainContent(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider, owner *ExportChain, held *contentFetch) (joined, read bool, _ error) {
	f := held
	if f == nil {
		var err error
		if f, _, err = cm.joinFetch(ctx, desc, provider, owner, nil, nil); err != nil {
			return false, false, err
		}
	}
	// leave drops this call's own hold; held stays the caller's.
	leave := func(f *contentFetch) {
		if f != held {
			cm.leaveFetch(ctx, f)
		}
	}
	for {
		select {
		case <-f.done:
		case <-ctx.Done():
		}
		if cause := context.Cause(ctx); cause != nil {
			if f != held {
				cm.dropFetch(ctx, f)
			}
			return false, false, cause
		}
		if f.err == nil {
			// Linked while the download still holds the content.
			err := cm.linkContentToContextLease(ctx, desc)
			leave(f)
			return f.owner != owner, f.read, err
		}
		leave(f)
		// This import's own attempt failed: it is done. Another import's
		// failed: this import tries next, or joins whichever import already
		// does, and that attempt's failure is judged the same way.
		if f.owner == owner {
			return false, false, f.err
		}
		next, _, err := cm.joinFetch(ctx, desc, provider, owner, f, nil)
		if err != nil {
			return false, false, err
		}
		f = next
	}
}

// fetchAhead starts downloading desc's blob for owner before its layer is
// imported, unless it is in the content store or the builtin image store
// already. Starting a download takes one of the engine's ahead slots; with
// none free it starts nothing and reports full, and the caller tries again
// later. The caller drops the returned hold, which may be nil, with
// leaveFetch once the layer is imported. It reports whether it started a
// download.
func (cm *snapshotManager) fetchAhead(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider, owner *ExportChain) (_ *contentFetch, started, full bool) {
	if _, err := cm.ContentStore.Info(ctx, desc.Digest); err == nil {
		return nil, false, false
	}
	if cm.builtinContent != nil {
		if _, err := cm.builtinContent.Info(ctx, desc.Digest); err == nil {
			return nil, false, false
		}
	}
	slots := cm.aheadSlots()
	if !slots.TryAcquire(1) {
		return nil, false, true
	}
	var once sync.Once
	f, started, err := cm.joinFetch(ctx, desc, provider, owner, nil, func() { once.Do(func() { slots.Release(1) }) })
	if err != nil {
		return nil, false, false
	}
	return f, started, false
}

func (cm *snapshotManager) aheadSlots() *semaphore.Weighted {
	f := &cm.fetches
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ahead == nil {
		f.ahead = semaphore.NewWeighted(maxAheadDownloads)
	}
	return f.ahead
}

// downloadChainContent copies desc's blob from provider into the content
// store, under ctx's lease. It reports whether it read the provider: a blob
// another writer stored meanwhile needs no read.
func (cm *snapshotManager) downloadChainContent(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider) (_ bool, rerr error) {
	ref := "snapshot-import-" + rand.Text()
	writer, err := content.OpenWriter(ctx, cm.ContentStore, content.WithRef(ref), content.WithDescriptor(desc))
	if cerrdefs.IsAlreadyExists(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		_ = writer.Close()
		if rerr != nil {
			_ = cm.ContentStore.Abort(context.WithoutCancel(ctx), ref)
		}
	}()
	reader, err := provider.ReaderAt(ctx, desc)
	if err != nil {
		return true, chainError(ctx, true, desc, "provider", err)
	}
	defer reader.Close()
	return true, copyChainContent(ctx, writer, reader, desc)
}
