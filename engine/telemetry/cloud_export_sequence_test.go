package telemetry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestExportSequencerIsolatesAndReusesConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.RemoteAddr)
	}))
	t.Cleanup(server.Close)

	request := func(client *http.Client, method string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return string(body)
	}

	// Drain each response so the connection is available for reuse. With the
	// default transport, the upload would reuse the reader's connection.
	readerConn := request(http.DefaultClient, http.MethodGet)
	sequencer := newExportSequencer()
	uploadConn := request(sequencer.httpClient(), http.MethodPost)
	require.NotEqual(t, readerConn, uploadConn)

	// Refreshing credentials constructs a new client for the same sequencer.
	require.Equal(t, uploadConn, request(sequencer.httpClient(), http.MethodPost))
	// Other export signals share the private upload pool, too.
	require.Equal(t, uploadConn, request(newExportSequencer().httpClient(), http.MethodPost))
	require.Equal(t, readerConn, request(http.DefaultClient, http.MethodGet))
}

func TestExportSequencerAddsRetryStableHeader(t *testing.T) {
	headers := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get(cloudExportHeader)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sequencer := newExportSequencer()
	client := sequencer.httpClient()

	firstExport := sequencer.nextContext(context.Background())
	request := func(ctx context.Context) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	request(firstExport)
	request(firstExport) // an OTLP retry reuses the export call's context
	request(sequencer.nextContext(context.Background()))

	first := <-headers
	require.Equal(t, first, <-headers)
	require.Equal(t, sequencer.writerID+"/1", first)
	require.Equal(t, sequencer.writerID+"/2", <-headers)
	_, err := uuid.Parse(sequencer.writerID)
	require.NoError(t, err)
}

func TestExportSequencersAreIndependent(t *testing.T) {
	traces := newExportSequencer()
	logs := newExportSequencer()

	require.NotEqual(t, traces.writerID, logs.writerID)
	traceMetadata := traces.nextContext(context.Background()).Value(exportSequenceContextKey{}).(exportSequenceMetadata)
	logMetadata := logs.nextContext(context.Background()).Value(exportSequenceContextKey{}).(exportSequenceMetadata)
	require.Equal(t, uint64(1), traceMetadata.sequence)
	require.Equal(t, uint64(1), logMetadata.sequence)
}
