package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func (srv *Server) initArchives() error {
	// Telemetry archives survive worker cache resets and engine restart.
	srv.clientDBDir = filepath.Join(srv.rootDir, "telemetry", "clientdbs")
	srv.clientDBs = clientdb.NewDBs(srv.clientDBDir)
	config := archive.Config{Root: filepath.Join(srv.rootDir, "telemetry", "archives"), RemoveStore: srv.clientDBs.Remove}
	var err error
	if value := os.Getenv("_EXPERIMENTAL_DAGGER_ARCHIVE_TTL"); value != "" {
		config.TTL, err = time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("archive TTL: %w", err)
		}
	}
	if value := os.Getenv("_EXPERIMENTAL_DAGGER_ARCHIVE_QUOTA_BYTES"); value != "" {
		config.QuotaBytes, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("archive quota: %w", err)
		}
	}
	srv.archives, err = archive.NewManager(config)
	return err
}

func (sess *daggerSession) ensureArchive(traceID string) (rerr error) {
	srv := sess.telemetryPubSub.srv
	if srv.archives == nil {
		return nil
	}
	sess.archiveMu.Lock()
	defer sess.archiveMu.Unlock()
	defer func() {
		if rerr != nil {
			sess.archiveRegisterErr = rerr
		}
	}()
	if sess.archiveRegisterErr != nil {
		return sess.archiveRegisterErr
	}
	if sess.archiveManifest != nil {
		if sess.archiveManifest.TraceID != traceID {
			return errors.New("agent control crossed archive trace boundary")
		}
		return nil
	}
	manifest, err := srv.archives.Register(traceID, sess.mainClientCallerID)
	if err != nil {
		return err
	}
	sess.archiveManifest = &manifest
	// The session may have named itself before its first control record.
	if sess.archivePendingTitle.traceID == traceID {
		sess.setArchiveTitleLocked(manifest, sess.archivePendingTitle.title)
	}
	sess.archivePendingTitle = pendingArchiveTitle{}
	return nil
}

// pendingArchiveTitle holds a session title that arrived before the archive
// was registered.
type pendingArchiveTitle struct {
	traceID string
	title   string
}

// setArchiveTitle records a session title published by the main client into
// the active manifest, so listings show it while the session runs and an
// archive recovered after an engine crash keeps it. The exporter only passes
// titles whose origin is the main client: nested clients (e.g. a dagger agent
// run by a tool) share the trace and the main client's store, but do not name
// the session.
func (sess *daggerSession) setArchiveTitle(traceID, title string) {
	if sess.telemetryPubSub.srv.archives == nil || archive.SanitizeTitle(title) == "" {
		return
	}
	sess.archiveMu.Lock()
	defer sess.archiveMu.Unlock()
	if sess.archiveManifest == nil {
		sess.archivePendingTitle = pendingArchiveTitle{traceID: traceID, title: title}
		return
	}
	if sess.archiveManifest.TraceID != traceID {
		return
	}
	sess.setArchiveTitleLocked(*sess.archiveManifest, title)
}

// setArchiveTitleLocked requires archiveMu, which serializes title writes.
func (sess *daggerSession) setArchiveTitleLocked(manifest archive.Manifest, title string) {
	// Titles are advisory; a finalizing or sealed archive keeps its title.
	if err := sess.telemetryPubSub.srv.archives.SetTitle(manifest.TraceID, title); err != nil {
		slog.Debug("record archive title", "err", err)
	}
}

