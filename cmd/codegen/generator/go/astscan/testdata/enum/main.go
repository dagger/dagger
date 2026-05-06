package main

// Status represents a state.
type Status string

const (
	// StatusPending is the initial state.
	StatusPending Status = "PENDING"
	// StatusActive is the running state.
	StatusActive Status = "ACTIVE"
)
