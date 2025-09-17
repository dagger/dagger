package main

// The struct represents a single user of the system.
type MyModule struct {
	Name string
	Age  int
}

func New(
	// The name of the user.
	name string,
	// The age of the user.
	age int,
) *MyModule {
	return &MyModule{
		Name: name,
		Age:  age,
	}
}
