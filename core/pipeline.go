package core

import (
	"encoding/json"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
)

type Pipeline struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type PipelinePath []Pipeline

func (g PipelinePath) Copy() PipelinePath {
	copy := make(PipelinePath, 0, len(g))
	copy = append(copy, g...)
	return copy
}

func (g PipelinePath) Add(pipeline Pipeline) PipelinePath {
	// make a copy of path, don't modify in-place
	newPath := g.Copy()
	// add the sub-pipeline
	newPath = append(newPath, pipeline)
	return newPath
}

func (g PipelinePath) ID() string {
	id, err := json.Marshal(g)
	if err != nil {
		panic(err)
	}
	return string(id)
}

func (g PipelinePath) Name() string {
	if len(g) == 0 {
		return ""
	}
	return g[len(g)-1].Name
}

func (g PipelinePath) ProgressGroup() *pb.ProgressGroup {
	return &pb.ProgressGroup{
		Id:   g.ID(),
		Name: g.Name(),
	}
}

func (g PipelinePath) LLBOpt() llb.ConstraintsOpt {
	pg := g.ProgressGroup()
	return llb.ProgressGroup(pg.Id, pg.Name, pg.Weak)
}

type CustomName struct {
	Name     string       `json:"name,omitempty"`
	Pipeline PipelinePath `json:"pipeline,omitempty"`
	Internal bool         `json:"internal,omitempty"`
}

func (c CustomName) String() string {
	enc, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(enc)
}

func (c CustomName) LLBOpt() llb.ConstraintsOpt {
	return llb.WithCustomName(c.String())
}
