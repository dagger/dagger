package telemetry

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"go.dagger.io/dagger/telemetry/event"
)

const queueSize = 2048

type Config struct {
	Enable bool
}

type Telemetry struct {
	cfg Config

	runID   string
	queueCh chan []byte
	doneCh  chan struct{}
}

func New(cfg Config) *Telemetry {
	t := &Telemetry{
		cfg:     cfg,
		runID:   uuid.NewString(),
		queueCh: make(chan []byte, queueSize),
		doneCh:  make(chan struct{}),
	}
	go t.send()
	return t
}

func (t *Telemetry) Push(ctx context.Context, props event.Properties) {
	e := event.New(props)
	if err := e.Validate(); err != nil {
		panic(err)
	}
	e.Run.ID = t.runID

	encoded, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}

	if t.cfg.Enable {
		t.queueCh <- encoded
	}
}

func (t *Telemetry) send() {
	defer close(t.doneCh)

	for e := range t.queueCh {
		// FIXME: send the event
		fmt.Println(string(e))
	}
}

func (t *Telemetry) Flush() {
	// Stop accepting new events
	t.cfg.Enable = false
	// Flush events in queue
	close(t.queueCh)
	// Wait for completion
	<-t.doneCh
}
