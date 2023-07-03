package core

import (
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
)

func RecordVertexes(recorder *progrock.Recorder, def *pb.Definition) {
	dgsts := []digest.Digest{}
	for dgst, meta := range def.Metadata {
		_ = meta
		if meta.ProgressGroup != nil {
			// Regular progress group, i.e. from Dockerfile; record it as a subgroup,
			// with 'weak' annotation so it's distinct from user-configured
			// pipelines.
			recorder.WithGroup(meta.ProgressGroup.Name, progrock.Weak()).Join(dgst)
		} else {
			dgsts = append(dgsts, dgst)
		}
	}

	recorder.Join(dgsts...)
}
