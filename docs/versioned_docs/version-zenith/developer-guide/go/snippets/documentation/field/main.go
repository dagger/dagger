package main

type Person struct {
	// The name of the person.
	Name string
	// The age of the person.
	Age int
}

func New(name string, age int) *Person {
	return &Person{
		Name: name,
		Age:  age,
	}
}
