package main

import "context"

type Minimal struct{}

func (m *Minimal) Hello() string {
	return "hello"
}

func (m *Minimal) HelloContext(ctx context.Context) string {
	return "hello context"
}

func (m *Minimal) HelloStringError(ctx context.Context) (string, error) {
	return "hello i worked", nil
}

func (m *Minimal) HelloVoid() {}

func (m *Minimal) HelloVoidError() error {
	return nil
}