// The producer witness is independent of received control rows. Only origins
// whose immutable telemetry route reaches this archive's main client contribute.
func (sess *daggerSession) closeArchiveControl(ctx context.Context) error {
	origins, err := sess.agents.CloseControl(ctx)
	sess.archiveCloseErr = err
	if err != nil {
		return err
	}
	want := agentcontrol.Expectation{Agents: map[agentcontrol.Key]int64{}, Subscriptions: map[agentcontrol.EdgeKey]int64{}}
	for origin, expected := range origins {
		route, err := sess.telemetryRouteOriginClientID(origin)
		if err != nil {
			sess.archiveCloseErr = err
			return err
		}
		if !slices.Contains(route, sess.mainClientCallerID) {
			continue
		}
		for key, rev := range expected.Agents {
			if err := sess.ensureArchive(key.Trace); err != nil {
				return err
			}
			if old, ok := want.Agents[key]; ok && old != rev {
				return fmt.Errorf("conflicting expected agent revision")
			}
			want.Agents[key] = rev
		}
		for key, rev := range expected.Subscriptions {
			if old, ok := want.Subscriptions[key]; ok && old != rev {
				return fmt.Errorf("conflicting expected subscription revision")
			}
			want.Subscriptions[key] = rev
		}
	}
	sess.archiveExpected = want
	return nil
}

func (srv *Server) finalizeSessionArchive(ctx context.Context, sess *daggerSession, drainErr error) (rerr error) {
	sess.archiveMu.Lock()
	manifest := sess.archiveManifest
	registrationErr := sess.archiveRegisterErr
	sess.archiveMu.Unlock()
	if manifest == nil || srv.archives == nil {
		return registrationErr
	}
	defer func() {
		if rerr != nil {
			_ = srv.archives.MarkIncomplete(manifest.TraceID, rerr)
		}
	}()
	if err := errors.Join(drainErr, sess.archiveCloseErr, registrationErr); err != nil {
		return err
	}
	if sess.archiveExpected.Agents == nil {
		return errors.New("missing independent final roster witness")
	}
	if err := srv.archives.BeginFinalizing(manifest.TraceID); err != nil {
		return err
	}
	db, err := srv.clientDBs.Open(ctx, manifest.MainClientID)
	if err != nil {
		return err
	}
	defer db.Close()
	cut, err := db.Checkpoint(ctx)
	if err != nil {
		return err
	}
	data, records, err := buildArchiveBootstrap(ctx, db, *manifest, cut, sess.archiveExpected)
	if err != nil {
		return err
	}
	size, err := db.SizeBytes()
	if err != nil {
		return err
	}
	header, _, err := archive.VerifyBootstrap(bytes.NewReader(data))
	if err != nil {
		return err
	}
	seal, err := time.Parse(time.RFC3339Nano, header.SealAt)
	if err != nil {
		return err
	}
	_, err = srv.archives.Finalize(manifest.TraceID, archive.FinalizeInput{
		HighWater: header.HighWater, SealAt: seal, StoreSizeBytes: size, BootstrapBytes: data, BootstrapRecords: records,
	})
	return err
}

func buildArchiveBootstrap(ctx context.Context, db *clientdb.DB, manifest archive.Manifest, cut clientdb.HighWater, want agentcontrol.Expectation) ([]byte, int64, error) {
	return buildArchiveBootstrapWithPayloadLimit(ctx, db, manifest, cut, want, archive.MaxBootstrapPayloadSize)
}

