package main

func New() *Objdoc {
	return &Objdoc{}
}

// Object-only description
type Objdoc struct{}

func (Objdoc) Hello() string {
	return "hello"
}
