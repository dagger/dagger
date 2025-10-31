package main

// FIXME: stopgap until core API defines MyCheckStatus
type MyCheckStatus string

const (
	CheckCompleted MyCheckStatus = "COMPLETED"
	CheckSkipped   MyCheckStatus = "SKIPPED"
)
