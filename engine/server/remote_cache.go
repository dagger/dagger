package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// RemoteCacheIntegrationConfig injects an in-process remote cache integration.
// Ordinary engines leave it nil. This is not a schema field, listener or
// service; Run owns the integration's channel consumer and transport.
type RemoteCacheIntegrationConfig struct {
	// Run is called once, after local cache initialization, under a context
	// that outlives the start of the engine's graceful stop: the stop lets the
	// exports in progress finish, then cancels it. Run must return once that
	// context is canceled, including for delivered requests' Done signals, and
	// must not hold a cache operation while waiting for future requests.
	Run func(context.Context, *RemoteCacheAdapter) error
}

var ErrRemoteCacheAdapterClosed = errors.New("remote cache adapter closed")

// remoteCacheStopReserve is the part of the graceful stop's deadline that the
// exports in progress leave to the stop's later phases: closing and
// persisting the cache, the final telemetry flushes. Those took up to about
// 30s on engines that had run a long test suite.
const remoteCacheStopReserve = 30 * time.Second

// DiscardedStateStopReserve is remoteCacheStopReserve for an engine whose
// state is discarded (state.discarded): its stop's later phases are only
// the sessions' end, the telemetry flushes and the cache volume directory,
// each under 0.5s on CI engines. The final telemetry flushes after the stop
// are each bounded by it too.
const DiscardedStateStopReserve = 10 * time.Second

var errRemoteCacheIntegrationExited = errors.New("remote cache integration exited")

// errRemoteCacheTimedOut cancels the exports in progress once the engine
// gives up its remote-cache work (remoteCacheSettleBound).
var errRemoteCacheTimedOut = errors.New("remote cache: the engine gave up its remote-cache work after its last session ended")

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
	// exportsCut is set by Stop when its drain cut exports off at the
	// drain's deadline.
	exportsCut bool
	// mergeValues is the cache's MergeValues. A test replaces it to return a
	// committed reply with a release error.
	mergeValues func(context.Context, dagql.CacheID, dagql.ValueBundle) (dagql.MergeReply, error)
	// lookupMerges counts the merges in progress that answer lookups,
	// which the others wait for.
	lookupMerges inProgress
	// exports counts the exports in progress, which a graceful stop lets
	// finish.
	exports inProgress
	// busy remembers the rows exports found busy, for their log lines.
	busy busyRows
	// testBusyReport, set by a test, sees each export's busy report.
	testBusyReport func(busyReport)
	// paused refuses the service's exports while the engine has given up its
	// remote-cache work, and cancels the exports in progress when it does.
	paused exportPause
	// ledger is the server's, whose settle state the adapter reports to Run;
	// nil for an adapter of no server.
	ledger *sessionLedger
	// stopReserve is remoteCacheStopReserve. A test shortens it.
	stopReserve time.Duration
}

// SettleState reports where the engine stands in settling with the
// service, and a channel closed when that next changes.
func (a *RemoteCacheAdapter) SettleState() (remotecache.SettleState, <-chan struct{}) {
	if a.ledger == nil {
		return remotecache.SettleState{}, nil
	}
	return a.ledger.settleState()
}

// Settled records the service's Settled for a settle of period that listed
// sessions.
func (a *RemoteCacheAdapter) Settled(period uint64, sessions []protocol.SettleSession) {
	if a.ledger != nil {
		a.ledger.settledFor(period, sessions)
	}
}

// exportPause refuses exports while paused, and cancels the exports in
// progress when it pauses.
type exportPause struct {
	mu      sync.Mutex
	paused  bool
	next    uint64
	running map[uint64]context.CancelCauseFunc
}

// begin admits an export unless paused, under a context pause cancels;
// release ends it.
func (p *exportPause) begin(ctx context.Context) (_ context.Context, release func(), ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused {
		return ctx, func() {}, false
	}
	ctx, cancel := context.WithCancelCause(ctx)
	id := p.next
	p.next++
	if p.running == nil {
		p.running = map[uint64]context.CancelCauseFunc{}
	}
	p.running[id] = cancel
	return ctx, func() {
		p.mu.Lock()
		delete(p.running, id)
		p.mu.Unlock()
		cancel(nil)
	}, true
}

func (p *exportPause) pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = true
	for _, cancel := range p.running {
		cancel(errRemoteCacheTimedOut)
	}
}

