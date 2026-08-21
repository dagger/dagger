package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	telemetry "github.com/dagger/otel-go"
	"github.com/sirupsen/logrus"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"resenje.org/singleflight"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/engineutil"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/util/cleanups"
)

type daggerSession struct {
	sessionID          string
	mainClientCallerID string

	// wcprofEnabled means this session opted into wall-clock profiling
	// (ClientMetadata.Profile); work for all its clients (including nested
	// module/SDK clients) is recorded even when engine-global recording is
	// off.
	wcprofEnabled bool

	// state is read lock-free by observer paths and written only under
	// lifecycleMu. The zero value is sessionStateUninitialized.
	state atomicSessionState

	// lifecycleMu serializes this session's initialization and teardown. It is
	// held across the (potentially slow, up to ~60s) init and teardown work, but
	// NO observer path (Clients/activeClientIDs/clientRecordFromIDs) ever acquires it,
	// so a session stuck initializing or tearing down can never stall the
	// active-clients API or the client-DB GC.
	lifecycleMu sync.Mutex

	// scopeMu is the single serialization point for client lifecycle lease
	// acquisition, release, closing, and session teardown.
	scopeMu        sync.Mutex
	nextScopeID    uint64
	scopeAuthority *engine.ClientScopeAuthority

	clientRecords  map[string]*clientRecord  // clientID -> stable identity and routing record
	clientRuntimes map[string]*clientRuntime // clientID -> published non-quiescent execution runtime
	clientMu       sync.RWMutex

	attachables *sessionAttachableManager

	// engineUtilClient is the session-owned engine gateway. Its caller routing
	// is derived from immutable client metadata in context and the session's
	// record/attachable graph, so host access never requires the metadata
	// owner's retained execution runtime.
	engineUtilClient *engineutil.Client
	getClientCaller  func(context.Context, string) (engineutil.SessionCaller, error)

	closingCtx       context.Context
	cancelClosing    context.CancelCauseFunc
	closeClosingOnce sync.Once

	// wcprofTraceID / wcprofRootSpanID are this session's trace and its session-root
	// (POST /query) span, captured once on the first traced main-client query (when
	// the propagated ids are in hand). At teardown removeDaggerSession stamps the
	// EXACT final engine span count on a carrier span parented here (so it lands in
	// this trace) and Reaps the counter entry. See wcprofSpanCounter
	// (engine/server/wcprofcount.go).
	wcprofTraceID    trace.TraceID
	wcprofRootSpanID trace.SpanID
	wcprofTraceOnce  sync.Once

	// closed after the shutdown endpoint is called
	shutdownCh        chan struct{}
	closeShutdownOnce sync.Once

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	endpoints  map[string]http.Handler
	endpointMu sync.RWMutex

	// Session-owned telemetry pipelines. Their processors/measurements capture
	// an origin client ID from each emission context and their exporters resolve
	// the session's immutable ancestry graph when a batch is exported.
	tracerProvider *sdktrace.TracerProvider
	loggerProvider *sdklog.LoggerProvider
	meterProvider  *sdkmetric.MeterProvider
	spanExporter   sdktrace.SpanExporter
	logExporter    sdklog.Exporter
	metricExporter sdkmetric.Exporter
	telemetryDebug LifecycleTelemetryCounts

	// informed when a client goes away to prevent hanging on drain
	telemetryPubSub *PubSub
	seenKeys        sync.Map

	services *core.Services
	agents   *core.AgentRuntimes
	resolver *serverresolver.Resolver

	analytics analytics.Tracker

	authProvider *auth.RegistryAuthProvider

	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex

	dagqlMu       sync.Mutex
	dagqlCond     *sync.Cond
	dagqlInFlight int
	dagqlClosing  bool

	interactive        bool
	interactiveCommand []string

	allowedLLMModules []string

	lockFiles  map[workspaceLockKey]*workspaceLockState
	lockFileMu sync.RWMutex
}

type workspaceLockKey struct {
	ownerClientID string
	lockPath      string
}

type workspaceLockState struct {
	ws       *core.Workspace
	lockPath string
	lock     *workspace.Lock
	delta    *workspace.Lock
	loaded   bool
	dirty    bool
}

// daggerSessionState is the lifecycle state of a session. It is intentionally
// int-backed (rather than string) so it can be stored in an atomic and read by
// observers without locking; the zero value is sessionStateUninitialized.
type daggerSessionState int32

const (
	sessionStateUninitialized daggerSessionState = iota
	sessionStateInitialized
	sessionStateRemoved
)

func (s daggerSessionState) String() string {
	switch s {
	case sessionStateUninitialized:
		return "uninitialized"
	case sessionStateInitialized:
		return "initialized"
	case sessionStateRemoved:
		return "removed"
	default:
		return fmt.Sprintf("unknown(%d)", int32(s))
	}
}

// atomicSessionState wraps the session's lifecycle state so it can be read
// lock-free by observer paths (Clients/activeClientIDs/clientRecordFromIDs). Writes
// only ever happen while holding the session's lifecycleMu, which serializes
// state transitions; the atomic is what makes concurrent lock-free reads safe.
type atomicSessionState struct {
	v atomic.Int32
}

func (a *atomicSessionState) Load() daggerSessionState   { return daggerSessionState(a.v.Load()) }
func (a *atomicSessionState) Store(s daggerSessionState) { a.v.Store(int32(s)) }

// clientRecord is the stable, lightweight identity and routing descriptor for
// one client. Records remain published for the entire session independently of
// retained executable runtimes.
type clientRecord struct {
	daggerSession  *daggerSession
	clientID       string
	clientVersion  string
	secretToken    string
	clientMetadata *engine.ClientMetadata
	// metadataSealed and clientMetadata bootstrap mutation are protected by
	// daggerSession.scopeMu. Once sealed, clientMetadata is immutable and every
	// externally returned value is a deep clone of it.
	metadataSealed bool

	// closed after the shutdown endpoint is called
	shutdownCh        chan struct{}
	closeShutdownOnce sync.Once

	// If the client is nested, parentClientIDs is its immutable ancestry from
	// the session root to its direct parent.
	parentClientIDs []string

	// accepting is protected by the session scopeMu and changes monotonically
	// from true to false. The opaque transport handle is stable record identity;
	// its typed lease is owned by the currently published runtime.
	accepting       bool
	nestedTransport *engine.NestedClientTransport

	// closedAt records the serialized reachability close from any source.
	// quiescentAt records the one transition that atomically unpublished the
	// runtime. shutdownAt separately records the first /shutdown signal for
	// diagnostics; none of these fields drive reclamation.
	closedAt    time.Time
	quiescentAt time.Time
	shutdownAt  time.Time

	// Host attachables and resources remain session-owned and keyed by clientID.
	// Synthetic clients may route host services through this stable ancestor ID.
	hostServiceProxyClientID string
}

// clientRuntime is reclaimable executable state for a client record. A runtime
// is published only while reachability or a typed executable/durable lease can
// still use it. Its record remains session-long after the runtime is atomically
// unpublished at the client's quiescent transition.
type clientRuntime struct {
	*clientRecord

	// lifecycleLeases and transport ownership are protected by scopeMu.
	lifecycleLeases        map[uint64]clientLifecycleLeaseRecord
	transportLease         *engine.ClientLifecycleLease
	parentClientScopeLease *engine.ClientLifecycleLease

	state   clientRuntimeState
	stateMu sync.RWMutex
	// the number of active http requests to any endpoint from this client,
	// used to determine when to cleanup the client+session
	activeCount int

	dag       *dagql.Server
	dagqlRoot *core.Query

	// if the client is coming from a module, this is that module
	mod dagql.ObjectResult[*core.Module]

	// the set of modules being served to this client, with per-module
	// install policy (constructor vs type-only)
	servedMods *core.SchemaBuilder
	// the default deps that each client/module starts out with (currently just core)
	defaultDeps *core.SchemaBuilder

	// If the client is itself from a function call in a user module, this is set with the
	// metadata of that ongoing function call
	fnCall *core.FunctionCall

	// Telemetry providers and readers are session-owned. These lightweight
	// bound exporters are retained only for integrations that deliver trace/log
	// telemetry for a known client without an emission context (for example
	// cloud scale-out).
	spanExporter sdktrace.SpanExporter
	logExporter  sdklog.Exporter

	// Workspace and extra module loading is deferred from initializeClientRuntime
	// to serveQuery because it requires the client's engine utility session, which
	// isn't available during initialization (the session attachables request
	// is blocked on the same locks that initializeClientRuntime holds).

	// Whether this client should detect its own workspace binding.
	// Non-module clients detect their own workspace; module clients inherit a
	// parent workspace binding instead.
	pendingWorkspaceLoad bool
	workspaceMu          sync.Mutex
	workspaceLoaded      bool
	workspaceErr         error

	// workspaceReadEpoch is a monotonically bumped token folded into cached
	// Workspace.file / Workspace.directory host reads' per-client cache
	// namespace. Bumped on Workspace.export / Workspace.reloaded so a
	// long-lived session re-reads
	// the host after the workspace's on-disk content changed under it, instead
	// of serving a stale per-client host.directory snapshot cached earlier in
	// the session. Atomic (not guarded by workspaceMu) so a read resolver can
	// consult it without risking the workspaceMu that ensureWorkspaceLoaded
	// holds across module loading.
	workspaceReadEpoch atomic.Uint64

	// Cached workspace result from ensureWorkspaceLoaded.
	workspace *core.Workspace

	pendingModules      []pendingModule      // gathered in detectAndLoadWorkspaceWithRootfs
	pendingExtraModules []engine.ExtraModule // populated from clientMD, can arrive late
	modulesMu           sync.Mutex
	extraModulesLoaded  bool
	extraModulesErr     error
	// load failures by moduleProgressName; failed modules stay pending to keep
	// reporting their error
	failedModules map[string]error
	// whether an entrypoint module has been served (extras outrank ambient)
	entrypointServed bool
	// resolved identities already served, for cross-batch deduplication
	servedModuleKeys map[string]struct{}
	// served workspace module names, so demand filters recognize them without
	// reloading
	servedWorkspaceModuleNames map[string]struct{}
	// whether the client-declared workspace module scope was applied; the
	// scope is one-shot so later introspections load everything (guarded by
	// modulesMu)
	workspaceModuleScopeConsumed bool
	singleQueryMu                sync.Mutex
	singleQueryServed            bool
}

func (srv *Server) getCoreSchemaBase(ctx context.Context) (*schema.CoreSchemaBase, error) {
	srv.coreSchemaBaseMu.Lock()
	defer srv.coreSchemaBaseMu.Unlock()

	if srv.coreSchemaBase != nil {
		return srv.coreSchemaBase, nil
	}

	base, err := schema.NewCoreSchemaBase(ctx, srv)
	if err != nil {
		return nil, err
	}
	srv.coreSchemaBase = base
	return base, nil
}

type clientRuntimeState string

const (
	clientStateUninitialized clientRuntimeState = "uninitialized"
	clientStateInitialized   clientRuntimeState = "initialized"
	clientStateReclaimed     clientRuntimeState = "reclaimed"
)

func (client *clientRuntime) String() string {
	return fmt.Sprintf("<Client %s: %s>", client.clientID, client.state)
}

// releaseHeavyState drops every execution-only edge after the runtime has been
// atomically unpublished. Released lease handles may remain in stale contexts,
// and their callbacks intentionally still point at this small shell, so clearing
// the fields here ensures those harmless handles cannot retain a DagQL/schema
// graph. Zero typed leases proves no legitimate execution can race these writes.
func (client *clientRuntime) releaseHeavyState() {
	client.stateMu.Lock()
	client.state = clientStateReclaimed
	client.dag = nil
	client.dagqlRoot = nil
	client.mod = dagql.ObjectResult[*core.Module]{}
	client.servedMods = nil
	client.defaultDeps = nil
	client.fnCall = nil
	client.spanExporter = nil
	client.logExporter = nil
	client.stateMu.Unlock()

	client.workspaceMu.Lock()
	client.pendingWorkspaceLoad = false
	client.workspaceLoaded = false
	client.workspaceErr = nil
	client.workspace = nil
	client.workspaceMu.Unlock()

	client.modulesMu.Lock()
	client.pendingModules = nil
	client.pendingExtraModules = nil
	client.extraModulesLoaded = false
	client.extraModulesErr = nil
	client.failedModules = nil
	client.entrypointServed = false
	client.servedModuleKeys = nil
	client.servedWorkspaceModuleNames = nil
	client.workspaceModuleScopeConsumed = false
	client.modulesMu.Unlock()
}

// parentClientIDsForRegistration validates a client's requested place in the
// session graph and returns its immutable root-to-direct-parent ancestry. An
// intentional root must be explicit; every nested client must name a live,
// ancestry-consistent parent record already published in this session.
func (sess *daggerSession) parentClientIDsForRegistration(clientID, parentClientID string, root bool) ([]string, error) {
	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()

	var parentIDs []string
	switch {
	case root && parentClientID != "":
		return nil, fmt.Errorf("root client %q cannot specify parent %q", clientID, parentClientID)
	case root:
		// Intentional roots have an explicitly empty route.
	case parentClientID == "":
		return nil, fmt.Errorf("nested client %q is missing parent client ID", clientID)
	case parentClientID == clientID:
		return nil, fmt.Errorf("client ancestry cycle: client %q cannot be its own parent", clientID)
	default:
		parent, ok := sess.clientRecords[parentClientID]
		if !ok {
			return nil, fmt.Errorf("parent client %q not found for nested client %q", parentClientID, clientID)
		}
		parentIDs = append(slices.Clone(parent.parentClientIDs), parentClientID)
	}

	seen := map[string]struct{}{clientID: {}}
	for i, ancestorID := range parentIDs {
		if ancestorID == "" {
			return nil, fmt.Errorf("client %q has empty ancestor at route index %d", clientID, i)
		}
		if _, ok := seen[ancestorID]; ok {
			return nil, fmt.Errorf("client ancestry cycle: client %q reaches %q more than once", clientID, ancestorID)
		}
		seen[ancestorID] = struct{}{}

		ancestor, ok := sess.clientRecords[ancestorID]
		if !ok {
			return nil, fmt.Errorf("ancestor client %q not found for client %q", ancestorID, clientID)
		}
		if !slices.Equal(ancestor.parentClientIDs, parentIDs[:i]) {
			return nil, fmt.Errorf("ancestor client %q has mismatched ancestry for client %q", ancestorID, clientID)
		}
	}

	if existing, ok := sess.clientRecords[clientID]; ok && !slices.Equal(existing.parentClientIDs, parentIDs) {
		return nil, fmt.Errorf("client %q already exists with different parent ancestry", clientID)
	}
	return parentIDs, nil
}

// ancestorRuntimes resolves executable ancestors for operations that need
// module or engine state. Record-only ancestry and telemetry routing never call
// this method and therefore do not imply runtime ownership.
func (sess *daggerSession) ancestorRuntimes(record *clientRecord) ([]*clientRuntime, error) {
	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()

	ancestors := make([]*clientRuntime, 0, len(record.parentClientIDs))
	for _, parentID := range record.parentClientIDs {
		parent, ok := sess.clientRuntimes[parentID]
		if !ok {
			return nil, fmt.Errorf("ancestor client runtime %q not found for client %q", parentID, record.clientID)
		}
		ancestors = append(ancestors, parent)
	}
	return ancestors, nil
}

