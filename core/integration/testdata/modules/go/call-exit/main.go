package main

import "os"

type Test struct{}

func (m *Test) Quit() {
	os.Exit(6)
}
