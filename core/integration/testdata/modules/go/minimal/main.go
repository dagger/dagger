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
	return m.EchoOpts(msg, EchoOpts{
		// TODO(vito): gotcha! Because we're calling the method directly, defaults
		// are not applied. Maybe the default:"..." struct tag should be removed in
		// favor of handling it at runtime? Maybe it should be kept anyway as a
		// convenience, and this won't be a common footgun?
		Suffix: "...",
		Times:  3,
	})
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

type EchoOpts struct {
	Suffix string `doc:"String to append to the echoed message." default:"..."`
	Times  int    `doc:"Number of times to repeat the message." default:"3"`
}

func (m *Minimal) EchoOpts(msg string, opts EchoOpts) string {
	return m.EchoOptsInline(msg, opts)
}

func (m *Minimal) EchoOptsInline(msg string, opts struct {
	Suffix string `doc:"String to append to the echoed message." default:"..."`
	Times  int    `doc:"Number of times to repeat the message." default:"3"`
}) string {
	msg += opts.Suffix
	return strings.Repeat(msg, opts.Times)
}
