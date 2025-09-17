package llbbuild

import (
	"context"
	"testing"

	"github.com/dagger/dagger/buildkit/client/llb"
	"github.com/dagger/dagger/buildkit/solver/pb"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestMarshal(t *testing.T) {
	t.Parallel()
	b := NewBuildOp(newDummyOutput("foobar"), WithFilename("myfilename"))
	dgst, dt, opMeta, _, err := b.Marshal(context.TODO(), &llb.Constraints{})
	_ = opMeta
	require.NoError(t, err)

	require.Equal(t, dgst, digest.FromBytes(dt))

	var op pb.Op
	err = op.Unmarshal(dt)
	require.NoError(t, err)

	buildop := op.GetBuild()
	require.NotNil(t, buildop)

	require.Equal(t, 1, len(op.Inputs))
	require.Equal(t, pb.LLBBuilder, buildop.Builder)
	require.Equal(t, 1, len(buildop.Inputs))
	require.Equal(t, &pb.BuildInput{Input: pb.InputIndex(0)}, buildop.Inputs[pb.LLBDefinitionInput])

	require.Equal(t, "myfilename", buildop.Attrs[pb.AttrLLBDefinitionFilename])
}

func newDummyOutput(key string) llb.Output {
	dgst := digest.FromBytes([]byte(key))
	return &dummyOutput{dgst: dgst}
}

type dummyOutput struct {
	dgst digest.Digest
}

func (d *dummyOutput) ToInput(context.Context, *llb.Constraints) (*pb.Input, error) {
	return &pb.Input{
		Digest: d.dgst,
		Index:  pb.OutputIndex(7), // random constant
	}, nil
}
func (d *dummyOutput) Vertex(context.Context, *llb.Constraints) llb.Vertex {
	return nil
}
