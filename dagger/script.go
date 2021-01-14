package dagger

import (
	"context"

	"github.com/pkg/errors"
)

type Script struct {
	v *Value
}

func (s Script) Validate() error {
	// FIXME this crashes when a script is incomplete or empty
	return s.Value().Validate("#Script")
}

func (s *Script) Value() *Value {
	return s.v
}

// Run a dagger script
func (s *Script) Execute(ctx context.Context, fs FS, out Fillable) (FS, error) {
	err := s.v.RangeList(func(idx int, v *Value) error {
		// If op not concrete, interrupt without error.
		// This allows gradual resolution:
		//    compute what you can compute.. leave the rest incomplete.
		if !v.IsConcreteR() {
			return nil
		}
		op, err := v.Op()
		if err != nil {
			return errors.Wrapf(err, "validate op %d/%d", idx+1, s.v.Len())
		}
		fs, err = op.Execute(ctx, fs, out)
		if err != nil {
			return errors.Wrapf(err, "execute op %d/%d", idx+1, s.v.Len())
		}
		return nil
	})
	return fs, err
}

func (s *Script) Walk(ctx context.Context, fn func(op *Op) error) error {
	return s.v.RangeList(func(idx int, v *Value) error {
		op, err := v.Op()
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
			return nil
		}
		dir, err := op.Get("dir").String()
		if err != nil {
			return err
		}
		dirs = append(dirs, dir)
		return nil
	})
	return dirs, err
}
