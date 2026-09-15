package main

// A local module
type Other struct{}

func (Other) Version() string {
	return "other function"
}
