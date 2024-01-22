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
	pushURL       = "https://api.dagger.cloud/events"
)

type Telemetry struct {
	enabled bool
	closed  bool

	runID string

	pushURL string
	token   string

	mu     sync.Mutex
	queue  []*Event
	stopCh chan struct{}
	doneCh chan struct{}
}

func New() *Telemetry {
	cloudToken := os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_TOKEN")
	// add DAGGER_CLOUD_TOKEN in backwards compat way.
	// TODO: deprecate in a future release
	if v, ok := os.LookupEnv("DAGGER_CLOUD_TOKEN"); ok {
		cloudToken = v
	}

	t := &Telemetry{
		runID:   uuid.NewString(),
		pushURL: os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_URL"),
		token:   cloudToken,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}

	if t.pushURL == "" {
		t.pushURL = pushURL
	}

	if t.token != "" {
		// only send telemetry if a token was configured
		t.enabled = true
		go t.start()
	}

	return t
}

func (t *Telemetry) Enabled() bool {
	return t.enabled
}

func (t *Telemetry) URL() string {
	return "https://dagger.cloud/runs/" + t.runID
}

func (t *Telemetry) Push(p Payload, ts time.Time) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
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

	t.queue = append(t.queue, ev)
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
			fmt.Fprintln(os.Stderr, "telemetry: encode:", err)
			continue
		}
	}

	req, err := http.NewRequest(http.MethodPost, t.pushURL, bytes.NewReader(payload.Bytes()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "telemetry: new request:", err)
		return
	}
	if t.token != "" {
		req.SetBasicAuth(t.token, "")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "telemetry: do request:", err)
		return
	}
	if resp.StatusCode != http.StatusCreated {
		fmt.Fprintln(os.Stderr, "telemetry: unexpected response:", resp.Status)
	}
	defer resp.Body.Close()
}

func (t *Telemetry) Close() {
	if !t.enabled {
		return
	}

	// Stop accepting new events
	t.mu.Lock()
	if t.closed {
		// prevent errors when trying to close multiple times on the same
		// telemetry instance
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()

	// Flush events in queue
	close(t.stopCh)

	// Wait for completion
	<-t.doneCh
}
