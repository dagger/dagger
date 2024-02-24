package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/dagger/dagger/telemetry/sdklog/otlploghttp/transform"
	"github.com/dagger/dagger/tracing"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/leaseutil"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type InitializeArgs struct {
	BuildkitClient *buildkit.Client
	Platform       specs.Platform
	OCIStore       content.Store
	LeaseManager   *leaseutil.Manager
	Auth           *auth.RegistryAuthProvider
	Secrets        *core.SecretStore
	PubSub         *tracing.PubSub
}

type OtelSubscriber interface {
	Subscribe(trace.TraceID, otlptrace.Client) func()
}

type APIServer struct {
	// The root of the schema, housing all of the state and dependencies for the
	// server for easy access from descendent objects.
	root    *core.Query
	pubsub  *tracing.PubSub
	traceID trace.TraceID
}

func New(ctx context.Context, params InitializeArgs) (*APIServer, error) {
	svcs := core.NewServices(params.BuildkitClient)

	root := core.NewRoot()
	root.Buildkit = params.BuildkitClient
	root.Services = svcs
	root.Platform = core.Platform(params.Platform)
	root.Secrets = params.Secrets
	root.OCIStore = params.OCIStore
	root.LeaseManager = params.LeaseManager
	root.Auth = params.Auth

	dag := dagql.NewServer(root)

	// stash away the cache so we can share it between other servers
	root.Cache = dag.Cache

	dag.Around(tracing.AroundFunc)

	coreMod := &CoreMod{dag: dag}
	if err := coreMod.Install(ctx, dag); err != nil {
		return nil, err
	}

	// the main client caller starts out with the core API loaded
	root.InstallDefaultClientContext(
		core.NewModDeps(root, []core.Mod{coreMod}),
	)

	traceID := trace.SpanContextFromContext(ctx).TraceID()
	if !traceID.IsValid() {
		return nil, fmt.Errorf("invalid traceID")
	}

	return &APIServer{
		root:    root,
		pubsub:  params.PubSub,
		traceID: traceID,
	}, nil
}

func (s *APIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	slog.Debug("schema serving HTTP with trace",
		"ctx", trace.SpanContextFromContext(ctx).TraceID(),
		"method", r.Method,
		"path", r.URL.Path)

	errorOut := func(err error, code int) {
		bklog.G(ctx).WithError(err).Error("failed to serve request")
		http.Error(w, err.Error(), code)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		errorOut(err, http.StatusInternalServerError)
		return
	}

	callContext, ok := s.root.ClientCallContext(clientMetadata.ModuleCallerDigest)
	if !ok {
		errorOut(fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest), http.StatusInternalServerError)
		return
	}

	schema, err := callContext.Deps.Schema(ctx)
	if err != nil {
		// TODO: technically this is not *always* bad request, should ideally be more specific and differentiate
		errorOut(err, http.StatusBadRequest)
		return
	}

	defer func() {
		if v := recover(); v != nil {
			bklog.G(context.TODO()).Errorf("panic serving schema: %v %s", v, string(debug.Stack()))
			// check whether this is a hijacked connection, if so we can't write any http errors to it
			_, err := w.Write(nil)
			if err == http.ErrHijacked {
				return
			}
			gqlErr := &gqlerror.Error{
				Message: "Internal Server Error",
			}
			code := http.StatusInternalServerError
			switch v := v.(type) {
			case error:
				gqlErr.Err = v
				gqlErr.Message = v.Error()
			case string:
				gqlErr.Message = v
			}
			res := graphql.Response{
				Errors: gqlerror.List{gqlErr},
			}
			bytes, err := json.Marshal(res)
			if err != nil {
				panic(err)
			}
			http.Error(w, string(bytes), code)
		}
	}()

	srv := handler.NewDefaultServer(schema)

	// NB: break glass when needed:
	// srv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	// 	res := next(ctx)
	// 	pl, err := json.Marshal(res)
	// 	slog.Debug("graphql response", "response", string(pl), "error", err)
	// 	return res
	// })

	mux := http.NewServeMux()

	mux.Handle("/query", srv)

	mux.Handle("/shutdown", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		bklog.G(ctx).Debugf("shutting down client %s from caller %s", clientMetadata.ClientID, clientMetadata.ModuleCallerDigest)

		if err := s.root.Services.StopClientServices(ctx, clientMetadata); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to shutdown")
		}

		if clientMetadata.ModuleCallerDigest == "" {
			// Flush all in-flight telemetry when a main client goes away.
			//
			// Awkwardly, we're flushing in-flight telemetry for _all_ clients when
			// _each_ client goes away. But we're only flushing the PubSub exporter,
			// which doesn't seem expensive enough to be worth the added complexity
			// of somehow only flushing a single client. This exporter already
			// flushes every 100ms anyway, so this really just helps ensure the last
			// few spans are received.
			tracing.FlushLiveProcessors(ctx)

			// Drain active /trace connections.
			//
			// NB(vito): Technically it should be safe to just let them be
			// interrupted when the connection dies, but draining/flushing is a pain
			// in the butt to troubleshoot, so it feels a bit nicer to do it more
			// methodically. I added this, then learned I don't need it, but if I'm
			// ever troubleshooting this again I'm likely to just write this code
			// again, so I kept it.
			s.pubsub.Drain(s.traceID)
		}
	}))

	mux.Handle("/trace", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusNotImplemented)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		exp, err := otlptrace.New(ctx, &chunkedTraceClient{
			w: w,
			f: flusher,
		})
		if err != nil {
			slog.Warn("failed to create exporter", "err", err)
			http.Error(w, "failed to create exporter", http.StatusInternalServerError)
			return
		}
		s.pubsub.SubscribeToSpans(ctx, s.traceID, exp)
	}))

	mux.Handle("/logs", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusNotImplemented)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		exp := &chunkedLogsClient{
			w: w,
			f: flusher,
		}
		if err := exp.Start(ctx); err != nil {
			slog.Warn("failed to emit first chunk", "err", err)
			return
		}
		s.pubsub.SubscribeToLogs(ctx, s.traceID, exp)
	}))

	s.root.MuxEndpoints(mux)

	r = r.WithContext(ctx)

	var handler http.Handler = mux
	handler = flushAfterNBytes(buildkit.MaxFileContentsChunkSize)(handler)
	handler.ServeHTTP(w, r)
}

