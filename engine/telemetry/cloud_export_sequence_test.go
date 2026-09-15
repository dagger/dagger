package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

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
