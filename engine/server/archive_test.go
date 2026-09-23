package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	logapi "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const archiveTestTrace = "01000000000000000000000000000000"

func archiveAgent() agentcontrol.Agent {
	return agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "session", Trace: archiveTestTrace, Incarnation: "incarnation"}, Handle: "worker"}, Revision: 7, Name: "worker", State: "IDLE", Digest: "pending", Activity: time.Unix(10, 0)}
}
func controlTestRecord(t *testing.T, rec logapi.Record) sdklog.Record {
	t.Helper()
	capture := new(callPayloadRecordCapture)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(capture)))
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}}))
	provider.Logger("dagger.io/agent").Emit(ctx, rec)
	require.NoError(t, provider.Shutdown(t.Context()))
	require.Len(t, capture.records, 1)
	return capture.records[0]
}
func archiveFixture(t *testing.T) (*Server, *daggerSession, *clientdb.DB, agentcontrol.Expectation) {
	t.Helper()
	root := t.TempDir()
	dbs := clientdb.NewDBs(filepath.Join(root, "stores"))
	manager, err := archive.NewManager(archive.Config{Root: filepath.Join(root, "archives"), RemoveStore: dbs.Remove})
	require.NoError(t, err)
	srv := &Server{clientDBs: dbs, archives: manager}
	sess := &daggerSession{sessionID: "session", mainClientCallerID: "main", clientRecords: map[string]*clientRecord{}}
	sess.telemetryPubSub = NewPubSub(srv)
	sess.clientRecords["main"] = &clientRecord{daggerSession: sess, clientID: "main", clientMetadata: &engine.ClientMetadata{ClientSecretToken: "secret"}}
	require.NoError(t, sess.ensureArchive(archiveTestTrace))
	db, err := dbs.Open(t.Context(), "main")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	id := call.New().Append(&ast.Type{NamedType: "LLM", NonNull: true}, "llm")
	a := archiveAgent()
	a.Digest = id.Digest().String()
	data, err := proto.Marshal(id.Call())
	require.NoError(t, err)
	payload := scopedLogRecord(t, "test", logapi.BytesValue(data), logapi.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	for _, rec := range []sdklog.Record{controlTestRecord(t, a.Record()), payload} {
		row, err := logRecordRow(&rec)
		require.NoError(t, err)
		_, err = db.AppendLogs([]clientdb.Log{row})
		require.NoError(t, err)
	}
	want := agentcontrol.Expectation{Agents: map[agentcontrol.Key]int64{a.Key: a.Revision}, Subscriptions: map[agentcontrol.EdgeKey]int64{}}
	sess.archiveExpected = want
	return srv, sess, db, want
}
func TestArchiveFinalWitnessAndClosure(t *testing.T) {
	for _, failure := range []string{"none", "whole agent", "final revision", "subscription", "capture", "payload", "drain"} {
		t.Run(failure, func(t *testing.T) {
			srv, sess, db, want := archiveFixture(t)
			a := archiveAgent()
			switch failure {
			case "whole agent":
				key := a.Key
				key.Handle = "missing"
				want.Agents[key] = 1
			case "final revision":
				want.Agents[a.Key]++
			case "subscription":
				want.Subscriptions[agentcontrol.EdgeKey{Namespace: a.Namespace, Watched: "worker", Subscriber: "missing"}] = 1
			case "capture":
				a.Revision++
				a.Digest = ""
				a.CaptureError = "latest capture failed"
				want.Agents[a.Key] = a.Revision
				rec := controlTestRecord(t, a.Record())
				row, err := logRecordRow(&rec)
				require.NoError(t, err)
				_, err = db.AppendLogs([]clientdb.Log{row})
				require.NoError(t, err)
			case "payload":
				a.Revision++
				a.Digest = "xxh3:missing"
				want.Agents[a.Key] = a.Revision
				rec := controlTestRecord(t, a.Record())
				row, err := logRecordRow(&rec)
				require.NoError(t, err)
				_, err = db.AppendLogs([]clientdb.Log{row})
				require.NoError(t, err)
			}
			var drain error
			if failure == "drain" {
				drain = errors.New("export exhausted")
			}
			err := srv.finalizeSessionArchive(t.Context(), sess, drain)
			m, merr := srv.archives.Manifest(archiveTestTrace)
			require.NoError(t, merr)
			if failure != "none" {
				require.Error(t, err)
				require.Equal(t, archive.StateIncomplete, m.State)
				_, err = srv.archives.Acquire(archiveTestTrace)
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, archive.StateClosed, m.State)
			lease, err := srv.archives.Acquire(archiveTestTrace)
			require.NoError(t, err)
			defer lease.Release()
			data, err := os.ReadFile(lease.BootstrapPath())
			require.NoError(t, err)
			header, _, err := archive.VerifyBootstrap(bytes.NewReader(data))
			require.NoError(t, err)
			require.Len(t, header.Completion.Agents, 1)
		})
	}
}

func TestArchiveReopensWithPersistedProjection(t *testing.T) {
	srv, sess, db, want := archiveFixture(t)
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), sess, nil))
	lease, err := srv.archives.Acquire(archiveTestTrace)
	require.NoError(t, err)
	root := filepath.Dir(lease.BootstrapPath())
	lease.Release()
	require.NoError(t, db.Close())
	require.NoError(t, srv.clientDBs.Close())
	// Reconstruct both registries: no session object or in-memory index remains.
	dbs := clientdb.NewDBs(srv.clientDBs.Root)
	manager, err := archive.NewManager(archive.Config{Root: root, RemoveStore: dbs.Remove})
	require.NoError(t, err)
	lease, err = manager.Acquire(archiveTestTrace)
	require.NoError(t, err)
	defer lease.Release()
	manifest := lease.Manifest()
	reopened, err := dbs.Open(t.Context(), manifest.MainClientID)
	require.NoError(t, err)
	defer reopened.Close()
	rows, err := reopened.ControlRows(t.Context(), archiveTestTrace, manifest.HighWater.Logs, want)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	data, _, err := buildArchiveBootstrap(t.Context(), reopened, manifest, clientdb.HighWater{Spans: manifest.HighWater.Spans, Logs: manifest.HighWater.Logs, Metrics: manifest.HighWater.Metrics}, want)
	require.NoError(t, err)
	require.NotEmpty(t, data)
}