// telemetryRouteClientIDs returns the exact nested-to-ancestor fan-out order:
// the origin first, followed by ancestors from the root to the direct parent.
func (sess *daggerSession) telemetryRouteClientIDs(record *clientRecord) ([]string, error) {
	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()

	route := append([]string{record.clientID}, record.parentClientIDs...)
	for i, parentID := range record.parentClientIDs {
		parent, ok := sess.clientRecords[parentID]
		if !ok {
			return nil, fmt.Errorf("telemetry ancestor client %q not found for client %q", parentID, record.clientID)
		}
		if !slices.Equal(parent.parentClientIDs, record.parentClientIDs[:i]) {
			return nil, fmt.Errorf("telemetry ancestor client %q has mismatched ancestry for client %q", parentID, record.clientID)
		}
	}
	return route, nil
}

// telemetryRouteOriginClientID resolves an immutable origin ID through the
// session's validated client record graph. Exporters call this at batch delivery
// time, so routing does not retain or require a client runtime.
func (sess *daggerSession) telemetryRouteOriginClientID(originClientID string) ([]string, error) {
	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()

	record, ok := sess.clientRecords[originClientID]
	if !ok {
		return nil, fmt.Errorf("telemetry origin client %q not found", originClientID)
	}
	route := make([]string, 0, len(record.parentClientIDs)+1)
	route = append(route, originClientID)
	seen := map[string]struct{}{originClientID: {}}
	for _, parentID := range record.parentClientIDs {
		parent, ok := sess.clientRecords[parentID]
		if !ok {
			return nil, fmt.Errorf("telemetry ancestor client %q not found for client %q", parentID, originClientID)
		}
		if _, duplicate := seen[parentID]; duplicate {
			return nil, fmt.Errorf("telemetry route for client %q reaches %q more than once", originClientID, parentID)
		}
		seen[parentID] = struct{}{}
		if len(parent.parentClientIDs) != len(route)-1 || !slices.Equal(parent.parentClientIDs, route[1:]) {
			return nil, fmt.Errorf("telemetry ancestor client %q has mismatched ancestry for client %q", parentID, originClientID)
		}
		route = append(route, parentID)
	}
	return route, nil
}

// telemetryDeliveryClientIDs preserves the delivery-domain key order used by
// call-payload claims: ancestors root-to-direct, followed by the origin.
func (sess *daggerSession) telemetryDeliveryClientIDs(record *clientRecord) ([]string, error) {
	route, err := sess.telemetryRouteClientIDs(record)
	if err != nil {
		return nil, err
	}
	return append(slices.Clone(route[1:]), route[0]), nil
}

// NOTE: be sure to defer closing the DB when done with it, otherwise it may leak
func (record *clientRecord) TelemetryDB(ctx context.Context) (*clientdb.DB, error) {
	return record.daggerSession.telemetryPubSub.srv.clientDBs.Open(ctx, record.clientID)
}

// slowDrainOp flags a shutdown-drain operation that ate a meaningful chunk of
// the drain budget: the CLI allows 10s for the whole shutdown.
const slowDrainOp = 2 * time.Second

func timedProviderOp(ctx context.Context, errs *error, op func(context.Context) error) time.Duration {
	start := time.Now()
	*errs = errors.Join(*errs, op(ctx))
	return time.Since(start)
}

// shutdownTelemetry is the final session telemetry barrier. Callers must first
// stop and drain every producer and complete cleanup that can emit telemetry.
// Metrics flush and shut down first so exporter cleanup telemetry is still
// accepted by the trace/log providers before their final flush and shutdown.
func (sess *daggerSession) shutdownTelemetry(ctx context.Context) error {
	start := time.Now()
	var errs error
	var traceDur, logDur, metricDur time.Duration
	if sess.meterProvider != nil {
		metricDur = timedProviderOp(ctx, &errs, sess.meterProvider.ForceFlush)
		metricDur += timedProviderOp(ctx, &errs, sess.meterProvider.Shutdown)
	}
	if sess.tracerProvider != nil {
		traceDur = timedProviderOp(ctx, &errs, sess.tracerProvider.ForceFlush)
	}
	if sess.loggerProvider != nil {
		logDur = timedProviderOp(ctx, &errs, sess.loggerProvider.ForceFlush)
	}
	if sess.tracerProvider != nil {
		traceDur += timedProviderOp(ctx, &errs, sess.tracerProvider.Shutdown)
	}
	if sess.loggerProvider != nil {
		logDur += timedProviderOp(ctx, &errs, sess.loggerProvider.Shutdown)
	}
	logClientTelemetryOp(slog.With("sessionID", sess.sessionID), "session telemetry shutdown", start, traceDur, logDur, metricDur, errs)
	return errs
}

func logClientTelemetryOp(lg *slog.Logger, what string, start time.Time, traceDur, logDur, metricDur time.Duration, errs error) {
	total := time.Since(start)
	lg = lg.With(
		"duration", total,
		"traces", traceDur,
		"logs", logDur,
		"metrics", metricDur,
		"error", errs,
	)
	switch {
	case total > slowDrainOp:
		lg.Warn("slow " + what)
	case total > 100*time.Millisecond:
		lg.Debug(what)
	default:
		lg.ExtraDebug(what)
	}
}

func (sess *daggerSession) getMainClientCaller(ctx context.Context) (engineutil.SessionCaller, error) {
	return sess.getClientCaller(ctx, sess.mainClientCallerID)
}

func (sess *daggerSession) LoadOrStoreTelemetrySeenKey(key string) bool {
	_, seen := sess.seenKeys.LoadOrStore(key, struct{}{})
	return seen
}

func (sess *daggerSession) StoreTelemetrySeenKey(key string) {
	sess.seenKeys.Store(key, struct{}{})
}

// inflightSessionTelemetryFlushes counts engine-wide concurrent session-level
// telemetry flushes. Every flush fans out to every client in its session, so
// concurrent flushes multiply pressure on the same client stores; the gauge makes
// that amplification visible next to each flush's duration.
var inflightSessionTelemetryFlushes atomic.Int64

func (sess *daggerSession) FlushTelemetry(ctx context.Context, reason string) error {
	inflight := inflightSessionTelemetryFlushes.Add(1)
	defer inflightSessionTelemetryFlushes.Add(-1)

	sess.clientMu.RLock()
	records := len(sess.clientRecords)
	sess.clientMu.RUnlock()

	lg := slog.With(
		"sessionID", sess.sessionID,
		"reason", reason,
		"clients", records,
		"inflightSessionFlushes", inflight)
	lg.Debug("flushing session telemetry")

	start := time.Now()
	var errs error
	var traceDur, logDur, metricDur time.Duration
	if sess.tracerProvider != nil {
		traceDur = timedProviderOp(ctx, &errs, sess.tracerProvider.ForceFlush)
	}
	if sess.loggerProvider != nil {
		logDur = timedProviderOp(ctx, &errs, sess.loggerProvider.ForceFlush)
	}
	if sess.meterProvider != nil {
		metricDur = timedProviderOp(ctx, &errs, sess.meterProvider.ForceFlush)
	}
	logClientTelemetryOp(lg, "session telemetry flush", start, traceDur, logDur, metricDur, errs)
	return errs
}

const metricReaderInterval = 5 * time.Second

func (srv *Server) initializeSessionTelemetry(sess *daggerSession) {
	spanExporter := sessionSpanExporter{sess: sess, ps: srv.telemetryPubSub}
	logExporter := sessionLogExporter{sess: sess, ps: srv.telemetryPubSub}
	metricExporter := sessionMetricExporter{sess: sess, ps: srv.telemetryPubSub}
	sess.spanExporter = spanExporter
	sess.logExporter = logExporter
	sess.metricExporter = metricExporter

	// Keep the raised link limit used by wcprof wait edges. One bounded trace
	// queue, one bounded log queue, and one periodic metric reader serve the
	// entire session.
	spanLimits := sdktrace.NewSpanLimits()
	spanLimits.LinkCountLimit = 16384
	tracerOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithRawSpanLimits(spanLimits),
		sdktrace.WithSpanProcessor(dagql.NewWcprofLazyParentProcessor()),
		sdktrace.WithSpanProcessor(srv.wcprofSpanCount),
		// Stamp origin before the live processor freezes its start snapshot.
		sdktrace.WithSpanProcessor(telemetryOriginSpanProcessor{sessionID: sess.sessionID}),
		sdktrace.WithSpanProcessor(enginetel.NewLargeQueueLiveSpanProcessor(spanExporter)),
	}
	loggerOpts := []sdklog.LoggerProviderOption{
		sdklog.WithResource(telemetry.Resource),
		// Stamp origin before the batch processor copies the record.
		sdklog.WithProcessor(telemetryOriginLogProcessor{sessionID: sess.sessionID}),
		sdklog.WithProcessor(enginetel.NewLogBatchProcessor(logExporter)),
	}
	sess.tracerProvider = sdktrace.NewTracerProvider(tracerOpts...)
	sess.loggerProvider = sdklog.NewLoggerProvider(loggerOpts...)
	sess.meterProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(telemetry.Resource),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			metricExporter,
			sdkmetric.WithInterval(metricReaderInterval),
		)),
	)
	sess.telemetryDebug = LifecycleTelemetryCounts{
		TracerProviders:          1,
		LoggerProviders:          1,
		MeterProviders:           1,
		ConfiguredSpanProcessors: 4,
		ConfiguredLogProcessors:  2,
		ConfiguredMetricReaders:  1,
		ConfiguredSpanQueueSlots: enginetel.LargeSpanQueueSize,
		ConfiguredLogQueueSlots:  enginetel.LogQueueSize,
	}
}

// requires that sess.lifecycleMu is held
func (srv *Server) initializeDaggerSession(
	clientMetadata *engine.ClientMetadata,
	sess *daggerSession,
	failureCleanups *cleanups.Cleanups,
) error {
	slog.Info("initializing new session", "session", clientMetadata.SessionID)
	defer slog.Debug("initialized new session", "session", clientMetadata.SessionID)

	// NOTE: sessionID, mainClientCallerID and the clients map are set at
	// construction (before the session is published) and are immutable /
	// clientMu-protected thereafter; they are deliberately not assigned here.
	sess.wcprofEnabled = clientMetadata.Profile
	sess.attachables = newSessionAttachableManager()
	sess.endpoints = map[string]http.Handler{}
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	sess.shutdownCh = make(chan struct{})
	sess.services = core.NewServices()
	sess.agents = core.NewAgentRuntimes()
	sess.authProvider = auth.NewRegistryAuthProvider()
	sess.resolver = serverresolver.New(serverresolver.Opts{
		Hosts: srv.registryHosts,
		Auth: serverresolver.NewSessionAuthSource(
			sess.authProvider,
			func(ctx context.Context) (*grpc.ClientConn, error) {
				return srv.sessionMainClientConn(ctx, sess)
			},
		),
		ContentStore: srv.contentStore,
		LeaseManager: srv.leaseManager,
	})
	failureCleanups.Add("close session resolver", sess.resolver.Close)
	sess.containers = map[bkgw.Container]struct{}{}
	sess.dagqlCond = sync.NewCond(&sess.dagqlMu)
	sess.telemetryPubSub = srv.telemetryPubSub
	srv.initializeSessionTelemetry(sess)
	failureCleanups.Add("shutdown session telemetry", func() error {
		return sess.shutdownTelemetry(context.Background())
	})
	sess.interactive = clientMetadata.Interactive
	sess.interactiveCommand = clientMetadata.InteractiveCommand
	sess.allowedLLMModules = clientMetadata.AllowedLLMModules

	sess.analytics = analytics.New(analytics.Config{
		DoNotTrack: clientMetadata.DoNotTrack || analytics.DoNotTrack(),
		Labels:     sessionAnalyticsLabels(clientMetadata, srv.engineName),
	})
	failureCleanups.Add("close session analytics", sess.analytics.Close)

	// NOTE: state is NOT set to sessionStateInitialized here. getOrInitClient
	// performs that atomic transition as its last step, after the main client
	// record is published, so observers never see initialized session resources
	// without their root identity record.
	return nil
}

func sessionAnalyticsLabels(clientMetadata *engine.ClientMetadata, engineName string) enginetel.Labels {
	return enginetel.NewLabels(maps.Clone(clientMetadata.Labels), nil, nil).
		WithEngineLabel(engineName).
		WithServerLabels(
			engine.Version,
			runtime.GOOS,
			runtime.GOARCH,
			false,
		)
}

var errSessionClosing = errors.New("session is closing")

func (sess *daggerSession) beginClosing() {
	sess.closeClosingOnce.Do(func() {
		if sess.cancelClosing != nil {
			sess.cancelClosing(errSessionClosing)
		}
	})
}

func (sess *daggerSession) withClosingCancel(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-sess.closingCtx.Done():
			cancel(context.Cause(sess.closingCtx))
		case <-ctx.Done():
		}
	}()
	return ctx
}

