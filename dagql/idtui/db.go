package idtui

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/idproto"
	"github.com/vito/progrock"
)

func init() {
}

type DB struct {
	l *slog.Logger

	Epoch, End time.Time

	IDs       map[string]*idproto.ID
	Vertices  map[string]*progrock.Vertex
	Tasks     map[string][]*progrock.VertexTask
	Outputs   map[string]map[string]struct{}
	OutputOf  map[string]map[string]struct{}
	Children  map[string]map[string]struct{}
	Intervals map[string]map[time.Time]*progrock.Vertex
}

func NewDB(log *slog.Logger) *DB {
	if log == nil {
		log = slog.Default()
	}

	return &DB{
		l: log,

		Epoch: time.Now(),  // replaced at runtime
		End:   time.Time{}, // replaced at runtime

		IDs:       make(map[string]*idproto.ID),
		Vertices:  make(map[string]*progrock.Vertex),
		Tasks:     make(map[string][]*progrock.VertexTask),
		OutputOf:  make(map[string]map[string]struct{}),
		Outputs:   make(map[string]map[string]struct{}),
		Children:  make(map[string]map[string]struct{}),
		Intervals: make(map[string]map[time.Time]*progrock.Vertex),
	}
}

var _ progrock.Writer = (*DB)(nil)

func (db *DB) WriteStatus(status *progrock.StatusUpdate) error {
	// collect IDs
	for _, meta := range status.Metas {
		switch meta.Name {
		case "id":
			var id idproto.ID
			if err := meta.Data.UnmarshalTo(&id); err != nil {
				return fmt.Errorf("unmarshal payload: %w", err)
			}
			db.IDs[meta.Vertex] = &id
		}
	}

	for _, v := range status.Vertexes {
		// track the earliest start time and latest end time
		if v.Started != nil && v.Started.AsTime().Before(db.Epoch) {
			db.Epoch = v.Started.AsTime()
		}
		if v.Completed != nil && v.Completed.AsTime().After(db.End) {
			db.End = v.Completed.AsTime()
		}

		// keep track of vertices, just so we track everything, not just IDs
		db.Vertices[v.Id] = v

		// keep track of outputs
		for _, out := range v.Outputs {
			if strings.HasPrefix(v.Name, "load") && strings.HasSuffix(v.Name, "FromID") {
				// don't consider loadFooFromID to be a 'creator'
				continue
			}
			if db.Outputs[v.Id] == nil {
				db.Outputs[v.Id] = make(map[string]struct{})
			}
			db.Outputs[v.Id][out] = struct{}{}
			if db.OutputOf[out] == nil {
				db.OutputOf[out] = make(map[string]struct{})
			}
			db.OutputOf[out][v.Id] = struct{}{}
		}

		// keep track of intervals seen for a digest
		if v.Started != nil {
			if db.Intervals[v.Id] == nil {
				db.Intervals[v.Id] = make(map[time.Time]*progrock.Vertex)
			}
			db.Intervals[v.Id][v.Started.AsTime()] = v
		}
	}

	// track vertex sub-tasks
	for _, t := range status.Tasks {
		db.recordTask(t)
	}

	// track parent/child vertices
	for _, v := range status.Children {
		if db.Children[v.Vertex] == nil {
			db.Children[v.Vertex] = make(map[string]struct{})
		}
		for _, out := range v.Vertexes {
			db.Children[v.Vertex][out] = struct{}{}
		}
	}

	return nil
}

func (db *DB) recordTask(t *progrock.VertexTask) {
	tasks := db.Tasks[t.Vertex]
	var updated bool
	for i, task := range tasks {
		if task.Name == t.Name {
			tasks[i] = t
			updated = true
		}
	}
	if !updated {
		tasks = append(tasks, t)
		db.Tasks[t.Vertex] = tasks
	}
}

// Step returns a Step for the given digest if and only if the step should be
// displayed.
//
// Currently this means:
//
// - We don't show `id` selections, since that would be way too much noise.
// - We don't show internal non-ID vertices, since they're not interesting.
// - We DO show internal ID vertices, since they're currently marked internal
// just to hide them from the old TUI.
func (db *DB) Step(dig string) (*Step, bool) {
	outVtx := db.FirstVertex(dig)
	outID := db.IDs[dig]
	switch {
	case outID != nil && outID.Field == "id":
		db.l.Info("ignoring id selection")
		return nil, false
	case outID == nil && outVtx != nil && outVtx.Internal:
		db.l.Info("ignoring internal vertex", "name", outVtx.Name, "id", outVtx.Id)
		return nil, false
	case outID != nil && outVtx != nil:
	case outID == nil && outVtx != nil:
		db.l.Warn("missing step ID", "digest", dig, "vertex", outVtx.Name)
		// return Step{}, false
	case outID != nil && outVtx == nil:
		db.l.Warn("missing step vertex", "digest", dig, "id", outID.DisplaySelf())
		return nil, false
	case outID == nil && outVtx == nil:
		db.l.Warn("missing all step info", "digest", dig)
		return nil, false
	}
	step := &Step{
		Digest: dig,
		db:     db,
	}
	if outID != nil {
		var ok bool
		if outID.Parent != nil {
			step.Base, ok = db.HighLevelStep(outID.Parent)
			if !ok {
				db.l.Warn("missing base", "step", outID.Display(), "digest", dig)
				return nil, false
			}
		}
	}
	return step, true
}

func (db *DB) HighLevelStep(id *idproto.ID) (*Step, bool) {
	parentDig, err := id.Digest()
	if err != nil {
		db.l.Warn("digest parent: %w", err)
		return nil, false
	}
	return db.Step(db.Simplify(parentDig.String()))
}

func (db *DB) FirstVertex(dig string) *progrock.Vertex {
	var earliest *progrock.Vertex
	for start, vtx := range db.Intervals[dig] {
		if earliest == nil {
			earliest = vtx
			continue
		}
		if vtx.Completed == nil && earliest.Completed != nil {
			// prioritize actively running vertex over a completed one
			earliest = vtx
			continue
		}
		if earliest.Completed == nil && vtx.Completed != nil {
			// never override a completed vertex with an incomplete one
			continue
		}
		if start.Before(earliest.Started.AsTime()) {
			earliest = vtx
		}
	}
	return earliest
}

func (db *DB) IsTransitiveDependency(dig, depDig string) bool {
	v := db.FirstVertex(dig)
	if v == nil {
		return false
	}
	for _, dig := range v.Inputs {
		if dig == depDig {
			return true
		}
		if db.IsTransitiveDependency(dig, depDig) {
			return true
		}
	}
	return false
}

func (*DB) Close() error {
	return nil
}

func idSize(id *idproto.ID) int {
	size := 0
	for id := id; id != nil; id = id.Parent {
		size++
		size += len(id.Args)
	}
	return size
}

func (db *DB) Simplify(dig string) string {
	creators, ok := db.OutputOf[dig]
	if !ok {
		return dig
	}
	var smallestCreator *idproto.ID
	var smallestSize int
	for creator := range creators {
		id, ok := db.IDs[creator]
		if ok {
			if size := idSize(id); smallestCreator == nil || size < smallestSize {
				smallestCreator = id
				smallestSize = size
			}
		}
	}
	if smallestCreator != nil {
		smallestDig, err := smallestCreator.Digest()
		if err != nil {
			db.l.Warn("digest id: %w", err)
			return dig
		}
		return db.Simplify(smallestDig.String())
	}
	return dig
}
