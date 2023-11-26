package core

import (
	"github.com/dagger/dagger/core/pipeline"
)

type Query struct {
	// Believe it or not, Query is also IDable because there are queries that
	// modify it.
	Identified

	// Pipeline
	Pipeline pipeline.Path `json:"pipeline"`
}

func (query *Query) PipelinePath() pipeline.Path {
	if query == nil {
		return nil
	}
	return query.Pipeline
}