type chunkedTraceClient struct {
	w http.ResponseWriter
	f http.Flusher
	l sync.Mutex
}

var _ otlptrace.Client = (*chunkedTraceClient)(nil)

func (h *chunkedTraceClient) Start(ctx context.Context) error {
	slog.Info("attached to traces; sending initial response")
	fmt.Fprintf(h.w, "0\n")
	h.f.Flush()
	return nil
}

func (h *chunkedTraceClient) Stop(ctx context.Context) error {
	return nil
}

func (h *chunkedTraceClient) UploadTraces(ctx context.Context, protoSpans []*tracepb.ResourceSpans) error {
	h.l.Lock()
	defer h.l.Unlock()
	pbRequest := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: protoSpans,
	}
	rawRequest, err := proto.Marshal(pbRequest)
	if err != nil {
		return err
	}
	// TODO hacky length-prefixed encoding
	fmt.Fprintf(h.w, "%d\n", len(rawRequest))
	h.w.Write(rawRequest)
	h.f.Flush()
	return nil
}

type chunkedLogsClient struct {
	w http.ResponseWriter
	f http.Flusher
	l sync.Mutex
}

func (h *chunkedLogsClient) Start(ctx context.Context) error {
	slog.Info("attached to traces; sending initial response")
	fmt.Fprintf(h.w, "0\n")
	h.f.Flush()
	return nil
}

var _ sdklog.LogExporter = (*chunkedLogsClient)(nil)

func (h *chunkedLogsClient) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	h.l.Lock()
	defer h.l.Unlock()
	pbRequest := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: transform.Logs(logs),
	}
	rawRequest, err := proto.Marshal(pbRequest)
	if err != nil {
		return err
	}
	// TODO hacky length-prefixed encoding
	fmt.Fprintf(h.w, "%d\n", len(rawRequest))
	h.w.Write(rawRequest)
	h.f.Flush()
	return nil
}

func (h *chunkedLogsClient) Shutdown(ctx context.Context) error {
	return nil
}

func (s *APIServer) ShutdownClient(ctx context.Context, client *engine.ClientMetadata) error {
	return s.root.Services.StopClientServices(ctx, client)
}

func (s *APIServer) CurrentServedDeps(ctx context.Context) (*core.ModDeps, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, ok := s.root.ClientCallContext(clientMetadata.ModuleCallerDigest)
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}
	return callCtx.Deps, nil
}

func (s *APIServer) Introspect(ctx context.Context) (string, error) {
	return s.root.DefaultDeps.SchemaIntrospectionJSON(ctx, false)
}

type SchemaResolvers interface {
	Install()
}

func schemaIntrospectionJSON(ctx context.Context, dag *dagql.Server) (json.RawMessage, error) {
	data, err := dag.Query(ctx, introspection.Query, nil)
	if err != nil {
		return nil, fmt.Errorf("introspection query failed: %w", err)
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal introspection result: %w", err)
	}
	return json.RawMessage(jsonBytes), nil
}

func gqlObjectName(name string) string {
	// gql object name is capitalized camel case
	return strcase.ToCamel(name)
}

func namespaceObject(objName, namespace string) string {
	gqlObjName := gqlObjectName(objName)
	if rest := strings.TrimPrefix(gqlObjName, gqlObjectName(namespace)); rest != gqlObjName {
		if len(rest) == 0 {
			// objName equals namespace, don't namespace this
			return gqlObjName
		}
		// we have this case check here to check for a boundary
		// e.g. if objName="Postman" and namespace="Post", then we should still namespace
		// this to "PostPostman" instead of just going for "Postman" (but we should do that
		// if objName="PostMan")
		if 'A' <= rest[0] && rest[0] <= 'Z' {
			// objName has namespace prefixed, don't namespace this
			return gqlObjName
		}
	}

	return gqlObjectName(namespace + "_" + objName)
}

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}
