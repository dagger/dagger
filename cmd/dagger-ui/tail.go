package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dagger/dagger/internal/engine"
	bkclient "github.com/moby/buildkit/client"
	"github.com/nxadm/tail"
)

type JournalEntry struct {
	Event *bkclient.SolveStatus
	TS    time.Time
}

func tailJournal(journal string, follow bool, stopCh chan struct{}) (engine.JournalReader, error) {
	f, err := tail.TailFile(journal, tail.Config{Follow: follow})
	if err != nil {
		return nil, err
	}

	r, w := engine.Pipe()

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
		defer w.Close()
		defer f.Cleanup()

		for line := range f.Lines {
			if err := line.Err; err != nil {
				fmt.Fprintf(os.Stderr, "tail err: %v\n", err)
				return
			}

			var entry engine.JournalEntry
			if err := json.Unmarshal([]byte(line.Text), &entry); err != nil {
				fmt.Fprintf(os.Stderr, "tail unmarshal error (%s): %v\n", line.Text, err)
				var syntaxErr *json.SyntaxError
				if errors.As(err, &syntaxErr) {
					continue
				}
				return
			}

			w.WriteStatus(&entry)
		}
	}()

	return r, nil
}
