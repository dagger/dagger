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
	"github.com/vito/progrock"
)

const (
	flushInterval = 1 * time.Second
	trackURL      = "https://api.dagger.cloud/analytics"
)

type Event struct {
	Timestamp  time.Time         `json:"ts,omitempty"`
	Type       string            `json:"type,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`

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
	Capture(ctx context.Context, event string, properties map[string]string)
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

func (t *noopTracker) Capture(ctx context.Context, event string, properties map[string]string) {
}

func (t *noopTracker) Close() error {
	return nil
}

func DoNotTrack() bool {
	// from https://consoledonottrack.com/
	return os.Getenv("DO_NOT_TRACK") == "1"
}

type Config struct {
	DoNotTrack bool
	Labels     pipeline.Labels
	CloudToken string
}

func DefaultConfig() Config {
	cfg := Config{
		DoNotTrack: DoNotTrack(),
		CloudToken: os.Getenv("DAGGER_CLOUD_TOKEN"),
	}
	// Backward compatibility with the old environment variable.
	if cfg.CloudToken == "" {
		cfg.CloudToken = os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_TOKEN")
	}

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get cwd: %v\n", err)
		return cfg
	}

	cfg.Labels.AppendCILabel()
	cfg.Labels = append(cfg.Labels, pipeline.LoadVCSLabels(workdir)...)
	cfg.Labels = append(cfg.Labels, pipeline.LoadClientLabels(engine.Version)...)

	return cfg
}

type queuedEvent struct {
	ctx   context.Context
	event *Event
}

type CloudTracker struct {
	cfg    Config
	labels map[string]string

	closed bool
	mu     sync.Mutex
	queue  []*queuedEvent
	stopCh chan struct{}
	doneCh chan struct{}
}

func New(cfg Config) Tracker {
	if cfg.DoNotTrack {
		return &noopTracker{}
	}

	t := &CloudTracker{
		cfg:    cfg,
		labels: make(map[string]string),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	for _, l := range cfg.Labels {
		t.labels[l.Name] = l.Value
	}

	go t.start()

	return t
}

func (t *CloudTracker) Capture(ctx context.Context, event string, properties map[string]string) {
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

	t.queue = append(t.queue, &queuedEvent{ctx: ctx, event: ev})
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
	queue := append([]*queuedEvent{}, t.queue...)
	t.queue = []*queuedEvent{}
	t.mu.Unlock()

	if len(queue) == 0 {
		return
	}

	// grab the progrock recorder from the last event in the queue
	rec := progrock.FromContext(queue[len(queue)-1].ctx)

	payload := bytes.NewBuffer([]byte{})
	enc := json.NewEncoder(payload)
	for _, q := range queue {
		err := enc.Encode(q.event)
		if err != nil {
			rec.Debug("analytics: encode failed", progrock.ErrorLabel(err))
			continue
		}
	}

	req, err := http.NewRequest(http.MethodPost, trackURL, bytes.NewReader(payload.Bytes()))
	if err != nil {
		rec.Debug("analytics: new request failed", progrock.ErrorLabel(err))
		return
	}
	if t.cfg.CloudToken != "" {
		req.SetBasicAuth(t.cfg.CloudToken, "")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		rec.Debug("analytics: do request failed", progrock.ErrorLabel(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		rec.Debug("analytics: unexpected response", progrock.Labelf("status", resp.Status))
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