func (p *exportPause) resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = false
}

func newRemoteCacheAdapter(cache *dagql.Cache, bridge *dagql.RemoteCacheBridge) *RemoteCacheAdapter {
	return &RemoteCacheAdapter{cache: cache, bridge: bridge, cancel: func(error) {}, runDone: make(chan struct{}), mergeValues: cache.MergeValues, stopReserve: remoteCacheStopReserve}
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

// inProgress counts operations in progress, and lets others wait until none
// is.
type inProgress struct {
	mu      sync.Mutex
	running int
	// idle closes when running drops to zero.
	idle chan struct{}
	// closed refuses later operations, once closeWhenIdle saw none running.
	closed bool
}

// tryBegin counts one more operation in progress, unless closeWhenIdle has
// closed p.
func (p *inProgress) tryBegin() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.beginLocked()
	return true
}

func (p *inProgress) begin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.beginLocked()
}

func (p *inProgress) beginLocked() {
	if p.running == 0 {
		p.idle = make(chan struct{})
	}
	p.running++
}

// count returns how many operations are in progress.
func (p *inProgress) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *inProgress) end() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running--
	if p.running == 0 {
		close(p.idle)
	}
}

// wait returns once no operation is in progress, or ctx ends.
func (p *inProgress) wait(ctx context.Context) error {
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

// closeWhenIdle waits until no operation is in progress and, in the same
// step, refuses later ones, or returns ctx's cause first. An operation that
// begins while it waits is waited for too.
func (p *inProgress) closeWhenIdle(ctx context.Context) error {
	for {
		p.mu.Lock()
		if p.running == 0 {
			p.closed = true
			p.mu.Unlock()
			return nil
		}
		idle := p.idle
		p.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// close refuses later control calls, detaches this adapter's bridge and
// cancels Run. It does not wait for Run.
func (a *RemoteCacheAdapter) close(cause error) {
	a.stopped.Store(true)
	a.cache.DetachRemoteCacheBridge(a.bridge)
	if a.ledger != nil {
		a.ledger.detachRemote()
	}
	a.cancel(cause)
}

// Stop lets the exports in progress finish, then closes the adapter and joins
// Run within ctx, the deadline engine shutdown already passes. Lookups stop at
// once. The exports get until ctx's deadline less stopReserve; the service may
// still start exports meanwhile, and Stop waits for those too. Without a
// deadline they get as long as ctx lasts. It is idempotent and keeps the first
// result. A Run that ignores cancellation is reported, not terminated.
func (a *RemoteCacheAdapter) Stop(ctx context.Context) error {
	a.stopOnce.Do(func() {
		a.exportsCut = !a.drainExports(ctx)
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

// drainExports stops lookups and waits until no export is in progress, then
// refuses later ones; or it gives up at ctx's deadline less stopReserve. It
// reports whether the exports finished.
func (a *RemoteCacheAdapter) drainExports(ctx context.Context) bool {
	a.StopLookups()
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-a.stopReserve))
		defer cancel()
	}
	start := time.Now()
	if err := a.exports.closeWhenIdle(ctx); err != nil {
		slog.Warn("remote cache exports did not finish before the engine stopped", "waited", time.Since(start), "error", err)
		return false
	}
	slog.Info("remote cache exports finished before the engine stopped", "waited", time.Since(start))
	return true
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
	if srv.stateDiscarded {
		adapter.stopReserve = DiscardedStateStopReserve
	}
	adapter.ledger = &srv.sessionLedger
	srv.sessionLedger.attachRemote(adapter.exports.count, adapter.paused.pause, adapter.paused.resume)
	// The graceful stop's start cancels shutdownCtx; Stop cancels Run once the
	// exports in progress finish.
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(srv.shutdownCtx))
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

// remoteCacheExportsCut reports whether the integration's stop cut exports
// off at its drain's deadline.
func (srv *Server) remoteCacheExportsCut() bool {
	return srv.remoteCacheAdapter != nil && srv.remoteCacheAdapter.exportsCut
}

func validateRemoteCacheIntegration(cfg *RemoteCacheIntegrationConfig) error {
	if cfg != nil && cfg.Run == nil {
		return errors.New("remote cache integration requires Run")
	}
	return nil
}
