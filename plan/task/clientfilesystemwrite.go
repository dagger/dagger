package task

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"cuelang.org/go/cue"
	"dagger.io/dagger"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientFilesystemWrite", func() Task { return &clientFilesystemWriteTask{} })
}

type clientFilesystemWriteTask struct {
}

func (t clientFilesystemWriteTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	path, err := clientFSPath(v.Lookup("path"))
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

	// FIXME: we should ideally fail when multiple writes to the same **file** are
	// detected, to protect the user from the random behavior. Not for directories
	// though, since it has a valid use case.

	fileLock, err := clientFSLock(ctx, pctx, path)
	if err != nil {
		return err
	}

	defer fileLock.Unlock()

	if utils.IsFSValue(contents) {
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

	if utils.IsSecretValue(contents) {
		lg.Debug().Str("path", path).Msg("writing secret to local file")
		secretid, err := utils.GetSecretId(contents)
		if err != nil {
			return err
		}
		plaintext, err := s.Client.Secret(secretid).Plaintext(ctx)
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(plaintext), permissions)
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

	fsid, err := utils.GetFSId(v)

	dgr := s.Client

	_, err = dgr.Directory(dagger.DirectoryOpts{ID: fsid}).Export(ctx, path)

	return err
}
