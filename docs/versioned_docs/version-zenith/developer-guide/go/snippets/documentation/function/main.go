package main

type MyModule struct{}

// Compute the sum of two numbers.
//
// Example usage: dagger call add --a=4 --b=5
func (*MyModule) Add(
	// The first number to add
	// +default=4
	a int,
	// The second number to add
	b int,
) int {
	return a + b
}
