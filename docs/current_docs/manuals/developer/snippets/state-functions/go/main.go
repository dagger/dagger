package main

import "fmt"

type MyModule struct {
	// The greeting to use
	// +default="Hello"
	Greeting string
	// Who to greet
	// +private
	// +default="World"
	Name string
}

func New(greeting string, name string) *MyModule {
	return &MyModule{
		// The greeting to use
		// +default="Hello"
		Greeting: greeting,
		// Who to greet
		// +default="World"
		Name: name,
	}
}

// Return the greeting message
func (m *MyModule) Message() string {
	str := fmt.Sprintf("%s, %s!", m.Greeting, m.Name)
	return str
}
