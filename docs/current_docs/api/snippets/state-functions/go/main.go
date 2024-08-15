package main

import "fmt"

type MyModule struct {
	// The greeting to use
<<<<<<< HEAD
	Greeting string
	// Who to greet
	// +private
=======
	// +default="Hello"
	Greeting string
	// Who to greet
	// +private
	// +default="World"
>>>>>>> 555fdff0e (Rebased)
	Name string
}

func New(
	// The greeting to use
	// +default="Hello"
	greeting string,
	// Who to greet
	// +default="World"
	name string,
) *MyModule {
	return &MyModule{
		Greeting: greeting,
		Name:     name,
	}
}

// Return the greeting message
func (m *MyModule) Message() string {
	str := fmt.Sprintf("%s, %s!", m.Greeting, m.Name)
	return str
}