// requires that sess.lifecycleMu is held.
//
// removeDaggerSession does NOT remove the session from srv.daggerSessions; it
// leaves it in place as a "removed" tombstone so observers see the removed state
// and a concurrent same-id getOrInitClient bails out instead of resurrecting the
// session while its cache is still being released. The caller must call
// deleteSession (after releasing lifecycleMu) to drop the tombstone once
// teardown is complete.
func (srv *Server) removeDaggerSession(ctx context.Context, sess *daggerSession) error {
	slog := slog.With("session", sess.sessionID)

	slog.Info("removing session; stopping client services and flushing")
	defer slog.Debug("session removed")

	// Publish the removed state first (atomic) so observers holding a stale
	// snapshot pointer, and any concurrent same-id getOrInitClient, observe it
	// immediately and skip/bail instead of using a tearing-down session. The
	// session is intentionally left in srv.daggerSessions as a tombstone until
	// the caller drops it via deleteSession after lifecycleMu is released.
	sess.markSessionRemoved()
	sess.beginClientScopeTeardown()
	sess.beginClosing()

	// check if the local cache needs pruning after session is removed, prune if so
	defer func() {
		if srv.isShuttingDown() {
			return
		}
		time.AfterFunc(time.Second, srv.throttledSessionGC)
	}()

	var errs error

	// in theory none of this should block very long, but add a safeguard just in case
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	if err := sess.services.StopSessionServices(ctx, sess.sessionID); err != nil {
		slog.Warn("error stopping services", "error", err)
		errs = errors.Join(errs, fmt.Errorf("stop client services: %w", err))
	}

	slog.Debug("stopped services")

	if sess.agents != nil {
		if err := sess.agents.KillAll(ctx, errors.New("session closed")); err != nil {
			slog.Warn("error stopping agents", "error", err)
			errs = errors.Join(errs, fmt.Errorf("stop session agents: %w", err))
		}
	}

	if sess.resolver != nil {
		errs = errors.Join(errs, sess.resolver.Close())
		sess.resolver = nil
	}

	// Drain in-flight queries before declaring the wcprof completeness count and
	// before shutting telemetry down below: it makes the per-trace span count EXACT
	// (no query is still creating spans) and means a late query's telemetry is
	// recorded before its provider closes rather than lost.
	sess.dagqlMu.Lock()
	sess.dagqlClosing = true
	for sess.dagqlInFlight > 0 {
		sess.dagqlCond.Wait()
	}
	sess.dagqlMu.Unlock()

	// Release containers and buildkit state before the final telemetry barrier.
	// Their cleanup paths may still emit spans, logs, and metrics.

	var releaseGroup errgroup.Group
	sess.containersMu.Lock()
	for ctr := range sess.containers {
		if ctr != nil {
			releaseGroup.Go(func() error {
				return ctr.Release(ctx)
			})
		}
	}
	// Keep the container set closed to mutation until all captured containers have
	// released, then drop the lock before later cleanup and telemetry barriers.
	releaseErr := releaseGroup.Wait()
	sess.containersMu.Unlock()
	errs = errors.Join(errs, releaseErr)

	// cleanup analytics and telemetry
	errs = errors.Join(errs, sess.analytics.Close())

	// (queries were already drained above, before the completeness stamp + telemetry
	// shutdown, so dagql is quiescent here for the cache release.)

	beforeDagqlEntries := srv.engineCache.Size()
	beforeDagqlStats := srv.engineCache.EntryStats()
	if err := srv.engineCache.ReleaseSession(ctx, sess.sessionID); err != nil {
		slog.Error("error releasing dagql cache", "error", err)
		errs = errors.Join(errs, fmt.Errorf("release dagql cache: %w", err))
	}
	afterDagqlEntries := srv.engineCache.Size()
	afterDagqlStats := srv.engineCache.EntryStats()
	if afterDagqlEntries != beforeDagqlEntries {
		slog.Debug(
			"released dagql cache refs for session",
			"beforeEntries", beforeDagqlEntries,
			"afterEntries", afterDagqlEntries,
			"beforeRetainedCalls", beforeDagqlStats.RetainedCalls,
			"afterRetainedCalls", afterDagqlStats.RetainedCalls,
		)
	} else {
		slog.Debug(
			"session dagql cache release did not change base cache size",
			"entries", afterDagqlEntries,
			"retainedCalls", afterDagqlStats.RetainedCalls,
		)
	}

	// Every telemetry-producing request, background runtime, container,
	// analytics hook, and cache cleanup is now stopped. Stamp the exact final
	// wcprof count, then make metric/trace/log ForceFlush+Shutdown the final
	// delivery barrier for the session-owned providers.
	srv.stampSessionComplete(ctx, sess)
	srv.wcprofSpanCount.Reap(sess.wcprofTraceID)
	errs = errors.Join(errs, sess.shutdownTelemetry(ctx))

	// ensure this chan is closed even if the client never explicitly called the /shutdown endpoint
	sess.closeShutdownOnce.Do(func() {
		close(sess.shutdownCh)
	})

	return errs
}

// deleteSession drops a session from the registry, but only if the map entry is
// still this exact session pointer. Pointer-conditional deletion ensures a slow
// teardown can't delete a freshly created same-id session. Call only after the
// session's teardown is complete and lifecycleMu has been released.
func (srv *Server) deleteSession(sess *daggerSession) {
	srv.daggerSessionsMu.Lock()
	if srv.daggerSessions[sess.sessionID] == sess {
		delete(srv.daggerSessions, sess.sessionID)
	}
	srv.daggerSessionsMu.Unlock()
}

// stampSessionComplete declares the EXACT engine span total for the session's trace
// (the wcprof completeness checksum). It is called from teardown once
// the session's queries are drained and its services stopped, so the counter is at
// its final value. It writes that total on a dedicated carrier span — parented at
// the session-root span recorded in serveQuery so it lands in this trace, named
// wcprofSessionCompleteSpanName so the counter excludes it from the total and the
// loader drops it from the compiled ops. The session's final trace/log barrier
// flushes the carrier after all cleanup producers have stopped. A trace that
// never ran a traced main query, or whose count is zero, gets no carrier
// and so fails the loader's gate by default (unverifiable → refused).
func (srv *Server) stampSessionComplete(ctx context.Context, sess *daggerSession) {
	if !sess.wcprofTraceID.IsValid() || !sess.wcprofRootSpanID.IsValid() {
		return
	}
	n := srv.wcprofSpanCount.Final(sess.wcprofTraceID)
	if n == 0 {
		return
	}
	sess.clientMu.RLock()
	mainRecord := sess.clientRecords[sess.mainClientCallerID]
	sess.clientMu.RUnlock()
	if mainRecord == nil || sess.tracerProvider == nil {
		return
	}
	// Parent the carrier at the recorded session-root span so it inherits this
	// trace (a teardown ctx carries no span); the loader excludes it from ops, but a
	// real parent also keeps it from ever reading as an orphaned-parent false root.
	parentCtx := trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    sess.wcprofTraceID,
		SpanID:     sess.wcprofRootSpanID,
		TraceFlags: trace.FlagsSampled,
	}))
	parentCtx = engine.ContextWithClientMetadata(parentCtx, mainRecord.clientMetadata)
	_, span := sess.tracerProvider.Tracer(InstrumentationLibrary).Start(
		parentCtx, wcprofSessionCompleteSpanName,
		trace.WithAttributes(
			attribute.Bool(telemetryattrs.WcprofSessionCompleteAttr, true),
			attribute.String(telemetryattrs.WcprofSessionSpanCountAttr, strconv.Itoa(n)),
			// The carrier is protocol bookkeeping, not a unit of work: keep it
			// out of rendered trees (TUI and Cloud); the loader reads its
			// attributes regardless.
			attribute.Bool(telemetry.UIInternalAttr, true),
		),
	)
	span.End()
}

type ClientInitOpts struct {
	*engine.ClientMetadata

	// NestedTransport proves that a nested proxy was explicitly registered
	// before serving. It is nil only for intentional root transports.
	NestedTransport *engine.NestedClientTransport

	// ParentClientID names the client that delegated reachability to this nested
	// client. It is required for nested clients and forbidden for explicit roots.
	ParentClientID string

	// RootClient explicitly marks a session/root transport. Empty parent IDs do
	// not implicitly create roots.
	RootClient bool

	// BootstrapOnly allows registration-adjacent attachable, /init, and shutdown
	// requests to reconcile metadata without constructing the executable runtime.
	// The first non-bootstrap request seals metadata before initialization.
	BootstrapOnly bool

	// requestOwner and requestLeaseOut connect HTTP admission to the request
	// scope without opening a second lease after the serialization point.
	requestOwner    string
	requestLeaseOut **engine.ClientLifecycleLease

	// If set, host-backed services for this client may proxy through this
	// ancestor when this client has no session attachables of its own.
	HostServiceProxyClientID string

	// If the client is running from a function in a module, this is that module.
	ModuleContext dagql.ObjectResult[*core.Module]

	// If the client is running from a function in a module, this is that function call.
	FunctionCall *core.FunctionCall
}

// initializeSessionEngineClient publishes the one engine gateway shared by all
// client runtimes in a session. Caller selection comes from ClientMetadata in
// context and resolves only through session records and attachables; the gateway
// never closes over a client runtime.
//
// requires that sess.lifecycleMu is held
func (srv *Server) initializeSessionEngineClient(ctx context.Context, sess *daggerSession) error {
	if sess.engineUtilClient != nil {
		return nil
	}

	var callerG singleflight.Group[string, engineutil.SessionCaller]
	sess.getClientCaller = func(ctx context.Context, id string) (engineutil.SessionCaller, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		caller, _, err := callerG.Do(ctx, id, func(ctx context.Context) (engineutil.SessionCaller, error) {
			caller, _, err := srv.clientAttachableCaller(ctx, sess.sessionID, id, false)
			return caller, err
		})
		return caller, err
	}

	dialer := &net.Dialer{}
	dialer.Resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if len(srv.dns.Nameservers) == 0 {
				return nil, errors.New("no nameservers configured")
			}

			var errs []error
			for _, ns := range srv.dns.Nameservers {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
				if err != nil {
					errs = append(errs, err)
					continue
				}

				return conn, nil
			}

			return nil, errors.Join(errs...)
		},
	}

	engineUtilOpts := *srv.engineUtilOpts
	engineUtilOpts.Dialer = dialer
	engineUtilOpts.GetClientCaller = sess.getClientCaller
	engineUtilOpts.GetHostServiceCaller = sess.resolveHostServiceCaller
	engineUtilOpts.GetMainClientCaller = sess.getMainClientCaller
	engineUtilOpts.GetRegistryResolver = srv.RegistryResolver
	engineUtilOpts.Interactive = sess.interactive
	engineUtilOpts.InteractiveCommand = sess.interactiveCommand
	engineUtilClient, err := engineutil.NewClient(ctx, &engineUtilOpts)
	if err != nil {
		return fmt.Errorf("failed to create session engine client: %w", err)
	}
	sess.engineUtilClient = engineUtilClient
	return nil
}

// requires that client.stateMu is held
func (srv *Server) initializeClientRuntime(
	ctx context.Context,
	client *clientRuntime,
	opts *ClientInitOpts,
) error {
	slog := slog.With(
		"isMainClient", client.clientID == client.daggerSession.mainClientCallerID,
		"sessionID", client.daggerSession.sessionID,
		"clientID", client.clientID,
		"mainClientID", client.daggerSession.mainClientCallerID,
	)
	slog.Info("initializing new client")
	client.hostServiceProxyClientID = opts.HostServiceProxyClientID
	if err := srv.initializeSessionEngineClient(ctx, client.daggerSession); err != nil {
		return err
	}

	client.fnCall = opts.FunctionCall

	// setup the graphql server + module/function state for the client
	client.dagqlRoot = core.NewRoot(srv)
	ctx = dagql.ContextWithOperationLeaseProvider(ctx, dagql.OperationLeaseProviderFunc(func(ctx context.Context) (context.Context, func(context.Context) error, error) {
		if leaseID, ok := leases.FromContext(ctx); ok && leaseID != "" {
			return ctx, func(context.Context) error { return nil }, nil
		}
		return bkcache.WithLazyLease(ctx, srv.leaseManager, bkcache.MakeTemporary)
	}))
	// make query available via context to all APIs
	ctx = core.ContextWithQuery(ctx, client.dagqlRoot)

	coreSchemaBase, err := srv.getCoreSchemaBase(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize core schema base: %w", err)
	}
	coreView := call.View(engine.BaseVersion(engine.NormalizeVersion(client.clientVersion)))
	client.dag, err = coreSchemaBase.Fork(ctx, client.dagqlRoot, coreView)
	if err != nil {
		return fmt.Errorf("failed to fork core schema base: %w", err)
	}
	coreMod := coreSchemaBase.CoreMod(coreView)
	client.defaultDeps = core.NewSchemaBuilder(client.dagqlRoot, []core.Mod{coreMod})
	client.servedMods = core.NewSchemaBuilder(client.dagqlRoot, []core.Mod{coreMod})

	if opts.ModuleContext.Self() != nil {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return fmt.Errorf("failed to get engine cache for module context: %w", err)
		}

		attached, err := cache.AttachResult(ctx, opts.SessionID, client.dag, opts.ModuleContext)
		if err != nil {
			return fmt.Errorf("attach module context during client init: %w", err)
		}
		modInst, ok := attached.(dagql.ObjectResult[*core.Module])
		if !ok {
			return fmt.Errorf("attach module context during client init: expected %T, got %T", opts.ModuleContext, attached)
		}
		client.mod = modInst

		// this is needed to set the view of the core api as compatible
		// with the module we're currently calling from
		engineVersion := client.mod.Self().Source.Value.Self().EngineVersion
		coreView = call.View(engine.BaseVersion(engine.NormalizeVersion(engineVersion)))
		client.dag.View = coreView
		coreMod = coreSchemaBase.CoreMod(coreView)

		client.defaultDeps = core.NewSchemaBuilder(client.dagqlRoot, []core.Mod{coreMod})
		client.servedMods = client.mod.Self().Deps.WithRoot(client.dagqlRoot)
		if len(client.mod.Self().ObjectDefs) > 0 {
			client.servedMods = client.servedMods.Append(core.NewUserMod(client.mod))
		}
	} else {
		client.pendingWorkspaceLoad = true
		if clientMD := client.clientMetadata; clientMD != nil && len(clientMD.ExtraModules) > 0 {
			client.pendingExtraModules = clientMD.ExtraModules
		}
	}

	// Telemetry providers, queues, metric aggregation, and the periodic reader are
	// session-owned. These bound exporters stamp telemetry delivered without an
	// emission context (cloud scale-out) with this client's immutable origin
	// before using the same session router.
	client.spanExporter = originSpanExporter{
		origin: client.clientID,
		next:   client.daggerSession.spanExporter,
	}
	client.logExporter = originLogExporter{
		origin: client.clientID,
		next:   client.daggerSession.logExporter,
	}

	client.state = clientStateInitialized
	return nil
}

func (sess *daggerSession) resolveHostServiceCaller(
	ctx context.Context,
	id string,
) (engineutil.SessionCaller, error) {
	// Synthetic nested clients (for example builtin Dang evaluation) do not
	// establish their own session attachables. Resolve their explicit proxy
	// through immutable ancestry records so this routing does not retain or
	// require any client runtime.
	sess.clientMu.RLock()
	record := sess.clientRecords[id]
	sess.clientMu.RUnlock()

	var hostServiceProxyClientID string
	var parentClientIDs []string
	if record != nil {
		// hostServiceProxyClientID is initialized under scopeMu. Do not hold
		// clientMu while taking scopeMu: runtime publication takes them in the
		// opposite order.
		sess.scopeMu.Lock()
		hostServiceProxyClientID = record.hostServiceProxyClientID
		parentClientIDs = slices.Clone(record.parentClientIDs)
		sess.scopeMu.Unlock()
	}

	if hostServiceProxyClientID != "" {
		if sess.attachables != nil {
			if caller, ok := sess.attachables.Lookup(id); ok {
				return caller, nil
			}
		}

		for i := len(parentClientIDs) - 1; i >= 0; i-- {
			if parentClientIDs[i] == hostServiceProxyClientID {
				return sess.resolveHostServiceCaller(ctx, hostServiceProxyClientID)
			}
		}
		return nil, fmt.Errorf("host service proxy client %q not found for client %q", hostServiceProxyClientID, id)
	}

	if sess.getClientCaller == nil {
		return nil, fmt.Errorf("session client caller gateway is not initialized")
	}
	return sess.getClientCaller(ctx, id)
}

func (srv *Server) sessionFromID(sessID string) (*daggerSession, error) {
	if sessID == "" {
		return nil, fmt.Errorf("missing session ID")
	}
	srv.daggerSessionsMu.RLock()
	sess, ok := srv.daggerSessions[sessID]
	srv.daggerSessionsMu.RUnlock()
	if !ok {
		return nil, flightcontrol.RetryableError{Err: fmt.Errorf("session %q not found", sessID)}
	}

	// Purpose-specific lookups remain lock-free with respect to lifecycleMu so
	// observers cannot block behind initialization or teardown.
	switch st := sess.state.Load(); st {
	case sessionStateInitialized:
		// continue
	case sessionStateRemoved:
		return nil, flightcontrol.RetryableError{Err: fmt.Errorf("session %q not found", sessID)}
	case sessionStateUninitialized:
		return nil, fmt.Errorf("session %q not initialized", sessID)
	default:
		return nil, fmt.Errorf("session %q has unknown state %s", sessID, st)
	}
	return sess, nil
}

