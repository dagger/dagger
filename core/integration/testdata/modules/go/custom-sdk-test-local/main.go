package main

import "os"

type Test struct{}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
