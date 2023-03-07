package core

import "github.com/pkg/errors"

var ErrHostRWDisabled = errors.New("host read/write is disabled")

var ErrContainerNoExec = errors.New("no command has been executed")
