package main

// Vito says hi.
type Vito struct{}

// HelloWorld says hi.
func (m *Vito) HelloWorld() string {
	return "hey"
}