func buildArchiveBootstrapWithPayloadLimit(ctx context.Context, db *clientdb.DB, manifest archive.Manifest, cut clientdb.HighWater, want agentcontrol.Expectation, maxPayloadSize int) ([]byte, int64, error) {
	if maxPayloadSize <= 0 || maxPayloadSize > archive.MaxBootstrapPayloadSize {
		return nil, 0, fmt.Errorf("invalid bootstrap payload limit %d", maxPayloadSize)
	}
	rows, err := db.ControlRows(ctx, manifest.TraceID, cut.Logs, want)
	if err != nil {
		return nil, 0, err
	}
	var roots []string
	for _, row := range rows {
		rec, err := clientdb.DecodeLogRecord(row)
		if err != nil {
			return nil, 0, err
		}
		a, _, err := agentcontrol.Decode(rec)
		if err != nil {
			return nil, 0, err
		}
		if a == nil {
			continue
		}
		if root, ok := a.ClosureRoot(); ok {
			roots = append(roots, root)
		}
	}
	_, err = archive.VerifyClosure(roots, func(d string) (*callpbv1.Call, error) {
		row, err := db.CallPayload(ctx, manifest.TraceID, d, cut.Logs)
		if err != nil {
			return nil, err
		}
		rec, err := clientdb.DecodeLogRecord(row)
		if err != nil {
			return nil, err
		}
		var c callpbv1.Call
		if err := proto.Unmarshal(rec.Body().AsBytes(), &c); err != nil {
			return nil, err
		}
		rows = append(rows, row)
		return &c, nil
	})
	if err != nil {
		return nil, 0, err
	}
	slices.SortFunc(rows, func(a, b clientdb.Log) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	header := archive.BootstrapHeader{TraceID: manifest.TraceID, SealAt: time.Now().UTC().Format(time.RFC3339Nano), HighWater: archive.HighWater{Spans: cut.Spans, Logs: cut.Logs, Metrics: cut.Metrics}, Completion: archive.Witness(want)}
	var signals []archive.BootstrapSignal
	var batches []archive.BootstrapBatch
	for start := 0; start < len(rows); {
		end := min(start+otlpBatchSize, len(rows))
		req := &collogspb.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(rows[start:end])}
		// As with live telemetry, shrink an oversized batch to a smaller prefix.
		// A single oversized record is fatal: a verified closure cannot omit it.
		for proto.Size(req) > maxPayloadSize {
			if end-start == 1 {
				return nil, 0, fmt.Errorf("bootstrap log row %d is %d bytes (maximum %d)", rows[start].ID, proto.Size(req), maxPayloadSize)
			}
			end = start + (end-start)/2
			req = &collogspb.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(rows[start:end])}
		}
		payload, err := proto.Marshal(req)
		if err != nil {
			return nil, 0, err
		}
		signals = append(signals, archive.BootstrapSignal{Kind: archive.BootstrapFrameLogs, Payload: payload, Records: int64(end - start)})
		batches = append(batches, archive.BootstrapBatch{Logs: req})
		start = end
	}
	// Verify converted OTLP as well: the display codec must not silently skip a
	// malformed row and leave a plausible terminal count/empty projection.
	if err := archive.ValidateBootstrap(ctx, header, batches); err != nil {
		return nil, 0, err
	}
	return archive.BuildBootstrap(header, signals)
}

func (srv *Server) archiveRequestRecord(clientID, sessionID, token string) (*clientRecord, error) {
	record, err := srv.clientRecordFromIDs(sessionID, clientID)
	if err != nil {
		return nil, err
	}
	if record.clientID != record.daggerSession.mainClientCallerID {
		return nil, errors.New("archive API requires main client authority")
	}
	record.daggerSession.scopeMu.Lock()
	stored := ""
	if record.clientMetadata != nil {
		stored = record.clientMetadata.ClientSecretToken
	}
	record.daggerSession.scopeMu.Unlock()
	if stored == "" || subtle.ConstantTimeCompare([]byte(stored), []byte(token)) != 1 {
		return nil, errors.New("invalid client secret token")
	}
	return record, nil
}

func (srv *Server) serveArchiveHTTP(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	if srv.archives == nil {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureNotFound})
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/telemetry/archives"), "/")
	if path == "" {
		if r.Method != http.MethodGet {
			return httpErr(errors.New("method not allowed"), http.StatusMethodNotAllowed)
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		record.daggerSession.archiveMu.Lock()
		exclude := ""
		if m := record.daggerSession.archiveManifest; m != nil {
			exclude = m.TraceID
		}
		record.daggerSession.archiveMu.Unlock()
		return writeArchiveJSON(w, http.StatusOK, srv.archives.List(r.URL.Query().Get("after"), exclude, limit))
	}
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		return httpErr(errors.New("unknown archive endpoint"), http.StatusNotFound)
	}
	if r.Method != http.MethodGet {
		return httpErr(errors.New("method not allowed"), http.StatusMethodNotAllowed)
	}
	m, err := srv.archives.Manifest(parts[0])
	if err != nil {
		return writeArchiveFailure(w, err)
	}
	// An unsealed archive (interrupted or incomplete) has no bootstrap or
	// verified cut; it can only be streamed at the current end of its store,
	// with control records included, for a best-effort restore.
	unsealed := r.URL.Query().Get("unsealed") == "1"
	if unsealed != m.State.Unsealed() || (!unsealed && m.State != archive.StateClosed) {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureState, State: m.State})
	}
	cut := m.HighWater
	if unsealed {
		if cut, err = srv.unsealedArchiveCut(r.Context(), m); err != nil {
			return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureIO, Err: err})
		}
	}
	if len(parts) == 1 {
		m.HighWater = cut
		return writeArchiveJSON(w, http.StatusOK, m)
	}
	switch resource := parts[1]; resource {
	case archive.AgentBootstrapResource:
		if unsealed {
			return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureState, State: m.State})
		}
		return serveArchiveBootstrap(w, srv.archives.BootstrapPath(m.TraceID), m)
	case "traces", "logs", "metrics":
		return srv.serveArchiveSignal(w, r, m, cut, unsealed, resource)
	default:
		return httpErr(errors.New("unknown archive endpoint"), http.StatusNotFound)
	}
}

