package task

import (
	"context"
	"fmt"
	"os"
	"path"

	"dagger.io/dagger"
	"github.com/rs/zerolog/log"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Export", func() Task { return &exportTask{} })
}

type exportTask struct {
}

func (t exportTask) Run(ctx context.Context, pctx *plancontext.Context, _ *solver.Solver, dgr *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	// dir := pctx.TempDirs.Get(v.Path().String())

	var opts struct {
		Tag  string
		Path string
		Type string
	}

	if err := v.Decode(&opts); err != nil {
		return nil, err
	}

	// switch opts.Type {
	// case bk.ExporterDocker, bk.ExporterOCI:
	// default:
	// 	return nil, fmt.Errorf("unsupported export type %q", opts.Type)
	// }

	// // Normalize tag
	// tag, err := reference.ParseNormalizedNamed(opts.Tag)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to parse ref %q: %w", opts.Tag, err)
	// }
	// tag = reference.TagNameOnly(tag)

	// lg.Debug().Str("tag", tag.String()).Msg("normalized tag")

	// Get input state
	fsid, err := utils.GetFSId(v.Lookup("input"))
	// input, err := pctx.FS.FromValue(v.Lookup("input"))
	// if err != nil {
	// 	return nil, err
	// }
	// st, err := input.State()
	// if err != nil {
	// 	return nil, err
	// }

	ctr := dgr.Container().WithFS(dgr.Directory(dagger.DirectoryOpts{ID: fsid}))

	tmpdir, err := os.MkdirTemp("", "dagger-export-*")

	if err != nil {
		return nil, err
	}
	pctx.TempDirs.Add(tmpdir, v.Path().String())

	exportfilepath := path.Join(tmpdir, opts.Path)
	lg.Debug().Str("path", exportfilepath).Msg("container tar path")

	success, err := ctr.Export(ctx, exportfilepath)
	if err != nil {
		return nil, err
	}

	if !success {
		return nil, fmt.Errorf("error exporting container image to %s", exportfilepath)
	}

	// Decode the image config
	// imageConfig := ImageConfig{}
	// if err := v.Lookup("config").Decode(&imageConfig); err != nil {
	// 	return nil, err
	// }

	// img := NewImage(imageConfig, pctx.Platform.Get())

	// // Export image
	// resp, err := s.Export(ctx, st, &img, bk.ExportEntry{
	// 	Type: opts.Type,
	// 	Attrs: map[string]string{
	// 		"name": tag.String(),
	// 	},
	// 	Output: func(a map[string]string) (io.WriteCloser, error) {
	// 		file := filepath.Join(dir, opts.Path)
	// 		return os.Create(file)
	// 	},
	// }, pctx.Platform.Get())

	// if err != nil {
	// 	return nil, err
	// }

	// Save the image id
	// imageID, ok := resp.ExporterResponse[exptypes.ExporterImageConfigDigestKey]
	// if !ok {
	// 	return nil, fmt.Errorf("image export for %q did not return an image id", tag.String())
	// }

	// FIXME: Remove the `Copy` and use `Local` directly.
	//
	// Copy'ing is a costly operation which should be unnecessary.
	// However, using llb.Local directly breaks caching sometimes for unknown reasons.
	// outputState := llb.Scratch().File(
	// 	llb.Copy(
	// 		llb.Local(
	// 			dir,
	// 			withCustomName(v, "Export %s", opts.Path),
	// 		),
	// 		"/",
	// 		"/",
	// 	),
	// 	withCustomName(v, "Local %s [copy]", opts.Path),
	// )

	// result, err := s.Solve(ctx, outputState, pctx.Platform.Get())
	// if err != nil {
	// 	return nil, err
	// }
	// lg.Debug().Str("tmpdir", tmpdir).Msg("export path")
	newDir := dgr.Host().Directory(tmpdir)

	newFSID, err := newDir.ID(ctx)
	if err != nil {
		return nil, err
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": utils.NewFS(newFSID),
		// "imageID": imageID,
	})
}