// clientRecordFromIDs resolves stable client identity without consulting or
// retaining executable runtime state.
func (srv *Server) clientRecordFromIDs(sessID, clientID string) (*clientRecord, error) {
	if clientID == "" {
		return nil, fmt.Errorf("missing client ID")
	}
	sess, err := srv.sessionFromID(sessID)
	if err != nil {
		return nil, err
	}
	sess.clientMu.RLock()
	record, ok := sess.clientRecords[clientID]
	sess.clientMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("client record %q not found", clientID)
	}
	if sess.state.Load() == sessionStateRemoved {
		return nil, flightcontrol.RetryableError{Err: fmt.Errorf("session %q not found", sessID)}
	}
	return record, nil
}

// clientRuntimeForRecord resolves retained execution state after the caller has
// deliberately selected the runtime path. It does not acquire ownership; entry
// points that execute work must validate a ClientScope first.
func (sess *daggerSession) clientRuntimeForRecord(record *clientRecord) (*clientRuntime, error) {
	sess.clientMu.RLock()
	runtime, ok := sess.clientRuntimes[record.clientID]
	sess.clientMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("client runtime %q not retained", record.clientID)
	}
	if runtime.clientRecord != record {
		return nil, fmt.Errorf("client runtime %q does not match its record", record.clientID)
	}
	return runtime, nil
}

// executableClientFromContext requires both a retained runtime and a valid held
// ClientScope from that exact session. Metadata alone is never execution
// authority.
func (srv *Server) executableClientFromContext(ctx context.Context) (*clientRuntime, error) {
	scope, ok := engine.ClientScopeFromContext(ctx)
	if !ok {
		return nil, errors.New("executable client lookup requires a client scope")
	}
	record, err := srv.clientRecordFromIDs(scope.SessionID(), scope.ClientID())
	if err != nil {
		return nil, err
	}
	runtime, err := record.daggerSession.clientRuntimeForRecord(record)
	if err != nil {
		return nil, err
	}
	sess := runtime.daggerSession
	sess.scopeMu.Lock()
	valid := scope.CanDelegateTo(sess.scopeAuthority)
	sess.scopeMu.Unlock()
	if !valid {
		return nil, fmt.Errorf("client scope for %q/%q is not held by the current session", scope.SessionID(), scope.ClientID())
	}
	return runtime, nil
}

func (srv *Server) clientRecordFromContext(ctx context.Context) (*clientRecord, error) {
	metadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata for session call: %w", err)
	}
	return srv.clientRecordFromIDs(metadata.SessionID, metadata.ClientID)
}

// initialize session+client if needed, return:
// * the initialized client
// * a cleanup func to run when the call is done
//
//nolint:gocyclo // session/client initialization is an intentionally linear state machine.
func (srv *Server) getOrInitClient(
	ctx context.Context,
	opts *ClientInitOpts,
) (_ *clientRuntime, _ func() error, rerr error) {
	if opts == nil || opts.ClientMetadata == nil {
		return nil, nil, errors.New("client metadata is required")
	}
	metadata, err := cloneClientMetadata(opts.ClientMetadata)
	if err != nil {
		return nil, nil, err
	}
	optsCopy := *opts
	optsCopy.ClientMetadata = metadata
	opts = &optsCopy

	if srv.isShuttingDown() {
		return nil, nil, errServerShuttingDown
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		return nil, nil, fmt.Errorf("session ID is required")
	}
	clientID := opts.ClientID
	if clientID == "" {
		return nil, nil, fmt.Errorf("client ID is required")
	}
	token := opts.ClientSecretToken
	if token == "" {
		return nil, nil, fmt.Errorf("client secret token is required")
	}

	// cleanup to do if this method fails; the failure handler that runs these is
	// installed below, once we hold the session's lifecycleMu (so it can publish
	// the removed tombstone before unlocking, then drop it only after cleanups).
	failureCleanups := &cleanups.Cleanups{}

	// get or initialize the session as a whole

	srv.daggerSessionsMu.Lock()
	if srv.isShuttingDown() {
		srv.daggerSessionsMu.Unlock()
		return nil, nil, errServerShuttingDown
	}
	sess, sessionExists := srv.daggerSessions[sessionID]
	createdSession := false
	if !sessionExists {
		// Construct with immutable identity and a non-nil clients map, and lock
		// the new session's lifecycleMu BEFORE publishing it, so this goroutine
		// is guaranteed to be the session's initializer: any concurrent caller
		// that finds the published-but-uninitialized session blocks on
		// lifecycleMu until initialization completes (or sees the removed
		// tombstone if init fails). The session is still unreachable here, so this
		// acquisition never contends and can't invert the lock order even though
		// we currently hold daggerSessionsMu (the one "unpublished object"
		// exception to "lifecycleMu and daggerSessionsMu are never nested").
		sess = &daggerSession{
			sessionID:          sessionID,
			mainClientCallerID: clientID,
			clientRecords:      map[string]*clientRecord{},
			clientRuntimes:     map[string]*clientRuntime{},
		}
		sess.lifecycleMu.Lock()
		createdSession = true
		srv.daggerSessions[sessionID] = sess
	}
	srv.daggerSessionsMu.Unlock()

	if !createdSession {
		// Fast, lock-free check: if the session is already a removed tombstone,
		// bail immediately rather than blocking on lifecycleMu for the (possibly
		// ~60s) teardown. A same-id reconnect then retries and gets a fresh
		// session once the tombstone is dropped.
		if sess.state.Load() == sessionStateRemoved {
			return nil, nil, flightcontrol.RetryableError{Err: fmt.Errorf("session %q removed", sessionID)}
		}
		sess.lifecycleMu.Lock()
	}

	// We now hold sess.lifecycleMu. The deferred handler below manages the unlock
	// for both success and failure. On failure of a session WE created it:
	// publishes the removed tombstone (while still holding lifecycleMu, so a
	// waiting same-id caller observes removed and bails instead of resurrecting a
	// half-built session), releases the lock, runs resource cleanups, and only
	// THEN drops the tombstone from the registry — so no fresh same-id session can
	// be created while this one's resources are still being released.
	lifecycleHeld := true
	unlockLifecycle := func() {
		if lifecycleHeld {
			lifecycleHeld = false
			sess.lifecycleMu.Unlock()
		}
	}
	defer func() {
		if rerr == nil {
			unlockLifecycle()
			return
		}
		if createdSession {
			sess.markSessionRemoved()
		}
		unlockLifecycle()
		rerr = errors.Join(rerr, failureCleanups.Run())
		// No engineCache.ReleaseSession is needed here: session-scoped cache refs
		// are only attached (via AttachResult) during nested-client init carrying
		// Env/Module context, which always targets an already-existing session
		// (createdSession=false). In current in-tree call paths a created session
		// is always a main-client session that has attached nothing session-scoped,
		// so there is nothing to release before dropping the tombstone.
		if createdSession {
			srv.deleteSession(sess)
		}
	}()

	switch sess.state.Load() {
	case sessionStateUninitialized:
		if err := srv.initializeDaggerSession(opts.ClientMetadata, sess, failureCleanups); err != nil {
			return nil, nil, fmt.Errorf("initialize session: %w", err)
		}
	case sessionStateInitialized:
		// nothing to do
	case sessionStateRemoved:
		return nil, nil, flightcontrol.RetryableError{Err: fmt.Errorf("session %q removed", sessionID)}
	}

	// Get or create the client record. Explicitly registered nested records and
	// root bootstrap requests may publish an uninitialized record; executable
	// state is only published after metadata seals under scopeMu. lifecycleMu
	// prevents concurrent creation of the same record.
	sess.clientMu.RLock()
	record, recordExists := sess.clientRecords[clientID]
	client, runtimeExists := sess.clientRuntimes[clientID]
	sess.clientMu.RUnlock()
	if recordExists && !runtimeExists {
		// Quiescent records remain session-long and are permanent closed-ID
		// tombstones. They can route metadata/telemetry but can never republish an
		// executable runtime.
		return nil, nil, fmt.Errorf("client %q is permanently closed", clientID)
	}
	if !recordExists && runtimeExists {
		return nil, nil, fmt.Errorf("client %q runtime is published without its record", clientID)
	}
	clientExists := recordExists
	if clientExists && client.clientRecord != record {
		return nil, nil, fmt.Errorf("client %q runtime does not match its record", clientID)
	}

	parentClientIDs, err := sess.parentClientIDsForRegistration(clientID, opts.ParentClientID, opts.RootClient)
	if err != nil {
		return nil, nil, fmt.Errorf("validate client ancestry: %w", err)
	}

	// Nested clients must already have an explicitly registered transport. The
	// scope lock is also the metadata bootstrap/seal serialization point for all
	// clients. Holding it through first initialization makes metadata seal,
	// runtime publication, transport close, and session teardown one ordering.
	if !opts.RootClient {
		if opts.NestedTransport == nil {
			return nil, nil, fmt.Errorf("nested client %q has no registered transport handle", clientID)
		}
		if !clientExists {
			return nil, nil, fmt.Errorf("nested client %q transport is not registered", clientID)
		}
	}

	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()

	if !clientExists {
		record = &clientRecord{
			daggerSession:   sess,
			clientID:        clientID,
			shutdownCh:      make(chan struct{}),
			parentClientIDs: parentClientIDs,
			accepting:       true,
		}
		client = &clientRuntime{
			clientRecord:    record,
			state:           clientStateUninitialized,
			lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord),
		}
	} else if !record.accepting {
		return nil, nil, fmt.Errorf("client %q is closed", clientID)
	}
	if opts.NestedTransport != nil {
		if client.nestedTransport != opts.NestedTransport {
			return nil, nil, fmt.Errorf("nested client %q transport ownership does not match", clientID)
		}
		if client.transportLease == nil || !client.transportLease.Held() || opts.NestedTransport.Closed() {
			return nil, nil, fmt.Errorf("nested client %q transport is closed", clientID)
		}
	}
	if err := sess.mergeClientMetadataLocked(client.clientRecord, opts.ClientMetadata); err != nil {
		return nil, nil, err
	}
	if client.clientMetadata.ClientSecretToken != token {
		return nil, nil, fmt.Errorf("client %q registered with different secret token", clientID)
	}
	if opts.HostServiceProxyClientID != "" && client.hostServiceProxyClientID != "" && client.hostServiceProxyClientID != opts.HostServiceProxyClientID {
		return nil, nil, fmt.Errorf("client %q already exists with different host service proxy client %q", clientID, client.hostServiceProxyClientID)
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	switch client.state {
	case clientStateUninitialized:
		if !opts.BootstrapOnly {
			if err := sess.sealClientMetadataLocked(client.clientRecord); err != nil {
				return nil, nil, fmt.Errorf("seal client metadata: %w", err)
			}
			if err := srv.initializeClientRuntime(ctx, client, opts); err != nil {
				return nil, nil, fmt.Errorf("initialize client: %w", err)
			}
		}
	case clientStateInitialized:
		// Metadata was reconciled above against the sealed snapshot. Identical
		// replay is accepted; completion or conflict after seal is rejected.
	}

	if opts.RootClient && !clientExists {
		// Root records and transport ownership are published on their first
		// bootstrap request. Unlike executable request scopes, the transport lease
		// carries no metadata snapshot, so adjacent /init metadata may still merge
		// before the first query seals the record.
		sess.clientMu.Lock()
		sess.clientRecords[clientID] = record
		sess.clientRuntimes[clientID] = client
		sess.clientMu.Unlock()
		client.transportLease = sess.newClientLifecycleLeaseLocked(client, engine.ClientLeaseTransport, clientID)
	}

	if opts.NestedTransport != nil && opts.NestedTransport.Closed() {
		// The proxy-side marker may win while its serialized callback is waiting
		// for scopeMu. Publish the same monotonic record transition before
		// rejecting; the callback will idempotently release transport ownership.
		sess.markClientClosedLocked(client.clientRecord)
		return nil, nil, fmt.Errorf("nested client %q transport closed during initialization", clientID)
	}

	// Every admitted HTTP connection, including bootstrap-only requests, owns a
	// typed request lease before the serialization lock is released. Executable
	// requests install this exact lease in ClientScope; there is no close/new-
	// acquire gap in which a runtime could be reclaimed under an accepted handler.
	requestOwner := opts.requestOwner
	if requestOwner == "" {
		requestOwner = "client connection"
	}
	requestLease := sess.newClientLifecycleLeaseLocked(client, engine.ClientLeaseRequest, requestOwner)
	if opts.requestLeaseOut != nil {
		*opts.requestLeaseOut = requestLease
	}

	// increment the number of active connections from this client
	client.activeCount++

	// If this call initialized the session, mark it initialized now — as the
	// LAST step, after the main client record has been published — so observers
	// never see initialized session resources without the root identity.
	if sess.state.Load() == sessionStateUninitialized {
		sess.state.Store(sessionStateInitialized)
	}

	var (
		cleanupOnce sync.Once
		cleanupErr  error
	)
	return client, func() error {
		cleanupOnce.Do(func() {
			cleanupErr = srv.releaseClientConnection(ctx, sess, client)
			requestLease.Release()
		})
		return cleanupErr
	}, nil
}

// releaseClientConnection is the per-request cleanup for a connection obtained
// via getOrInitClient. When the main client's last connection closes it only
// SCHEDULES teardown (reapDaggerSession) rather than running it: this cleanup
// runs in the request handler before the response is flushed — typically the
// main client's /shutdown POST — and the client enforces a hard budget
// (default 10s) on that response, while teardown is unbounded (the dagql cache
// release alone is proportional to the session's cache footprint).
func (srv *Server) releaseClientConnection(ctx context.Context, sess *daggerSession, client *clientRuntime) error {
	client.stateMu.Lock()
	client.activeCount--
	activeCount := client.activeCount
	client.stateMu.Unlock()

	if activeCount > 0 {
		return nil
	}

	slog.With(
		"sessionID", sess.sessionID,
		"clientID", client.clientID,
	).Info("all client connections closed")

	if client.clientID != sess.mainClientCallerID {
		return nil
	}

	// The teardown decision itself is (re-)made inside reapDaggerSession under
	// lifecycleMu, so a concurrent getOrInitClient (which bumps activeCount
	// under lifecycleMu) either lands before the reap and aborts it, or
	// observes the removed tombstone and retries against a fresh session.
	go srv.reapDaggerSession(context.WithoutCancel(ctx), sess, client)
	return nil
}

