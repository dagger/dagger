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
	spec := v.cc.Spec().Get("#Script")
	final, err := spec.Merge(v)
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
	// Assume script is valid.
	// Spec validation is already done at component creation.
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
				Msg("script is unspecified, aborting execution")
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

func (s *Script) LocalDirs(ctx context.Context) ([]string, error) {
	var dirs []string
	err := s.Walk(ctx, func(op *Op) error {
		if err := op.Validate("#Local"); err != nil {
			// Ignore all operations except 'do:"local"'
			return nil
		}
		dir, err := op.Get("dir").String()
		if err != nil {
			return errors.Wrap(err, "invalid 'local' operation")
		}
		dirs = append(dirs, dir)
		return nil
	})
	return dirs, err
}