// unsealedArchiveCut is the current end of an unsealed archive's store. Its
// session is gone, so the cut is stable across every stream.
func (srv *Server) unsealedArchiveCut(ctx context.Context, m archive.Manifest) (archive.HighWater, error) {
	db, err := srv.clientDBs.Open(ctx, m.MainClientID)
	if err != nil {
		return archive.HighWater{}, err
	}
	defer db.Close()
	cut := db.HighWater()
	return archive.HighWater{Spans: cut.Spans, Logs: cut.Logs, Metrics: cut.Metrics}, nil
}

func serveArchiveBootstrap(w http.ResponseWriter, path string, m archive.Manifest) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// Collected since the manifest lookup.
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureNotFound})
	}
	if err != nil {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureCorrupt, Err: err})
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != m.Bootstrap.SHA256 {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureCorrupt, Err: errors.New("bootstrap checksum mismatch")})
	}
	if _, _, err := archive.VerifyBootstrap(bytes.NewReader(data)); err != nil {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureCorrupt, Err: err})
	}
	w.Header().Set("Content-Type", archive.BootstrapContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, err = w.Write(data)
	return err
}

// serveArchiveSignal streams one OTLP signal up to cut. A sealed archive's
// logs omit control records, which its bootstrap already supplied; an unsealed
// archive has no bootstrap, so they are included. Call payloads the bootstrap
// also carried are re-sent: the frontend ignores digests it already has.
func (srv *Server) serveArchiveSignal(w http.ResponseWriter, r *http.Request, m archive.Manifest, cut archive.HighWater, includeControl bool, signal string) error {
	return srv.serveArchiveSignalWithPayloadLimit(w, r, m, cut, includeControl, signal, enginetel.MaxLivePayloadSize)
}

