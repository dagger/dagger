package server

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/internal/odag/cloudpull"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

type Config struct {
	ListenAddr string
	DBPath     string
	DevMode    bool
	WebDir     string
}

type Server struct {
	cfg   Config
	store *store.Store
	http  *http.Server
	web   *webAssets
}

func New(cfg Config) (*Server, error) {
	if cfg.ListenAddr == "" {
		return nil, fmt.Errorf("listen address is required")
	}
	if cfg.DBPath == "" {
		return nil, fmt.Errorf("db path is required")
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	web, err := newWebAssets(cfg)
	if err != nil {
		return nil, err
	}

	srv := &Server{
		cfg:   cfg,
		store: st,
		web:   web,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", srv.handleHealthz)
	mux.HandleFunc("GET /", srv.handleIndex)
	mux.HandleFunc("GET /__odag_dev_hash", srv.handleDevHash)
	mux.HandleFunc("POST /v1/traces", srv.handleTraceIngest)
	mux.HandleFunc("POST /v1/logs", srv.handleNoopIngest)
	mux.HandleFunc("POST /v1/metrics", srv.handleNoopIngest)
	mux.HandleFunc("GET /api/traces", srv.handleListTraces)
	mux.HandleFunc("GET /api/traces/{traceID}/meta", srv.handleTraceMeta)
	mux.HandleFunc("GET /api/traces/{traceID}/events", srv.handleTraceEvents)
	mux.HandleFunc("GET /api/traces/{traceID}/snapshot", srv.handleTraceSnapshot)
	mux.HandleFunc("POST /api/traces/open", srv.handleOpenTrace)
	mux.HandleFunc("GET /api/v2/spans", srv.handleV2Spans)
	mux.HandleFunc("GET /api/v2/calls", srv.handleV2Calls)
	mux.HandleFunc("GET /api/v2/object-snapshots", srv.handleV2ObjectSnapshots)
	mux.HandleFunc("GET /api/v2/object-bindings", srv.handleV2ObjectBindings)
	mux.HandleFunc("GET /api/v2/mutations", srv.handleV2Mutations)
	mux.HandleFunc("GET /api/v2/sessions", srv.handleV2Sessions)
	mux.HandleFunc("GET /api/v2/clients", srv.handleV2Clients)

	srv.http = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ConnState: func(conn net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				log.Printf("odag: client connected: %s", conn.RemoteAddr().String())
			case http.StateClosed, http.StateHijacked:
				log.Printf("odag: client disconnected: %s", conn.RemoteAddr().String())
			}
		},
	}

	return srv, nil
}