// reapDaggerSession tears down a session in the background after the main
// client's last connection closed. It re-checks the teardown conditions under
// lifecycleMu because the session may have changed since the disconnect that
// scheduled it: a reconnected main client abandons the reap (the next last
// disconnect schedules a fresh one), and an already-removed session (a
// concurrent reap or GracefulStop won the race) is left alone.
func (srv *Server) reapDaggerSession(ctx context.Context, sess *daggerSession, mainClient *clientRuntime) {
	sess.lifecycleMu.Lock()

	if sess.state.Load() != sessionStateInitialized {
		// Already removed, or never fully initialized: nothing to tear down.
		sess.lifecycleMu.Unlock()
		return
	}

	mainClient.stateMu.RLock()
	activeCount := mainClient.activeCount
	mainClient.stateMu.RUnlock()
	if activeCount > 0 {
		// The main client reconnected before this reap ran; the session is
		// live again.
		sess.lifecycleMu.Unlock()
		return
	}

	err := srv.removeDaggerSession(ctx, sess)
	sess.lifecycleMu.Unlock()
	// Drop the tombstone now that teardown is complete and lifecycleMu is
	// released (pointer-conditional, so a fresh same-id session is never
	// deleted).
	srv.deleteSession(sess)
	if err != nil {
		slog.Error("session teardown failed",
			"sessionID", sess.sessionID,
			"error", err,
		)
	}
}

// ServeHTTP serves clients directly hitting the engine API (i.e. main client callers, not nested execs like module functions)
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clientMetadata, err := engine.ClientMetadataFromHTTPHeaders(r.Header)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get client metadata: %v", err), http.StatusInternalServerError)
		return
	}

	if !engine.CheckVersionCompatibility(engine.NormalizeVersion(clientMetadata.ClientVersion), engine.MinimumClientVersion) {
		http.Error(w, fmt.Sprintf("incompatible client version %s", engine.NormalizeVersion(clientMetadata.ClientVersion)), http.StatusInternalServerError)
		return
	}

	httpHandlerFunc(srv.serveHTTPToClient, &ClientInitOpts{
		ClientMetadata: clientMetadata,
		RootClient:     true,
	}).ServeHTTP(w, r)
}

// ServeHTTPToNestedClient serves nested clients, including module function calls.
func (srv *Server) ServeHTTPToNestedClient(
	w http.ResponseWriter,
	r *http.Request,
	transport *engine.NestedClientTransport,
	nestedClientMetadata *engine.ClientMetadata,
	callerClientID string,
	hostServiceProxyToCaller bool,
	moduleCtx dagql.AnyObjectResult,
	functionCall dagql.Typed,
) {
	if transport == nil {
		http.Error(w, "nested client transport is not registered", http.StatusServiceUnavailable)
		return
	}
	if transport.Closed() {
		if r.URL.Path == engine.ShutdownEndpoint {
			// /shutdown is an additional idempotent close signal. Proxy cleanup
			// may have won first, so acknowledge retries without reopening or
			// looking up the permanently closed client record.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "nested client transport is closed", http.StatusServiceUnavailable)
		return
	}
	if nestedClientMetadata == nil {
		http.Error(w, "nested client metadata is nil", http.StatusInternalServerError)
		return
	}
	clientMetadata := nestedClientMetadataForRequest(r.Header, nestedClientMetadata)

	var moduleContext dagql.ObjectResult[*core.Module]
	if moduleCtx != nil {
		typed, ok := moduleCtx.(dagql.ObjectResult[*core.Module])
		if !ok {
			http.Error(w, fmt.Sprintf("nested client module context is %T, not Module", moduleCtx), http.StatusInternalServerError)
			return
		}
		if typed.Self() != nil {
			moduleContext = typed
		}
	}

	var fnCall *core.FunctionCall
	if functionCall != nil {
		typed, ok := functionCall.(*core.FunctionCall)
		if !ok {
			http.Error(w, fmt.Sprintf("nested client function call is %T, not FunctionCall", functionCall), http.StatusInternalServerError)
			return
		}
		fnCall = typed
	}

	var hostServiceProxyClientID string
	if hostServiceProxyToCaller {
		hostServiceProxyClientID = callerClientID
	}

	httpHandlerFunc(srv.serveHTTPToClient, &ClientInitOpts{
		ClientMetadata:           clientMetadata,
		NestedTransport:          transport,
		ParentClientID:           callerClientID,
		HostServiceProxyClientID: hostServiceProxyClientID,
		ModuleContext:            moduleContext,
		FunctionCall:             fnCall,
	}).ServeHTTP(w, r)
}

func nestedClientMetadataForRequest(h http.Header, nestedClientMetadata *engine.ClientMetadata) *engine.ClientMetadata {
	clientMetadata := *nestedClientMetadata
	clientMetadata.AllowedLLMModules = slices.Clone(nestedClientMetadata.AllowedLLMModules)
	if clientMetadata.ClientVersion == "" {
		clientMetadata.ClientVersion = engine.Version
	}
	clientMetadata.Labels = map[string]string{}

	var extraModules []engine.ExtraModule
	var loadWorkspaceModules bool
	var singleQuery bool
	var workspaceModuleScope string
	var eagerRuntime bool
	var suppressCompatWorkspaceWarning bool
	var workspaceRef *string
	var workspaceEnv *string
	var userConfigPath string
	if md, _ := engine.ClientMetadataFromHTTPHeaders(h); md != nil {
		clientMetadata.ClientVersion = md.ClientVersion
		clientMetadata.AllowedLLMModules = slices.Clone(md.AllowedLLMModules)
		extraModules = md.ExtraModules
		loadWorkspaceModules = md.LoadWorkspaceModules
		singleQuery = md.SingleQuery
		// A nested CLI declares its own scope in its own request headers; the
		// parent's scope is never inherited (the struct copy above is reset
		// here like the other load-shaping fields).
		workspaceModuleScope = md.WorkspaceModuleScope
		eagerRuntime = md.EagerRuntime
		suppressCompatWorkspaceWarning = md.SuppressCompatWorkspaceWarning
		if declaredWorkspace, ok := workspaceRefFromClientMetadata(md); ok {
			ref := declaredWorkspace
			workspaceRef = &ref
		}
		if md.LockMode != "" {
			clientMetadata.LockMode = md.LockMode
		}
		if declaredEnv, ok := workspaceEnvFromClientMetadata(md); ok {
			env := declaredEnv
			workspaceEnv = &env
		}
		userConfigPath = md.UserConfigPath
	}

	clientMetadata.ExtraModules = extraModules
	clientMetadata.LoadWorkspaceModules = loadWorkspaceModules
	clientMetadata.SingleQuery = singleQuery
	clientMetadata.WorkspaceModuleScope = workspaceModuleScope
	clientMetadata.EagerRuntime = eagerRuntime
	clientMetadata.SuppressCompatWorkspaceWarning = suppressCompatWorkspaceWarning
	clientMetadata.Workspace = workspaceRef
	clientMetadata.WorkspaceEnv = workspaceEnv
	clientMetadata.UserConfigPath = userConfigPath
	return &clientMetadata
}

const InstrumentationLibrary = "dagger.io/engine.server"

func (srv *Server) serveHTTPToClient(w http.ResponseWriter, r *http.Request, opts *ClientInitOpts) (rerr error) {
	if srv.isShuttingDown() {
		switch r.URL.Path {
		case engine.QueryEndpoint:
			return gqlErr(errServerShuttingDown, http.StatusServiceUnavailable)
		default:
			return httpErr(errServerShuttingDown, http.StatusServiceUnavailable)
		}
	}

	ctx := srv.withShutdownCancel(r.Context())

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("http request done for client %q", opts.ClientID))

	clientMetadata := opts.ClientMetadata
	ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)

	// propagate span context and baggage from the client
	ctx = telemetry.Propagator.Extract(ctx, propagation.HeaderCarrier(r.Header))

	// Check if repeated telemetry is requested via baggage
	baggage := baggage.FromContext(ctx)
	if member := baggage.Member("repeat-telemetry"); member.Value() != "" {
		ctx = dagql.WithRepeatedTelemetry(ctx)
	}

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("trace", trace.SpanContextFromContext(ctx).TraceID().String()).
		WithField("span", trace.SpanContextFromContext(ctx).SpanID().String()).
		WithField("client_id", clientMetadata.ClientID).
		WithField("client_hostname", clientMetadata.ClientHostname).
		WithField("session_id", clientMetadata.SessionID))
	ctx = slog.WithLogger(ctx, slog.FromContext(ctx).With(
		"client_id", clientMetadata.ClientID,
		"client_hostname", clientMetadata.ClientHostname,
		"session_id", clientMetadata.SessionID,
		"trace", trace.SpanContextFromContext(ctx).TraceID().String(),
		"span", trace.SpanContextFromContext(ctx).SpanID().String(),
	))
	ctx = dagql.ContextWithCache(ctx, srv.engineCache)

	// Debug https://github.com/dagger/dagger/issues/7592 by logging method and some headers, which
	// are checked by gqlgen's handler
	bklog.G(ctx).WithFields(logrus.Fields{
		"path":          r.URL.Path,
		"method":        r.Method,
		"upgradeHeader": r.Header.Get("Upgrade"),
		"contentType":   r.Header.Get("Content-Type"),
		"trace":         trace.SpanContextFromContext(ctx).TraceID().String(),
		"span":          trace.SpanContextFromContext(ctx).SpanID().String(),
	}).Debug("handling http request")

	mux := http.NewServeMux()
	switch r.URL.Path {
	case "/v1/traces", "/v1/logs", "/v1/metrics":
		// Telemetry subscriptions require only the stable client record.
		record, err := srv.clientRecordFromIDs(clientMetadata.SessionID, clientMetadata.ClientID)
		if err != nil {
			return fmt.Errorf("get client record: %w", err)
		}
		mux.HandleFunc("GET /v1/traces", httpHandlerFunc(srv.telemetryPubSub.TracesSubscribeHandler, record))
		mux.HandleFunc("GET /v1/logs", httpHandlerFunc(srv.telemetryPubSub.LogsSubscribeHandler, record))
		mux.HandleFunc("GET /v1/metrics", httpHandlerFunc(srv.telemetryPubSub.MetricsSubscribeHandler, record))
	default:
		requestOpts := *opts
		requestOpts.requestOwner = r.Method + " " + r.URL.Path
		var requestLease *engine.ClientLifecycleLease
		requestOpts.requestLeaseOut = &requestLease
		switch r.URL.Path {
		case engine.SessionAttachablesEndpoint, engine.InitEndpoint, engine.ShutdownEndpoint:
			requestOpts.BootstrapOnly = true
		}
		client, cleanup, err := srv.getOrInitClient(ctx, &requestOpts)
		if err != nil {
			err = fmt.Errorf("get or init client: %w", err)
			switch r.URL.Path {
			case engine.QueryEndpoint:
				err = gqlErr(err, http.StatusInternalServerError)
			default:
				err = httpErr(err, http.StatusInternalServerError)
			}
			return err
		}

		if requestLease == nil || !requestLease.Held() {
			_ = cleanup()
			return httpErr(fmt.Errorf("accepted request for client %q has no lifecycle lease", client.clientID), http.StatusInternalServerError)
		}
		if client.daggerSession.clientMetadataSealed(client.clientRecord) {
			requestScope, err := engine.NewClientScope(client.clientMetadata, requestLease)
			if err != nil {
				_ = cleanup()
				return httpErr(fmt.Errorf("create request client scope: %w", err), http.StatusInternalServerError)
			}
			ctx, err = engine.ContextWithClientScope(ctx, requestScope)
			if err != nil {
				_ = cleanup()
				return httpErr(fmt.Errorf("install request client scope: %w", err), http.StatusInternalServerError)
			}
		} else if !requestOpts.BootstrapOnly {
			_ = cleanup()
			return httpErr(fmt.Errorf("client %q metadata was not sealed for executable request", client.clientID), http.StatusInternalServerError)
		}
		defer func() {
			err := cleanup()
			if err != nil {
				bklog.G(ctx).WithError(err).Error("client serve cleanup failed")
				rerr = errors.Join(rerr, err)
			}
		}()

		sess := client.daggerSession
		ctx = analytics.WithContext(ctx, sess.analytics)
		r = r.WithContext(ctx)

		mux.Handle(engine.SessionAttachablesEndpoint, httpHandlerFunc(srv.serveSessionAttachables, client.clientRecord))
		mux.Handle(engine.QueryEndpoint, httpHandlerFunc(srv.serveQuery, client))
		mux.Handle(engine.InitEndpoint, httpHandlerFunc(srv.serveInit, client))
		mux.Handle(engine.ShutdownEndpoint, httpHandlerFunc(srv.serveShutdown, client))
		sess.endpointMu.RLock()
		for path, handler := range sess.endpoints {
			mux.Handle(path, handler)
		}
		sess.endpointMu.RUnlock()
	}

	mux.ServeHTTP(w, r)
	return nil
}

func (srv *Server) serveSessionAttachables(w http.ResponseWriter, r *http.Request, record *clientRecord) (rerr error) {
	ctx := r.Context()
	slog.DebugContext(ctx, "session attachables handling conn", "clientID", record.clientID)
	defer func() {
		slog.DebugContext(ctx, "session attachables handle conn done",
			"err", rerr,
			"ctxErr", ctx.Err(),
			"clientID", record.clientID,
		)
	}()

	// verify this isn't overwriting an existing active session
	if _, ok := record.daggerSession.attachables.Lookup(record.clientID); ok {
		err := fmt.Errorf("session attachables for client %q already exist", record.clientID)
		return httpErr(err, http.StatusBadRequest)
	}

	// hijack the connection so we can use it for our gRPC client
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return errors.New("handler does not support hijack")
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return fmt.Errorf("failed to hijack connection: %w", err)
	}
	defer func() {
		if rerr != nil {
			conn.Close()
		}
	}()
	if err := conn.SetDeadline(time.Time{}); err != nil {
		panic(fmt.Errorf("failed to clear deadline: %w", err))
	}

	// confirm to the client that everything went swimmingly and we're ready to become a gRPC client
	resp := &http.Response{
		StatusCode: http.StatusSwitchingProtocols,
		Header:     http.Header{},
	}
	resp.Header.Set("Connection", "Upgrade")
	resp.Header.Set("Upgrade", "h2c")
	if err := resp.Write(conn); err != nil {
		panic(fmt.Errorf("failed to write response: %w", err))
	}

	// The client confirms it has fully read the response and is ready to serve gRPC by sending
	// a single byte ack. This prevents race conditions where we start trying to connect gRPC clients
	// before the response has been fully read, which can mix http and gRPC traffic.
	ack := make([]byte, 1)
	if _, err := conn.Read(ack); err != nil {
		panic(fmt.Errorf("failed to read ack: %w", err))
	}

	ctx = record.daggerSession.withClosingCancel(ctx)

	// Disable collecting otel metrics on these grpc connections for now. We don't use them and
	// they add noticeable memory allocation overhead, especially for heavy filesync use cases.
	ctx = trace.ContextWithSpan(ctx, trace.SpanFromContext(nil)) //nolint:staticcheck // we have to provide a nil context...

	err = record.daggerSession.attachables.Register(ctx, record.clientID, conn, r.Header.Values(engine.SessionMethodNameMetaKey))
	if err != nil {
		panic(fmt.Errorf("handle session attachables: %w", err))
	}
	return nil
}

// withRequestTelemetrySuppression applies a request's telemetry opt-out (the
// engine.SuppressTelemetryHeader header): when opted out, the returned context
// is marked with dagql.WithSkip so core.AroundFunc emits no spans, no
// seen-keys, and no call payloads for the request's selection, and the
// returned bool tells serveQuery to skip the per-request wrapper span as well.
func withRequestTelemetrySuppression(ctx context.Context, r *http.Request) (context.Context, bool) {
	if r.Header.Get(engine.SuppressTelemetryHeader) != "true" {
		return ctx, false
	}
	return dagql.WithSkip(ctx), true
}

