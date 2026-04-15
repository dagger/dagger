package main

// Animal is a thing that makes sound.
type Animal interface {
	// Sound produces a noise.
	Sound() string
}
