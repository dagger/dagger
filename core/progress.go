package core

import (
	"github.com/dagger/dagger/core/pipeline"
	"github.com/moby/buildkit/client/llb"
	"github.com/vito/progrock"
)

// Override the progress pipeline of every LLB vertex in the DAG.
//
// FIXME: this can't be done in a normal way because Buildkit doesn't currently
// allow overriding the metadata of DefinitionOp. See this PR and comment:
// https://github.com/moby/buildkit/pull/2819
func overrideProgress(rec *progrock.Recorder, def *llb.Definition, pipeline pipeline.Path) {
	for dgst, metadata := range def.Metadata {
		metadata.ProgressGroup = pipeline.ProgressGroup(rec)
		def.Metadata[dgst] = metadata
	}
}
