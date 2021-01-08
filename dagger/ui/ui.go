package ui

import (
	"fmt"
	"os"
	"strings"
)

func Fatalf(msg string, args ...interface{}) {
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprintf(os.Stderr, msg, args...)
	os.Exit(1)
}

func Fatal(msg interface{}) {
	Fatalf("%s\n", msg)
}

func Info(msg string, args ...interface{}) {
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprintf(os.Stderr, "[info] "+msg, args...)
}
