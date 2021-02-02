package dagger

import (
	"context"

	"cuelang.org/go/cue"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

var (
	ErrAbortExecution = errors.New("execution stopped")
)

type Script struct {
	v *Value
}

func NewScript(v *Value) (*Script, error) {
	// Validate & merge with spec
	final, err := v.Finalize(v.cc.Spec().Get("#Script"))
	if err != nil {
		return nil, errors.Wrap(err, "invalid script")
	}
	return newScript(final)
}

// Same as newScript, but without spec merge + validation.
func newScript(v *Value) (*Script, error) {
	if !v.Exists() {
		return nil, ErrNotExist
	}
	return &Script{
		v: v,
	}, nil
}

func (s *Script) Value() *Value {
	return s.v
}

// Return the operation at index idx
func (s *Script) Op(idx int) (*Op, error) {
	v := s.v.LookupPath(cue.MakePath(cue.Index(idx)))
	if !v.Exists() {
		return nil, ErrNotExist
	}
	return newOp(v)
}

// Return the number of operations in the script
func (s *Script) Len() uint64 {
	l, _ := s.v.Len().Uint64()
	return l
}

// Run a dagger script
func (s *Script) Execute(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	err := s.v.RangeList(func(idx int, v *Value) error {
		// If op not concrete, interrupt without error.
		// This allows gradual resolution:
		//    compute what you can compute.. leave the rest incomplete.
		if err := v.IsConcreteR(); err != nil {
			log.
				Ctx(ctx).
				Warn().
				Err(err).
				Int("op", idx).
				// FIXME: tell user which inputs are missing (by inspecting references)
				Msg("script is missing inputs and has not been fully executed")
			return ErrAbortExecution
		}
		op, err := newOp(v)
		if err != nil {
			return errors.Wrapf(err, "validate op %d/%d", idx+1, s.v.Len())
		}
		fs, err = op.Execute(ctx, fs, out)
		if err != nil {
			return errors.Wrapf(err, "execute op %d/%d", idx+1, s.v.Len())
		}
		return nil
	})

	// If the execution was gracefully stopped, do not return an error
	if err == ErrAbortExecution {
		return fs, nil
	}
	return fs, err
}

func (s *Script) Walk(ctx context.Context, fn func(op *Op) error) error {
	return s.v.RangeList(func(idx int, v *Value) error {
		op, err := newOp(v)
		if err != nil {
			return errors.Wrapf(err, "validate op %d/%d", idx+1, s.v.Len())
		}
		if err := op.Walk(ctx, fn); err != nil {
			return err
		}
		return nil
	})
}

func (s *Script) LocalDirs(ctx context.Context) (map[string]string, error) {
	lg := log.Ctx(ctx)
	lg.Debug().
		Str("func", "Script.LocalDirs").
		Str("location", s.Value().Path().String()).
		Msg("starting")
	dirs := map[string]string{}
	err := s.Walk(ctx, func(op *Op) error {
		if err := op.Validate("#Local"); err != nil {
			// Ignore all operations except 'do:"local"'
			return nil
		}
		dir, err := op.Get("dir").String()
		if err != nil {
			return errors.Wrap(err, "invalid 'local' operation")
		}
		dirs[dir] = dir
		return nil
	})
	lg.Debug().
		Str("func", "Script.LocalDirs").
		Str("location", s.Value().Path().String()).
		Interface("err", err).
		Interface("result", dirs).
		Msg("done")
	return dirs, err
}
