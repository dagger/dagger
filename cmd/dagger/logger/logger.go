// Logger utilities for the CLI
//
// These utilities rely on command line flags to set up the logger, therefore
// they are tightly coupled with the CLI and should not be used outside.

package logger

import (
	"fmt"
	"io"
	"os"

	"github.com/mattn/go-colorable"
	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/api/auth"
	"golang.org/x/term"
)

func New() zerolog.Logger {
	logger := zerolog.
		New(os.Stderr).
		With().
		Timestamp().
		Logger()

	if !jsonLogs() {
		logger = logger.Output(&PlainOutput{Out: colorable.NewColorableStderr()})
	} else {
		logger = logger.With().Caller().Logger()
	}

	level := viper.GetString("log-level")
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		panic(err)
	}
	return logger.Level(lvl)
}

func NewWithCloud() zerolog.Logger {
	logger := zerolog.
		New(TeeCloud(os.Stderr)).
		With().
		Timestamp().
		Caller().
		Logger()

	if !jsonLogs() {
		logger = logger.Output(
			TeeCloud(&PlainOutput{Out: colorable.NewColorableStderr()}),
		)
	}

	level := viper.GetString("log-level")
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		panic(err)
	}
	// TODO: send all events to the cloud, regardless of the log level
	return logger.Level(lvl)
}

func TeeCloud(w io.Writer) zerolog.LevelWriter {
	// TODO: maybe wrap the logger - should solve the level issue too
	if !auth.HasCredentials() {
		return zerolog.MultiLevelWriter(w)
	}

	return zerolog.MultiLevelWriter(
		w,
		NewCloud())
}

func jsonLogs() bool {
	switch f := viper.GetString("log-format"); f {
	case "json":
		return true
	case "plain":
		return false
	case "tty":
		return false
	case "auto":
		return !term.IsTerminal(int(os.Stdout.Fd()))
	default:
		fmt.Fprintf(os.Stderr, "invalid --log-format %q\n", f)
		os.Exit(1)
	}
	return false
}
