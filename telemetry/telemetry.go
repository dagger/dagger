package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/google/uuid"
	"go.dagger.io/dagger/api"
	"go.dagger.io/dagger/api/auth"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/telemetry/event"
)

const queueSize = 2048

type Telemetry struct {
	enable bool

	engineID string
	runID    string

	client  *api.Client
	url     string
	queueCh chan []byte
	doneCh  chan struct{}
}

func New() *Telemetry {
	engineID, _ := engine.ID()

	t := &Telemetry{
		enable: auth.HasCredentials(),

		runID:    uuid.NewString(),
		engineID: engineID,

		client:  api.New(),
		url:     eventsURL(),
		queueCh: make(chan []byte, queueSize),
		doneCh:  make(chan struct{}),
	}
	go t.send()
	return t
}

func (t *Telemetry) Disable() {
	t.enable = false
}

func (t *Telemetry) Enable() {
	t.enable = true
}

func (t *Telemetry) EngineID() string {
	return t.engineID
}

func (t *Telemetry) RunID() string {
	return t.runID
}

func (t *Telemetry) Push(ctx context.Context, props event.Properties) {
	e := event.New(props)
	e.Engine.ID = t.engineID
	e.Run.ID = t.runID

	if err := e.Validate(); err != nil {
		panic(err)
	}

	encoded, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}

	t.Write(encoded)
}

func (t *Telemetry) Write(p []byte) {
	if t.enable {
		t.queueCh <- p
	}
}

func (t *Telemetry) send() {
	defer close(t.doneCh)

	for e := range t.queueCh {
		reqBody := bytes.NewBuffer(e)
		req, err := http.NewRequest(http.MethodPost, t.url, reqBody)
		fmt.Printf("🐝 REQUEST: %#v\n", req)
		if err != nil {
			continue
		}
		if resp, err := t.client.Do(req.Context(), req); err == nil {
			fmt.Printf("🐶 RESPONSE: %#v\n", resp)
			resp.Body.Close()
		} else {
			// TODO: re-auth does not seem to work as expected
			panic(err)
		}
	}
}

func (t *Telemetry) Flush() {
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
		url = "https://api.dagger.cloud/events"
	}
	return url
}
