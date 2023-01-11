package core

type Query struct {
	Context QueryContext
}

type QueryContext struct {
	// Pipeline
	Pipeline PipelinePath `json:"pipeline"`
}
