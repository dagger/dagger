package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
)

// RemoteCacheIntegrationConfig injects an in-process remote cache integration.
// Ordinary engines leave it nil. This is not a schema field, listener or
// service; Run owns the integration's channel consumer and transport.
type RemoteCacheIntegrationConfig struct {
	// Run is called once, after local cache initialization, under the server
	// lifetime context. It must return once that context is canceled, including
	// for delivered requests' Done signals, and must not hold a cache operation
	// while waiting for future requests.
	Run func(context.Context, *RemoteCacheAdapter) error
}

var ErrRemoteCacheAdapterClosed = errors.New("remote cache adapter closed")

var errRemoteCacheIntegrationExited = errors.New("remote cache integration exited")

// RemoteCacheAdapter forwards control calls to its cache and renewal calls to
// the one bridge attached for it, never to a later attachment.
type RemoteCacheAdapter struct {
	cache   *dagql.Cache
	bridge  *dagql.RemoteCacheBridge
	stopped atomic.Bool
	cancel  context.CancelCauseFunc
	// runDone is owned by the server and closes when Run returns.
	runDone  chan struct{}
	stopOnce sync.Once
	stopErr  error
	// mergeValues is the cache's MergeValues. A test replaces it to return a
	// committed reply with a release error.
	mergeValues func(context.Context, dagql.CacheID, dagql.ValueBundle) (dagql.MergeReply, error)
	// lookupMerges counts the merges in progress that answer lookups,
	// which the others wait for.
	lookupMerges priorityMerges
}

func newRemoteCacheAdapter(cache *dagql.Cache, bridge *dagql.RemoteCacheBridge) *RemoteCacheAdapter {
	return &RemoteCacheAdapter{cache: cache, bridge: bridge, cancel: func(error) {}, runDone: make(chan struct{}), mergeValues: cache.MergeValues}
}

func (a *RemoteCacheAdapter) TakeRenewalRequest(ctx context.Context) (*dagql.RenewalRequest, error) {
	if a.stopped.Load() {
		return nil, ErrRemoteCacheAdapterClosed
	}
	request, err := a.bridge.TakeRenewalRequest(ctx)
	if errors.Is(err, dagql.ErrRemoteCacheBridgeClosed) {
		err = fmt.Errorf("%w: %w", ErrRemoteCacheAdapterClosed, err)
	}
	return request, err
}

func (a *RemoteCacheAdapter) ReplyRenewal(reply dagql.RenewalReply) dagql.RenewalReplyDisposition {
	if a.stopped.Load() {
		return dagql.RenewalReplyDiscarded
	}
	return a.bridge.ReplyRenewal(reply)
}

func (a *RemoteCacheAdapter) StartLookups() {
	if a.stopped.Load() {
		return
	}
	a.bridge.StartRemoteLookups()
}

func (a *RemoteCacheAdapter) StopLookups() {
	a.bridge.StopRemoteLookups()
}

func (a *RemoteCacheAdapter) TakeLookupMisses(ctx context.Context, limit int, linger time.Duration) ([]dagql.RemoteMiss, uint64, error) {
	if a.stopped.Load() {
		return nil, 0, ErrRemoteCacheAdapterClosed
	}
	misses, dropped, err := a.bridge.TakeRemoteMisses(ctx, limit, linger)
	if errors.Is(err, dagql.ErrRemoteCacheBridgeClosed) {
		err = fmt.Errorf("%w: %w", ErrRemoteCacheAdapterClosed, err)
	}
	return misses, dropped, err
}

func (a *RemoteCacheAdapter) ExpectMerge(recipe digest.Digest, wait *dagql.RemoteMergeWait, deadline time.Time) {
	if a.stopped.Load() {
		return
	}
	a.bridge.ExpectRemoteMerge(recipe, wait, deadline)
}

// priorityMerges counts the merges in progress that go first. The others
// wait until none is.
type priorityMerges struct {
	mu      sync.Mutex
	running int
	// idle closes when running drops to zero.
	idle chan struct{}
}

func (p *priorityMerges) begin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running == 0 {
		p.idle = make(chan struct{})
	}
	p.running++
}

func (p *priorityMerges) end() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running--
	if p.running == 0 {
		close(p.idle)
	}
}

// wait returns once no priority merge is in progress, or ctx ends.
func (p *priorityMerges) wait(ctx context.Context) error {
	p.mu.Lock()
	if p.running == 0 {
		p.mu.Unlock()
		return nil
	}
	idle := p.idle
	p.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// close refuses later control calls, detaches this adapter's bridge and
// cancels Run. It does not wait for Run.
func (a *RemoteCacheAdapter) close(cause error) {
	a.stopped.Store(true)
	a.cache.DetachRemoteCacheBridge(a.bridge)
	a.cancel(cause)
}

// Stop closes the adapter and joins Run within ctx, the deadline engine
// shutdown already passes. It is idempotent and keeps the first result. A
// Run that ignores cancellation is reported, not terminated.
func (a *RemoteCacheAdapter) Stop(ctx context.Context) error {
	a.stopOnce.Do(func() {
		a.close(errServerShuttingDown)
		// A Run that has already returned has stopped, whatever ctx says.
		select {
		case <-a.runDone:
			return
		default:
		}
		select {
		case <-a.runDone:
		case <-ctx.Done():
			a.stopErr = fmt.Errorf("remote cache integration did not stop: %w", context.Cause(ctx))
		}
	})
	return a.stopErr
}

// startRemoteCacheIntegration attaches the bridge and starts Run for a newly
// created attachment. A nil config allocates nothing.
func (srv *Server) startRemoteCacheIntegration(cfg *RemoteCacheIntegrationConfig) error {
	if cfg == nil {
		return nil
	}
	bridge, created, err := srv.engineCache.AttachRemoteCacheBridge()
	if err != nil {
		return fmt.Errorf("attach remote cache integration: %w", err)
	}
	if !created {
		return nil
	}
	adapter := newRemoteCacheAdapter(srv.engineCache, bridge)
	ctx, cancel := context.WithCancelCause(srv.shutdownCtx)
	adapter.cancel = cancel
	srv.remoteCacheAdapter = adapter
	go func() {
		defer close(adapter.runDone)
		err := cfg.Run(ctx, adapter)
		if err != nil && ctx.Err() == nil {
			slog.Error("remote cache integration exited", "error", err)
		}
		// The engine keeps serving; pending exchanges complete as unavailable.
		adapter.close(errRemoteCacheIntegrationExited)
	}()
	return nil
}

func (srv *Server) stopRemoteCacheIntegration(ctx context.Context) error {
	if srv.remoteCacheAdapter == nil {
		return nil
	}
	return srv.remoteCacheAdapter.Stop(ctx)
}

func validateRemoteCacheIntegration(cfg *RemoteCacheIntegrationConfig) error {
	if cfg != nil && cfg.Run == nil {
		return errors.New("remote cache integration requires Run")
	}
	return nil
}
