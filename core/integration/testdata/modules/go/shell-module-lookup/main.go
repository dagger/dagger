// Main module
//
// Multiline module description.
package main

import "dagger/test/internal/dagger"

// Constructor description.
func New(
	// +defaultPath=.
	source *dagger.Directory,
) *Test {
	return &Test{Source: source}
}

// Test main object
//
// Multiline object description.
type Test struct {
	Source *dagger.Directory
}

// Test version
func (Test) Version() string {
	return "test function"
}

// Encouragement
func (Test) Go() string {
	return "Let's go!"
}
