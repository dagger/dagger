package core

import (
	"github.com/dagger/dagger/core/pipeline"
)

type Query struct {
	Context QueryContext
}

type QueryContext struct {
	// Pipeline
	Pipeline pipeline.Path `json:"pipeline"`
}
