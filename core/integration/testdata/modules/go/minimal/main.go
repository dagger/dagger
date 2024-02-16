package main

import (
	"context"
	"strings"
)

type Minimal struct{}

func (m *Minimal) Hello() string {
	return "hello"
}

func (m *Minimal) Echo(msg string) string {
	return m.EchoOpts(msg, "...", 3)
}

func (m *Minimal) EchoPointer(msg *string) *string {
	v := m.Echo(*msg)
	return &v
}

func (m *Minimal) EchoPointerPointer(msg **string) **string {
	v := m.Echo(**msg)
	v2 := &v
	return &v2
}

func (m *Minimal) EchoOptional(
	// +optional
	msg string,
) string {
	if msg == "" {
		return m.Echo("default")
	}
	return m.Echo(msg)
}

func (m *Minimal) EchoOptionalPointer(
	// +optional
	msg *string,
) string {
	if msg == nil {
		return m.Echo("default")
	}
	return m.Echo(*msg)
}

func (m *Minimal) EchoOptionalSlice(
	// +optional
	msg []string,
) string {
	if msg == nil {
		msg = []string{"foobar"}
	}
	return m.Echo(strings.Join(msg, "+"))
}

func (m *Minimal) Echoes(msgs []string) []string {
	return []string{m.Echo(strings.Join(msgs, " "))}
}

func (m *Minimal) EchoesVariadic(msgs ...string) string {
	return m.Echo(strings.Join(msgs, " "))
}

func (m *Minimal) HelloContext(ctx context.Context) string {
	return "hello context"
}

func (m *Minimal) EchoContext(ctx context.Context, msg string) string {
	return m.Echo("ctx." + msg)
}

func (m *Minimal) HelloStringError(ctx context.Context) (string, error) {
	return "hello i worked", nil
}

func (m *Minimal) HelloVoid() {}

func (m *Minimal) HelloVoidError() error {
	return nil
}

// EchoOpts does some opts things
func (m *Minimal) EchoOpts(
	msg string, // the message to echo

	// String to append to the echoed message
	// +optional
	suffix string,
	// Number of times to repeat the message
	// +optional
	times int,
) string {
	msg += suffix
	if times == 0 {
		times = 1
	}
	return strings.Repeat(msg, times)
}

// EchoOptsInline does some opts things
func (m *Minimal) EchoOptsInline(opts struct {
	Msg string // the message to echo

	// String to append to the echoed message
	// +optional
	Suffix string
	// Number of times to repeat the message
	// +optional
	Times int
}) string {
	return m.EchoOpts(opts.Msg, opts.Suffix, opts.Times)
}

func (m *Minimal) EchoOptsInlinePointer(opts *struct {
	Msg string

	// String to append to the echoed message
	// +optional
	Suffix string
	// Number of times to repeat the message
	// +optional
	Times int
}) string {
	return m.EchoOptsInline(*opts)
}

func (m *Minimal) EchoOptsInlineCtx(ctx context.Context, opts struct {
	Msg string

	// String to append to the echoed message
	// +optional
	Suffix string
	// Number of times to repeat the message
	// +optional
	Times int
}) string {
	return m.EchoOpts(opts.Msg, opts.Suffix, opts.Times)
}

func (m *Minimal) EchoOptsInlineTags(ctx context.Context, opts struct {
	Msg string

	// String to append to the echoed message
	// +optional
	Suffix string `tag:"hello"`
	// Number of times to repeat the message
	// +optional
	Times int `tag:"hello again"`
}) string {
	return m.EchoOpts(opts.Msg, opts.Suffix, opts.Times)
}

func (m *Minimal) EchoOptsPragmas(
	msg string,

	// String to append to the echoed message
	// +optional=true
	// +default="..."
	suffix string,
	// Number of times to repeat the message
	// +optional
	times int, // +default=3
) string {
	return strings.Repeat(msg+suffix, times)
}
