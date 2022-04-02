package task

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"cuelang.org/go/cue"
	bk "github.com/moby/buildkit/client"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientFilesystemWrite", func() Task { return &clientFilesystemWriteTask{} })
}

type clientFilesystemWriteTask struct {
}

func (t clientFilesystemWriteTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	path, err := v.Lookup("path").String()
	if err != nil {
		return nil, err
	}

	path, err = clientFilePath(path)
	if err != nil {
		return nil, err
	}

	if err := t.writeContents(ctx, pctx, s, v, path); err != nil {
		return nil, err
	}

	return compiler.NewValue(), nil
}

func (t clientFilesystemWriteTask) writeContents(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value, path string) error {
	lg := log.Ctx(ctx)
	contents := v.Lookup("contents")

	if plancontext.IsFSValue(contents) {
		lg.Debug().Str("path", path).Msg("writing files to local directory")
		return t.writeFS(ctx, pctx, s, contents, path)
	}

	permissions := fs.FileMode(0644) // default permission
	if vl := v.Lookup("permissions"); vl.Exists() {
		p, err := vl.Int64()
		if err != nil {
			return err
		}
		permissions = fs.FileMode(p)
	}

	if plancontext.IsSecretValue(contents) {
		lg.Debug().Str("path", path).Msg("writing secret to local file")
		secret, err := pctx.Secrets.FromValue(contents)
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(secret.PlainText()), permissions)
	}

	k := contents.Kind()
	if k == cue.StringKind {
		lg.Debug().Str("path", path).Msg("writing to local file")
		text, err := contents.String()
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(text), permissions)
	}

	return fmt.Errorf("unsupported type %q", k)
}

func (t clientFilesystemWriteTask) writeFS(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value, path string) error {
	contents, err := pctx.FS.FromValue(v)
	if err != nil {
		return err
	}

	st, err := contents.State()
	if err != nil {
		return err
	}

	_, err = s.Export(ctx, st, nil, bk.ExportEntry{
		Type:      bk.ExporterLocal,
		OutputDir: path,
	}, pctx.Platform.Get())

	return err
}
