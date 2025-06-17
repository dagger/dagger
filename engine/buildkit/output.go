package buildkit

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	solverpb "github.com/moby/buildkit/solver/pb"
)

type output struct {
	vertex llb.Vertex
	idx    solverpb.OutputIndex
}

func (o *output) ToInput(ctx context.Context, c *llb.Constraints) (*solverpb.Input, error) {
	//nolint:dogsled
	dgst, _, _, _, err := o.vertex.Marshal(ctx, c)
	if err != nil {
		return nil, err
	}
	return &solverpb.Input{Digest: dgst, Index: o.idx}, nil
}

func (o *output) Vertex(context.Context, *llb.Constraints) llb.Vertex {
	return o.vertex
}

func StateIdx(ctx context.Context, st llb.State, idx solverpb.OutputIndex, c *llb.Constraints) llb.State {
	vtx := st.Output().Vertex(ctx, c)
	return llb.NewState(&output{
		vertex: vtx,
		idx:    idx,
	})
}
