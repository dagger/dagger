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

	// pipelinePaths stores the mapping from group IDs to pipeline paths
	pipelinePaths map[string]pipeline.Path

	// memberships stores the groups IDs that a vertex is a member of
	memberships map[string][]string

	// ops stores ops converted from vertexes so that they can be emitted with
	// pipeline paths once their membership is known
	ops map[string]OpPayload

	mu     sync.Mutex
	queue  []*Event
	stopCh chan struct{}
	doneCh chan struct{}
	closed bool
}

func NewWriter() (progrock.Writer, string, bool) {
	t := &writer{
		runID:         uuid.NewString(),
		url:           os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_URL"),
		token:         os.Getenv("_EXPERIMENTAL_DAGGER_CLOUD_TOKEN"),
		pipelinePaths: map[string]pipeline.Path{},
		memberships:   map[string][]string{},
		ops:           map[string]OpPayload{},
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
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

	ts := time.Now().UTC()

	for _, g := range ev.Groups {
		t.pipelinePaths[g.Id] = t.groupPath(g)
	}

	for _, m := range ev.Memberships {
		for _, vid := range m.Vertexes {
			t.memberships[vid] = append(t.memberships[vid], m.Group)

			op, found := t.ops[vid]
			if found {
				t.pushOp(ts, op, m.Group)
			}
		}
	}

	for _, v := range ev.Vertexes {
		id := v.Id

		op := t.vertexOp(v)
		t.ops[v.Id] = op

		for _, gid := range t.memberships[id] {
			t.pushOp(ts, op, gid)
		}
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

func (t *writer) vertexOp(v *progrock.Vertex) OpPayload {
	op := OpPayload{
		OpID:     v.Id,
		OpName:   v.Name,
		Internal: v.Internal,

		// pipeline is provided via pushOp when groups are known
		// Pipeline: ,

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

func (t *writer) groupPath(group *progrock.Group) pipeline.Path {
	self := pipeline.Pipeline{
		Name: group.Name,
	}
	for _, l := range group.Labels {
		if l.Name == pipeline.ProgrockDescriptionLabel {
			// Progrock doesn't have a separate 'description' field, so we escort it
			// through labels instead
			self.Description = l.Value
		} else {
			self.Labels = append(self.Labels, pipeline.Label{
				Name:  l.Name,
				Value: l.Value,
			})
		}
	}
	path := pipeline.Path{}
	if group.Parent != nil {
		parentPath, found := t.pipelinePaths[group.GetParent()]
		if found {
			path = append(path, parentPath...)
		}
	}
	path = append(path, self)
	return path
}

func (t *writer) pushOp(ts time.Time, op OpPayload, gid string) bool {
	pipeline, found := t.pipelinePaths[gid]
	if !found {
		return false
	}

	op.Pipeline = pipeline

	t.push(op, ts)

	return true
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
