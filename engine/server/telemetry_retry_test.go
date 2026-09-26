package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// callPayloadMissingTargets reports the route targets whose DB does not hold
// the digest yet, in route order. Test-only: production code never needs to
// observe delivery state outside the claim/take/settle transitions.
func (sess *daggerSession) callPayloadMissingTargets(digest string, targets []string) []string {
	sess.callPayloadMu.Lock()
	defer sess.callPayloadMu.Unlock()

	states := sess.callPayloadStates(digest, false)
	missing := make([]string, 0, len(targets))
	for _, target := range targets {
		if states[target] != callPayloadDelivered {
			missing = append(missing, target)
		}
	}
	return missing
}

func TestArchiveRegistrationFailureDoesNotDropControl(t *testing.T) {
	srv, _, _, _ := archiveFixture(t) //nolint:dogsled // This test creates its own session and control records.
	other := &daggerSession{sessionID: "other-session", mainClientCallerID: "other", clientRecords: map[string]*clientRecord{}}
	other.telemetryPubSub = NewPubSub(srv)
	other.archiveRegisterErr = fmt.Errorf("injected archive registration failure")
	other.clientRecords["other"] = &clientRecord{daggerSession: other, clientID: "other"}
	a := archiveAgent()
	a.Session = other.sessionID
	rec := controlTestRecord(t, a.Record())
	rec.AddAttributes(otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "other"))
	exp := sessionLogExporter{sess: other, ps: other.telemetryPubSub}
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{rec}))
	db, err := srv.clientDBs.Open(t.Context(), "other")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1, "archive registration must not gate live persistence")
}

func TestReportedSpanDoesNotSuppressDurablePayload(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	body, digest := serverCallPayload(t, "lookup", "span-reported")
	store := &callPayloadDeliveryStore{session: sess, targets: []string{"client"}}
	require.True(t, store.ClaimCallPayload(digest))
	store.CallPayloadDelivered(digest)
	require.False(t, store.ClaimCallPayload(digest), "avoid duplicate ordinary span walks")
	require.Equal(t, []string{"client"}, sess.callPayloadMissingTargets(digest, store.targets))
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "client"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.Empty(t, sess.callPayloadMissingTargets(digest, store.targets))
	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

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

// A control record that can never persist (malformed, outside its emission
// session/trace, or unroutable) is skipped on its own: failing the batch
// would have the control processor retry and then drop the valid revisions
// batched with it.
func TestSessionLogExporterSkipsInvalidControlRecords(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{sessionID: "session", clientRecords: map[string]*clientRecord{}}
	sess.telemetryPubSub = NewPubSub(srv)
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	exporter := sessionLogExporter{sess: sess, ps: sess.telemetryPubSub}
	withOrigin := func(rec sdklog.Record, origin string) sdklog.Record {
		rec.AddAttributes(otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, origin))
		return rec
	}

	valid := withOrigin(controlTestRecord(t, archiveAgent().Record()), "client")
	var bad otellog.Record
	bad.SetBody(otellog.StringValue(""))
	bad.AddAttributes(otellog.Int(agentcontrol.VersionAttr, agentcontrol.Version+1))
	malformed := withOrigin(controlTestRecord(t, bad), "client")
	stranger := archiveAgent()
	stranger.Session = "another-session"
	stranger.Handle = "stranger"
	foreign := withOrigin(controlTestRecord(t, stranger.Record()), "client")
	originless := controlTestRecord(t, archiveAgent().Record())
	unroutable := withOrigin(controlTestRecord(t, archiveAgent().Record()), "nobody")

	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{malformed, foreign, valid, originless, unroutable}),
		"invalid control records are skipped rather than failing the batch")

	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 1, "only the valid control record persists")
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
