package task

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/platforms"
	"github.com/docker/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	"github.com/rs/zerolog/log"
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
	lg := log.Ctx(ctx)

	rawRef, err := v.Lookup("source").String()
	if err != nil {
		return nil, err
	}

	// Read auth info
	auth, err := decodeAuthValue(pctx, v.Lookup("auth"))
	if err != nil {
		return nil, err
	}
	for _, a := range auth {
		s.AddCredentials(a.Target, a.Username, a.Secret.PlainText())
		lg.Debug().Str("target", a.Target).Msg("add target credentials")
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

	// Retrieve platform
	platform := pctx.Platform.Get()
	if p := v.Lookup("platform"); p.Exists() {
		targetPlatform, err := p.String()
		if err != nil {
			return nil, err
		}

		platform, err = platforms.Parse(targetPlatform)
		if err != nil {
			return nil, err
		}
	}

	// Load image metadata and convert to to LLB.
	image, digest, err := s.ResolveImageConfig(ctx, ref.String(), llb.ResolveImageConfigOpt{
		LogName:  vertexNamef(v, "load metadata for %s", ref.String()),
		Platform: &platform,
	})
	if err != nil {
		return nil, err
	}

	result, err := s.Solve(ctx, st, platform)
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
