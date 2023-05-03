package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	flushInterval = 100 * time.Millisecond
	queueSize     = 2048

	pushURL = "https://api.dagger.cloud/events"
)

type Telemetry struct {
	enable bool

	runID string

	url   string
	token string

	mu     sync.Mutex
	queue  []*Event
	stopCh chan struct{}
	doneCh chan struct{}
}

func New(printURLMessage bool) *Telemetry {
	t := &Telemetry{
		runID:  uuid.NewString(),
		url:    pushURL,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	if url := os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_URL"); url != "" {
		t.url = url
	}
	if token := os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_TOKEN"); token != "" {
		t.token = token
		t.enable = true
		if printURLMessage {
			fmt.Fprintf(os.Stderr, "Dagger Cloud URL: https://dagger.cloud/runs/%s\n\n", t.runID)
		}
	}
	go t.start()
	return t
}

func (t *Telemetry) Disable() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.enable = false
}

func (t *Telemetry) Enable() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.enable = true
}

func (t *Telemetry) RunID() string {
	return t.runID
}

func (t *Telemetry) SetRunID(id string) {
	t.runID = id
}

func (t *Telemetry) Push(p Payload, ts time.Time) {
	if !t.enable {
		return
	}

	ev := &Event{
		Version:   eventVersion,
		Timestamp: ts,
		Type:      p.Type(),
		Payload:   p,
	}

	if p.Scope() == EventScopeRun {
		ev.RunID = t.runID
	}

	t.mu.Lock()
	t.queue = append(t.queue, ev)
	t.mu.Unlock()
}

func (t *Telemetry) start() {
	defer close(t.doneCh)

	for {
		select {
		case <-time.After(flushInterval):
			t.send()
		case <-t.stopCh:
			// On stop, send the current queue and exit
			t.send()
			return
		}
	}
}

func (t *Telemetry) send() {
	t.mu.Lock()
	queue := append([]*Event{}, t.queue...)
	t.queue = []*Event{}
	t.mu.Unlock()

	if len(queue) == 0 {
		return
	}

	payload := bytes.NewBuffer([]byte{})
	enc := json.NewEncoder(payload)
	for _, ev := range queue {
		err := enc.Encode(ev)
		if err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
			continue
		}
	}

	req, err := http.NewRequest(http.MethodPost, t.url, bytes.NewReader(payload.Bytes()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		return
	}
	if t.token != "" {
		req.SetBasicAuth(t.token, "")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		return
	}
	defer resp.Body.Close()
}

func (t *Telemetry) Flush() {
	// Stop accepting new events
	t.mu.Lock()
	if !t.enable {
		// prevent errors when trying to flush multiple times on the same
		// telemetry instance
		t.mu.Unlock()
		return
	}
	t.enable = false
	t.mu.Unlock()

	// Flush events in queue
	close(t.stopCh)

	// Wait for completion
	<-t.doneCh
}
