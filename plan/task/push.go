package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/containerd/containerd/platforms"
	"github.com/docker/distribution/reference"
	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/gateway/client"
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

	var resp *bk.SolveResponse

	if inputs := v.Lookup("inputs"); inputs.Exists() {
		fields, err := inputs.Fields()
		if err != nil {
			return nil, err
		}
		if len(fields) > 0 {
			resp, err = c.pushMultiArch(ctx, pctx, s, dest, fields)
			if err != nil {
				return nil, err
			}
		}
	}

	// if multi inputs not exists or empty
	if resp == nil {
		resp, err = c.pushSingleArch(ctx, pctx, s, dest, v)
		if err != nil {
			return nil, err
		}
	}

	digest, hasImageDigest := resp.ExporterResponse[exptypes.ExporterImageDigestKey]
	if !hasImageDigest {
		return nil, fmt.Errorf("image push target %q did not return an image digest", dest.String())
	}

	imageRef := fmt.Sprintf("%s@%s", resp.ExporterResponse["image.name"], digest)

	// Fill result
	return compiler.NewValue().FillFields(map[string]interface{}{
		"result": imageRef,
	})
}

func (c *pushTask) pushSingleArch(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, dest reference.Named, v *compiler.Value) (*bk.SolveResponse, error) {
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

	// Retrieve platform
	platform := pctx.Platform.Get()
	if p := v.Lookup("platform"); p.Exists() {
		targetPlatform, err := p.String()
		if err != nil {
			return nil, err
		}
		if targetPlatform != "" {
			platform, err = platforms.Parse(targetPlatform)
			if err != nil {
				return nil, err
			}
		}
	}

	img := NewImage(imageConfig, platform)

	// Export image
	log.Ctx(ctx).Debug().Str("dest", dest.String()).Str("platform", platforms.Format(platform)).Msg("export image")
	return s.Export(ctx, st, &img, bk.ExportEntry{
		Type: bk.ExporterImage,
		Attrs: map[string]string{
			"name": dest.String(),
			"push": "true",
		},
	}, pctx.Platform.Get())
}

func (c *pushTask) pushMultiArch(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, dest reference.Named, fields []compiler.Field) (*bk.SolveResponse, error) {
	pls := make([]exptypes.Platform, len(fields))
	buildFuncs := make([]solver.BuildFunc, len(fields))

	for i := range fields {
		platformID, err := strconv.Unquote(fields[i].Selector.String())
		if err != nil {
			return nil, err
		}
		platform, err := platforms.Parse(platformID)
		if err != nil {
			return nil, err
		}
		// Normalized platform
		platformID = platforms.Format(platform)

		fieldValue := fields[i].Value

		// Decode the image config
		imageConfig := ImageConfig{}
		if err := fieldValue.Lookup("config").Decode(&imageConfig); err != nil {
			return nil, err
		}

		img := NewImage(imageConfig, platform)

		// Retrieve filesystem
		input, err := pctx.FS.FromValue(fieldValue.Lookup("input"))
		if err != nil {
			return nil, err
		}

		buildFuncs[i] = func(ctx context.Context, ret *client.Result, c client.Client) error {
			st, err := input.State()
			if err != nil {
				return err
			}
			st = st.Platform(platform)
			def, err := st.Marshal(ctx)
			if err != nil {
				return err
			}
			r, err := c.Solve(ctx, client.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				return err
			}
			ref, err := r.SingleRef()
			if err != nil {
				return err
			}

			// Attach the image config if provided
			config, err := json.Marshal(img)
			if err != nil {
				return fmt.Errorf("failed to marshal image config: %w", err)
			}
			ret.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformID), config)
			ret.AddRef(platformID, ref)
			return nil
		}

		pls[i] = exptypes.Platform{
			ID:       platformID,
			Platform: platform,
		}
	}

	buildFuncs = append(buildFuncs, func(ctx context.Context, ret *client.Result, c client.Client) error {
		dt, err := json.Marshal(&exptypes.Platforms{Platforms: pls})
		if err != nil {
			return err
		}
		// Add manifest list
		ret.AddMeta(exptypes.ExporterPlatformsKey, dt)
		return nil
	})

	ee := bk.ExportEntry{
		Type: bk.ExporterImage,
		Attrs: map[string]string{
			"name": dest.String(),
			"push": "true",
		},
	}

	// Export multi-arch image
	log.Ctx(ctx).Debug().Str("dest", dest.String()).Msg("export multi-arch image")
	return s.BuildExport(ctx, ee, buildFuncs...)
}
