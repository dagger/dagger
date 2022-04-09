package task

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"

	"github.com/docker/distribution/reference"
	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Export", func() Task { return &exportTask{} })
}

type exportTask struct {
}

func (t exportTask) PreRun(_ context.Context, pctx *plancontext.Context, v *compiler.Value) error {
	dir, err := os.MkdirTemp("", "dagger-export-*")
	if err != nil {
		return err
	}

	pctx.TempDirs.Add(dir, v.Path().String())
	pctx.LocalDirs.Add(dir)

	return nil
}

func (t exportTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	dir := pctx.TempDirs.Get(v.Path().String())

	var opts struct {
		Tag  string
		Path string
		Type string
	}

	if err := v.Decode(&opts); err != nil {
		return nil, err
	}

	switch opts.Type {
	case bk.ExporterDocker, bk.ExporterOCI:
	default:
		return nil, fmt.Errorf("unsupported export type %q", opts.Type)
	}

	// Normalize tag
	tag, err := reference.ParseNormalizedNamed(opts.Tag)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ref %s: %w", opts.Tag, err)
	}
	tag = reference.TagNameOnly(tag)

	lg.Debug().Str("tag", tag.String()).Msg("normalized tag")

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
	resp, err := s.Export(ctx, st, &img, bk.ExportEntry{
		Type: opts.Type,
		Attrs: map[string]string{
			"name": tag.String(),
		},
		Output: func(a map[string]string) (io.WriteCloser, error) {
			file := filepath.Join(dir, opts.Path)
			return os.Create(filepath.Clean(file))
		},
	}, pctx.Platform.Get())

	if err != nil {
		return nil, err
	}

	// Save the image id
	imageID, ok := resp.ExporterResponse[exptypes.ExporterImageConfigDigestKey]
	if !ok {
		return nil, fmt.Errorf("image export for %q did not return an image id", tag.String())
	}

	// FIXME: Remove the `Copy` and use `Local` directly.
	//
	// Copy'ing is a costly operation which should be unnecessary.
	// However, using llb.Local directly breaks caching sometimes for unknown reasons.
	outputState := llb.Scratch().File(
		llb.Copy(
			llb.Local(
				dir,
				withCustomName(v, "Export %s", opts.Path),
			),
			"/",
			"/",
		),
		withCustomName(v, "Local %s [copy]", opts.Path),
	)

	result, err := s.Solve(ctx, outputState, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output":  fs.MarshalCUE(),
		"imageID": imageID,
	})
}
