package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/google/uuid"
)

const (
	flushInterval = 100 * time.Millisecond
	queueSize     = 2048

	pushURL = "https://api.us-east.tinybird.co/v0/events?name=events"
)

const eventVersion = "2023-02-21.01"

type Event struct {
	Version   string    `json:"v"`
	Timestamp time.Time `json:"ts"`

	RunID    string        `json:"run_id"`
	OpID     string        `json:"op_id"`
	OpName   string        `json:"op_name"`
	Pipeline pipeline.Path `json:"pipeline"`
	Internal bool          `json:"internal"`
	Inputs   []string      `json:"inputs"`

	Started   *time.Time `json:"started"`
	Completed *time.Time `json:"completed"`
	Cached    bool       `json:"cached"`
}

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

func NewTelemetry() *Telemetry {
	t := &Telemetry{
		runID:  uuid.NewString(),
		url:    pushURL,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	if url := os.Getenv("DAGGER_CLOUD_URL"); url != "" {
		t.url = url
	}
	if token := os.Getenv("DAGGER_CLOUD_TOKEN"); token != "" {
		t.token = token
		t.enable = true
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

func (t *Telemetry) Push(ev *Event) {
	if !t.enable {
		return
	}

	ev.RunID = t.runID

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

	fmt.Fprintf(os.Stdout, "===\n%s===\n", payload.String())

	req, err := http.NewRequest(http.MethodPost, t.url, bytes.NewReader(payload.Bytes()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		return
	}
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		return
	}
	defer resp.Body.Close()
	fmt.Fprintf(os.Stderr, "%s %db via %s → %s %s\n", req.Method, req.ContentLength, req.Proto, resp.Status, resp.Proto)
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
