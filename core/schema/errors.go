package schema

import "errors"

var (
	ErrMergeTypeConflict   = errors.New("object type re-defined")
	ErrMergeFieldConflict  = errors.New("field re-defined")
	ErrMergeScalarConflict = errors.New("scalar re-defined")
)

type InvalidInputError struct {
	Err error
}

func (e InvalidInputError) Error() string {
	return e.Err.Error()
}

func (e InvalidInputError) Unwrap() error {
	return e.Err
}
