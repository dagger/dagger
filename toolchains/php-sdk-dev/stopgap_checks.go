package main

// This file contains temporary code, to be removed once 'dagger checks' is merged and released.
type MyCheckStatus string

const (
	CheckCompleted MyCheckStatus = "COMPLETED"
	CheckSkipped   MyCheckStatus = "SKIPPED"
)
