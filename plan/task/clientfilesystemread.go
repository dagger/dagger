package task

import (
	"context"
	"errors"
	"fmt"
	"os"

	"cuelang.org/go/cue"
	"github.com/moby/buildkit/client/llb"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientFilesystemRead", func() Task { return &clientFilesystemReadTask{} })
}

type clientFilesystemReadTask struct{}

func (t clientFilesystemReadTask) PreRun(_ context.Context, pctx *plancontext.Context, v *compiler.Value) error {
	pv := v.Lookup("path")
	path, err := clientFSPath(pv)
	if err != nil {
		return err
	}

	if plancontext.IsFSValue(v.Lookup("contents")) {
		// attempt to detect if it's a dynamic path to give a more useful error message
		if pv.IsReference() {
			return fmt.Errorf("reading directories without a static path is not supported")
		}
		// only validate directories on load, to allow dynamic paths when possible.
		if err := t.validatePath(path, true); err != nil {
			return err
		}
		pctx.LocalDirs.Add(path)
	}

	return nil
}

func (t clientFilesystemReadTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	path, err := clientFSPath(v.Lookup("path"))
	if err != nil {
		return nil, err
	}

	contents, err := t.readContents(ctx, pctx, s, v, path)
	if err != nil {
		return nil, err
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"contents": contents,
	})
}

func (t clientFilesystemReadTask) validatePath(path string, isFS bool) error {
	switch pi, err := os.Stat(path); {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("path %q does not exist", path)
	case !pi.IsDir() && isFS:
		return fmt.Errorf("path %q is not a directory", path)
	case pi.IsDir() && !isFS:
		return fmt.Errorf("path %q cannot be a directory", path)
	case err != nil:
		return fmt.Errorf("cannot get info on path %q: %w", path, err)
	}
	return nil
}

func (t clientFilesystemReadTask) readContents(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value, path string) (interface{}, error) {
	lg := log.Ctx(ctx)

	contents := v.Lookup("contents")
	isFS := plancontext.IsFSValue(contents)

	if err := t.validatePath(path, isFS); err != nil {
		return nil, err
	}

	fileLock, err := clientFSLock(ctx, pctx, path)
	if err != nil {
		return nil, err
	}

	defer fileLock.Unlock()

	if isFS {
		lg.Debug().Str("path", path).Msg("loading local directory")
		return t.readFS(ctx, pctx, s, v, path)
	}

	if plancontext.IsSecretValue(contents) {
		lg.Debug().Str("path", path).Msg("loading local secret file")
		return t.readSecret(pctx, path)
	}

	if contents.IsConcrete() {
		return nil, fmt.Errorf("unexpected concrete value, please use a type")
	}

	k := contents.IncompleteKind()
	if k == cue.StringKind {
		lg.Debug().Str("path", path).Msg("loading local file")
		return t.readString(path)
	}

	return nil, fmt.Errorf("unsupported type %q", k)
}

func (t clientFilesystemReadTask) readFS(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value, path string) (*compiler.Value, error) {
	var dir struct {
		Include []string
		Exclude []string
	}

	if err := v.Decode(&dir); err != nil {
		return nil, err
	}

	opts := []llb.LocalOption{
		withCustomName(v, "Local %s", path),
		// Without hint, multiple `llb.Local` operations on the
		// same path get a different digest.
		llb.SessionID(s.SessionID()),
		llb.SharedKeyHint(path),
	}

	if len(dir.Include) > 0 {
		opts = append(opts, llb.IncludePatterns(dir.Include))
	}

	if len(dir.Exclude) > 0 {
		opts = append(opts, llb.ExcludePatterns(dir.Exclude))
	}

	// FIXME: Remove the `Copy` and use `Local` directly.
	//
	// Copy'ing is a costly operation which should be unnecessary.
	// However, using llb.Local directly breaks caching sometimes for unknown reasons.
	st := llb.Scratch().File(
		llb.Copy(
			llb.Local(
				path,
				opts...,
			),
			"/",
			"/",
		),
		withCustomName(v, "Local %s [copy]", path),
	)

	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return fs.MarshalCUE(), nil
}

func (t clientFilesystemReadTask) readSecret(pctx *plancontext.Context, path string) (*compiler.Value, error) {
	contents, err := t.readString(path)
	if err != nil {
		return nil, err
	}
	secret := pctx.Secrets.New(contents)
	return secret.MarshalCUE(), nil
}

func (t clientFilesystemReadTask) readString(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(contents), nil
}
