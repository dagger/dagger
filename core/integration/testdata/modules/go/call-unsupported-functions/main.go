package main

type Test struct{}

// Sanity check
func (m *Test) Echo(msg string) string {
	return msg
}

// Skips adding the function
func (m *Test) FnA(msg string, matrix [][]string) string {
	return msg
}

// Skips adding the optional flag
func (m *Test) FnB(
	msg string,
	// +optional
	matrix [][]string,
) *Chain {
	return new(Chain)
}

type Chain struct{}

// Repeat message back
func (m *Chain) Echo(msg string) string {
	return msg
}