func (s *Server) Run(ctx context.Context) error {
	defer s.store.Close()

	errCh := make(chan error, 1)
	go func() {
		err := s.http.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.web.serve(w, r)
}

func (s *Server) handleDevHash(w http.ResponseWriter, r *http.Request) {
	if s.web == nil || !s.web.devMode {
		http.NotFound(w, r)
		return
	}
	hash, err := s.web.devHash()
	if err != nil {
		http.Error(w, fmt.Sprintf("dev hash: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(hash))
}

func (s *Server) handleListTraces(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		v, err := strconv.Atoi(rawLimit)
		if err != nil || v <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = v
	}

	traces, err := s.store.ListTraces(r.Context(), limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("list traces: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"traces": traces,
	})
}

func (s *Server) handleTraceMeta(w http.ResponseWriter, r *http.Request) {
	traceID := strings.TrimSpace(r.PathValue("traceID"))
	if traceID == "" {
		http.Error(w, "traceID is required", http.StatusBadRequest)
		return
	}

	trace, err := s.store.GetTrace(r.Context(), traceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("get trace: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, trace)
}

type openTraceRequest struct {
	Mode      string `json:"mode"`
	TraceID   string `json:"traceID"`
	Org       string `json:"org,omitempty"`
	TimeoutMS int64  `json:"timeoutMs,omitempty"`
}

func (s *Server) handleOpenTrace(w http.ResponseWriter, r *http.Request) {
	var req openTraceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Mode) == "" {
		req.Mode = "cloud"
	}
	if req.Mode != "cloud" {
		http.Error(w, "unsupported mode (expected 'cloud')", http.StatusBadRequest)
		return
	}
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.TraceID == "" {
		http.Error(w, "traceID is required", http.StatusBadRequest)
		return
	}

	timeout := 2 * time.Minute
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}

	res, err := cloudpull.PullTrace(r.Context(), s.store, req.TraceID, cloudpull.PullOptions{
		OrgName: strings.TrimSpace(req.Org),
		Timeout: timeout,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("pull trace: %v", err), http.StatusBadGateway)
		return
	}

	traceMeta, err := s.store.GetTrace(r.Context(), req.TraceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("lookup imported trace: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"result": res,
		"trace":  traceMeta,
	})
}

func (s *Server) handleTraceEvents(w http.ResponseWriter, r *http.Request) {
	traceID := strings.TrimSpace(r.PathValue("traceID"))
	if traceID == "" {
		http.Error(w, "traceID is required", http.StatusBadRequest)
		return
	}

	proj, err := s.projectTrace(r.Context(), traceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("project trace: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"traceID":  traceID,
		"warnings": proj.Warnings,
		"events":   proj.Events,
	})
}

func (s *Server) handleTraceSnapshot(w http.ResponseWriter, r *http.Request) {
	traceID := strings.TrimSpace(r.PathValue("traceID"))
	if traceID == "" {
		http.Error(w, "traceID is required", http.StatusBadRequest)
		return
	}

	proj, err := s.projectTrace(r.Context(), traceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("project trace: %v", err), http.StatusInternalServerError)
		return
	}

	var snap transform.Snapshot
	if rawStep := strings.TrimSpace(r.URL.Query().Get("step")); rawStep != "" {
		step, err := strconv.Atoi(rawStep)
		if err != nil || step < 0 {
			http.Error(w, "invalid step", http.StatusBadRequest)
			return
		}
		snap = transform.SnapshotAtStep(proj, step)
	} else {
		unixNano := proj.EndUnixNano
		if rawT := strings.TrimSpace(r.URL.Query().Get("t")); rawT != "" {
			v, err := strconv.ParseInt(rawT, 10, 64)
			if err != nil {
				http.Error(w, "invalid t", http.StatusBadRequest)
				return
			}
			unixNano = v
		}
		snap = transform.SnapshotAt(proj, unixNano)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"traceID":    traceID,
		"projection": proj,
		"snapshot":   snap,
	})
}

func (s *Server) projectTrace(ctx context.Context, traceID string) (*transform.TraceProjection, error) {
	if _, err := s.store.GetTrace(ctx, traceID); err != nil {
		return nil, err
	}

	spans, err := s.store.ListTraceSpans(ctx, traceID)
	if err != nil {
		return nil, err
	}

	return transform.ProjectTrace(traceID, spans)
}

func (s *Server) handleTraceIngest(w http.ResponseWriter, r *http.Request) {
	clientAddr := r.RemoteAddr
	if clientAddr == "" {
		clientAddr = "unknown"
	}

	body, err := readOTLPBody(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("read request body: %v", err), http.StatusBadRequest)
		return
	}

	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("decode OTLP protobuf: %v", err), http.StatusBadRequest)
		return
	}

	spans, err := spanRecordsFromOTLP(&req)
	if err != nil {
		http.Error(w, fmt.Sprintf("convert spans: %v", err), http.StatusBadRequest)
		return
	}
	traceCounts := countSpansByTrace(spans)
	for _, traceID := range sortedTraceIDs(traceCounts) {
		log.Printf("odag: client %s started sending trace %s (%d spans)", clientAddr, traceID, traceCounts[traceID])
	}

	summary, err := s.store.UpsertSpans(r.Context(), "collector", spans)
	if err != nil {
		http.Error(w, fmt.Sprintf("persist spans: %v", err), http.StatusInternalServerError)
		return
	}
	for _, traceID := range sortedTraceIDs(traceCounts) {
		log.Printf("odag: client %s completed trace %s (%d spans)", clientAddr, traceID, traceCounts[traceID])
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"traces": summary.Traces,
		"spans":  summary.Spans,
	})
}

func (s *Server) handleNoopIngest(w http.ResponseWriter, r *http.Request) {
	// OTLP exporters are configured as a set. Keep logs/metrics endpoints
	// available so clients can point all telemetry at odag without failures.
	_, _ = io.Copy(io.Discard, r.Body)
	w.WriteHeader(http.StatusAccepted)
}