//nolint:gocyclo // Keep bounded batching and cursor advancement in one stream state machine.
func (srv *Server) serveArchiveSignalWithPayloadLimit(w http.ResponseWriter, r *http.Request, m archive.Manifest, cut archive.HighWater, includeControl bool, signal string, maxPayloadSize int) (rerr error) {
	if maxPayloadSize <= 0 || maxPayloadSize > enginetel.MaxLivePayloadSize {
		return fmt.Errorf("invalid archive payload limit %d", maxPayloadSize)
	}
	cursor := int64(0)
	if s := r.Header.Get(enginetel.LiveCursorHeader); s != "" {
		var err error
		cursor, err = strconv.ParseInt(s, 10, 64)
		if err != nil || cursor < 0 {
			return httpErr(errors.New("invalid archive cursor"), http.StatusBadRequest)
		}
	}
	high := cut.Spans
	switch signal {
	case "logs":
		high = cut.Logs
	case "metrics":
		high = cut.Metrics
	}
	if cursor > high {
		return httpErr(errors.New("cursor exceeds archive cut"), http.StatusBadRequest)
	}
	db, err := srv.clientDBs.Open(r.Context(), m.MainClientID)
	if err != nil {
		return writeArchiveFailure(w, &archive.Failure{Kind: archive.FailureIO, Err: err})
	}
	defer db.Close()
	w.Header().Set("Content-Type", enginetel.LiveContentType)
	w.Header().Set("Cache-Control", "no-store")
	defer func() {
		if rerr != nil {
			_ = enginetel.WriteLiveError(w, cursor, rerr)
		}
	}()
	batchLimit := otlpBatchSize
	for cursor < high {
		if err := r.Context().Err(); err != nil {
			return err
		}
		var message proto.Message
		var next int64
		var rowCount int
		switch signal {
		case "traces":
			rows, err := db.SelectSpansRange(r.Context(), clientdb.SelectSpansRangeParams{AfterID: cursor, ThroughID: high, Limit: int64(batchLimit)})
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return errors.New("archive span stream truncated before cut")
			}
			next, rowCount = rows[len(rows)-1].ID, len(rows)
			var spans []sdktrace.ReadOnlySpan
			for _, row := range rows {
				if row.TraceID == m.TraceID {
					spans = append(spans, row.ReadOnly())
				}
			}
			message = &coltracepb.ExportTraceServiceRequest{ResourceSpans: telemetry.SpansToPB(spans)}
		case "logs":
			rows, err := db.SelectLogsRange(r.Context(), clientdb.SelectLogsRangeParams{AfterID: cursor, ThroughID: high, Limit: int64(batchLimit)})
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return errors.New("archive log stream truncated before cut")
			}
			next, rowCount = rows[len(rows)-1].ID, len(rows)
			filtered, err := archiveHistoryLogs(rows, m.TraceID, includeControl)
			if err != nil {
				return err
			}
			message = &collogspb.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(filtered)}
		case "metrics":
			rows, err := db.SelectMetricsRange(r.Context(), clientdb.SelectMetricsRangeParams{AfterID: cursor, ThroughID: high, Limit: int64(batchLimit)})
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return errors.New("archive metric stream truncated before cut")
			}
			next, rowCount = rows[len(rows)-1].ID, len(rows)
			message = &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: clientdb.MetricsToPB(rows)}
		}
		if size := proto.Size(message); size > maxPayloadSize {
			if rowCount == 1 {
				return fmt.Errorf("archive %s row %d is %d bytes (maximum %d)", signal, next, size, maxPayloadSize)
			}
			// Retry a smaller prefix at the last written cursor.
			batchLimit = max(1, rowCount/2)
			continue
		}
		payload, err := proto.Marshal(message)
		if err != nil {
			return err
		}
		if err := enginetel.WriteLiveFrame(w, next, payload); err != nil {
			return err
		}
		cursor = next
		batchLimit = otlpBatchSize
	}
	return enginetel.WriteLiveTerminal(w, high)
}
func archiveHistoryLogs(rows []clientdb.Log, traceID string, includeControl bool) ([]clientdb.Log, error) {
	var filtered []clientdb.Log
	for _, row := range rows {
		if row.TraceID.String != traceID {
			continue
		}
		rec, err := clientdb.DecodeLogRecord(row)
		if err != nil {
			return nil, err
		}
		// A sealed archive's bootstrap is the sole source control cut. Historical
		// records cannot redefine it or interfere with destination runtime
		// incarnations.
		if agentcontrol.IsRecord(rec) && !includeControl {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered, nil
}

func writeArchiveJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}
func writeArchiveFailure(w http.ResponseWriter, err error) error {
	status := http.StatusServiceUnavailable
	kind := archive.FailureIO
	state := archive.State("")
	var failure *archive.Failure
	if errors.As(err, &failure) {
		kind, state = failure.Kind, failure.State
		switch kind {
		case archive.FailureNotFound:
			status = http.StatusNotFound
		case archive.FailureState:
			status = http.StatusConflict
		case archive.FailureCorrupt:
			status = http.StatusUnprocessableEntity
		}
	}
	return writeArchiveJSON(w, status, struct {
		Kind    archive.FailureKind `json:"error"`
		State   archive.State       `json:"state"`
		Message string              `json:"message"`
	}{kind, state, err.Error()})
}
