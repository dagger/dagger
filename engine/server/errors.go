package server

import (
	"errors"
)

var (
	// TODO: where is this used?
	ErrHostRWDisabled = errors.New("host read/write is disabled")
)
