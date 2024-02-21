package pipeline

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/vito/progrock"
)

type Pipeline struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Labels      []Label `json:"labels,omitempty"`
	Weak        bool    `json:"weak,omitempty"`
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

const ProgrockDescriptionLabel = "dagger.io/pipeline.description"

// RecorderGroup converts the path to a Progrock recorder for the group.
func (g Path) WithGroups(ctx context.Context) context.Context {
	if len(g) == 0 {
		return ctx
	}

	rec := progrock.FromContext(ctx)

	for _, p := range g {
		var labels []*progrock.Label

		if p.Description != "" {
			labels = append(labels, &progrock.Label{
				Name:  ProgrockDescriptionLabel,
				Value: p.Description,
			})
		}

		for _, l := range p.Labels {
			labels = append(labels, &progrock.Label{
				Name:  l.Name,
				Value: l.Value,
			})
		}

		opts := []progrock.GroupOpt{}

		if len(labels) > 0 {
			opts = append(opts, progrock.WithLabels(labels...))
		}

		if p.Weak {
			opts = append(opts, progrock.Weak())
		}

		// WithGroup stores an internal hierarchy of groups by name, so this will
		// always return the same group ID throughout the session.
		rec = rec.WithGroup(p.Name, opts...)
	}

	return progrock.ToContext(ctx, rec)
}
