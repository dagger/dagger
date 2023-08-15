package core

import (
	"github.com/dagger/dagger/core/pipeline"
)

type Query struct {
	// Pipeline
	Pipeline pipeline.Path `json:"pipeline"`
}

func (query *Query) PipelinePath() pipeline.Path {
	if query == nil {
		return nil
	}
	return query.Pipeline
}
