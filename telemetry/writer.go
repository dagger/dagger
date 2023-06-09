package telemetry

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
	"github.com/vito/progrock"
)

const (
	flushInterval = 100 * time.Millisecond
	queueSize     = 2048

	pushURL = "https://api.dagger.cloud/events"
)

type writer struct {
	runID string

	url   string
	token string

	pipeliner *Pipeliner

	// emittedMemberships keeps track of whether we've emitted an OpPayload for a
	// vertex yet.
	emittedMemberships map[vertexMembership]bool

	mu     sync.Mutex
	queue  []*Event
	stopCh chan struct{}
	doneCh chan struct{}
	closed bool
}

type vertexMembership struct {
	vertexID string
	groupID  string
}

func NewWriter() (progrock.Writer, string, bool) {
	t := &writer{
		runID: uuid.NewString(),
		url:   os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_URL"),
		token: os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_TOKEN"),

		pipeliner:          NewPipeliner(),
		emittedMemberships: map[vertexMembership]bool{},

		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	if t.token == "" {
		// no token; don't send telemetry
		return nil, "", false
	}

	if t.url == "" {
		t.url = pushURL
	}

	go t.start()

	return t, "https://dagger.cloud/runs/" + t.runID, true
}

func (t *writer) WriteStatus(ev *progrock.StatusUpdate) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	if err := t.pipeliner.WriteStatus(ev); err != nil {
		// should never happen
		return err
	}

	ts := time.Now().UTC()

	for _, m := range ev.Memberships {
		for _, vid := range m.Vertexes {
			t.maybeEmitOp(ts, vid, false)
		}
	}

	for _, v := range ev.Vertexes {
		t.maybeEmitOp(ts, v.Id, true)
	}

	for _, l := range ev.Logs {
		t.push(LogPayload{
			OpID:   l.Vertex,
			Data:   string(l.Data),
			Stream: int(l.Stream.Number()),
		}, l.Timestamp.AsTime())
	}

	return nil
}

// maybeEmitOp emits a OpPayload for a vertex if either A) an OpPayload hasn't
// been emitted yet because we saw the vertex before its membership, or B) the
// vertex has been updated.
func (t *writer) maybeEmitOp(ts time.Time, vid string, isUpdated bool) {
	v, found := t.pipeliner.Vertex(vid)
	if !found {
		return
	}

	if len(v.Groups) == 0 {
		// should be impossible, since the vertex is found and we've processed
		// a membership for it
		return
	}

	// TODO(vito): for now, we only let a vertex be a member of a single
	// group. I spent a long time supporting many-to-many memberships, and
	// intelligently tree-shaking in the frontend to only show vertices in
	// their most relevant groups, but still haven't found a great heuristic.
	// Limiting vertices to a single group allows us to fully switch to
	// Progrock without having to figure all that out yet.
	group := v.Groups[0]
	pipeline := v.Pipelines[0]

	key := vertexMembership{
		vertexID: vid,
		groupID:  group,
	}

	if !t.emittedMemberships[key] || isUpdated {
		t.push(t.vertexOp(v.Vertex, pipeline), ts)
		t.emittedMemberships[key] = true
	}
}

func (t *writer) Close() error {
	// Stop accepting new events
	t.mu.Lock()
	if t.closed {
		// prevent errors when trying to close multiple times
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

func (t *writer) vertexOp(v *progrock.Vertex, pl pipeline.Path) OpPayload {
	op := OpPayload{
		OpID:     v.Id,
		OpName:   v.Name,
		Internal: v.Internal,

		Pipeline: pl,

		Cached: v.Cached,
		Error:  v.GetError(),

		Inputs: v.Inputs,
	}

	if v.Started != nil {
		t := v.Started.AsTime()
		op.Started = &t
	}

	if v.Completed != nil {
		t := v.Completed.AsTime()
		op.Completed = &t
	}

	return op
}

func (t *writer) push(p Payload, ts time.Time) {
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

func (t *writer) start() {
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

func (t *writer) send() {
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

	req, err := http.NewRequest(http.MethodPost, t.url, bytes.NewReader(payload.Bytes()))
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