func (srv *Server) serveQuery(w http.ResponseWriter, r *http.Request, client *clientRuntime) (rerr error) {
	sess := client.daggerSession

	// Profiling is recorded for this request if the engine is recording
	// globally, or this session opted in (in which case contexts are marked
	// so only this session's work records).
	profiledSession := sess.wcprofEnabled ||
		(client.clientMetadata != nil && client.clientMetadata.Profile)
	if profiledSession {
		wcprof.EnsureRecorder()
	}
	profiling := profiledSession || wcprof.GloballyEnabled()
	sess.dagqlMu.Lock()
	if sess.dagqlClosing {
		sess.dagqlMu.Unlock()
		return gqlErr(errSessionClosing, http.StatusServiceUnavailable)
	}
	sess.dagqlInFlight++
	sess.dagqlMu.Unlock()
	defer func() {
		sess.dagqlMu.Lock()
		sess.dagqlInFlight--
		if sess.dagqlInFlight == 0 {
			sess.dagqlCond.Broadcast()
		}
		sess.dagqlMu.Unlock()
	}()

	ctx := sess.withClosingCancel(r.Context())

	// A request may opt out of telemetry wholesale (e.g. the CLI's context
	// visualizer polling ever-growing read-only conversation state, whose
	// telemetry volume would otherwise grow quadratically): mark the context
	// so core.AroundFunc emits nothing for the whole selection.
	ctx, telemetrySuppressed := withRequestTelemetrySuppression(ctx, r)

	// turn panics into graphql errors — must be set up before any code that
	// could panic (including ensureExtraModulesLoaded and schema loading).
	defer func() {
		if v := recover(); v != nil {
			bklog.G(ctx).Errorf("panic serving schema: %v %s", v, string(debug.Stack()))
			switch v := v.(type) {
			case error:
				rerr = gqlErr(v, http.StatusInternalServerError)
			case string:
				rerr = gqlErr(errors.New(v), http.StatusInternalServerError)
			default:
				rerr = gqlErr(errors.New("internal server error"), http.StatusInternalServerError)
			}
		}
	}()

	// only record telemetry if the request is traced, otherwise
	// we end up with orphaned spans in their own separate traces from tests etc.
	// A telemetry-suppressed request skips the wrapper span too: a suppressed
	// poll must contribute zero spans to the client's telemetry DB.
	if !telemetrySuppressed && trace.SpanContextFromContext(ctx).IsValid() {
		// create a span to record telemetry into the client's DB
		//
		// downstream components must use otel.SpanFromContext(ctx).TracerProvider()
		clientTracer := sess.tracerProvider.Tracer(InstrumentationLibrary)
		var span trace.Span
		attrs := []attribute.KeyValue{
			attribute.Bool(telemetry.UIPassthroughAttr, true),
		}
		if engineID := client.clientMetadata.CloudScaleOutEngineID; engineID != "" {
			attrs = append(attrs, attribute.String(telemetry.EngineIDAttr, engineID))
		}
		ctx, span = clientTracer.Start(ctx,
			fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			trace.WithAttributes(attrs...),
		)
		defer telemetry.EndWithCause(span, &rerr)

		// wcprof completeness checksum: record this trace and its
		// session-root span once, from the OUTERMOST query (a main client has no
		// parents; nested module-runtime clients do). The engine span total is NOT
		// declared here — a command issues many queries under one trace and a per-query
		// stamp is only a running floor (it cannot see a whole trailing query drop or
		// post-query async padding). It is declared ONCE, exactly, at session teardown
		// (removeDaggerSession) on a carrier span parented at the ids recorded here.
		if len(client.parentClientIDs) == 0 {
			sess.wcprofTraceOnce.Do(func() {
				sc := trace.SpanContextFromContext(ctx)
				sess.wcprofTraceID = sc.TraceID()
				sess.wcprofRootSpanID = sc.SpanID()
			})
		}
	}

	// Install the session-owned logger and meter providers.
	ctx = telemetry.WithLoggerProvider(ctx, sess.loggerProvider)
	ctx = telemetry.WithMeterProvider(ctx, sess.meterProvider)

	ctx = dagql.ContextWithOperationLeaseProvider(ctx, dagql.OperationLeaseProviderFunc(func(ctx context.Context) (context.Context, func(context.Context) error, error) {
		if leaseID, ok := leases.FromContext(ctx); ok && leaseID != "" {
			return ctx, func(context.Context) error { return nil }, nil
		}
		return bkcache.WithLazyLease(ctx, srv.leaseManager, bkcache.MakeTemporary)
	}))

	// make query available via context to all APIs
	ctx = core.ContextWithQuery(ctx, client.dagqlRoot)

	var profServeOp *wcprof.Op
	if profiling {
		if profiledSession {
			ctx = wcprof.ContextWithProfiling(ctx)
		}
		ctx, profServeOp = wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, "session.serveQuery", wcprof.OpOpts{
			ClientID: client.clientID,
		})
		defer func() {
			profServeOp.EndErr(rerr)
		}()
	}

	r = r.WithContext(ctx)

	if client.hostServiceProxyClientID == "" {
		profWait := wcprof.BeginWaitIdent(ctx, "session:attachables", wcprof.WaitReasonIO)
		_, err := sess.getClientCaller(ctx, client.clientID)
		profWait.End()
		if err != nil {
			return gqlErr(fmt.Errorf("waiting for client session attachables: %w", err), http.StatusInternalServerError)
		}
	}

	if err := client.claimSingleQueryRequest(); err != nil {
		return gqlErr(err, http.StatusBadRequest)
	}

	// Load workspace modules and extra modules (e.g. from -m flag). These are
	// deferred from initializeClientRuntime because they need the client's
	// session attachables, which only become available after the session
	// attachables handshake completes (after init locks are released).
	wsCtx, wsOp := wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, "session.workspaceLoad", wcprof.OpOpts{ClientID: client.clientID})
	err := srv.ensureWorkspaceLoaded(wsCtx, client)
	wsOp.EndErr(err)
	if err != nil {
		return gqlErr(fmt.Errorf("loading workspace: %w", err), http.StatusInternalServerError)
	}
	modCtx, modOp := wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, "session.modulesLoad", wcprof.OpOpts{ClientID: client.clientID})
	err = srv.ensureRequestModulesLoaded(modCtx, client, r)
	modOp.EndErr(err)
	if err != nil {
		return gqlErr(fmt.Errorf("loading modules: %w", err), http.StatusInternalServerError)
	}

	// get the schema we're gonna serve to this client based on which modules they have loaded, if any
	schemaCtx, schemaOp := wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, "session.schemaBuild", wcprof.OpOpts{ClientID: client.clientID})
	schema, err := client.servedMods.Schema(schemaCtx)
	schemaOp.EndErr(err)
	if err != nil {
		return gqlErr(fmt.Errorf("failed to get schema: %w", err), http.StatusBadRequest)
	}

	gqlSrv := dagql.NewDefaultHandler(schema)
	// NB: break glass when needed:
	// gqlSrv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	// 	res := next(ctx)
	// 	pl, err := json.Marshal(res)
	// 	slog.Debug("graphql response", "response", string(pl), "error", err)
	// 	return res
	// })

	if profiling {
		queryCtx, queryOp := wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, "session.query", wcprof.OpOpts{ClientID: client.clientID})
		defer queryOp.End(wcprof.OutcomeOK)
		r = r.WithContext(queryCtx)
	}

	gqlSrv.ServeHTTP(w, r)
	return nil
}

func (client *clientRuntime) claimSingleQueryRequest() error {
	if client.clientMetadata == nil || !client.clientMetadata.SingleQuery {
		return nil
	}

	client.singleQueryMu.Lock()
	defer client.singleQueryMu.Unlock()
	if client.singleQueryServed {
		return errors.New("client declared single_query but sent multiple GraphQL requests")
	}
	client.singleQueryServed = true
	return nil
}

// ensureRequestModulesLoaded loads the modules this request demands, read from
// the request's root fields: fields naming pending modules demand those, and
// full-schema or unrecognized fields demand everything. The rest stay pending.
func (srv *Server) ensureRequestModulesLoaded(ctx context.Context, client *clientRuntime, r *http.Request) error {
	return srv.ensureRequestModulesLoadedWithPostLoad(ctx, client, r, nil)
}

// ensureRequestModulesLoadedWithPostLoad is split out so synchronization-
// sensitive tests can observe the client immediately after module loading
// returns, before any subsequent request finalization.
func (srv *Server) ensureRequestModulesLoadedWithPostLoad(ctx context.Context, client *clientRuntime, r *http.Request, postLoad func()) error {
	var filter func([]pendingModule) []pendingModule
	scopeApplied := false
	if client.hasPendingWorkspaceModules() {
		if ok, rootFields, err := dagql.PeekRootFields(r); err == nil && ok {
			filter = func(mods []pendingModule) []pendingModule {
				// runs under client.modulesMu, which also guards
				// servedWorkspaceModuleNames and workspaceModuleScopeConsumed
				scope := client.pendingWorkspaceModuleScopeLocked()
				selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, client.servedWorkspaceModuleNames, client.failedModules, rootFields, scope, client.entrypointServed)
				if applied {
					scopeApplied = true
					names := make([]string, 0, len(selected))
					for _, mod := range selected {
						names = append(names, moduleProgressName(mod))
					}
					slog.Debug("narrowing workspace module load to client scope",
						"scope", scope,
						"modules", names)
				}
				return selected
			}
		}
	}
	_, err := srv.ensureModulesLoadedModeWithSuccess(ctx, client, filter, false, func() {
		// Consume only after a successful load, but before modulesMu is
		// released, so another request cannot claim the one-shot scope.
		if scopeApplied {
			client.workspaceModuleScopeConsumed = true
		}
	})
	if postLoad != nil {
		postLoad()
	}
	return err
}

// pendingWorkspaceModuleScopeLocked returns the client-declared workspace
// module scope while it is still consumable. client.modulesMu must be held.
func (client *clientRuntime) pendingWorkspaceModuleScopeLocked() string {
	if client.workspaceModuleScopeConsumed || client.clientMetadata == nil {
		return ""
	}
	return client.clientMetadata.WorkspaceModuleScope
}

func (client *clientRuntime) hasPendingWorkspaceModules() bool {
	client.modulesMu.Lock()
	defer client.modulesMu.Unlock()
	return len(client.pendingModules) > 0
}

func (srv *Server) serveInit(w http.ResponseWriter, _ *http.Request, client *clientRuntime) (rerr error) {
	sess := client.daggerSession
	slog := slog.With(
		"isMainClient", client.clientID == sess.mainClientCallerID,
		"sessionID", sess.sessionID,
		"clientID", client.clientID,
		"mainClientID", sess.mainClientCallerID)

	slog.Trace("initialized client")

	// nothing to actually do, client was passed in
	w.WriteHeader(http.StatusNoContent)

	return nil
}

func (srv *Server) serveShutdown(w http.ResponseWriter, r *http.Request, client *clientRuntime) (rerr error) {
	ctx := r.Context()
	var shutdownErr error

	sess := client.daggerSession
	slog := slog.With(
		"isMainClient", client.clientID == sess.mainClientCallerID,
		"sessionID", sess.sessionID,
		"clientID", client.clientID,
		"mainClientID", sess.mainClientCallerID)

	slog.Info("client shutdown")
	sess.scopeMu.Lock()
	if client.shutdownAt.IsZero() {
		client.shutdownAt = time.Now()
	}
	sess.scopeMu.Unlock()
	// Reachability closes independently from this accepted shutdown request. An
	// initialized request keeps its scope and may still delegate cleanup work; a
	// close-before-init bootstrap request deliberately has no executable scope.
	// A nested /shutdown is an additional idempotent signal to the proxy-owned
	// registration handle; intentional roots close their direct transport.
	if client.nestedTransport != nil {
		client.nestedTransport.Close()
	} else {
		sess.closeClientScope(client)
	}
	shutdownStart := time.Now()
	defer func() {
		slog.Info("client shutdown done", "duration", time.Since(shutdownStart), "error", shutdownErr)
	}()

	// drainPhase times one shutdown drain phase, logging before and after: the
	// client enforces a hard deadline on the whole shutdown request, so a
	// phase that stalls (or wedges forever on an uncancellable context) must
	// be attributable from the engine log alone.
	drainPhase := func(name string, fn func() error) error {
		slog.Debug("shutdown drain phase starting", "phase", name)
		start := time.Now()
		err := fn()
		slog.Info("shutdown drain phase done", "phase", name, "duration", time.Since(start), "error", err)
		return err
	}

	if client.clientID == sess.mainClientCallerID {
		slog.Info("main client is shutting down")
		err := drainPhase("flush workspace locks", func() error {
			return srv.flushWorkspaceLocks(context.WithoutCancel(ctx), client)
		})
		if err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("flush workspace locks: %w", err))
			slog.Error("failed to flush workspace locks", "error", err)
		}

		// this must be done after lockfile flushing (since lockfiles make use of attachables to write data to host)
		sess.beginClosing()

		// Stop services, since the main client is going away, and we
		// want the client to see them stop. Stop errors are not surfaced
		// (matching prior behavior), so the phase always returns nil.
		_ = drainPhase("stop session services", func() error {
			sess.services.StopSessionServices(ctx, sess.sessionID)
			return nil
		})

		defer func() {
			// Signal shutdown at the very end, _after_ flushing telemetry/etc.,
			// so we can respect the shutdownCh to short-circuit any telemetry
			// subscribers that appeared _while_ shutting down.
			sess.closeShutdownOnce.Do(func() {
				close(sess.shutdownCh)
			})
		}()
	} else {
		err := drainPhase("flush workspace locks", func() error {
			return srv.flushWorkspaceLocks(context.WithoutCancel(ctx), client)
		})
		if err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("flush workspace locks: %w", err))
			slog.Error("failed to flush workspace locks", "error", err)
		}
	}

	// All providers and metric aggregation are session-owned, so any client
	// shutdown flushes the one session pipeline before acknowledging that the
	// client's current telemetry is durable.
	flushErr := drainPhase("flush session telemetry", func() error {
		return sess.FlushTelemetry(ctx, "client shutdown")
	})
	if flushErr != nil {
		slog.Error("failed to flush telemetry", "error", flushErr)
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("flush telemetry: %w", flushErr))
	}

	client.closeShutdownOnce.Do(func() {
		close(client.shutdownCh)
	})

	return shutdownErr
}

