package router

type InvalidInputError struct {
	Err error
}

func (e InvalidInputError) Error() string {
	return e.Err.Error()
}

func (e InvalidInputError) Unwrap() error {
	return e.Err
}
