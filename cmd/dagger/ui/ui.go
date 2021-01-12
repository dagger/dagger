package ui

import (
	"fmt"
	"hash/adler32"
	"io"
	"os"
	"unicode/utf8"

	"github.com/mitchellh/colorstring"
)

var (
	// Colorize is the main colorizer
	Colorize = colorstring.Colorize{
		Colors: colorstring.DefaultColors,
		Reset:  true,
	}
)

// Finfo prints an info message to w.
func Finfo(w io.Writer, msg string, args ...interface{}) {
	fmt.Fprintf(w, Colorize.Color("[bold][blue]info[reset] %s\n"), fmt.Sprintf(msg, args...))
}

// Info prints an info message.
func Info(msg string, args ...interface{}) {
	Finfo(os.Stdout, msg, args...)
}

// Fverbose prints a verbose message to w.
func Fverbose(w io.Writer, msg string, args ...interface{}) {
	fmt.Fprintf(w, Colorize.Color("[dim]%s\n"), fmt.Sprintf(msg, args...))
}

// Verbose prints a verbose message.
func Verbose(msg string, args ...interface{}) {
	Fverbose(os.Stdout, msg, args...)
}

// Fsuccess prints a success message to w.
func Fsuccess(w io.Writer, msg string, args ...interface{}) {
	fmt.Fprintf(w, Colorize.Color("[bold][green]success[reset] %s\n"), fmt.Sprintf(msg, args...))
}

// Success prints a success message.
func Success(msg string, args ...interface{}) {
	Fsuccess(os.Stdout, msg, args...)
}

// Fwrror prints an error message to w.
func Ferror(w io.Writer, msg string, args ...interface{}) {
	fmt.Fprintf(w, Colorize.Color("[bold][red]error[reset] %s\n"), fmt.Sprintf(msg, args...))
}

// Error prints an error message.
func Error(msg string, args ...interface{}) {
	Ferror(os.Stdout, msg, args...)
}

// Fwarning prints a warning message to w.
func Fwarning(w io.Writer, msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, Colorize.Color("[bold][yellow]warning[reset] %s\n"), fmt.Sprintf(msg, args...))
}

// Warning prints a warning message.
func Warning(msg string, args ...interface{}) {
	Fwarning(os.Stdout, msg, args...)
}

// Ffatal prints an error message to w and exits.
func Ffatal(w io.Writer, msg string, args ...interface{}) {
	fmt.Fprintf(w, Colorize.Color("[bold][red]fatal[reset] %s\n"), fmt.Sprintf(msg, args...))
	os.Exit(1)
}

// Fatal prints an error message and exits.
func Fatal(msg string, args ...interface{}) {
	Ffatal(os.Stderr, msg, args...)
}

// FfatalErr prints an error object to w and exits.
func FfatalErr(w io.Writer, err error) {
	Ffatal(w, "%v", err)
}

// FatalErr prints an error object and exits.
func FatalErr(err error) {
	FfatalErr(os.Stderr, err)
}

// Small returns a `small` colored string.
func Small(msg string) string {
	return Colorize.Color("[dim]" + msg)
}

// Primary returns a `primary` colored string.
func Primary(msg string) string {
	return Colorize.Color("[light_green]" + msg)
}

// Highlight returns a `highlighted` colored string.
func Highlight(msg string) string {
	return Colorize.Color("[cyan]" + msg)
}

// Truncate truncates a string to the given length
func Truncate(msg string, length int) string {
	for utf8.RuneCountInString(msg) > length {
		msg = msg[0:len(msg)-4] + "…"
	}
	return msg
}

// HashColor returns a consistent color for a given string
func HashColor(text string) string {
	colors := []string{
		"green",
		"light_green",
		"light_blue",
		"blue",
		"magenta",
		"light_magenta",
		"light_yellow",
		"cyan",
		"light_cyan",
		"red",
		"light_red",
	}
	h := adler32.Checksum([]byte(text))
	return colors[int(h)%len(colors)]
}

// PrintLegend prints a demo of the ui functions
func PrintLegend() {
	Info("info message")
	Success("success message")
	Error("error message")
	Warning("warning message")
	Verbose("verbose message")
	fmt.Printf("this is %s\n", Small("small"))
	fmt.Printf("this is %s\n", Primary("primary"))
	fmt.Printf("this is a %s\n", Highlight("highlight"))
}