// Stitch in the given module to the list being served to the current client.
// When includeDependencies is true, dependency modules are also served with
// their constructors on the Query root.
// When entrypoint is true, the module's main-object methods are promoted
// onto the Query root.
func (srv *Server) ServeModule(ctx context.Context, mod dagql.ObjectResult[*core.Module], includeDependencies bool, entrypoint bool) error {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return err
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()

	if err := srv.serveModule(client, core.NewUserMod(mod), core.InstallOpts{Entrypoint: entrypoint}); err != nil {
		return err
	}
	if includeDependencies {
		for _, dep := range mod.Self().Deps.Mods() {
			if err := srv.serveModule(client, dep, core.InstallOpts{}); err != nil {
				return fmt.Errorf("error serving dependency %s: %w", dep.Name(), err)
			}
		}

		// Also serve toolchains so their functions are available in the
		// client schema (e.g. when `dagger shell` `.cd`s into a module).
		if mod.Self().Source.Valid && mod.Self().Source.Value.Self() != nil {
			src := mod.Self().Source.Value
			defaultPathContextSrc := src
			if mod.Self().ContextSource.Valid && mod.Self().ContextSource.Value.Self() != nil {
				defaultPathContextSrc = mod.Self().ContextSource.Value
			}
			for i, tcSrc := range src.Self().Toolchains {
				if tcSrc.Self() == nil {
					continue
				}
				var cfg *modules.ModuleConfigDependency
				if i < len(src.Self().ConfigToolchains) {
					cfg = src.Self().ConfigToolchains[i]
				}
				pending := pendingRelatedModule(defaultPathContextSrc, tcSrc.Self(), cfg, false)
				tcMod, err := srv.resolveModuleSourceAsModule(ctx, client.dag, tcSrc, pending)
				if err != nil {
					return fmt.Errorf("error resolving toolchain module: %w", err)
				}
				if err := srv.serveModule(client, core.NewUserMod(tcMod), core.InstallOpts{}); err != nil {
					return fmt.Errorf("error serving toolchain %s: %w", tcMod.Self().Name(), err)
				}
			}
		}
	}
	return nil
}

// serveModule adds a module to the client's served set with the given install
// policy.
//
// Not threadsafe: client.stateMu must be held when calling.
func (srv *Server) serveModule(client *clientRuntime, mod core.Mod, opts core.InstallOpts) error {
	existing, ok := client.servedMods.Lookup(mod.Name())
	if ok {
		if !isSameModuleReference(existing.GetSource(), mod.GetSource()) {
			return fmt.Errorf("module %s (source: %s | pin: %s) already exists with different source %s (pin: %s)",
				mod.Name(), mod.GetSource().AsString(), mod.GetSource().Pin(), existing.GetSource().AsString(), existing.GetSource().Pin(),
			)
		}
	}
	// With handles deduplication and promotion internally.
	client.servedMods = client.servedMods.With(mod, opts)
	return nil
}

// Returns true if the module source a is the same as b or they come from the
// core module.
// Returns false if:
// - AsString() of a and b are different
// - Pin() of a and b are different
func isSameModuleReference(a *core.ModuleSource, b *core.ModuleSource) bool {
	// If one of them is empty, that means they are from code module so they shouldn't
	// be compared.
	if a.AsString() == "" || b.AsString() == "" {
		return true
	}

	// Match by canonical module identity:
	// - local: absolute source-code path (context dir + sourceSubpath)
	// - git: clone ref + sourceSubpath
	// plus pin equality for immutable provenance.
	if canonicalModuleReference(a) != canonicalModuleReference(b) {
		return false
	}
	return a.Pin() == b.Pin()
}
func (srv *Server) CurrentWorkspaceLock(ctx context.Context, requireWritable bool) (*workspace.Lock, bool, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, false, err
	}
	if !requireWritable {
		lock, ok, err := readImmutableRemoteWorkspaceLock(ctx, client.workspace)
		if err != nil || ok {
			return lock, ok, err
		}
	}

	ws, key, lockPath, ok, err := srv.currentWorkspaceLockBinding(client)
	if err != nil || !ok {
		return nil, ok, err
	}

	sess := client.daggerSession

	sess.lockFileMu.RLock()
	if state, ok := sess.lockFiles[key]; ok && state.loaded {
		cloned, err := state.lock.Clone()
		sess.lockFileMu.RUnlock()
		return cloned, true, err
	}
	sess.lockFileMu.RUnlock()

	sess.lockFileMu.Lock()
	defer sess.lockFileMu.Unlock()

	state, err := srv.loadWorkspaceLockStateLocked(ctx, client, ws, key, lockPath)
	if err != nil {
		return nil, false, err
	}
	cloned, err := state.lock.Clone()
	if err != nil {
		return nil, false, err
	}
	return cloned, true, nil
}

func (srv *Server) SetCurrentWorkspaceLookup(
	ctx context.Context,
	namespace string,
	operation string,
	inputs []any,
	result workspace.LookupResult,
) error {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return err
	}

	ws, key, lockPath, ok, err := srv.currentWorkspaceLockBinding(client)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("workspace lock is not available")
	}

	sess := client.daggerSession
	sess.lockFileMu.Lock()
	defer sess.lockFileMu.Unlock()

	state, err := srv.loadWorkspaceLockStateLocked(ctx, client, ws, key, lockPath)
	if err != nil {
		return err
	}
	if err := state.lock.SetLookup(namespace, operation, inputs, result); err != nil {
		return err
	}
	if err := state.delta.SetLookup(namespace, operation, inputs, result); err != nil {
		return err
	}
	state.dirty = true
	return nil
}

func (srv *Server) currentWorkspaceLockBinding(client *clientRuntime) (*core.Workspace, workspaceLockKey, string, bool, error) {
	ws := client.workspace
	if ws == nil || ws.HostPath() == "" || ws.LockFile == "" {
		return nil, workspaceLockKey{}, "", false, nil
	}
	lockPath, err := workspaceLockPath(ws)
	if err != nil {
		return nil, workspaceLockKey{}, "", false, err
	}
	return ws, workspaceLockKey{
		ownerClientID: ws.ClientID,
		lockPath:      lockPath,
	}, lockPath, true, nil
}

func (srv *Server) loadWorkspaceLockStateLocked(
	ctx context.Context,
	client *clientRuntime,
	ws *core.Workspace,
	key workspaceLockKey,
	lockPath string,
) (*workspaceLockState, error) {
	sess := client.daggerSession
	if sess.lockFiles == nil {
		sess.lockFiles = make(map[workspaceLockKey]*workspaceLockState)
	}
	if state, ok := sess.lockFiles[key]; ok && state.loaded {
		return state, nil
	}

	workspaceCtx, bk, err := srv.workspaceOwnerAccess(ctx, sess, ws)
	if err != nil {
		return nil, err
	}
	lock, err := readWorkspaceLockState(workspaceCtx, bk, ws)
	if err != nil {
		return nil, err
	}

	state := &workspaceLockState{
		ws:       ws.Clone(),
		lockPath: lockPath,
		lock:     lock,
		delta:    workspace.NewLock(),
		loaded:   true,
	}
	sess.lockFiles[key] = state
	return state, nil
}

func (srv *Server) workspaceOwnerAccess(
	ctx context.Context,
	sess *daggerSession,
	ws *core.Workspace,
) (context.Context, *engineutil.Client, error) {
	if ws.ClientID == "" {
		return nil, nil, fmt.Errorf("workspace has no client ID")
	}

	caller, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace owner access requires executable scope: %w", err)
	}
	if caller.daggerSession != sess {
		return nil, nil, fmt.Errorf("workspace owner session %q does not match executable scope session %q", sess.sessionID, caller.daggerSession.sessionID)
	}
	ownerRecord, err := srv.clientRecordFromIDs(sess.sessionID, ws.ClientID)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace owner client: %w", err)
	}
	ownerMetadata, err := sess.clientMetadataSnapshot(ownerRecord)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace owner client metadata: %w", err)
	}
	if sess.engineUtilClient == nil {
		return nil, nil, fmt.Errorf("workspace owner engine gateway not initialized")
	}

	workspaceCtx := engine.ContextWithClientMetadata(ctx, ownerMetadata)
	return workspaceCtx, sess.engineUtilClient, nil
}

func workspaceLockPath(ws *core.Workspace) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace is required")
	}
	if ws.HostPath() == "" {
		return "", fmt.Errorf("workspace has no host path")
	}
	if ws.LockFile == "" {
		return "", fmt.Errorf("workspace lockfile is not selected")
	}
	return filepath.Join(ws.HostPath(), ws.LockFile), nil
}

func readWorkspaceLockState(ctx context.Context, bk interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
}, ws *core.Workspace) (*workspace.Lock, error) {
	lockPath, err := workspaceLockPath(ws)
	if err != nil {
		return nil, err
	}

	data, err := bk.ReadCallerHostFile(ctx, lockPath)
	if err != nil {
		if isWorkspaceLockNotFound(err) {
			legacyPath := legacyWorkspaceLockPath(ws)
			if legacyPath == "" || legacyPath == lockPath {
				return workspace.NewLock(), nil
			}
			data, err = bk.ReadCallerHostFile(ctx, legacyPath)
			if err != nil {
				if isWorkspaceLockNotFound(err) {
					return workspace.NewLock(), nil
				}
				return nil, fmt.Errorf("reading legacy lock: %w", err)
			}
		} else {
			return nil, fmt.Errorf("reading lock: %w", err)
		}
	}

	lock, err := workspace.ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("parsing lock: %w", err)
	}
	return lock, nil
}

func readImmutableRemoteWorkspaceLock(ctx context.Context, ws *core.Workspace) (*workspace.Lock, bool, error) {
	if ws == nil || ws.LockFile == "" {
		return nil, false, nil
	}

	// The remote loader records whether the original workspace selector was a
	// commit. A resolved SHA alone is insufficient because branches and tags have
	// one too.
	source, ok := ws.Source().(*core.WorkspaceSourceGitRef)
	if !ok || !source.ExplicitCommit {
		return nil, false, nil
	}

	root := ws.Rootfs()
	if root.Self() == nil {
		return nil, true, fmt.Errorf("immutable remote Git workspace has no root filesystem")
	}
	lock, err := readWorkspaceLockFromRootfs(ctx, root, ws.LockFile)
	return lock, true, err
}

func readWorkspaceLockFromRootfs(
	ctx context.Context,
	root dagql.ObjectResult[*core.Directory],
	lockPath string,
) (*workspace.Lock, error) {
	lockPath = filepath.ToSlash(lockPath)
	legacyPath := filepath.ToSlash(workspace.LegacyLockFilePathForCanonical(lockPath))
	statFS := &core.DirectoryStatFS{Dir: root}

	_, exists, err := core.StatFSExists(ctx, statFS, lockPath)
	if err != nil {
		return nil, fmt.Errorf("reading lock: %w", err)
	}
	if !exists {
		lockPath = legacyPath
		_, exists, err = core.StatFSExists(ctx, statFS, lockPath)
		if err != nil {
			return nil, fmt.Errorf("reading legacy lock: %w", err)
		}
	}
	if !exists {
		return workspace.NewLock(), nil
	}

	data, err := core.DirectoryReadFile(ctx, root, lockPath)
	if err != nil {
		if lockPath == legacyPath {
			return nil, fmt.Errorf("reading legacy lock: %w", err)
		}
		return nil, fmt.Errorf("reading lock: %w", err)
	}
	lock, err := workspace.ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("parsing lock: %w", err)
	}
	return lock, nil
}

func legacyWorkspaceLockPath(ws *core.Workspace) string {
	if ws == nil || ws.HostPath() == "" || ws.LockFile == "" {
		return ""
	}
	return filepath.Join(ws.HostPath(), workspace.LegacyLockFilePathForCanonical(ws.LockFile))
}

func isWorkspaceLockNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || status.Code(err) == codes.NotFound
}

func exportWorkspaceLockToHost(ctx context.Context, bk *engineutil.Client, ws *core.Workspace, lock *workspace.Lock) error {
	lockBytes, err := lock.Marshal()
	if err != nil {
		return fmt.Errorf("marshal lock: %w", err)
	}

	lockPath, err := workspaceLockPath(ws)
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp("", "workspace-lock-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(lockBytes); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := bk.LocalFileExport(ctx, tmpFile.Name(), workspace.LockFileName, lockPath, true); err != nil {
		return fmt.Errorf("export lock: %w", err)
	}
	return nil
}

func (srv *Server) flushWorkspaceLocks(ctx context.Context, client *clientRuntime) error {
	sess := client.daggerSession

	type pendingWorkspaceLockExport struct {
		ws       *core.Workspace
		lockPath string
		delta    *workspace.Lock
	}

	var pending []pendingWorkspaceLockExport

	sess.lockFileMu.RLock()
	for _, state := range sess.lockFiles {
		if state == nil || !state.loaded || !state.dirty {
			continue
		}
		delta, err := state.delta.Clone()
		if err != nil {
			sess.lockFileMu.RUnlock()
			return fmt.Errorf("clone workspace lock delta for %s: %w", state.lockPath, err)
		}
		if state.ws.ClientID == client.clientID {
			pending = append(pending, pendingWorkspaceLockExport{
				ws:       state.ws.Clone(),
				lockPath: state.lockPath,
				delta:    delta,
			})
		}
	}
	sess.lockFileMu.RUnlock()

	var flushErr error
	for _, export := range pending {
		lg := slog.With("lockPath", export.lockPath, "clientID", client.clientID)
		// Log before the host roundtrip: it runs on an uncancellable context
		// against the client's session attachables, so if the client is gone
		// it can wedge here and this line is the only breadcrumb.
		lg.Debug("flushing workspace lock")
		lockStart := time.Now()
		srv.locker.Lock(export.lockPath)
		lockWait := time.Since(lockStart)

		workspaceCtx, bk, err := srv.workspaceOwnerAccess(ctx, sess, export.ws)
		if err == nil {
			var latest *workspace.Lock
			latest, err = readWorkspaceLockState(workspaceCtx, bk, export.ws)
			if err == nil {
				err = latest.Merge(export.delta)
			}
			if err == nil {
				err = exportWorkspaceLockToHost(workspaceCtx, bk, export.ws, latest)
			}
		}

		srv.locker.Unlock(export.lockPath)

		lg = lg.With("duration", time.Since(lockStart), "lockWait", lockWait, "error", err)
		if time.Since(lockStart) > slowDrainOp {
			lg.Warn("slow workspace lock flush")
		} else {
			lg.Debug("workspace lock flush done")
		}

		if err != nil {
			flushErr = errors.Join(flushErr, fmt.Errorf("flush workspace lock %s: %w", export.lockPath, err))
		}
	}

	return flushErr
}

// If the current client is coming from a function, return the module that function is from
func (srv *Server) CurrentModule(ctx context.Context) (dagql.ObjectResult[*core.Module], error) {
	var zero dagql.ObjectResult[*core.Module]
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return zero, err
	}
	if client.clientID == client.daggerSession.mainClientCallerID {
		return zero, fmt.Errorf("%w: main client caller has no current module", core.ErrNoCurrentModule)
	}
	if client.mod.Self() != nil {
		return client.mod, nil
	}

	return zero, core.ErrNoCurrentModule
}

// If the current client is a module client or a client created by a module function, returns that module.
func (srv *Server) ModuleParent(ctx context.Context) (dagql.ObjectResult[*core.Module], error) {
	var zero dagql.ObjectResult[*core.Module]
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return zero, err
	}
	if client.mod.Self() != nil {
		return client.mod, nil
	}
	ancestors, err := client.daggerSession.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return zero, fmt.Errorf("resolve module parent ancestry: %w", err)
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		parent := ancestors[i]
		if parent.mod.Self() != nil {
			return parent.mod, nil
		}
	}
	return zero, core.ErrNoCurrentModule
}

