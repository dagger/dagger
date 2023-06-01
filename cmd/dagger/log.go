package main

import (
	"io"
	"os"

	"github.com/rs/zerolog"
)

func Logger(dest io.Writer) zerolog.Logger {
	logger := zerolog.
		New(os.Stderr).
		With().
		Timestamp().
		Logger().
		Output(zerolog.ConsoleWriter{Out: dest})

	if debug {
		logger = logger.Level(zerolog.DebugLevel)
	}

	return logger
}
