package telemetry

import (
	"sync"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/vito/progrock"
)

type writer struct {
	telemetry *Telemetry
	pipeliner *Pipeliner

	// emittedMemberships keeps track of whether we've emitted an OpPayload for a
	// vertex yet.
	emittedMemberships map[vertexMembership]bool

	mu sync.Mutex
}

type vertexMembership struct {
	vertexID string
	groupID  string
}

func NewWriter(t *Telemetry) progrock.Writer {
	return &writer{
		telemetry:          t,
		pipeliner:          NewPipeliner(),
		emittedMemberships: map[vertexMembership]bool{},
	}
}

func (t *writer) WriteStatus(ev *progrock.StatusUpdate) error {
	t.pipeliner.TrackUpdate(ev)

	t.mu.Lock()
	defer t.mu.Unlock()

	ts := time.Now().UTC()

	for _, m := range ev.Memberships {
		for _, vid := range m.Vertexes {
			if v, found := t.pipeliner.Vertex(vid); found {
				t.maybeEmitOp(ts, v, false)
			}
		}
	}

	for _, eventVertex := range ev.Vertexes {
		if v, found := t.pipeliner.Vertex(eventVertex.Id); found {
			// override the vertex with the current event vertex since a
			// single PipelineEvent could contain duplicated vertices with
			// different data like started and completed
			v.Vertex = eventVertex
			t.maybeEmitOp(ts, v, true)
		}
	}

	for _, l := range ev.Logs {
		t.telemetry.Push(LogPayload{
			OpID:   l.Vertex,
			Data:   string(l.Data),
			Stream: int(l.Stream.Number()),
		}, l.Timestamp.AsTime())
	}

	return nil
}

func (t *writer) Close() error {
	t.telemetry.Close()
	return nil
}

// maybeEmitOp emits a OpPayload for a vertex if either A) an OpPayload hasn't
// been emitted yet because we saw the vertex before its membership, or B) the
// vertex has been updated.
func (t *writer) maybeEmitOp(ts time.Time, v *PipelinedVertex, isUpdated bool) {
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
		vertexID: v.Id,
		groupID:  group,
	}

	if !t.emittedMemberships[key] || isUpdated {
		t.telemetry.Push(t.vertexOp(v.Vertex, pipeline), ts)
		t.emittedMemberships[key] = true
	}
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