// If the current client is coming from a function, return the function call metadata
func (srv *Server) CurrentFunctionCall(ctx context.Context) (*core.FunctionCall, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.clientID == client.daggerSession.mainClientCallerID {
		return nil, fmt.Errorf("%w: main client caller has no current module", core.ErrNoCurrentModule)
	}
	return client.fnCall, nil
}

// Return the modules being served to the current client
func (srv *Server) CurrentServedDeps(ctx context.Context) (*core.SchemaBuilder, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.servedMods, nil
}

// The Client metadata of the main client caller (i.e. the one who created the
// session, typically the CLI invoked by the user)
func (srv *Server) MainClientCallerMetadata(ctx context.Context) (*engine.ClientMetadata, error) {
	record, err := srv.clientRecordFromContext(ctx)
	if err != nil {
		return nil, err
	}
	mainRecord, err := srv.clientRecordFromIDs(record.daggerSession.sessionID, record.daggerSession.mainClientCallerID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve session main client: %w", err)
	}
	return record.daggerSession.clientMetadataSnapshot(mainRecord)
}

// The Client metadata of a specific client ID within the same session as the
// current client.
func (srv *Server) SpecificClientMetadata(ctx context.Context, clientID string) (*engine.ClientMetadata, error) {
	record, err := srv.clientRecordFromContext(ctx)
	if err != nil {
		return nil, err
	}
	requested, err := srv.clientRecordFromIDs(record.daggerSession.sessionID, clientID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve session client: %w", err)
	}
	return record.daggerSession.clientMetadataSnapshot(requested)
}

func (srv *Server) clientAttachableCaller(
	ctx context.Context,
	sessID, clientID string,
	ifAvailable bool,
) (engineutil.SessionCaller, bool, error) {
	record, err := srv.clientRecordFromIDs(sessID, clientID)
	if err != nil {
		return nil, false, err
	}
	if ifAvailable {
		caller, ok := record.daggerSession.attachables.Lookup(record.clientID)
		return caller, ok, nil
	}
	caller, err := record.daggerSession.attachables.Wait(ctx, record.clientID)
	if err != nil {
		return nil, false, err
	}
	if caller == nil {
		return nil, false, fmt.Errorf("session attachable caller for client %q was nil", clientID)
	}
	return caller, true, nil
}

func (srv *Server) SpecificClientAttachableConn(ctx context.Context, clientID string, opts core.SpecificClientAttachableConnOpts) (*grpc.ClientConn, bool, error) {
	record, err := srv.clientRecordFromContext(ctx)
	if err != nil {
		return nil, false, err
	}
	caller, ok, err := srv.clientAttachableCaller(ctx, record.daggerSession.sessionID, clientID, opts.IfAvailable)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get session attachable caller for client %q: %w", clientID, err)
	}
	if !ok {
		return nil, false, nil
	}

	conn := caller.Conn()
	if conn == nil {
		return nil, false, fmt.Errorf("session attachable conn for client %q was nil", clientID)
	}
	return conn, true, nil
}

// SessionScopedContext returns a context that lives for the remainder of the
// current client's session: it is detached from the given context's
// cancellation and is canceled when the session begins closing.
func (srv *Server) SessionScopedContext(ctx context.Context) (context.Context, error) {
	record, err := srv.clientRecordFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return record.daggerSession.withClosingCancel(context.WithoutCancel(ctx)), nil
}

func (srv *Server) sessionMainClientConn(ctx context.Context, sess *daggerSession) (*grpc.ClientConn, error) {
	if sess == nil {
		return nil, errors.New("session is nil")
	}
	record, err := srv.clientRecordFromIDs(sess.sessionID, sess.mainClientCallerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get main client %q: %w", sess.mainClientCallerID, err)
	}
	caller, _, err := srv.clientAttachableCaller(ctx, sess.sessionID, record.clientID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get main client caller %q: %w", sess.mainClientCallerID, err)
	}
	if caller == nil {
		return nil, fmt.Errorf("main client caller %q was nil", sess.mainClientCallerID)
	}
	conn := caller.Conn()
	if conn == nil {
		return nil, fmt.Errorf("main client conn %q was nil", sess.mainClientCallerID)
	}
	return conn, nil
}

// The nearest ancestor client that is not a module (either a caller from the host like the CLI
// or a nested exec). Useful for figuring out where local sources should be resolved from through
// chains of dependency modules.
func (srv *Server) nonModuleParentClient(ctx context.Context) (*clientRuntime, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.mod.Self() == nil {
		// not a module client, return the current client
		return client, nil
	}
	ancestors, err := client.daggerSession.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return nil, fmt.Errorf("resolve non-module parent ancestry: %w", err)
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		parent := ancestors[i]
		if parent.mod.Self() == nil {
			// not a module client: match
			return parent, nil
		}
	}
	return nil, fmt.Errorf("no non-module parent found")
}

// The nearest ancestor client that is not a module (either a caller from the host like the CLI
// or a nested exec). Useful for figuring out where local sources should be resolved from through
// chains of dependency modules.
func (srv *Server) NonModuleParentClientMetadata(ctx context.Context) (*engine.ClientMetadata, error) {
	client, err := srv.nonModuleParentClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.clientMetadataSnapshot(client.clientRecord)
}

// The default deps of every user module (currently just core)
func (srv *Server) DefaultDeps(ctx context.Context) (*core.SchemaBuilder, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.defaultDeps.Clone(), nil
}

func (srv *Server) TelemetrySeenKeyStore(ctx context.Context) (dagql.TelemetrySeenKeyStore, error) {
	record, err := srv.clientRecordFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return record.daggerSession, nil
}

// CallPayloadSeenKeyStore returns the claim store for call-payload telemetry
// (core/dag_call_telemetry.go), scoped to the current client's DELIVERY
// domain rather than the whole session.
//
// Telemetry emitted in a client's context is delivered to that client's DB
// and every ancestor's (PubSub's fan-out in engine/server/telemetry.go), so a
// session-wide claim would let one client's emission permanently satisfy the
// claim for clients that never received it: a client attaching to the session
// later — a nested `dagger agent`, a sibling joining via a shared session ID —
// could then never obtain the payloads its ID rebuilds need, and every agent
// referenced through an already-claimed frame would be unaddressable there.
// Claiming per delivery target instead marks a digest "seen" exactly where it
// actually landed, so a later client's first closure walk re-publishes into
// its own domain.
func (srv *Server) CallPayloadSeenKeyStore(ctx context.Context) (dagql.TelemetrySeenKeyStore, error) {
	record, err := srv.clientRecordFromContext(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := record.daggerSession.telemetryDeliveryClientIDs(record)
	if err != nil {
		return nil, fmt.Errorf("resolve call payload delivery route: %w", err)
	}
	return &callPayloadDeliveryStore{
		session: record.daggerSession,
		targets: targets,
	}, nil
}

// callPayloadDeliveryStore marks seen-keys per delivery target — the emitting
// client and its ancestors, exactly the DBs PubSub fans this telemetry out to —
// backed by the session's seen-key map with a per-target suffix. A key counts
// as seen only when EVERY target has it, so an emission is skipped only when
// nobody in the delivery domain still needs it. The NUL separator cannot occur
// in a digest or client ID, keeping the suffixed key space disjoint from the
// session store's other keys by construction.
type callPayloadDeliveryStore struct {
	session *daggerSession
	targets []string
}

var _ dagql.TelemetrySeenKeyStore = (*callPayloadDeliveryStore)(nil)

func (s *callPayloadDeliveryStore) LoadOrStoreTelemetrySeenKey(key string) bool {
	seenEverywhere := true
	for _, target := range s.targets {
		if !s.session.LoadOrStoreTelemetrySeenKey(key + "\x00" + target) {
			seenEverywhere = false
		}
	}
	return seenEverywhere
}

func (s *callPayloadDeliveryStore) StoreTelemetrySeenKey(key string) {
	for _, target := range s.targets {
		s.session.StoreTelemetrySeenKey(key + "\x00" + target)
	}
}

// The DagQL server for the current client's session
func (srv *Server) Server(ctx context.Context) (*dagql.Server, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.dag, nil
}

// Mix in this http endpoint+handler to the current client's session
func (srv *Server) MuxEndpoint(ctx context.Context, path string, handler http.Handler) error {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return err
	}
	client.daggerSession.endpointMu.Lock()
	defer client.daggerSession.endpointMu.Unlock()
	client.daggerSession.endpoints[path] = handler
	return nil
}

// The auth provider for the current client
func (srv *Server) Auth(ctx context.Context) (*auth.RegistryAuthProvider, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.authProvider, nil
}

// The session-owned engine utility gateway for the current executable scope.
func (srv *Server) Engine(ctx context.Context) (*engineutil.Client, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.daggerSession.engineUtilClient == nil {
		return nil, errors.New("session engine gateway not initialized")
	}
	return client.daggerSession.engineUtilClient, nil
}

func (srv *Server) RegistryResolver(ctx context.Context) (*serverresolver.Resolver, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if client.daggerSession.resolver == nil {
		return nil, errors.New("session registry resolver not initialized")
	}
	return client.daggerSession.resolver, nil
}

// The services for the current client's session
func (srv *Server) Services(ctx context.Context) (*core.Services, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.services, nil
}

// The agent runtimes for the current client's session
func (srv *Server) Agents(ctx context.Context) (*core.AgentRuntimes, error) {
	client, err := srv.executableClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.daggerSession.agents, nil
}

// The default platform for the engine as a whole
func (srv *Server) Platform() core.Platform {
	return core.Platform(srv.defaultPlatform)
}

// The content store for the engine as a whole
func (srv *Server) OCIStore() content.Store {
	return srv.contentStore
}

func (srv *Server) BuiltinOCIStore() content.Store {
	return srv.builtinContentStore
}

// The dns configuration for the engine as a whole
func (srv *Server) DNS() *oci.DNSConfig {
	return srv.dns
}

// The lease manager for the engine as a whole
func (srv *Server) LeaseManager() *bkcache.LeaseManager {
	return srv.leaseManager
}

// A shared engine-wide salt used when creating cache keys for secrets based on their plaintext
func (srv *Server) SecretSalt() []byte {
	return srv.secretSalt
}

// Provides access to the client's telemetry database.
func (srv *Server) FlushSessionTelemetry(ctx context.Context) error {
	record, err := srv.clientRecordFromContext(ctx)
	if err != nil {
		return err
	}
	return record.daggerSession.FlushTelemetry(ctx, "FlushSessionTelemetry API")
}

func (srv *Server) ClientTelemetry(ctx context.Context, sessID, clientID string) (*clientdb.DB, error) {
	record, err := srv.clientRecordFromIDs(sessID, clientID)
	if err != nil {
		return nil, err
	}
	// Flush the one session-owned telemetry pipeline before captureLogs walks the
	// requested DB.
	if err := record.daggerSession.FlushTelemetry(ctx, "ClientTelemetry API"); err != nil {
		return nil, fmt.Errorf("flush telemetry: %w", err)
	}
	return record.TelemetryDB(ctx)
}

// Return a client connected to a cloud engine. If bool return is false, the local engine should be used. Session attachables for the returned client will be proxied back to the calling client.
func (srv *Server) CloudEngineClient(
	ctx context.Context,
	module string,
	function string,
	execCmd []string,
) (*engineclient.Client, bool, error) {
	parentClient, err := srv.nonModuleParentClient(ctx)
	if err != nil {
		return nil, false, err
	}
	parentCallerCtx := engine.ContextWithClientMetadata(ctx, parentClient.clientMetadata)
	parentSession, err := parentClient.daggerSession.engineUtilClient.GetSessionCaller(parentCallerCtx)
	if err != nil {
		return nil, false, err
	}

	// TODO: cloud support for "run on yourself", return (nil, false, nil) in that case

	engineClient, err := engineclient.ConnectEngineToEngine(ctx, engineclient.EngineToEngineParams{
		Params: engineclient.Params{
			RunnerHost: engine.DefaultCloudRunnerHost,

			Module:   module,
			Function: function,
			ExecCmd:  execCmd,

			CloudAuth: parentClient.clientMetadata.CloudAuth,

			EngineTrace: parentClient.spanExporter,
			EngineLogs:  parentClient.logExporter,
			EngineMetrics: []sdkmetric.Exporter{originMetricExporter{
				origin: parentClient.clientID,
				next:   parentClient.daggerSession.metricExporter,
			}},

			// FIXME: for now, disable recursive scale out to prevent any
			// surprise "fork-bomb" scenarios. Eventually this should be
			// permitted.
			EnableCloudScaleOut: false,
		},
		CallerSessionConn: parentSession.Conn(),
		Labels:            enginetel.NewLabels(parentClient.clientMetadata.Labels, nil, nil),
		StableClientID:    parentClient.clientMetadata.ClientStableID,
	})
	if err != nil {
		return nil, false, err
	}

	return engineClient, true, nil
}

// A mount namespace guaranteed to not have any mounts created by engine operations.
// Should be used when creating goroutines/processes that unshare a mount namespace,
// otherwise those unshared mnt namespaces may inherit mounts from engine operations
// and leak them.
func (srv *Server) CleanMountNS() *os.File {
	return srv.cleanMntNS
}

func (srv *Server) EngineVolumeState() core.EngineVolumeState {
	return core.EngineVolumeState{
		RootDir:                    srv.rootDir,
		RecursiveReadOnlySupported: srv.recursiveReadOnlyMounts,
	}
}

type httpError struct {
	error
	code int
}

func httpErr(err error, code int) httpError {
	return httpError{err, code}
}

func (e httpError) WriteTo(w http.ResponseWriter) {
	http.Error(w, e.Error(), e.code)
}

type gqlError struct {
	error
	httpCode int
}

func gqlErr(err error, httpCode int) gqlError {
	return gqlError{err, httpCode}
}

func (e gqlError) WriteTo(w http.ResponseWriter) {
	gqlerr := &gqlerror.Error{
		Err:     e.error,
		Message: e.Error(),
	}
	res := graphql.Response{
		Errors: gqlerror.List{gqlerr},
	}
	bytes, err := json.Marshal(res)
	if err != nil {
		panic(err)
	}
	http.Error(w, string(bytes), e.httpCode)
}

// httpHandlerFunc lets you write an http handler that just returns an error, which will be
// turned into a 500 http response if non-nil, or a specific code if the error is of type httpError.
// It also accepts a generic extra argument that will be passed to the handler function to support
// providing any extra already initialized state.
func httpHandlerFunc[T any](fn func(http.ResponseWriter, *http.Request, T) error, t T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r, t)
		if err == nil {
			return
		}

		bklog.G(r.Context()).
			WithField("method", r.Method).
			WithField("path", r.URL.Path).
			WithError(err).Error("failed to serve request")

		// check whether this is a hijacked connection, if so we can't write any http errors to it
		if _, testErr := w.Write(nil); testErr == http.ErrHijacked {
			return
		}

		var httpErr httpError
		var gqlErr gqlError
		switch {
		case errors.As(err, &httpErr):
			httpErr.WriteTo(w)
		case errors.As(err, &gqlErr):
			gqlErr.WriteTo(w)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
