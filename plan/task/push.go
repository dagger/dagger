package task

import (
	"context"
	"fmt"

	"github.com/docker/distribution/reference"
	bk "github.com/moby/buildkit/client"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Push", func() Task { return &pushTask{} })
}

type pushTask struct {
}

func (c *pushTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	rawDest, err := v.Lookup("dest").String()
	if err != nil {
		return nil, err
	}

	dest, err := reference.ParseNormalizedNamed(rawDest)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ref %s: %w", rawDest, err)
	}
	// Add the default tag "latest" to a reference if it only has a repo name.
	dest = reference.TagNameOnly(dest)

	// Read auth info
	if auth := v.Lookup("auth"); auth.Exists() {
		// Read auth info
		a, err := decodeAuthValue(pctx, auth)
		if err != nil {
			return nil, err
		}
		// Extract registry target from dest
		target, err := solver.ParseAuthHost(rawDest)
		if err != nil {
			return nil, err
		}
		s.AddCredentials(target, a.Username, a.Secret.PlainText())
		lg.Debug().Str("target", target).Msg("add target credentials")
	}

	// Get input state
	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}
	st, err := input.State()
	if err != nil {
		return nil, err
	}

	// Decode the image config
	imageConfig := ImageConfig{}
	if err := v.Lookup("config").Decode(&imageConfig); err != nil {
		return nil, err
	}

	img := NewImage(imageConfig, pctx.Platform.Get())

	// Export image
	lg.Debug().Str("dest", dest.String()).Msg("export image")
	resp, err := s.Export(ctx, st, &img, bk.ExportEntry{
		Type: bk.ExporterImage,
		Attrs: map[string]string{
			"name": dest.String(),
			"push": "true",
		},
	}, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	digest, hasImageDigest := resp.ExporterResponse["containerimage.digest"]
	if !hasImageDigest {
		return nil, fmt.Errorf("image push target %q did not return an image digest", dest.String())
	}
	imageRef := fmt.Sprintf("%s@%s", resp.ExporterResponse["image.name"], digest)

	// Fill result
	return compiler.NewValue().FillFields(map[string]interface{}{
		"result": imageRef,
	})
}
