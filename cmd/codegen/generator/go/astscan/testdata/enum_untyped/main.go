package main

// Status represents a state.
type Status string

const (
	// StatusPending is the initial state.
	StatusPending Status = "PENDING"
	StatusActive         = "ACTIVE"
	StatusDone           = "DONE"
)

const StatusCancelled = Status("CANCELLED")
