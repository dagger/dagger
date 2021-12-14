package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/docker/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Pull", func() Task { return &pullTask{} })
}

type pullTask struct {
}

func (c *pullTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	// FIXME: handle auth
	rawRef, err := v.Lookup("source").String()
	if err != nil {
		return nil, err
	}

	ref, err := reference.ParseNormalizedNamed(rawRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ref %s: %w", rawRef, err)
	}
	// Add the default tag "latest" to a reference if it only has a repo name.
	ref = reference.TagNameOnly(ref)

	st := llb.Image(
		ref.String(),
		withCustomName(v, "Pull %s", rawRef),
	)

	// Load image metadata and convert to to LLB.
	platform := pctx.Platform.Get()
	image, digest, err := s.ResolveImageConfig(ctx, ref.String(), llb.ResolveImageConfigOpt{
		LogName:  vertexNamef(v, "load metadata for %s", ref.String()),
		Platform: &platform,
	})
	if err != nil {
		return nil, err
	}
	imageJSON, err := json.Marshal(image)
	if err != nil {
		return nil, err
	}
	// Apply Image Config on top of LLB instructions
	st, err = st.WithImageConfig(imageJSON)
	if err != nil {
		return nil, err
	}

	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}
	fs := pctx.FS.New(result)

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
		"digest": digest,
		"config": image.Config,
	})
}
