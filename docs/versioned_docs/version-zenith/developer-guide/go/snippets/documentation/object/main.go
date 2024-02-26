package main

// The Person struct represents a single user of the system
type Person struct {
	Name string
	Age  int
}

func New(name string, age int) *Person {
	return &Person{
		Name: name,
		Age:  age,
	}
}
