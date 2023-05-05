package pipeline

import (
	"context"
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

// RecorderGroup converts the path to a Progrock recorder for the group.
func (g Path) RecorderGroup(rec *progrock.Recorder) *progrock.Recorder {
	if len(g) == 0 {
		return rec
	}

	// drop the "root" pipeline; it's already initialized by Progrock
	g = g[1:]

	for _, p := range g {
		var labels []*progrock.Label
		for _, l := range p.Labels {
			labels = append(labels, &progrock.Label{
				Name:  l.Name,
				Value: l.Value,
			})
		}

		// WithGroup stores an internal hierarchy of groups by name, so this will
		// always return the same group ID throughout the session.
		rec = rec.WithGroup(p.Name, labels...)
	}

	return rec
}

func (g Path) ProgressGroup(ctx context.Context) *pb.ProgressGroup {
	rec := g.RecorderGroup(progrock.RecorderFromContext(ctx))
	return &pb.ProgressGroup{
		Id:   g.ID(),
		Name: g.Name(),
	}
}

func (g Path) LLBOpt(ctx context.Context) llb.ConstraintsOpt {
	pg := g.ProgressGroup(ctx)
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
