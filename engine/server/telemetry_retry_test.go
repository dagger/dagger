package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func TestSessionLogExporterRetriesPayloadAfterStoreFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.WriteFile(root, []byte("temporarily unavailable"), 0600))
	dbs := clientdb.NewDBs(root)
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	body, _ := serverCallPayload(t, "lookup", "retry")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "client"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	require.Error(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.NoError(t, os.Remove(root))
	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 1, "retry after storage recovery must persist the payload")
}

func TestSessionLogExporterRetriesOnlyFailedPayloadTargets(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "parent.logs.log")
	require.NoError(t, os.Mkdir(blocked, 0700))
	dbs := clientdb.NewDBs(root)
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["parent"] = &clientRecord{daggerSession: sess, clientID: "parent"}
	sess.clientRecords["child"] = &clientRecord{daggerSession: sess, clientID: "child", parentClientIDs: []string{"parent"}}
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	body, digest := serverCallPayload(t, "lookup", "retry")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "child"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	require.Error(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.Equal(t, []string{"parent"}, sess.callPayloadMissingTargets(digest, []string{"child", "parent"}))
	require.NoError(t, os.Remove(blocked))

	// The payload processor retries a failed batch, and a fresh closure walk
	// may re-emit the same digest in the meantime; several such exports can
	// overlap. The successful child must not get a duplicate, and the parent
	// must receive exactly one.
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := exporter.Export(t.Context(), []sdklog.Record{record, record}); err != nil {
				t.Errorf("retry export: %v", err)
			}
		})
	}
	wg.Wait()
	require.Empty(t, sess.callPayloadMissingTargets(digest, []string{"child", "parent"}))
	for _, target := range []string{"child", "parent"} {
		db, err := dbs.Open(t.Context(), target)
		require.NoError(t, err)
		rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
		require.NoError(t, err)
		require.NoError(t, db.Close())
		require.Len(t, rows, 1, "target %s", target)
	}
}

// A record that cannot be routed (no origin, or an origin the session does
// not know) must not poison its batch: the routable payloads still land, and
// nothing is left claimed on their behalf.
func TestSessionLogExporterSkipsUnroutableRecords(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	body, digest := serverCallPayload(t, "lookup", "retry")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "client"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	strayBody, strayDigest := serverCallPayload(t, "lookup", "stray")
	originless := scopedLogRecord(t, "test.core", otellog.BytesValue(strayBody),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	unknownOrigin := scopedLogRecord(t, "test.core", otellog.BytesValue(strayBody),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "nobody"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))

	store := &callPayloadDeliveryStore{session: sess, targets: []string{"client"}}
	require.True(t, store.ClaimCallPayload(digest))
	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{originless, record, unknownOrigin, {}}),
		"unroutable records are skipped rather than failing the batch")
	require.Empty(t, sess.callPayloadMissingTargets(digest, []string{"client"}),
		"the routable payload in the same batch must be delivered")
	require.False(t, store.ClaimCallPayload(digest))
	require.Equal(t, []string{"client"}, sess.callPayloadMissingTargets(strayDigest, []string{"client"}))
	require.True(t, store.ClaimCallPayload(strayDigest),
		"a skipped record must not leave its digest claimed or delivered")

	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// A payload the producer claimed but whose write failed must be claimable
// again, so that once the payload processor gives up on the record a later
// closure walk can repair the gap.
func TestSessionLogExporterReleasesFailedPayloadClaims(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.WriteFile(root, []byte("temporarily unavailable"), 0600))
	dbs := clientdb.NewDBs(root)
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	body, digest := serverCallPayload(t, "lookup", "retry")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "client"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))

	store := &callPayloadDeliveryStore{session: sess, targets: []string{"client"}}
	require.True(t, store.ClaimCallPayload(digest))
	require.Error(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.True(t, store.ClaimCallPayload(digest),
		"a failed write must release the producer's claim")
	require.NoError(t, os.Remove(root))
	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.False(t, store.ClaimCallPayload(digest))
}
