package main

// A Dagger module to say hello to the world!
type Hello struct{}

// Hello prints out a greeting.
func (m *Hello) Hello() string {
	return "hi"
}
