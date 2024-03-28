package pipeline

import (
	"encoding/json"
	"strings"
)

type Pipeline struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Weak        bool   `json:"weak,omitempty"`
}

// Pipelineable is any object which can return a pipeline.Path.
type Pipelineable interface {
	PipelinePath() Path
}

type Path []*Pipeline

func (g Path) Copy() Path {
	copy := make(Path, 0, len(g))
	copy = append(copy, g...)
	return copy
}

func (g Path) Add(pipeline Pipeline) Path {
	// make a copy of path, don't modify in-place
	newPath := g.Copy()
	// add the sub-pipeline
	newPath = append(newPath, &pipeline)
	return newPath
}

func (g Path) ID() string {
	id, err := json.Marshal(g)
	if err != nil {
		panic(err)
	}
	return string(id)
}

func (g Path) Name() string {
	if len(g) == 0 {
		return ""
	}
	return g[len(g)-1].Name
}

func (g Path) String() string {
	parts := []string{}
	for _, part := range g {
		parts = append(parts, part.Name)
	}
	return strings.Join(parts, " / ")
}