func TestControlPersistenceRetriesOnlyFailedTargets(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "parent.logs.log"), 0700))
	srv := &Server{clientDBs: clientdb.NewDBs(root)}
	sess := &daggerSession{sessionID: "session", clientRecords: map[string]*clientRecord{}}
	sess.telemetryPubSub = NewPubSub(srv)
	sess.clientRecords["parent"] = &clientRecord{daggerSession: sess, clientID: "parent"}
	sess.clientRecords["child"] = &clientRecord{daggerSession: sess, clientID: "child", parentClientIDs: []string{"parent"}}
	exp := sessionLogExporter{sess: sess, ps: sess.telemetryPubSub}
	a := archiveAgent()
	rec := controlTestRecord(t, a.Record())
	rec.AddAttributes(logapi.String(telemetryattrs.TelemetryOriginClientIDAttr, "child"))
	require.Error(t, exp.Export(t.Context(), []sdklog.Record{rec}))
	require.NoError(t, os.Remove(filepath.Join(root, "parent.logs.log")))
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{rec}))
	for _, target := range []string{"parent", "child"} {
		db, err := srv.clientDBs.Open(t.Context(), target)
		require.NoError(t, err)
		rows, err := db.SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 10})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.NoError(t, db.Close())
	}
	foreign := rec.Clone()
	foreign.SetAttributes(logapi.String(agentcontrol.VersionAttr, "unsupported"))
	require.Error(t, originLogExporter{origin: "child", next: exp}.Export(t.Context(), []sdklog.Record{foreign}))
}

func TestArchiveSharedTraceKeepsSourceClosuresSeparate(t *testing.T) {
	srv, first, _, _ := archiveFixture(t)
	second := &daggerSession{sessionID: "second", mainClientCallerID: "second-main", clientRecords: map[string]*clientRecord{}}
	second.telemetryPubSub = NewPubSub(srv)
	second.clientRecords["second-main"] = &clientRecord{daggerSession: second, clientID: "second-main"}
	id := call.New().Append(&ast.Type{NamedType: "LLM"}, "secondRecipe")
	a := archiveAgent()
	a.Session, a.Handle, a.Digest = second.sessionID, "second-worker", id.Digest().String()
	control := controlTestRecord(t, a.Record())
	payloadBytes, err := proto.Marshal(id.Call())
	require.NoError(t, err)
	payload := scopedLogRecord(t, "test", logapi.BytesValue(payloadBytes), logapi.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	for _, rec := range []sdklog.Record{control, payload} {
		rec.AddAttributes(logapi.String(telemetryattrs.TelemetryOriginClientIDAttr, "second-main"))
		require.NoError(t, sessionLogExporter{sess: second, ps: second.telemetryPubSub}.Export(t.Context(), []sdklog.Record{rec}))
	}
	second.archiveExpected = agentcontrol.Expectation{Agents: map[agentcontrol.Key]int64{a.Key: a.Revision}}
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), first, nil))
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), second, nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := srv.serveArchiveHTTP(w, r, first.clientRecords["main"]); err != nil {
			t.Errorf("archive HTTP: %v", err)
		}
	}))
	defer server.Close()
	client, err := archive.NewClientWithURL(server.Client(), server.URL)
	require.NoError(t, err)
	_, err = client.Bootstrap(t.Context(), archiveTestTrace, "", nil)
	require.ErrorIs(t, err, archive.ErrState)
	require.False(t, archive.IsCleanMiss(err))
	for _, sess := range []*daggerSession{first, second} {
		generation := sess.archiveManifest.Generation
		release, err := client.AcquireGeneration(t.Context(), archiveTestTrace, generation)
		require.NoError(t, err)
		result, err := client.Bootstrap(t.Context(), archiveTestTrace, generation, nil)
		release()
		require.NoError(t, err)
		require.Equal(t, sess.sessionID, result.Header.SourceSession)
		require.Len(t, result.Header.Completion.Agents, 1)
		require.Equal(t, sess.sessionID, result.Header.Completion.Agents[0].Key.Session)
	}
	_, err = client.WithSourceSession(second.sessionID).Bootstrap(t.Context(), archiveTestTrace, first.archiveManifest.Generation, nil)
	require.ErrorIs(t, err, archive.ErrCorrupt)
}

