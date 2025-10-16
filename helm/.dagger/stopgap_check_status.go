package main

// FIXME: stopgap until core API defines CheckStatus
type CheckStatus string

const (
	CheckCompleted CheckStatus = "COMPLETED"
	CheckSkipped   CheckStatus = "SKIPPED"
)
