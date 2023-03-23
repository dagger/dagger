package journal

import (
	"time"

	bkclient "github.com/moby/buildkit/client"
)

type Writer interface {
	WriteStatus(*Entry) error
	Close() error
}

type Reader interface {
	ReadStatus() (*Entry, bool)
}

type Entry struct {
	Event *bkclient.SolveStatus
	TS    time.Time
}
