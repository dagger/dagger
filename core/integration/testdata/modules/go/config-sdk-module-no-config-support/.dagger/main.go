package main

import "os"

type Foo struct{}

func (m *Foo) GetCoolName() string {
	return os.Getenv("COOL")
}
