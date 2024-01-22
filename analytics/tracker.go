package analytics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
)

const (
	flushInterval = 1 * time.Second
	trackURL      = "https://api.dagger.cloud/analytics"
)

type Event struct {
	Timestamp  time.Time              `json:"ts,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`

	DeviceID string `json:"device_id,omitempty"`
	OrgID    string `json:"org_id,omitempty"`
	IP       string `json:"ip,omitempty"`
	ServerID string `json:"server_id,omitempty"`

	ClientVersion string `json:"client_version,omitempty"`
	ClientOS      string `json:"client_os,omitempty"`
	ClientArch    string `json:"client_arch,omitempty"`

	CI       bool   `json:"ci"`
	CIVendor string `json:"ci_vendor,omitempty"`

	GitRemoteEncoded string `json:"git_remote_encoded,omitempty"`
	GitAuthorHashed  string `json:"git_author_hashed,omitempty"`
}

type Tracker interface {
	Capture(ctx context.Context, event string, properties map[string]any)
	io.Closer
}

type analyticsKey struct{}

func WithContext(ctx context.Context, t Tracker) context.Context {
	return context.WithValue(ctx, analyticsKey{}, t)
}

func Ctx(ctx context.Context) Tracker {
	if t := ctx.Value(analyticsKey{}); t != nil {
		return t.(Tracker)
	}
	return &noopTracker{}
}

type noopTracker struct {
}

func (t *noopTracker) Capture(ctx context.Context, event string, properties map[string]any) {
}

func (t *noopTracker) Close() error {
	return nil
}

type CloudTracker struct {
	labels     map[string]string
	cloudToken string

	closed bool
	mu     sync.Mutex
	queue  []*Event
	stopCh chan struct{}
	doneCh chan struct{}
}

func DoNotTrack() bool {
	// from https://consoledonottrack.com/
	return os.Getenv("DO_NOT_TRACK") == "1"
}

func New(doNotTrack bool, labels ...pipeline.Label) Tracker {
	if doNotTrack {
		return &noopTracker{}
	}

	t := &CloudTracker{
		cloudToken: os.Getenv("DAGGER_CLOUD_TOKEN"),
		labels:     make(map[string]string),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	t.loadLabels(labels)
	go t.start()

	return t
}

func (t *CloudTracker) loadLabels(labels pipeline.Labels) {
	// Load default labels if none provided
	if len(labels) == 0 {
		workdir, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get cwd: %v\n", err)
			return
		}

		labels.AppendCILabel()
		labels = append(labels, pipeline.LoadVCSLabels(workdir)...)
		labels = append(labels, pipeline.LoadClientLabels(engine.Version)...)
	}
	for _, l := range labels {
		t.labels[l.Name] = l.Value
	}
}

func (t *CloudTracker) Capture(ctx context.Context, event string, properties map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}

	ev := &Event{
		Timestamp:  time.Now().UTC(),
		Type:       event,
		Properties: properties,

		DeviceID: t.labels["dagger.io/client.machine_id"],

		ClientVersion: t.labels["dagger.io/client.version"],
		ClientOS:      t.labels["dagger.io/client.os"],
		ClientArch:    t.labels["dagger.io/client.arch"],

		CI:       t.labels["dagger.io/ci"] == "true",
		CIVendor: t.labels["dagger.io/ci.vendor"],
	}
	if remote := t.labels["dagger.io/git.remote"]; remote != "" {
		ev.GitRemoteEncoded = fmt.Sprintf("%x", base64.StdEncoding.EncodeToString([]byte(remote)))
	}
	if author := t.labels["dagger.io/git.author.email"]; author != "" {
		ev.GitAuthorHashed = fmt.Sprintf("%x", sha256.Sum256([]byte(author)))
	}
	if clientMetadata, err := engine.ClientMetadataFromContext(ctx); err == nil {
		ev.ServerID = clientMetadata.ServerID
	}

	t.queue = append(t.queue, ev)
}

func (t *CloudTracker) start() {
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

func (t *CloudTracker) send() {
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
			fmt.Fprintln(os.Stderr, "analytics: encode:", err)
			continue
		}
	}

	// FIXME: remove this
	fmt.Fprintf(os.Stderr, "ANALYTICS: SENDING %s\n", payload.String())

	req, err := http.NewRequest(http.MethodPost, trackURL, bytes.NewReader(payload.Bytes()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "analytics: new request:", err)
		return
	}
	if t.cloudToken != "" {
		req.SetBasicAuth(t.cloudToken, "")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analytics: do request:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		fmt.Fprintln(os.Stderr, "analytics: unexpected response:", resp.Status)
	}
}

func (t *CloudTracker) Close() error {
	// Stop accepting new events
	t.mu.Lock()
	if t.closed {
		// prevent errors when trying to close multiple times on the same
		// telemetry instance
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	// Flush events in queue
	close(t.stopCh)

	// Wait for completion
	<-t.doneCh

	return nil
}
