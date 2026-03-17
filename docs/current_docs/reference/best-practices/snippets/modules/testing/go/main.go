package main

import "fmt"

type Greeter struct {
	Greeting string
}

func New(
	// +default="Hello"
	// +optional
	greeting string,
) *Greeter {
	return &Greeter{
		Greeting: greeting,
	}
}

// Greets the provided name.
func (m *Greeter) Hello(name string) string {
	return fmt.Sprintf("%s, %s!", m.Greeting, name)
}