func readOTLPBody(r *http.Request) ([]byte, error) {
	var reader io.Reader = r.Body
	if enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func spanRecordsFromOTLP(req *coltracepb.ExportTraceServiceRequest) ([]store.SpanRecord, error) {
	if req == nil {
		return nil, nil
	}

	var spans []store.SpanRecord
	now := time.Now().UnixNano()
	for _, rs := range req.ResourceSpans {
		resourceAttrs := keyValuesToMap(nil)
		if rs.GetResource() != nil {
			resourceAttrs = keyValuesToMap(rs.GetResource().GetAttributes())
		}

		for _, ss := range rs.GetScopeSpans() {
			scopeName := ""
			scopeVersion := ""
			if ss.GetScope() != nil {
				scopeName = ss.GetScope().GetName()
				scopeVersion = ss.GetScope().GetVersion()
			}

			for _, span := range ss.GetSpans() {
				traceID := hex.EncodeToString(span.GetTraceId())
				spanID := hex.EncodeToString(span.GetSpanId())
				if traceID == "" || spanID == "" {
					continue
				}

				data := make(map[string]any)
				if attrs := keyValuesToMap(span.GetAttributes()); len(attrs) > 0 {
					data["attributes"] = attrs
				}
				if len(resourceAttrs) > 0 {
					data["resource"] = resourceAttrs
				}
				if scopeName != "" || scopeVersion != "" {
					data["scope"] = map[string]any{
						"name":    scopeName,
						"version": scopeVersion,
					}
				}
				if events := spanEventsToJSON(span.GetEvents()); len(events) > 0 {
					data["events"] = events
				}
				if links := spanLinksToJSON(span.GetLinks()); len(links) > 0 {
					data["links"] = links
				}

				payload, err := json.Marshal(data)
				if err != nil {
					return nil, fmt.Errorf("marshal data for span %s/%s: %w", traceID, spanID, err)
				}

				spans = append(spans, store.SpanRecord{
					TraceID:         traceID,
					SpanID:          spanID,
					ParentSpanID:    hex.EncodeToString(span.GetParentSpanId()),
					Name:            span.GetName(),
					StartUnixNano:   u64ToI64(span.GetStartTimeUnixNano()),
					EndUnixNano:     u64ToI64(span.GetEndTimeUnixNano()),
					StatusCode:      span.GetStatus().GetCode().String(),
					StatusMessage:   span.GetStatus().GetMessage(),
					DataJSON:        string(payload),
					UpdatedUnixNano: now,
				})
			}
		}
	}

	return spans, nil
}

func spanEventsToJSON(events []*tracepb.Span_Event) []map[string]any {
	if len(events) == 0 {
		return nil
	}

	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		ev := map[string]any{
			"time_unix_nano": u64ToI64(event.GetTimeUnixNano()),
			"name":           event.GetName(),
		}
		if attrs := keyValuesToMap(event.GetAttributes()); len(attrs) > 0 {
			ev["attributes"] = attrs
		}
		out = append(out, ev)
	}
	return out
}

func spanLinksToJSON(links []*tracepb.Span_Link) []map[string]any {
	if len(links) == 0 {
		return nil
	}

	out := make([]map[string]any, 0, len(links))
	for _, link := range links {
		if link == nil {
			continue
		}
		item := map[string]any{
			"trace_id": hex.EncodeToString(link.GetTraceId()),
			"span_id":  hex.EncodeToString(link.GetSpanId()),
		}
		if attrs := keyValuesToMap(link.GetAttributes()); len(attrs) > 0 {
			item["attributes"] = attrs
		}
		out = append(out, item)
	}
	return out
}

func keyValuesToMap(attrs []*commonpb.KeyValue) map[string]any {
	if len(attrs) == 0 {
		return nil
	}

	out := make(map[string]any, len(attrs))
	for _, kv := range attrs {
		if kv == nil {
			continue
		}
		out[kv.GetKey()] = anyValueToJSON(kv.GetValue())
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func anyValueToJSON(v *commonpb.AnyValue) any {
	if v == nil {
		return nil
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue
	case *commonpb.AnyValue_ArrayValue:
		vals := x.ArrayValue.GetValues()
		out := make([]any, 0, len(vals))
		for _, item := range vals {
			out = append(out, anyValueToJSON(item))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		return keyValuesToMap(x.KvlistValue.GetValues())
	case *commonpb.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(x.BytesValue)
	default:
		return nil
	}
}

func u64ToI64(v uint64) int64 {
	const maxInt64 = ^uint64(0) >> 1
	if v > maxInt64 {
		return int64(maxInt64)
	}
	return int64(v)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func countSpansByTrace(spans []store.SpanRecord) map[string]int {
	counts := make(map[string]int, len(spans))
	for _, span := range spans {
		if span.TraceID == "" {
			continue
		}
		counts[span.TraceID]++
	}
	return counts
}

func sortedTraceIDs(counts map[string]int) []string {
	if len(counts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(counts))
	for traceID := range counts {
		ids = append(ids, traceID)
	}
	sort.Strings(ids)
	return ids
}
