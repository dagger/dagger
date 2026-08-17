package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestSessionTelemetryStopsAtCompletionBoundWhileLaterRowsAppend(t *testing.T) {
	t.Parallel()

	dbs := clientdb.NewDBs(t.TempDir())
	keepAlive, err := dbs.Open(t.Context(), "creator")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, keepAlive.Close()) })

	srv := &Server{clientDBs: dbs}
	pubsub := NewPubSub(srv)
	srv.telemetryPubSub = pubsub
	sess := &daggerSession{telemetryPubSub: pubsub}
	creator := &daggerClient{
		clientID: "creator", daggerSession: sess, keepAliveTelemetryDB: keepAlive,
	}

	_, err = keepAlive.AppendMetrics([]clientdb.Metric{{Data: []byte(`{"resource":{}}`)}})
	require.NoError(t, err)
	completionBound := keepAlive.LastIDs().Metrics
	require.EqualValues(t, 1, completionBound)
	_, err = keepAlive.AppendMetrics([]clientdb.Metric{{Data: []byte(`{"resource":{}}`)}})
	require.NoError(t, err, "post-completion row setup")

	queryDone := make(chan struct{})
	close(queryDone)
	finalRow := make(chan struct{})
	releaseFinalRow := make(chan struct{})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/telemetry/metrics", nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- pubsub.metricsSubscribeHandler(recorder, req, creator, sessionSSEOptions{
			terminating: queryDone,
			maxID: func() (int64, bool) {
				return completionBound, true
			},
			finalRow: func() {
				close(finalRow)
				<-releaseFinalRow
			},
		})
	}()

	<-finalRow
	// Model services or nested clients continuously producing telemetry after
	// query completion while the bounded subscriber is draining its final row.
	_, err = keepAlive.AppendMetrics([]clientdb.Metric{
		{Data: []byte(`{"resource":{}}`)},
		{Data: []byte(`{"resource":{}}`)},
		{Data: []byte(`{"resource":{}}`)},
	})
	require.NoError(t, err)
	close(releaseFinalRow)
	require.NoError(t, <-errCh)

	body := recorder.Body.String()
	require.Contains(t, body, "event: subscribed")
	require.Contains(t, body, "event: metrics")
	require.Equal(t, 1, strings.Count(body, "event: metrics"))
	require.Contains(t, body, "id: 1\n")
	require.NotContains(t, body, "id: 2\n")
	require.EqualValues(t, 5, keepAlive.LastIDs().Metrics)
}

func TestSessionTelemetryBoundCanBecomeVisibleAfterSubscription(t *testing.T) {
	t.Parallel()

	dbs := clientdb.NewDBs(t.TempDir())
	keepAlive, err := dbs.Open(t.Context(), "creator")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, keepAlive.Close()) })

	srv := &Server{clientDBs: dbs}
	pubsub := NewPubSub(srv)
	sess := &daggerSession{telemetryPubSub: pubsub}
	creator := &daggerClient{
		clientID: "creator", daggerSession: sess, keepAliveTelemetryDB: keepAlive,
	}

	boundReady := make(chan struct{})
	queryDone := make(chan struct{})
	bound := int64(0)
	recorder := httptest.NewRecorder()
	req, cancel := context.WithCancel(httptest.NewRequest(http.MethodGet, "/telemetry/metrics", nil).Context())
	defer cancel()
	httpReq := httptest.NewRequest(http.MethodGet, "/telemetry/metrics", nil).WithContext(req)
	errCh := make(chan error, 1)
	go func() {
		errCh <- pubsub.metricsSubscribeHandler(recorder, httpReq, creator, sessionSSEOptions{
			terminating: queryDone,
			maxID: func() (int64, bool) {
				select {
				case <-boundReady:
					return bound, true
				default:
					return 0, false
				}
			},
		})
	}()

	_, err = keepAlive.AppendMetrics([]clientdb.Metric{{Data: []byte(`{"resource":{}}`)}})
	require.NoError(t, err)
	bound = keepAlive.LastIDs().Metrics
	close(boundReady)
	close(queryDone)
	require.NoError(t, <-errCh)
	require.Contains(t, recorder.Body.String(), "id: 1\n")
}

func TestFullAttachmentTelemetryStaysOutOfCreatorHistory(t *testing.T) {
	t.Parallel()

	dbs := clientdb.NewDBs(t.TempDir())
	creatorDB, err := dbs.Open(t.Context(), "creator")
	require.NoError(t, err)
	attachmentDB, err := dbs.Open(t.Context(), "attachment")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, creatorDB.Close())
		require.NoError(t, attachmentDB.Close())
	})

	srv := &Server{clientDBs: dbs}
	pubsub := NewPubSub(srv)
	srv.telemetryPubSub = pubsub
	sess := &daggerSession{sessionID: "session", telemetryPubSub: pubsub}
	creator := &daggerClient{clientID: "creator", daggerSession: sess, keepAliveTelemetryDB: creatorDB}
	attachment := &daggerClient{clientID: "attachment", daggerSession: sess, keepAliveTelemetryDB: attachmentDB}

	metric := func(name string) *metricdata.ResourceMetrics {
		return &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: instrumentation.Scope{Name: "detachable-up-test"},
			Metrics: []metricdata.Metrics{{
				Name: name,
				Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Value: 1}}},
			}},
		}}}
	}
	require.NoError(t, pubsub.Metrics(creator).Export(t.Context(), metric("creator-history")))
	completionBound := creatorDB.LastIDs().Metrics
	require.EqualValues(t, 1, completionBound)
	require.NoError(t, pubsub.Metrics(attachment).Export(t.Context(), metric("attached-work")))
	require.EqualValues(t, completionBound, creatorDB.LastIDs().Metrics)
	require.EqualValues(t, 1, attachmentDB.LastIDs().Metrics)

	queryDone := make(chan struct{})
	close(queryDone)
	creatorRecorder := httptest.NewRecorder()
	require.NoError(t, pubsub.metricsSubscribeHandler(
		creatorRecorder,
		httptest.NewRequest(http.MethodGet, "/telemetry/metrics", nil),
		creator,
		sessionSSEOptions{
			terminating: queryDone,
			maxID:       func() (int64, bool) { return completionBound, true },
		},
	))
	require.Contains(t, creatorRecorder.Body.String(), "creator-history")
	require.NotContains(t, creatorRecorder.Body.String(), "attached-work")

	attachmentDone := make(chan struct{})
	close(attachmentDone)
	attachmentRecorder := httptest.NewRecorder()
	require.NoError(t, pubsub.metricsSubscribeHandler(
		attachmentRecorder,
		httptest.NewRequest(http.MethodGet, "/v1/metrics", nil),
		attachment,
		sessionSSEOptions{terminating: attachmentDone},
	))
	require.Contains(t, attachmentRecorder.Body.String(), "attached-work")
}
