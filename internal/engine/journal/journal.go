package journal

import (
	"time"

	bkclient "github.com/moby/buildkit/client"
)

type Writer interface {
	WriteEntry(*Entry) error
	Close() error
}

type Reader interface {
	ReadEntry() (*Entry, bool)
}

type Entry struct {
	Event *bkclient.SolveStatus `json:"event"`
	TS    time.Time             `json:"ts"`
}
