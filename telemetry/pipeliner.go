package telemetry

import (
	"sync"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/vito/progrock"
)

// Pipeliner listens to events and collects pipeline paths for vertices based
// on groups and group memberships.
type Pipeliner struct {
	mu sync.Mutex

	closed bool

	// pipelinePaths stores the mapping from group IDs to pipeline paths
	pipelinePaths map[string]pipeline.Path

	// memberships stores the groups IDs that a vertex is a member of
	memberships map[string][]string

	// vertices stores and updates vertexes as they received, so that they can
	// be associated to pipeline paths once their membership is known.
	vertices map[string]*progrock.Vertex
}

func NewPipeliner() *Pipeliner {
	return &Pipeliner{
		pipelinePaths: map[string]pipeline.Path{},
		memberships:   map[string][]string{},
		vertices:      map[string]*progrock.Vertex{},
	}
}

// PipelinedVertex is a Progrock vertex paired with all of its pipeline paths.
type PipelinedVertex struct {
	*progrock.Vertex

	// Groups stores the group IDs that this vertex is a member of. Each entry
	// has a corresponding entry in Pipelines.
	Groups []string

	// Pipelines stores the pipeline paths computed from Progrock groups.
	Pipelines []pipeline.Path
}

func (t *Pipeliner) WriteStatus(ev *progrock.StatusUpdate) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	for _, g := range ev.Groups {
		t.pipelinePaths[g.Id] = t.groupPath(g)
	}

	for _, m := range ev.Memberships {
		for _, vid := range m.Vertexes {
			t.memberships[vid] = append(t.memberships[vid], m.Group)
		}
	}

	for _, v := range ev.Vertexes {
		t.vertices[v.Id] = v
	}

	return nil
}

func (t *Pipeliner) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}

func (t *Pipeliner) Vertex(id string) (*PipelinedVertex, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	v, found := t.vertices[id]
	if !found {
		return nil, false
	}

	return t.vertex(v), true
}

func (t *Pipeliner) Vertices() []*PipelinedVertex {
	t.mu.Lock()
	defer t.mu.Unlock()

	vertices := make([]*PipelinedVertex, 0, len(t.vertices))
	for _, v := range t.vertices {
		vertices = append(vertices, t.vertex(v))
	}
	return vertices
}

func (t *Pipeliner) vertex(v *progrock.Vertex) *PipelinedVertex {
	return &PipelinedVertex{
		Vertex:    v,
		Groups:    t.memberships[v.Id],
		Pipelines: t.pipelines(t.memberships[v.Id]),
	}
}

func (t *Pipeliner) pipelines(groups []string) []pipeline.Path {
	paths := make([]pipeline.Path, 0, len(groups))
	for _, gid := range groups {
		paths = append(paths, t.pipelinePaths[gid])
	}
	return paths
}

func (t *Pipeliner) groupPath(group *progrock.Group) pipeline.Path {
	self := pipeline.Pipeline{
		Name: group.Name,
		Weak: group.Weak,
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
