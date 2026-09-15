package main

import "os"

type Foo struct{}

func (m *Foo) CheckEnv() string {
	return os.Getenv("GOPRIVATE")
}
