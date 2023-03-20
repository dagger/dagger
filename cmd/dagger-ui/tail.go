package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	bkclient "github.com/moby/buildkit/client"
	"github.com/nxadm/tail"
)

type JournalEntry struct {
	Event *bkclient.SolveStatus
	TS    time.Time
}

func tailJournal(journal string, follow bool, stopCh chan struct{}) (chan *bkclient.SolveStatus, error) {
	f, err := tail.TailFile(journal, tail.Config{Follow: follow})
	if err != nil {
		return nil, err
	}

	ch := make(chan *bkclient.SolveStatus)

	go func() {
		if stopCh == nil {
			return
		}
		<-stopCh
		fmt.Fprintf(os.Stderr, "quitting\n")
		if err := f.StopAtEOF(); err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
		}
	}()

	go func() {
		defer close(ch)
		defer f.Cleanup()

		for line := range f.Lines {
			if err := line.Err; err != nil {
				fmt.Fprintf(os.Stderr, "tail err: %v\n", err)
				return
			}

			var entry JournalEntry
			if err := json.Unmarshal([]byte(line.Text), &entry); err != nil {
				fmt.Fprintf(os.Stderr, "tail unmarshal error (%s): %v\n", line.Text, err)
				var syntaxErr *json.SyntaxError
				if errors.As(err, &syntaxErr) {
					continue
				}
				return
			}

			ch <- entry.Event
		}
	}()

	return ch, nil
}
