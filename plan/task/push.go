package task

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/platforms"
	"github.com/docker/distribution/reference"
	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerfile/dockerfile2llb"
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

func (c *pushTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
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
	auth, err := decodeAuthValue(pctx, v.Lookup("auth"))
	if err != nil {
		return nil, err
	}
	for _, a := range auth {
		s.AddCredentials(a.Target, a.Username, a.Secret.PlainText())
		lg.Debug().Str("target", a.Target).Msg("add target credentials")
	}

	// FIXME maybe we can find a better way than an if statement to
	// handle push single image and multiple platform image
	if input := v.Lookup("input"); input.Exists() {
		return pushSinglePlatformImage(ctx, pctx, s, v, dest)
	}

	if inputs := v.Lookup("inputs"); inputs.Exists() {
		return pushMultiPlatformImage(ctx, pctx, s, v, dest)
	}
	return nil, fmt.Errorf("no input provided")
}

func pushSinglePlatformImage(
	ctx context.Context,
	pctx *plancontext.Context,
	s solver.Solver,
	v *compiler.Value,
	dest reference.Named) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

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
	imageConfig := dockerfile2llb.ImageConfig{}
	if err := v.Lookup("config").Decode(&imageConfig); err != nil {
		return nil, err
	}

	// Export image
	lg.Debug().Str("dest", dest.String()).Msg("export image")
	resp, err := s.Export(ctx, st, &dockerfile2llb.Image{Config: imageConfig}, bk.ExportEntry{
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

func pushMultiPlatformImage(
	ctx context.Context,
	pctx *plancontext.Context,
	s solver.Solver,
	v *compiler.Value,
	dest reference.Named) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	// Retrieve inputs
	var inputs map[string]interface{}
	if err := v.Lookup("inputs").Decode(&inputs); err != nil {
		return nil, err
	}

	images := []*solver.Image{}
	// Retrieve images configuration
	for e := range inputs {
		// Parse platform
		platform, err := platforms.Parse(e)
		if err != nil {
			return nil, err
		}

		path := fmt.Sprintf("inputs.\"%s\"", e)

		// Retrieve filesystem
		input, err := pctx.FS.FromValue(v.Lookup(fmt.Sprintf("%s.input", path)))
		if err != nil {
			return nil, err
		}
		st, err := input.State()
		if err != nil {
			return nil, err
		}

		// Decode the image config
		imageConfig := dockerfile2llb.ImageConfig{}
		if err := v.Lookup(fmt.Sprintf("%s.config", path)).Decode(&imageConfig); err != nil {
			return nil, err
		}

		// Push it to images
		images = append(images, &solver.Image{
			Platform: platform,
			State:    st,
			Image:    &dockerfile2llb.Image{Config: imageConfig}})
	}

	lg.Debug().Str("dest", dest.String()).Msg("export image")
	resp, err := s.Exports(ctx, images, bk.ExportEntry{
		Type: bk.ExporterImage,
		Attrs: map[string]string{
			"name": dest.String(),
			"push": "true",
		},
	})
	if err != nil {
		return nil, err
	}

	// Retrieve manifest list digest
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
