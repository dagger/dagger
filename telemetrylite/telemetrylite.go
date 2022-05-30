package telemetrylite

import (
	"bytes"
	"context"
	"net/http"
	"os"

	"github.com/google/uuid"
	"go.dagger.io/dagger/api"
	"go.dagger.io/dagger/api/auth"
)

const queueSize = 2048

type TelemetryLite struct {
	enable bool
	client *api.Client
	url    string

	runID   string
	queueCh chan []byte
	doneCh  chan struct{}
}

func New() *TelemetryLite {
	t := &TelemetryLite{
		enable:  auth.HasCredentials(),
		client:  api.New(),
		url:     eventsURL(),
		runID:   uuid.NewString(),
		queueCh: make(chan []byte, queueSize),
		doneCh:  make(chan struct{}),
	}
	go t.send()
	return t
}

func (t *TelemetryLite) Disable() {
	t.enable = false
}

func (t *TelemetryLite) Enable() {
	t.enable = true
}

func (t *TelemetryLite) Started() {
}

func (t *TelemetryLite) Push(ctx context.Context, p []byte) {
	if t.enable {
		t.queueCh <- p
	}
}

func (t *TelemetryLite) send() {
	defer close(t.doneCh)

	for e := range t.queueCh {
		reqBody := bytes.NewBuffer(e)
		req, err := http.NewRequest(http.MethodPost, t.url, reqBody)
		if err == nil {
			if resp, err := t.client.Do(req.Context(), req); err == nil {
				defer resp.Body.Close()
			}
		}
	}
}

func (t *TelemetryLite) Flush() {
	// Stop accepting new events
	t.Disable()
	// Flush events in queue
	close(t.queueCh)
	// Wait for completion
	<-t.doneCh
}

func eventsURL() string {
	url := os.Getenv("DAGGER_CLOUD_EVENTS_URL")
	if url == "" {
		url = "http://localhost:8020/events"
	}
	return url
}
