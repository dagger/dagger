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
	return m.EchoOpts(msg, Opt("..."), Opt(3))
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

func (m *Minimal) EchoOptional(msg Optional[string]) string {
	v, ok := msg.Get()
	if !ok {
		v = "default"
	}
	return m.Echo(v)
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
	msg string,

	// String to append to the echoed message
	suffix Optional[string],
	// Number of times to repeat the message
	times Optional[int],
) string {
	msg += suffix.GetOr("")
	return strings.Repeat(msg, times.GetOr(1))
}

// EchoOptsInline does some opts things
func (m *Minimal) EchoOptsInline(opts struct {
	Msg string

	// String to append to the echoed message
	Suffix Optional[string]
	// Number of times to repeat the message
	Times Optional[int]
}) string {
	return m.EchoOpts(opts.Msg, opts.Suffix, opts.Times)
}

func (m *Minimal) EchoOptsInlinePointer(opts *struct {
	Msg string

	// String to append to the echoed message
	Suffix Optional[string]
	// Number of times to repeat the message
	Times Optional[int]
}) string {
	return m.EchoOptsInline(*opts)
}

func (m *Minimal) EchoOptsInlineCtx(ctx context.Context, opts struct {
	Msg string

	// String to append to the echoed message
	Suffix Optional[string]
	// Number of times to repeat the message
	Times Optional[int]
}) string {
	return m.EchoOpts(opts.Msg, opts.Suffix, opts.Times)
}

func (m *Minimal) EchoOptsInlineTags(ctx context.Context, opts struct {
	Msg string

	// String to append to the echoed message
	Suffix Optional[string] `tag:"hello"`
	// Number of times to repeat the message
	Times Optional[int] `tag:"hello again"`
}) string {
	return m.EchoOpts(opts.Msg, opts.Suffix, opts.Times)
}
