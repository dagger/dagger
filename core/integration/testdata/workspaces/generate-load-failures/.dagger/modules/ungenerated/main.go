package main

// Ungenerated has no committed generated files, so its runtime cannot be
// built until `dagger generate` produces them.
type Ungenerated struct{}

func (m *Ungenerated) Hello() string {
	return "hello"
}
