package main

import (
	"fmt"
	"strings"
)

type Language string

const (
	Go         Language = "GO"
	Python     Language = "PYTHON"
	Typescript Language = "TYPESCRIPT"
	PHP        Language = "PHP"
	Elixir     Language = "ELIXIR"
)

type Test struct{}

func (m *Test) Faves(
	// +default=["GO", "PYTHON"]
	langs []Language,
) string {
	return strings.Trim(fmt.Sprint(langs), "[]")
}

func (m *Test) Official() []Language {
	return []Language{Go, Python, Typescript}
}
