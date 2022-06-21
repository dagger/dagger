package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.dagger.io/dagger/api"
	"go.dagger.io/dagger/api/auth"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/telemetry/event"
)

const (
	queueSize = 2048
	workers   = 5
)

type Telemetry struct {
	enable bool

	engineID string
	runID    string

	log zerolog.Logger

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

		log: zerolog.Nop(),

		client:  api.New(),
		url:     eventsURL(),
		queueCh: make(chan []byte, queueSize),
		doneCh:  make(chan struct{}),
	}
	go t.start()
	return t
}

func (t *Telemetry) EnableLogToFile() {
	lw, err := os.OpenFile(t.logFile(), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err == nil {
		t.log = zerolog.New(lw).
			With().
			Timestamp().
			Caller().
			Logger()
		t.log.Trace().Msgf("starting telemetry with queueSize [%d] and workers [%d]", queueSize, workers)
	}
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

func (t *Telemetry) start() {
	defer close(t.doneCh)
	wg := &sync.WaitGroup{}
	for w := 1; w <= workers; w++ {
		wg.Add(1)
		go t.send(wg)
	}

	wg.Wait()
}

func (t *Telemetry) send(wg *sync.WaitGroup) {
	defer wg.Done()
	for e := range t.queueCh {
		t.log.Info().Msg(string(e))
		reqBody := bytes.NewBuffer(e)
		req, err := http.NewRequest(http.MethodPost, t.url, reqBody)
		if err != nil {
			t.log.Error().Err(err)
			continue
		}
		if resp, err := t.client.Do(req.Context(), req); err == nil {
			t.log.Info().Msgf("%s %db via %s → %s %s", req.Method, req.ContentLength, req.Proto, resp.Status, resp.Proto)
			resp.Body.Close()
		} else {
			// We want to log all errors and continue.
			// If we don't, this will fail for unwanted reasons, e.g.:
			// - if there is no internet connection
			// - if the API is slow to respond and requests time out
			t.log.Error().Err(err)
			continue
		}
	}
}

func (t *Telemetry) logFile() string {
	lf := os.Getenv("DAGGER_CLOUD_LOG_FILE")
	if lf == "" {
		lf = fmt.Sprintf("telemetry.%s.log", t.runID)
	}
	return lf
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
