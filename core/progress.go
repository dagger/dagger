package core

import (
	"github.com/dagger/dagger/core/pipeline"
	"github.com/moby/buildkit/client/llb"
)

// Override the progress pipeline of every LLB vertex in the DAG.
//
// FIXME: this can't be done in a normal way because Buildkit doesn't currently
// allow overriding the metadata of DefinitionOp. See this PR and comment:
// https://github.com/moby/buildkit/pull/2819
func overrideProgress(def *llb.Definition, pipeline pipeline.Path) {
	for dgst, metadata := range def.Metadata {
		// FIXME: this clobbers any existing progress groups, e.g. image pulls and
		// Dockerfile builds. We can't just check for != nil either because the
		// goal of this is sometimes to nest beneath it.
		metadata.ProgressGroup = pipeline.ProgressGroup()
		def.Metadata[dgst] = metadata
	}
}
