package core

import (
	"context"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/moby/buildkit/client/llb"
)

// Override the progress pipeline of every LLB vertex in the DAG.
//
// FIXME: this can't be done in a normal way because Buildkit doesn't currently
// allow overriding the metadata of DefinitionOp. See this PR and comment:
// https://github.com/moby/buildkit/pull/2819
func overrideProgress(ctx context.Context, def *llb.Definition, pipeline pipeline.Path) {
	for dgst, metadata := range def.Metadata {
		metadata.ProgressGroups = append(
			metadata.ProgressGroups,
			pipeline.ProgressGroup(ctx),
		)
		def.Metadata[dgst] = metadata
	}
}
