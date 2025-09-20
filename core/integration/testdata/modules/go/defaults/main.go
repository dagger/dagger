package main

import "fmt"

func New(
	// +default="hello"
	greeting string,
) *Defaults {
	return &Defaults{
		Greeting: greeting,
	}
}

type Defaults struct {
	Greeting string
}

func (m *Defaults) Message(
	// +default="world"
	name string,
) string {
	return fmt.Sprintf("%s, %s!", m.Greeting, name)
}
