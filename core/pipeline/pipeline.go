package pipeline

import (
	"encoding/json"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/vito/progrock"
)

type Pipeline struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Labels      []Label `json:"labels,omitempty"`
}

type Path []Pipeline

func (g Path) Copy() Path {
	copy := make(Path, 0, len(g))
	copy = append(copy, g...)
	return copy
}

func (g Path) Add(pipeline Pipeline) Path {
	// make a copy of path, don't modify in-place
	newPath := g.Copy()
	// add the sub-pipeline
	newPath = append(newPath, pipeline)
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

func (g Path) RecorderGroup(rec *progrock.Recorder) *progrock.Recorder {
	for _, p := range g {
		var labels []*progrock.Label
		for _, l := range p.Labels {
			labels = append(labels, &progrock.Label{
				Name:  l.Name,
				Value: l.Value,
			})
		}
		rec = rec.WithGroup(p.Name, labels...)
	}
	return rec
}

func (g Path) ProgressGroup(rec *progrock.Recorder) *pb.ProgressGroup {
	return &pb.ProgressGroup{
		Id:   g.ID(),
		Name: g.Name(),
	}
}

func (g Path) LLBOpt(rec *progrock.Recorder) llb.ConstraintsOpt {
	pg := g.ProgressGroup(rec)
	return llb.ProgressGroup(pg.Id, pg.Name, pg.Weak)
}

type CustomName struct {
	Name     string `json:"name,omitempty"`
	Pipeline Path   `json:"pipeline,omitempty"`
	Internal bool   `json:"internal,omitempty"`
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
