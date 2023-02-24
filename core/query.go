package core

import (
	"github.com/dagger/dagger/core/pipeline"
)

type Query struct {
	Context QueryContext
}

const RootPipelineName = "root"

// PipelinePath returns the current pipeline path prepended with a "root"
// pipeline containing default labels.
//
// When called against a nil receiver, as will happen if no pipelines have
// been created, it will return a path with only the root pipeline.
func (query *Query) PipelinePath() pipeline.Path {
	pipeline := pipeline.Path{
		{
			Name:   RootPipelineName,
			Labels: pipeline.RootLabels(),
		},
	}

	if query != nil {
		pipeline = append(pipeline, query.Context.Pipeline...)
	}

	return pipeline
}

type QueryContext struct {
	// Pipeline
	Pipeline pipeline.Path `json:"pipeline"`
}
