package idtui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/idproto"
	"github.com/vito/progrock"
)

func init() {
}

type DB struct {
	Epoch, End time.Time

	IDs       map[string]*idproto.ID
	Vertices  map[string]*progrock.Vertex
	Tasks     map[string][]*progrock.VertexTask
	Outputs   map[string]map[string]struct{}
	OutputOf  map[string]map[string]struct{}
	Children  map[string]map[string]struct{}
	Intervals map[string]map[time.Time]*progrock.Vertex
}

func NewDB() *DB {
	return &DB{
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
		if meta.Name == "id" {
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
	step := &Step{
		Digest: dig,
		db:     db,
	}

	outID := db.IDs[dig]
	switch {
	case outID != nil && outID.Field == "id":
		return nil, false
	case outID == nil && step.IsInternal():
		return nil, false
	case outID != nil && !step.HasStarted():
		return nil, false
	case outID == nil && !step.HasStarted():
		return nil, false
	}
	if outID != nil && outID.Base != nil {
		parentDig, err := outID.Base.Digest()
		if err != nil {
			return nil, false
		}
		step.Base = db.Simplify(parentDig.String())
	}
	return step, true
}

func (db *DB) HighLevelStep(id *idproto.ID) (*Step, bool) {
	parentDig, err := id.Digest()
	if err != nil {
		return nil, false
	}
	return db.Step(db.Simplify(parentDig.String()))
}

func (db *DB) MostInterestingVertex(dig string) *progrock.Vertex {
	var earliest *progrock.Vertex
	vs := make([]*progrock.Vertex, 0, len(db.Intervals[dig]))
	for _, vtx := range db.Intervals[dig] {
		vs = append(vs, vtx)
	}
	sort.Slice(vs, func(i, j int) bool {
		return vs[i].Started.AsTime().Before(vs[j].Started.AsTime())
	})
	for _, vtx := range db.Intervals[dig] {
		if vtx.Cached {
			continue
		}
		if earliest == nil {
			earliest = vtx
			continue
		}
		// if vtx.Completed == nil && earliest.Completed != nil {
		// 	// prioritize actively running vertex over a completed one
		// 	earliest = vtx
		// 	continue
		// }
		// if earliest.Completed == nil && vtx.Completed != nil {
		// 	// never replace a running vertex with a completed one
		// 	continue
		// }
		if vtx.Started.AsTime().Before(earliest.Started.AsTime()) {
			earliest = vtx
		}
	}
	return earliest
}

// func (db *DB) IsTransitiveDependency(dig, depDig string) bool {
// 	for _, v := range db.Intervals[dig] {
// 		for _, dig := range v.Inputs {
// 			if dig == depDig {
// 				return true
// 			}
// 			if db.IsTransitiveDependency(dig, depDig) {
// 				return true
// 			}
// 		}
// 		// assume they all have the same inputs
// 		return false
// 	}
// 	return false
// }

func (*DB) Close() error {
	return nil
}

func litSize(lit *idproto.Literal) int {
	switch x := lit.Value.(type) {
	case *idproto.Literal_Id:
		return idSize(x.Id)
	case *idproto.Literal_List:
		size := 0
		for _, lit := range x.List.Values {
			size += litSize(lit)
		}
		return size
	case *idproto.Literal_Object:
		size := 0
		for _, field := range x.Object.Values {
			size += litSize(field.Value)
		}
		return size
	}
	return 1
}

func idSize(id *idproto.ID) int {
	size := 0
	for id := id; id != nil; id = id.Base {
		size++
		size += len(id.Args)
		for _, arg := range id.Args {
			size += litSize(arg.Value)
		}
	}
	return size
}

func (db *DB) Simplify(dig string) string {
	creators, ok := db.OutputOf[dig]
	if !ok {
		return dig
	}
	var smallest = db.IDs[dig]
	var smallestSize = idSize(smallest)
	var simplified bool
	for creatorDig := range creators {
		creator, ok := db.IDs[creatorDig]
		if ok {
			if size := idSize(creator); smallest == nil || size < smallestSize {
				smallest = creator
				smallestSize = size
				simplified = true
			}
		}
	}
	if simplified {
		smallestDig, err := smallest.Digest()
		if err != nil {
			return dig
		}
		return db.Simplify(smallestDig.String())
	}
	return dig
}
