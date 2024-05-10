package main

// The struct represents a single user of the system.
type MyModule struct {
	// The name of the user.
	Name string
	// The age of the user.
	Age int
}

func New(name string, age int) *MyModule {
	return &MyModule{
		Name: name,
		Age:  age,
	}
}