func TestArchiveLeaseProtectsRequestGapsAndCancels(t *testing.T) {
	for _, cancelOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelOnly), func(t *testing.T) {
			srv, sess, db, _ := archiveFixture(t)
			require.NoError(t, srv.finalizeSessionArchive(t.Context(), sess, nil))
			lease, err := srv.archives.Acquire(archiveTestTrace)
			require.NoError(t, err)
			root, manifest := filepath.Dir(lease.BootstrapPath()), lease.Manifest()
			lease.Release()
			require.NoError(t, db.Close())
			var now atomic.Int64
			now.Store(time.Now().UnixNano())
			srv.archives, err = archive.NewManager(archive.Config{Root: root, Now: func() time.Time { return time.Unix(0, now.Load()) }, RemoveStore: srv.clientDBs.Remove})
			require.NoError(t, err)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := srv.serveArchiveHTTP(w, r, sess.clientRecords["main"]); err != nil {
					t.Errorf("archive HTTP: %v", err)
				}
			}))
			defer server.Close()
			client, err := archive.NewClientWithURL(server.Client(), server.URL)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			release, err := client.Acquire(ctx, archiveTestTrace)
			require.NoError(t, err)
			defer release()
			now.Store(manifest.ExpiresAt.Add(time.Hour).UnixNano())
			_, err = srv.archives.GC()
			require.NoError(t, err)
			// The long-lived lease survives the idle gap before the next request.
			_, err = client.Bootstrap(t.Context(), archiveTestTrace, manifest.Generation, nil)
			require.NoError(t, err)
			if cancelOnly {
				cancel()
			} else {
				release()
			}
			require.Eventually(t, func() bool {
				_, err := srv.archives.GC()
				if err != nil {
					return false
				}
				_, err = srv.archives.Manifest(archiveTestTrace)
				var failure *archive.Failure
				return errors.As(err, &failure) && failure.Kind == archive.FailureEvicted
			}, time.Second, time.Millisecond)
		})
	}
}

func TestArchiveHTTPBootstrapBeforeHistoryAndAuthentication(t *testing.T) {
	srv, sess, _, _ := archiveFixture(t)
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), sess, nil))
	srv.daggerSessions = map[string]*daggerSession{"session": sess}
	sess.state.Store(sessionStateInitialized)
	_, err := srv.archiveRequestRecord("main", "session", "bad")
	require.Error(t, err)
	record, err := srv.archiveRequestRecord("main", "session", "secret")
	require.NoError(t, err)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { require.NoError(t, srv.serveArchiveHTTP(w, r, record)) }))
	defer httpServer.Close()
	c, err := archive.NewClientWithURL(httpServer.Client(), httpServer.URL)
	require.NoError(t, err)
	release, err := c.Acquire(t.Context(), archiveTestTrace)
	require.NoError(t, err)
	defer release()
	applied := 0
	result, err := c.Bootstrap(t.Context(), archiveTestTrace, "", func(_ archive.BootstrapHeader, b archive.BootstrapBatch) error {
		require.NotNil(t, b.Logs)
		applied++
		return nil
	})
	require.NoError(t, err)
	require.Positive(t, applied)
	// No history request was necessary for verified control and recipes.
	opts := archive.StreamOptions{Generation: result.Header.Generation, HighWater: result.Header.HighWater.Logs, ExcludeLogRowIDs: result.Terminal.Exclusions.LogRowIDs}
	cursor, err := c.Logs(t.Context(), archiveTestTrace, opts, nil)
	require.NoError(t, err)
	require.Equal(t, opts.HighWater, cursor)
	release()
}
