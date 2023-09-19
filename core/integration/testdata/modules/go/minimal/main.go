package main

import (
	"context"
	"fmt"
)

type Minimal struct{}

func (m *Minimal) Hello() string {
	return "hello"
}

func (m *Minimal) Echo(msg string) string {
	return fmt.Sprintf("%s... %s... %s...", msg, msg, msg)
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
