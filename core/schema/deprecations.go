package schema

// PipelineLabel is deprecated and has no effect.
type PipelineLabel struct {
	Name  string `field:"true" doc:"Label name."`
	Value string `field:"true" doc:"Label value."`
}

func (PipelineLabel) TypeName() string {
	return "PipelineLabel"
}

func (PipelineLabel) TypeDescription() string {
	return "Key value object that represents a pipeline label."
}
