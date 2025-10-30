// A generated module for HelloWithObjects functions

package main

type HelloWithObjects struct{}

// Returns a container that echoes whatever string argument is provided
func (m *HelloWithObjects) SayGreeting() *Greetings {
	return &Greetings{}
}

type Greetings struct{}

func (g *Greetings) Hello() string {
	return "Hello!"
}

func (g *Greetings) Bonjour() string {
	return "Bonjour!"
}
